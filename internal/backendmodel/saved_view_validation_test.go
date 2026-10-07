package backendmodel

import (
	"fmt"
	"math"
	"reflect"
	"slices"
	"testing"
	"uuid"

	"github.com/yashok111/mocker/internal/testkit"
)

func TestSavedViewDirectValidationBoundaries(t *testing.T) {
	r, _ := testRepo(t)
	source, ids := commitRelationalFixture(t, r, "sqlite", "v1")
	base := savedDatabaseInput(source, ids, "direct")
	for _, tc := range []struct {
		name   string
		change func(*CreateSavedViewInput)
	}{
		{"mixed-target", func(in *CreateSavedViewInput) {
			in.Target.Proposal = &ProposalReadTarget{ProposalID: uuid.NewV7().String(), ProposalRevisionID: uuid.NewV7().String()}
		}},
		{"mixed-state", func(in *CreateSavedViewInput) {
			in.State.Flow = &SavedFlowViewState{Kind: "flow", Positions: []SavedViewPosition{}, CollapsedGroupIDs: []string{}}
		}},
		{"wrong-kind", func(in *CreateSavedViewInput) { in.State.Database.Kind = "flow" }},
		{"nil-positions", func(in *CreateSavedViewInput) { in.State.Database.Positions = nil }},
		{"nil-groups", func(in *CreateSavedViewInput) { in.State.Database.CollapsedGroupIDs = nil }},
		{"duplicate-positions", func(in *CreateSavedViewInput) {
			in.State.Database.Positions = append(in.State.Database.Positions, in.State.Database.Positions[0])
		}},
		{"201-positions", func(in *CreateSavedViewInput) { in.State.Database.Positions = make([]SavedViewPosition, 201) }},
		{"nan-x", func(in *CreateSavedViewInput) {
			in.State.Database.Positions = []SavedViewPosition{{NodeID: ids["table:orders"], X: math.NaN()}}
		}},
		{"infinite-y", func(in *CreateSavedViewInput) {
			in.State.Database.Positions = []SavedViewPosition{{NodeID: ids["table:orders"], Y: math.Inf(1)}}
		}},
		{"range-y", func(in *CreateSavedViewInput) {
			in.State.Database.Positions = []SavedViewPosition{{NodeID: ids["table:orders"], Y: -1000001}}
		}},
		{"malformed-selection", func(in *CreateSavedViewInput) {
			in.State.Database.Selection = &SavedViewSelection{RecordType: "node", ID: "foreign"}
		}},
		{"facet-control", func(in *CreateSavedViewInput) { in.State.Database.Scope.FacetKey = "sql\n" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			d := *base.State.Database
			d.Positions = slices.Clone(d.Positions)
			in.State = SavedViewState{Database: &d}
			in.IdempotencyKey = tc.name
			tc.change(&in)
			_, err := r.CreateSavedView(t.Context(), source.Project.ID, in)
			assertFault(t, err, "backend_invalid")
		})
	}
}

