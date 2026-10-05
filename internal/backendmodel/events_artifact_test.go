package backendmodel

import (
	"encoding/json/v2"
	"slices"
	"testing"
)

func upgradeEventsArtifactFixture(t *testing.T, s *ArtifactService, base *ImportCommitResult) (*ImportCommitResult, *ImportSession) {
	t.Helper()
	input := eventsProfileInput(lineageOrdersInput(t, &base.Project), true)
	input.Mode = "reconcile"
	input.RepositoryID = new(base.Project.Repositories[0].ID)
	input.GraphScope = &GraphScope{Profile: EventsProfile, Status: "complete", Gaps: []string{}}
	input.IdempotencyKey = "events-upgrade"
	session, err := s.repo.BeginImport(t.Context(), base.Project.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	v, _ := stageRelational(t, s.repo, &base.Project, session, lineageOrdersCommands(t, session), "events-upgrade")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, s.repo, &base.Project, session, v, "events-upgrade-commit")
	if err != nil {
		t.Fatal(err)
	}
	return out, session
}
func TestEventsArtifactMutationGatesAndReplay(t *testing.T) {
	t.Parallel()
	s, base, ids, d := artifactServiceFixture(t)
	up, _ := upgradeEventsArtifactFixture(t, s, base)
	// Source5 admits both generic preview/apply and legacy API preview/apply.
	in := scenarioSet(up, ids, d)
	pinned, apply := applyArtifactTest(t, s, base.Project.ID, in, "events-scenario")
	replay, err := s.Apply(t.Context(), base.Project.ID, apply)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(pinned)
	after, _ := json.Marshal(replay)
	if string(before) != string(after) {
		t.Fatal("generic receipt drift")
	}
	legacy := NewAPIArtifactService(s.repo, s.api)
	apiIn := PreviewAPIPinsInput{BaseRevisionID: pinned.Revision.ID, ExpectedVersion: pinned.Project.Version, Commands: []APIPinCommand{{Type: "set_api_pin", ArtifactID: "1", RevisionID: "1", Bindings: []APIPinBindingInput{{SourceNodeID: ids["http"], Selector: APIArtifactSelector{ObjectKey: "orders-read"}}}, Reason: "Exact source5 pin"}}}
	next, legacyApply := applyPinTest(t, legacy, base.Project.ID, apiIn, "events-api")
	legacyReplay, err := legacy.Apply(t.Context(), base.Project.ID, legacyApply)
	if err != nil {
		t.Fatal(err)
	}
	before, _ = json.Marshal(next)
	after, _ = json.Marshal(legacyReplay)
	if string(before) != string(after) {
		t.Fatal("API receipt drift")
	}
	if next.Revision.SchemaVersion != "5" || len(next.Revision.ArtifactPins) != 2 {
		t.Fatal("source5 context successor drift")
	}
}
func TestEventsUpgradeCarriesFrozenV2AndInheritedLineage(t *testing.T) {
	t.Parallel()
	s, base, ids, d := artifactServiceFixture(t)
	pinned, _ := applyArtifactTest(t, s, base.Project.ID, scenarioSet(base, ids, d), "scenario-before-upgrade")
	old, err := loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, pinned.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.api = nil
	s.scenarios = nil
	up, _ := upgradeEventsArtifactFixture(t, s, &ImportCommitResult{Project: pinned.Project, Revision: pinned.Revision})
	state, err := loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, up.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(old.Revision.ArtifactPins, state.Revision.ArtifactPins) {
		t.Fatal("pin vector changed")
	}
	before, _ := json.Marshal(old.ArtifactContext.EditorBindings)
	after, _ := json.Marshal(state.ArtifactContext.EditorBindings)
	if string(before) != string(after) {
		t.Fatal("frozen labels/roster changed")
	}
	// Ordinary field mappings retain their exact strict4 shape and ownership on source5.
	mappings := 0
	for _, n := range state.Nodes {
		if n.Kind == "field_mapping" {
			mappings++
			if n.Ownership.Profile != LineageProfile {
				t.Fatal("ordinary mapping owner drift")
			}
			if _, err := decodeLineageMapping(n.Attributes); err != nil {
				t.Fatal(err)
			}
		}
	}
	if mappings == 0 {
		t.Fatal("inherited mappings absent")
	}
}
func TestEventsDatabaseProposalAndSavedViewEligibility(t *testing.T) {
	t.Parallel()
	s, base, ids, _ := artifactServiceFixture(t)
	up, _ := upgradeEventsArtifactFixture(t, s, base)
	state, err := loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, up.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	datastore := ""
	for _, n := range state.Nodes {
		if n.Kind == "datastore" {
			datastore = n.ID
			break
		}
	}
	page, err := s.repo.QueryDatabase(t.Context(), base.Project.ID, DatabaseQueryInput{RevisionID: up.Revision.ID, DatastoreID: datastore, FacetKey: "sql", RecordType: "tables"})
	if err != nil || len(page.TableItems) == 0 {
		t.Fatal(page, err)
	}
	proposal, err := s.repo.CreateProposal(t.Context(), base.Project.ID, CreateProposalInput{Name: "Events source proposal", BaseRevisionID: up.Revision.ID, RepositoryID: up.Project.Repositories[0].ID, DatastoreID: datastore, FacetKey: "sql", IdempotencyKey: "events-proposal"})
	if err != nil || proposal.Proposal.BaseRevisionID != up.Revision.ID {
		t.Fatal(proposal, err)
	}
	saved, err := s.repo.CreateSavedView(t.Context(), base.Project.ID, CreateSavedViewInput{Name: "Events database", Target: BackendReadTarget{RevisionID: up.Revision.ID}, State: SavedViewState{Database: &SavedDatabaseViewState{Kind: "database", Scope: SavedDatabaseViewScope{DatastoreID: datastore, FacetKey: "sql"}, Filters: SavedDatabaseViewFilters{Search: ""}, Positions: []SavedViewPosition{}, CollapsedGroupIDs: []string{}}}, IdempotencyKey: "events-saved"})
	if err != nil || saved.Pins.RevisionID != up.Revision.ID {
		t.Fatal(saved, err)
	}
	if ids["http"] == "" {
		t.Fatal("inherited source missing")
	}
}
func TestEventsUpgradeCarriesFrozenV1(t *testing.T) {
	t.Parallel()
	s, base, ids, _ := artifactServiceFixture(t)
	legacy := NewAPIArtifactService(s.repo, s.api)
	in := PreviewAPIPinsInput{BaseRevisionID: base.Revision.ID, ExpectedVersion: base.Project.Version, Commands: []APIPinCommand{{Type: "set_api_pin", ArtifactID: "1", RevisionID: "1", Bindings: []APIPinBindingInput{{SourceNodeID: ids["http"], Selector: APIArtifactSelector{ObjectKey: "orders-read"}}}, Reason: "Source4 API association"}}}
	pinned, receipt := applyPinTest(t, legacy, base.Project.ID, in, "before-upgrade")
	old, err := loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, pinned.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(old.APIArtifactContext.Bindings)
	s.api = nil
	s.scenarios = nil
	up, _ := upgradeEventsArtifactFixture(t, s, &ImportCommitResult{Project: pinned.Project, Revision: pinned.Revision})
	state, err := loadRevisionState(t.Context(), s.repo.db.R, base.Project.ID, up.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(state.APIArtifactContext.Bindings)
	if string(before) != string(after) || !slices.Equal(old.Revision.ArtifactPins, state.Revision.ArtifactPins) || state.ArtifactContext == nil || !ArtifactContextUsesV1(state.Revision.ArtifactPins, *state.ArtifactContext) {
		t.Fatal("v1 labels/vector/context branch drift")
	}
	replay, err := legacy.Apply(t.Context(), base.Project.ID, receipt)
	if err != nil {
		t.Fatal(err)
	}
	before, _ = json.Marshal(pinned)
	after, _ = json.Marshal(replay)
	if string(before) != string(after) {
		t.Fatal("old source4 receipt changed after events upgrade")
	}
}
