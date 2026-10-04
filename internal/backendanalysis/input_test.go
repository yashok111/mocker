package backendanalysis

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

const validStart = `{"kind":"impact","fromRevisionId":"11111111-1111-4111-8111-111111111111","target":{"revisionId":"22222222-2222-4222-8222-222222222222"},"scope":{},"limits":{},"observationMode":"none","idempotencyKey":"key1"}`

func TestAnalysisInputClosed(t *testing.T) {
	cases := []struct {
		name, raw string
		status    int
	}{
		{"valid", validStart, 0},
		{"missing_scope", strings.Replace(validStart, `"scope":{},`, "", 1), 400},
		{"null_scope", strings.Replace(validStart, `"scope":{}`, `"scope":null`, 1), 400},
		{"mixed_target", strings.Replace(validStart, `"revisionId":"22222222-2222-4222-8222-222222222222"`, `"revisionId":"22222222-2222-4222-8222-222222222222","proposal":{"proposalId":"p1","proposalRevisionId":"p11111111-1111-4111-8111-111111111111"}`, 1), 400},
		{"import", strings.Replace(validStart, `{"revisionId":"22222222-2222-4222-8222-222222222222"}`, `{"importCandidate":{"id":"x"}}`, 1), 400},
		{"future", strings.Replace(validStart, `"impact"`, `"future"`, 1), 422},
		{"pinned", strings.Replace(validStart, `"none"`, `"pinned"`, 1), 422},
		{"scalar_pin", strings.Replace(validStart, `"scope":{}`, `"observationPins":[7],"scope":{}`, 1), 422},
		{"object_pin", strings.Replace(validStart, `"scope":{}`, `"observationPins":[{"future":true}],"scope":{}`, 1), 422},
		{"null_pins", strings.Replace(validStart, `"scope":{}`, `"observationPins":null,"scope":{}`, 1), 400},
		{"object_pins", strings.Replace(validStart, `"scope":{}`, `"observationPins":{},"scope":{}`, 1), 400},
		{"fraction", strings.Replace(validStart, `"limits":{}`, `"limits":{"states":1.5}`, 1), 400},
		{"overflow", strings.Replace(validStart, `"limits":{}`, `"limits":{"states":9223372036854775808}`, 1), 400},
		{"null_limit", strings.Replace(validStart, `"limits":{}`, `"limits":{"states":null}`, 1), 400},
		{"unknown", strings.Replace(validStart, `"scope":{}`, `"scope":{"surprise":1}`, 1), 400},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var in StartInput
			err := json.Unmarshal([]byte(tc.raw), &in)
			if tc.status == 0 {
				if err != nil {
					t.Fatal(err)
				}
				if in.Limits.States != 10000 {
					t.Fatalf("defaults: %+v", in.Limits)
				}
				return
			}
			var f *backendmodel.FaultError
			if !errors.As(err, &f) || f.Status != tc.status {
				t.Fatalf("want %d, got %v", tc.status, err)
			}
		})
	}
}

func TestAnalysisInputCanonicalEmptyPins(t *testing.T) {
	var a, b StartInput
	if err := json.Unmarshal([]byte(validStart), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(strings.Replace(validStart, `"scope":{}`, `"observationPins":[],"scope":{}`, 1)), &b); err != nil {
		t.Fatal(err)
	}
	x, _ := requestHash(a)
	y, _ := requestHash(b)
	if x != y {
		t.Fatal("empty pins changed request fingerprint")
	}
}

func TestAnalysisInputLegacyContext(t *testing.T) {
	for _, version := range []string{"", backendmodel.EditorArtifactDocumentVersion} {
		t.Run(version, func(t *testing.T) {
			c := backendmodel.ArtifactContext{DocumentVersion: version, SourceContentHash: strings.Repeat("a", 64), SourceSemanticHash: strings.Repeat("b", 64)}
			in := ImmutableInput{DocumentVersion: "backend-analysis-input/v1", From: backendmodel.BackendReadTarget{RevisionID: "11111111-1111-4111-8111-111111111111"}, Limits: defaultLimits(), BeforePins: backendmodel.EffectiveGraphPins{ArtifactContext: &c}, AfterPins: backendmodel.EffectiveGraphPins{ArtifactContext: &c}}
			raw, err := json.Marshal(in)
			if err != nil {
				t.Fatal(err)
			}
			var out ImmutableInput
			if err = json.Unmarshal(raw, &out); err != nil {
				t.Fatal(err)
			}
			again, err := json.Marshal(out)
			if err != nil {
				t.Fatal(err)
			}
			if string(raw) != string(again) {
				t.Fatalf("codec changed bytes: %s -> %s", raw, again)
			}
		})
	}
}

