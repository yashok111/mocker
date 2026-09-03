package jsonpatch

import (
	"errors"
	"fmt"

	"github.com/yashok111/mocker/internal/jsonx"
)

// Apply patches doc and returns a NEW map — a fully patched deep copy — or
// an error. It never mutates doc, never returns a shallow copy, and never
// returns a partially patched result: the first operation that fails aborts
// the whole call, and the caller gets doc's zero value (nil) back, never a
// map with only some of the patch's operations applied.
//
// An empty Patch (p.Empty()) still deep-copies and returns doc unchanged;
// callers that want to skip the allocation entirely for a patch that does
// nothing check Empty() themselves before calling Apply at all — that is
// what the runtime build in internal/mockplane does, because for it "skip"
// also means "build no patched root, delete no examples key", not merely
// "save an allocation".
func Apply(p Patch, doc map[string]any) (map[string]any, error) {
	result, ok := deepCopy(doc).(map[string]any)
	if !ok {
		// Unreachable: deepCopy's map[string]any case always returns
		// map[string]any, and doc's static type guarantees the input case
		// taken. Kept as a defensive check rather than a panic because a
		// silently wrong type assertion here is exactly the kind of bug
		// this package exists to make impossible.
		return nil, fmt.Errorf("%w: input document copied to an unexpected type", errApply)
	}

	for i, o := range p.ops {
		if err := applyOne(result, o); err != nil {
			return nil, fmt.Errorf("%w: operation %d (%s /%s): %w",
				errApply, i, o.kind, joinPath(o.path), err)
		}
	}
	return result, nil
}

// deepCopy recursively copies v. doc, as decoded by internal/openapi and
// internal/jsonx from JSON, is built from exactly five shapes: map[string]any
// and []any as containers, and string/float64/bool/nil as leaves (jsonx.Number
// if a caller ever unmarshals with UseNumber). Every leaf shape is an
// immutable value in Go, so only the two container cases need to allocate; a
// value decoded fresh out of an operation's raw bytes (decodeValue) is
// already exclusive to this call and does not need a further copy.
func deepCopy(v any) any {
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, vv := range val {
			out[k] = deepCopy(vv)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, vv := range val {
			out[i] = deepCopy(vv)
		}
		return out
	default:
		return val
	}
}

// applyOne performs one operation against result in place, resolving the
// operation's path token by token. Every intermediate token (all but the
// last) MUST already resolve to an existing map key or a valid array index —
// an add whose parent is absent is an Apply error, not an implicit mkdir -p,
// because a patch that silently builds out a schema shape by omission is a
// different feature than "добавить поле" (DESIGN §9).
//
// The last token is handled separately from the walk because it is the only
// one whose container might need to be REPLACED rather than mutated in
// place: a map mutates through its existing reference (Go maps are
// reference types, so writing into one reached by a value copy still lands
// on the original), but growing or shrinking an array — add's insert,
// remove's delete — allocates a new slice, which has to be written back into
// whatever held the old one. setParent below is exactly that write-back,
// captured while walking so it exists for the array case and costs nothing
// for the map case.
func applyOne(result map[string]any, o operation) error {
	var current any = result
	setParent := func(any) {} // never called for a root-level target: current stays the map itself

	for _, tok := range o.path[:len(o.path)-1] {
		switch node := current.(type) {
		case map[string]any:
			next, ok := node[tok]
			if !ok {
				return fmt.Errorf("%q: no such property", tok)
			}
			current = next
			setParent = func(v any) { node[tok] = v }
		case []any:
			idx, err := arrayIndex(tok, len(node), false)
			if err != nil {
				return err
			}
			current = node[idx]
			setParent = func(v any) { node[idx] = v }
		default:
			return fmt.Errorf("%q: parent is not an object or array", tok)
		}
	}

	last := o.path[len(o.path)-1]
	switch node := current.(type) {
	case map[string]any:
		return applyToMap(node, last, o)
	case []any:
		newArr, err := applyToArray(node, last, o)
		if err != nil {
			return err
		}
		setParent(newArr)
		return nil
	default:
		return fmt.Errorf("%q: parent is not an object or array", last)
	}
}

// applyToMap performs one operation against m at key, mutating m directly —
// safe because m is (transitively) the deep copy Apply made, never doc
// itself.
func applyToMap(m map[string]any, key string, o operation) error {
	switch o.kind {
	case opAdd:
		// RFC 6902: add over an existing member REPLACES its value. A
		// "create only" reading would turn an ordinary overwrite into an
		// apply failure and drop the whole patch (Apply's atomicity), which
		// is a worse outcome than the overwrite it was trying to prevent.
		val, err := decodeValue(o.value)
		if err != nil {
			return err
		}
		m[key] = val
		return nil
	case opReplace:
		if _, ok := m[key]; !ok {
			return fmt.Errorf("%q: no such property to replace", key)
		}
		val, err := decodeValue(o.value)
		if err != nil {
			return err
		}
		m[key] = val
		return nil
	case opRemove:
		if _, ok := m[key]; !ok {
			return fmt.Errorf("%q: no such property to remove", key)
		}
		delete(m, key)
		return nil
	default:
		return fmt.Errorf("unreachable operation kind %q", o.kind)
	}
}

// applyToArray performs one operation against arr at token, returning a new
// slice — arr itself is never mutated in place for add or remove, because
// both change its length and Go slices do not grow or shrink the backing
// array their holder already has a reference to. replace does not change
// length, but still returns a fresh copy rather than index-assigning arr in
// place, so this function has one allocation discipline instead of two.
func applyToArray(arr []any, token string, o operation) ([]any, error) {
	switch o.kind {
	case opAdd:
		idx, err := arrayIndex(token, len(arr), true)
		if err != nil {
			return nil, err
		}
		val, err := decodeValue(o.value)
		if err != nil {
			return nil, err
		}
		out := make([]any, 0, len(arr)+1)
		out = append(out, arr[:idx]...)
		out = append(out, val)
		out = append(out, arr[idx:]...)
		return out, nil

	case opReplace:
		idx, err := arrayIndex(token, len(arr), false)
		if err != nil {
			return nil, err
		}
		val, err := decodeValue(o.value)
		if err != nil {
			return nil, err
		}
		out := make([]any, len(arr))
		copy(out, arr)
		out[idx] = val
		return out, nil

	case opRemove:
		idx, err := arrayIndex(token, len(arr), false)
		if err != nil {
			return nil, err
		}
		out := make([]any, 0, len(arr)-1)
		out = append(out, arr[:idx]...)
		out = append(out, arr[idx+1:]...)
		return out, nil

	default:
		return nil, fmt.Errorf("unreachable operation kind %q", o.kind)
	}
}

// decodeValue decodes one operation's raw value bytes fresh, on every call —
// see rawOperation's doc comment for why that is itself the copy B2 (the
// package doc's "Values inserted by the patch are copied too") requires: a
// jsonx.Unmarshal into `any` always allocates new maps and slices, so the
// result can never alias the compiled Patch's own bytes or a value produced
// by an earlier Apply call against the same Patch.
func decodeValue(raw jsonx.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, errors.New("operation has no value")
	}
	var v any
	if err := jsonx.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("decode value: %w", err)
	}
	return v, nil
}

// joinPath renders a token slice back into an RFC 6901-shaped string for an
// error message. It does not re-escape "~" or "/" inside a token — this
// package's own errors are the only reader, and an unescaped token is more
// legible in a log line than a technically-round-trippable one.
func joinPath(tokens []string) string {
	out := ""
	for i, t := range tokens {
		if i > 0 {
			out += "/"
		}
		out += t
	}
	return out
}
