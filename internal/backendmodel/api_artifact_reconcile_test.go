package backendmodel

import (
	"encoding/json/v2"
	"slices"
	"sync"
	"testing"
)

func TestAPIArtifactReimportCarriesPinsAndComparison(t *testing.T) {
	t.Parallel()
	s, base, ids, api := apiPinFixture(t)
	pinned, _ := applyPinTest(t, s, base.Project.ID, pinTestInput(base, ids, api), "pin")
	comparison, err := s.repo.CompareRevisions(t.Context(), base.Project.ID, CompareRevisionsInput{FromRevisionID: base.Revision.ID, ToRevisionID: pinned.Revision.ID, RecordType: "artifact"})
	if err != nil {
		t.Fatalf("artifact category missing: %v", err)
	}
	raw, _ := json.Marshal(comparison)
	var generic map[string]any
	json.Unmarshal(raw, &generic)
	if len(comparison.Items) != 1 || comparison.Summary.Nodes != (ComparisonCounts{}) || comparison.Summary.Edges != (ComparisonCounts{}) || comparison.Summary.Evidence != (ComparisonCounts{}) {
		t.Fatalf("pin graph delta %+v", comparison)
	}
	item := generic["items"].([]any)[0].(map[string]any)
	if item["artifactAfter"] == nil || item["recordType"] != "artifact" {
		t.Fatalf("no typed frozen artifact side %s", raw)
	}
	in := lineageOrdersInput(t, &pinned.Project)
	for i := range in.Inventory {
		if in.Inventory[i].Category == "endpoints" {
			in.Inventory[i].Status = "partial"
			in.Inventory[i].Denominator = nil
			in.Inventory[i].Gaps = []string{"Source operation was not reobserved"}
		}
	}
	in.Mode = "reconcile"
	in.RepositoryID = new(base.Project.Repositories[0].ID)
	in.GraphScope = &GraphScope{Profile: LineageProfile, Status: "partial", Gaps: []string{"Partial observation"}}
	in.IdempotencyKey = "reimport"
	session, err := s.repo.BeginImport(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	commands := lineageOrdersCommands(t, session)
	// Deliberately leave the source HTTP operation unobserved: its pin association
	// must survive as source freshness becomes stale.
	commands = slices.DeleteFunc(commands, func(c ImportCommand) bool {
		return c.Node != nil && c.Node.ExternalKey == "http" || c.Evidence != nil && c.Evidence.SubjectKey == "http"
	})
	preview, _ := stageRelational(t, s.repo, &pinned.Project, session, commands, "reimport")
	if preview.State != "ready" {
		t.Fatal(preview.Diagnostics)
	}
	// No owner dependency is available during source commit.
	s.artifacts = nil
	out, err := commitFixture(t, s.repo, &pinned.Project, session, preview, "reimport-commit")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(out.Revision.ArtifactPins, pinned.Revision.ArtifactPins) {
		t.Fatalf("reimport cleared pins %+v", out.Revision)
	}
	page, err := s.Query(t.Context(), base.Project.ID, APIArtifactQueryInput{RevisionID: out.Revision.ID})
	if err != nil || len(page.Items) != 1 || page.Items[0].Binding.SourceNodeID != ids["http"] {
		t.Fatalf("frozen binding lost %+v %v", page, err)
	}
	node, err := s.repo.Node(t.Context(), base.Project.ID, out.Revision.ID, ids["http"])
	if err != nil || node.Freshness == nil || node.Freshness.Status != "stale" {
		t.Fatalf("pin refreshed source %+v %v", node, err)
	}
	remove := PreviewAPIPinsInput{BaseRevisionID: out.Revision.ID, ExpectedVersion: out.Project.Version, Commands: []APIPinCommand{{Type: "remove_api_pin", ArtifactID: pinned.Revision.ArtifactPins[0].ID, Reason: "Detach after source import"}}}
	cleared, _ := applyPinTest(t, s, base.Project.ID, remove, "clear")
	if cleared.Revision.SemanticHash == base.Revision.SemanticHash || cleared.Revision.SemanticHash == out.Revision.SemanticHash {
		t.Fatal("reimport anchors reused an old source semantic hash")
	}
	legacy, err := s.repo.CompareRevisions(t.Context(), base.Project.ID, CompareRevisionsInput{FromRevisionID: base.Revision.ID, ToRevisionID: base.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	legacyJSON, _ := json.Marshal(legacy)
	var legacyMap map[string]any
	json.Unmarshal(legacyJSON, &legacyMap)
	if _, ok := legacyMap["summary"].(map[string]any)["artifacts"]; ok {
		t.Fatal("empty legacy comparison added artifact field")
	}
}

func TestAPIArtifactConcurrentImportAndPinWriters(t *testing.T) {
	s, base, ids, api := apiPinFixture(t)
	importInput := lineageOrdersInput(t, &base.Project)
	importInput.Mode = "reconcile"
	importInput.RepositoryID = new(base.Project.Repositories[0].ID)
	importInput.GraphScope = &GraphScope{Profile: LineageProfile, Status: "partial", Gaps: []string{"New observation"}}
	importInput.IdempotencyKey = "concurrent-import"
	session, err := s.repo.BeginImport(t.Context(), base.Project.ID, importInput)
	if err != nil {
		t.Fatal(err)
	}
	importPreview, _ := stageRelational(t, s.repo, &base.Project, session, lineageOrdersCommands(t, session), "import-batch")
	if importPreview.State != "ready" {
		t.Fatal(importPreview.Diagnostics)
	}
	in := pinTestInput(base, ids, api)
	preview, err := s.Preview(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	var wg sync.WaitGroup
	wg.Go(func() {
		_, err := s.Apply(t.Context(), base.Project.ID, ApplyAPIPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands, preview.CandidateHash, "concurrent-pin"})
		results <- err
	})
	wg.Go(func() {
		_, err := s.repo.CommitImport(t.Context(), base.Project.ID, session.ID, CommitImportInput{ExpectedVersion: base.Project.Version, ExpectedImportVersion: importPreview.Version, CandidateHash: *importPreview.CandidateHash, IdempotencyKey: "import-commit"})
		results <- err
	})
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			fault := assertFault(t, err, "backend_version_conflict")
			if fault.CurrentVersion != base.Project.Version+1 {
				t.Fatalf("wrong current version %+v", fault)
			}
		}
	}
	current, err := s.repo.Get(t.Context(), base.Project.ID)
	if err != nil || success != 1 || current.Version != base.Project.Version+1 {
		t.Fatalf("competing writers success%d %+v %v", success, current, err)
	}
}

func TestAPIArtifactReimportWithoutComparisonStillCarriesBindings(t *testing.T) {
	t.Parallel()
	s, base, ids, api := apiPinFixture(t)
	pinned, _ := applyPinTest(t, s, base.Project.ID, pinTestInput(base, ids, api), "pin")
	in := lineageOrdersInput(t, &pinned.Project)
	in.Mode = "reconcile"
	in.RepositoryID = new(base.Project.Repositories[0].ID)
	in.GraphScope = &GraphScope{Profile: LineageProfile, Status: "partial", Gaps: []string{"Partial observation"}}
	in.IdempotencyKey = "reimport"
	session, err := s.repo.BeginImport(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	preview, _ := stageRelational(t, s.repo, &pinned.Project, session, lineageOrdersCommands(t, session), "reimport")
	if preview.State != "ready" {
		t.Fatal(preview.Diagnostics)
	}
	out, err := commitFixture(t, s.repo, &pinned.Project, session, preview, "commit-next")
	if err != nil {
		t.Fatal(err)
	}
	if len(out.Revision.ArtifactPins) != 1 {
		t.Fatal("source commit dropped exact base pins")
	}
	page, err := s.Query(t.Context(), base.Project.ID, APIArtifactQueryInput{RevisionID: out.Revision.ID})
	if err != nil || len(page.Items) != 1 || page.Items[0].Resolution.Status != "resolved" {
		t.Fatalf("carried query %+v %v", page, err)
	}
}

func TestAPIArtifactHistoricalProposalSavedViewAndFlowPins(t *testing.T) {
	t.Parallel()
	s, base, ids, api := apiPinFixture(t)
	pinned, _ := applyPinTest(t, s, base.Project.ID, pinTestInput(base, ids, api), "pin")
	source := &ImportCommitResult{Project: pinned.Project, Revision: pinned.Revision}
	proposal, err := s.repo.CreateProposal(t.Context(), base.Project.ID, proposalCreateInput(source, ids, "proposal"))
	if err != nil {
		t.Fatal(err)
	}
	view, err := s.repo.CreateSavedView(t.Context(), base.Project.ID, savedDatabaseInput(source, ids, "view"))
	if err != nil {
		t.Fatal(err)
	}
	remove := PreviewAPIPinsInput{BaseRevisionID: pinned.Revision.ID, ExpectedVersion: pinned.Project.Version, Commands: []APIPinCommand{{Type: "remove_api_pin", ArtifactID: pinned.Revision.ArtifactPins[0].ID, Reason: "Current detach"}}}
	cleared, _ := applyPinTest(t, s, base.Project.ID, remove, "clear")
	if len(cleared.Revision.ArtifactPins) != 0 {
		t.Fatal("head pin not cleared")
	}
	saved, err := s.repo.GetSavedView(t.Context(), base.Project.ID, view.ID, GetSavedViewInput{})
	if err != nil {
		t.Fatal(err)
	}
	if saved.Target.RevisionID != pinned.Revision.ID {
		t.Fatal("saved view floated to current head")
	}
	page, err := s.Query(t.Context(), base.Project.ID, APIArtifactQueryInput{RevisionID: proposal.Proposal.BaseRevisionID})
	if err != nil || len(page.Items) != 1 || page.Pins[0].RevisionID != pinned.Revision.ArtifactPins[0].RevisionID {
		t.Fatalf("proposal base lost binding %+v %v", page, err)
	}
	flow, err := s.repo.QueryFlow(t.Context(), base.Project.ID, FlowQueryInput{RevisionID: pinned.Revision.ID, View: "steps", FlowID: ids["flow"]})
	if err != nil || len(flow.StepItems) != 1 {
		t.Fatalf("old Flow unreadable %+v %v", flow, err)
	}
}
