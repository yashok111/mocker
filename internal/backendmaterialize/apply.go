package backendmaterialize

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

	"github.com/yashok111/mocker/internal/backendblob"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

func (s *Service) Apply(ctx context.Context, pid string, in ApplyInput) (*Receipt, error) {
	if strings.TrimSpace(in.IdempotencyKey) == "" || len(in.IdempotencyKey) > 200 || !validHash(in.CandidateHash) {
		return nil, invalid("Idempotency key and candidate hash are required")
	}
	request := struct {
		Project   string
		Input     PreviewInput
		Candidate string
	}{pid, in.PreviewInput, in.CandidateHash}
	raw, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if len(raw) > MaxBytes+1024 {
		return nil, quota()
	}
	hash, err := digest(request)
	if err != nil {
		return nil, err
	}
	actor := actorFrom(ctx)
	var result *Receipt
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		prior, found, err := priorReceipt(ctx, tx, pid, in.IdempotencyKey, hash)
		if err != nil || found {
			result = prior
			return err
		}
		preview, err := s.previewTx(ctx, tx, pid, in.PreviewInput)
		if err != nil {
			return err
		}
		if !preview.CanApply {
			return invalid("Unsupported scope must be explicitly excluded")
		}
		if preview.CandidateHash != in.CandidateHash {
			return conflict("Candidate changed; preview again")
		}
		installation, err := s.models.InstallationIDTx(ctx, tx)
		if err != nil {
			return err
		}
		result = &Receipt{Author: actor.Name, ID: uuid.NewV7().String(), ProjectID: pid, CandidateHash: in.CandidateHash, RequestHash: hash, Owners: []OwnerResult{}, Coverage: preview.Coverage, Equivalence: preview.Equivalence}
		w := &ownerWriter{s: s, ctx: ctx, tx: tx, reason: in.Reason, actor: actor, installation: installation, result: result, apis: map[int64]*apidesign.Detail{}}
		if err = w.write(preview.Input.Targets); err != nil {
			return err
		}
		return s.recordApply(ctx, tx, pid, in, hash, preview, result)
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

// priorReceipt answers a repeated idempotency key from its stored receipt;
// the same key with a different request is a conflict.
func priorReceipt(ctx context.Context, tx *sql.Tx, pid, key, hash string) (*Receipt, bool, error) {
	var old, response string
	err := tx.QueryRowContext(ctx, `SELECT request_hash,response FROM backend_materialization_receipts WHERE project_id=? AND idempotency_key=?`, pid, key).Scan(&old, &response)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if old != hash {
		return nil, true, conflict("Idempotency key already used with a different request")
	}
	var result *Receipt
	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return result, true, err
	}
	result.raw = response
	return result, true, nil
}

// ownerWriter writes the planned owners: APIs first, then scenarios linked
// to exactly the API revisions just written.
type ownerWriter struct {
	s            *Service
	ctx          context.Context
	tx           *sql.Tx
	reason       string
	actor        Actor
	installation string
	result       *Receipt
	apis         map[int64]*apidesign.Detail
}

func (w *ownerWriter) write(targets []Target) error {
	for _, t := range targets {
		if t.Kind == "api_design" {
			if err := w.writeAPI(t); err != nil {
				return err
			}
		}
	}
	for _, t := range targets {
		if t.Kind == "design_scenario" {
			if err := w.writeScenario(t); err != nil {
				return err
			}
		}
	}
	// A scenario save must not have moved any API it links.
	for _, d := range w.apis {
		head, _, err := w.s.apis.DraftTx(w.ctx, w.tx, d.Design.ID)
		if err != nil {
			return err
		}
		if head.Version != d.Design.Version || head.DraftRevisionID != d.Draft.ID {
			return invalid("Scenario produced an unplanned API effect")
		}
	}
	for i := range w.result.Coverage {
		for _, o := range w.result.Owners {
			if w.result.Coverage[i].TargetKey == o.TargetKey {
				pin := o.Pin
				w.result.Coverage[i].TargetOwnerRef = &pin
			}
		}
	}
	return nil
}

