package backendobservations

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strconv"

	"github.com/yashok111/mocker/internal/backendblob"
	p "github.com/yashok111/mocker/internal/ordersprotocol"
)

func (r *Repo) saveCorrelation(ctx context.Context, pid, sid, hash string, out *CorrelationSnapshot) (*CorrelationSnapshot, error) {
	e := r.db.Write(ctx, func(tx *sql.Tx) error {
		in := out.Input
		if ok, e := readCorrelationReceipt(ctx, tx, pid, sid, in.IdempotencyKey, hash, out); e != nil || ok {
			return e
		}
		var head int64
		if e := tx.QueryRowContext(ctx, `SELECT COALESCE(max(version),0) FROM backend_observation_correlations_documents WHERE project_id=? AND set_id=?`, pid, sid).Scan(&head); e != nil {
			return e
		}
		if head != in.ExpectedCorrelationVersion {
			return fault(409, "correlation_version_conflict")
		}
		if head >= 100 {
			return fault(413, "correlation_limit")
		}
		content, e := p.Hash(CorrelationPolicy, struct {
			Input      CorrelateInput
			Compatible bool
			Rows       []CorrelationRow
			Diagrams   []DiagramCorrelationRow
		}{in, out.SourceCompatible, out.Rows, out.DiagramRows})
		if e != nil {
			return e
		}
		out.ContentHash = content
		out.Version = head + 1
		raw, e := canonical(out)
		if e != nil {
			return e
		}
		if len(raw) > 64<<20 {
			return fault(413, "correlation_quota")
		}
		if _, e = backendblob.Exec(ctx, tx, `INSERT INTO backend_observation_correlations(project_id,set_id,version,content_hash,document) VALUES(?,?,?,?,?)`, pid, sid, out.Version, content, string(raw)); e != nil {
			return e
		}
		if e = projectQuota(ctx, tx, pid); e != nil {
			return e
		}
		return saveReceipt(ctx, tx, pid, "correlate/"+sid, in.IdempotencyKey, hash, correlationReceipt{Ref: &correlationReceiptRef{Version: out.Version, ContentHash: content}})
	})
	return out, e
}

// correlationReceipt is what a correlate receipt stores: the immutable
// correlation version it answered.
//
// Review 2026-10-06, F162: the receipt held canonical(out), the whole snapshot
// of up to 64 MiB a second time, outside projectQuota (which sums sets,
// correlations and versions only), and receipts can be neither updated nor
// deleted, so a project held about twice its documented 512 MiB. The stored
// correlation document is canonical(out) of the same value, so replay
// rebuilds the identical snapshot from it. Receipts written before this
// change hold the full snapshot and replay as they did.
type correlationReceipt struct {
	Ref *correlationReceiptRef `json:"correlationReceipt"`
}
type correlationReceiptRef struct {
	Version     int64  `json:"version"`
	ContentHash string `json:"contentHash"`
}

func readCorrelationReceipt(ctx context.Context, q reader, pid, sid, key, hash string, out *CorrelationSnapshot) (bool, error) {
	var raw jsontext.Value
	if ok, e := receipt(ctx, q, pid, "correlate/"+sid, key, hash, &raw); e != nil || !ok {
		return ok, e
	}
	var stored correlationReceipt
	if e := json.Unmarshal(raw, &stored); e != nil {
		return true, e
	}
	if stored.Ref == nil {
		return true, json.Unmarshal(raw, out)
	}
	var document []byte
	if e := q.QueryRowContext(ctx, `SELECT document FROM backend_observation_correlations_documents WHERE project_id=? AND set_id=? AND version=?`, pid, sid, stored.Ref.Version).Scan(&document); e != nil {
		return true, e
	}
	if e := json.Unmarshal(document, out); e != nil {
		return true, e
	}
	if out.ContentHash != stored.Ref.ContentHash {
		return true, errors.New("correlation receipt does not match its stored version")
	}
	return true, nil
}
func (r *Repo) Correlation(ctx context.Context, pid, sid string, v int64) (*CorrelationSnapshot, error) {
	if !p.ValidID(pid) || !p.ValidID(sid) || v < 1 {
		return nil, invalid()
	}
	var raw []byte
	e := r.db.R.QueryRowContext(ctx, `SELECT document FROM backend_observation_correlations_documents WHERE project_id=? AND set_id=? AND version=?`, pid, sid, v).Scan(&raw)
	if errors.Is(e, sql.ErrNoRows) {
		return nil, fault(404, "not_found")
	}
	if e != nil {
		return nil, e
	}
	out := new(CorrelationSnapshot)
	e = json.Unmarshal(raw, out)
	return out, e
}

// CorrelationPage slices one frozen result, retaining its original content hash and pins.
func (r *Repo) CorrelationPage(ctx context.Context, pid, sid string, v int64, limit int, c string) (*CorrelationSnapshot, error) {
	out, e := r.Correlation(ctx, pid, sid, v)
	if e != nil {
		return nil, e
	}
	return correlationPage(out, pid, limit, c)
}
func correlationPage(out *CorrelationSnapshot, pid string, limit int, c string) (*CorrelationSnapshot, error) {
	limit, e := pageLimit(limit)
	if e != nil {
		return nil, e
	}
	domain := pid + "/correlation/" + out.Input.Observation.SetID + "/" + fmt.Sprint(out.Version) + "/" + out.ContentHash
	last, e := after(c, domain)
	if e != nil {
		return nil, e
	}
	start := 0
	if last != "" {
		n, e := strconv.Atoi(last)
		if e != nil || n < 0 || n >= len(out.Rows) {
			return nil, invalid()
		}
		start = n
	}
	end := min(start+limit, len(out.Rows))
	out.Total = len(out.Rows)
	if end < len(out.Rows) {
		out.NextCursor = cursor(domain, strconv.Itoa(end))
	}
	out.Rows = out.Rows[start:end]
	if len(out.DiagramRows) > 0 {
		out.DiagramRows = out.DiagramRows[start:end]
	}
	return out, nil
}
