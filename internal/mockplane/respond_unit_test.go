// respond_unit_test.go: the pure unit tests — chooseVariant, acceptable,
// wrapEnvelope, clampedDelay/awaitDelay, setSafeHeader, variantForStatus,
// pinnedBody, withListSizeRecipe, effectiveDelayMs — proving what respond.go's
// unexported pieces do in isolation, before any of them goes through HTTP.
// Split out of the former respond_test.go (package mockplane, not
// mockplane_test — see helpers_test.go's own comment for why).
package mockplane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
)

// TestChooseVariant is the direct table-driven proof of DESIGN §7 step 5's
// selection rule, independent of any HTTP plumbing: lowest numeric 2xx,
// then "2XX", then "default", then (a shape the real indexer never actually
// produces, since it always marks exactly one row IsDefault, but handled
// defensively anyway) the lowest status of any kind. Every case also checks
// that HTTPStatus — never the Selector string, never 0 — is what a caller
// would actually send: the blocker-grade defect the phase digest calls out
// by name.
func TestChooseVariant(t *testing.T) {
	v := func(sel string, status int) gen.ResponseVariant {
		return gen.ResponseVariant{Selector: sel, HTTPStatus: status}
	}

	tests := []struct {
		name       string
		variants   []gen.ResponseVariant
		wantOK     bool
		wantStatus int
		wantSel    string
	}{
		{"empty", nil, false, 0, ""},
		{
			"lowest numeric 2xx among several wins over a higher one and over 204",
			[]gen.ResponseVariant{v("204", 204), v("201", 201), v("default", 200)},
			true, 201, "201",
		},
		{
			"a bare 204 IS itself the lowest numeric 2xx when nothing lower exists",
			[]gen.ResponseVariant{v("204", 204), v("default", 200)},
			true, 204, "204",
		},
		{
			"no numeric 2xx: 2XX row wins over default",
			[]gen.ResponseVariant{v("2XX", 200), v("default", 200), v("404", 404)},
			true, 200, "2XX",
		},
		{
			"no numeric 2xx, no 2XX: default row wins",
			[]gen.ResponseVariant{v("default", 200), v("404", 404)},
			true, 200, "default",
		},
		{
			"nothing in 2xx/2XX/default shape at all: lowest status of any kind, defensively",
			[]gen.ResponseVariant{v("404", 404), v("500", 500)},
			true, 404, "404",
		},
		{
			"a single default-only operation",
			[]gen.ResponseVariant{v("default", 200)},
			true, 200, "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := chooseVariant(tt.variants)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if got.HTTPStatus != tt.wantStatus {
				t.Errorf("HTTPStatus = %d, want %d (never the literal selector string, never 0)", got.HTTPStatus, tt.wantStatus)
			}
			if got.Selector != tt.wantSel {
				t.Errorf("chose selector %q, want %q", got.Selector, tt.wantSel)
			}
		})
	}
}

// TestAcceptable covers exactly what acceptable's own doc comment claims to
// implement: split-on-comma media ranges, a ";q=" weight defaulting to 1,
// most-specific-match-wins among candidates, and q=0 as an explicit refusal
// even when a less specific range would otherwise accept.
func TestAcceptable(t *testing.T) {
	tests := []struct {
		name      string
		accept    string
		mediaType string
		want      bool
	}{
		{"missing Accept accepts the variant's own type", "", "application/json", true},
		{"blank Accept accepts", "   ", "application/json", true},
		{"*/* accepts anything", "*/*", "application/json", true},
		{"exact match", "application/json", "application/json", true},
		{"type wildcard matches", "application/*", "application/json", true},
		{"case-insensitive match", "Application/JSON", "application/json", true},
		{"mismatched type refuses", "text/plain", "application/json", false},
		{"mismatched subtype refuses", "application/xml", "application/json", false},
		{"charset parameter on the declared type is ignored when comparing", "application/json", "application/json; charset=utf-8", true},
		{"explicit q=0 refuses", "application/json;q=0", "application/json", false},
		{"q=0 with no decimal still refuses", "application/json ; q=0", "application/json", false},
		{"nonzero q accepts", "application/json;q=0.1", "application/json", true},
		{
			"most specific match's q wins over a more general match's q",
			"application/json;q=0, */*;q=0.9",
			"application/json",
			false,
		},
		{
			"a more general acceptance does not rescue a specific exclusion",
			"*/*;q=1, application/json;q=0",
			"application/json",
			false,
		},
		{
			"multiple ranges: the one that matches decides, others are irrelevant",
			"text/plain, application/json",
			"application/json",
			true,
		},
		{"malformed q value falls back to the default (accepts)", "application/json;q=banana", "application/json", true},
		{"empty declared media type always accepts (nothing to negotiate)", "text/plain", "", true},
		{"malformed declared media type (no slash) always accepts", "text/plain", "not-a-media-type", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := acceptable(tt.accept, tt.mediaType); got != tt.want {
				t.Errorf("acceptable(%q, %q) = %v, want %v", tt.accept, tt.mediaType, got, tt.want)
			}
		})
	}
}

