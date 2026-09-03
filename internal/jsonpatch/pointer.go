package jsonpatch

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// splitPointer turns a raw RFC 6901 JSON Pointer string into its unescaped
// tokens. The tree already has a pointer walker — unescapePointerToken in
// internal/openapi/resolver.go — but it is the wrong model here: it reads a
// document internal/openapi itself produced and passes an illegal escape
// like "~2" through as two literal characters, which RFC 6901 forbids. This
// one reads a pointer an operator TYPED, so it rejects what that one lets
// through.
func splitPointer(pointer string) ([]string, error) {
	if pointer == "" {
		// The empty pointer means the whole document (RFC 6901), and Apply
		// returns a map[string]any — it cannot express a root replaced by a
		// JSON Schema boolean subschema, which a resolved schema pointer is
		// otherwise allowed to be (internal/gen/gen.go's asSchemaObject
		// accepts one). Refusing the root at ingress beats a return type
		// that has no way to say so.
		return nil, errors.New("path must not be the document root")
	}
	if !strings.HasPrefix(pointer, "/") {
		return nil, errors.New(`path must start with "/"`)
	}

	raw := strings.Split(pointer[1:], "/")
	tokens := make([]string, len(raw))
	for i, tok := range raw {
		unescaped, err := unescapePointerToken(tok)
		if err != nil {
			return nil, err
		}
		tokens[i] = unescaped
	}
	return tokens, nil
}

// unescapePointerToken decodes exactly the two RFC 6901 escapes, "~0" (to
// "~") and "~1" (to "/"), and rejects any other character following a "~" —
// including a "~" with nothing after it. This is the strict reading
// unescapePointerToken in internal/openapi/resolver.go deliberately is not:
// that one passes an illegal "~2" through as two literal characters because
// it is unescaping a pointer this codebase already produced, never one an
// operator typed by hand.
func unescapePointerToken(tok string) (string, error) {
	if !strings.Contains(tok, "~") {
		return tok, nil
	}

	var b strings.Builder
	b.Grow(len(tok))
	for i := 0; i < len(tok); i++ {
		c := tok[i]
		if c != '~' {
			b.WriteByte(c)
			continue
		}
		if i+1 >= len(tok) {
			return "", errors.New(`invalid escape: "~" at end of token`)
		}
		switch tok[i+1] {
		case '0':
			b.WriteByte('~')
		case '1':
			b.WriteByte('/')
		default:
			return "", fmt.Errorf("invalid escape %q: only \"~0\" and \"~1\" are valid", tok[i:i+2])
		}
		i++
	}
	return b.String(), nil
}

// arrayIndex resolves one path token against an array of the given length.
// allowDash accepts the RFC 6902 "-" end-of-array token and resolves it to
// length (one past the last element) — valid only when Apply is about to add
// there. Every other caller passes allowDash=false, so "-" falls through to
// the ordinary integer parse and fails it; in practice this branch is never
// exercised for remove/replace anyway, because compileOp already refuses a
// trailing "-" for those two ops at Parse time regardless of what the target
// turns out to be (map or array) — see compileOp's comment. It stays here,
// rather than only in compileOp, so this function is correct on its own
// terms and does not rely on a caller having pre-filtered the token.
func arrayIndex(tok string, length int, allowDash bool) (int, error) {
	if tok == "-" {
		if allowDash {
			return length, nil
		}
		return 0, fmt.Errorf("array index %q is out of range", tok)
	}
	idx, err := strconv.Atoi(tok)
	if err != nil || idx < 0 {
		return 0, fmt.Errorf("%q is not a valid array index", tok)
	}
	if idx > length || (idx == length && !allowDash) {
		return 0, fmt.Errorf("array index %d is out of range (length %d)", idx, length)
	}
	return idx, nil
}
