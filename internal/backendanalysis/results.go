package backendanalysis

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/yashok111/mocker/internal/backendblob"

	"github.com/yashok111/mocker/internal/backendmodel"
)

var sections = []string{"changes", "findings", "witnesses", "checks", "gaps"}

// Every section shares these selectors. Detail remains exact JSON (in particular,
// native types and numeric lexemes); filters never inspect untyped detail.
type ResultRecord struct {
	ID        string         `json:"id"`
	Service   string         `json:"service"`
	Kind      string         `json:"kind"`
	Certainty string         `json:"certainty"`
	Direction string         `json:"direction"`
	Depth     int            `json:"depth"`
	Object    ObjectAddress  `json:"object"`
	Detail    jsontext.Value `json:"detail"`
}

// Snapshots carry the cumulative immutable prefix. This bounds validation memory
// by the job limit and lets publication compare retries without hashing under the
// writer lock. Engines may change chunk sizes without changing semantic hashes.
func prepareSnapshot(s PreparedSnapshot) (PreparedSnapshot, error) {
	s.Chunks = slices.Clone(s.Chunks)
	s.chunkCounts = make([]int, len(s.Chunks))
	totals := map[string]SectionManifest{}
	content := map[string][]jsontext.Value{}
	var size int64
	var records int
	for i := range s.Chunks {
		c := &s.Chunks[i]
		if c.Sequence != int64(i+1) || !slices.Contains(sections, c.Section) {
			return s, malformed("Invalid chunk sequence or section")
		}
		if int64(len(c.ItemsJSON)) > maxResultBytes-size {
			return s, fault(413, "result_limit", "Result exceeds byte limit")
		}
		var items []jsontext.Value
		if len(bytes.TrimSpace(c.ItemsJSON)) == 0 || bytes.TrimSpace(c.ItemsJSON)[0] != '[' || json.Unmarshal(c.ItemsJSON, &items) != nil {
			return s, malformed("Chunk items must be an array")
		}
		for _, item := range items {
			if len(item) == 0 || item[0] != '{' {
				return s, malformed("Report record must be an object")
			}
		}
		s.chunkCounts[i] = len(items)
		records += len(items)
		if records > 20000 {
			return s, fault(413, "record_limit", "Too many report records")
		}
		raw, err := canonical(items)
		if err != nil {
			return s, err
		}
		c.ItemsJSON = raw
		c.ContentHash = digest(raw)
		c.JobID = s.Manifest.JobID
		count := totals[c.Section]
		count.Section = c.Section
		count.Count += len(items)
		count.Bytes += int64(len(raw))
		totals[c.Section] = count
		content[c.Section] = append(content[c.Section], items...)
		size += int64(len(raw))
	}
	s.Manifest.HighWaterSequence = int64(len(s.Chunks))
	s.Manifest.Sections = nil
	for _, section := range sections {
		m := totals[section]
		m.Section = section
		s.Manifest.Sections = append(s.Manifest.Sections, m)
	}
	s.Manifest.RuntimeVerified = false
	if err := sealManifest(&s, content); err != nil {
		return s, err
	}
	if int64(len(s.ManifestJSON)) > maxManifestBytes {
		// Only an oversized manifest is summarized, so every manifest that was
		// storable before keeps its bytes and semantic hash (F135/F189/F190).
		s.Manifest = fitManifest(s.Manifest)
		if err := sealManifest(&s, content); err != nil {
			return s, err
		}
	}
	if int64(len(s.ManifestJSON)) > maxManifestBytes {
		return s, fault(413, "manifest_limit", "Manifest exceeds reserved terminal headroom")
	}
	return s, nil
}

