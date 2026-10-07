package statediagram

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

const configuredDiagramJSON = `{"id":"order","name":"Order","initialStateId":"start","entity":{"family":"/orders","keyParam":"orderId","stateField":"status"},"states":[{"id":"start","name":"Created","x":0,"y":0,"terminal":false,"value":"created"},{"id":"end","name":"Paid","x":0,"y":0,"terminal":true,"value":"paid"}],"transitions":[{"id":"pay","name":"Pay","from":"start","to":"end","patchJSON":"{\"receipt\":9007199254740993}","responseStatus":200}]}`

func decodeConfigured(t *testing.T) Diagram {
	t.Helper()
	var raw any
	if err := jsonx.Unmarshal([]byte(configuredDiagramJSON), &raw); err != nil {
		t.Fatal(err)
	}
	env, err := Decode(map[string]any{Extension: map[string]any{"formatVersion": 1, "diagrams": []any{raw}}})
	if err != nil {
		t.Fatal(err)
	}
	return env.Diagrams[0]
}

func TestEntityCodecRoundtripAndStrictFields(t *testing.T) {
	t.Parallel()
	d := decodeConfigured(t)
	b, err := jsonx.Marshal(d)
	if err != nil || !strings.Contains(string(b), `"stateField":"status"`) || !strings.Contains(string(b), `"value":"created"`) {
		t.Fatalf("roundtrip: %s %v", b, err)
	}
	for _, edit := range []string{
		strings.Replace(configuredDiagramJSON, `"stateField":"status"`, `"stateField":"status","unknown":true`, 1),
		strings.Replace(configuredDiagramJSON, `"value":"created"`, `"value":null`, 1),
		strings.Replace(configuredDiagramJSON, `"value":"created"`, `"value":""`, 1),
	} {
		var raw any
		_ = jsonx.Unmarshal([]byte(edit), &raw)
		if _, err := Decode(map[string]any{Extension: map[string]any{"formatVersion": 1, "diagrams": []any{raw}}}); err == nil {
			t.Fatalf("invalid config accepted: %s", edit)
		}
	}
}

func TestConfiguredSimulationReadsAndWritesEffectiveValue(t *testing.T) {
	t.Parallel()
	d := decodeConfigured(t)
	initial, err := Simulate(t.Context(), d, nil, `{}`, nil)
	if err != nil || initial.StateID != "start" || initial.DataJSON != "{}" {
		t.Fatalf("initial %+v %v", initial, err)
	}
	got, err := Simulate(t.Context(), d, nil, `{"status":"created","amount":9007199254740993}`, []string{"pay"})
	if err != nil || got.StateID != "end" || !strings.Contains(got.DataJSON, `"status":"paid"`) || !strings.Contains(got.DataJSON, `"amount":9007199254740993`) {
		t.Fatalf("transition %+v %v", got, err)
	}
	terminal, err := Simulate(t.Context(), d, nil, `{"status":"paid"}`, []string{"pay"})
	if err != nil || terminal.StateID != "end" || len(terminal.Steps) != 1 || terminal.Steps[0].Accepted {
		t.Fatalf("terminal %+v %v", terminal, err)
	}
	for _, raw := range []string{`{"status":null}`, `{"status":17}`, `{"status":"unknown"}`} {
		invalid, err := Simulate(t.Context(), d, nil, raw, []string{"pay"})
		if err != nil || !HasErrors(invalid.Diagnostics) || len(invalid.Steps) != 0 || invalid.DataJSON != raw {
			t.Fatalf("invalid %+v %v", invalid, err)
		}
	}
}

func TestEntitySettingsRejectUnrelatedAndNullFields(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`{"kind":"settings","clearEntity":null}`, `{"kind":"remove_state","id":"created","entity":{"family":"/orders","keyParam":"id","stateField":"status"}}`, `{"kind":"upsert_state","clearEntity":false}`} {
		var command Command
		if err := jsonx.Unmarshal([]byte(raw), &command); err == nil {
			t.Fatalf("invalid entity settings accepted: %s", raw)
		}
	}
}
