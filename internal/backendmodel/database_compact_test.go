package backendmodel

import (
	"reflect"
	"slices"
	"testing"
)

func TestCompactDatabasePreservesRowsAndPagesAllLimitations(t *testing.T) {
	t.Parallel()
	for _, target := range []string{"source5", "source6", "proposal"} {
		t.Run(target, func(t *testing.T) { testCompactDatabaseTarget(t, target) })
	}
}

func testCompactDatabaseTarget(t *testing.T, target string) {
	t.Helper()
	r, base, ids := effectiveFiveRelationalFixture(t)
	if target == "source6" {
		input := source6Input(t, &base.Project)
		input.IdempotencyKey = "compact-source6"
		input.Manifest.RepositoryName = "compact-source"
		input.ProfileExtension = &ImportProfileExtension{FromProfile: EventsProfile, ToProfile: ComposedProfile}
		_, base = commitSource6Fixture(t, r, &base.Project, input)
	}
	in := DatabaseQueryInput{RevisionID: base.Revision.ID, DatastoreID: ids["database:orders"], FacetKey: "orm", RecordType: "relationships", Limit: 1}
	if target == "proposal" {
		draft, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "Compact target", BaseRevisionID: base.Revision.ID, IdempotencyKey: "compact"})
		if err != nil {
			t.Fatal(err)
		}
		in.RevisionID = ""
		in.ChangeProposal = &ProposalReadTarget{ProposalID: draft.Proposal.ID, ProposalRevisionID: draft.Revision.ID}
	}
	legacy, err := r.QueryDatabase(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	in.ResponseMode = "compact-v1"
	compact, err := r.QueryDatabase(t.Context(), base.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(legacy.RelationshipItems, compact.RelationshipItems) || compact.SemanticHash != legacy.SemanticHash || len(compact.Coverage.Snapshots) != 0 || len(compact.Limitations) != 0 || compact.CoverageSummary == nil || compact.LimitationSummary == nil {
		t.Fatal("compact response changed rows/pins or repeated details")
	}
	in.Section = "limitations"
	got := []string{}
	for {
		page, err := r.QueryDatabase(t.Context(), base.Project.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.RelationshipItems) != 0 || len(page.TableItems) != 0 {
			t.Fatal("limitation page repeated data rows")
		}
		for _, item := range page.LimitationItems {
			if item.Code == "" {
				t.Fatal("missing normalized code")
			}
			got = append(got, item.Message)
		}
		if page.NextCursor == "" {
			break
		}
		in.Cursor = page.NextCursor
	}
	slices.Sort(got)
	got = slices.Compact(got)
	if !slices.Equal(got, legacy.Limitations) {
		t.Fatalf("limitation union changed: %v vs %v", got, legacy.Limitations)
	}
}
