package backendobservations

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"uuid"

	"github.com/yashok111/mocker/internal/backendblob"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
	"github.com/yashok111/mocker/internal/store"
)

type Repo struct{ db *store.DB }

func NewRepo(db *store.DB) *Repo { return &Repo{db: db} }

type reader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func receipt(ctx context.Context, q reader, pid, action, key, hash string, out any) (bool, error) {
	var old string
	var raw []byte
	e := q.QueryRowContext(ctx, `SELECT request_hash,document FROM backend_observation_receipts WHERE project_id=? AND action=? AND key=?`, pid, action, key).Scan(&old, &raw)
	if errors.Is(e, sql.ErrNoRows) {
		return false, nil
	}
	if e != nil {
		return false, e
	}
	if old != hash {
		return false, fault(409, "idempotency_conflict")
	}
	return true, json.Unmarshal(raw, out)
}
func saveReceipt(ctx context.Context, tx *sql.Tx, pid, action, key, hash string, out any) error {
	b, e := canonical(out)
	if e != nil {
		return e
	}
	_, e = tx.ExecContext(ctx, `INSERT INTO backend_observation_receipts VALUES(?,?,?,?,?)`, pid, action, key, hash, string(b))
	return e
}
func (r *Repo) Import(ctx context.Context, pid string, in ImportInput) (*VersionReceipt, error) {
	b, e := canonical(in)
	if e != nil || len(b) > BatchBytes {
		return nil, fault(413, "batch_limit")
	}
	var checked ImportInput
	if json.Unmarshal(b, &checked) != nil {
		return nil, invalid()
	}
	if e = validateImport(pid, in); e != nil {
		return nil, e
	}
	hash, _ := p.Hash(DocumentVersion+"/import", in)
	out := new(VersionReceipt)
	e = r.db.Write(ctx, func(tx *sql.Tx) error {
		if ok, e := receipt(ctx, tx, pid, "import", in.IdempotencyKey, hash, out); e != nil || ok {
			return e
		}
		if in.Mode == "create" && in.Context.Source.Status == "known" {
			var n int
			err := tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_repositories WHERE project_id=? AND id=?`, pid, in.Context.Source.RepositoryID).Scan(&n)
			if err != nil {
				return err
			}
			if n != 1 {
				return fault(422, "repository_ownership")
			}
		}
		set, e := openImportSet(ctx, tx, pid, in)
		if e != nil {
			return e
		}
		var used int
		if e = tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_observation_batches WHERE project_id=? AND set_id=? AND batch_id=?`, pid, set.sid, in.BatchID).Scan(&used); e != nil {
			return e
		}
		if used != 0 {
			return fault(409, "batch_conflict")
		}
		added, e := set.admit(ctx, tx, pid, in.Records)
		if e != nil {
			return e
		}
		if set.count > 100000 || set.size > 128<<20 {
			return fault(413, "set_quota")
		}
		if e = set.write(ctx, tx, pid, in.Mode, added, out); e != nil {
			return e
		}
		if e = projectQuota(ctx, tx, pid); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO backend_observation_batches VALUES(?,?,?,?)`, pid, set.sid, in.BatchID, hash); e != nil {
			return e
		}
		return saveReceipt(ctx, tx, pid, "import", in.IdempotencyKey, hash, out)
	})
	return out, e
}

// validateImport: a create carries a valid context and a name and nothing
// of an existing set; an append names the set version it extends.
func validateImport(pid string, in ImportInput) error {
	if !p.ValidID(pid) || !bounded(in.BatchID, 128) || !bounded(in.IdempotencyKey, 128) || len(in.Records) == 0 || len(in.Records) > 500 {
		return invalid()
	}
	if in.Mode != "create" {
		if in.Mode != "append" || in.Context != nil || in.Name != "" || !p.ValidID(in.SetID) || in.ExpectedVersion < 1 {
			return invalid()
		}
		return nil
	}
	if in.Context == nil || !bounded(in.Name, 256) || in.SetID != "" || in.ExpectedVersion != 0 {
		return invalid()
	}
	return ValidateContext(*in.Context)
}

// importSet is the set an import writes: a new one, or the head of an
// existing one, with the size and count it grows by.
type importSet struct {
	sid   string
	v     int64
	c     ObservationContext
	name  string
	size  int64
	count int
}

type importEntry struct{ id, hash, raw string }

func openImportSet(ctx context.Context, tx *sql.Tx, pid string, in ImportInput) (*importSet, error) {
	set := &importSet{sid: in.SetID, v: 1}
	if in.Mode == "create" {
		set.sid = uuid.NewV7().String()
		set.c = *in.Context
		set.name = in.Name
		cb, _ := canonical(set.c)
		set.size = int64(len(cb))
		var sets int
		if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_observation_sets WHERE project_id=?`, pid).Scan(&sets); e != nil {
			return nil, e
		}
		if sets >= 256 {
			return nil, fault(413, "set_limit")
		}
		return set, nil
	}
	var raw []byte
	var head int64
	e := tx.QueryRowContext(ctx, `SELECT version,context,name,logical_bytes,record_count FROM backend_observation_sets WHERE project_id=? AND id=?`, pid, set.sid).Scan(&head, &raw, &set.name, &set.size, &set.count)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, fault(404, "not_found")
	}
	if e != nil {
		return nil, e
	}
	if head != in.ExpectedVersion {
		return nil, fault(409, "version_conflict")
	}
	if head >= 1000 {
		return nil, fault(413, "version_limit")
	}
	set.v = head + 1
	if e = json.Unmarshal(raw, &set.c); e != nil {
		return nil, e
	}
	return set, nil
}

