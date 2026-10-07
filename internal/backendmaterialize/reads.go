package backendmaterialize

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json/v2"
	"errors"
	"time"

	"github.com/yashok111/mocker/internal/backendmodel"
)

type ResultSummary struct {
	ID          string                         `json:"id"`
	ProjectID   string                         `json:"projectId"`
	Author      string                         `json:"author"`
	CreatedAt   time.Time                      `json:"createdAt"`
	Reason      string                         `json:"reason"`
	Target      backendmodel.BackendReadTarget `json:"target"`
	TargetHash  string                         `json:"targetHash"`
	TargetCount int                            `json:"targetCount"`
}
type Result struct {
	ID        string    `json:"id"`
	ProjectID string    `json:"projectId"`
	CreatedAt time.Time `json:"createdAt"`
	Preview   *Preview  `json:"preview"`
	Receipt   *Receipt  `json:"receipt"`
}
type ResultPage struct {
	Items      []ResultSummary `json:"items"`
	NextCursor string          `json:"nextCursor"`
}
type resultCursor struct {
	Project string `json:"project"`
	Time    int64  `json:"time"`
	ID      string `json:"id"`
}

func unavailableResult() error {
	return &backendmodel.FaultError{Status: 404, Code: "backend_materialization_not_found", Message: "Recorded materialization result unavailable"}
}

// The documents view also resolves pre-UI rows migrated to backendblob. The
// original Preview/Receipt pair is authoritative; reads never consult owner heads.
func (s *Service) ReadResult(ctx context.Context, pid, id string) (*Result, error) {
	if !backendmodel.ValidID(pid) || !backendmodel.ValidID(id) {
		return nil, unavailableResult()
	}
	var document string
	var created int64
	err := s.db.R.QueryRowContext(ctx, `SELECT document,created_at FROM backend_materializations_documents WHERE project_id=? AND id=?`, pid, id).Scan(&document, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, unavailableResult()
	}
	if err != nil {
		return nil, err
	}
	return decodeResult(pid, id, created, document)
}
func decodeResult(pid, id string, created int64, document string) (*Result, error) {
	// Apply historically persisted uppercase field names; decode those exact bytes.
	var stored struct {
		Preview *Preview
		Receipt *Receipt
	}
	if err := json.Unmarshal([]byte(document), &stored); err != nil {
		return nil, err
	}
	if stored.Preview == nil || stored.Receipt == nil || stored.Receipt.ID != id || stored.Receipt.ProjectID != pid {
		return nil, unavailableResult()
	}
	return &Result{ID: id, ProjectID: pid, CreatedAt: time.Unix(created, 0).UTC(), Preview: stored.Preview, Receipt: stored.Receipt}, nil
}
func (s *Service) ListResults(ctx context.Context, pid string, limit int, cursor string) (*ResultPage, error) {
	if _, err := s.models.Get(ctx, pid); err != nil {
		return nil, err
	}
	if limit == 0 {
		limit = 20
	}
	if limit < 1 || limit > 100 || len(cursor) > 1024 {
		return nil, invalid("Invalid result pagination")
	}
	c := resultCursor{Project: pid, Time: 1 << 62, ID: ""}
	if cursor != "" {
		b, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
		if err != nil {
			return nil, invalid("Invalid result cursor")
		}
		if err = json.Unmarshal(b, &c, json.RejectUnknownMembers(true)); err != nil || c.Project != pid || !backendmodel.ValidID(c.ID) || c.Time < 0 {
			return nil, invalid("Result cursor belongs to another project")
		}
	}
	rows, err := s.db.R.QueryContext(ctx, `SELECT id,created_at,document FROM backend_materializations_documents WHERE project_id=? AND (created_at<? OR (created_at=? AND id<?)) ORDER BY created_at DESC,id DESC LIMIT ?`, pid, c.Time, c.Time, c.ID, limit+1)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := &ResultPage{Items: []ResultSummary{}}
	for rows.Next() {
		var id, doc string
		var created int64
		if err = rows.Scan(&id, &created, &doc); err != nil {
			return nil, err
		}
		if len(out.Items) == limit {
			last := out.Items[len(out.Items)-1]
			b, _ := json.Marshal(resultCursor{Project: pid, Time: last.CreatedAt.Unix(), ID: last.ID})
			out.NextCursor = base64.RawURLEncoding.EncodeToString(b)
			break
		}
		result, e := decodeResult(pid, id, created, doc)
		if e != nil {
			return nil, e
		}
		out.Items = append(out.Items, ResultSummary{ID: id, ProjectID: pid, CreatedAt: result.CreatedAt, Author: result.Receipt.Author, Reason: result.Preview.Input.Reason, Target: result.Preview.Input.Target, TargetHash: result.Preview.Input.TargetHash, TargetCount: len(result.Receipt.Owners)})
	}
	return out, rows.Err()
}
