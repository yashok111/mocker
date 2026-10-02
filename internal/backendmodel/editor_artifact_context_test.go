package backendmodel

import (
	"encoding/json/v2"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

func TestEditorArtifactContextBranches(t *testing.T) {
	anchor := strings.Repeat("a", 64)
	source := strings.Repeat("b", 64)
	apiPins, apiBindings := apiTestVector()
	cases := []struct {
		name   string
		pins   []ArtifactPin
		api    []APIArtifactBinding
		editor []EditorBinding
		v1     bool
	}{
		{"empty", nil, nil, nil, true}, {"legacy", apiPins, apiBindings, nil, true},
		{"projection API", apiPins, nil, nil, false},
		{"scenario", []ArtifactPin{{Kind: "design_scenario", ID: "1", RevisionID: "2", ContentHash: anchor}}, nil, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := ArtifactContext{DocumentVersion: EditorArtifactDocumentVersion, SourceContentHash: source, SourceSemanticHash: anchor, APIBindings: tc.api, EditorBindings: tc.editor}
			if ArtifactContextUsesV1(tc.pins, c) != tc.v1 {
				t.Fatal("wrong branch")
			}
			raw, err := EncodeArtifactContext(c, tc.pins)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(raw), "documentVersion") == tc.v1 {
				t.Fatal("wrong encoding branch")
			}
			out, err := DecodeArtifactContext(raw, tc.pins)
			if err != nil {
				t.Fatal(err)
			}
			if (out.DocumentVersion == "") != tc.v1 {
				t.Fatal("wrong decoding branch")
			}
			h, err := ArtifactSemanticHash(source, anchor, tc.pins, tc.api, tc.editor)
			if err != nil {
				t.Fatal(err)
			}
			if tc.name == "empty" && h != anchor {
				t.Fatal("clear changed anchor")
			}
			if tc.name == "legacy" {
				old, e := APIArtifactSemanticHash(source, anchor, tc.pins, tc.api)
				if e != nil || old != h {
					t.Fatal("legacy domain changed")
				}
			}
		})
	}
	for _, version := range []string{`null`, `"future"`, `1`} {
		raw := []byte(`{"documentVersion":` + version + `,"sourceContentHash":"` + source + `","sourceSemanticHash":"` + anchor + `","apiBindings":[],"editorBindings":[]}`)
		if _, err := DecodeArtifactContext(raw, nil); err == nil {
			t.Fatal("unknown version decoded")
		}
	}
	// V1 decoder keeps the old ordinary JSON branch; new tagged branch is strict.
	raw, _ := json.Marshal(APIArtifactContext{source, anchor, apiBindings})
	out, err := DecodeArtifactContext(raw, apiPins)
	if err != nil || out.DocumentVersion != "" {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"documentVersion":"backend-editor-artifacts-v1","sourceContentHash":"` + source + `","sourceSemanticHash":"` + anchor + `","apiBindings":[],"editorBindings":null}`, `{"documentVersion":"backend-editor-artifacts-v1","documentVersion":"backend-editor-artifacts-v1","sourceContentHash":"` + source + `","sourceSemanticHash":"` + anchor + `","apiBindings":[],"editorBindings":[]}`} {
		if _, err := DecodeArtifactContext([]byte(raw), nil); err == nil {
			t.Fatal("invalid tagged context admitted")
		}
	}
}

func TestEditorArtifactCompleteSelectedGroupRoster(t *testing.T) {
	pins, api := apiTestVector()
	one := editorTestBinding("api_design", "1", EditorSelector{Kind: "state_diagram", DiagramID: "first"})
	two := editorTestBinding("api_design", "1", EditorSelector{Kind: "response_rule", RuleID: "second"})
	other := editorTestBinding("design_scenario", "9", EditorSelector{Kind: "participant", ParticipantID: "p"})
	c := ArtifactContext{APIBindings: api, EditorBindings: []EditorBinding{two, other, one}}
	a, e, err := SelectedArtifactBindings(c, ArtifactKey{"api_design", "1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 1 || len(e) != 2 {
		t.Fatal("selected group lost frozen bindings")
	}
	// Neither paged items nor a live owner/source resolver is an input to roster.
	page := ArtifactProjectionPage{RevisionID: apiTestID, SemanticHash: strings.Repeat("a", 64), Pins: pins, SelectedPin: pins[0], APIBindings: a, EditorBindings: e, BindingsComplete: true, Items: []ArtifactProjectionItem{}, Resolution: ArtifactResolution{Status: "missing"}}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"bindingsComplete":true`) || !strings.Contains(string(raw), `"items":[]`) {
		t.Fatal(string(raw))
	}
	e[0].SourceLabels[0] = "edited"
	e[0].SourceNodeIDs[0] = "edited"
	if c.EditorBindings[0].SourceLabels[0] != "source" {
		t.Fatal("roster mutated context")
	}
}

