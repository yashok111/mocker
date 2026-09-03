// workspace_handlers_test.go is a WHITE-BOX test file (package admin, not
// admin_test): it reuses loopback_test.go's own loopbackTestServer/
// loopbackTestSrc and autocheckpoint_test.go's own
// newResourceCheckpointTestClient (the session-cookie client), and it reads
// the two unexported wire codes (codeInvalidBasePath/
// codeInvalidBasePathValues) the PATCH handler answers with — a black-box
// admin_test.go file cannot see either.
package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/yashok111/mocker/internal/testspec"
)

// TestPatchWorkspace_basePathValidation is P19 (mocker-p3h-basepath D4.3/
// D14): a settings write whose basePath/basePathValues violates D4.3's
// shape rules is refused with 400 and the code D4.3 names, through BOTH
// writers that reach [Server.handlePatchWorkspace] — the admin PATCH
// (session cookie, via [newResourceCheckpointTestClient]) and MCP's
// update_workspace_settings, exercised here the same way
// autocheckpoint_test.go already does for the identical reason: MCP
// dispatches through [Server.CallAsMCP], the SAME mux the session path
// hits (routeMux's own doc comment), so calling CallAsMCP directly
// exercises the tool's own dispatch path without pulling in internal/mcp,
// a package this file does not own. This property's own mutation is
// deleting the validator CALL from the PATCH handler while leaving the
// validator itself defined — before this test existed, every other
// property in this corpus stayed green under that mutation.
func TestPatchWorkspace_basePathValidation(t *testing.T) {
	t.Parallel()
	srv := loopbackTestServer(t, nil)
	client := newResourceCheckpointTestClient(t, srv)
	mcpSrc := loopbackTestSrc()

	// DerivationDoc declares "/widgets/{id}" — a route parameter named
	// "id", the one this test's collision case reuses as a base-path
	// parameter name to trigger D4.3's half two (the spec-collision
	// refusal, which needs the bound spec and is why this test lives here
	// rather than as a domain-package unit test of the two pure
	// validators alone).
	specRec := client.do(http.MethodPost, "http://mocker.local/api/specs",
		mustJSON(t, map[string]string{"name": "Derivation", "source": "upload", "document": string(testspec.DerivationDoc())}))
	if specRec.Code != http.StatusCreated {
		t.Fatalf("import spec: status = %d, want 201; body = %s", specRec.Code, specRec.Body.String())
	}
	var spec struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(specRec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("decode import spec response: %v", err)
	}

	wsRec := client.do(http.MethodPost, "http://mocker.local/api/workspaces",
		fmt.Sprintf(`{"name":"BasePath Demo","specId":%d}`, spec.ID))
	if wsRec.Code != http.StatusCreated {
		t.Fatalf("create workspace: status = %d, want 201; body = %s", wsRec.Code, wsRec.Body.String())
	}
	var ws struct {
		ID          int64 `json:"id"`
		EditVersion int64 `json:"editVersion"`
	}
	if err := json.Unmarshal(wsRec.Body.Bytes(), &ws); err != nil {
		t.Fatalf("decode create workspace response: %v", err)
	}
	target := fmt.Sprintf("http://mocker.local/api/workspaces/%d", ws.ID)

	// getSettings reads back the workspace's CURRENT settings, byte for
	// byte (via a re-marshal of the decoded value, since map key order
	// from encoding/json is not guaranteed stable across two decodes of
	// the same document, but IS stable for two decodes of the identical
	// bytes compared as Go values) — used below to assert a refused write
	// changed nothing.
	getSettings := func() string {
		t.Helper()
		rec := client.do(http.MethodGet, target, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("get workspace: status = %d; body = %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Settings json.RawMessage `json:"settings"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode get-workspace response: %v", err)
		}
		return string(canonicalizeJSON(t, body.Settings))
	}
	before := getSettings()

	cases := []struct {
		name       string
		settings   map[string]any
		wantCode   string
		wantReason string
	}{
		{
			name:       "unbalanced brace",
			settings:   map[string]any{"basePath": "/v{n"},
			wantCode:   codeInvalidBasePath,
			wantReason: "an unbalanced brace never spans a whole segment the router would compile as a parameter",
		},
		{
			name:       "parameter name collides with the bound spec's own route parameter",
			settings:   map[string]any{"basePath": "/{id}", "basePathValues": []string{"7"}},
			wantCode:   codeInvalidBasePath,
			wantReason: `"id" is the parameter name /widgets/{id} declares`,
		},
		{
			name:       "basePathValues element of the wrong arity",
			settings:   map[string]any{"basePath": "/orgs/{orgId}", "basePathValues": []string{"7/eu"}},
			wantCode:   codeInvalidBasePathValues,
			wantReason: "basePath declares exactly ONE parameter, so a declared element must split into exactly one component",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/admin PATCH", func(t *testing.T) {
			body := mustJSON(t, map[string]any{"settings": tc.settings, "editVersion": ws.EditVersion})
			rec := client.do(http.MethodPatch, target, body)
			assertBasePathRefusal(t, rec.Code, rec.Body.Bytes(), tc.wantCode, tc.wantReason)
			if got := getSettings(); got != before {
				t.Errorf("settings after refused PATCH changed:\nbefore = %s\nafter  = %s", before, got)
			}
		})

		t.Run(tc.name+"/MCP update_workspace_settings", func(t *testing.T) {
			body := mustJSON(t, map[string]any{"settings": tc.settings, "editVersion": ws.EditVersion})
			status, respBody, err := srv.CallAsMCP(t.Context(), mcpSrc, http.MethodPatch, target, []byte(body))
			if err != nil {
				t.Fatalf("CallAsMCP: %v", err)
			}
			assertBasePathRefusal(t, status, respBody, tc.wantCode, tc.wantReason)
			if got := getSettings(); got != before {
				t.Errorf("settings after refused MCP write changed:\nbefore = %s\nafter  = %s", before, got)
			}
		})
	}
}

// assertBasePathRefusal is the shared assertion both callers in
// TestPatchWorkspace_basePathValidation make: 400, and error.code exactly
// the one D4.3 names for this case.
func assertBasePathRefusal(t *testing.T, status int, body []byte, wantCode, context string) {
	t.Helper()
	if status != http.StatusBadRequest {
		t.Fatalf("%s: status = %d, want 400; body = %s", context, status, body)
	}
	var doc struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("%s: decode refusal body: %v; body = %s", context, err, body)
	}
	if doc.Error.Code != wantCode {
		t.Errorf("%s: error.code = %q, want %q; body = %s", context, doc.Error.Code, wantCode, body)
	}
}

// mustJSON marshals v for a request body, failing the test on error rather
// than threading one more return value through every call site above.
func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	return string(b)
}

// canonicalizeJSON re-encodes raw so two decodes of byte-identical JSON
// compare equal as strings regardless of insignificant whitespace —
// getSettings' own doc comment explains why this, rather than a raw byte
// comparison, is what "byte-identical" means for a document this test
// never controls the literal encoding of (it comes back through
// encoding/json inside the admin handler, not through this test's own
// jsonx-free comparison).
func canonicalizeJSON(t *testing.T, raw json.RawMessage) []byte {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("canonicalize settings JSON: %v", err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("re-marshal settings JSON: %v", err)
	}
	return b
}
