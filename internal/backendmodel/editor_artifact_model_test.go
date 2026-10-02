package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
)

func TestEditorArtifactStoredNullLabelsAndProjectionUnion(t *testing.T) {
	b := editorTestBinding("design_scenario", "1", EditorSelector{Kind: "participant", ParticipantID: "p"})
	raw, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	var out EditorBinding
	raw = []byte(strings.Replace(string(raw), `"sourceLabels":["source"]`, `"sourceLabels":[null]`, 1))
	if json.Unmarshal(raw, &out) == nil {
		t.Error("null nested frozen source label accepted")
	}
	var data ArtifactProjectionData
	for _, raw := range []string{`{"kind":"participant","participant":{"id":"p","name":"P","kind":"service","description":""}}`, `{"kind":"event_edge","eventEdge":{"id":"e","kind":"uses","source":"a","target":"b","label":"","locator":{"pointer":"/eventModel"}}}`} {
		if err = json.Unmarshal([]byte(raw), &data); err != nil {
			t.Fatal(err)
		}
	}
	for _, raw := range []string{`{"kind":"participant"}`, `{"kind":"future"}`, `{"kind":"participant","participant":null}`, `{"kind":"participant","eventEdge":{"id":"e","kind":"uses","source":"a","target":"b","label":"","locator":{"pointer":"/eventModel"}}}`} {
		if json.Unmarshal([]byte(raw), &data) == nil {
			t.Errorf("invalid data union accepted: %s", raw)
		}
	}
}

func TestEditorArtifactWireOwnerIdentityStringsAndRawReceipt(t *testing.T) {
	exact := "9007199254740993"
	page := ArtifactProjectionPage{Items: []ArtifactProjectionItem{{ID: "aux", Locator: ArtifactProjectionLocator{Pin: ArtifactPin{Kind: "design_scenario", ID: exact, RevisionID: exact}, View: "event_model", Owner: ArtifactOwnerAddress{Pointer: "/eventModel/servers/0", ServerID: "server", EventMap: &ArtifactEventMapLocator{PinnedRevisionID: exact}}, Embedded: &ArtifactEmbeddedContract{ContractID: "copy", Mode: "copy", Origin: &ArtifactContractSource{DesignID: exact, RevisionID: exact, Version: exact}}}, Data: ArtifactProjectionData{Kind: "event_server", EventServer: &designscenario.EventServer{ID: "server"}}}}}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"designId", "revisionId", "version", "pinnedRevisionId"} {
		if !strings.Contains(string(raw), `"`+field+`":"`+exact+`"`) {
			t.Fatal("numeric identity not string", string(raw))
		}
	}
	if strings.Contains(string(raw), "bindingSelector") {
		t.Fatal("auxiliary server became eligible")
	}
	receipt := `{"project":{"version":1},"revision":{"semanticHash":"frozen"}}`
	result := ArtifactPinsResult{receiptJSON: receipt}
	raw, err = result.MarshalJSON()
	if err != nil || string(raw) != receipt {
		t.Fatal("receipt rewritten", err)
	}
}

