package backendmodel

import (
	"context"
	"database/sql"
	"reflect"
	"testing"
)

type architectureQueryCounter struct {
	importReader
	queries int
}

func (q *architectureQueryCounter) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	q.queries++
	return q.importReader.QueryContext(ctx, query, args...)
}

func (q *architectureQueryCounter) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	q.queries++
	return q.importReader.QueryRowContext(ctx, query, args...)
}

func TestArchitectureSource5ReadHasBoundedQueries(t *testing.T) {
	t.Parallel()
	r, base, _ := effectiveFiveRelationalFixture(t)
	q := &architectureQueryCounter{importReader: r.db.R}
	graph, err := loadArchitectureGraph(t.Context(), q, base.Project.ID, BackendReadTarget{RevisionID: base.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.State.Evidence) == 0 {
		t.Fatal("fixture must exercise source proof reads")
	}
	if q.queries > 12 {
		t.Fatalf("C4 read issued %d SQL statements; expected at most 12 bulk/metadata reads without per-evidence bootstrap", q.queries)
	}
}

func TestArchitectureReadMatchesFullResolver(t *testing.T) {
	t.Parallel()
	for _, schema := range []string{"source5", "source5-artifacts", "source6", "proposal"} {
		t.Run(schema, func(t *testing.T) {
			r, base, _ := effectiveFiveRelationalFixture(t)
			if schema == "source6" {
				r, base, _ = effectiveRepresentationFixture(t)
			}
			if schema == "source5-artifacts" {
				service, old, _, _ := artifactServiceFixture(t)
				base, _ = upgradeEventsArtifactFixture(t, service, old)
				r = service.repo
			}
			target := BackendReadTarget{RevisionID: base.Revision.ID}
			if schema == "proposal" {
				draft, err := r.CreateChangeProposal(t.Context(), base.Project.ID, CreateChangeProposalInput{Name: "C4 parity", BaseRevisionID: base.Revision.ID, IdempotencyKey: "c4-parity"})
				if err != nil {
					t.Fatal(err)
				}
				target = BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: draft.Proposal.ID, ProposalRevisionID: draft.Revision.ID}}
			}
			full, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, target)
			if err != nil {
				t.Fatal(err)
			}
			fast, err := loadArchitectureGraph(t.Context(), r.db.R, base.Project.ID, target)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(full.Pins, fast.Pins) || !reflect.DeepEqual(full.State, fast.State) || !reflect.DeepEqual(full.Origins, fast.Origins) {
				t.Fatal("architecture read changed exact pins, source records or effective origins")
			}
			assertArchitecturePagesMatch(t, full, fast)
		})
	}
}

func assertArchitecturePagesMatch(t *testing.T, full, fast *EffectiveGraphSnapshot) {
	t.Helper()
	doc := diagramTestDocument(full.Pins.BaseRevisionID)
	doc.Target = full.Target
	const application = "10000000-0000-4000-8000-000000000003"
	origin := DiagramOrigin{Kind: "authored", Reason: "Explicit test responsibility"}
	doc.Payload.Elements = append(doc.Payload.Elements, ArchitectureElement{ID: application, Label: "Backend", Role: "application", ParentID: doc.Payload.PrimarySystemID, Origin: origin, Refs: []DiagramRef{}})
	for _, n := range full.State.Nodes {
		doc.Payload.Elements = append(doc.Payload.Elements, ArchitectureElement{ID: n.ID, Label: n.Name, Role: "component", ParentID: application, Origin: origin, Refs: []DiagramRef{{Kind: "record", RecordType: "node", ID: n.ID}}})
	}
	hash, err := requestDigest(doc)
	if err != nil {
		t.Fatal(err)
	}
	v := &DiagramVersion{Document: doc, TargetHash: full.Pins.TargetHash, Pin: DiagramPin{ID: "10000000-0000-4000-8000-000000000080", Version: 1, ContentHash: hash}, Gaps: []DiagramGap{}}
	for _, level := range []string{"context", "containers", "components"} {
		root := doc.Payload.PrimarySystemID
		if level == "components" {
			root = application
		}
		memberID := doc.Payload.PrimarySystemID
		for _, section := range []string{"elements", "links", "gaps", "members"} {
			q := DiagramQueryInput{Pin: v.Pin, RootID: root, Level: level, Section: section, Origin: "all", Limit: 2}
			if section == "members" {
				q.SubjectID = memberID
			}
			for {
				want, err := ProjectArchitecture(t.Context(), v, full, q)
				if err != nil {
					t.Fatal(err)
				}
				got, err := ProjectArchitecture(t.Context(), v, fast, q)
				if err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(want, got) {
					t.Fatalf("%s/%s projection changed", level, section)
				}
				if section == "links" && len(want.Items) > 0 {
					memberID = want.Items[0].ArchitectureLink.ID
				}
				if want.NextCursor == "" {
					break
				}
				q.Cursor = want.NextCursor
			}
		}
	}
}