func TestEditorArtifactLegacyOpaqueCompatibility(t *testing.T) {
	// B24 permits opaque non-API groups without normalizing/resolving them;
	// even duplicate opaque entries remain hash input instead of disappearing.
	pins, api := apiTestVector()
	opaque := ArtifactPin{Kind: "opaque", ID: "not-a-number", RevisionID: "legacy"}
	pins = append(pins, opaque, opaque)
	source, anchor := strings.Repeat("a", 64), strings.Repeat("b", 64)
	old, err := APIArtifactSemanticHash(source, anchor, pins, api)
	if err != nil {
		t.Fatal(err)
	}
	newHash, err := ArtifactSemanticHash(source, anchor, pins, api, nil)
	if err != nil || old != newHash {
		t.Fatal("opaque legacy domain changed", err)
	}
	pins = append(pins, ArtifactPin{Kind: "design_scenario", ID: "9", RevisionID: "8", ContentHash: source})
	if err = ValidateArtifactVector(pins, api, nil); err != nil {
		t.Fatal("v2 rejected retained opaque legacy groups", err)
	}
}

func editorLimitFixture(t *testing.T, bytes int) (ArtifactContext, []ArtifactPin) {
	t.Helper()
	c := ArtifactContext{DocumentVersion: EditorArtifactDocumentVersion, SourceContentHash: strings.Repeat("a", 64), SourceSemanticHash: strings.Repeat("b", 64), APIBindings: []APIArtifactBinding{}, EditorBindings: make([]EditorBinding, 11)}
	pins := []ArtifactPin{{Kind: "design_scenario", ID: "1", RevisionID: "2", ContentHash: strings.Repeat("c", 64)}}
	for i := range c.EditorBindings {
		c.EditorBindings[i] = editorTestBinding("design_scenario", "1", EditorSelector{Kind: "participant", ParticipantID: fmt.Sprint(i)})
		c.EditorBindings[i].SourceNodeIDs = make([]string, 100)
		c.EditorBindings[i].SourceLabels = make([]string, 100)
		for j := range c.EditorBindings[i].SourceNodeIDs {
			c.EditorBindings[i].SourceNodeIDs[j] = fmt.Sprintf("018f2396-cf02-7000-8000-%012d", j+1)
			c.EditorBindings[i].SourceLabels[j] = strings.Repeat("x", 3000)
		}
	}
	raw, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	remaining := bytes - len(raw)
	if remaining < 0 {
		t.Fatal("fixture base larger than target")
	}
	for i := range c.EditorBindings {
		for j := range c.EditorBindings[i].SourceLabels {
			extra := min(1096, remaining)
			c.EditorBindings[i].SourceLabels[j] += strings.Repeat("x", extra)
			remaining -= extra
		}
	}
	if remaining != 0 {
		t.Fatal("fixture capacity too small")
	}
	return c, pins
}

func TestEditorArtifactContextNewV2ResourceAdmission(t *testing.T) {
	const limit = 4 << 20
	c, pins := editorLimitFixture(t, limit)
	raw, err := EncodeArtifactContext(c, pins)
	if err != nil || len(raw) != limit {
		t.Fatal("inclusive context bound", len(raw), err)
	}
	if size, err := CheckEditorArtifactContextSize(c); err != nil || size != limit {
		t.Fatal("count differs from encoded bytes", size, err)
	}
	// Locate the final partially-filled label so adding one byte stays <=4096.
	for i := range c.EditorBindings {
		for j := range c.EditorBindings[i].SourceLabels {
			if len(c.EditorBindings[i].SourceLabels[j]) < 4096 {
				c.EditorBindings[i].SourceLabels[j] += "x"
				goto overflow
			}
		}
	}
overflow:
	raw, err = EncodeArtifactContext(c, pins)
	var fault *FaultError
	if !errors.As(err, &fault) || fault.Status != 413 || len(raw) != 0 {
		t.Fatalf("bound+1 must reject without output: bytes=%d err=%v", len(raw), err)
	}
	if fault.Code != "backend_artifact_context_limit" || fault.Details["allowedBytes"] != MaxEditorArtifactContextBytes || fault.Details["actualLowerBoundBytes"].(int64) < limit+1 {
		t.Fatal("missing resource metadata", fault)
	}
}