func TestEditorArtifactFrozenRosterByteAmplification(t *testing.T) {
	// All strings are synthetic; 11×100 UUIDs fit a full-set request below
	// 128 KiB while bounded source labels alone exceed the event-map ceiling.
	bindings := make([]EditorBinding, 11)
	inputs := make([]EditorBindingInput, 11)
	// Existing source admission requires a nonblank node name and does not cap
	// it at 200 characters (that separate bound is a project name rule).
	sourceName := strings.Repeat("x", 4096)
	command := ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "source", Kind: "handler", Name: sourceName, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof"}}}
	if err := validateCommand(command, &ImportSession{Profile: "foundation-graph-v1"}); err != nil {
		t.Fatal("existing source name admission", err)
	}
	if boundedArtifactLabel(sourceName) != sourceName {
		t.Fatal("existing label derivation shortened valid 4096-byte name")
	}
	for i := range bindings {
		bindings[i] = editorTestBinding("design_scenario", "1", EditorSelector{Kind: "participant", ParticipantID: fmt.Sprint(i)})
		bindings[i].SourceNodeIDs = make([]string, 100)
		bindings[i].SourceLabels = make([]string, 100)
		for j := range bindings[i].SourceNodeIDs {
			bindings[i].SourceNodeIDs[j] = fmt.Sprintf("018f2396-cf02-7000-8000-%012d", j+1)
			bindings[i].SourceLabels[j] = boundedArtifactLabel(sourceName)
		}
		inputs[i] = EditorBindingInput{Selector: bindings[i].Selector, SourceNodeIDs: bindings[i].SourceNodeIDs}
	}
	in := PreviewArtifactPinsInput{BaseRevisionID: apiTestID, ExpectedVersion: 1, Commands: []ArtifactPinCommand{{Type: "set_artifact_pin", Artifact: ArtifactKey{"design_scenario", "1"}, RevisionID: "2", EditorBindings: inputs, Reason: "bind"}}}
	raw, err := json.Marshal(in)
	if err != nil || len(raw) > MaxAPIPinBodyBytes {
		t.Fatal("reduced input exceeds body", err, len(raw))
	}
	sourceBytes := len(raw)
	context := ArtifactContext{EditorBindings: bindings}
	_, roster, err := SelectedArtifactBindings(context, ArtifactKey{"design_scenario", "1"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(roster)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= designscenario.MaxEventMapBytes {
		t.Fatal("fixture failed to demonstrate roster amplification")
	}
	t.Logf("synthetic input=%d bytes; complete frozen roster=%d bytes; labels alone=%d bytes", sourceBytes, len(raw), 11*100*4096)
}

func TestEditorArtifactExactLimits(t *testing.T) {
	for _, id := range []string{"1", "9007199254740993", "9223372036854775807"} {
		if (ArtifactKey{"api_design", id}).Validate() != nil {
			t.Fatal(id)
		}
	}
	for _, id := range []string{"0", "01", "+1", "-1", "1.0", "9223372036854775808", " 1"} {
		if (ArtifactKey{"api_design", id}).Validate() == nil {
			t.Fatal(id)
		}
	}
	command := ArtifactPinCommand{Type: "set_artifact_pin", Artifact: ArtifactKey{"design_scenario", "1"}, RevisionID: "2", EditorBindings: []EditorBindingInput{}, Reason: strings.Repeat("я", 2048)}
	if err := command.Validate(); err != nil {
		t.Fatal(err)
	}
	command.Reason += "я"
	if command.Validate() == nil {
		t.Fatal("reason overflow accepted")
	}
	command.Reason = "r"
	commands := make([]ArtifactPinCommand, 0, 21)
	for i := range 20 {
		entry := command
		entry.Artifact.ID = fmt.Sprint(i + 1)
		commands = append(commands, entry)
	}
	if err := ValidateArtifactPinCommands(commands); err != nil {
		t.Fatal(err)
	}
	if ValidateArtifactPinCommands(append(commands, command)) == nil {
		t.Fatal("21 commands accepted")
	}
	pins := make([]ArtifactPin, 0, 21)
	for i := range 20 {
		pins = append(pins, ArtifactPin{Kind: "legacy", ID: fmt.Sprint(i), RevisionID: "opaque"})
	}
	if err := ValidateArtifactVector(pins, nil, nil); err != nil {
		t.Fatal("opaque legacy groups rejected", err)
	}
	if ValidateArtifactVector(append(pins, ArtifactPin{Kind: "legacy", ID: "21"}), nil, nil) == nil {
		t.Fatal("21 full pins accepted")
	}
	binding := editorTestBinding("design_scenario", "1", EditorSelector{Kind: "participant", ParticipantID: "p"})
	binding.SourceNodeIDs = []string{}
	binding.SourceLabels = []string{}
	if binding.Validate() == nil {
		t.Fatal("empty source set accepted")
	}
	for i := 0; i < 100; i++ {
		binding.SourceNodeIDs = append(binding.SourceNodeIDs, fmt.Sprintf("018f2396-cf02-7000-8000-%012d", i+1))
		binding.SourceLabels = append(binding.SourceLabels, strings.Repeat("x", 4096))
	}
	if err := binding.Validate(); err != nil {
		t.Fatal(err)
	}
	binding.SourceNodeIDs = append(binding.SourceNodeIDs, "018f2396-cf02-7000-8000-000000000101")
	binding.SourceLabels = append(binding.SourceLabels, "")
	if binding.Validate() == nil {
		t.Fatal("101 sources accepted")
	}
	apiPins, api := apiTestVector()
	pins = make([]ArtifactPin, 0, 1+len(apiPins))
	pins = append(pins, ArtifactPin{Kind: "design_scenario", ID: "1", RevisionID: "2", ContentHash: strings.Repeat("a", 64)})
	bindings := make([]EditorBinding, 200)
	for i := range bindings {
		bindings[i] = editorTestBinding("design_scenario", "1", EditorSelector{Kind: "participant", ParticipantID: fmt.Sprint(i)})
	}
	if err := ValidateArtifactVector(pins, nil, bindings); err != nil {
		t.Fatal(err)
	}
	pins = append(pins, apiPins...)
	if ValidateArtifactVector(pins, api, bindings) == nil {
		t.Fatal("combined 201 bindings accepted")
	}
	in := PreviewArtifactPinsInput{BaseRevisionID: apiTestID, ExpectedVersion: 1, Commands: []ArtifactPinCommand{command}}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var decoded PreviewArtifactPinsInput
	atLimit := []byte("{" + strings.Repeat(" ", MaxAPIPinBodyBytes-len(raw)) + string(raw[1:]))
	if err = json.Unmarshal(atLimit, &decoded); err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal([]byte("{ "+string(atLimit[1:])), &decoded) == nil {
		t.Fatal("body above inclusive limit accepted")
	}
}

func TestEditorArtifactStrictCommands(t *testing.T) {
	good := `{"type":"set_artifact_pin","artifact":{"kind":"api_design","id":"1"},"revisionId":"2","apiBindings":[],"editorBindings":[{"selector":{"kind":"state_diagram","diagramId":"order-life"},"sourceNodeIds":["00000000-0000-7000-8000-000000000001"]}],"reason":"Explicit manual association"}`
	var command ArtifactPinCommand
	if err := json.Unmarshal([]byte(good), &command); err != nil {
		t.Fatal(err)
	}
	bad := []string{
		strings.Replace(good, `"id":"1"`, `"id":"01"`, 1),
		strings.Replace(good, `"apiBindings":[],`, ``, 1),
		strings.Replace(good, `"reason":"Explicit manual association"`, `"reason":null`, 1),
		strings.Replace(good, `"diagramId":"order-life"`, `"diagramId":"order-life","messageId":"m"`, 1),
		strings.Replace(good, `"diagramId":"order-life"`, `"diagramId":"order-life","diagramId":"other"`, 1),
		strings.Replace(good, `"sourceNodeIds":`, `"objectHash":"`+strings.Repeat("a", 64)+`","sourceNodeIds":`, 1),
		strings.Replace(good, `"kind":"api_design"`, `"kind":"design_scenario"`, 1),
	}
	for _, raw := range bad {
		if err := json.Unmarshal([]byte(raw), &command); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	scenario := `{"type":"set_artifact_pin","artifact":{"kind":"design_scenario","id":"9223372036854775807"},"revisionId":"9007199254740993","editorBindings":[],"reason":"projection"}`
	if err := json.Unmarshal([]byte(scenario), &command); err != nil {
		t.Fatal(err)
	}
}

func TestEditorArtifactSelectors(t *testing.T) {
	selectors := []EditorSelector{
		{Kind: "sequence_message", MessageID: "opaque / message"}, {Kind: "participant", ParticipantID: "participant"},
		{Kind: "event_operation", ContractID: "contract", OperationID: "operation"}, {Kind: "event_channel", ChannelID: "channel"}, {Kind: "event_message", MessageID: "message"},
		{Kind: "state_diagram", DiagramID: "diagram"}, {Kind: "state_transition", DiagramID: "diagram", TransitionID: "transition"},
		{Kind: "response_rule", RuleID: "rule"}, {Kind: "response_node", RuleID: "rule", NodeID: "node"},
	}
	for i, s := range selectors {
		owner := "design_scenario"
		if i >= 5 {
			owner = "api_design"
		}
		if err := s.ValidateForArtifact(owner); err != nil {
			t.Fatalf("%s: %v", s.Kind, err)
		}
		raw, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		var out EditorSelector
		if err = json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		if i < 5 {
			if s.ValidateForArtifact("api_design") == nil {
				t.Fatal("scenario selector admitted for API")
			}
		} else {
			if s.ValidateForArtifact("design_scenario") == nil {
				t.Fatal("scenario missing embedded contract")
			}
			s.EmbeddedContractID = "copy-a"
			if err = s.ValidateForArtifact("design_scenario"); err != nil {
				t.Fatal(err)
			}
			if s.ValidateForArtifact("api_design") == nil {
				t.Fatal("API embedded contract accepted")
			}
		}
	}
	for _, raw := range []string{`{"kind":"future"}`, `{"kind":"state_diagram","diagramId":null}`, `{"kind":"participant","participantId":""}`, `{"kind":"state_diagram","diagramId":"d","embeddedContractId":""}`} {
		var s EditorSelector
		if json.Unmarshal([]byte(raw), &s) == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestEditorArtifactSourceSetsAndStrictQueries(t *testing.T) {
	ids := []string{"00000000-0000-7000-8000-000000000002", "00000000-0000-7000-8000-000000000001"}
	b := EditorBinding{ArtifactKind: "design_scenario", ArtifactID: "1", Selector: EditorSelector{Kind: "participant", ParticipantID: "p"}, SourceNodeIDs: ids, SourceLabels: []string{"two", "one"}, ObjectHash: strings.Repeat("a", 64), LastKnownLabel: "P", Origin: "manual", Reason: "link"}
	normalized, err := CanonicalEditorBinding(b)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.SourceLabels[0] != "one" || b.SourceLabels[0] != "two" {
		t.Fatal("labels lost alignment or input mutated")
	}
	b.SourceNodeIDs = []string{ids[0], ids[0]}
	if _, err = CanonicalEditorBinding(b); err == nil {
		t.Fatal("duplicate source normalized away")
	}
	for _, raw := range []string{`{"revisionId":"00000000-0000-7000-8000-000000000001","artifact":{"kind":"design_scenario","id":"1"},"view":"sequence"}`, `{"revisionId":"00000000-0000-7000-8000-000000000001","artifact":{"kind":"api_design","id":"1"},"view":"states"}`} {
		var in ArtifactQueryInput
		if err = json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatal(err)
		}
		for _, extra := range []string{`,"limit":0`, `,"limit":101`, `,"sourceNodeId":"x"`, `,"cursor":null`, `,"embeddedContractId":""`} {
			if json.Unmarshal([]byte(strings.TrimSuffix(raw, "}")+extra+"}"), &in) == nil {
				t.Errorf("accepted extra %s", extra)
			}
		}
	}
}
