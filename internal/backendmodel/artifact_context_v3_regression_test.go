package backendmodel

import (
	"encoding/json/v2"
	"github.com/yashok111/mocker/internal/testkit"
	"strings"
	"testing"
)

func TestV3ProposalAndCompositionPreserveForeignNamespace(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	p := createProject(t, r, "v3-source")
	_, base := commitSource6Fixture(t, r, p, source6Input(t, p))
	g, err := loadSourceGraph(t.Context(), db.R, p.ID, base.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	anchor, err := source6SemanticHash(g)
	if err != nil {
		t.Fatal(err)
	}
	installation, err := r.InstallationID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	c := ArtifactContextV3{DocumentVersion: ArtifactContextV3Version, SourceContentHash: g.SourceContentHash, SourceSemanticHash: anchor, Groups: []ArtifactNamespaceGroup{{Namespace: ArtifactNamespace{Scope: "foreign", InstallationID: installation}, Pins: []ArtifactPin{{Kind: "api_design", ID: "1", RevisionID: "1", ContentHash: strings.Repeat("a", 64)}}}}}
	raw, err := EncodeArtifactContextV3(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := testkit.ExecBackendOwner(t.Context(), db.W, `INSERT INTO backend_revision_api_artifacts VALUES(?,?,?,?)`, base.Revision.ID, c.SourceContentHash, c.SourceSemanticHash, string(raw)); err != nil {
		t.Fatal(err)
	}
	revision := base.Revision
	revision.SemanticHash, err = ArtifactContextV3SemanticHash(c)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.W.ExecContext(t.Context(), `UPDATE backend_revisions SET document=? WHERE id=?`, string(raw), revision.ID); err != nil {
		t.Fatal(err)
	}
	proposal, err := r.CreateChangeProposal(t.Context(), p.ID, CreateChangeProposalInput{Name: "Keep foreign", BaseRevisionID: revision.ID, IdempotencyKey: "v3-proposal"})
	if err != nil {
		t.Fatal(err)
	}
	if proposal.Revision.ArtifactContextV3 == nil || len(proposal.Revision.ArtifactPins) != 0 || len(proposal.Revision.ArtifactContextV3.Groups) != 1 {
		t.Fatal("proposal lost namespace")
	}
	got, err := r.GetChangeProposal(t.Context(), p.ID, proposal.Proposal.ID, GetChangeProposalInput{})
	if err != nil {
		t.Fatal(err)
	}
	if got.Revision.ArtifactContextV3 == nil {
		t.Fatal("persistent proposal lost namespace")
	}
	// Source composition carries the complete context and changes only source anchors.
	next := source6Input(t, &base.Project)
	next.IdempotencyKey = "next-v3"
	next.Manifest.RepositoryName = "other"
	_, composed := commitSource6Fixture(t, r, &base.Project, next)
	graph, err := r.ResolveEffectiveGraph(t.Context(), p.ID, BackendReadTarget{RevisionID: composed.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	if graph.Pins.ArtifactContextV3 == nil || graph.Pins.ArtifactContextV3.Groups[0].Namespace.Scope != "foreign" {
		t.Fatal("composition dropped namespace")
	}
}

func TestV3DiagramForeignReferenceIsGapWithoutOwnerLookup(t *testing.T) {
	t.Parallel()
	id := "11111111-1111-4111-8111-111111111111"
	pin := ArtifactPin{Kind: "api_design", ID: "1", RevisionID: "1", ContentHash: strings.Repeat("a", 64)}
	ns := ArtifactNamespace{Scope: "foreign", InstallationID: id}
	g := &EffectiveGraphSnapshot{Pins: EffectiveGraphPins{ArtifactContextV3: &ArtifactContextV3{Groups: []ArtifactNamespaceGroup{{Namespace: ns, Pins: []ArtifactPin{pin}}}}}}
	ref := DiagramRef{Kind: "namespaced_artifact", NamespacedLocator: &NamespacedDiagramLocator{Namespace: ns, Locator: ArtifactProjectionLocator{Pin: pin}}, RowID: "row"}
	resolver := newDiagramArtifactResolver(t.Context(), g)
	gaps, err := diagramReferenceGaps(resolver, id, []DiagramRef{ref}, nil, map[string]bool{}, map[string]bool{})
	if err != nil || len(gaps) != 1 || gaps[0].Code != "foreign_artifact_unresolved" {
		t.Fatal(gaps, err)
	}
	ref.NamespacedLocator.Namespace.Scope = "local"
	if _, err := resolver.resolve(ref); err == nil {
		t.Fatal("namespace mismatch accepted")
	}
}
