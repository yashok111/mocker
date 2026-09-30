package jsonx

import (
	"strings"
	"testing"
)

func TestEqualValueExactNumbersAndKinds(t *testing.T) {
	for _, tt := range []struct {
		name, a, b string
		want       bool
	}{
		{"integer spelling", "1", "1.0", true},
		{"exponent spelling", "1e0", "1.000", true},
		{"signed zero", "-0e999999999999999999999999", "0", true},
		{"large integers", "9007199254740993", "9007199254740992", false},
		{"close fractions", "0.1000000000000000001", "0.1000000000000000002", false},
		{"huge exponent", "1e999999999999999999999999", "10e999999999999999999999998", true},
		{"small exponent", "1e-1000000000000000000000000", "10e-1000000000000000000000001", true},
		{"exponent padding", "1E+000000000000000000000003", "1000", true},
		{"null", "null", "null", true},
		{"null versus false", "null", "false", false},
		{"no string coercion", `"1"`, "1", false},
		{"no boolean coercion", "true", "1", false},
		{"object numbers", `{"n":1.00,"a":[null,true]}`, `{"a":[null,true],"n":1e0}`, true},
		{"object absent differs from null", `{"n":null}`, `{}`, false},
		{"array order", `[1,2]`, `[2,1]`, false},
		{"nested precision", `[9007199254740993]`, `[9007199254740992]`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			decode := func(raw string) any {
				decoder := NewDecoder(strings.NewReader(raw))
				decoder.UseNumber()
				var value any
				if err := decoder.Decode(&value); err != nil {
					t.Fatal(err)
				}
				return value
			}
			if got := EqualValue(decode(tt.a), decode(tt.b)); got != tt.want {
				t.Fatalf("%s == %s: %v", tt.a, tt.b, got)
			}
		})
	}
}
