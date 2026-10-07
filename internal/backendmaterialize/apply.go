package backendmaterialize

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"github.com/yashok111/mocker/internal/backendblob"
	"slices"
	"strconv"
	"strings"
	"time"
	"uuid"

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
		var old, response string
		err := tx.QueryRowContext(ctx, `SELECT request_hash,response FROM backend_materialization_receipts WHERE project_id=? AND idempotency_key=?`, pid, in.IdempotencyKey).Scan(&old, &response)
		if err == nil {
			if old != hash {
				return conflict("Idempotency key already used with a different request")
			}
			if err := json.Unmarshal([]byte(response), &result); err != nil {
				return err
			}
			result.raw = response
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
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
		apis := map[int64]*apidesign.Detail{}
		add := func(t Target, id, revision, version int64, hash string) {
			result.Owners = append(result.Owners, OwnerResult{TargetKey: t.Key, Version: version, Pin: backendmodel.NamespacedArtifactPin{Namespace: backendmodel.ArtifactNamespace{Scope: "local", InstallationID: installation}, Pin: backendmodel.ArtifactPin{Kind: t.Kind, ID: strconv.FormatInt(id, 10), RevisionID: strconv.FormatInt(revision, 10), ContentHash: hash}}})
		}
		for _, t := range preview.Input.Targets {
			if t.Kind != "api_design" {
				continue
			}
			var d *apidesign.Detail
			c := t.Commands[0]
			if t.Pin == nil {
				d, err = s.apis.CreateTx(ctx, tx, apidesign.CreateInput{Name: t.Name, Document: c.APIDocument, Source: actor.Source, OwnerID: actor.OwnerID})
			} else {
				d, err = s.apis.SaveTx(ctx, tx, ownerID(t.Pin.Pin.ID), apidesign.SaveInput{ExpectedVersion: t.ExpectedVersion, Document: c.APIDocument, Summary: in.Reason, Source: actor.Source})
			}
			if err != nil {
				return err
			}
			apis[d.Design.ID] = d
			add(t, d.Design.ID, d.Draft.ID, d.Design.Version, d.Draft.Hash)
			if s.afterWrite != nil {
				if err := s.afterWrite("api"); err != nil {
					return err
				}
			}
		}
		for _, t := range preview.Input.Targets {
			if t.Kind != "design_scenario" {
				continue
			}
			document := *t.Commands[0].Scenario
			// The struct copy shares Contracts with preview.Input; rewriting it in
			// place changed the stored Preview after candidateHash bound it
			// (review 2026-10-06, F130).
			document.Contracts = slices.Clone(document.Contracts)
			for i := range document.Contracts {
				c := &document.Contracts[i]
				d := apis[c.Source.DesignID]
				if d == nil {
					return invalid("Unplanned linked API write")
				}
				// Only the returned exact API revision can enter the scenario; SaveTx then
				// observes an identical pinned snapshot and cannot produce a hidden write.
				c.Source = &designscenario.ContractSource{DesignID: d.Design.ID, RevisionID: d.Draft.ID, Version: d.Draft.Version}
				c.Document = jsonx.RawMessage(d.Draft.Document)
			}
			var d *designscenario.Detail
			if t.Pin == nil {
				d, err = s.scenarios.CreateTx(ctx, tx, designscenario.CreateInput{Document: document, Summary: in.Reason, Source: actor.Source})
			} else {
				d, err = s.scenarios.SaveTx(ctx, tx, ownerID(t.Pin.Pin.ID), designscenario.SaveInput{ExpectedVersion: t.ExpectedVersion, Document: document, Summary: in.Reason, Source: actor.Source})
			}
			if err != nil {
				return err
			}
			add(t, d.Scenario.ID, d.Draft.ID, d.Scenario.Version, d.Draft.Hash)
			if s.afterWrite != nil {
				if err := s.afterWrite("scenario"); err != nil {
					return err
				}
			}
		}
		for _, d := range apis {
			head, _, err := s.apis.DraftTx(ctx, tx, d.Design.ID)
			if err != nil {
				return err
			}
			if head.Version != d.Design.Version || head.DraftRevisionID != d.Draft.ID {
				return invalid("Scenario produced an unplanned API effect")
			}
		}
		for i := range result.Coverage {
			for _, o := range result.Owners {
				if result.Coverage[i].TargetKey == o.TargetKey {
					pin := o.Pin
					result.Coverage[i].TargetOwnerRef = &pin
				}
			}
		}
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
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
