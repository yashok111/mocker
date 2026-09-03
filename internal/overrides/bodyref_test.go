package overrides_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
)

// A6 (A8): bodyRef on a Variant — refused by name on every wrong shape,
// kept byte-for-byte through the JSON round trip every snapshot (scenario,
// checkpoint, bundle) is.
func TestValidateVariant_bodyRef(t *testing.T) {
	t.Parallel()
	ok := overrides.Variant{Mode: "pinned", BodyRef: "asset:avatar-1.jpg"}
	if err := overrides.ValidateVariant(ok); err != nil {
		t.Fatalf("a well-formed bodyRef refused: %v", err)
	}
	for _, tc := range []struct {
		name string
		v    overrides.Variant
		want string
	}{
		{"generated mode", overrides.Variant{Mode: "generated", BodyRef: "asset:a.png"}, "requires mode"},
		{"default mode", overrides.Variant{BodyRef: "asset:a.png"}, "requires mode"},
		{"with body", overrides.Variant{Mode: "pinned", BodyRef: "asset:a.png", Body: jsonx.RawMessage(`{}`)}, "exclusive"},
		{"with bodyEncoding", overrides.Variant{Mode: "pinned", BodyRef: "asset:a.png", BodyEncoding: "base64"}, "exclusive"},
		{"with mediaType", overrides.Variant{Mode: "pinned", BodyRef: "asset:a.png", MediaType: "image/png"}, "no mediaType"},
		{"no prefix", overrides.Variant{Mode: "pinned", BodyRef: "a.png"}, "must be"},
		{"bad name", overrides.Variant{Mode: "pinned", BodyRef: "asset:a/b.png"}, "must be"},
		{"empty name", overrides.Variant{Mode: "pinned", BodyRef: "asset:"}, "must be"},
	} {
		err := overrides.ValidateVariant(tc.v)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one containing %q", tc.name, err, tc.want)
		}
	}

	// The wire shape: omitted when empty, kept when set — a v4 document
	// without the field is byte-identical to before (D9).
	b, err := json.Marshal(overrides.Variant{Mode: "pinned", Body: jsonx.RawMessage(`"x"`)})
	if err != nil || strings.Contains(string(b), "bodyRef") {
		t.Fatalf("bodyRef leaked into a variant without one: %s %v", b, err)
	}
	b, _ = json.Marshal(ok)
	var back overrides.Variant
	if err := jsonx.Unmarshal(b, &back); err != nil || back.BodyRef != ok.BodyRef {
		t.Fatalf("round trip: %s → %+v (%v)", b, back, err)
	}
	if err := overrides.ValidateVariant(back); err != nil {
		t.Fatalf("the decoded variant fails the same gate a restore re-enters: %v", err)
	}
}
