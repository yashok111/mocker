package backendportable

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"math"
	"strings"
	"time"
	"uuid"

	"github.com/yashok111/mocker/internal/store"
)

// Staging owns only portable sessions/chunks/receipts. It never publishes projects.
// Domain preparation and atomic publication must use the shared owner Tx seams.
type Staging struct{ db *store.DB }

func NewStaging(db *store.DB) *Staging { return &Staging{db: db} }

type Session struct {
	ID           string `json:"id"`
	Direction    string `json:"direction"`
	Version      int64  `json:"version"`
	State        string `json:"state"`
	ManifestHash string `json:"manifestHash"`
}
type BeginInput struct {
	Manifest       Manifest `json:"manifest"`
	IdempotencyKey string   `json:"idempotencyKey"`
}
type SessionInput struct {
	ExpectedVersion int64  `json:"expectedVersion"`
	IdempotencyKey  string `json:"idempotencyKey"`
}
type PutInput struct {
	ExpectedVersion int64  `json:"expectedVersion"`
	Index           int    `json:"index"`
	Body            string `json:"body"`
	IdempotencyKey  string `json:"idempotencyKey"`
}

func (s *Staging) mutation(ctx context.Context, scope, op, key string, input any, fn func(*sql.Tx) (*Session, error)) (*Session, error) {
	if len(key) < 1 || len(key) > 200 || strings.TrimSpace(key) != key {
		return nil, fault(422, "Invalid idempotency key")
	}
	h, err := DocumentHash(struct {
		Scope     string
		Operation string
		Input     any
	}{scope, op, input})
	if err != nil {
		return nil, err
	}
	var result *Session
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		var prior, raw string
		err := tx.QueryRowContext(ctx, `SELECT request_hash,response FROM backend_portable_receipts WHERE scope=? AND operation=? AND idempotency_key=?`, scope, op, key).Scan(&prior, &raw)
		if err == nil {
			if h != prior {
				return fault(409, "Idempotency key belongs to different input")
			}
			result = new(Session)
			return json.Unmarshal([]byte(raw), result, json.RejectUnknownMembers(true))
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		result, err = fn(tx)
		if err != nil {
			return err
		}
		b, err := json.Marshal(result)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_portable_receipts(scope,operation,idempotency_key,session_id,request_hash,response) VALUES(?,?,?,?,?,?)`, scope, op, key, result.ID, h, string(b))
		return err
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
func (s *Staging) Begin(ctx context.Context, in BeginInput) (*Session, error) {
	return s.mutation(ctx, "installation", "begin", in.IdempotencyKey, in.Manifest, func(tx *sql.Tx) (*Session, error) {
		if err := in.Manifest.Validate(); err != nil {
			return nil, err
		}
		return insertSession(ctx, tx, "import", in.Manifest)
	})
}
func insertSession(ctx context.Context, tx *sql.Tx, direction string, m Manifest) (*Session, error) {
	b, err := canonical(m)
	if err != nil {
		return nil, err
	}
	// Bound uncommitted reservations, including declared but not yet uploaded bytes.
	var sessions int
	var reserved int64
	if err = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(sum(byte_count),0) FROM backend_portable_sessions WHERE state IN ('staging','ready')`).Scan(&sessions, &reserved); err != nil {
		return nil, err
	}
	var total int64
	for _, c := range m.Chunks {
		total += int64(c.Bytes)
	}
	if sessions >= 5 || reserved+total > 512<<20 {
		return nil, fault(413, "Portable staging quota exceeded")
	}
	out := &Session{ID: uuid.NewV7().String(), Direction: direction, Version: 1, State: "staging", ManifestHash: bytesHash(b)}
	now := time.Now().Unix()
	_, err = tx.ExecContext(ctx, `INSERT INTO backend_portable_sessions(id,direction,version,state,manifest_hash,manifest,byte_count,chunk_count,created_at,updated_at) VALUES(?,?,1,'staging',?,?,?,?,?,?)`, out.ID, direction, out.ManifestHash, string(b), total, len(m.Chunks), now, now)
	return out, err
}
func loadSession(ctx context.Context, tx *sql.Tx, id string) (*Session, *Manifest, error) {
	var out Session
	var raw string
	err := tx.QueryRowContext(ctx, `SELECT id,direction,version,state,manifest_hash,manifest FROM backend_portable_sessions WHERE id=?`, id).Scan(&out.ID, &out.Direction, &out.Version, &out.State, &out.ManifestHash, &raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, fault(404, "Portable session not found")
	}
	if err != nil {
		return nil, nil, err
	}
	var m Manifest
	if err = json.Unmarshal([]byte(raw), &m, json.RejectUnknownMembers(true)); err != nil {
		return nil, nil, err
	}
	return &out, &m, nil
}
func requireVersion(s *Session, expected int64) error {
	if expected <= 0 || s.Version != expected || s.Version == math.MaxInt64 {
		return fault(409, "Portable session version conflict")
	}
	return nil
}
func advanceSession(ctx context.Context, tx *sql.Tx, s *Session) error {
	s.Version++
	_, err := tx.ExecContext(ctx, `UPDATE backend_portable_sessions SET state=?,version=?,updated_at=? WHERE id=?`, s.State, s.Version, time.Now().Unix(), s.ID)
	return err
}
func (s *Staging) Put(ctx context.Context, id string, in PutInput) (*Session, error) {
	if len(in.Body) > MaxChunkBytes {
		return nil, fault(413, "Chunk byte quota")
	}
	return s.mutation(ctx, id, "put", in.IdempotencyKey, struct {
		Version int64
		Index   int
		Body    string
	}{in.ExpectedVersion, in.Index, in.Body}, func(tx *sql.Tx) (*Session, error) {
		session, m, err := loadSession(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if err = requireVersion(session, in.ExpectedVersion); err != nil {
			return nil, err
		}
		if session.Direction != "import" || session.State != "staging" {
			return nil, fault(409, "Session is not accepting import chunks")
		}
		if in.Index < 0 || in.Index >= len(m.Chunks) {
			return nil, fault(422, "Undeclared chunk")
		}
		if err = putChunk(ctx, tx, id, *m, in.Index, []byte(in.Body)); err != nil {
			return nil, err
		}
		if err = advanceSession(ctx, tx, session); err != nil {
			return nil, err
		}
		return session, nil
	})
}
func putChunk(ctx context.Context, tx *sql.Tx, id string, m Manifest, index int, body []byte) error {
	c := m.Chunks[index]
	records, err := DecodeChunk(c, body)
	if err != nil {
		return err
	}
	seen := map[recordKey]bool{}
	for _, r := range records {
		if r.Identity.InstallationID != m.OriginInstallationID || r.Identity.ProjectID != m.Selection.ProjectID {
			return fault(422, "Record outside declared namespace")
		}
		k, err := r.key()
		if err != nil {
			return err
		}
		seen[k] = true
	}
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_portable_chunks WHERE session_id=? AND chunk_index=?`, id, index).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return fault(409, "Chunk is immutable; retry original request/key")
	}
	rows, err := tx.QueryContext(ctx, `SELECT body FROM backend_portable_chunks WHERE session_id=?`, id)
	if err != nil {
		return err
	}
	for rows.Next() {
		var b []byte
		if err = rows.Scan(&b); err != nil {
			_ = rows.Close()
			return err
		}
		var prior []Record
		if err = json.Unmarshal(b, &prior); err != nil {
			_ = rows.Close()
			return err
		}
		for _, r := range prior {
			k, err := r.key()
			if err != nil {
				_ = rows.Close()
				return err
			}
			if seen[k] {
				_ = rows.Close()
				return fault(422, "Duplicate record across chunks")
			}
		}
	}
	err = rows.Err()
	closeErr := rows.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO backend_portable_chunks(session_id,chunk_index,content_hash,record_count,byte_count,body) VALUES(?,?,?,?,?,?)`, id, index, c.SHA256, c.Records, c.Bytes, body)
	return err
}
func (s *Staging) Abort(ctx context.Context, id string, in SessionInput) (*Session, error) {
	return s.mutation(ctx, id, "abort", in.IdempotencyKey, in.ExpectedVersion, func(tx *sql.Tx) (*Session, error) {
		out, _, err := loadSession(ctx, tx, id)
		if err != nil {
			return nil, err
		}
		if err = requireVersion(out, in.ExpectedVersion); err != nil {
			return nil, err
		}
		if out.State != "staging" && out.State != "ready" {
			return nil, fault(409, "Session cannot be aborted")
		}
		out.State = "aborted"
		if err = advanceSession(ctx, tx, out); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `UPDATE backend_portable_sessions SET preview=NULL,candidate_hash=NULL WHERE id=?`, id); err != nil {
			return nil, err
		}
		if _, err = tx.ExecContext(ctx, `DELETE FROM backend_portable_chunks WHERE session_id=?`, id); err != nil {
			return nil, err
		}
		return out, nil
	})
}