func TestEditorArtifactContextEscapedSourceLabel(t *testing.T) {
	c, pins := editorLimitFixture(t, 4<<20)
	label := c.EditorBindings[0].SourceLabels[0]
	// Quotation marks are valid in existing nonblank source node names. Same
	// UTF-8 input bytes, one additional actual JSON escape byte at the boundary.
	c.EditorBindings[0].SourceLabels[0] = "\"" + label[1:]
	if len(c.EditorBindings[0].SourceLabels[0]) != len(label) {
		t.Fatal("fixture changed UTF-8 byte count")
	}
	raw, err := EncodeArtifactContext(c, pins)
	var fault *FaultError
	if !errors.As(err, &fault) || fault.Status != 413 || len(raw) != 0 {
		t.Fatal("escaped label counted as unescaped bytes", err)
	}
}

func TestEditorArtifactContextEscapeAndOldV1Compatibility(t *testing.T) {
	pins, api := apiTestVector()
	all := make([]APIArtifactBinding, 200)
	for i := range all {
		all[i] = api[0]
		all[i].SourceNodeID = fmt.Sprintf("018f2396-cf02-7000-8000-%012d", i+1)
		all[i].Reason = strings.Repeat("\x01", 4096)
	}
	c := ArtifactContext{DocumentVersion: EditorArtifactDocumentVersion, SourceContentHash: strings.Repeat("a", 64), SourceSemanticHash: strings.Repeat("b", 64), APIBindings: all, EditorBindings: []EditorBinding{}}
	raw, err := EncodeArtifactContext(c, pins)
	if err != nil || len(raw) <= 4<<20 || strings.Contains(string(raw), "documentVersion") {
		t.Fatal("new cap altered unchanged v1 branch", len(raw), err)
	}
	// One scenario pin triggers v2 while retaining every legacy API binding.
	pins = append(pins, ArtifactPin{Kind: "design_scenario", ID: "9", RevisionID: "8", ContentHash: strings.Repeat("c", 64)})
	raw, err = EncodeArtifactContext(c, pins)
	var fault *FaultError
	if !errors.As(err, &fault) || fault.Status != 413 || len(raw) != 0 {
		t.Fatal("escaped v1->v2 transition admitted", len(raw), err)
	}
}

func TestEditorArtifactContextStopsBeforeHugeSerialization(t *testing.T) {
	c, pins := editorLimitFixture(t, 4<<20)
	c.EditorBindings = make([]EditorBinding, 200)
	label := strings.Repeat("x", 4096)
	for i := range c.EditorBindings {
		b := editorTestBinding("design_scenario", "1", EditorSelector{Kind: "participant", ParticipantID: fmt.Sprint(i)})
		b.SourceNodeIDs = make([]string, 100)
		b.SourceLabels = make([]string, 100)
		for j := range b.SourceNodeIDs {
			b.SourceNodeIDs[j] = fmt.Sprintf("018f2396-cf02-7000-8000-%012d", j+1)
			b.SourceLabels[j] = label
		}
		c.EditorBindings[i] = b
	}
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	raw, err := EncodeArtifactContext(c, pins)
	runtime.ReadMemStats(&after)
	var fault *FaultError
	if !errors.As(err, &fault) || fault.Status != 413 || len(raw) != 0 {
		t.Fatal("huge context serialized", err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	if allocated > 16<<20 {
		t.Fatalf("encoder allocated %d bytes before rejecting 82MB context", allocated)
	}
	t.Logf("oversized context rejected without output; allocated=%d bytes", allocated)
}
