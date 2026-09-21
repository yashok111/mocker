package designscenario

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/store"
)

type Repo struct {
	db      *store.DB
	cfg     *config.Config
	designs *apidesign.Repo
}

func NewRepo(db *store.DB, cfg *config.Config, designs *apidesign.Repo) *Repo {
	return &Repo{db: db, cfg: cfg, designs: designs}
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type scanner interface{ Scan(...any) error }

const scenarioColumns = `id,name,version,draft_revision_id,created_at,updated_at`
const revisionColumns = `id,scenario_id,version,hash,source,summary,created_at,document,form_drafts`

func scanScenario(row scanner) (Scenario, error) {
	var out Scenario
	err := row.Scan(&out.ID, &out.Name, &out.Version, &out.DraftRevisionID, &out.CreatedAt, &out.UpdatedAt)
	return out, notFound(err)
}

func getScenario(ctx context.Context, q queryer, id int64) (Scenario, error) {
	return scanScenario(q.QueryRowContext(ctx, "SELECT "+scenarioColumns+" FROM design_scenarios WHERE id=?", id))
}

func scanRevision(row scanner) (Revision, error) {
	var (
		out                  Revision
		document, formDrafts string
	)
	err := row.Scan(&out.ID, &out.ScenarioID, &out.Version, &out.Hash, &out.Source, &out.Summary, &out.CreatedAt, &document, &formDrafts)
	if err != nil {
		return Revision{}, notFound(err)
	}
	if err = jsonx.Unmarshal([]byte(document), &out.Document); err != nil {
		return Revision{}, fmt.Errorf("decode design scenario document: %w", err)
	}
	if err = jsonx.Unmarshal([]byte(formDrafts), &out.FormDrafts); err != nil {
		return Revision{}, fmt.Errorf("decode design scenario form drafts: %w", err)
	}
	if out.FormDrafts == nil {
		out.FormDrafts = map[string]string{}
	}
	return out, nil
}

func getRevision(ctx context.Context, q queryer, scenarioID, revisionID int64) (Revision, error) {
	return scanRevision(q.QueryRowContext(ctx, "SELECT "+revisionColumns+" FROM design_scenario_revisions WHERE scenario_id=? AND id=?", scenarioID, revisionID))
}

func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func checkSource(source string) error {
	if source == "ui" || source == "mcp" {
		return nil
	}
	return &InvalidError{Diagnostics: []Diagnostic{{Pointer: "", Message: "invalid actor source", Severity: "error"}}}
}

func checkVersion(s Scenario, expected int64) error {
	if s.Version != expected {
		return &ConflictError{Version: s.Version, DraftRevisionID: s.DraftRevisionID}
	}
	return nil
}

type preparedDocument struct {
	document   string
	formDrafts string
	hash       string
}

func (r *Repo) prepare(document Document, formDrafts map[string]string) (preparedDocument, []Diagnostic, error) {
	diagnostics, err := r.validate(document, formDrafts)
	if err != nil {
		return preparedDocument{}, diagnostics, err
	}
	if formDrafts == nil {
		formDrafts = map[string]string{}
	}
	documentJSON, err := jsonx.Marshal(document)
	if err != nil {
		return preparedDocument{}, nil, fmt.Errorf("encode design scenario document: %w", err)
	}
	draftsJSON, err := jsonx.Marshal(formDrafts)
	if err != nil {
		return preparedDocument{}, nil, fmt.Errorf("encode design scenario form drafts: %w", err)
	}
	if r.cfg.MaxBody > 0 && int64(len(documentJSON)+len(draftsJSON)) > r.cfg.MaxBody {
		return preparedDocument{}, nil, ErrTooLarge
	}
	envelope, err := jsonx.Marshal(struct {
		Document   Document          `json:"document"`
		FormDrafts map[string]string `json:"formDrafts"`
	}{Document: document, FormDrafts: formDrafts})
	if err != nil {
		return preparedDocument{}, nil, fmt.Errorf("encode design scenario hash input: %w", err)
	}
	sum := sha256.Sum256(envelope)
	return preparedDocument{document: string(documentJSON), formDrafts: string(draftsJSON), hash: hex.EncodeToString(sum[:])}, diagnostics, nil
}

func cloneDocument(document Document) (Document, error) {
	raw, err := jsonx.Marshal(document)
	if err != nil {
		return Document{}, err
	}
	var clone Document
	if err = jsonx.Unmarshal(raw, &clone); err != nil {
		return Document{}, err
	}
	return clone, nil
}

func decodeJSONValue(raw []byte) (any, error) {
	decoder := jsonx.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	return value, nil
}