// admit validates each record against the set's context and returns the new
// ones. A record id already present, in the batch or the set, must carry the
// same content.
func (set *importSet) admit(ctx context.Context, tx *sql.Tx, pid string, records []Record) ([]importEntry, error) {
	added := []importEntry{}
	seen := map[string]string{}
	for _, rec := range records {
		if e := ValidateRecord(set.c, rec); e != nil {
			return nil, e
		}
		h, _ := RecordHash(rec)
		if old, ok := seen[rec.ID]; ok {
			if old != h {
				return nil, fault(409, "record_conflict")
			}
			continue
		}
		seen[rec.ID] = h
		var old string
		e := tx.QueryRowContext(ctx, `SELECT hash FROM backend_observation_members WHERE project_id=? AND set_id=? AND record_id=?`, pid, set.sid, rec.ID).Scan(&old)
		if e == nil {
			if old != h {
				return nil, fault(409, "record_conflict")
			}
			continue
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		}
		raw, _ := canonical(rec)
		added = append(added, importEntry{rec.ID, h, string(raw)})
		set.size += int64(len(raw))
		set.count++
	}
	return added, nil
}

// write stores the new records and the set's next version, whose content
// hash covers the context and every member.
func (set *importSet) write(ctx context.Context, tx *sql.Tx, pid, mode string, added []importEntry, out *VersionReceipt) error {
	if mode == "create" {
		cb, _ := canonical(set.c)
		if _, e := tx.ExecContext(ctx, `INSERT INTO backend_observation_sets VALUES(?,?,?,?,?,?,?)`, pid, set.sid, set.v, set.name, string(cb), set.size, set.count); e != nil {
			return e
		}
	}
	for _, a := range added {
		if _, e := backendblob.Exec(ctx, tx, `INSERT OR IGNORE INTO backend_observation_blobs(hash,document) VALUES(?,?)`, a.hash, a.raw); e != nil {
			return e
		}
		if _, e := backendblob.Exec(ctx, tx, `INSERT INTO backend_observation_members(project_id,set_id,record_id,hash,introduced_version) VALUES(?,?,?,?,?)`, pid, set.sid, a.id, a.hash, set.v); e != nil {
			return e
		}
	}
	members, e := setMembers(ctx, tx, pid, set.sid)
	if e != nil {
		return e
	}
	content, e := p.Hash(DocumentVersion, struct {
		Context ObservationContext `json:"context"`
		Members []setMember        `json:"members"`
	}{set.c, members})
	if e != nil {
		return e
	}
	*out = VersionReceipt{set.sid, set.v, content, set.count}
	doc := Version{*out, set.c, set.name, set.size}
	raw, _ := canonical(doc)
	if _, e = backendblob.Exec(ctx, tx, `INSERT INTO backend_observation_versions(project_id,set_id,version,content_hash,document) VALUES(?,?,?,?,?)`, pid, set.sid, set.v, content, string(raw)); e != nil {
		return e
	}
	if mode == "append" {
		if _, e = tx.ExecContext(ctx, `UPDATE backend_observation_sets SET version=?,logical_bytes=?,record_count=? WHERE project_id=? AND id=?`, set.v, set.size, set.count, pid, set.sid); e != nil {
			return e
		}
	}
	return nil
}

type setMember struct {
	ID   string `json:"id"`
	Hash string `json:"hash"`
}

// setMembers reads every member of the set in record order; the rows are
// closed before the transaction's next statement.
func setMembers(ctx context.Context, tx *sql.Tx, pid, sid string) ([]setMember, error) {
	members := []setMember{}
	rows, e := tx.QueryContext(ctx, `SELECT record_id,hash FROM backend_observation_members WHERE project_id=? AND set_id=? ORDER BY record_id`, pid, sid)
	if e != nil {
		return nil, e
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var m setMember
		if e = rows.Scan(&m.ID, &m.Hash); e != nil {
			return nil, e
		}
		members = append(members, m)
	}
	return members, rows.Err()
}
func projectQuota(ctx context.Context, q reader, pid string) error {
	size, e := projectBytes(ctx, q, pid)
	if e != nil {
		return e
	}
	if size > 512<<20 {
		return fault(413, "project_quota")
	}
	return nil
}