// TestWrapEnvelope proves the envelope helper wraps valid JSON under key and
// refuses (rather than corrupts) anything that is not valid JSON — the
// mechanism serveGenerated relies on to skip enveloping a non-JSON body
// (e.g. this document's one text/csv response, or a binary placeholder)
// without needing to branch on media type itself.
func TestWrapEnvelope(t *testing.T) {
	t.Run("wraps a JSON object", func(t *testing.T) {
		got, err := wrapEnvelope("data", []byte(`{"id":1}`))
		if err != nil {
			t.Fatalf("wrapEnvelope: %v", err)
		}
		want := `{"data":{"id":1}}`
		if string(got) != want {
			t.Errorf("wrapEnvelope = %s, want %s", got, want)
		}
	})

	t.Run("wraps a JSON array", func(t *testing.T) {
		got, err := wrapEnvelope("items", []byte(`[1,2,3]`))
		if err != nil {
			t.Fatalf("wrapEnvelope: %v", err)
		}
		want := `{"items":[1,2,3]}`
		if string(got) != want {
			t.Errorf("wrapEnvelope = %s, want %s", got, want)
		}
	})

	t.Run("escapes a key that needs it", func(t *testing.T) {
		got, err := wrapEnvelope(`we"ird`, []byte(`1`))
		if err != nil {
			t.Fatalf("wrapEnvelope: %v", err)
		}
		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(got, &decoded); err != nil {
			t.Fatalf("wrapEnvelope produced invalid JSON: %v (%s)", err, got)
		}
		if string(decoded[`we"ird`]) != "1" {
			t.Errorf("wrapEnvelope = %s, want a key exactly %q", got, `we"ird`)
		}
	})

	t.Run("refuses non-JSON content instead of corrupting it", func(t *testing.T) {
		if _, err := wrapEnvelope("data", []byte("a,b,c\n1,2,3\n")); err == nil {
			t.Error("wrapEnvelope(csv-ish text) = nil error, want a refusal so the caller leaves it unwrapped")
		}
	})
}

// TestSetSafeHeader_DropsCRLF is the direct proof of the response-splitting
// guard: a header value containing a raw CR or LF — plausible from an
// attacker-controllable uploaded spec, DESIGN §15's threat model — is
// dropped entirely rather than set, mangled or otherwise reaching the wire.
func TestSetSafeHeader_DropsCRLF(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string // "" means: header must not be set at all
	}{
		{"clean value passes through", "abc123", "abc123"},
		{"embedded CRLF is dropped", "abc\r\nX-Injected: evil", ""},
		{"bare LF is dropped", "abc\ninjected", ""},
		{"bare CR is dropped", "abc\rinjected", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			setSafeHeader(rec, "X-Test", tt.value)
			if got := rec.Header().Get("X-Test"); got != tt.want {
				t.Errorf("header = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("empty name is a no-op", func(t *testing.T) {
		rec := httptest.NewRecorder()
		setSafeHeader(rec, "", "value")
		if len(rec.Header()) != 0 {
			t.Errorf("headers = %v, want none set for an empty name", rec.Header())
		}
	})
}

// TestClampedDelay proves the cap without ever having to sleep out
// maxSimulatedDelay itself.
func TestClampedDelay(t *testing.T) {
	tests := []struct {
		name    string
		delayMs int
		want    time.Duration
	}{
		{"zero", 0, 0},
		{"negative (Settings.Normalize should already prevent this, but defend anyway)", -50, 0},
		{"ordinary value passes through", 250, 250 * time.Millisecond},
		{"exactly the cap", int(maxSimulatedDelay / time.Millisecond), maxSimulatedDelay},
		{"far past the cap is clamped", 10_000_000, maxSimulatedDelay},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampedDelay(tt.delayMs); got != tt.want {
				t.Errorf("clampedDelay(%d) = %v, want %v", tt.delayMs, got, tt.want)
			}
		})
	}
}

// TestAwaitDelay covers the timer/select wrapper itself: it actually waits
// out a small delay, returns immediately for delayMs<=0, and bails out via
// ctx.Done() rather than the full delay when the caller's context is
// canceled first — DESIGN's "cancellable via the request context" (5a).
func TestAwaitDelay(t *testing.T) {
	t.Run("zero delay returns immediately", func(t *testing.T) {
		start := time.Now()
		if !awaitDelay(t.Context(), 0) {
			t.Fatal("awaitDelay(0) = false, want true")
		}
		if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
			t.Errorf("elapsed = %v, want effectively instant for delayMs<=0", elapsed)
		}
	})

	t.Run("positive delay actually waits", func(t *testing.T) {
		const delay = 30 * time.Millisecond
		start := time.Now()
		if !awaitDelay(t.Context(), int(delay/time.Millisecond)) {
			t.Fatal("awaitDelay = false, want true (context never canceled)")
		}
		if elapsed := time.Since(start); elapsed < delay {
			t.Errorf("elapsed = %v, want at least %v", elapsed, delay)
		}
	})

	t.Run("canceled context returns false well before a long delay elapses", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		start := time.Now()
		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()
		// A delay far longer than the cancellation: if awaitDelay ignored
		// ctx, this would block for the full 5s and the test would time out.
		if awaitDelay(ctx, 5000) {
			t.Fatal("awaitDelay = true, want false (context was canceled mid-sleep)")
		}
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Errorf("elapsed = %v, want well under the 5s delay (cancellation must cut it short)", elapsed)
		}
	})
}

