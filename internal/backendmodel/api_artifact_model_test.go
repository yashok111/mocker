package backendmodel

import (
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
)

func TestAPIArtifactStrictAdmission(t *testing.T) {
	for _, tc := range []struct{ name, raw string }{
		{"null reason", `{"type":"remove_api_pin","artifactId":"1","reason":null}`},
		{"leading zero", `{"type":"remove_api_pin","artifactId":"01","reason":"review"}`},
		{"mixed", `{"type":"remove_api_pin","artifactId":"1","revisionId":"2","reason":"review"}`},
		{"unknown", `{"type":"remove_api_pin","artifactId":"1","reason":"review","extra":true}`},
		{"unsafe number", `{"type":"remove_api_pin","artifactId":9007199254740993,"reason":"review"}`},
		{"duplicate", `{"type":"remove_api_pin","artifactId":"1","artifactId":"2","reason":"review"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out APIPinCommand
			if json.Unmarshal([]byte(tc.raw), &out) == nil {
				t.Fatal("invalid command accepted")
			}
		})
	}
}
func TestAPIArtifactIDBounds(t *testing.T) {
	for _, id := range []string{"0", "01", "+1", "-1", "1.0", "9223372036854775808", "９"} {
		if ValidAPIArtifactID(id) {
			t.Fatalf("accepted %q", id)
		}
	}
	for _, id := range []string{"1", "9007199254740993", "9223372036854775807"} {
		if !ValidAPIArtifactID(id) {
			t.Fatalf("rejected exact string %q", id)
		}
	}
}
func TestAPIArtifactInputBounds(t *testing.T) {
	valid := `{"baseRevisionId":"` + apiTestID + `","expectedVersion":1,"commands":[{"type":"remove_api_pin","artifactId":"1","reason":"review"}]}`
	var in PreviewAPIPinsInput
	if err := json.Unmarshal([]byte(valid), &in); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, raw string }{{"null commands", `{"baseRevisionId":"` + apiTestID + `","expectedVersion":1,"commands":null}`}, {"null binding", `{"type":"set_api_pin","artifactId":"1","revisionId":"2","reason":"review","bindings":[null]}`}, {"mixed selector", `{"objectKey":"op","jsonPointer":"/a"}`}} {
		t.Run(tc.name, func(t *testing.T) {
			var err error
			switch tc.name {
			case "null commands":
				err = json.Unmarshal([]byte(tc.raw), &in)
			case "null binding":
				var c APIPinCommand
				err = json.Unmarshal([]byte(tc.raw), &c)
			default:
				var s APIArtifactSelector
				err = json.Unmarshal([]byte(tc.raw), &s)
			}
			if err == nil {
				t.Fatal("invalid admission")
			}
		})
	}
	c := APIPinCommand{Type: "set_api_pin", ArtifactID: "1", RevisionID: "2", Reason: "review", Bindings: make([]APIPinBindingInput, 201)}
	if c.Validate() == nil {
		t.Fatal("201 bindings admitted")
	}
	r := APIPinCommand{Type: "remove_api_pin", ArtifactID: "1", Reason: "review"}
	if ValidateAPIPinCommands([]APIPinCommand{r, r}) == nil {
		t.Fatal("duplicate artifact")
	}
	b := APIPinBindingInput{SourceNodeID: apiTestID, Selector: APIArtifactSelector{ObjectKey: "op"}}
	c.Bindings = []APIPinBindingInput{b, b}
	if c.Validate() == nil {
		t.Fatal("duplicate source")
	}
}
func TestAPIArtifactLegacySerialization(t *testing.T) {
	raw, err := json.Marshal(ArtifactPin{Kind: "old", ID: "x", RevisionID: "r"})
	if err != nil || string(raw) != `{"kind":"old","id":"x","revisionId":"r"}` {
		t.Fatalf("legacy changed %s %v", raw, err)
	}
	out := APIPinsResult{receiptJSON: ` {"project":{},"revision":{}} `}
	raw, err = out.MarshalJSON()
	if err != nil || string(raw) != out.receiptJSON {
		t.Fatal("receipt carrier changed bytes")
	}
}
func TestAPIArtifactRefStrictAndPointerBounds(t *testing.T) {
	_, bindings := apiTestVector()
	raw, err := json.Marshal(bindings[0].Ref)
	if err != nil {
		t.Fatal(err)
	}
	var ref ArtifactRef
	if err = json.Unmarshal(raw, &ref); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ name, raw string }{{"null", strings.Replace(string(raw), `"lastKnownLabel":"API"`, `"lastKnownLabel":null`, 1)}, {"wrong scalar", strings.Replace(string(raw), `"artifactId":"1"`, `"artifactId":1`, 1)}, {"unknown", strings.TrimSuffix(string(raw), "}") + `,"document":{}}`}} {
		t.Run(tc.name, func(t *testing.T) {
			if json.Unmarshal([]byte(tc.raw), &ref) == nil {
				t.Fatal("invalid reference accepted")
			}
		})
	}
	for _, p := range []string{"#schema", "/bad~2", "/bad~", strings.Repeat("/x", 65), "/" + strings.Repeat("x", 2048)} {
		if ValidateAPIArtifactPointer(p) == nil {
			t.Fatalf("invalid pointer %q", p)
		}
	}
	if err := ValidateAPIArtifactPointer("/properties/%literal/~0/~1"); err != nil {
		t.Fatal(err)
	}
	p, b := apiTestVector()
	for len(b) < 201 {
		item := b[0]
		item.SourceNodeID = fmt.Sprintf("018f2396-cf02-7000-8000-%012d", len(b)+1)
		b = append(b, item)
	}
	if ValidateAPIArtifactVector(p, b) == nil {
		t.Fatal("201 final bindings admitted")
	}
}

func TestAPIArtifactFullVectorLimit(t *testing.T) {
	pins, bindings := apiTestVector()
	for i := 1; i < 19; i++ {
		p := pins[0]
		p.ID = fmt.Sprint(i + 1)
		b := bindings[0]
		b.SourceNodeID = fmt.Sprintf("018f2396-cf02-7000-8000-%012d", i+1)
		b.Ref.ArtifactID = p.ID
		pins = append(pins, p)
		bindings = append(bindings, b)
	}
	retained := ArtifactPin{Kind: "other_artifact", ID: "legacy", RevisionID: "r"}
	pins = append(pins, retained)
	if err := ValidateAPIArtifactVector(pins, bindings); err != nil {
		t.Fatal("mixed vector at20 rejected", err)
	}
	before, _ := json.Marshal(pins)
	extra := pins[0]
	extra.ID = "20"
	b := bindings[0]
	b.SourceNodeID = "018f2396-cf02-7000-8000-000000000020"
	b.Ref.ArtifactID = "20"
	oversized := append(append([]ArtifactPin(nil), pins...), extra)
	allBindings := append(append([]APIArtifactBinding(nil), bindings...), b)
	if ValidateAPIArtifactVector(oversized, allBindings) == nil {
		t.Error("20 API pins plus retained artifact accepted")
	}
	after, _ := json.Marshal(pins)
	if string(before) != string(after) {
		t.Error("retained vector mutated during admission")
	}
	onlyRetained := make([]ArtifactPin, 21)
	for i := range onlyRetained {
		onlyRetained[i] = ArtifactPin{Kind: "other_artifact", ID: fmt.Sprint(i), RevisionID: "r"}
	}
	if ValidateAPIArtifactVector(onlyRetained, nil) == nil {
		t.Error("21 retained artifacts accepted")
	}
}
