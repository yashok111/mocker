// Package jsonpatch implements the RFC 6902 subset DESIGN §9 calls "Патч
// схемы": "add", "remove" and "replace" over map[string]any documents, the
// shape internal/openapi resolves a schema into. It is a LEAF package — it
// imports internal/jsonx and the stdlib ONLY, never anything above it in the
// tree — so the ingress gate (internal/admin, validating a PUT) and the
// runtime build (internal/mockplane, applying at startup) share this exact
// code instead of two hand-written dialects that could disagree about which
// patches are legal.
//
// "move", "copy" and "test" are REJECTED BY NAME at Parse. A format that
// accepts an operation it does not implement is worse than one that refuses
// it outright: the caller would believe the patch did something it never did.
//
// The split between Parse and Apply is deliberate and mirrors what each can
// actually know. Parse sees only the bytes: it settles the operation is one
// of the three, the JSON Pointer is well-formed under a strict reading of
// RFC 6901, and the two ingress bounds hold. It cannot know whether a target
// key exists, because it is never handed a document. Apply sees the document
// and answers everything that requires one — an absent parent, an
// out-of-range index, a path that runs into a non-container node — and it is
// atomic: it returns a fully patched deep copy or an error, never a
// half-patched map and never a shallow one. A shallow top-level copy would
// satisfy the word "new" while still writing into a shared properties map
// that concurrent requests are reading, which is the single worst outcome
// available to this package.
package jsonpatch

import (
	"bytes"
	"errors"
	"fmt"

	"github.com/yashok111/mocker/internal/jsonx"
)

const (
	// MaxPatchBytes bounds the wire size of the patch itself — the array of
	// operations and the values it carries — never the deep copy Apply
	// produces, whose size is a property of the spec's own schema. A pinned
	// response body may run to 4 MiB because it IS the response; a patch is
	// a handful of schema edits, so an order of magnitude less is generous.
	MaxPatchBytes = 64 * 1024
	// maxOps bounds the operation count for the same reason: an order of
	// magnitude past what "добавить поле" (DESIGN §9) ever needs.
	maxOps = 64
)

// op names one of the three RFC 6902 operations this subset performs.
type op string

const (
	opAdd     op = "add"
	opRemove  op = "remove"
	opReplace op = "replace"
)

// errInvalidPatch is wrapped by every error [Parse] returns: an unsupported
// operation name, a malformed or strictly-invalid JSON Pointer, the document
// root as a target, or bytes/operation counts past the two ingress bounds.
// Unexported deliberately — the package's stated contract (context.md §B) is
// Parse, Apply, the compiled type and one emptiness observer, and nothing
// more; a caller outside this package (and its own test, which is `package
// jsonpatch` and needs no export) distinguishes a Parse failure from an
// Apply failure by WHICH FUNCTION returned it, not by a sentinel it matches.
var errInvalidPatch = errors.New("jsonpatch: invalid patch")

// errApply is wrapped by every error [Apply] returns. Every reason is
// schema-dependent and therefore only knowable once a document is in hand:
// an add whose parent does not exist, a remove or replace of an absent key,
// an out-of-range array index, or a pointer that runs into a node that is
// neither a map nor an array. Unexported for the same reason as
// errInvalidPatch above.
var errApply = errors.New("jsonpatch: apply failed")

// rawOperation is the wire shape of one entry in the patch array. Value stays
// raw ([]byte, via jsonx.RawMessage) through Parse and is decoded only inside
// Apply, for two reasons. First, a value decoded at Parse time would make
// Parse reject an operation for the shape of its PAYLOAD instead of for its
// own structure — an op carrying `"value": 1e400` would refuse on the
// number, not on the operation, which is the wrong failure to report at
// ingress. Second, Apply decoding fresh bytes on every call is itself a copy
// that cannot alias anything: two Apply calls sharing one compiled Patch (the
// same stored patch is applied once per matching selector — see the runtime
// build that calls this package) each get their own maps and slices, never a
// sub-map spliced out of the compiled patch and into a document another
// request is reading concurrently.
type rawOperation struct {
	Op    string           `json:"op"`
	Path  string           `json:"path"`
	Value jsonx.RawMessage `json:"value"`
}

// operation is one compiled, validated entry: the op kind, the pointer
// already split into unescaped tokens (never empty — Parse rejects the
// document root for every op), and the raw value bytes for add/replace.
type operation struct {
	kind  op
	path  []string
	value jsonx.RawMessage
}

// Patch is a compiled, structurally valid RFC 6902 subset patch, ready to
// [Apply]. Its zero value is the empty patch.
type Patch struct {
	ops []operation
}

