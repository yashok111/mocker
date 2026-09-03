// Variant selection helpers: which of an operation's responses answers, and
// what a pinned one carries. Split out of respond.go 2026-09-03; the text is
// unchanged.
package mockplane

import (
	"encoding/base64"
	"fmt"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
)

// variantForStatus finds the declared response variant whose HTTPStatus is
// exactly status, for active_status's own override: the operator wants THIS
// status served, whichever declared row happens to carry it, regardless of
// what chooseVariant's own DESIGN §7 priority would otherwise have picked.
func variantForStatus(variants []gen.ResponseVariant, status int) (gen.ResponseVariant, bool) {
	for _, v := range variants {
		if v.HTTPStatus == status {
			return v, true
		}
	}
	return gen.ResponseVariant{}, false
}

// pinnedBody decodes ov's own literal body for mode "pinned": verbatim when
// BodyEncoding is "" (Body is already the intended wire bytes — whatever
// JSON literal the admin API wrote for the "body" field: an object, array,
// string, ...), base64-decoded when BodyEncoding says so (the one shape that
// can carry bytes that are not themselves valid JSON, e.g. a real CSV or
// binary placeholder under a non-JSON media type). Both shapes were already
// proved to decode once, at write time (overrides.ValidateVariant) — this
// is not re-validating, only replaying the same decode on an
// unauthenticated request path, so it must still fail cleanly rather than
// panic if a row somehow reached storage some other way (a hand-run UPDATE,
// a future schema version).
func pinnedBody(ov overrides.Variant) ([]byte, error) {
	if ov.BodyEncoding != "base64" {
		return []byte(ov.Body), nil
	}
	var encoded string
	if err := jsonx.Unmarshal(ov.Body, &encoded); err != nil {
		return nil, fmt.Errorf("mockplane: pinned body: base64 body is not a JSON string: %w", err)
	}
	decoded, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return nil, fmt.Errorf("mockplane: pinned body: base64 decode: %w", err)
	}
	return decoded, nil
}

// withListSizeRecipe layers the row's own list_size column on top of base
// (stage 1's compiled recipe set for this variant, or nil) as one more
// bound recipe: a synthetic "listSize" pinned to "" — the data path
// [gen]'s own walker asks about for a bare top-level array response, the
// shape DESIGN's list contract calls the common case. It cannot reach the
// OTHER list shape the contract also recognizes (an object with exactly one
// array-typed property, keyed by that property's own name): op_overrides'
// list_size column carries no path, only a size, so there is nothing here
// to derive that property name from without inspecting the resolved schema
// — a cost this function deliberately does not take on.
//
// recipes.Compile — normally kept off the request path entirely (see
// serveGenerated's own call to [runtime.lookupRecipes], never
// [recipes.Compile], for the row's stored per-path recipes) — runs here
// ONLY when row.ListSize is actually set, which is by construction never the
// "no override" path HARD RULE 6 protects: an operator who pinned a list
// size on this one operation has already opted this one operation out of
// the zero-work default, and list_size has no build-time counterpart in
// runtime.go/overrides.go to look up instead (it is not part of any
// variant's Recipes map, so buildRecipeSets never sees it).
func withListSizeRecipe(base *recipes.Set, ls *overrides.ListSize) *recipes.Set {
	bindings := base.Bindings()
	if bindings == nil {
		bindings = make(map[string]recipes.Recipe, 1)
	}
	var raw jsonx.RawMessage
	if ls.Min == ls.Max {
		raw, _ = jsonx.Marshal(ls.Min)
	} else {
		raw, _ = jsonx.Marshal([2]int{ls.Min, ls.Max})
	}
	bindings[""] = recipes.Recipe{Kind: recipes.KindListSize, Data: raw}
	merged, err := recipes.Compile(bindings)
	if err != nil {
		// Every input here is this function's own construction —
		// overrides.normalizeAndValidate already enforced Min<=Max at write
		// time — so reaching this branch would mean a bug in this glue, not
		// bad stored input. Decline back to base rather than let a
		// should-never-happen error take the whole response down.
		return base
	}
	return merged
}

// chooseVariant picks the one response variant a matched request answers
// with, per DESIGN §7 step 5: the lowest numeric 2xx wins; absent that, the
// "2XX" row; absent that, the "default" row; absent all three (a shape this
// phase's own indexer never actually produces, since it already always
// marks one row IsDefault — handled anyway rather than trusted blindly) the
// lowest HTTPStatus of any kind. variants may be nil or empty: ok reports
// false, telling the caller there is nothing to answer with at all.
//
// This never returns the literal selector string ("2XX"/"default") as a
// status, and never 0: the returned ResponseVariant's own HTTPStatus field
// is always what actually gets sent, exactly as [gen.ResponseVariant]'s own
// contract promises.
func chooseVariant(variants []gen.ResponseVariant) (gen.ResponseVariant, bool) {
	if len(variants) == 0 {
		return gen.ResponseVariant{}, false
	}

	best := -1
	for i, v := range variants {
		if v.Selector == "2XX" || v.Selector == "default" {
			continue
		}
		if v.HTTPStatus < 200 || v.HTTPStatus >= 300 {
			continue
		}
		if best == -1 || v.HTTPStatus < variants[best].HTTPStatus {
			best = i
		}
	}
	if best == -1 {
		for i, v := range variants {
			if v.Selector == "2XX" {
				best = i
				break
			}
		}
	}
	if best == -1 {
		for i, v := range variants {
			if v.Selector == "default" {
				best = i
				break
			}
		}
	}
	if best == -1 {
		for i, v := range variants {
			if best == -1 || v.HTTPStatus < variants[best].HTTPStatus {
				best = i
			}
		}
	}
	return variants[best], true
}