// sealManifest computes the semantic hash and the stored bytes of a manifest.
func sealManifest(s *PreparedSnapshot, content map[string][]jsontext.Value) error {
	s.Manifest.SemanticResultHash = ""
	semantic := s.Manifest
	semantic.JobID = ""
	semantic.ResultVersion = 0
	semantic.HighWaterSequence = 0
	semantic.Sections = slices.Clone(s.Manifest.Sections)
	for i := range semantic.Sections {
		semantic.Sections[i].Bytes = 0
	}
	raw, err := canonical(struct {
		Manifest ResultManifest              `json:"manifest"`
		Content  map[string][]jsontext.Value `json:"content"`
	}{semantic, content})
	if err != nil {
		return err
	}
	s.Manifest.SemanticResultHash = digest(raw)
	s.ManifestJSON, err = canonical(s.Manifest)
	return err
}
func (r *Repo) Publish(ctx context.Context, pid, id, token string, s PreparedSnapshot) (*ResultManifest, error) {
	s.Manifest.JobID = id
	prepared, err := prepareSnapshot(s)
	if err != nil {
		return nil, err
	}
	err = r.db.Write(ctx, func(tx *sql.Tx) error { return publishTx(ctx, tx, pid, id, token, prepared, false) })
	if err != nil {
		return nil, err
	}
	return &prepared.Manifest, nil
}
func publishTx(ctx context.Context, tx *sql.Tx, pid, id, token string, s PreparedSnapshot, terminal bool) error {
	j, err := readJob(ctx, tx, pid, id)
	if err != nil {
		return err
	}
	var actualToken *string
	var retained, reserved, headroom int64
	if err = tx.QueryRowContext(ctx, `SELECT worker_token,result_bytes,reserved_output_bytes,reserved_terminal_bytes FROM backend_analysis_jobs WHERE project_id=? AND id=?`, pid, id).Scan(&actualToken, &retained, &reserved, &headroom); err != nil {
		return err
	}
	if j.Status != "running" || actualToken == nil || *actualToken != token {
		return fault(409, "lost_claim", "Worker no longer owns this job")
	}
	if s.Manifest.AnalysisInputHash != j.AnalysisInputHash {
		return fault(409, "input_conflict", "Report input differs")
	}
	var previous string
	err = tx.QueryRowContext(ctx, `SELECT document FROM backend_analysis_manifests_documents WHERE project_id=? AND job_id=? AND result_version=?`, pid, id, s.Manifest.ResultVersion).Scan(&previous)
	if err == nil {
		if previous != string(s.ManifestJSON) {
			return fault(409, "snapshot_conflict", "Published manifest differs")
		}
		return verifyChunks(ctx, tx, pid, id, s.Chunks)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	next := int64(1)
	if j.ResultVersion != nil {
		next = *j.ResultVersion + 1
	}
	if s.Manifest.ResultVersion != next {
		return fault(409, "snapshot_conflict", "Result version must advance by one")
	}
	var high int64
	if err = tx.QueryRowContext(ctx, `SELECT COALESCE(max(sequence),0) FROM backend_analysis_chunks_documents WHERE project_id=? AND job_id=?`, pid, id).Scan(&high); err != nil {
		return err
	}
	if s.Manifest.HighWaterSequence < high {
		return fault(409, "snapshot_conflict", "Cannot discard accepted prefix")
	}
	if err = verifyChunks(ctx, tx, pid, id, s.Chunks[:high]); err != nil {
		return err
	}
	cost := int64(len(s.ManifestJSON))
	for _, c := range s.Chunks[high:] {
		cost += int64(len(c.ItemsJSON))
	}
	available := reserved
	if !terminal {
		available -= headroom
	}
	if cost > available || retained+cost > maxResultBytes {
		return fault(413, "result_limit", "Publication would consume terminal reserve")
	}
	return appendSnapshotTx(ctx, tx, pid, id, s, high, cost, reserved, headroom, terminal)
}
func appendSnapshotTx(ctx context.Context, tx *sql.Tx, pid, id string, s PreparedSnapshot, high, cost, reserved, headroom int64, terminal bool) error {
	var err error
	for index, c := range s.Chunks[high:] {
		if _, err = backendblob.Exec(ctx, tx, `INSERT INTO backend_analysis_chunks(project_id,job_id,sequence,section,content_hash,items_json,record_count,chunk_bytes) VALUES(?,?,?,?,?,?,?,?)`, pid, id, c.Sequence, c.Section, c.ContentHash, string(c.ItemsJSON), s.chunkCounts[int(high)+index], len(c.ItemsJSON)); err != nil {
			return err
		}
	}
	if _, err = backendblob.Exec(ctx, tx, `INSERT INTO backend_analysis_manifests(project_id,job_id,result_version,high_water_sequence,result_hash,document,manifest_bytes) VALUES(?,?,?,?,?,?,?)`, pid, id, s.Manifest.ResultVersion, s.Manifest.HighWaterSequence, s.Manifest.SemanticResultHash, string(s.ManifestJSON), len(s.ManifestJSON)); err != nil {
		return err
	}
	nextReserve := reserved - cost
	nextHeadroom := headroom
	if terminal {
		nextReserve = 0
		nextHeadroom = 0
	}
	_, err = tx.ExecContext(ctx, `UPDATE backend_analysis_jobs SET result_version=?,result_bytes=result_bytes+?,reserved_output_bytes=?,reserved_terminal_bytes=?,progress_states=?,progress_dependency_visits=?,progress_findings=?,progress_records=?,version=version+1,updated_at=? WHERE project_id=? AND id=?`, s.Manifest.ResultVersion, cost, nextReserve, nextHeadroom, s.Progress.States, s.Progress.DependencyVisits, s.Progress.Findings, s.Progress.Records, time.Now().UTC().Format(time.RFC3339Nano), pid, id)
	return err
}
func verifyChunks(ctx context.Context, q reader, pid, id string, chunks []ResultChunk) error {
	for _, c := range chunks {
		var section, hash, raw string
		var count, size int64
		err := q.QueryRowContext(ctx, `SELECT section,content_hash,items_json,record_count,chunk_bytes FROM backend_analysis_chunks_documents WHERE project_id=? AND job_id=? AND sequence=?`, pid, id, c.Sequence).Scan(&section, &hash, &raw, &count, &size)
		if err != nil {
			return err
		}
		if section != c.Section || hash != c.ContentHash || raw != string(c.ItemsJSON) || size != int64(len(raw)) {
			return fault(409, "chunk_conflict", "Immutable chunk differs")
		}
	}
	return nil
}
func (r *Repo) Finalize(ctx context.Context, pid, id, token string, t TerminalSnapshot) (*Job, error) {
	if !slices.Contains([]string{"completed", "failed", "interrupted"}, t.Status) {
		return nil, malformed("Invalid terminal status")
	}
	t.Snapshot.Manifest.JobID = id
	s, err := prepareSnapshot(t.Snapshot)
	if err != nil {
		return nil, err
	}
	var diagnostic any
	if t.Diagnostic != nil {
		raw, e := canonical(t.Diagnostic)
		if e != nil {
			return nil, e
		}
		if len(raw) > 4096 {
			return nil, malformed("Diagnostic exceeds bound")
		}
		diagnostic = string(raw)
	}
	var out *Job
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		if err := publishTx(ctx, tx, pid, id, token, s, true); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE backend_analysis_jobs SET status=?,worker_token=NULL,diagnostic=?,reserved_output_bytes=0,reserved_terminal_bytes=0,version=version+1 WHERE project_id=? AND id=?`, t.Status, diagnostic, pid, id); err != nil {
			return err
		}
		out, err = readJob(ctx, tx, pid, id)
		if err != nil {
			return err
		}
		if out.Kind == "diagnostics" && t.Status == "completed" {
			findings := []backendmodel.Finding{}
			checks := []backendmodel.FindingCheck{}
			for _, chunk := range s.Chunks {
				var records []ResultRecord
				if err = json.Unmarshal(chunk.ItemsJSON, &records); err != nil {
					return err
				}
				for _, record := range records {
					if chunk.Section == "findings" {
						var f backendmodel.Finding
						if err = json.Unmarshal(record.Detail, &f); err != nil {
							return err
						}
						findings = append(findings, f)
					}
					if chunk.Section == "checks" {
						var c backendmodel.FindingCheck
						if err = json.Unmarshal(record.Detail, &c); err != nil {
							return err
						}
						if !s.Manifest.Complete && c.Status == "absent" {
							c.Status = "unknown"
						}
						checks = append(checks, c)
					}
				}
			}
			return backendmodel.IndexFindingReportTx(ctx, tx, pid, backendmodel.FindingAnalysisRef{JobID: id, ResultVersion: s.Manifest.ResultVersion}, findings, checks)
		}
		return nil
	})
	return out, err
}

type pageCursor struct {
	Binding  string `json:"binding"`
	Offset   int    `json:"offset"`
	Checksum string `json:"checksum"`
}

func encodeCursor(binding string, offset int) string {
	c := pageCursor{Binding: binding, Offset: offset}
	c.Checksum = digest([]byte(fmt.Sprintf("%s:%d", binding, offset)))
	raw, _ := canonical(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeCursor(raw, binding string) (int, error) {
	if raw == "" {
		return 0, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return 0, malformed("Invalid cursor")
	}
	var c pageCursor
	if json.Unmarshal(b, &c, json.RejectUnknownMembers(true)) != nil || c.Binding != binding || c.Offset < 0 || c.Checksum != digest([]byte(fmt.Sprintf("%s:%d", binding, c.Offset))) {
		return 0, malformed("Cursor does not match pinned query")
	}
	return c.Offset, nil
}

// jobKey is the job list's keyset position: the last row a page returned.
type jobKey struct {
	CreatedAt string `json:"createdAt"`
	ID        string `json:"id"`
}

// jobCursor binds a keyset position to its query exactly as pageCursor binds
// an offset; the checksum makes a hand-edited position a malformed cursor.
type jobCursor struct {
	Binding  string `json:"binding"`
	After    jobKey `json:"after"`
	Checksum string `json:"checksum"`
}

func jobCursorChecksum(binding string, after jobKey) string {
	return digest([]byte(binding + "\x00" + after.CreatedAt + "\x00" + after.ID))
}
func encodeJobCursor(binding string, after jobKey) string {
	raw, _ := canonical(jobCursor{Binding: binding, After: after, Checksum: jobCursorChecksum(binding, after)})
	return base64.RawURLEncoding.EncodeToString(raw)
}
func decodeJobCursor(raw, binding string) (jobKey, error) {
	if raw == "" {
		return jobKey{}, nil
	}
	b, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return jobKey{}, malformed("Invalid cursor")
	}
	var c jobCursor
	if json.Unmarshal(b, &c, json.RejectUnknownMembers(true)) != nil || c.Binding != binding || c.After.ID == "" || c.Checksum != jobCursorChecksum(binding, c.After) {
		return jobKey{}, malformed("Cursor does not match pinned query")
	}
	return c.After, nil
}
func (r *Repo) Results(ctx context.Context, pid, id string, in ResultQuery) (*ResultPage, error) {
	if in.ResultVersion < 1 || !slices.Contains(sections, in.Section) || in.Limit < 0 || in.Limit > 500 || in.Depth < 0 {
		return nil, malformed("Invalid result query")
	}
	if in.Limit == 0 {
		in.Limit = 100
	}
	selection := in
	selection.Cursor = ""
	binding, err := requestHash(struct {
		Project, Job string
		Query        ResultQuery
	}{pid, id, selection})
	if err != nil {
		return nil, err
	}
	offset, err := decodeCursor(in.Cursor, binding)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	j, err := readJob(ctx, tx, pid, id)
	if err != nil {
		return nil, err
	}
	if j.ResultVersion == nil {
		return nil, fault(409, "unpublished", "No published result")
	}
	var raw string
	err = tx.QueryRowContext(ctx, `SELECT document FROM backend_analysis_manifests_documents WHERE project_id=? AND job_id=? AND result_version=?`, pid, id, in.ResultVersion).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fault(404, "not_found", "Pinned result not found")
	}
	if err != nil {
		return nil, err
	}
	page := &ResultPage{Section: in.Section}
	if err = json.Unmarshal([]byte(raw), &page.Manifest); err != nil {
		return nil, err
	}
	items, more, err := readPageItems(ctx, tx, pid, id, in, page.Manifest.HighWaterSequence, offset)
	if err != nil {
		return nil, err
	}
	page.ItemsJSON, err = canonical(items)
	if err != nil {
		return nil, err
	}
	if more {
		page.NextCursor = encodeCursor(binding, offset+len(items))
	}
	return page, nil
}
func matches(raw []byte, in ResultQuery) bool {
	var record struct {
		Service, Kind, Certainty, Direction string
		Depth                               int
	}
	if json.Unmarshal(raw, &record, json.MatchCaseInsensitiveNames(true)) != nil {
		return false
	}
	return (in.Service == "" || in.Service == record.Service) && (in.Kind == "" || in.Kind == record.Kind) && (in.Certainty == "" || in.Certainty == record.Certainty) && (in.Direction == "" || in.Direction == record.Direction) && (in.Depth == 0 && !in.DepthSet || record.Depth <= in.Depth)
}
func (r *Repo) List(ctx context.Context, pid string, in ListQuery) (*JobPage, error) {
	if in.Limit < 0 || in.Limit > 500 {
		return nil, malformed("Invalid list limit")
	}
	if in.Limit == 0 {
		in.Limit = 100
	}
	selection := in
	selection.Cursor = ""
	binding, err := requestHash(struct {
		Project string
		Query   ListQuery
	}{pid, selection})
	if err != nil {
		return nil, err
	}
	after, err := decodeJobCursor(in.Cursor, binding)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	// Keyset, not OFFSET (review 2026-10-06, F142): status is a live column,
	// so a job that completed, was cancelled or was retained away between
	// pages shifted every later row and an offset skipped one. Resuming
	// strictly after the last (created_at, id) returned cannot skip or repeat.
	rows, err := tx.QueryContext(ctx, `SELECT id,created_at FROM backend_analysis_jobs WHERE project_id=? AND (?='' OR status=?) AND (?='' OR kind=?) AND (?=0 OR created_at>? OR (created_at=? AND id>?)) ORDER BY created_at,id LIMIT ?`, pid, in.Status, in.Status, in.Kind, in.Kind, len(after.ID), after.CreatedAt, after.CreatedAt, after.ID, in.Limit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	ids := []string{}
	created := []string{}
	for rows.Next() {
		var id, at string
		if err = rows.Scan(&id, &at); err != nil {
			return nil, err
		}
		ids = append(ids, id)
		created = append(created, at)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	page := &JobPage{Items: []Job{}}
	if len(ids) > in.Limit {
		page.NextCursor = encodeJobCursor(binding, jobKey{CreatedAt: created[in.Limit-1], ID: ids[in.Limit-1]})
		ids = ids[:in.Limit]
	}
	for _, id := range ids {
		j, e := readJob(ctx, tx, pid, id)
		if e != nil {
			return nil, e
		}
		page.Items = append(page.Items, *j)
	}
	return page, nil
}

func readPageItems(ctx context.Context, tx *sql.Tx, pid, id string, in ResultQuery, high int64, offset int) ([]jsontext.Value, bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT items_json FROM backend_analysis_chunks_documents WHERE project_id=? AND job_id=? AND section=? AND sequence<=? ORDER BY sequence`, pid, id, in.Section, high)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	items := []jsontext.Value{}
	seen := 0
	more := false
	for rows.Next() {
		var chunk []byte
		if err = rows.Scan(&chunk); err != nil {
			return nil, false, err
		}
		var records []jsontext.Value
		if err = json.Unmarshal(chunk, &records); err != nil {
			return nil, false, err
		}
		for _, record := range records {
			if !matches(record, in) {
				continue
			}
			if seen >= offset {
				if len(items) == in.Limit {
					more = true
					break
				}
				items = append(items, record)
			}
			seen++
		}
		if more {
			break
		}
	}
	if err = rows.Err(); err != nil {
		return nil, false, err
	}
	return items, more, nil
}