func TestAnalysisResultItemsArray(t *testing.T) {
	raw, err := json.Marshal(ResultPage{ItemsJSON: []byte(`[{"id":"c1"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"items":[{"id":"c1"}]`) {
		t.Fatal(string(raw))
	}
}

func TestAnalysisInputExactNumbersAndSemanticHash(t *testing.T) {
	a := map[string]jsontext.Value{"z": []byte(`9007199254740993`), "a": []byte(`{"z":1,"a":2}`)}
	b := map[string]jsontext.Value{"a": []byte(`{"a":2,"z":1}`), "z": []byte(`9007199254740993`)}
	x, err := requestHash(a)
	if err != nil {
		t.Fatal(err)
	}
	y, err := requestHash(b)
	if err != nil {
		t.Fatal(err)
	}
	if x != y {
		t.Fatal("raw object insertion order changed hash")
	}
	b["z"] = []byte(`9007199254740992`)
	y, _ = requestHash(b)
	if x == y {
		t.Fatal("integer precision lost")
	}
	b["z"] = []byte(`9007199254740993.0`)
	y, _ = requestHash(b)
	if x == y {
		t.Fatal("number lexeme lost")
	}
}

func TestAnalysisInputPinnedMatrix(t *testing.T) {
	uuid := "22222222-2222-4222-8222-222222222222"
	for _, schema := range []string{"1", "2", "3", "4", "5", "6"} {
		for _, target := range []string{"source", "legacy", "full", "preview"} {
			for _, tagged := range []bool{false, true} {
				t.Run(schema+"/"+target+fmt.Sprint(tagged), func(t *testing.T) {
					c := backendmodel.ArtifactContext{SourceContentHash: strings.Repeat("a", 64), SourceSemanticHash: strings.Repeat("b", 64)}
					pins := []backendmodel.ArtifactPin{}
					if tagged {
						c.DocumentVersion = backendmodel.EditorArtifactDocumentVersion
						pins = append(pins, backendmodel.ArtifactPin{Kind: "design_scenario", ID: "9007199254740993", RevisionID: "9007199254740995", ContentHash: strings.Repeat("c", 64)})
					}
					in := ImmutableInput{DocumentVersion: "backend-analysis-input/v1", Kind: "impact", ProjectID: projectID, From: backendmodel.BackendReadTarget{RevisionID: revisionID}, Limits: defaultLimits()}
					pin := backendmodel.EffectiveGraphPins{StructuralSchemaVersion: schema, ArtifactContext: &c, ArtifactPins: pins, EffectiveSemanticHash: strings.Repeat("d", 64), BaseSemanticHash: strings.Repeat("e", 64)}
					in.BeforePins = pin
					in.AfterPins = pin
					exact := backendmodel.ProposalReadTarget{ProposalID: uuid, ProposalRevisionID: revisionID}
					switch target {
					case "source":
						in.To = &backendmodel.BackendReadTarget{RevisionID: revisionID}
					case "legacy":
						in.To = &backendmodel.BackendReadTarget{Proposal: &exact}
					case "full":
						in.To = &backendmodel.BackendReadTarget{ChangeProposal: &exact}
					case "preview":
						in.CommandPreview = &backendmodel.FrozenChangePreview{ChangeProposal: exact, ArtifactPins: pins, ArtifactContext: c, Admission: backendmodel.PreviewAdmission{CommandIDs: []string{uuid}}}
					}
					raw, err := canonical(in)
					if err != nil {
						t.Fatal(err)
					}
					var out ImmutableInput
					if err = json.Unmarshal(raw, &out); err != nil {
						t.Fatal(err)
					}
					again, err := canonical(out)
					if err != nil {
						t.Fatal(err)
					}
					if string(raw) != string(again) {
						t.Fatal("immutable roundtrip changed pins")
					}
					out.AfterSource.SourceVectorHash = "changed"
					changed, _ := canonical(out)
					if digest(raw) == digest(changed) {
						t.Fatal("source vector omitted from input hash")
					}
					if out.CommandPreview != nil {
						out.AfterSource = in.AfterSource
						out.CommandPreview.Admission.CommandIDs = append(out.CommandPreview.Admission.CommandIDs, revisionID)
						changed, _ = canonical(out)
						if digest(raw) == digest(changed) {
							t.Fatal("admission omitted from hash")
						}
					}
				})
			}
		}
	}
}

func TestAnalysisInputScopeDefaults(t *testing.T) {
	var implicit, explicit StartInput
	if err := json.Unmarshal([]byte(validStart), &implicit); err != nil {
		t.Fatal(err)
	}
	raw := strings.Replace(validStart, `"scope":{}`, `"scope":{"direction":"downstream","depth":32}`, 1)
	if err := json.Unmarshal([]byte(raw), &explicit); err != nil {
		t.Fatal(err)
	}
	a, _ := requestHash(implicit)
	b, _ := requestHash(explicit)
	if a != b {
		t.Fatal("implicit scope defaults changed fingerprint")
	}
}
