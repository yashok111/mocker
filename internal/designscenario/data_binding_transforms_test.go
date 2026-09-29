package designscenario

import (
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestBindingTransformValuesAndExactNumbers(t *testing.T) {
	for _, tc := range []struct {
		name               string
		value              any
		chain              []DataBindingTransform
		wantJSON, wantType string
	}{
		{"Unicode trim lower", " \u2003ÄBc\u2003 ", []DataBindingTransform{{Kind: "trim"}, {Kind: "lower"}}, `"äbc"`, "string"},
		{"upper after trim", " \u2003straße\u2003", []DataBindingTransform{{Kind: "trim"}, {Kind: "upper"}}, `"STRAßE"`, "string"},
		{"exact number", "1.2300e+4", []DataBindingTransform{{Kind: "to_number"}}, `1.2300e+4`, "number"},
		{"exact integer", "9007199254740993", []DataBindingTransform{{Kind: "to_integer"}}, `9007199254740993`, "integer"},
		{"number spelling to string", jsonx.Number("9007199254740993"), []DataBindingTransform{{Kind: "to_string"}}, `"9007199254740993"`, "string"},
		{"boolean to string", true, []DataBindingTransform{{Kind: "to_string"}}, `"true"`, "string"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			value, typ, err := applyBindingTransforms(tc.value, tc.chain)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := jsonx.Marshal(value)
			if err != nil || string(encoded) != tc.wantJSON || typ != tc.wantType {
				t.Fatalf("got %s (%s), %v; want %s (%s)", encoded, typ, err, tc.wantJSON, tc.wantType)
			}
		})
	}
}

func TestBindingTransformRejectsInvalidNumberTokensAndNonScalars(t *testing.T) {
	for _, text := range []string{" 42", "42 ", "+42", "042", "-01", "NaN", "Infinity", "1.", "1e", "true", "1 2"} {
		if _, _, err := applyBindingTransforms(text, []DataBindingTransform{{Kind: "to_number"}}); err == nil {
			t.Errorf("accepted number token %q", text)
		}
	}
	for _, text := range []string{"1.0", "1e2", " 1", "+1", "01"} {
		if _, _, err := applyBindingTransforms(text, []DataBindingTransform{{Kind: "to_integer"}}); err == nil {
			t.Errorf("accepted integer token %q", text)
		}
	}
	for _, value := range []any{nil, map[string]any{"x": 1}, []any{1}} {
		if _, _, err := applyBindingTransforms(value, []DataBindingTransform{{Kind: "to_string"}}); err == nil {
			t.Errorf("accepted %T", value)
		}
	}
}
