package bundle_test

import (
	"bytes"
	"testing"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
)

// C10: "A snapshot is spelling-independent for the new payloads"
// (gates/p2e/artifacts/context.md §J/C10; decisions.md D5, §5/C10). Two
// Bundle values differ ONLY in how a bound "sequence" recipe's
// {"start":…,"step":…} payload is SPELLED on the wire — key order and
// insignificant whitespace, never its meaning — Encode both and compare
// the resulting ENCODED BYTES directly.
//
// Deliberately NOT "save and re-encode a single document": a raw field
// canonicalizeVariant FORGOT to canonicalise still round-trips
// byte-identically against ITSELF (Encode -> Decode -> Encode agrees with
// its own prior output trivially, because nothing about a single value's
// own bytes ever changes across that loop), so that form would pass
// against exactly the implementation this check exists to fail. Comparing
// two INDEPENDENTLY spelled sources is the only form that actually
// observes canonicalisation running, rather than merely observing that
// Decode is the left inverse of Encode.
//
// Built as a bare Bundle value (bundle.New + one OverrideEntry), not a
// workspace-shaped fixture: this package's unit is Bundle, it opens no
// store at all, and standing up a database this package has never needed
// would test something this check is not about.

// sequenceOverride builds one OverrideEntry binding a "sequence" recipe
// (D5's ride-the-existing-field kind) to items[*].name, with its Data
// payload exactly as given — so the caller controls spelling without this
// helper normalising anything on its own.
func sequenceOverride(payload string) bundle.OverrideEntry {
	return bundle.OverrideEntry{
		Method: "GET",
		Path:   "/widgets",
		Responses: map[string]overrides.Variant{
			"200": {
				Recipes: map[string]recipes.Recipe{
					"items[*].name": {
						Kind: recipes.KindSequence,
						Data: jsonx.RawMessage(payload),
					},
				},
			},
		},
	}
}

// Fails against a payload that canonicalizeVariant does not reach — the
// production line this pins is internal/bundle/bundle.go's
// canonicalizeVariant, the `cr.Data, err = canonicalizeRaw(rec.Data)`
// statement: reverting that one line back to `cr.Data = rec.Data` (no
// canonicalisation) makes bundleA and bundleB encode to two different key
// orders and this test goes red, because nothing else in either Bundle
// differs.
// Does not fail correct code: Data is canonicalised by name (D5), so both
// spellings decode to the identical Go value, canonicalizeRaw re-encodes
// both through the same key-sorted, whitespace-free pass, and the rest of
// each Bundle is byte-for-byte identical by construction.
func TestEncode_canonicalizesSequenceRecipePayloadSpelling(t *testing.T) {
	settings := domain.Settings{Seed: 1, ListSize: 5}
	spec := bundle.SpecRef{Hash: "abc123", Name: "widgets-spec"}

	// Same sequence recipe, decoding to the identical {start:1000,step:1}
	// object, spelled two different ways on the wire — reordered keys and
	// inserted whitespace, the exact pair context.md §J/C10 names.
	bundleA := bundle.New("ws", settings, spec, []bundle.OverrideEntry{
		sequenceOverride(`{"start":1000,"step":1}`),
	})
	bundleB := bundle.New("ws", settings, spec, []bundle.OverrideEntry{
		sequenceOverride(`{ "step":1, "start":1000 }`),
	})

	bytesA, err := bundle.Encode(bundleA)
	if err != nil {
		t.Fatalf("Encode(bundleA): %v", err)
	}
	bytesB, err := bundle.Encode(bundleB)
	if err != nil {
		t.Fatalf("Encode(bundleB): %v", err)
	}

	if !bytes.Equal(bytesA, bytesB) {
		t.Fatalf("encoded bytes differ for two spellings of the same sequence payload (D5):\nA: %s\nB: %s", bytesA, bytesB)
	}
}
