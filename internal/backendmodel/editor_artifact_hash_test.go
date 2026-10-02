package backendmodel

import (
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func editorTestBinding(kind, id string, s EditorSelector) EditorBinding {
	return EditorBinding{ArtifactKind: kind, ArtifactID: id, Selector: s, SourceNodeIDs: []string{apiTestID}, SourceLabels: []string{"source"}, ObjectHash: strings.Repeat("d", 64), LastKnownLabel: "object", Origin: "manual", Reason: "manual"}
}
func TestEditorArtifactSemanticOrderingAndSensitivity(t *testing.T) {
	pins, api := apiTestVector()
	pins = append(pins, ArtifactPin{Kind: "design_scenario", ID: "3", RevisionID: "4", ContentHash: strings.Repeat("c", 64)})
	editor := []EditorBinding{editorTestBinding("api_design", "1", EditorSelector{Kind: "state_diagram", DiagramID: "orders"}), editorTestBinding("design_scenario", "3", EditorSelector{Kind: "participant", ParticipantID: "actor"})}
	source, anchor := strings.Repeat("a", 64), strings.Repeat("f", 64)
	h, err := ArtifactSemanticHash(source, anchor, pins, api, editor)
	if err != nil {
		t.Fatal(err)
	}
	if h != editorGoldenSHA {
		t.Fatalf("golden digest %s, want %s", h, editorGoldenSHA)
	}
	sum := sha256.Sum256([]byte(editorGoldenCanonical))
	if fmt.Sprintf("%x", sum) != editorGoldenSHA {
		t.Fatal("literal golden corrupt")
	}
	// The golden's field/tree choice is independent of production serialization.
	for _, alter := range []func([]ArtifactPin, []APIArtifactBinding, []EditorBinding){
		func(p []ArtifactPin, a []APIArtifactBinding, e []EditorBinding) { slices.Reverse(p); slices.Reverse(e) },
		func(p []ArtifactPin, a []APIArtifactBinding, e []EditorBinding) {
			a[0].Reason = "changed"
			a[0].SourceLastKnownLabel = "new"
			a[0].Ref.LastKnownLabel = "new"
			e[0].Reason = "new"
			e[0].LastKnownLabel = "new"
			e[0].SourceLabels = []string{"new"}
		},
	} {
		p, a, e := slices.Clone(pins), slices.Clone(api), slices.Clone(editor)
		alter(p, a, e)
		got, err := ArtifactSemanticHash(source, anchor, p, a, e)
		if err != nil || got != h {
			t.Fatalf("invariance %s %v", got, err)
		}
	}
	for _, alter := range []func([]ArtifactPin, []APIArtifactBinding, []EditorBinding){
		func(p []ArtifactPin, a []APIArtifactBinding, e []EditorBinding) { p[1].RevisionID = "5" },
		func(p []ArtifactPin, a []APIArtifactBinding, e []EditorBinding) {
			p[1].ContentHash = strings.Repeat("e", 64)
		},
		func(p []ArtifactPin, a []APIArtifactBinding, e []EditorBinding) {
			e[0].ObjectHash = strings.Repeat("e", 64)
		},
		func(p []ArtifactPin, a []APIArtifactBinding, e []EditorBinding) {
			e[1].SourceNodeIDs = []string{"018f2396-cf02-7000-8000-000000000002"}
		},
		func(p []ArtifactPin, a []APIArtifactBinding, e []EditorBinding) {
			e[1].Selector.ParticipantID = "other"
		},
		func(p []ArtifactPin, a []APIArtifactBinding, e []EditorBinding) { p[1].ID = "9"; e[1].ArtifactID = "9" },
	} {
		p, a, e := slices.Clone(pins), slices.Clone(api), slices.Clone(editor)
		alter(p, a, e)
		got, err := ArtifactSemanticHash(source, anchor, p, a, e)
		if err != nil || got == h {
			t.Fatalf("sensitivity %s %v", got, err)
		}
	}
	if other, err := ArtifactSemanticHash(strings.Repeat("b", 64), anchor, pins, api, editor); err != nil || other == h {
		t.Fatal("source content anchor ignored")
	}
}

func TestEditorArtifactBranchRoundTripsAndFullIdentity(t *testing.T) {
	pins, api := apiTestVector()
	source, anchor := strings.Repeat("e", 64), strings.Repeat("f", 64)
	binding := editorTestBinding("api_design", "1", EditorSelector{Kind: "state_diagram", DiagramID: "orders"})
	a, err := ArtifactSemanticHash(source, anchor, pins, api, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ArtifactSemanticHash(source, anchor, pins, api, []EditorBinding{binding})
	if err != nil || a == b {
		t.Fatal("editor addition ignored")
	}
	again, err := ArtifactSemanticHash(source, anchor, pins, api, nil)
	if err != nil || again != a {
		t.Fatal("A-B-A history dependent")
	}
	projection, err := ArtifactSemanticHash(source, anchor, pins, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = ArtifactSemanticHash(source, anchor, pins, nil, []EditorBinding{binding})
	if err != nil {
		t.Fatal(err)
	}
	again, err = ArtifactSemanticHash(source, anchor, pins, nil, nil)
	if err != nil || again != projection {
		t.Fatal("projection return hash drift")
	}
	context := ArtifactContext{DocumentVersion: EditorArtifactDocumentVersion, SourceContentHash: source, SourceSemanticHash: anchor}
	if ArtifactContextUsesV1(pins, context) {
		t.Fatal("projection-only pin downgraded")
	}
	context.APIBindings = api
	if !ArtifactContextUsesV1(pins, context) {
		t.Fatal("valid legacy return refused")
	}
	cleared, err := ArtifactSemanticHash(source, anchor, nil, nil, nil)
	if err != nil || cleared != anchor {
		t.Fatal("clear not source anchor")
	}
	scenarioPin := []ArtifactPin{{Kind: "design_scenario", ID: "1", RevisionID: "2", ContentHash: strings.Repeat("a", 64)}}
	one := editorTestBinding("design_scenario", "1", EditorSelector{Kind: "state_diagram", DiagramID: "d", EmbeddedContractID: "copy-a"})
	two := one
	two.Selector.EmbeddedContractID = "copy-b"
	if err = ValidateArtifactVector(scenarioPin, nil, []EditorBinding{one, two}); err != nil {
		t.Fatal("many-to-one/two copies rejected", err)
	}
	identity1, _ := EditorArtifactComparisonIdentity(one)
	identity2, _ := EditorArtifactComparisonIdentity(two)
	if identity1 == identity2 {
		t.Fatal("two copies identity collision")
	}
	two = one
	two.SourceNodeIDs = []string{"018f2396-cf02-7000-8000-000000000002"}
	identity2, _ = EditorArtifactComparisonIdentity(two)
	if identity1 != identity2 {
		t.Fatal("comparison keyed by source node")
	}
	group, _ := ArtifactGroupIdentity(scenarioPin[0])
	if group == identity1 {
		t.Fatal("group/editor collision")
	}
}

func TestEditorArtifactCandidateKeepsOriginalCommand(t *testing.T) {
	input := PreviewArtifactPinsInput{BaseRevisionID: apiTestID, ExpectedVersion: 1, Commands: []ArtifactPinCommand{{Type: "set_artifact_pin", Artifact: ArtifactKey{"api_design", "1"}, RevisionID: "2", APIBindings: []APIPinBindingInput{}, EditorBindings: []EditorBindingInput{}, Reason: "original"}}}
	candidate := ArtifactPinsPreview{}
	first, err := ArtifactCandidateHash(input, candidate)
	if err != nil {
		t.Fatal(err)
	}
	input.Commands[0].Reason = "edited"
	second, err := ArtifactCandidateHash(input, candidate)
	if err != nil || first == second {
		t.Fatal("candidate lost original reason")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"apiBindings":[]`) || !strings.Contains(string(raw), `"editorBindings":[]`) {
		t.Fatalf("empty full sets omitted: %s", raw)
	}
}

const editorGoldenSHA = "1661387e0b632ee1f6463ac1ed58d8b187418f076fa4e3280e6869c72f2cfe09"
const editorGoldenCanonical = `{"apiBindings":[{"ref":{"artifactId":"1","contentHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","kind":"api_design","objectHash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","resolvedPointer":"/paths/~1a/get","revisionId":"2","selector":{"objectKey":"op"}},"sourceKind":"http_operation","sourceNodeId":"018f2396-cf02-7000-8000-000000000001"}],"domain":"backend-editor-artifacts-semantic-v1","editorBindings":[{"artifactId":"1","artifactKind":"api_design","objectHash":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","selector":{"diagramId":"orders","kind":"state_diagram"},"sourceNodeIds":["018f2396-cf02-7000-8000-000000000001"]},{"artifactId":"3","artifactKind":"design_scenario","objectHash":"dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd","selector":{"kind":"participant","participantId":"actor"},"sourceNodeIds":["018f2396-cf02-7000-8000-000000000001"]}],"pins":[{"contentHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","id":"1","kind":"api_design","revisionId":"2"},{"contentHash":"cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","id":"3","kind":"design_scenario","revisionId":"4"}],"sourceContentHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`
