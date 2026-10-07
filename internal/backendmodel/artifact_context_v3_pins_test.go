package backendmodel

import (
	"database/sql"
	"strings"
	"testing"
)

// v3ImportedHead imports a source6 project through the portable path, which
// stores an artifact-context-v3 for every imported source revision
// (portable_source_write.go). The imported project's head is the shape the
// V1/V2 pin paths used to read as "no pins" (review 2026-10-06, C6).
func v3ImportedHead(t *testing.T) (*Repo, *Project) {
	t.Helper()
	r, model, mapping := portableOwnerFixture(t)
	remapped, err := RemapPortableModel(model, mapping)
	if err != nil {
		t.Fatal(err)
	}
	var imported *PortableModel
	if err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		var err error
		imported, err = r.ImportPortableModelTx(t.Context(), tx, *remapped, PortableImportOptions{Remap: mapping, OriginProjectID: model.Project.ID})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	p, err := r.Get(t.Context(), imported.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	state, err := loadSourceState(t.Context(), r.db.R, p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if state.ArtifactContextV3 == nil {
		t.Fatal("fixture head carries no v3 context")
	}
	return r, p
}

// review 2026-10-06, F55: the source anchor of a v3 head is the frozen
// SourceSemanticHash, never the artifact-bound v3 semantic hash.
func TestArtifactSourceAnchorsIgnoreV3Context(t *testing.T) {
	t.Parallel()
	r, p := v3ImportedHead(t)
	state, err := loadSourceState(t.Context(), r.db.R, p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	content, anchor, err := artifactSourceAnchors(t.Context(), r.db.R, state)
	if err != nil {
		t.Fatal(err)
	}
	if content != state.ArtifactContextV3.SourceContentHash || anchor != state.ArtifactContextV3.SourceSemanticHash {
		t.Fatalf("anchors %s/%s; want frozen v3 anchors %s/%s (revision hash %s)", content, anchor,
			state.ArtifactContextV3.SourceContentHash, state.ArtifactContextV3.SourceSemanticHash, state.Revision.SemanticHash)
	}
}

// review 2026-10-06, F60 and F92: a legacy pin preview on a v3 head used to
// build an empty context, drop the namespaced groups and report canApply,
// while Apply then always failed in loadArtifactContext.
func TestLegacyPinPreviewRefusesV3Head(t *testing.T) {
	t.Parallel()
	r, p := v3ImportedHead(t)
	editor := NewArtifactService(r, nil, nil)
	_, err := editor.Preview(t.Context(), p.ID, PreviewArtifactPinsInput{BaseRevisionID: p.CurrentRevisionID, ExpectedVersion: p.Version, Commands: []ArtifactPinCommand{{Type: "remove_artifact_pin", Artifact: ArtifactKey{"design_scenario", "1"}, Reason: "Drop"}}})
	assertFault(t, err, "backend_artifact_pins_unsupported")
	api := NewAPIArtifactService(r, nil)
	_, err = api.Preview(t.Context(), p.ID, PreviewAPIPinsInput{BaseRevisionID: p.CurrentRevisionID, ExpectedVersion: p.Version, Commands: []APIPinCommand{{Type: "remove_api_pin", ArtifactID: "1", Reason: "Drop"}}})
	assertFault(t, err, "backend_api_pins_unsupported")
}

// review 2026-10-06, F97: one source UUID belongs to at most one API binding
// across the full vector, so two namespace groups cannot both bind it.
func TestV3RejectsSourceBoundInTwoNamespaces(t *testing.T) {
	t.Parallel()
	pin := ArtifactPin{Kind: "api_design", ID: "1", RevisionID: "1", ContentHash: strings.Repeat("a", 64)}
	binding := APIArtifactBinding{SourceNodeID: "33333333-3333-4333-8333-333333333333", SourceKind: "api_field", Origin: "manual", Reason: "Manual", Ref: ArtifactRef{Kind: "api_design", ArtifactID: "1", RevisionID: "1", ContentHash: pin.ContentHash, ObjectHash: strings.Repeat("d", 64), Selector: APIArtifactSelector{JSONPointer: "/components/schemas/Order"}, ResolvedPointer: "/components/schemas/Order"}}
	group := func(scope string) ArtifactNamespaceGroup {
		return ArtifactNamespaceGroup{Namespace: ArtifactNamespace{Scope: scope, InstallationID: "11111111-1111-4111-8111-111111111111"}, Pins: []ArtifactPin{pin}, APIBindings: []APIArtifactBinding{binding}, EditorBindings: []EditorBinding{}}
	}
	c := ArtifactContextV3{DocumentVersion: ArtifactContextV3Version, SourceContentHash: strings.Repeat("b", 64), SourceSemanticHash: strings.Repeat("c", 64), Groups: []ArtifactNamespaceGroup{group("local")}}
	if _, err := EncodeArtifactContextV3(c); err != nil {
		t.Fatal(err)
	}
	c.Groups = append(c.Groups, group("foreign"))
	if _, err := EncodeArtifactContextV3(c); err == nil {
		t.Fatal("one source bound in two namespace groups")
	}
}

// review 2026-10-06, F108: a retained namespaced ref whose namespace/pin left
// the target is a historical gap, as a retained plain artifact ref is; only a
// ref the caller newly introduces is refused.
func TestNamespacedRefLeavingTargetIsHistoricalGap(t *testing.T) {
	t.Parallel()
	pin := ArtifactPin{Kind: "api_design", ID: "1", RevisionID: "1", ContentHash: strings.Repeat("a", 64)}
	for _, scope := range []string{"local", "foreign"} {
		ns := ArtifactNamespace{Scope: scope, InstallationID: "11111111-1111-4111-8111-111111111111"}
		ref := DiagramRef{Kind: "namespaced_artifact", NamespacedLocator: &NamespacedDiagramLocator{Namespace: ns, Locator: ArtifactProjectionLocator{Pin: pin, View: "api_operations"}}, RowID: "row"}
		for name, g := range map[string]*EffectiveGraphSnapshot{
			"no v3":     {},
			"other pin": {Pins: EffectiveGraphPins{ArtifactContextV3: &ArtifactContextV3{Groups: []ArtifactNamespaceGroup{{Namespace: ns}}}}},
		} {
			resolver := newDiagramArtifactResolver(t.Context(), g)
			gaps, err := diagramReferenceGaps(resolver, "e", []DiagramRef{ref}, []DiagramRef{ref}, map[string]bool{}, map[string]bool{})
			if err != nil || len(gaps) != 1 || !strings.HasPrefix(gaps[0].Code, "historical_ref:") {
				t.Fatalf("%s/%s retained: %+v %v", scope, name, gaps, err)
			}
			if _, err := diagramReferenceGaps(resolver, "e", []DiagramRef{ref}, nil, map[string]bool{}, map[string]bool{}); err == nil {
				t.Fatalf("%s/%s: new ref outside the target accepted", scope, name)
			}
		}
	}
}

// review 2026-10-06, F103: on a v3 target the only valid message mapping is a
// namespaced_artifact ref; it must count as an implementation, and two events
// sharing it must be reported as a shared message.
func TestBusinessMapCountsNamespacedMessageImplementation(t *testing.T) {
	t.Parallel()
	ns := ArtifactNamespace{Scope: "local", InstallationID: "11111111-1111-4111-8111-111111111111"}
	ref := DiagramRef{Kind: "namespaced_artifact", RowID: "row", NamespacedLocator: &NamespacedDiagramLocator{Namespace: ns, Locator: ArtifactProjectionLocator{View: "event_model", Owner: ArtifactOwnerAddress{MessageID: "m"}}}}
	p := &BusinessMapPayload{Elements: []BusinessElement{{ID: "a", Role: "business_event", Refs: []DiagramRef{ref}}, {ID: "b", Role: "business_event", Refs: []DiagramRef{ref}}}}
	gaps := businessMapImplementationGaps(&EffectiveGraphSnapshot{}, p)
	shared := 0
	for _, g := range gaps {
		if g.Code == "unresolved_business_message" {
			t.Fatalf("namespaced message mapping reported as a gap: %+v", gaps)
		}
		if strings.HasPrefix(g.Code, "shared_business_message:") {
			shared++
		}
	}
	if shared != 2 {
		t.Fatalf("shared namespaced message not detected: %+v", gaps)
	}
}
