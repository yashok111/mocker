package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/config"
)

func applyPinTest(t *testing.T, s *APIArtifactService, pid string, in PreviewAPIPinsInput, key string) (*APIPinsResult, ApplyAPIPinsInput) {
	t.Helper()
	preview, err := s.Preview(t.Context(), pid, in)
	if err != nil {
		t.Fatal(err)
	}
	if !preview.CanApply {
		t.Fatalf("blocked %+v", preview)
	}
	apply := ApplyAPIPinsInput{BaseRevisionID: in.BaseRevisionID, ExpectedVersion: in.ExpectedVersion, Commands: in.Commands, CandidateHash: preview.CandidateHash, IdempotencyKey: key}
	result, err := s.Apply(t.Context(), pid, apply)
	if err != nil {
		t.Fatal(err)
	}
	return result, apply
}

func TestAPIArtifactApplyCopiesAndReplaysBeforeDependencies(t *testing.T) {
	s, base, ids, api := apiPinFixture(t)
	in := pinTestInput(base, ids, api)
	for _, table := range []string{"backend_graph_records", "backend_revision_sources"} {
		if _, err := s.repo.db.W.ExecContext(t.Context(), `UPDATE `+table+` SET document=char(10)||'  '||document||char(10) WHERE revision_id=?`, base.Revision.ID); err != nil {
			t.Fatal(err)
		}
	}
	// An absent decision row must remain absent rather than becoming an empty bundle.
	if _, err := s.repo.db.W.ExecContext(t.Context(), `DELETE FROM backend_revision_decisions WHERE revision_id=?`, base.Revision.ID); err != nil {
		t.Fatal(err)
	}
	first, request := applyPinTest(t, s, base.Project.ID, in, "pin")
	if first.Project.Version != base.Project.Version+1 || first.Revision.ParentRevisionID == nil || *first.Revision.ParentRevisionID != base.Revision.ID || first.Revision.SchemaVersion != "4" {
		t.Fatalf("append %+v", first)
	}
	var unequal, count int
	err := s.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_graph_records a JOIN backend_graph_records b ON b.project_id=a.project_id AND b.record_type=a.record_type AND b.id=a.id WHERE a.revision_id=? AND b.revision_id=? AND (a.document IS NOT b.document OR a.kind IS NOT b.kind OR a.name IS NOT b.name OR a.parent_id IS NOT b.parent_id OR a.from_id IS NOT b.from_id OR a.to_id IS NOT b.to_id OR a.subject_id IS NOT b.subject_id)`, base.Revision.ID, first.Revision.ID).Scan(&unequal)
	if err != nil || unequal != 0 {
		t.Fatalf("raw rows differed %d %v", unequal, err)
	}
	var countBefore, countAfter int
	s.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_graph_records WHERE revision_id=?`, base.Revision.ID).Scan(&countBefore)
	s.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_graph_records WHERE revision_id=?`, first.Revision.ID).Scan(&countAfter)
	if countBefore == 0 || countBefore != countAfter {
		t.Fatal("raw copy lost graph records")
	}
	if err = s.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_revision_decisions WHERE revision_id=?`, first.Revision.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("absent decisions materialized %d %v", count, err)
	}
	var sourceBefore, sourceAfter string
	s.repo.db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_revision_sources WHERE revision_id=?`, base.Revision.ID).Scan(&sourceBefore)
	s.repo.db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_revision_sources WHERE revision_id=?`, first.Revision.ID).Scan(&sourceAfter)
	if sourceBefore != sourceAfter {
		t.Fatal("source bytes changed")
	}
	if _, err = s.repo.Apply(t.Context(), base.Project.ID, renameInput(first.Project.Version, "advance", "Advanced")); err != nil {
		t.Fatal(err)
	}
	unavailable := NewAPIArtifactService(s.repo, nil)
	replay, err := unavailable.Apply(t.Context(), base.Project.ID, request)
	if err != nil || string(replay.ReceiptBytes()) != string(first.ReceiptBytes()) || replay.Project.Version != first.Project.Version {
		t.Fatalf("lost response replay %+v %v", replay, err)
	}
	request.Commands = slices.Clone(request.Commands)
	request.Commands[0].Reason = "Different body"
	_, err = unavailable.Apply(t.Context(), base.Project.ID, request)
	assertFault(t, err, "backend_idempotency_conflict")
}