// projectBytes is the project's logical observation bytes: set records plus
// every stored correlation and version document.
//
// Review 2026-10-06, F164: the document sums went through the _documents
// views, whose column is a subquery that reads the payload blob and CASTs it
// to text per row, so every Import and Correlate read and copied every stored
// document byte of the project inside the writer. backend_payload_blobs keeps
// byte_length, CHECKed equal to length(payload), and it precedes payload in
// the row, so the sum is an indexed join that never touches payload pages.
func projectBytes(ctx context.Context, q reader, pid string) (int64, error) {
	var size int64
	e := q.QueryRowContext(ctx, `SELECT COALESCE((SELECT sum(logical_bytes) FROM backend_observation_sets WHERE project_id=?),0)
 +COALESCE((SELECT sum(b.byte_length) FROM backend_observation_correlations r JOIN backend_payload_blobs b ON b.key=r.payload_key WHERE r.project_id=?),0)
 +COALESCE((SELECT sum(b.byte_length) FROM backend_observation_versions r JOIN backend_payload_blobs b ON b.key=r.payload_key WHERE r.project_id=?),0)`, pid, pid, pid).Scan(&size)
	return size, e
}
func readVersion(ctx context.Context, q reader, pid, sid string, v int64) (*Version, error) {
	if !p.ValidID(pid) || !p.ValidID(sid) || v < 1 {
		return nil, invalid()
	}
	var raw []byte
	e := q.QueryRowContext(ctx, `SELECT document FROM backend_observation_versions_documents WHERE project_id=? AND set_id=? AND version=?`, pid, sid, v).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, fault(404, "not_found")
	}
	if e != nil {
		return nil, e
	}
	out := new(Version)
	e = json.Unmarshal(raw, out)
	return out, e
}
func (r *Repo) Version(ctx context.Context, pid, sid string, v int64) (*Version, error) {
	return readVersion(ctx, r.db.R, pid, sid, v)
}
func pageLimit(n int) (int, error) {
	if n == 0 {
		return 100, nil
	}
	if n < 1 || n > 500 {
		return 0, invalid()
	}
	return n, nil
}
func cursor(domain, last string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(domain + "\n" + last))
}
func after(c, domain string) (string, error) {
	if c == "" {
		return "", nil
	}
	raw, e := base64.RawURLEncoding.DecodeString(c)
	if e != nil || len(raw) > 2048 {
		return "", invalid()
	}
	d, last, ok := strings.Cut(string(raw), "\n")
	if !ok || d != domain {
		return "", fault(409, "cursor_mismatch")
	}
	return last, nil
}
func (r *Repo) Records(ctx context.Context, pid, sid string, v int64, limit int, c string) (*RecordPage, error) {
	ver, e := r.Version(ctx, pid, sid, v)
	if e != nil {
		return nil, e
	}
	limit, e = pageLimit(limit)
	if e != nil {
		return nil, e
	}
	domain := fmt.Sprint(pid, "/", sid, "/", v, "/", ver.ContentHash)
	last, e := after(c, domain)
	if e != nil {
		return nil, e
	}
	rows, e := r.db.R.QueryContext(ctx, `SELECT b.document FROM backend_observation_members m JOIN backend_observation_blobs_documents b ON b.hash=m.hash WHERE m.project_id=? AND m.set_id=? AND m.introduced_version<=? AND m.record_id>? ORDER BY m.record_id LIMIT ?`, pid, sid, v, last, limit+1)
	if e != nil {
		return nil, e
	}
	defer func() { _ = rows.Close() }()
	out := &RecordPage{Pin: ver.VersionReceipt, Items: []Record{}}
	for rows.Next() {
		var raw []byte
		var rec Record
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &rec); e != nil {
			return nil, e
		}
		out.Items = append(out.Items, rec)
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = cursor(domain, out.Items[limit-1].ID)
	}
	return out, rows.Err()
}
func (r *Repo) List(ctx context.Context, pid string, limit int, c string) (*SetPage, error) {
	limit, e := pageLimit(limit)
	if e != nil {
		return nil, e
	}
	last, e := after(c, pid+"/sets")
	if e != nil {
		return nil, e
	} // immutable version catalog: later versions do not alter prior rows
	rows, e := r.db.R.QueryContext(ctx, `SELECT document FROM backend_observation_versions_documents WHERE project_id=? AND (set_id||'/'||printf('%04d',version))>? ORDER BY set_id,version LIMIT ?`, pid, last, limit+1)
	if e != nil {
		return nil, e
	}
	defer func() { _ = rows.Close() }()
	out := &SetPage{Items: []Version{}}
	for rows.Next() {
		var raw []byte
		var v Version
		if e = rows.Scan(&raw); e != nil {
			return nil, e
		}
		if e = json.Unmarshal(raw, &v); e != nil {
			return nil, e
		}
		out.Items = append(out.Items, v)
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		v := out.Items[limit-1]
		out.NextCursor = cursor(pid+"/sets", fmt.Sprintf("%s/%04d", v.SetID, v.Version))
	}
	return out, rows.Err()
}