func TestVariantForStatus(t *testing.T) {
	variants := []gen.ResponseVariant{
		{Selector: "200", HTTPStatus: 200},
		{Selector: "404", HTTPStatus: 404},
	}
	if v, ok := variantForStatus(variants, 404); !ok || v.Selector != "404" {
		t.Errorf("variantForStatus(404) = (%+v, %v), want the 404 variant", v, ok)
	}
	if _, ok := variantForStatus(variants, 500); ok {
		t.Error("variantForStatus(500) found something, want ok=false: no variant declares it")
	}
}

func TestPinnedBody(t *testing.T) {
	t.Run("verbatim JSON body", func(t *testing.T) {
		ov := overrides.Variant{Body: json.RawMessage(`{"a":1}`)}
		got, err := pinnedBody(ov)
		if err != nil {
			t.Fatalf("pinnedBody: %v", err)
		}
		if string(got) != `{"a":1}` {
			t.Errorf("pinnedBody = %s, want the stored bytes verbatim", got)
		}
	})

	t.Run("base64 decodes to the original bytes", func(t *testing.T) {
		encoded, _ := json.Marshal(base64.StdEncoding.EncodeToString([]byte("raw bytes, not JSON")))
		ov := overrides.Variant{Body: json.RawMessage(encoded), BodyEncoding: "base64"}
		got, err := pinnedBody(ov)
		if err != nil {
			t.Fatalf("pinnedBody: %v", err)
		}
		if string(got) != "raw bytes, not JSON" {
			t.Errorf("pinnedBody = %q, want the decoded original", got)
		}
	})

	t.Run("malformed base64 fails cleanly, never panics", func(t *testing.T) {
		ov := overrides.Variant{Body: json.RawMessage(`"not-valid-base64!!"`), BodyEncoding: "base64"}
		if _, err := pinnedBody(ov); err == nil {
			t.Error("pinnedBody = nil error, want a decode failure surfaced, not silently swallowed")
		}
	})
}

func TestWithListSizeRecipe(t *testing.T) {
	t.Run("fixed size", func(t *testing.T) {
		set := withListSizeRecipe(nil, &overrides.ListSize{Min: 3, Max: 3})
		lo, hi, ok := set.ListSizeAt("")
		if !ok || lo != 3 || hi != 3 {
			t.Errorf("ListSizeAt(\"\") = (%d, %d, %v), want (3, 3, true)", lo, hi, ok)
		}
	})

	t.Run("range", func(t *testing.T) {
		set := withListSizeRecipe(nil, &overrides.ListSize{Min: 2, Max: 5})
		lo, hi, ok := set.ListSizeAt("")
		if !ok || lo != 2 || hi != 5 {
			t.Errorf("ListSizeAt(\"\") = (%d, %d, %v), want (2, 5, true)", lo, hi, ok)
		}
	})

	t.Run("layers on top of an existing compiled set without losing its other recipes", func(t *testing.T) {
		base, err := recipes.Compile(map[string]recipes.Recipe{
			"name": {Kind: recipes.KindConst, Data: json.RawMessage(`"x"`)},
		})
		if err != nil {
			t.Fatalf("recipes.Compile: %v", err)
		}
		merged := withListSizeRecipe(base, &overrides.ListSize{Min: 4, Max: 4})
		if _, ok := merged.Lookup("name"); !ok {
			t.Error("merged set lost the base recipe at \"name\"")
		}
		if lo, hi, ok := merged.ListSizeAt(""); !ok || lo != 4 || hi != 4 {
			t.Errorf("merged.ListSizeAt(\"\") = (%d, %d, %v), want (4, 4, true)", lo, hi, ok)
		}
	})
}

// TestEffectiveDelayMs is DESIGN §4's delay precedence table, pure and
// direct: SESSION beats ROW beats WORKSPACE settings — Session being the
// outermost of the four layers, exactly like a live-state status force
// already beats an override, which itself already beats the document.
func TestEffectiveDelayMs(t *testing.T) {
	rowDelay := 42
	tests := []struct {
		name       string
		sessionMs  int
		rowDelayMs *int
		settingsMs int
		want       int
	}{
		{"nothing set at all: the workspace setting wins", 0, nil, 10, 10},
		{"a row delay beats the workspace setting", 0, &rowDelay, 10, 42},
		{"a session delay beats a row delay AND the workspace setting", 300, &rowDelay, 10, 300},
		{"a session delay beats the workspace setting when there is no row", 300, nil, 10, 300},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveDelayMs(tt.sessionMs, tt.rowDelayMs, tt.settingsMs); got != tt.want {
				t.Errorf("effectiveDelayMs(%d, %v, %d) = %d, want %d", tt.sessionMs, tt.rowDelayMs, tt.settingsMs, got, tt.want)
			}
		})
	}
}
