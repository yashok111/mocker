package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"slices"
	"strings"
	"testing"
)

func lineageOrdersInput(t *testing.T, p *Project) BeginImportInput {
	t.Helper()
	in := lineageProfileInput(firstImportFixture(p), false)
	raw, err := os.ReadFile("testdata/lineage/orders/source.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	in.Manifest.Snapshot.Files = []ManifestFile{{Path: "source.go.txt", ContentHash: hashBytes(raw), FileType: "go", AnalysisStatus: "analyzed"}}
	for i := range in.Inventory {
		if slices.Contains([]string{"files", "endpoints", "datastores"}, in.Inventory[i].Category) {
			in.Inventory[i].KnownCount = 1
			in.Inventory[i].Denominator = new(int64(1))
		}
	}
	return in
}
func lineageOrdersCommands(t *testing.T, s *ImportSession) []ImportCommand {
	t.Helper()
	raw, err := os.ReadFile("testdata/lineage/orders/commands.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.NewReplacer("@repositoryId@", s.RepositoryID, "@snapshotId@", s.SnapshotID).Replace(string(raw)))
	var cs []ImportCommand
	if err = json.Unmarshal(raw, &cs, json.RejectUnknownMembers(true)); err != nil {
		t.Fatal(err)
	}
	return cs
}
func lineageOrdersCommitted(t *testing.T) (*Repo, *ImportCommitResult, *ImportSession, map[string]string) {
	t.Helper()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, lineageOrdersInput(t, p))
	if err != nil {
		t.Fatal(err)
	}
	v, ids := stageRelational(t, r, p, s, lineageOrdersCommands(t, s), "orders")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	out, err := commitFixture(t, r, p, s, v, "commit")
	if err != nil {
		t.Fatal(err)
	}
	return r, out, s, ids
}
func TestLineageOrdersFixtureAndInheritedReads(t *testing.T) {
	r, out, _, ids := lineageOrdersCommitted(t)
	raw, err := os.ReadFile("testdata/lineage/orders/expected.json")
	if err != nil {
		t.Fatal(err)
	}
	var expected struct {
		AggregateSources []ImportLineageValueRef `json:"aggregateSources"`
		MappingKeys      []string                `json:"mappingKeys"`
	}
	if err = json.Unmarshal(raw, &expected); err != nil {
		t.Fatal(err)
	}
	state, err := loadRevisionState(t.Context(), r.db.R, out.Project.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	mappings := []string{}
	for _, n := range state.Nodes {
		if n.Kind == "field_mapping" {
			mappings = append(mappings, n.ExternalKey)
		}
	}
	slices.Sort(mappings)
	if !slices.Equal(mappings, expected.MappingKeys) {
		t.Fatalf("mapping inventory %v", mappings)
	}
	n, err := r.Node(t.Context(), out.Project.ID, out.Revision.ID, ids["m06-total"])
	if err != nil {
		t.Fatal(err)
	}
	attrs, err := decodeLineageMapping(n.Attributes)
	if err != nil {
		t.Fatal(err)
	}
	for i, ref := range expected.AggregateSources {
		want := LineageValueRef{Kind: ref.Kind, NodeID: ids[ref.NodeKey], Collection: ref.Collection, PortKey: ref.PortKey}
		if attrs.Sources[i] != want {
			t.Fatalf("source order %+v", attrs.Sources)
		}
	}
	db, err := r.QueryDatabase(t.Context(), out.Project.ID, DatabaseQueryInput{RevisionID: out.Revision.ID, DatastoreID: ids["database:orders"], FacetKey: "sql", RecordType: "tables"})
	if err != nil || len(db.TableItems) != 1 {
		t.Fatalf("source4 db read %+v %v", db, err)
	}
	flow, err := r.QueryFlow(t.Context(), out.Project.ID, FlowQueryInput{RevisionID: out.Revision.ID, View: "steps", FlowID: ids["flow"]})
	if err != nil || len(flow.StepItems) != 1 {
		t.Fatalf("source4 flow read %+v %v", flow, err)
	}
}
func TestLineageColumnDeletionRequiresMappingClosure(t *testing.T) {
	for _, closure := range []bool{false, true} {
		t.Run(map[bool]string{false: "retained mapping", true: "deleted mapping"}[closure], func(t *testing.T) {
			r, out, old, ids := lineageOrdersCommitted(t)
			p := &out.Project
			in := lineageOrdersInput(t, p)
			in.Mode = "reconcile"
			in.RepositoryID = new(old.RepositoryID)
			in.GraphScope = &GraphScope{Profile: LineageProfile, Status: "complete", Gaps: []string{}}
			in.IdempotencyKey = "delete"
			s, err := r.BeginImport(t.Context(), p.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			cs := lineageOrdersCommands(t, s)
			deleted := map[string]bool{"column:orders:tax": true, "contains:column:orders:tax": true}
			if closure {
				deleted["m05-tax"] = true
				deleted["contains:m05-tax"] = true
			}
			next := []ImportCommand{}
			for _, c := range cs {
				typ, key, _ := commandAddress(c)
				if c.Evidence != nil && deleted[c.Evidence.SubjectKey] {
					continue
				}
				if deleted[key] {
					next = append(next, ImportCommand{Op: "delete_assertion", Deletion: &ImportDeletion{RecordType: typ, ExternalKey: key, ExpectedID: ids[key], Reason: "Removed explicitly"}})
				} else {
					next = append(next, c)
				}
			}
			v, _ := stageRelational(t, r, p, s, next, "delete")
			if !closure {
				if v.State == "ready" {
					t.Fatal("dangling column mapping accepted")
				}
				return
			}
			if v.State != "ready" {
				t.Fatal(v.Diagnostics)
			}
			if _, err := commitFixture(t, r, p, s, v, "delete-commit"); err != nil {
				t.Fatal(err)
			}
			if _, err := r.Node(t.Context(), p.ID, out.Revision.ID, ids["column:orders:tax"]); err != nil {
				t.Fatal("historical column lost", err)
			}
		})
	}
}
func TestLineageFinalCandidateRejectsDroppedFacet(t *testing.T) {
	r, out, s, ids := lineageOrdersCommitted(t)
	state, err := loadRevisionState(t.Context(), r.db.R, out.Project.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	g := &graphCandidate{Nodes: state.Nodes, Edges: state.Edges, Evidence: state.Evidence, Sources: state.Sources}
	for i := range g.Nodes {
		if g.Nodes[i].ID == ids["column:orders:amount"] {
			g.Nodes[i].Attributes = map[string]jsontext.Value{"facets": jsontext.Value(`{}`)}
		}
	}
	var d []ImportDiagnostic
	if err = validateLineageGraph(t.Context(), s, g, &d); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(d, func(d ImportDiagnostic) bool { return strings.Contains(d.Path, "/attributes/sources/0") }) {
		t.Fatalf("missing dangling facet diagnostic %+v", d)
	}
}

func TestLineageSelectedFacetStaleness(t *testing.T) {
	r, out, _, ids := lineageOrdersCommitted(t)
	state, err := loadRevisionState(t.Context(), r.db.R, out.Project.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	g := &graphCandidate{Nodes: state.Nodes, Edges: state.Edges, Evidence: state.Evidence, Sources: state.Sources}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.ID != ids["column:orders:amount"] {
			continue
		}
		facets, _, _ := relationalFacetObject(n.Kind, n.Attributes)
		f, _ := relationalObject(facets["sql"])
		f["freshness"] = relationalRaw(t, AssertionFreshness{Status: "stale", ConfirmedSnapshotID: out.Revision.SourceSnapshotIDs[0], Reasons: []string{"not_reobserved"}})
		facets["sql"] = relationalRaw(t, f)
		n.Attributes = replaceRelationalFacets(n.Kind, n.Attributes, facets)
	}
	markLineageEndpointStaleness(g)
	propagateStaleness(g)
	for _, n := range g.Nodes {
		if n.ID == ids["m04-amount"] && n.Freshness.Status != "stale" {
			t.Fatalf("selected facet proof not propagated %+v", n.Freshness)
		}
	}
}
func TestLineageAPISelectorIdentity(t *testing.T) {
	for _, duplicate := range []bool{false, true} {
		t.Run(map[bool]string{false: "different property", true: "same property different display"}[duplicate], func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "create")
			s, err := r.BeginImport(t.Context(), p.ID, lineageOrdersInput(t, p))
			if err != nil {
				t.Fatal(err)
			}
			cs := lineageOrdersCommands(t, s)
			if duplicate {
				relationalCommand(cs, "kind").Node.Attributes["selector"] = relationalCommand(cs, "response").Node.Attributes["selector"]
			}
			v, _ := stageRelational(t, r, p, s, cs, "selectors")
			if (v.State == "ready") == duplicate {
				t.Fatalf("duplicate=%v ready=%s diagnostics=%+v", duplicate, v.State, v.Diagnostics)
			}
		})
	}
}
func TestLineageGraphReferenceAndAPIFieldLimits(t *testing.T) {
	r, out, s, ids := lineageOrdersCommitted(t)
	state, err := loadRevisionState(t.Context(), r.db.R, out.Project.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	var mapping, field Node
	for _, n := range state.Nodes {
		if n.ID == ids["m06-total"] {
			mapping = n
		}
		if n.ID == ids["request"] {
			field = n
		}
	}
	for _, test := range []string{"fields", "references"} {
		t.Run(test, func(t *testing.T) {
			g := &graphCandidate{Nodes: slices.Clone(state.Nodes), Edges: state.Edges, Evidence: state.Evidence, Sources: state.Sources}
			if test == "fields" {
				for range MaxAPIFieldsPerOperation {
					g.Nodes = append(g.Nodes, field)
				}
			} else {
				for range MaxLineageReferences / 3 {
					g.Nodes = append(g.Nodes, mapping)
				}
			}
			var d []ImportDiagnostic
			err := validateLineageGraph(t.Context(), s, g, &d)
			if err == nil {
				t.Fatal("revision bound ignored")
			}
			assertFault(t, err, "backend_import_limit")
		})
	}
}
func TestLineageSource4SavedFlowAndProposal(t *testing.T) {
	r, out, _, ids := lineageOrdersCommitted(t)
	view, err := r.CreateSavedView(t.Context(), out.Project.ID, CreateSavedViewInput{Name: "Pinned source4", Target: BackendReadTarget{RevisionID: out.Revision.ID}, State: SavedViewState{Flow: &SavedFlowViewState{Kind: "flow", Scope: SavedFlowViewScope{EntrypointID: ids["http"], FlowID: ids["flow"]}, Positions: []SavedViewPosition{}, CollapsedGroupIDs: []string{}}}, IdempotencyKey: "view"})
	if err != nil {
		t.Fatal(err)
	}
	if view == nil {
		t.Fatal("no saved flow")
	}
	detail, err := r.CreateProposal(t.Context(), out.Project.ID, proposalCreateInput(out, ids, "source4-proposal"))
	if err != nil {
		t.Fatal(err)
	}
	apply := proposalApplyInput(t, r, detail, "source4-apply", proposalNullable(ids, "nullable", true))
	applied, err := r.ApplyProposal(t.Context(), out.Project.ID, detail.Proposal.ID, apply)
	if err != nil {
		t.Fatal(err)
	}
	graph, err := r.QueryGraph(t.Context(), out.Project.ID, GraphQueryInput{Proposal: &ProposalReadTarget{ProposalID: detail.Proposal.ID, ProposalRevisionID: applied.Revision.ID}, RecordType: "nodes", Kind: "field_mapping"})
	if err != nil || len(graph.Nodes) != 10 {
		t.Fatalf("inherited mapping proposal %+v %v", graph, err)
	}
}

func TestLineageSourceComparisonTypedChanges(t *testing.T) {
	r, out, old, ids := lineageOrdersCommitted(t)
	p := &out.Project
	in := lineageOrdersInput(t, p)
	in.Mode = "reconcile"
	in.RepositoryID = new(old.RepositoryID)
	in.GraphScope = &GraphScope{Profile: LineageProfile, Status: "complete", Gaps: []string{}}
	in.IdempotencyKey = "change"
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	cs := lineageOrdersCommands(t, s)
	relationalCommand(cs, "m06-total").Node.Attributes["transform"] = jsontext.Value(`{"kind":"compute","description":"Explicit arithmetic shape","redacted":true}`)
	relationalCommand(cs, "response").Node.Attributes["selector"] = jsontext.Value(`{"kind":"body","path":[{"property":"grandTotal"}]}`)
	v, _ := stageRelational(t, r, p, s, cs, "change")
	if v.State != "ready" {
		t.Fatal(v.Diagnostics)
	}
	next, err := commitFixture(t, r, p, s, v, "change-commit")
	if err != nil {
		t.Fatal(err)
	}
	comparison, err := r.CompareRevisions(t.Context(), p.ID, CompareRevisionsInput{FromRevisionID: out.Revision.ID, ToRevisionID: next.Revision.ID, RecordType: "node", Limit: 500})
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"m06-total", "response"} {
		if !slices.ContainsFunc(comparison.Items, func(item ComparisonItem) bool {
			return item.ID == ids[key] && slices.Contains(item.ChangeKinds, "modified") && slices.ContainsFunc(item.ChangedPaths, func(path string) bool { return strings.HasPrefix(path, "/attributes/") })
		}) {
			t.Fatalf("typed change missing %s %+v", key, comparison.Items)
		}
	}
}