func savedRuntimeFixture(t *testing.T) (*Repo, *ImportCommitResult, map[string]string) {
	t.Helper()
	r, _ := testRepo(t)
	p := createProject(t, r, "runtime-saved")
	s, err := r.BeginImport(t.Context(), p.ID, runtimeProfileInput(relationalFixtureInput(t, p, "sqlite", "v1"), false))
	if err != nil {
		t.Fatal(err)
	}
	cs := runtimeTransactionCommands(t, runtimeRelationalCommands(t, p, s, "sqlite"))
	v, ids := stageRelational(t, r, p, s, cs, "runtime")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "runtime-commit")
	if err != nil {
		t.Fatal(err)
	}
	return r, out, ids
}
func TestSavedViewFlowRoundTripAndReferences(t *testing.T) {
	r, out, ids := savedRuntimeFixture(t)
	in := CreateSavedViewInput{Name: "Flow", Target: BackendReadTarget{RevisionID: out.Revision.ID}, State: SavedViewState{Flow: &SavedFlowViewState{Kind: "flow", Scope: SavedFlowViewScope{EntrypointID: ids["http"], FlowID: ids["flow"], DataNodeID: ids["column:orders:user_id"]}, Filters: SavedFlowViewFilters{Search: " /value ", AccessKind: "reads", ReverseAccessKind: "writes"}, Selection: &SavedViewSelection{RecordType: "edge", ID: ids["query-read"]}, Positions: []SavedViewPosition{{NodeID: ids["return"], X: -1000000, Y: 1000000}}, CollapsedGroupIDs: []string{ids["tx"]}}}, IdempotencyKey: "flow"}
	v, err := r.CreateSavedView(t.Context(), out.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(v.State, in.State) {
		t.Fatal("flow state lost", v.State)
	}
	got, err := r.GetSavedView(t.Context(), out.Project.ID, v.ID, GetSavedViewInput{Version: 1})
	if err != nil || !reflect.DeepEqual(got.State, in.State) {
		t.Fatal(got, err)
	}
	for _, tc := range []struct {
		name, code string
		change     func(*SavedFlowViewState)
	}{
		{"entrypoint-kind", "backend_invalid", func(f *SavedFlowViewState) { f.Scope.EntrypointID = ids["handler"] }},
		{"flow-kind", "backend_invalid", func(f *SavedFlowViewState) { f.Scope.FlowID = ids["query"] }},
		{"data-kind", "backend_invalid", func(f *SavedFlowViewState) { f.Scope.DataNodeID = ids["http"] }},
		{"missing-node", "backend_not_found", func(f *SavedFlowViewState) {
			f.Selection = &SavedViewSelection{RecordType: "node", ID: uuid.NewV7().String()}
		}},
		{"missing-edge", "backend_not_found", func(f *SavedFlowViewState) {
			f.Selection = &SavedViewSelection{RecordType: "edge", ID: uuid.NewV7().String()}
		}},
		{"position-kind", "backend_invalid", func(f *SavedFlowViewState) { f.Positions = []SavedViewPosition{{NodeID: ids["query"]}} }},
		{"group-kind", "backend_invalid", func(f *SavedFlowViewState) { f.CollapsedGroupIDs = []string{ids["return"]} }},
		{"missing-group", "backend_not_found", func(f *SavedFlowViewState) { f.CollapsedGroupIDs = []string{uuid.NewV7().String()} }},
		{"no-flow-positions", "backend_invalid", func(f *SavedFlowViewState) { f.Scope.FlowID = "" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := in
			f := *in.State.Flow
			tc.change(&f)
			bad.State = SavedViewState{Flow: &f}
			bad.IdempotencyKey = tc.name
			_, err := r.CreateSavedView(t.Context(), out.Project.ID, bad)
			assertFault(t, err, tc.code)
		})
	}
	// A flow is valid without an entrypoint, and inspectors can select outside it.
	clone := in
	f := *in.State.Flow
	f.Scope.EntrypointID = ""
	f.Selection = &SavedViewSelection{RecordType: "node", ID: ids["query"]}
	clone.State = SavedViewState{Flow: &f}
	clone.IdempotencyKey = "flow-only"
	if _, err := r.CreateSavedView(t.Context(), out.Project.ID, clone); err != nil {
		t.Fatal(err)
	}
	other := createProject(t, r, "foreign")
	_, err = r.CreateSavedView(t.Context(), other.ID, in)
	assertFault(t, err, "backend_not_found")
	// Schema3 relational Database state shares the same exact revision.
	dbIn := savedDatabaseInput(out, ids, "source3-db")
	if _, err := r.CreateSavedView(t.Context(), out.Project.ID, dbIn); err != nil {
		t.Fatal(err)
	}
}

func TestSavedViewDatabaseScopeAndPinnedProposal(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgresql"} {
		t.Run(dialect, func(t *testing.T) {
			r, d, _, ids := proposalEvaluationFixture(t, dialect)
			out, err := r.ApplyProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, proposalApplyInput(t, r, d, "design", proposalFK(ids)))
			if err != nil {
				t.Fatal(err)
			}
			designed := out.Changes[0].GeneratedIDs["constraintId"]
			edgeID := out.Changes[0].GeneratedIDs["edgeId"]
			base := CreateSavedViewInput{Name: "Designed", Target: BackendReadTarget{Proposal: &ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: out.Revision.ID}}, State: SavedViewState{Database: &SavedDatabaseViewState{Kind: "database", Scope: SavedDatabaseViewScope{DatastoreID: d.Proposal.DatastoreID, FacetKey: "sql"}, Filters: SavedDatabaseViewFilters{Search: "orders", RelationshipTableID: ids["table:orders"]}, Selection: &SavedViewSelection{RecordType: "node", ID: designed}, Positions: []SavedViewPosition{{NodeID: ids["table:orders"], X: 0.5, Y: -9}}, CollapsedGroupIDs: []string{}}}, IdempotencyKey: "designed"}
			v, err := r.CreateSavedView(t.Context(), d.Proposal.ProjectID, base)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(v.State, base.State) {
				t.Fatal("proposal presentation fields changed")
			}
			raw := savedViewBytes(t, v)
			if v.Pins.Proposal == nil || v.Pins.Proposal.ProposalRevisionID != out.Revision.ID || v.Pins.Proposal.EffectiveGraphHash != out.CandidateGraphHash {
				t.Fatal("wrong proposal pins", v.Pins)
			}
			if edgeID == "" {
				for _, o := range out.Revision.Overlays {
					if o.RecordType == "edge" && o.Base == nil {
						edgeID = o.SubjectID
					}
				}
			}
			if !ValidID(edgeID) {
				t.Fatal("fixture edge missing", out.Changes)
			}
			edge := base
			state := *base.State.Database
			state.Selection = &SavedViewSelection{RecordType: "edge", ID: edgeID}
			edge.State = SavedViewState{Database: &state}
			edge.IdempotencyKey = "designed-edge"
			if _, err := r.CreateSavedView(t.Context(), d.Proposal.ProjectID, edge); err != nil {
				t.Fatal(err)
			}
			current, err := r.GetProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, GetProposalInput{})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := r.ApplyProposal(t.Context(), d.Proposal.ProjectID, d.Proposal.ID, proposalApplyInput(t, r, current, "advance", proposalNullable(ids, "required", false))); err != nil {
				t.Fatal(err)
			}
			proposalAdvanceSource(t, r, d, ids, dialect)
			replay, err := r.CreateSavedView(t.Context(), d.Proposal.ProjectID, base)
			if err != nil || savedViewBytes(t, replay) != raw {
				t.Fatal("lost proposal replay", err)
			}
			old, err := r.GetSavedView(t.Context(), d.Proposal.ProjectID, v.ID, GetSavedViewInput{Version: 1})
			if err != nil || savedViewBytes(t, old) != raw {
				t.Fatal("lost historical proposal", err)
			}
			for _, tc := range []struct {
				name, code string
				change     func(*SavedDatabaseViewState)
			}{
				{"wrong-datastore", "backend_invalid", func(s *SavedDatabaseViewState) { s.Scope.DatastoreID = ids["table:orders"] }},
				{"missing-facet", "backend_invalid", func(s *SavedDatabaseViewState) { s.Scope.FacetKey = "missing" }},
				{"wrong-position-kind", "backend_invalid", func(s *SavedDatabaseViewState) { s.Positions = []SavedViewPosition{{NodeID: designed}} }},
				{"missing-selection", "backend_not_found", func(s *SavedDatabaseViewState) {
					s.Selection = &SavedViewSelection{RecordType: "node", ID: uuid.NewV7().String()}
				}},
				{"wrong-filter-kind", "backend_invalid", func(s *SavedDatabaseViewState) { s.Filters.RelationshipTableID = designed }},
				{"wrong-collapse-kind", "backend_invalid", func(s *SavedDatabaseViewState) { s.CollapsedGroupIDs = []string{ids["table:orders"]} }},
			} {
				t.Run(tc.name, func(t *testing.T) {
					bad := base
					state := *base.State.Database
					tc.change(&state)
					bad.State = SavedViewState{Database: &state}
					bad.IdempotencyKey = tc.name
					_, err := r.CreateSavedView(t.Context(), d.Proposal.ProjectID, bad)
					assertFault(t, err, tc.code)
				})
			}
			flow := base
			flow.State = SavedViewState{Flow: &SavedFlowViewState{Kind: "flow", Positions: []SavedViewPosition{}, CollapsedGroupIDs: []string{}}}
			flow.IdempotencyKey = "proposal-flow"
			_, err = r.CreateSavedView(t.Context(), d.Proposal.ProjectID, flow)
			assertFault(t, err, "backend_unsupported_scope")
			state = *base.State.Database
			state.Scope.FacetKey = "orm"
			_, err = r.SaveSavedView(t.Context(), d.Proposal.ProjectID, v.ID, SaveSavedViewInput{Name: "Rebind", State: SavedViewState{Database: &state}, ExpectedVersion: 1, IdempotencyKey: "rebind"})
			assertFault(t, err, "backend_invalid")
		})
	}
}

