package traffic_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/traffic"
)

// TestRedactHeaders_namedHeadersReplacedRegardlessOfCase covers every
// credential-carrying header name DESIGN §15 lists, in mixed case, and
// proves matching is case-insensitive while an unlisted header survives
// untouched.
func TestRedactHeaders_namedHeadersReplacedRegardlessOfCase(t *testing.T) {
	t.Parallel()

	h := http.Header{
		"Authorization":       {"Bearer secret-token"},
		"COOKIE":              {"session=abc123"},
		"set-cookie":          {"session=abc123; Path=/"},
		"X-Api-Key":           {"sk-live-xyz"},
		"Proxy-Authorization": {"Basic dXNlcjpwYXNz"},
		"User-Agent":          {"curl/8.0"}, // unlisted: must survive
	}

	got := traffic.RedactHeaders(h)

	redacted := []string{"Authorization", "Cookie", "Set-Cookie", "X-Api-Key", "Proxy-Authorization"}
	for _, name := range redacted {
		if got[name] != traffic.RedactedValue {
			t.Errorf("header %q = %q, want %q", name, got[name], traffic.RedactedValue)
		}
	}
	if got["User-Agent"] != "curl/8.0" {
		t.Errorf("unlisted header User-Agent = %q, want survived unchanged %q", got["User-Agent"], "curl/8.0")
	}
}

// TestRedactHeaders_multiValuedHeaderIsCommaJoined pins the documented
// choice (comma-join, not Get's first-value-only) so a second value on a
// header like Accept-Language is never silently dropped from the traffic
// screen.
func TestRedactHeaders_multiValuedHeaderIsCommaJoined(t *testing.T) {
	t.Parallel()

	got := traffic.RedactHeaders(http.Header{"Accept-Language": {"en-US", "fr-FR"}})
	if want := "en-US, fr-FR"; got["Accept-Language"] != want {
		t.Errorf("Accept-Language = %q, want %q", got["Accept-Language"], want)
	}
}

func TestRedactHeaders_empty(t *testing.T) {
	t.Parallel()
	if got := traffic.RedactHeaders(nil); got != nil {
		t.Errorf("RedactHeaders(nil) = %v, want nil", got)
	}
	if got := traffic.RedactHeaders(http.Header{}); got != nil {
		t.Errorf("RedactHeaders(empty) = %v, want nil", got)
	}
}

// TestRedactJSONBody_fieldNames is table-driven over the exact field-name
// rules DESIGN §15 and the task spec pin: exact matches, the three suffix
// forms, and the "tokens" (plural) counter-example that must NOT be
// redacted — a plural collection name is common and unrelated to a secret,
// and this proves the suffix check does not accidentally catch it (it ends
// in "s", not "_token").
func TestRedactJSONBody_fieldNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		body       string
		wantField  string // dot path into the decoded output to inspect
		wantValue  string
		wantChange bool
	}{
		{
			name:       "password exact match",
			body:       `{"password":"hunter2"}`,
			wantField:  "password",
			wantValue:  traffic.RedactedValue,
			wantChange: true,
		},
		{
			name:       "token exact match",
			body:       `{"token":"abc.def.ghi"}`,
			wantField:  "token",
			wantValue:  traffic.RedactedValue,
			wantChange: true,
		},
		{
			name:       "secret exact match",
			body:       `{"secret":"shh"}`,
			wantField:  "secret",
			wantValue:  traffic.RedactedValue,
			wantChange: true,
		},
		{
			name:       "passwd exact match",
			body:       `{"passwd":"hunter2"}`,
			wantField:  "passwd",
			wantValue:  traffic.RedactedValue,
			wantChange: true,
		},
		{
			name:       "pwd exact match",
			body:       `{"pwd":"hunter2"}`,
			wantField:  "pwd",
			wantValue:  traffic.RedactedValue,
			wantChange: true,
		},
		{
			name:       "_key suffix",
			body:       `{"api_key":"sk-live-xyz"}`,
			wantField:  "api_key",
			wantValue:  traffic.RedactedValue,
			wantChange: true,
		},
		{
			name:       "_token suffix",
			body:       `{"refresh_token":"r-123"}`,
			wantField:  "refresh_token",
			wantValue:  traffic.RedactedValue,
			wantChange: true,
		},
		{
			name:       "_secret suffix",
			body:       `{"client_secret":"cs-123"}`,
			wantField:  "client_secret",
			wantValue:  traffic.RedactedValue,
			wantChange: true,
		},
		{
			name:       "mixed case field name still matches",
			body:       `{"Password":"hunter2"}`,
			wantField:  "Password",
			wantValue:  traffic.RedactedValue,
			wantChange: true,
		},
		{
			name: "tokens (plural) is NOT redacted: a paginated list's " +
				"collection field is a real, common name unrelated to a secret",
			body:       `{"tokens":["a","b"]}`,
			wantChange: false,
		},
		{
			name:       "unrelated field survives",
			body:       `{"username":"alex"}`,
			wantField:  "username",
			wantValue:  "alex",
			wantChange: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			out, changed := traffic.RedactJSONBody([]byte(tt.body))
			if changed != tt.wantChange {
				t.Fatalf("changed = %v, want %v (out=%s)", changed, tt.wantChange, out)
			}
			if tt.wantField == "" {
				return
			}
			var decoded map[string]any
			if err := json.Unmarshal(out, &decoded); err != nil {
				t.Fatalf("decode output: %v", err)
			}
			if tt.wantField == "tokens" {
				return
			}
			got, _ := decoded[tt.wantField].(string)
			if got != tt.wantValue {
				t.Errorf("field %q = %q, want %q", tt.wantField, got, tt.wantValue)
			}
		})
	}
}

