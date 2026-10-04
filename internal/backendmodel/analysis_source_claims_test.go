package backendmodel_test

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	model "github.com/yashok111/mocker/internal/backendmodel"
)

const (
	analysisClaimSubject    = "01900000-0000-7000-8000-000000000001"
	analysisClaimRepository = "01900000-0000-7000-8000-000000000002"
	analysisClaimParent     = "01900000-0000-7000-8000-000000000003"
)

func analysisClaimFixture() *model.SourceGraphSnapshot {
	current := model.AssertionFreshness{Status: "current", Reasons: []string{}}
	name := model.TypedSourcePropertySelector{Kind: "name"}
	parent := model.TypedSourcePropertySelector{Kind: "parent"}
	graph := &model.SourceGraphSnapshot{
		State: model.RevisionState{Nodes: []model.Node{{ID: analysisClaimSubject, Kind: "handler", Name: "Handler", ParentID: new(analysisClaimParent), Attributes: map[string]jsontext.Value{}, Freshness: new(current)}}},
	}
	// Deliberately reverse provider input order; both claims address one UUID.
	for _, provider := range []string{"provider-b", "provider-a"} {
		assertion := model.ProviderAssertion{
			RecordType: "node", RecordID: analysisClaimSubject,
			Owner:       model.AssertionOwnership{RepositoryID: analysisClaimRepository, ProviderNamespace: provider, Profile: model.ComposedProfile},
			ExternalKey: "handler", AssertionHash: strings.Repeat("a", 64),
			Payload:          model.SourceAssertionPayload{RecordType: "node", Kind: "handler", Name: "Handler", ParentID: new(analysisClaimParent), Attributes: map[string]jsontext.Value{}},
			Freshness:        current,
			DependencyClaims: []model.SourceDependencyBinding{{Basis: "candidate", Site: "/parentId", Property: parent, Target: model.BaseAssertionRef{RepositoryID: analysisClaimRepository, ProviderNamespace: provider, RecordType: "node", ExternalKey: "service", ExpectedID: analysisClaimParent, AssertionHash: strings.Repeat("b", 64)}}},
		}
		graph.Assertions = append(graph.Assertions, assertion)
		graph.Currentness = append(graph.Currentness, model.SourceClaimCurrentness{RecordType: "node", RecordID: analysisClaimSubject, RepositoryID: analysisClaimRepository, ProviderNamespace: provider, AssertionHash: assertion.AssertionHash, Own: current, Dependency: current, Fields: []model.TypedFieldCurrentness{{Property: name, Own: current, Dependency: current}, {Property: parent, Own: current, Dependency: current}}})
	}
	graph.Selections = []model.SourceAssertionResolution{{DecisionID: "01900000-0000-7000-8000-000000000004", RecordType: "node", ID: analysisClaimSubject, Property: name, ConflictHash: strings.Repeat("c", 64), Select: model.SourceAssertionSelection{RepositoryID: analysisClaimRepository, ProviderNamespace: "provider-a", AssertionHash: strings.Repeat("a", 64)}, Reason: "Selected provider"}}
	return graph
}