func TestSavedViewWholeScopeAndUnknownTransaction(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	p := createProject(t, r, "whole-scope")
	s := runtimeQueryFixture(t)
	runtimeQueryAddRelationalFacets(t, s)
	s.Revision.ProjectID = p.ID
	runtimeQueryAddNode(t, s, 20, "handler", 0, nil)
	runtimeQueryAddNode(t, s, 21, "flow", 20, nil)
	runtimeQueryAddNode(t, s, 22, "flow_step", 21, nil)
	runtimeQueryAddNode(t, s, 23, "transaction", 3, nil)
	for i := range 205 {
		runtimeQueryAddNode(t, s, 1000+i, "flow_step", 3, nil)
	}
	runtimeQueryPersist(t, r, s)
	base := CreateSavedViewInput{Name: "Off-page", Target: BackendReadTarget{RevisionID: s.Revision.ID}, State: SavedViewState{Flow: &SavedFlowViewState{Kind: "flow", Scope: SavedFlowViewScope{EntrypointID: runtimeQueryID(1), FlowID: runtimeQueryID(3)}, Filters: SavedFlowViewFilters{Search: "invisible"}, Selection: &SavedViewSelection{RecordType: "node", ID: runtimeQueryID(1204)}, Positions: []SavedViewPosition{{NodeID: runtimeQueryID(1204), X: 123, Y: 456}}, CollapsedGroupIDs: []string{}}}, IdempotencyKey: "off-page"}
	page, err := r.QueryFlow(t.Context(), p.ID, FlowQueryInput{RevisionID: s.Revision.ID, View: "steps", FlowID: runtimeQueryID(3), Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range page.StepItems {
		if n.ID == runtimeQueryID(1204) {
			t.Fatal("fixture reference was visible")
		}
	}
	if _, err := r.CreateSavedView(t.Context(), p.ID, base); err != nil {
		t.Fatal("off-page reference rejected", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*SavedFlowViewState)
	}{
		{"wrong-operation-flow-relation", func(f *SavedFlowViewState) { f.Scope.FlowID = runtimeQueryID(21); f.Positions = []SavedViewPosition{} }},
		{"other-flow-step", func(f *SavedFlowViewState) { f.Positions = []SavedViewPosition{{NodeID: runtimeQueryID(22)}} }},
		{"unknown-transaction", func(f *SavedFlowViewState) { f.CollapsedGroupIDs = []string{runtimeQueryID(23)} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bad := base
			f := *base.State.Flow
			tc.change(&f)
			bad.State = SavedViewState{Flow: &f}
			bad.IdempotencyKey = tc.name
			_, err := r.CreateSavedView(t.Context(), p.ID, bad)
			assertFault(t, err, "backend_invalid")
		})
	}
	// Unknown contexts must never create transaction groups.
	if _, err := testkit.EditLegacyBackendPayload(t.Context(), db, `UPDATE backend_graph_records SET document=json_set(document,'$.attributes.transactionContext',json(?)) WHERE revision_id=? AND id=?`, fmt.Sprintf(`{"status":"unknown","reason":"unresolved","transactionId":%q}`, runtimeQueryID(23)), s.Revision.ID, runtimeQueryID(4)); err != nil {
		t.Fatal(err)
	}
	f := *base.State.Flow
	f.CollapsedGroupIDs = []string{runtimeQueryID(23)}
	base.State = SavedViewState{Flow: &f}
	base.IdempotencyKey = "unknown-explicit"
	_, err = r.CreateSavedView(t.Context(), p.ID, base)
	assertFault(t, err, "backend_invalid")
}
