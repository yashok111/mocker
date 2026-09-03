package overrides

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
)

// ErrInvalidOpKey is returned by ParseOpKey when key does not decode to a
// "METHOD /path" pair — a caller holding a hand-edited or truncated URL
// segment gets a clean error, not a silently wrong (method, path).
var ErrInvalidOpKey = errors.New("overrides: malformed operation key")

// OpKey builds the admin URL segment that identifies one operation: method
// plus the RELATIVE path (DESIGN §14: "метод + относительный путь в
// percent-encoding"), percent-encoded so it fits in ONE path segment. It is
// built from the relative path — never a row id — precisely so it survives
// a spec re-import or a basePath edit unchanged.
//
// url.PathEscape uses the path-SEGMENT escape table, which — unlike the
// path-as-a-whole table url.QueryEscape or a raw string would give you —
// escapes "/" to "%2F". Verified, not assumed: every path with more than
// one segment has a "/" in it, and if that landed in the URL unescaped it
// would split the admin route into extra segments instead of naming one
// operation.
func OpKey(method, path string) string {
	return url.PathEscape(upperASCII(method) + " " + path)
}

// ParseOpKey reverses OpKey. A key that fails to percent-decode, or that
// decodes to something other than "METHOD /path" — no space, or a path with
// no leading slash — is rejected outright: half-parsing it would hand the
// caller a lookup against the wrong operation instead of a clear error.
func ParseOpKey(key string) (method, path string, err error) {
	raw, uerr := url.PathUnescape(key)
	if uerr != nil {
		return "", "", fmt.Errorf("%w: %q: %w", ErrInvalidOpKey, key, uerr)
	}
	m, p, ok := strings.Cut(raw, " ")
	if !ok || m == "" || p == "" || p[0] != '/' {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidOpKey, key)
	}
	return upperASCII(m), p, nil
}
