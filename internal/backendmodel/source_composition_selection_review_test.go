package backendmodel

import (
	"encoding/json/v2"
	"testing"
	"uuid"
)

func TestSource6SelectedValueUpdatesDependencyCurrentness(t *testing.T) {
	for _, variant := range []string{"different", "equal"} {
		t.Run(variant, func(t *testing.T) {
			f := sourceReviewSharedValue(t, "column", variant)
			s, b := f.mapping(t, f.a)
			consumer := sourceReviewCommit(t, f.repo, f.project, s, b.AcceptedVersion, "fixture")
			f.project = &consumer.Project
			before, err := f.repo.ResolveSourceGraph(t.Context(), f.project.ID, f.project.CurrentRevisionID)
			if err != nil {
				t.Fatal(err)
			}
			var mapping ProviderAssertion
			for _, a := range before.Assertions {
				if a.Owner.ProviderNamespace == "provider-c" && a.Payload.Kind == "field_mapping" {
					mapping = a
				}
			}
			var rawBefore string
			if err := f.repo.db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_revision_assertions_documents WHERE revision_id=? AND record_id=? AND provider_namespace='provider-c'`, consumer.Revision.ID, mapping.RecordID).Scan(&rawBefore); err != nil {
				t.Fatal(err)
			}
			in := source6Input(t, f.project)
			in.SourceScope = &SourceScope{Kind: "add_provider", RepositoryID: f.repository}
			in.Manifest.Provider.Namespace = "provider-d"
			in.IdempotencyKey = "selection-only"
			session, err := f.repo.BeginImport(t.Context(), f.project.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := f.repo.PreviewImport(t.Context(), f.project.ID, session.ID, PreviewImportInput{ExpectedImportVersion: 1, BaseRevisionID: f.project.CurrentRevisionID})
			if err != nil {
				t.Fatal(err)
			}
			page, err := f.repo.ImportChanges(t.Context(), f.project.ID, session.ID, ImportChangesInput{PreviewVersion: preview.Version, RecordType: "assertion_conflict"})
			if err != nil || len(page.Items) == 0 {
				t.Fatalf("no target conflict: %+v %v", page, err)
			}
			commands := make([]ImportCommand, 0, len(page.Items))
			for _, item := range page.Items {
				conflict := item.AssertionConflict
				for _, choice := range conflict.Contenders {
					if choice.Owner.ProviderNamespace != "provider-b" {
						continue
					}
					commands = append(commands, ImportCommand{Op: "resolve_assertion", Resolution: &SourceAssertionResolution{DecisionID: uuid.NewV7().String(), RecordType: conflict.RecordType, ID: conflict.ID, Property: conflict.Property, ConflictHash: conflict.ConflictHash, Select: SourceAssertionSelection{RepositoryID: choice.Owner.RepositoryID, ProviderNamespace: choice.Owner.ProviderNamespace, AssertionHash: choice.AssertionHash}, Reason: "select another provider value"}})
				}
			}
			batch := sendCommands(t, f.repo, f.project, session, preview.Version, "select-b", commands...)
			out := sourceReviewCommit(t, f.repo, f.project, session, batch.AcceptedVersion, "provider-b")
			after, err := f.repo.ResolveSourceGraph(t.Context(), f.project.ID, out.Revision.ID)
			if err != nil {
				t.Fatal(err)
			}
			want := "stale"
			if variant == "equal" {
				want = "current"
			}
			found := false
			for _, fresh := range after.Currentness {
				if fresh.RecordID == mapping.RecordID && fresh.ProviderNamespace == "provider-c" {
					found = true
					if fresh.Dependency.Status != want || fresh.Own.Status != "current" {
						t.Fatalf("selected %s value: own=%+v dependency=%+v want dependency %s", variant, fresh.Own, fresh.Dependency, want)
					}
				}
			}
			if !found {
				t.Fatal("consumer currentness missing")
			}
			var rawAfter string
			if err := f.repo.db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_revision_assertions_documents WHERE revision_id=? AND record_id=? AND provider_namespace='provider-c'`, out.Revision.ID, mapping.RecordID).Scan(&rawAfter); err != nil {
				t.Fatal(err)
			}
			if rawAfter != rawBefore {
				t.Fatal("selection-only change rewrote retained consumer claim bytes")
			}
			for _, eid := range mapping.EvidenceIDs {
				if string(after.RawEvidence[eid]) != string(before.RawEvidence[eid]) {
					t.Fatal("selection rewrote retained consumer proof")
				}
			}
			for _, a := range after.Assertions {
				if sourceAssertionKey(a) == sourceAssertionKey(mapping) {
					old, _ := json.Marshal(mapping.DependencyClaims)
					next, _ := json.Marshal(a.DependencyClaims)
					if string(old) != string(next) {
						t.Fatal("selection silently advanced dependency pins")
					}
				}
			}
		})
	}
}

func TestSource6SelectionCurrentnessCycleReconfirms(t *testing.T) {
	t.Parallel()
	f := sourceReviewSharedValue(t, "port", "borrow")
	graph, err := f.repo.ResolveSourceGraph(t.Context(), f.project.ID, f.project.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	selected := false
	for _, choice := range graph.Selections {
		if choice.Property == (TypedSourcePropertySelector{Kind: "flow_ports", Collection: "outputs"}) && choice.Select.ProviderNamespace == "provider-b" {
			selected = true
		}
	}
	if !selected {
		t.Fatal("cyclic currentness prevented exact selection from stabilizing")
	}
	stale := false
	for _, current := range graph.Currentness {
		if current.RecordID == f.b.ExpectedID && current.ProviderNamespace == "provider-b" {
			stale = current.Dependency.Status == "stale"
		}
	}
	if !stale {
		t.Fatal("flow/step cycle did not retain derived dependency staleness")
	}
	var superseded int
	err = f.repo.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_import_source_decisions d JOIN backend_import_sessions s ON s.id=d.session_id WHERE s.project_id=? AND d.decision_kind='resolve_assertion' AND d.state='superseded'`, f.project.ID).Scan(&superseded)
	if err != nil || superseded < 1 {
		t.Fatalf("new conflict currentness was not acknowledged: %d %v", superseded, err)
	}
}
