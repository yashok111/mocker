package backendobservations

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"fmt"
	"github.com/yashok111/mocker/internal/backendblob"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
	"github.com/yashok111/mocker/internal/store"
	"strings"
	"uuid"
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
	if !p.ValidID(pid) || !bounded(in.BatchID, 128) || !bounded(in.IdempotencyKey, 128) || len(in.Records) == 0 || len(in.Records) > 500 {
		return nil, invalid()
	}
	if in.Mode == "create" {
		if in.Context == nil || !bounded(in.Name, 256) || in.SetID != "" || in.ExpectedVersion != 0 {
			return nil, invalid()
		}
		if e = ValidateContext(*in.Context); e != nil {
			return nil, e
		}
	} else if in.Mode != "append" || in.Context != nil || in.Name != "" || !p.ValidID(in.SetID) || in.ExpectedVersion < 1 {
		return nil, invalid()
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
		sid := in.SetID
		v := int64(1)
		var c ObservationContext
		var name string
		var size int64
		var count int
		if in.Mode == "create" {
			sid = uuid.NewV7().String()
			c = *in.Context
			name = in.Name
			cb, _ := canonical(c)
			size = int64(len(cb))
			var sets int
			if e := tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_observation_sets WHERE project_id=?`, pid).Scan(&sets); e != nil {
				return e
			}
			if sets >= 256 {
				return fault(413, "set_limit")
			}
		} else {
			var raw []byte
			var head int64
			e := tx.QueryRowContext(ctx, `SELECT version,context,name,logical_bytes,record_count FROM backend_observation_sets WHERE project_id=? AND id=?`, pid, sid).Scan(&head, &raw, &name, &size, &count)
			if errors.Is(e, sql.ErrNoRows) {
				return fault(404, "not_found")
			}
			if e != nil {
				return e
			}
			if head != in.ExpectedVersion {
				return fault(409, "version_conflict")
			}
			if head >= 1000 {
				return fault(413, "version_limit")
			}
			v = head + 1
			if e = json.Unmarshal(raw, &c); e != nil {
				return e
			}
		}
		var used int
		e := tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_observation_batches WHERE project_id=? AND set_id=? AND batch_id=?`, pid, sid, in.BatchID).Scan(&used)
		if e != nil {
			return e
		}
		if used != 0 {
			return fault(409, "batch_conflict")
		}
		type entry struct{ id, hash, raw string }
		added := []entry{}
		seen := map[string]string{}
		for _, rec := range in.Records {
			if e := ValidateRecord(c, rec); e != nil {
				return e
			}
			h, _ := RecordHash(rec)
			if old, ok := seen[rec.ID]; ok {
				if old != h {
					return fault(409, "record_conflict")
				}
				continue
			}
			seen[rec.ID] = h
			var old string
			e := tx.QueryRowContext(ctx, `SELECT hash FROM backend_observation_members WHERE project_id=? AND set_id=? AND record_id=?`, pid, sid, rec.ID).Scan(&old)
			if e == nil {
				if old != h {
					return fault(409, "record_conflict")
				}
				continue
			}
			if !errors.Is(e, sql.ErrNoRows) {
				return e
			}
			raw, _ := canonical(rec)
			added = append(added, entry{rec.ID, h, string(raw)})
			size += int64(len(raw))
			count++
		}
		if count > 100000 || size > 128<<20 {
			return fault(413, "set_quota")
		}
		if in.Mode == "create" {
			cb, _ := canonical(c)
			if _, e = tx.ExecContext(ctx, `INSERT INTO backend_observation_sets VALUES(?,?,?,?,?,?,?)`, pid, sid, v, name, string(cb), size, count); e != nil {
				return e
			}
		}
		for _, a := range added {
			if _, e = backendblob.Exec(ctx, tx, `INSERT OR IGNORE INTO backend_observation_blobs(hash,document) VALUES(?,?)`, a.hash, a.raw); e != nil {
				return e
			}
			if _, e = backendblob.Exec(ctx, tx, `INSERT INTO backend_observation_members(project_id,set_id,record_id,hash,introduced_version) VALUES(?,?,?,?,?)`, pid, sid, a.id, a.hash, v); e != nil {
				return e
			}
		}
		type member struct {
			ID   string `json:"id"`
			Hash string `json:"hash"`
		}
		members := []member{}
		rows, e := tx.QueryContext(ctx, `SELECT record_id,hash FROM backend_observation_members WHERE project_id=? AND set_id=? ORDER BY record_id`, pid, sid)
		if e != nil {
			return e
		}
		for rows.Next() {
			var m member
			if e = rows.Scan(&m.ID, &m.Hash); e != nil {
				rows.Close()
				return e
			}
			members = append(members, m)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		content, e := p.Hash(DocumentVersion, struct {
			Context ObservationContext `json:"context"`
			Members []member           `json:"members"`
		}{c, members})
		if e != nil {
			return e
		}
		*out = VersionReceipt{sid, v, content, count}
		doc := Version{*out, c, name, size}
		raw, _ := canonical(doc)
		if _, e = backendblob.Exec(ctx, tx, `INSERT INTO backend_observation_versions(project_id,set_id,version,content_hash,document) VALUES(?,?,?,?,?)`, pid, sid, v, content, string(raw)); e != nil {
			return e
		}
		if in.Mode == "append" {
			if _, e = tx.ExecContext(ctx, `UPDATE backend_observation_sets SET version=?,logical_bytes=?,record_count=? WHERE project_id=? AND id=?`, v, size, count, pid, sid); e != nil {
				return e
			}
		}
		if e = projectQuota(ctx, tx, pid); e != nil {
			return e
		}
		if _, e = tx.ExecContext(ctx, `INSERT INTO backend_observation_batches VALUES(?,?,?,?)`, pid, sid, in.BatchID, hash); e != nil {
			return e
		}
		return saveReceipt(ctx, tx, pid, "import", in.IdempotencyKey, hash, out)
	})
	return out, e
}
func projectQuota(ctx context.Context, q reader, pid string) error {
	var size int64
	e := q.QueryRowContext(ctx, `SELECT COALESCE((SELECT sum(logical_bytes) FROM backend_observation_sets WHERE project_id=?),0)+COALESCE((SELECT sum(length(CAST(document AS BLOB))) FROM backend_observation_correlations_documents WHERE project_id=?),0)+COALESCE((SELECT sum(length(CAST(document AS BLOB))) FROM backend_observation_versions_documents WHERE project_id=?),0)`, pid, pid, pid).Scan(&size)
	if e != nil {
		return e
	}
	if size > 512<<20 {
		return fault(413, "project_quota")
	}
	return nil
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
	defer rows.Close()
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
	defer rows.Close()
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
