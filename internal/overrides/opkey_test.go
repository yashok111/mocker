package overrides_test

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/overrides"
)

// TestOpKey_RoundTrip covers exactly the shapes the task calls out as
// dangerous: slashes (every real path has at least one), braces (path
// params), unicode, and a "?"-bearing suffix that would look like a query
// string if OpKey ever leaked one unescaped into an admin URL.
func TestOpKey_RoundTrip(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		wantMethod string
	}{
		{name: "simple", method: "GET", path: "/users"},
		{name: "multi-segment with param", method: "get", path: "/users/{id}/orders/{orderId}", wantMethod: "GET"},
		{name: "unicode segment", method: "POST", path: "/пользователи/{id}/日本語"},
		{name: "query-looking suffix", method: "GET", path: "/search?x=1&y=2"},
		{name: "space in path", method: "GET", path: "/foo bar/baz"},
		{name: "root path", method: "DELETE", path: "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := overrides.OpKey(tt.method, tt.path)

			// The key must not contain a literal "/" — that is the one
			// property the whole encoding exists to guarantee, since the
			// key rides in a single admin URL path segment.
			if strings.Contains(key, "/") {
				t.Errorf("OpKey(%q, %q) = %q, contains an unescaped \"/\"", tt.method, tt.path, key)
			}

			gotMethod, gotPath, err := overrides.ParseOpKey(key)
			if err != nil {
				t.Fatalf("ParseOpKey(%q): %v", key, err)
			}
			wantMethod := tt.wantMethod
			if wantMethod == "" {
				wantMethod = tt.method
			}
			if gotMethod != wantMethod {
				t.Errorf("ParseOpKey(%q) method = %q, want %q", key, gotMethod, wantMethod)
			}
			if gotPath != tt.path {
				t.Errorf("ParseOpKey(%q) path = %q, want %q", key, gotPath, tt.path)
			}
		})
	}
}

func TestParseOpKey_malformed(t *testing.T) {
	tests := []struct {
		name string
		key  string
	}{
		{name: "not percent-encoded garbage", key: "%zz"},
		{name: "no space separator", key: url.PathEscape("GET/users")},
		{name: "empty method", key: url.PathEscape(" /users")},
		{name: "path without leading slash", key: url.PathEscape("GET users")},
		{name: "empty string", key: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := overrides.ParseOpKey(tt.key)
			if !errors.Is(err, overrides.ErrInvalidOpKey) {
				t.Fatalf("ParseOpKey(%q) error = %v, want ErrInvalidOpKey", tt.key, err)
			}
		})
	}
}
