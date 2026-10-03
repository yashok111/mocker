package backendmodel

import (
	"encoding/json/v2"
	"slices"
	"testing"
)

// A shared column is reobserved through a second facet. The selected SQL facet
// and its proof may be retained, so whole-node freshness cannot classify mapping.
func TestEventsReconcileSelectedLineageFacetStaleness(t *testing.T) {
	for _, profile := range []string{LineageProfile, EventsProfile} {
		t.Run(profile, func(t *testing.T) {
			for _, omitSelected := range []bool{false, true} {
				t.Run(map[bool]string{false: "fully reobserved", true: "retained selected facet"}[omitSelected], func(t *testing.T) {
					r, _ := testRepo(t)
					p := createProject(t, r, "create")
					input := lineageOrdersInput(t, p)
					if profile == EventsProfile {
						input = eventsProfileInput(input, false)
					}
					initial, err := r.BeginImport(t.Context(), p.ID, input)
					if err != nil {
						t.Fatal(err)
					}
					commands := eventsLineageTwoFacetCommands(t, initial, false)
					preview, ids := stageRelational(t, r, p, initial, commands, "two-facets")
					if preview.State != "ready" {
						t.Fatal(preview.Diagnostics)
					}
					base, err := commitFixture(t, r, p, initial, preview, "two-facets-commit")
					if err != nil {
						t.Fatal(err)
					}
					before, err := loadRevisionState(t.Context(), r.db.R, p.ID, base.Revision.ID)
					if err != nil {
						t.Fatal(err)
					}
					oldBytes, err := json.Marshal(before, json.Deterministic(true))
					if err != nil {
						t.Fatal(err)
					}

					repeat := lineageOrdersInput(t, &base.Project)
					if profile == EventsProfile {
						repeat = eventsProfileInput(repeat, false)
					}
					repeat.Mode = "reconcile"
					repeat.RepositoryID = new(initial.RepositoryID)
					repeat.GraphScope = &GraphScope{Profile: profile, Status: "partial", Gaps: []string{"One column facet may not be reobserved"}}
					repeat.IdempotencyKey = "selected-facet-repeat"
					session, err := r.BeginImport(t.Context(), p.ID, repeat)
					if err != nil {
						t.Fatal(err)
					}
					preview, _ = stageRelational(t, r, &base.Project, session, eventsLineageTwoFacetCommands(t, session, omitSelected), "selected-facet")
					if preview.State != "ready" {
						t.Fatal(preview.Diagnostics)
					}
					next, err := commitFixture(t, r, &base.Project, session, preview, "selected-facet-commit")
					if err != nil {
						t.Fatal(err)
					}

					column, err := r.Node(t.Context(), p.ID, next.Revision.ID, ids["column:orders:amount"])
					if err != nil {
						t.Fatal(err)
					}
					if column.Freshness.Status != "current" {
						t.Fatalf("shared column must remain current: %+v", column.Freshness)
					}
					facets, _, err := relationalFacetObject(column.Kind, column.Attributes)
					if err != nil {
						t.Fatal(err)
					}
					sql, err := decodeRelationalFacet(column.Kind, facets["sql"], true)
					if err != nil {
						t.Fatal(err)
					}
					other, err := decodeRelationalFacet(column.Kind, facets["alternative"], true)
					if err != nil {
						t.Fatal(err)
					}
					want := "current"
					if omitSelected {
						want = "stale"
					}
					if sql.Freshness.Status != want || other.Freshness.Status != "current" {
						t.Fatalf("facet freshness: selected=%+v other=%+v", sql.Freshness, other.Freshness)
					}
					mapping, err := r.Node(t.Context(), p.ID, next.Revision.ID, ids["m04-amount"])
					if err != nil {
						t.Fatal(err)
					}
					if mapping.Freshness.Status != want {
						t.Fatalf("selected-facet mapping freshness=%s want=%s; column remains current", mapping.Freshness.Status, want)
					}
					if omitSelected && !slices.Contains(mapping.Freshness.Reasons, "dependency_stale") {
						t.Fatal("mapping staleness reason lost", mapping.Freshness)
					}
					proof, err := r.Evidence(t.Context(), p.ID, next.Revision.ID, EvidenceQueryInput{SubjectID: mapping.ID})
					if err != nil || len(proof.Items) != 1 {
						t.Fatal(proof, err)
					}
					if proof.Items[0].Freshness.Status != want {
						t.Fatal("mapping proof freshness did not follow selected facet", proof.Items[0].Freshness)
					}
					sourceProof, err := r.Evidence(t.Context(), p.ID, next.Revision.ID, EvidenceQueryInput{SubjectID: column.ID})
					if err != nil {
						t.Fatal(err)
					}
					if !slices.ContainsFunc(sourceProof.Items, func(e Evidence) bool { return e.ID == ids["proof:column:orders:amount"] && e.Freshness.Status == want }) {
						t.Fatal("selected retained evidence freshness lost", sourceProof.Items)
					}

					after, err := loadRevisionState(t.Context(), r.db.R, p.ID, base.Revision.ID)
					if err != nil {
						t.Fatal(err)
					}
					oldAfter, err := json.Marshal(after, json.Deterministic(true))
					if err != nil || string(oldBytes) != string(oldAfter) {
						t.Fatal("reimport changed old pinned revision", err)
					}
				})
			}
		})
	}
}

func eventsLineageTwoFacetCommands(t *testing.T, s *ImportSession, omitSelected bool) []ImportCommand {
	t.Helper()
	cs := lineageOrdersCommands(t, s)
	column := relationalCommand(cs, "column:orders:amount").Node
	facets, _, err := relationalFacetObject(column.Kind, column.Attributes)
	if err != nil {
		t.Fatal(err)
	}
	alternate, err := relationalObject(facets["sql"])
	if err != nil {
		t.Fatal(err)
	}
	alternate["evidenceKeys"] = relationalRaw(t, []string{"proof:column:orders:amount:alternative"})
	facets["alternative"] = relationalRaw(t, alternate)
	if omitSelected {
		delete(facets, "sql")
	}
	column.Attributes = replaceRelationalFacets(column.Kind, column.Attributes, facets)
	column.EvidenceKeys = []string{"proof:column:orders:amount:alternative"}
	if !omitSelected {
		column.EvidenceKeys = append(column.EvidenceKeys, "proof:column:orders:amount")
	}
	proof := *relationalCommand(cs, "proof:column:orders:amount").Evidence
	proof.ExternalKey = "proof:column:orders:amount:alternative"
	proof.PropertyPath = new("/attributes/facets/alternative")
	cs = append(cs, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
	if omitSelected {
		cs = slices.DeleteFunc(cs, func(c ImportCommand) bool {
			return c.Evidence != nil && c.Evidence.ExternalKey == "proof:column:orders:amount"
		})
	}
	return cs
}
