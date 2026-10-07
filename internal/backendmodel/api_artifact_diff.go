package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

const maxAPIArtifactDiffPointerBytes = 2048

type apiArtifactDiffReader struct {
	ctx    context.Context
	reader *strings.Reader
}

func (r apiArtifactDiffReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

// Decode each document once. Numbers retain their authored tokens and parent
// maps/arrays reference children rather than owning copies of entire subtrees.
func decodeAPIArtifactDiffValue(ctx context.Context, raw string) (any, error) {
	if raw == "" {
		return nil, ctx.Err()
	}
	decoder := jsonx.NewDecoder(apiArtifactDiffReader{ctx, strings.NewReader(raw)})
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err != nil {
			return nil, err
		}
		return nil, invalid("document", "Expected one selected JSON value")
	}
	nodes := 0
	var guard func(any, int) error
	guard = func(v any, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		nodes++
		if depth > 128 || nodes > 100000 {
			return apiPinsLimit()
		}
		switch v := v.(type) {
		case map[string]any:
			for _, child := range v {
				if err := guard(child, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range v {
				if err := guard(child, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := guard(value, 0); err != nil {
		return nil, err
	}
	return value, nil
}

// Hash only subtrees emitted as changes, using the original canonical API JSON
// hash domain. The jsonx boundary encodes Number tokens as JSON numbers before
// conversion to jsontext.Value, avoiding json/v2's ordinary string treatment.
func hashAPIArtifactDiffValue(value any, present bool) (string, error) {
	if !present {
		return "", nil
	}
	raw, err := jsonx.Marshal(value)
	if err != nil {
		return "", err
	}
	return hashAPIJSON(jsontext.Value(raw))
}

// Equality is needed only when an unrepresentable child path must be skipped.
// Compare shared children directly and check cancellation during the traversal.
func equalAPIArtifactDiffValues(ctx context.Context, a, b any) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	switch a := a.(type) {
	case map[string]any:
		right, ok := b.(map[string]any)
		if !ok || len(a) != len(right) {
			return false, nil
		}
		for key, left := range a {
			right, exists := right[key]
			if !exists {
				return false, nil
			}
			equal, err := equalAPIArtifactDiffValues(ctx, left, right)
			if err != nil || !equal {
				return equal, err
			}
		}
		return true, nil
	case []any:
		right, ok := b.([]any)
		if !ok || len(a) != len(right) {
			return false, nil
		}
		for i, left := range a {
			equal, err := equalAPIArtifactDiffValues(ctx, left, right[i])
			if err != nil || !equal {
				return equal, err
			}
		}
		return true, nil
	default:
		return jsonx.EqualValue(a, b), nil
	}
}

// Check raw and escaped UTF-8 byte length before allocating an escaped segment
// or complete path. '/' and '~' each add one byte when escaped as RFC6901.
func appendAPIArtifactDiffPath(parent, segment string) (string, bool) {
	available := maxAPIArtifactDiffPointerBytes - len(parent) - 1
	escaped := len(segment)
	if escaped > available {
		return "", false
	}
	for i := range len(segment) {
		if segment[i] == '~' || segment[i] == '/' {
			escaped++
			if escaped > available {
				return "", false
			}
		}
	}
	var path strings.Builder
	path.Grow(len(parent) + 1 + escaped)
	path.WriteString(parent)
	path.WriteByte('/')
	for i := range len(segment) {
		switch segment[i] {
		case '~':
			path.WriteString("~0")
		case '/':
			path.WriteString("~1")
		default:
			path.WriteByte(segment[i])
		}
	}
	return path.String(), true
}

// Changes retain only bounded pointers and canonical subtree hashes. There is
// no per-ancestor decode/hash buffer: emitted subtrees are disjoint, so their
// total encoding work follows input size rather than depth multiplied by size.
func apiArtifactObjectDiff(ctx context.Context, before, after string, remaining int) ([]APIArtifactObjectChange, bool, error) {
	left, err := decodeAPIArtifactDiffValue(ctx, before)
	if err != nil {
		return nil, false, err
	}
	right, err := decodeAPIArtifactDiffValue(ctx, after)
	if err != nil {
		return nil, false, err
	}
	d := &apiArtifactDiffer{ctx: ctx, remaining: remaining, out: []APIArtifactObjectChange{}}
	err = d.walk(left, before != "", right, after != "", "")
	return d.out, d.truncated, err
}

// apiArtifactDiffer is one bounded structural diff: it stops descending once
// remaining changes are emitted or a pointer would exceed its bound, and
// reports that as truncated.
type apiArtifactDiffer struct {
	ctx       context.Context
	remaining int
	out       []APIArtifactObjectChange
	truncated bool
}

func (d *apiArtifactDiffer) child(a any, aPresent bool, b any, bPresent bool, p, segment string) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	path, ok := appendAPIArtifactDiffPath(p, segment)
	if !ok {
		equal := false
		if aPresent && bPresent {
			var err error
			equal, err = equalAPIArtifactDiffValues(d.ctx, a, b)
			if err != nil {
				return err
			}
		}
		if !equal {
			d.truncated = true
		}
		return nil
	}
	return d.walk(a, aPresent, b, bPresent, path)
}

func (d *apiArtifactDiffer) walk(a any, aPresent bool, b any, bPresent bool, p string) error {
	if err := d.ctx.Err(); err != nil {
		return err
	}
	if d.truncated {
		return nil
	}
	if aPresent && bPresent {
		handled, err := d.descend(a, b, p)
		if handled || err != nil {
			return err
		}
	}
	return d.emit(a, aPresent, b, bPresent, p)
}

// descend walks into two containers of the same kind, and reports two equal
// scalars as handled too; anything else is a change at p itself.
func (d *apiArtifactDiffer) descend(a, b any, p string) (bool, error) {
	switch a := a.(type) {
	case map[string]any:
		if right, ok := b.(map[string]any); ok {
			return true, d.walkObject(a, right, p)
		}
	case []any:
		if right, ok := b.([]any); ok {
			return true, d.walkArray(a, right, p)
		}
	default:
		return jsonx.EqualValue(a, b), nil
	}
	return false, nil
}

func (d *apiArtifactDiffer) walkObject(a, right map[string]any, p string) error {
	keys := make([]string, 0, len(a)+len(right))
	for k := range a {
		keys = append(keys, k)
	}
	for k := range right {
		if _, exists := a[k]; !exists {
			keys = append(keys, k)
		}
	}
	slices.Sort(keys)
	for _, k := range keys {
		av, aOK := a[k]
		bv, bOK := right[k]
		if err := d.child(av, aOK, bv, bOK, p, k); err != nil {
			return err
		}
		if d.truncated {
			return nil
		}
	}
	return nil
}

func (d *apiArtifactDiffer) walkArray(a, right []any, p string) error {
	for i := range max(len(a), len(right)) {
		var av, bv any
		aOK, bOK := i < len(a), i < len(right)
		if aOK {
			av = a[i]
		}
		if bOK {
			bv = right[i]
		}
		if err := d.child(av, aOK, bv, bOK, p, strconv.Itoa(i)); err != nil {
			return err
		}
		if d.truncated {
			return nil
		}
	}
	return nil
}

func (d *apiArtifactDiffer) emit(a any, aPresent bool, b any, bPresent bool, p string) error {
	if len(d.out) >= d.remaining {
		d.truncated = true
		return nil
	}
	ah, err := hashAPIArtifactDiffValue(a, aPresent)
	if err != nil {
		return err
	}
	bh, err := hashAPIArtifactDiffValue(b, bPresent)
	if err != nil {
		return err
	}
	kind := "changed"
	if !aPresent {
		kind = "added"
	} else if !bPresent {
		kind = "removed"
	}
	d.out = append(d.out, APIArtifactObjectChange{Pointer: p, Kind: kind, BeforeHash: ah, AfterHash: bh})
	return nil
}
