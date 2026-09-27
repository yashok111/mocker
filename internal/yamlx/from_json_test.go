package yamlx

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
)

func TestFromJSONPreservesScalarTypesAndPrecision(t *testing.T) {
	raw := []byte(`{"example":9007199254740993,"fraction":1.2300,"huge":1e300,"responses":{"200":{"description":"yes"}},"date":"2026-09-27","flag":"true","nil":"null","value":null,"list":[false,"",-0]}`)
	encoded, err := FromJSON(raw)
	if err != nil {
		t.Fatal(err)
	}
	back, err := ToJSON(encoded)
	if err != nil {
		t.Fatal(err)
	}
	decode := func(raw []byte) any {
		t.Helper()
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		var v any
		if err := dec.Decode(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	if !reflect.DeepEqual(decode(raw), decode(back)) {
		t.Fatalf("roundtrip changed JSON:\n%s\n%s", encoded, back)
	}
	other, err := FromJSON(raw)
	if err != nil || !bytes.Equal(encoded, other) {
		t.Fatal("non-deterministic YAML", err)
	}
}

func TestFromJSONRejectsTrailingInputAndBoundsOutput(t *testing.T) {
	for _, raw := range []string{`{} {}`, `{broken`, `{"x":NaN}`} {
		if _, err := FromJSON([]byte(raw)); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	if _, err := FromJSONLimit([]byte(`{"long":"abcdefghijklmnopqrstuvwxyz"}`), 12); err == nil {
		t.Fatal("unbounded output")
	}
}
