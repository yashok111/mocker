package backendportable

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"unicode/utf8"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/store"
)

type Service struct {
	db         *store.DB
	models     *bm.Repo
	staging    *Staging
	afterStage func(string) error
}

func NewService(db *store.DB, models *bm.Repo) *Service {
	return &Service{db: db, models: models, staging: NewStaging(db)}
}
func (s *Service) Begin(ctx context.Context, in BeginInput) (*Session, error) {
	return s.staging.Begin(ctx, in)
}
func (s *Service) Put(ctx context.Context, id string, in PutInput) (*Session, error) {
	return s.staging.Put(ctx, id, in)
}
func (s *Service) Abort(ctx context.Context, id string, in SessionInput) (*Session, error) {
	return s.staging.Abort(ctx, id, in)
}
func (s *Service) ExportChunk(ctx context.Context, id, hash string, index int) ([]byte, error) {
	return s.staging.ExportChunk(ctx, id, hash, index)
}

type ExportInput struct {
	Selection      Selection `json:"selection"`
	IdempotencyKey string    `json:"idempotencyKey"`
}
type ExportResult struct {
	Session  Session  `json:"session"`
	Manifest Manifest `json:"manifest"`
}
type PreviewInput struct {
	ExpectedVersion  int64                        `json:"expectedVersion"`
	Name             string                       `json:"name"`
	ArtifactMappings []bm.PortableArtifactMapping `json:"artifactMappings"`
	IdempotencyKey   string                       `json:"idempotencyKey"`
}
type CommitInput struct {
	ExpectedVersion int64  `json:"expectedVersion"`
	CandidateHash   string `json:"candidateHash"`
	IdempotencyKey  string `json:"idempotencyKey"`
}
type IDMapEntry struct {
	Origin      Identity  `json:"origin"`
	Local       Identity  `json:"local"`
	Parent      *Identity `json:"parent,omitzero"`
	LocalParent *Identity `json:"localParent,omitzero"`
	OriginHash  string    `json:"originHash"`
	LocalHash   string    `json:"localHash"`
}
type PreviewResult struct {
	Session       Session                    `json:"session"`
	CandidateHash string                     `json:"candidateHash"`
	ProjectID     string                     `json:"projectId"`
	Target        bm.BackendReadTarget       `json:"target"`
	TargetHash    string                     `json:"targetHash"`
	IDMap         []IDMapEntry               `json:"idMap"`
	Unresolved    []bm.NamespacedArtifactPin `json:"unresolved"`
	RecordCount   int                        `json:"recordCount"`
}
type CommitResult struct {
	Session       Session              `json:"session"`
	Project       bm.Project           `json:"project"`
	Target        bm.BackendReadTarget `json:"target"`
	TargetHash    string               `json:"targetHash"`
	CandidateHash string               `json:"candidateHash"`
	IDMap         []IDMapEntry         `json:"idMap"`
}
type preparedImport struct {
	Input      bm.PortableModel `json:"input"`
	Remap      bm.PortableRemap `json:"remap"`
	OutputHash string           `json:"outputHash"`
	Result     PreviewResult    `json:"result"`
}

// validIdempotencyKey is exactly what api/openapi.json states for every
// portable idempotencyKey: a string of 1–200 characters (review 2026-10-06,
// F15). Both the staging and the service mutations call it. It used to count
// bytes and refuse surrounding whitespace, rules the contract never stated,
// so a contract-valid key got a 422 that named neither. A key is an opaque
// receipt address compared byte for byte; whitespace cannot make two keys
// collide.
func validIdempotencyKey(key string) bool {
	n := utf8.RuneCountInString(key)
	return n >= 1 && n <= 200
}

