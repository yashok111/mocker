package backendmodel

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"github.com/yashok111/mocker/internal/testkit"
	"strings"
	"sync"
	"testing"
)

func applyArtifactTest(t *testing.T, s *ArtifactService, pid string, in PreviewArtifactPinsInput, key string) (*ArtifactPinsResult, ApplyArtifactPinsInput) {
	t.Helper()
	p, err := s.Preview(t.Context(), pid, in)
	if err != nil {
		t.Fatal(err)
	}
	if !p.CanApply {
		t.Fatalf("blocked %+v", p)
	}
	request := ApplyArtifactPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands, p.CandidateHash, key}
	out, err := s.Apply(t.Context(), pid, request)
	if err != nil {
		t.Fatal(err)
	}
	return out, request
}
func TestArtifactApplyRawCopyReceiptAndClear(t *testing.T) {
	t.Parallel()
	s, base, ids, d := artifactServiceFixture(t)
	if err := testkit.EditLegacyBackendFixture(t.Context(), s.repo.db, func(tx *sql.Tx) error {
		for _, table := range []string{"backend_graph_records", "backend_revision_sources", "backend_revision_decisions"} {
			if _, err := tx.ExecContext(t.Context(), `UPDATE `+table+` SET document=char(10)||'  '||document||char(10) WHERE revision_id=?`, base.Revision.ID); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	ownerRows := artifactOwnerRows(t, s.repo)
	before, err := s.scenarios.ArtifactInspectionSnapshot(t.Context(), d.Scenario.ID, d.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	a, request := applyArtifactTest(t, s, base.Project.ID, scenarioSet(base, ids, d), "generic")
	if a.Project.Version != base.Project.Version+1 || a.Revision.ParentRevisionID == nil || *a.Revision.ParentRevisionID != base.Revision.ID || a.Revision.SchemaVersion != "4" {
		t.Fatal(a)
	}
	var unequal int
	err = s.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_graph_records_documents a JOIN backend_graph_records_documents b ON b.project_id=a.project_id AND b.record_type=a.record_type AND b.id=a.id WHERE a.revision_id=? AND b.revision_id=? AND (a.document IS NOT b.document OR a.kind IS NOT b.kind OR a.name IS NOT b.name OR a.parent_id IS NOT b.parent_id OR a.from_id IS NOT b.from_id OR a.to_id IS NOT b.to_id OR a.subject_id IS NOT b.subject_id)`, base.Revision.ID, a.Revision.ID).Scan(&unequal)
	if err != nil || unequal != 0 {
		t.Fatalf("raw graph copies: %d %v", unequal, err)
	}
	for _, table := range []string{"backend_revision_sources", "backend_revision_decisions"} {
		var left, right string
		if err = s.repo.db.R.QueryRowContext(t.Context(), `SELECT document FROM `+table+`_documents WHERE revision_id=?`, base.Revision.ID).Scan(&left); err != nil {
			t.Fatal(err)
		}
		if err = s.repo.db.R.QueryRowContext(t.Context(), `SELECT document FROM `+table+`_documents WHERE revision_id=?`, a.Revision.ID).Scan(&right); err != nil {
			t.Fatal(err)
		}
		if left != right {
			t.Fatal("source/decision bytes changed")
		}
	}
	if !bytes.Equal(ownerRows, artifactOwnerRows(t, s.repo)) {
		t.Fatal("backend pin command wrote owner SQL rows/head/revisions")
	}
	after, err := s.scenarios.ArtifactInspectionSnapshot(t.Context(), d.Scenario.ID, d.Draft.ID)
	if err != nil || before.DocumentJSON != after.DocumentJSON || before.FormDraftsJSON != after.FormDraftsJSON || before.ContentHash != after.ContentHash {
		t.Fatal("owner mutated", err)
	}
	state, err := loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, a.Revision.ID)
	if err != nil || state.ArtifactContext.DocumentVersion != EditorArtifactDocumentVersion || len(state.ArtifactContext.EditorBindings) != 2 {
		t.Fatalf("persisted context: %+v %v", state, err)
	}
	remove := PreviewArtifactPinsInput{BaseRevisionID: a.Revision.ID, ExpectedVersion: a.Project.Version, Commands: []ArtifactPinCommand{{Type: "remove_artifact_pin", Artifact: artifactKey(a.Revision.ArtifactPins[0]), Reason: "Explicit whole group removal"}}}
	unavailable := NewArtifactService(s.repo, nil, nil)
	p, err := unavailable.Preview(t.Context(), base.Project.ID, remove)
	if err != nil || !p.CanApply || len(p.Diff) != 3 {
		t.Fatalf("broken removal: %+v %v", p, err)
	}
	cleared, _ := applyArtifactTest(t, unavailable, base.Project.ID, remove, "remove")
	if cleared.Revision.SemanticHash != base.Revision.SemanticHash || len(cleared.Revision.ArtifactPins) != 0 {
		t.Fatal("full removal lost source anchor")
	}
	replay, err := unavailable.Apply(t.Context(), base.Project.ID, request)
	if err != nil || !bytes.Equal(replay.ReceiptBytes(), a.ReceiptBytes()) {
		t.Fatal("receipt changed", err)
	}
	// Replay is independent of project and owners, including a fresh service instance.
	// Isolated corruption fixture: bypass FK admission solely to prove receipt-first reads.
	if _, err = s.repo.db.W.ExecContext(t.Context(), `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.repo.db.W.ExecContext(t.Context(), `DELETE FROM backend_projects WHERE id=?`, base.Project.ID); err != nil {
		t.Fatal(err)
	}
	replay, err = NewArtifactService(s.repo, nil, nil).Apply(t.Context(), base.Project.ID, request)
	if err != nil || !bytes.Equal(replay.ReceiptBytes(), a.ReceiptBytes()) {
		t.Fatal("receipt required project", err)
	}
	request.Commands[0].Reason = "Different exact request"
	_, err = unavailable.Apply(t.Context(), base.Project.ID, request)
	assertFault(t, err, "backend_idempotency_conflict")
}

type artifactScenarioDigestChanged struct{ ScenarioArtifactReader }

func (r artifactScenarioDigestChanged) ArtifactDigestTx(context.Context, *sql.Tx, int64, int64) (string, error) {
	return strings.Repeat("f", 64), nil
}
func TestArtifactApplyCASRollbackAndOneWinner(t *testing.T) {
	for _, failure := range []string{"digest", "receipt"} {
		t.Run(failure, func(t *testing.T) {
			s, base, ids, d := artifactServiceFixture(t)
			in := scenarioSet(base, ids, d)
			p, err := s.Preview(t.Context(), base.Project.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "digest" {
				s.scenarios = artifactScenarioDigestChanged{s.scenarios}
			}
			if failure == "receipt" {
				if _, err = s.repo.db.W.ExecContext(t.Context(), `CREATE TRIGGER reject_artifact_receipt BEFORE INSERT ON backend_command_receipts WHEN NEW.scope LIKE 'artifact-pins:%' BEGIN SELECT RAISE(ABORT,'injected rollback'); END`); err != nil {
					t.Fatal(err)
				}
			}
			_, err = s.Apply(t.Context(), base.Project.ID, ApplyArtifactPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands, p.CandidateHash, "failure"})
			if err == nil {
				t.Fatal("failed writer committed")
			}
			current, _ := s.repo.Get(t.Context(), base.Project.ID)
			if current.Version != base.Project.Version || current.CurrentRevisionID != base.Revision.ID {
				t.Fatal("failed writer advanced head")
			}
			var receipts int
			s.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_command_receipts WHERE scope LIKE 'artifact-pins:%'`).Scan(&receipts)
			if receipts != 0 {
				t.Fatal("failed writer saved receipt")
			}
		})
	}
	s, base, ids, d := artifactServiceFixture(t)
	in := scenarioSet(base, ids, d)
	p, err := s.Preview(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, key := range []string{"winner1", "winner2"} {
		wg.Go(func() {
			_, e := s.Apply(t.Context(), base.Project.ID, ApplyArtifactPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands, p.CandidateHash, key})
			results <- e
		})
	}
	wg.Wait()
	close(results)
	count := 0
	for e := range results {
		if e == nil {
			count++
		} else if _, ok := errors.AsType[*FaultError](e); !ok {
			t.Fatal(e)
		}
	}
	if count != 1 {
		t.Fatalf("winners: %d", count)
	}
	var contexts int
	s.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_revision_api_artifacts_documents`).Scan(&contexts)
	if contexts != 1 {
		t.Fatalf("partial loser context: %d", contexts)
	}
}

// A changed request body after preview must conflict without a persisted successor.
func TestArtifactApplyCandidateReasonAndScope(t *testing.T) {
	t.Parallel()
	s, base, ids, d := artifactServiceFixture(t)
	in := scenarioSet(base, ids, d)
	p, err := s.Preview(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	in.Commands[0].Reason = "Changed reason"
	_, err = s.Apply(t.Context(), base.Project.ID, ApplyArtifactPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands, p.CandidateHash, "same-key"})
	assertFault(t, err, "backend_artifact_pins_hash_conflict")
	var raw string
	s.repo.db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_revisions_documents WHERE id=?`, base.Revision.ID).Scan(&raw)
	var rev Revision
	if err = json.Unmarshal([]byte(raw), &rev); err != nil {
		t.Fatal(err)
	}
	if len(rev.ArtifactPins) != 0 {
		t.Fatal("hash conflict wrote")
	}
}

func artifactOwnerRows(t *testing.T, repo *Repo) []byte {
	t.Helper()
	saved := map[string][][]any{}
	for _, table := range []string{"api_designs", "api_design_revisions", "design_scenarios", "design_scenario_revisions"} {
		func() {
			rows, err := repo.db.R.QueryContext(t.Context(), `SELECT * FROM `+table+` ORDER BY id`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			columns, err := rows.Columns()
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			saved[table] = [][]any{}
			for rows.Next() {
				values := make([]any, len(columns))
				dest := make([]any, len(columns))
				for i := range values {
					dest[i] = &values[i]
				}
				if err = rows.Scan(dest...); err != nil {
					rows.Close()
					t.Fatal(err)
				}
				saved[table] = append(saved[table], values)
			}
			if err = rows.Err(); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			rows.Close()
		}()
	}
	raw, err := json.Marshal(saved, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestArtifactApplyKeepsAbsentDecisionRow(t *testing.T) {
	t.Parallel()
	s, base, ids, d := artifactServiceFixture(t)
	if _, err := testkit.EditLegacyBackendPayload(t.Context(), s.repo.db, `DELETE FROM backend_revision_decisions WHERE revision_id=?`, base.Revision.ID); err != nil {
		t.Fatal(err)
	}
	result, _ := applyArtifactTest(t, s, base.Project.ID, scenarioSet(base, ids, d), "absent-decision")
	var count int
	if err := s.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_revision_decisions_documents WHERE revision_id=?`, result.Revision.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("raw copy materialized absent decision: %d %v", count, err)
	}
}