func (w *ownerWriter) add(t Target, id, revision, version int64, hash string) {
	w.result.Owners = append(w.result.Owners, OwnerResult{TargetKey: t.Key, Version: version, Pin: backendmodel.NamespacedArtifactPin{Namespace: backendmodel.ArtifactNamespace{Scope: "local", InstallationID: w.installation}, Pin: backendmodel.ArtifactPin{Kind: t.Kind, ID: strconv.FormatInt(id, 10), RevisionID: strconv.FormatInt(revision, 10), ContentHash: hash}}})
}

func (w *ownerWriter) writeAPI(t Target) error {
	var d *apidesign.Detail
	var err error
	c := t.Commands[0]
	if t.Pin == nil {
		d, err = w.s.apis.CreateTx(w.ctx, w.tx, apidesign.CreateInput{Name: t.Name, Document: c.APIDocument, Source: w.actor.Source, OwnerID: w.actor.OwnerID})
	} else {
		d, err = w.s.apis.SaveTx(w.ctx, w.tx, ownerID(t.Pin.Pin.ID), apidesign.SaveInput{ExpectedVersion: t.ExpectedVersion, Document: c.APIDocument, Summary: w.reason, Source: w.actor.Source})
	}
	if err != nil {
		return err
	}
	w.apis[d.Design.ID] = d
	w.add(t, d.Design.ID, d.Draft.ID, d.Design.Version, d.Draft.Hash)
	if w.s.afterWrite != nil {
		return w.s.afterWrite("api")
	}
	return nil
}

func (w *ownerWriter) writeScenario(t Target) error {
	document := *t.Commands[0].Scenario
	// The struct copy shares Contracts with preview.Input; rewriting it in
	// place changed the stored Preview after candidateHash bound it
	// (review 2026-10-06, F130).
	document.Contracts = slices.Clone(document.Contracts)
	for i := range document.Contracts {
		c := &document.Contracts[i]
		d := w.apis[c.Source.DesignID]
		if d == nil {
			return invalid("Unplanned linked API write")
		}
		// Only the returned exact API revision can enter the scenario; SaveTx then
		// observes an identical pinned snapshot and cannot produce a hidden write.
		c.Source = &designscenario.ContractSource{DesignID: d.Design.ID, RevisionID: d.Draft.ID, Version: d.Draft.Version}
		c.Document = jsonx.RawMessage(d.Draft.Document)
	}
	var d *designscenario.Detail
	var err error
	if t.Pin == nil {
		d, err = w.s.scenarios.CreateTx(w.ctx, w.tx, designscenario.CreateInput{Document: document, Summary: w.reason, Source: w.actor.Source})
	} else {
		d, err = w.s.scenarios.SaveTx(w.ctx, w.tx, ownerID(t.Pin.Pin.ID), designscenario.SaveInput{ExpectedVersion: t.ExpectedVersion, Document: document, Summary: w.reason, Source: w.actor.Source})
	}
	if err != nil {
		return err
	}
	w.add(t, d.Scenario.ID, d.Draft.ID, d.Scenario.Version, d.Draft.Hash)
	if w.s.afterWrite != nil {
		return w.s.afterWrite("scenario")
	}
	return nil
}

// recordApply stores the materialization with its plan and receipt, and the
// receipt under the idempotency key.
func (s *Service) recordApply(ctx context.Context, tx *sql.Tx, pid string, in ApplyInput, hash string, preview *Preview, result *Receipt) error {
	document, err := json.Marshal(struct {
		Preview *Preview
		Receipt *Receipt
	}{preview, result})
	if err != nil {
		return err
	}
	normalized, err := json.Marshal(preview.Input)
	if err != nil {
		return err
	}
	count := 0
	for _, t := range preview.Input.Targets {
		count += len(t.Commands)
	}
	_, err = backendblob.Exec(ctx, tx, `INSERT INTO backend_materializations(project_id,id,profile_version,request_hash,candidate_hash,target_count,command_count,byte_count,document,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, pid, result.ID, ProfileVersion, hash, in.CandidateHash, len(preview.Input.Targets), count, len(normalized), string(document), time.Now().Unix())
	if err != nil {
		return err
	}
	receipt, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO backend_materialization_receipts(project_id,idempotency_key,materialization_id,request_hash,response) VALUES(?,?,?,?,?)`, pid, in.IdempotencyKey, result.ID, hash, string(receipt))
	return err
}
