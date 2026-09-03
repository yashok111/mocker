package authpreset

import (
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/testspec"
)

// TestAcceptance_AuthPreset runs Derive over the real acceptance document
// (skips cleanly via internal/testspec when it isn't present — a fresh
// clone has no such file). It pins the handful of facts the phase digest
// measured with jq, not remembered: the three auth paths, the jwt bindings
// on POST /api/v1/auth/token, the expiry binding on token_expires_at, and
// the identity binding AuthUserInfo.app_id -> identity.org.id.
//
// This test deliberately does NOT import internal/specs to build its
// operation list — authpreset is a leaf specifically so it stays testable
// without pulling that package (or a database) in, and the test should
// prove that property, not quietly undermine it. acceptanceOperations
// below is a small, self-contained walk of doc.Root()["paths"], just
// enough to produce the (Method, Path, Status, SchemaPtr, OpPointer) tuples
// Derive's own contract asks for.
func TestAcceptance_AuthPreset(t *testing.T) {
	raw := testspec.Bytes(t)
	doc, _, err := openapi.Load(raw)
	if err != nil {
		t.Fatalf("openapi.Load: %v", err)
	}
	res := openapi.NewResolver(doc, openapi.DefaultRefBudget)

	ops := acceptanceOperations(t, doc, res)
	t.Logf("measured %d response-variant operations across the document", len(ops))

	set := domain.DefaultSettings()
	prop, err := Derive(res, doc, ops, set, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	t.Logf("Schemes: %v", prop.Schemes)
	t.Logf("AuthPaths: %v", prop.AuthPaths)
	t.Logf("Bindings: %d, Notes: %d", len(prop.Bindings), len(prop.Notes))

	// --- securitySchemes: signAuth (apiKey), bearerAuth (http bearer),
	// apiAuth (apiKey), BearerAuth (http bearer) — measured with jq.
	if len(prop.Schemes) != 4 {
		t.Errorf("Schemes = %v, want 4 entries", prop.Schemes)
	}

	// --- the three auth paths, and ONLY those three (DESIGN §10's trap
	// paths — ledger/tokens, id/{token}, the two linkInfo/{tokenId}
	// paths — must not appear).
	wantAuthPaths := []string{
		"/api/v1/auth/refresh",
		"/api/v1/auth/token",
		"/api/v1/auth/userinfo",
	}
	if got := prop.AuthPaths; !equalStrings(got, wantAuthPaths) {
		t.Errorf("AuthPaths = %v, want %v", got, wantAuthPaths)
	}

	byOp := map[string][]Binding{}
	for _, b := range prop.Bindings {
		key := b.Method + " " + b.Path + " " + strconv.Itoa(b.Status)
		byOp[key] = append(byOp[key], b)
	}

	// --- POST /api/v1/auth/token, 200: jwt on token and refresh_token,
	// now on token_expires_at, nothing on is_whitelisted (boolean).
	tokenBindings := bindingsByPath(byOp["POST /api/v1/auth/token 200"])
	if b, ok := tokenBindings["response.token"]; !ok || b.Recipe.Kind != "jwt" {
		t.Errorf("response.token = %+v, ok=%v — want kind jwt", b, ok)
	}
	if b, ok := tokenBindings["response.refresh_token"]; !ok || b.Recipe.Kind != "jwt" {
		t.Errorf("response.refresh_token = %+v, ok=%v — want kind jwt", b, ok)
	} else if b.Recipe.TTLSec <= set.Auth.JWTTTLSec {
		t.Errorf("response.refresh_token ttlSec = %d, want longer than the access token's %d", b.Recipe.TTLSec, set.Auth.JWTTTLSec)
	}
	if b, ok := tokenBindings["response.token_expires_at"]; !ok || b.Recipe.Kind != "now" {
		t.Errorf("response.token_expires_at = %+v, ok=%v — want kind now", b, ok)
	}
	if _, ok := tokenBindings["response.is_whitelisted"]; ok {
		t.Errorf("response.is_whitelisted must not get a binding (boolean, not an alias/token/expiry field)")
	}

	// --- POST /api/v1/auth/userinfo, 200: identity binding on app_id ->
	// identity.org.id (AuthUserInfo's only required property, and one of
	// DESIGN §10's org-id aliases).
	userinfoBindings := bindingsByPath(byOp["POST /api/v1/auth/userinfo 200"])
	b, ok := userinfoBindings["response.app_id"]
	if !ok {
		t.Fatalf("response.app_id: no binding found among %v", userinfoBindings)
	}
	if b.Recipe.Kind != "identity" || b.Recipe.Field != "org.id" {
		t.Errorf("response.app_id recipe = %+v, want kind identity, field org.id", b.Recipe)
	}

	if len(prop.Notes) == 0 {
		t.Error("Notes is empty — a 130-operation document should have at least some skipped fields/schemas worth explaining")
	}
	if prop.SampleJWT == "" {
		t.Error("SampleJWT must not be empty")
	}
}

func bindingsByPath(bs []Binding) map[string]Binding {
	out := make(map[string]Binding, len(bs))
	for _, b := range bs {
		out[b.DataPath] = b
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sort.Strings(a)
	sort.Strings(b)
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// acceptanceOperations walks doc's paths/methods/responses into the
// (Method, Path, Status, SchemaPtr, OpPointer) tuples Derive consumes —
// ONE Operation per declared response selector with a JSON schema,
// mirroring what internal/specs' indexer computes but without importing it
// (see the package doc comment on why authpreset stays a leaf). It is
// intentionally small: no parse-error bookkeeping, no default-selection
// rule, just "every numeric or default selector this document declares,
// with whatever schema pointer its content.application/json.schema
// resolves to".
func acceptanceOperations(t *testing.T, doc *openapi.Document, res *openapi.Resolver) []Operation {
	t.Helper()
	pathsRaw, _ := doc.Root()["paths"].(map[string]any)
	methods := []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

	var ops []Operation
	for path, itemRaw := range pathsRaw {
		item, ok := itemRaw.(map[string]any)
		if !ok {
			continue
		}
		for _, method := range methods {
			opRaw, ok := item[method]
			if !ok {
				continue
			}
			opObj, ok := opRaw.(map[string]any)
			if !ok {
				continue
			}
			opPointer := "#/paths/" + escapePointerToken(path) + "/" + method
			responses, _ := opObj["responses"].(map[string]any)
			for sel, respRaw := range responses {
				status, ok := classifySelector(sel)
				if !ok {
					continue
				}
				respPointer := opPointer + "/responses/" + escapePointerToken(sel)
				schemaPtr := schemaPointerFor(res, respRaw, respPointer)
				ops = append(ops, Operation{
					Method:    strings.ToUpper(method),
					Path:      path,
					Status:    status,
					SchemaPtr: schemaPtr,
					OpPointer: opPointer,
				})
			}
		}
	}
	return ops
}

// classifySelector mirrors internal/specs' own rule closely enough for this
// test's purposes: a 3-digit numeric status is sent as-is, "default" and
// "2XX" are reported as 200 (they are not sendable codes on their own),
// anything else is not a response this test tries to interpret.
func classifySelector(sel string) (int, bool) {
	switch sel {
	case "default", "2XX":
		return 200, true
	}
	if n, err := strconv.Atoi(sel); err == nil && n >= 100 && n <= 599 {
		return n, true
	}
	return 0, false
}

// schemaPointerFor follows respRaw's own $ref chain (a response object can
// be a $ref, OAS 3.0 §4.8.16) and returns the pointer to its
// content.application/json.schema node, or "" when there is none —
// matching gen.ResponseVariant.SchemaPtr's own contract.
func schemaPointerFor(res *openapi.Resolver, respRaw any, pointer string) string {
	lastPointer, resolved, err := res.ResolveNodePointer(respRaw)
	if err != nil {
		return ""
	}
	basePointer := pointer
	if lastPointer != "" {
		basePointer = lastPointer
	}
	respObj, ok := resolved.(map[string]any)
	if !ok {
		return ""
	}
	content, ok := respObj["content"].(map[string]any)
	if !ok {
		return ""
	}
	entry, ok := content["application/json"].(map[string]any)
	if !ok {
		return ""
	}
	if _, hasSchema := entry["schema"]; !hasSchema {
		return ""
	}
	return basePointer + "/content/application~1json/schema"
}

// escapePointerToken applies RFC 6901's two escapes ("~" -> "~0" first,
// then "/" -> "~1") to one JSON-pointer path segment — the same rule
// internal/specs applies when it builds operations.pointer.
func escapePointerToken(tok string) string {
	tok = strings.ReplaceAll(tok, "~", "~0")
	tok = strings.ReplaceAll(tok, "/", "~1")
	return tok
}
