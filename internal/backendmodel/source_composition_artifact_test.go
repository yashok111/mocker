package backendmodel

import (
	"reflect"
	"slices"
	"testing"
)

func TestSource6WholeCompositionCarriesV2ArtifactContext(t *testing.T) {
	service, source, ids, scenario := artifactServiceFixture(t)
	pinned, _ := applyArtifactTest(t, service, source.Project.ID, scenarioSet(source, ids, scenario), "pin-v2")
	in := eventsProfileInput(lineageOrdersInput(t, &pinned.Project), true)
	in.Mode = "reconcile"
	in.RepositoryID = new(source.Project.Repositories[0].ID)
	in.GraphScope = &GraphScope{Profile: EventsProfile, Status: "partial", Gaps: []string{"Fixture extension"}}
	in.IdempotencyKey = "source5"
	session, err := service.repo.BeginImport(t.Context(), source.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	commands := lineageOrdersCommands(t, session)
	preview, _ := stageRelational(t, service.repo, &pinned.Project, session, commands, "source5")
	if preview.State != "ready" {
		t.Fatalf("source5 extension: %+v", preview)
	}
	base, err := commitFixture(t, service.repo, &pinned.Project, session, preview, "source5-commit")
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadRevisionState(t.Context(), service.repo.db.R, source.Project.ID, base.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ArtifactContext == nil || state.ArtifactContext.DocumentVersion != EditorArtifactDocumentVersion || len(state.ArtifactContext.EditorBindings) != 2 {
		t.Fatal("fixture lacks real v2 context")
	}
	before := immutableBytes(t, service.repo)
	ownerBefore := artifactOwnerRows(t, service.repo)
	next := source6Input(t, &base.Project)
	next.Manifest.RepositoryName = "composed-sidecar"
	next.ProfileExtension = &ImportProfileExtension{FromProfile: EventsProfile, ToProfile: ComposedProfile}
	next.IdempotencyKey = "source6"
	_, out := commitSource6Fixture(t, service.repo, &base.Project, next)
	graph, err := service.repo.ResolveSourceGraph(t.Context(), source.Project.ID, out.Revision.ID)
	if err != nil {
		t.Fatal(err)
	}
	context := graph.State.ArtifactContext
	if context == nil || !reflect.DeepEqual(context.EditorBindings, state.ArtifactContext.EditorBindings) || !slices.Equal(out.Revision.ArtifactPins, base.Revision.ArtifactPins) {
		t.Fatal("source6 lost exact frozen artifact bindings")
	}
	if context.SourceContentHash != graph.SourceContentHash || context.SourceContentHash == state.ArtifactContext.SourceContentHash {
		t.Fatal("artifact context reused graph-only or old source anchor")
	}
	semantic, err := source6SemanticHash(graph)
	if err != nil || semantic != out.Revision.SemanticHash {
		t.Fatalf("persisted semantic=%s rebuilt=%s err=%v", out.Revision.SemanticHash, semantic, err)
	}
	sourceOnly := *graph
	sourceOnly.State = graph.State
	sourceOnly.State.ArtifactContext = nil
	sourceOnly.State.APIArtifactContext = nil
	sourceOnly.State.Revision.ArtifactPins = nil
	anchor, err := source6SemanticHash(&sourceOnly)
	if err != nil || anchor != context.SourceSemanticHash {
		t.Fatalf("recursive/wrong source anchor: %s vs %s %v", anchor, context.SourceSemanticHash, err)
	}
	after := immutableBytes(t, service.repo)
	for key, value := range before {
		if after[key] != value {
			t.Fatalf("old bytes changed: %s", key)
		}
	}
	if !reflect.DeepEqual(ownerBefore, artifactOwnerRows(t, service.repo)) {
		t.Fatal("source composition mutated live artifact owners")
	}
}
