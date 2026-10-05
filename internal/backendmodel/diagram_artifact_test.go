package backendmodel

import (
	"context"
	"strconv"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
)

type diagramCountingScenarioReader struct {
	ScenarioArtifactReader
	inspections int
}

func (r *diagramCountingScenarioReader) ArtifactInspectionSnapshot(ctx context.Context, id, revision int64) (*designscenario.ArtifactInspectionSnapshot, error) {
	r.inspections++
	return r.ScenarioArtifactReader.ArtifactInspectionSnapshot(ctx, id, revision)
}

func TestDiagramArtifactReferencesSharePinnedRequest(t *testing.T) {
	service, base, ids, scenario := artifactServiceFixture(t)
	pinned, _ := applyArtifactTest(t, service, base.Project.ID, scenarioSet(base, ids, scenario), "diagram-artifact-pin")
	page, err := service.Query(t.Context(), base.Project.ID, ArtifactQueryInput{RevisionID: pinned.Revision.ID, Artifact: ArtifactKey{Kind: "design_scenario", ID: strconv.FormatInt(scenario.Scenario.ID, 10)}, View: "sequence", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	refs := []DiagramRef{}
	for _, item := range page.Items {
		if validHash(item.ObjectHash) {
			refs = append(refs, DiagramRef{Kind: "artifact", Locator: new(item.Locator), RowID: item.ID})
		}
	}
	if len(refs) < 2 {
		t.Fatal("fixture requires two distinct exact artifact rows")
	}
	doc := diagramTestDocument(pinned.Revision.ID)
	doc.Payload.Elements[0].Refs = []DiagramRef{refs[0]}
	second := doc.Payload.Elements[0]
	second.ID = "10000000-0000-4000-8000-000000000009"
	second.Role = "person"
	second.Refs = []DiagramRef{refs[1]}
	doc.Payload.Elements = append(doc.Payload.Elements, second)
	counter := &diagramCountingScenarioReader{ScenarioArtifactReader: service.scenarios}
	reader := NewArtifactService(service.repo, service.api, counter)
	version, err := service.repo.CreateDiagram(reader.DiagramContext(t.Context()), base.Project.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "diagram-artifacts"})
	if err != nil {
		t.Fatal(err)
	}
	if version.Document.Payload.Elements[0].Refs[0].RowID != refs[0].RowID {
		t.Fatal("artifact locator substituted")
	}
	if counter.inspections != 1 {
		t.Fatalf("artifact snapshot budget reset per reference: %d reads", counter.inspections)
	}
}