func analysisClaimBytes(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value, json.Deterministic(true))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestAppendAnalysisSourceClaimDeltas(t *testing.T) {
	address := "assertion/" + analysisClaimSubject + "/" + analysisClaimRepository + "/"
	for _, tc := range []struct {
		name          string
		change        func(*model.SourceGraphSnapshot)
		wantIDs       []string
		wantPaths     [][]string
		wantFreshness int
	}{
		{name: "selected field becomes stale", change: func(g *model.SourceGraphSnapshot) {
			g.Currentness[1].Fields[0].Own = model.AssertionFreshness{Status: "stale", Reasons: []string{"not_reobserved"}}
		}, wantIDs: []string{address + `provider-a/property/{"kind":"name"}`}, wantPaths: [][]string{{"/source/currentness/field"}}, wantFreshness: 1},
		{name: "winning property selection changes", change: func(g *model.SourceGraphSnapshot) { g.Selections[0].Select.ProviderNamespace = "provider-b" }, wantIDs: []string{address + `provider-a/property/{"kind":"name"}`, address + `provider-b/property/{"kind":"name"}`}, wantPaths: [][]string{{"/source/property"}, {"/source/property"}}},
		{name: "exact dependency claim changes", change: func(g *model.SourceGraphSnapshot) {
			g.Assertions[1].DependencyClaims[0].Target.AssertionHash = strings.Repeat("d", 64)
		}, wantIDs: []string{address + "provider-a", address + `provider-a/property/{"kind":"parent"}`}, wantPaths: [][]string{{"/source/assertion"}, {"/source/property"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before, after := analysisClaimFixture(), analysisClaimFixture()
			tc.change(after)
			if !reflect.DeepEqual(before.State.Nodes, after.State.Nodes) {
				t.Fatal("fixture must retain byte-equal structural nodes")
			}
			beforeRaw, afterRaw := analysisClaimBytes(t, before), analysisClaimBytes(t, after)
			delta, err := model.CompareRevisionStates(t.Context(), before.State, after.State)
			if err != nil {
				t.Fatal(err)
			}
			if len(delta.Changes) != 0 {
				t.Fatalf("structural comparator unexpectedly detects claim change: %+v", delta.Changes)
			}
			if err = model.AppendAnalysisSourceClaimDeltas(t.Context(), delta, before, after); err != nil {
				t.Fatal(err)
			}
			if len(delta.Changes) != len(tc.wantIDs) || delta.Summary.SourceChanges != int64(len(tc.wantIDs)) || delta.Summary.FreshnessChanges != int64(tc.wantFreshness) {
				t.Fatalf("wrong qualified change counts: %+v", delta)
			}
			for i, change := range delta.Changes {
				if change.ID != tc.wantIDs[i] || !reflect.DeepEqual(change.ChangedPaths, tc.wantPaths[i]) {
					t.Fatalf("change %d: %+v", i, change)
				}
				for _, side := range []*model.RecordSide{change.Before, change.After} {
					if side == nil || side.SourceClaim == nil || side.SourceClaim.Assertion.RepositoryID != analysisClaimRepository || side.SourceClaim.Assertion.ExpectedID != analysisClaimSubject {
						t.Fatalf("lost exact qualified side: %+v", side)
					}
					if !strings.HasPrefix(change.ID, address+side.SourceClaim.Assertion.ProviderNamespace) {
						t.Fatalf("provider collapsed: %+v", side.SourceClaim)
					}
				}
				old, next := change.Before.SourceClaim, change.After.SourceClaim
				switch tc.name {
				case "selected field becomes stale":
					if old.Currentness.Fields[0].Own.Status != "current" || next.Currentness.Fields[0].Own.Status != "stale" {
						t.Fatal("lost selected field currentness")
					}
				case "winning property selection changes":
					if old.Selections[0].Select.ProviderNamespace != "provider-a" || next.Selections[0].Select.ProviderNamespace != "provider-b" {
						t.Fatal("lost property winner")
					}
				case "exact dependency claim changes":
					if old.DependencyClaims[0].Target.AssertionHash != strings.Repeat("b", 64) || next.DependencyClaims[0].Target.AssertionHash != strings.Repeat("d", 64) {
						t.Fatal("lost exact dependency binding")
					}
				}
			}
			if string(beforeRaw) != string(analysisClaimBytes(t, before)) || string(afterRaw) != string(analysisClaimBytes(t, after)) {
				t.Fatal("comparison mutated source snapshots")
			}
			expected := analysisClaimBytes(t, delta)
			slices.Reverse(before.Assertions)
			slices.Reverse(after.Assertions)
			slices.Reverse(before.Currentness)
			slices.Reverse(after.Currentness)
			beforeRaw, afterRaw = analysisClaimBytes(t, before), analysisClaimBytes(t, after)
			reordered := new(model.RevisionDelta)
			if err = model.AppendAnalysisSourceClaimDeltas(t.Context(), reordered, before, after); err != nil {
				t.Fatal(err)
			}
			if string(expected) != string(analysisClaimBytes(t, reordered)) {
				t.Fatal("qualified delta order depends on input order")
			}
			if string(beforeRaw) != string(analysisClaimBytes(t, before)) || string(afterRaw) != string(analysisClaimBytes(t, after)) {
				t.Fatal("comparison reordered source snapshots")
			}
		})
	}
}

func TestAppendAnalysisSourceClaimDeltasCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	delta := new(model.RevisionDelta)
	if err := model.AppendAnalysisSourceClaimDeltas(ctx, delta, analysisClaimFixture(), analysisClaimFixture()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation lost: %v", err)
	}
	if len(delta.Changes) != 0 {
		t.Fatal("cancelled comparison appended changes")
	}
}