// Empty reports whether p carries no operations. [Parse] of nil bytes, of a
// JSON `[]`, and of a JSON `null` all return a Patch for which Empty is true
// — the three ways a caller-supplied schemaPatch means "no patch" (DESIGN
// never picks one spelling and the wire has produced all three: nil is what
// a variant that has never carried a patch stores, `[]` is what an operator
// clearing one leaves behind, and `null` is what jsonx.RawMessage decodes a
// wire null to, which is NOT the empty byte slice a naive len(...) != 0 check
// would assume).
//
// A caller that skips work on an empty patch — the runtime build in
// internal/mockplane builds no patched root, deletes no `examples`, applies
// no deep copy — needs this to be able to see the distinction; that is the
// entire reason the exported surface carries one more symbol than Parse and
// Apply alone would need.
func (p Patch) Empty() bool { return len(p.ops) == 0 }

// Parse turns patch bytes into a compiled [Patch], deciding everything
// decidable from the bytes alone. It never sees the document the patch will
// later be applied to, so nothing here can know whether a target key exists
// — that is [Apply]'s question, not this one.
func Parse(raw []byte) (Patch, error) {
	if len(raw) > MaxPatchBytes {
		return Patch{}, fmt.Errorf("%w: patch is %d bytes, exceeds the %d byte limit",
			errInvalidPatch, len(raw), MaxPatchBytes)
	}

	// nil, "" and the four bytes "null" are all "no patch" (Empty's doc
	// comment). Trim first: a caller-supplied byte slice may carry
	// insignificant whitespace around either, and jsonx.Unmarshal would
	// otherwise be asked to decode "  null  " into a []rawOperation and fail
	// for the wrong reason.
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || string(trimmed) == "null" {
		return Patch{}, nil
	}

	var raws []rawOperation
	if err := jsonx.Unmarshal(raw, &raws); err != nil {
		return Patch{}, fmt.Errorf("%w: %w", errInvalidPatch, err)
	}
	if len(raws) == 0 {
		// A JSON `[]` decodes to a zero-length, non-nil slice — still "no
		// patch", the third spelling Empty's doc comment names.
		return Patch{}, nil
	}
	if len(raws) > maxOps {
		return Patch{}, fmt.Errorf("%w: patch has %d operations, exceeds the %d operation limit",
			errInvalidPatch, len(raws), maxOps)
	}

	ops := make([]operation, len(raws))
	for i, ro := range raws {
		compiled, err := compileOp(ro)
		if err != nil {
			return Patch{}, fmt.Errorf("%w: operation %d: %w", errInvalidPatch, i, err)
		}
		ops[i] = compiled
	}
	return Patch{ops: ops}, nil
}

// compileOp validates one operation against everything Parse can decide
// without a document: the op name, the pointer's own syntax, and — for add
// and replace — that a value member is present and is itself well-formed
// JSON (its shape against the target schema is Apply's problem, not this
// one).
func compileOp(ro rawOperation) (operation, error) {
	kind := op(ro.Op)
	switch kind {
	case opAdd, opRemove, opReplace:
		// supported
	default:
		// The message names the operation because the admin ingress gate
		// (internal/admin) renders it verbatim into the 400 body, and a
		// generic "invalid request body" is what the decoder already says
		// for an unrelated failure (an unknown JSON field) — the two must
		// stay distinguishable.
		return operation{}, fmt.Errorf("unsupported patch operation %q", ro.Op)
	}

	tokens, err := splitPointer(ro.Path)
	if err != nil {
		return operation{}, fmt.Errorf("path %q: %w", ro.Path, err)
	}

	// The "-" end-of-array token is how RFC 6902 spells "append", and it is
	// only meaningful for add — DESIGN §9's headline use case ("добавить
	// поле, которого в спеке нет, и потребовать его") is exactly `add
	// /properties/x` followed by `add /required/-`. For remove and replace
	// it names nothing: whether it would resolve to a map key literally
	// spelled "-" or to an out-of-range array index is Apply's question, but
	// that it is not the append marker for these two ops is decidable from
	// the op name alone, so it is refused here rather than left to behave
	// unpredictably once a document is in hand.
	if kind != opAdd && tokens[len(tokens)-1] == "-" {
		return operation{}, errors.New(`the "-" end-of-array token is valid only for "add"`)
	}

	if kind == opAdd || kind == opReplace {
		if len(ro.Value) == 0 {
			return operation{}, fmt.Errorf("%s operation requires a value", ro.Op)
		}
		if !jsonx.Valid(ro.Value) {
			return operation{}, fmt.Errorf("%s operation value is not well-formed JSON", ro.Op)
		}
	}

	return operation{kind: kind, path: tokens, value: ro.Value}, nil
}
