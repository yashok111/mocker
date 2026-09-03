package admin

import (
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
)

// This file is `package admin`, not `package admin_test`, for one reason:
// [pinObservedBody] is the whole of the traffic-to-override conversion's write
// semantics and it is unexported. Every other test of that conversion drives it
// through HTTP from admin_test, which is right for the handler's own behaviour
// — but the two properties below can no longer be reached that way at all, and
// a property that cannot be reached is a property nothing pins.
//
// main_test.go and openapi_contract_test.go already use this package clause, so
// this is the established shape here rather than a new one.

// TestPinObservedBody_changesOnlyTheObservedResponse is the unit form of the
// invariant scripts/smoke.sh checks end to end. The conversion exists to
// replace what was OBSERVED — mode, body, encoding — and the auth preset writes
// its JWT recipe into the very same map entry (preset_handlers.go's
// variant.Recipes[b.DataPath] = b.Recipe), so a version of this function built
// from a fresh overrides.Variant{} literal silently deletes the token the
// mocked login returns. Every field below is one the earlier implementation
// dropped.
func TestPinObservedBody_changesOnlyTheObservedResponse(t *testing.T) {
	t.Parallel()

	existing := overrides.Variant{
		Mode:         "generated",
		Body:         jsonx.RawMessage(`{"stale":true}`),
		BodyEncoding: "",
		MediaType:    "application/json",
		Headers:      map[string]string{"X-Seeded": "yes"},
		When:         []overrides.Condition{{In: "query", Name: "debug", Op: "exists"}},
		SchemaPatch:  jsonx.RawMessage(`{"type":"object"}`),
		Recipes:      map[string]recipes.Recipe{"token": {Kind: "jwt"}},
	}

	got := pinObservedBody(existing, jsonx.RawMessage(`{"id":"42"}`), "")

	if got.Mode != "pinned" {
		t.Errorf("Mode = %q, want pinned", got.Mode)
	}
	if string(got.Body) != `{"id":"42"}` {
		t.Errorf("Body = %s, want the observed body", got.Body)
	}
	if got.MediaType != "application/json" {
		t.Errorf("MediaType = %q, want the existing application/json preserved", got.MediaType)
	}
	if got.Headers["X-Seeded"] != "yes" {
		t.Errorf("Headers = %+v, want X-Seeded=yes preserved", got.Headers)
	}
	if len(got.When) != 1 || got.When[0].Name != "debug" {
		t.Errorf("When = %+v, want the existing condition preserved", got.When)
	}
	if string(got.SchemaPatch) != `{"type":"object"}` {
		t.Errorf("SchemaPatch = %s, want it preserved", got.SchemaPatch)
	}
	if len(got.Recipes) != 1 {
		t.Errorf("Recipes = %+v, want the seeded recipe preserved — this is the mocked login's token", got.Recipes)
	}
}

// TestPinObservedBody_dropsABrowserExecutableMediaType pins the depth half.
// handlePutOperation now refuses a browser-executable media type under ANY
// mode, so no writer can hand this function such a variant through the API any
// more — which is exactly why the check cannot be tested through HTTP, and
// exactly why it stays: overrides.Repo.Put has more than one caller,
// overrides.ValidateVariant never inspects MediaType, and a row written before
// that guard was widened is still readable here. Flipping Mode to "pinned"
// while keeping text/html would store the row the write boundary exists to
// refuse; mockplane's second gate would then decline to serve it and the
// operator would watch a freshly pinned body answer empty.
func TestPinObservedBody_dropsABrowserExecutableMediaType(t *testing.T) {
	t.Parallel()

	for _, mt := range []string{"text/html", "TEXT/HTML; charset=utf-8", "application/xhtml+xml", "image/svg+xml"} {
		existing := overrides.Variant{
			Mode:      "generated",
			MediaType: mt,
			Headers:   map[string]string{"X-Seeded": "yes"},
		}
		got := pinObservedBody(existing, jsonx.RawMessage(`{"id":"42"}`), "")

		if got.Mode != "pinned" {
			t.Errorf("mediaType %q: Mode = %q, want pinned", mt, got.Mode)
		}
		if got.MediaType != "" {
			t.Errorf("mediaType %q: MediaType = %q, want it dropped so the document's declared type is used instead", mt, got.MediaType)
		}
		// Dropping the dangerous type must not become dropping everything.
		if got.Headers["X-Seeded"] != "yes" {
			t.Errorf("mediaType %q: Headers = %+v, want X-Seeded=yes preserved alongside the dropped type", mt, got.Headers)
		}
	}
}

// TestPinObservedBody_keepsAnInnocuousMediaType is the other side of the check
// above: only the three browser-executable types go, and a variant that never
// had one is not given an empty string it did not ask for.
func TestPinObservedBody_keepsAnInnocuousMediaType(t *testing.T) {
	t.Parallel()

	for _, mt := range []string{"application/json", "text/plain", "application/xml", ""} {
		got := pinObservedBody(overrides.Variant{Mode: "generated", MediaType: mt}, jsonx.RawMessage(`{}`), "")
		if got.MediaType != mt {
			t.Errorf("MediaType %q was rewritten to %q; only browser-executable types may be dropped", mt, got.MediaType)
		}
	}
}