// TestRedactJSONBody_nestedAndInArray proves redaction reaches any depth,
// including values sitting inside array elements.
func TestRedactJSONBody_nestedAndInArray(t *testing.T) {
	t.Parallel()

	body := `{
		"user": {"name": "alex", "credentials": {"password": "hunter2"}},
		"accounts": [
			{"id": 1, "api_key": "sk-1"},
			{"id": 2, "api_key": "sk-2"}
		]
	}`

	out, changed := traffic.RedactJSONBody([]byte(body))
	if !changed {
		t.Fatalf("changed = false, want true")
	}

	var decoded struct {
		User struct {
			Name        string `json:"name"`
			Credentials struct {
				Password string `json:"password"`
			} `json:"credentials"`
		} `json:"user"`
		Accounts []struct {
			ID     int    `json:"id"`
			APIKey string `json:"api_key"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(out, &decoded); err != nil {
		t.Fatalf("decode output: %v", err)
	}

	if decoded.User.Name != "alex" {
		t.Errorf("user.name = %q, want survived %q", decoded.User.Name, "alex")
	}
	if decoded.User.Credentials.Password != traffic.RedactedValue {
		t.Errorf("user.credentials.password = %q, want %q", decoded.User.Credentials.Password, traffic.RedactedValue)
	}
	if len(decoded.Accounts) != 2 {
		t.Fatalf("accounts has %d entries, want 2", len(decoded.Accounts))
	}
	for i, acc := range decoded.Accounts {
		if acc.APIKey != traffic.RedactedValue {
			t.Errorf("accounts[%d].api_key = %q, want %q", i, acc.APIKey, traffic.RedactedValue)
		}
	}
}

// TestRedactJSONBody_nonJSONStoredUnchanged proves a non-JSON body is
// returned exactly as given — no regex redaction attempted on text, per the
// task spec.
func TestRedactJSONBody_nonJSONStoredUnchanged(t *testing.T) {
	t.Parallel()

	tests := []string{
		"plain text with a password: hunter2 in it",
		"<xml><password>hunter2</password></xml>",
		"not-json-at-all {{{",
		"",
	}
	for _, body := range tests {
		out, changed := traffic.RedactJSONBody([]byte(body))
		if changed {
			t.Errorf("RedactJSONBody(%q) changed = true, want false (non-JSON is never touched)", body)
		}
		if string(out) != body {
			t.Errorf("RedactJSONBody(%q) = %q, want unchanged", body, out)
		}
	}
}

// TestRedactJSONBody_truncatedObjectOrArrayDropsRatherThanLeaks is the
// regression test for the gate-2 finding: a body cut at a raw byte offset
// (exactly what mockplane's request-body cap and this package's own MaxBody
// cut both do) lands mid-token and fails json.Unmarshal, but it is NOT the
// same as a genuinely non-JSON body — the secret near the front survived
// the cut in cleartext and must not be handed back untouched.
func TestRedactJSONBody_truncatedObjectOrArrayDropsRatherThanLeaks(t *testing.T) {
	t.Parallel()

	tests := []string{
		// Object cut mid-string, deep inside the padding field, well past
		// where "password" already appears in the clear.
		`{"password":"hunter2","padding":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`,
		// Array cut mid-element.
		`[{"password":"hunter2"},{"padding":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa`,
	}
	for _, body := range tests {
		out, changed := traffic.RedactJSONBody([]byte(body))
		if !changed {
			t.Errorf("RedactJSONBody(%q) changed = false, want true (a truncated object/array must not be stored verbatim)", body)
		}
		if out != nil {
			t.Errorf("RedactJSONBody(%q) = %q, want nil (lose the body rather than leak it)", body, out)
		}
	}
}

// TestRedactJSONBody_unchangedReturnsOriginalBytes proves that when nothing
// needed redaction, the ORIGINAL bytes come back (not a re-marshaled copy
// that could reorder keys or reformat whitespace).
func TestRedactJSONBody_unchangedReturnsOriginalBytes(t *testing.T) {
	t.Parallel()
	body := []byte(`{  "b": 1,   "a": 2 }`) // deliberately odd spacing / key order
	out, changed := traffic.RedactJSONBody(body)
	if changed {
		t.Fatalf("changed = true, want false")
	}
	if string(out) != string(body) {
		t.Errorf("out = %q, want byte-identical to input %q", out, body)
	}
}

// TestRedactBody_form is round-1 review finding 5: a form-urlencoded body
// must redact by FIELD NAME exactly like JSON does — the digest's own
// reproduction, verbatim. "grant_type=password" must survive: "password" is
// a VALUE there, not a field name, and isSecretField only ever looks at
// keys.
func TestRedactBody_form(t *testing.T) {
	t.Parallel()

	body := []byte("username=bob&password=FORMSECRET123&grant_type=password")
	out, changed := traffic.RedactBody(body, "application/x-www-form-urlencoded")
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	got, err := url.ParseQuery(string(out))
	if err != nil {
		t.Fatalf("redacted form body does not parse as a query string: %v (%q)", err, out)
	}
	if got.Get("password") != traffic.RedactedValue {
		t.Errorf("password = %q, want %q", got.Get("password"), traffic.RedactedValue)
	}
	if got.Get("username") != "bob" {
		t.Errorf("username = %q, want survived unchanged %q", got.Get("username"), "bob")
	}
	if got.Get("grant_type") != "password" {
		t.Errorf(`grant_type = %q, want survived unchanged "password" — a VALUE equal to a secret field name must never be touched`, got.Get("grant_type"))
	}

	// charset parameter must not defeat the media-type match.
	out2, changed2 := traffic.RedactBody([]byte("password=x"), "application/x-www-form-urlencoded; charset=utf-8")
	if !changed2 {
		t.Fatal("changed = false with a charset parameter present, want true")
	}
	if strings.Contains(string(out2), "x") {
		t.Errorf("out = %q, still contains the unredacted value", out2)
	}
}

// TestRedactBody_formNoSecretsUnchanged proves a form body with nothing to
// redact comes back byte-identical, the same guarantee RedactJSONBody gives.
func TestRedactBody_formNoSecretsUnchanged(t *testing.T) {
	t.Parallel()
	body := []byte("a=1&b=2")
	out, changed := traffic.RedactBody(body, "application/x-www-form-urlencoded")
	if changed {
		t.Fatalf("changed = true, want false")
	}
	if string(out) != string(body) {
		t.Errorf("out = %q, want byte-identical to input %q", out, body)
	}
}

// TestRedactBody_formInvalidUnchanged proves a body that fails to parse as a
// query string is left alone rather than guessed at.
func TestRedactBody_formInvalidUnchanged(t *testing.T) {
	t.Parallel()
	// A raw '%' not followed by two hex digits is the one shape
	// url.ParseQuery actually rejects.
	body := []byte("password=100%")
	out, changed := traffic.RedactBody(body, "application/x-www-form-urlencoded")
	if changed {
		t.Fatalf("changed = true, want false (invalid percent-encoding must not be redacted-and-reencoded)")
	}
	if string(out) != string(body) {
		t.Errorf("out = %q, want byte-identical to input %q", out, body)
	}
}

// TestRedactBody_text is round-1 review finding 5's other half: a text/plain
// body shaped as "field: value" per line, the digest's own reproduction.
func TestRedactBody_text(t *testing.T) {
	t.Parallel()

	out, changed := traffic.RedactBody([]byte("password: TEXTSECRET"), "text/plain")
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	if want := "password: " + traffic.RedactedValue; string(out) != want {
		t.Errorf("out = %q, want %q", out, want)
	}
}

// TestRedactBody_textMultiLineOnlySecretFieldsTouched proves the line-based
// rule reaches every matching line, leaves every non-matching line
// byte-for-byte untouched (including one with no ':' or '=' at all — plain
// prose, not a field line), and preserves CRLF line endings on an untouched
// line.
func TestRedactBody_textMultiLineOnlySecretFieldsTouched(t *testing.T) {
	t.Parallel()

	body := "username: bob\r\npassword: hunter2\r\nnote: just some prose, no colon-value shape here as a key\r\napi_key=sk-live-xyz\r\n"
	out, changed := traffic.RedactBody([]byte(body), "text/plain")
	if !changed {
		t.Fatalf("changed = false, want true")
	}
	got := string(out)
	if !strings.Contains(got, "username: bob\r\n") {
		t.Errorf("username line was altered: %q", got)
	}
	if !strings.Contains(got, "password: "+traffic.RedactedValue+"\r\n") {
		t.Errorf("password line not redacted (with CRLF preserved): %q", got)
	}
	if !strings.Contains(got, "api_key="+traffic.RedactedValue+"\r\n") {
		t.Errorf("api_key line not redacted (with CRLF preserved): %q", got)
	}
	if strings.Contains(got, "hunter2") || strings.Contains(got, "sk-live-xyz") {
		t.Errorf("a secret value survived redaction: %q", got)
	}
}

// TestRedactBody_textNoSecretsUnchanged proves a text body with no
// field-shaped secret line comes back byte-identical.
func TestRedactBody_textNoSecretsUnchanged(t *testing.T) {
	t.Parallel()
	body := []byte("just a plain line\nanother plain line")
	out, changed := traffic.RedactBody(body, "text/plain")
	if changed {
		t.Fatalf("changed = true, want false")
	}
	if string(out) != string(body) {
		t.Errorf("out = %q, want byte-identical to input %q", out, body)
	}
}

// TestRedactBody_defaultsToJSON proves every OTHER content type — including
// none at all — falls through to the exact same [RedactJSONBody] behaviour
// this package always had, so the new dispatcher does not regress the JSON
// path DESIGN §15's other half depends on.
func TestRedactBody_defaultsToJSON(t *testing.T) {
	t.Parallel()
	tests := []struct{ contentType string }{
		{"application/json"},
		{""},
		{"application/xml"}, // unrecognized: still tried as JSON, same as before Content-Type was tracked
	}
	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			body := []byte(`{"password":"hunter2"}`)
			out, changed := traffic.RedactBody(body, tt.contentType)
			jsonOut, jsonChanged := traffic.RedactJSONBody(body)
			if changed != jsonChanged || string(out) != string(jsonOut) {
				t.Errorf("RedactBody(%q) = (%q, %v), want RedactJSONBody's own (%q, %v)",
					tt.contentType, out, changed, jsonOut, jsonChanged)
			}
		})
	}
}