func portableMutation[T any](ctx context.Context, s *Service, scope, operation, key string, input any, run func(*sql.Tx) (*T, string, error)) (*T, error) {
	if !validIdempotencyKey(key) {
		return nil, fault(422, "Invalid idempotency key")
	}
	hash, err := DocumentHash(struct {
		Scope, Operation string
		Input            any
	}{scope, operation, input})
	if err != nil {
		return nil, err
	}
	var out *T
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		var prior, raw string
		err := tx.QueryRowContext(ctx, `SELECT request_hash,response FROM backend_portable_receipts WHERE scope=? AND operation=? AND idempotency_key=?`, scope, operation, key).Scan(&prior, &raw)
		if err == nil {
			if prior != hash {
				return fault(409, "Idempotency key belongs to a different exact request")
			}
			return json.Unmarshal([]byte(raw), &out, json.RejectUnknownMembers(true))
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		var id string
		out, id, err = run(tx)
		if err != nil {
			return err
		}
		bytes, err := json.Marshal(out)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_portable_receipts(scope,operation,idempotency_key,session_id,request_hash,response) VALUES(?,?,?,?,?,?)`, scope, operation, key, id, hash, string(bytes))
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func loadRecords(ctx context.Context, tx *sql.Tx, session string, m *Manifest) ([]Record, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT chunk_index,body FROM backend_portable_chunks WHERE session_id=? ORDER BY chunk_index`, session)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Record{}
	index := 0
	total := 0
	for rows.Next() {
		var i int
		var raw []byte
		if err := rows.Scan(&i, &raw); err != nil {
			return nil, err
		}
		if i != index || i >= len(m.Chunks) {
			return nil, fault(422, "Missing/extra portable chunk")
		}
		records, err := DecodeChunk(m.Chunks[i], raw)
		if err != nil {
			return nil, err
		}
		out = append(out, records...)
		total += len(raw)
		if total > MaxBundleBytes {
			return nil, fault(413, "Bundle byte quota")
		}
		index++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if index != len(m.Chunks) {
		return nil, fault(422, "Upload all declared chunks before preview")
	}
	return out, nil
}

type SelectionInput struct {
	Target       bm.BackendReadTarget `json:"target"`
	DiagramViews []SVGInput           `json:"diagramViews"`
	SavedViews   []SavedViewPin       `json:"savedViews,omitzero"`
}

func (s *Service) ResolveSelection(ctx context.Context, pid string, in SelectionInput) (*Selection, error) {
	if in.Target.ImportCandidate != nil || in.DiagramViews == nil {
		return nil, fault(422, "Select immutable target and exact view list")
	}
	tx, err := s.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	graph, err := s.models.ResolveEffectiveGraphTx(ctx, tx, pid, in.Target)
	if err != nil {
		return nil, err
	}
	out := &Selection{ProjectID: pid, Target: in.Target, TargetHash: graph.Pins.TargetHash, DiagramViews: in.DiagramViews, SavedViews: in.SavedViews}
	for _, pin := range in.DiagramViews {
		v, err := s.models.GetDiagramViewTx(ctx, tx, pid, pin.ViewID, pin.ViewVersion)
		if err != nil {
			return nil, err
		}
		d, err := s.models.GetDiagramTx(ctx, tx, pid, v.State.Diagram)
		if err != nil {
			return nil, err
		}
		if d.TargetHash != out.TargetHash {
			return nil, fault(422, "Selected diagram view targets another exact graph")
		}
	}
	for _, pin := range in.SavedViews {
		v, err := s.models.GetSavedViewTx(ctx, tx, pid, pin.ID, pin.Version)
		if err != nil {
			return nil, err
		}
		a, _ := DocumentHash(v.Target)
		b, _ := DocumentHash(in.Target)
		if a != b {
			return nil, fault(422, "Selected saved view targets another exact graph")
		}
	}
	// The guide promises that the returned selection exports unchanged, so the
	// resolver runs export's own model validation on this read transaction
	// (review 2026-10-06, F74): duplicate view or saved-view pins, a selected
	// base below source schema 5 and every other closure rule used to pass
	// here with 200 and fail only after export had done its work under the
	// writer. One validator, not a copy of its rules; the cost is a read on a
	// reader connection, bounded by the same closure quotas as export.
	if _, err := s.exportModelTx(ctx, tx, *out); err != nil {
		return nil, err
	}
	return out, nil
}