func TestAPIArtifactApplyABAClearAndContextOnlyChange(t *testing.T) {
	s, base, ids, api := apiPinFixture(t)
	a, _ := applyPinTest(t, s, base.Project.ID, pinTestInput(base, ids, api), "A")
	owner := s.artifacts.(*apidesign.Repo)
	b, err := owner.Save(t.Context(), api.Design.ID, apidesign.SaveInput{ExpectedVersion: api.Design.Version, Document: strings.Replace(artifactTestDocument, `"paths":`, `"servers":[{"url":"https://context.test"}],"paths":`, 1), Summary: "Context", Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	next := pinTestInput(base, ids, b)
	next.BaseRevisionID = a.Revision.ID
	next.ExpectedVersion = a.Project.Version
	preview, err := s.Preview(t.Context(), base.Project.ID, next)
	if err != nil || !preview.CanApply || len(preview.Diff) != 1 || !preview.Diff[0].ContextChanged || preview.Diff[0].Before.ObjectHash != preview.Diff[0].After.ObjectHash || preview.SemanticHash == a.Revision.SemanticHash {
		t.Fatalf("context-only %+v %v", preview, err)
	}
	changed, _ := applyPinTest(t, s, base.Project.ID, next, "B")
	next = pinTestInput(base, ids, api)
	next.BaseRevisionID = changed.Revision.ID
	next.ExpectedVersion = changed.Project.Version
	restored, _ := applyPinTest(t, s, base.Project.ID, next, "A-again")
	if restored.Revision.SemanticHash != a.Revision.SemanticHash {
		t.Fatal("A→B→A did not restore semantics")
	}
	remove := PreviewAPIPinsInput{BaseRevisionID: restored.Revision.ID, ExpectedVersion: restored.Project.Version, Commands: []APIPinCommand{{Type: "remove_api_pin", ArtifactID: strconv.FormatInt(api.Design.ID, 10), Reason: "Detach"}}}
	cleared, _ := applyPinTest(t, NewAPIArtifactService(s.repo, nil), base.Project.ID, remove, "clear")
	if cleared.Revision.SemanticHash != base.Revision.SemanticHash || len(cleared.Revision.ArtifactPins) != 0 {
		t.Fatal("clear lost source anchor")
	}
	old, err := s.Query(t.Context(), base.Project.ID, APIArtifactQueryInput{RevisionID: a.Revision.ID})
	if err != nil || len(old.Items) != 1 || old.Items[0].Binding.Ref.RevisionID != strconv.FormatInt(api.Draft.ID, 10) || !old.Items[0].Resolution.UpdateAvailable {
		t.Fatalf("historical query %+v %v", old, err)
	}
}

type changedArtifactDigest struct{ APIArtifactReader }

func (r changedArtifactDigest) ArtifactDigestTx(context.Context, *sql.Tx, int64, int64) (string, error) {
	return strings.Repeat("f", 64), nil
}

func TestAPIArtifactApplyRollbackDigestOverflowAndConcurrency(t *testing.T) {
	for _, failure := range []string{"digest", "receipt", "overflow"} {
		t.Run(failure, func(t *testing.T) {
			s, base, ids, api := apiPinFixture(t)
			in := pinTestInput(base, ids, api)
			if failure == "overflow" {
				s.repo.db.W.ExecContext(t.Context(), `UPDATE backend_projects SET version=? WHERE id=?`, int64(math.MaxInt64), base.Project.ID)
				in.ExpectedVersion = math.MaxInt64
			}
			preview, err := s.Preview(t.Context(), base.Project.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			if failure == "digest" {
				s.artifacts = changedArtifactDigest{s.artifacts}
			}
			if failure == "receipt" {
				if _, err = s.repo.db.W.ExecContext(t.Context(), `CREATE TRIGGER reject_pin_receipt BEFORE INSERT ON backend_command_receipts WHEN NEW.scope LIKE 'api-pins:%' BEGIN SELECT RAISE(ABORT,'injected rollback'); END`); err != nil {
					t.Fatal(err)
				}
			}
			_, err = s.Apply(t.Context(), base.Project.ID, ApplyAPIPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands, preview.CandidateHash, "failure"})
			if err == nil {
				t.Fatal("failed writer committed")
			}
			if failure == "digest" {
				assertFault(t, err, "backend_api_pins_hash_conflict")
			}
			if failure == "overflow" {
				assertFault(t, err, "backend_version_exhausted")
			}
			current, _ := s.repo.Get(t.Context(), base.Project.ID)
			var contexts, revisions, receipts int
			s.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_revision_api_artifacts`).Scan(&contexts)
			s.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_revisions WHERE project_id=?`, base.Project.ID).Scan(&revisions)
			s.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_command_receipts WHERE scope LIKE 'api-pins:%'`).Scan(&receipts)
			if current.CurrentRevisionID != base.Revision.ID || contexts != 0 || revisions != 2 || receipts != 0 {
				t.Fatalf("rollback leaked %+v contexts%d revisions%d receipts%d", current, contexts, revisions, receipts)
			}
		})
	}
	s, base, ids, api := apiPinFixture(t)
	in := pinTestInput(base, ids, api)
	preview, err := s.Preview(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, key := range []string{"one", "two"} {
		wg.Go(func() {
			_, err := s.Apply(t.Context(), base.Project.ID, ApplyAPIPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands, preview.CandidateHash, key})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	successful := 0
	for err := range results {
		if err == nil {
			successful++
		} else if _, ok := errors.AsType[*FaultError](err); !ok {
			t.Fatal(err)
		}
	}
	if successful != 1 {
		t.Fatalf("concurrent successful writers%d", successful)
	}
}

const artifactTestDocument = `{"openapi":"3.1.0","info":{"title":"Orders","version":"1"},"paths":{"/orders":{"get":{"x-mocker-canvas-operation-id":"orders-read","responses":{"200":{"description":"OK"}}}}},"components":{"schemas":{"Order":{"type":"object","properties":{"total":{"type":"number"},"secret":{"type":"string"}}}}}}`

func apiPinFixture(t *testing.T) (*APIArtifactService, *ImportCommitResult, map[string]string, *apidesign.Detail) {
	t.Helper()
	r, out, _, ids := lineageOrdersCommitted(t)
	owner := apidesign.NewRepo(r.db, &config.Config{MaxBody: 1 << 20})
	api, err := owner.Create(t.Context(), apidesign.CreateInput{Name: "Orders API", Document: artifactTestDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	return NewAPIArtifactService(r, owner), out, ids, api
}

func pinTestInput(out *ImportCommitResult, ids map[string]string, api *apidesign.Detail) PreviewAPIPinsInput {
	return PreviewAPIPinsInput{BaseRevisionID: out.Revision.ID, ExpectedVersion: out.Project.Version, Commands: []APIPinCommand{{Type: "set_api_pin", ArtifactID: strconv.FormatInt(api.Design.ID, 10), RevisionID: strconv.FormatInt(api.Draft.ID, 10), Reason: "Explicit association", Bindings: []APIPinBindingInput{{SourceNodeID: ids["http"], Selector: APIArtifactSelector{ObjectKey: "orders-read"}}}}}}
}

func TestAPIArtifactPreviewExactSource4AndLegacyQuery(t *testing.T) {
	s, out, ids, api := apiPinFixture(t)
	// Find the source operation from the fixture rather than guessing correspondence.
	state, err := loadRevisionState(t.Context(), s.repo.db.R, out.Project.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range state.Nodes {
		if n.Kind == "http_operation" {
			ids["http"] = n.ID
		}
	}
	in := pinTestInput(out, ids, api)
	before, _ := s.repo.Get(t.Context(), out.Project.ID)
	got, err := s.Preview(t.Context(), out.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if !got.CanApply || len(got.Bindings) != 1 || len(got.Pins) != 1 || len(got.Diff) != 1 || got.Diff[0].Status != "added" || got.Bindings[0].Ref.ResolvedPointer != "/paths/~1orders/get" {
		t.Fatalf("preview %+v", got)
	}
	again, err := s.Preview(t.Context(), out.Project.ID, in)
	if err != nil || again.CandidateHash != got.CandidateHash {
		t.Fatalf("nondeterministic %+v %v", again, err)
	}
	after, _ := s.repo.Get(t.Context(), out.Project.ID)
	if after.CurrentRevisionID != before.CurrentRevisionID || after.Version != before.Version {
		t.Fatal("preview wrote head")
	}
	page, err := s.Query(t.Context(), out.Project.ID, APIArtifactQueryInput{RevisionID: out.Revision.ID})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("legacy query %+v %v", page, err)
	}
	other := createProject(t, s.repo, "foreign")
	_, err = s.Query(t.Context(), other.ID, APIArtifactQueryInput{RevisionID: out.Revision.ID})
	assertFault(t, err, "backend_not_found")
	in.Commands[0].Bindings[0].Selector = APIArtifactSelector{JSONPointer: "/components/schemas/Order"}
	_, err = s.Preview(t.Context(), out.Project.ID, in)
	assertFault(t, err, "backend_invalid")
	raw, _ := json.Marshal(got)
	if len(raw) == 0 {
		t.Fatal("empty preview")
	}
}
