package authpreset

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/openapi"
)

// buildResolver loads doc (a decoded-from-JSON document root, as a Go
// literal so a test reads like the fixture it describes) through the real
// openapi package, matching internal/gen's own test helper: Load normalizes
// dialect quirks exactly as production does, and NewResolver gives Derive
// the real, budget-bounded $ref chaser.
func buildResolver(t *testing.T, doc map[string]any) (*openapi.Document, *openapi.Resolver) {
	t.Helper()
	raw, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	d, _, err := openapi.Load(raw)
	if err != nil {
		t.Fatalf("openapi.Load: %v", err)
	}
	return d, openapi.NewResolver(d, 0)
}

func baseDoc() map[string]any {
	return map[string]any{
		"openapi": "3.0.0",
		"info":    map[string]any{"title": "t", "version": "1"},
		"paths":   map[string]any{},
	}
}

// schemaOp builds one path -> method -> responses[status] operation with an
// inline application/json schema, and returns the Operation plus the exact
// SchemaPtr openapi/specs would compute for it — the same "#/paths/..."
// pointer grammar used throughout the repo's own fixtures.
func schemaOp(doc map[string]any, path, method string, status int, schema map[string]any) Operation {
	statusKey := "200"
	if status != 200 {
		statusKey = itoa(status)
	}
	paths := doc["paths"].(map[string]any)
	item, ok := paths[path].(map[string]any)
	if !ok {
		item = map[string]any{}
		paths[path] = item
	}
	item[method] = map[string]any{
		"responses": map[string]any{
			statusKey: map[string]any{
				"description": "ok",
				"content": map[string]any{
					"application/json": map[string]any{"schema": schema},
				},
			},
		},
	}
	opPtr := "#/paths/" + escapeSlashes(path) + "/" + method
	return Operation{
		Method:    method,
		Path:      path,
		Status:    status,
		SchemaPtr: opPtr + "/responses/" + statusKey + "/content/application~1json/schema",
		OpPointer: opPtr,
	}
}

func escapeSlashes(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '/' {
			out = append(out, '~', '1')
			continue
		}
		if s[i] == '~' {
			out = append(out, '~', '0')
			continue
		}
		out = append(out, s[i])
	}
	return string(out)
}

func itoa(n int) string {
	// status codes in these fixtures are always small positive 3-digit
	// numbers; strconv would work identically, this just avoids the import
	// for a one-line need.
	digits := [...]byte{byte('0' + n/100), byte('0' + (n/10)%10), byte('0' + n%10)}
	return string(digits[:])
}

func testSettings() domain.Settings {
	s := domain.DefaultSettings()
	s.Auth.SigningKey = "test-signing-key"
	s.Auth.JWTTTLSec = 3600
	return s
}

// --- IsAuthPath ----------------------------------------------------------

func TestIsAuthPath(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/api/v1/auth/token", true},
		{"/api/v1/auth/refresh", true},
		{"/api/v1/auth/userinfo", true},
		{"/login", true},
		{"/oauth/callback", true},
		{"/api/v1/me", true},
		{"/token", true},
		{"/refresh", true},
		{"/userinfo", true},
		// The four traps DESIGN §10 names explicitly: a whole-segment
		// match is required, a substring inside a longer segment (plural,
		// or a param name) must NOT match.
		{"/api/v1/ledger/tokens", false},
		{"/api/v1/id/{token}", false},
		{"/api/v1/ledger/accounts/link/{tokenId}", false},
		{"/api/v1/ledger/accounts/linkInfo/{tokenId}", false},
		// Unrelated paths.
		{"/api/v1/widgets/{id}", false},
		{"/api/v1/ledger/accounts", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsAuthPath(tt.path); got != tt.want {
				t.Errorf("IsAuthPath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

// --- expiryFieldName -------------------------------------------------------

func TestExpiryFieldName(t *testing.T) {
	tests := []struct {
		name         string
		wantExpiry   bool
		wantDuration bool
	}{
		{"exp", true, false},
		{"expires_in", true, true},
		{"expiresAt", true, false},
		{"expires_at", true, false},
		{"not_after", true, false},
		{"valid_until", true, false},
		{"token_expires_at", true, false},
		// The trap: a COUNT, not a time. Must not match by any
		// substring-of-"expire" heuristic.
		{"expire_count", false, false},
		{"expire_time", false, false}, // in the acceptance document too, and also not on DESIGN's list
		{"count", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotExpiry, gotDuration := expiryFieldName(tt.name)
			if gotExpiry != tt.wantExpiry || gotDuration != tt.wantDuration {
				t.Errorf("expiryFieldName(%q) = (%v, %v), want (%v, %v)", tt.name, gotExpiry, gotDuration, tt.wantExpiry, tt.wantDuration)
			}
		})
	}
}

// --- Derive: jwt + expiry on an auth path ---------------------------------

func TestDerive_AuthPath_JWTAndExpiry(t *testing.T) {
	doc := baseDoc()
	op := schemaOp(doc, "/api/v1/auth/token", "post", 200, map[string]any{
		"type":     "object",
		"required": []any{"token", "refresh_token", "token_expires_at", "is_whitelisted"},
		"properties": map[string]any{
			"token":            map[string]any{"type": "string"},
			"refresh_token":    map[string]any{"type": "string"},
			"token_expires_at": map[string]any{"type": "integer", "format": "int64"},
			"is_whitelisted":   map[string]any{"type": "boolean"},
		},
	})
	d, res := buildResolver(t, doc)

	prop, err := Derive(res, d, []Operation{op}, testSettings(), time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}

	byPath := map[string]Binding{}
	for _, b := range prop.Bindings {
		byPath[b.DataPath] = b
	}

	tok, ok := byPath["token"]
	if !ok || tok.Recipe.Kind != "jwt" || tok.Recipe.TTLSec != 0 {
		t.Errorf("token binding = %+v, ok=%v — want kind jwt, ttlSec 0 (use workspace default)", tok, ok)
	}
	if tok.Source != sourceAuthPath {
		t.Errorf("token binding source = %q, want %q", tok.Source, sourceAuthPath)
	}

	refresh, ok := byPath["refresh_token"]
	if !ok || refresh.Recipe.Kind != "jwt" {
		t.Fatalf("refresh_token binding = %+v, ok=%v — want kind jwt", refresh, ok)
	}
	if refresh.Recipe.TTLSec != 3600*refreshTTLMultiplier {
		t.Errorf("refresh_token ttlSec = %d, want %d (%dx the access ttl)", refresh.Recipe.TTLSec, 3600*refreshTTLMultiplier, refreshTTLMultiplier)
	}

	expiry, ok := byPath["token_expires_at"]
	if !ok || expiry.Recipe.Kind != "now" {
		t.Fatalf("token_expires_at binding = %+v, ok=%v — want kind now", expiry, ok)
	}
	if expiry.Recipe.Offset != "+3600s" {
		t.Errorf("token_expires_at offset = %q, want %q", expiry.Recipe.Offset, "+3600s")
	}

	if _, ok := byPath["is_whitelisted"]; ok {
		t.Errorf("is_whitelisted must not get a binding, got %+v", byPath["is_whitelisted"])
	}

	if len(prop.AuthPaths) != 1 || prop.AuthPaths[0] != "/api/v1/auth/token" {
		t.Errorf("AuthPaths = %v, want [/api/v1/auth/token]", prop.AuthPaths)
	}
}

// --- expires_in is a duration, not a timestamp ----------------------------

func TestDerive_ExpiresIn_IsAConstDuration_NotNow(t *testing.T) {
	doc := baseDoc()
	op := schemaOp(doc, "/api/v1/auth/token", "post", 200, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"expires_in": map[string]any{"type": "integer"},
		},
	})
	d, res := buildResolver(t, doc)

	set := testSettings()
	set.Auth.JWTTTLSec = 1800
	prop, err := Derive(res, d, []Operation{op}, set, time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(prop.Bindings) != 1 {
		t.Fatalf("Bindings = %v, want exactly one", prop.Bindings)
	}
	b := prop.Bindings[0]
	if b.Recipe.Kind != "const" {
		t.Fatalf("expires_in recipe kind = %q, want const (a duration is NOT a now+offset)", b.Recipe.Kind)
	}
	var n int
	if err := json.Unmarshal(b.Recipe.Data, &n); err != nil {
		t.Fatalf("unmarshal const value: %v", err)
	}
	if n != 1800 {
		t.Errorf("expires_in const value = %d, want 1800 (the ttl itself, not a future epoch)", n)
	}
}

// --- expire_count must never be treated as an expiry field ----------------

func TestDerive_ExpireCount_IsNeverBound(t *testing.T) {
	doc := baseDoc()
	op := schemaOp(doc, "/api/v1/invites", "post", 200, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"expire_time":  map[string]any{"type": "integer"},
			"expire_count": map[string]any{"type": "integer"},
		},
	})
	d, res := buildResolver(t, doc)

	prop, err := Derive(res, d, []Operation{op}, testSettings(), time.Unix(1_700_000_000, 0))
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	for _, b := range prop.Bindings {
		if b.DataPath == "expire_count" || b.DataPath == "expire_time" {
			t.Errorf("unexpected binding on %q: %+v — neither expire_time nor expire_count is on DESIGN's expiry list", b.DataPath, b)
		}
	}
}

// --- bare "id": bound on a profile shape, skipped on a list --------------

func TestDerive_BareID_ProfileShapeVsListItem(t *testing.T) {
	profileSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":    map[string]any{"type": "integer"},
			"name":  map[string]any{"type": "string"},
			"email": map[string]any{"type": "string"},
		},
	}

	t.Run("bound on a non-auth profile-shaped response", func(t *testing.T) {
		doc := baseDoc()
		op := schemaOp(doc, "/api/v1/profile", "get", 200, profileSchema)
		d, res := buildResolver(t, doc)

		prop, err := Derive(res, d, []Operation{op}, testSettings(), time.Now())
		if err != nil {
			t.Fatalf("Derive: %v", err)
		}
		found := false
		for _, b := range prop.Bindings {
			if b.DataPath == "id" {
				found = true
				if b.Recipe.Kind != "identity" || b.Recipe.Field != "id" {
					t.Errorf("id binding = %+v, want identity/id", b)
				}
			}
		}
		if !found {
			t.Errorf("expected a binding on \"id\" for a profile-shaped response, got %+v", prop.Bindings)
		}
	})

	t.Run("skipped on a list of the same shape", func(t *testing.T) {
		doc := baseDoc()
		op := schemaOp(doc, "/api/v1/users", "get", 200, map[string]any{
			"type":  "array",
			"items": profileSchema,
		})
		d, res := buildResolver(t, doc)

		prop, err := Derive(res, d, []Operation{op}, testSettings(), time.Now())
		if err != nil {
			t.Fatalf("Derive: %v", err)
		}
		for _, b := range prop.Bindings {
			if b.DataPath == "[*].id" {
				t.Errorf("bare \"id\" inside a list must NOT be bound (would overwrite every row's own id): %+v", b)
			}
		}
		noted := false
		for _, n := range prop.Notes {
			if containsAll(n, "id", "skipped") {
				noted = true
			}
		}
		if !noted {
			t.Errorf("expected a Note explaining the skipped bare id, got %v", prop.Notes)
		}
	})

	t.Run("skipped on a plain entity with no name/email/roles sibling", func(t *testing.T) {
		doc := baseDoc()
		op := schemaOp(doc, "/api/v1/widgets/{id}", "get", 200, map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":     map[string]any{"type": "integer"},
				"status": map[string]any{"type": "string"},
			},
		})
		d, res := buildResolver(t, doc)

		prop, err := Derive(res, d, []Operation{op}, testSettings(), time.Now())
		if err != nil {
			t.Fatalf("Derive: %v", err)
		}
		for _, b := range prop.Bindings {
			if b.DataPath == "id" {
				t.Errorf("bare \"id\" on a non-profile, non-auth object must not be bound: %+v", b)
			}
		}
	})
}

// TestDerive_BareID_SingleGenericAliasIsNotProfileShaped pins the Gate 2
// finding: a non-list object carrying exactly ONE generic alias sibling
// (name, or roles) is not a user profile just because it has one. Measured
// against the acceptance document, ReminderInfo (id+name+... — a created
// reminder, not the caller) and TenantGeneralInvite (id+roles+... —
// the roles the invite GRANTS, not the caller's own) are both real,
// non-list schemas with exactly one such sibling; before the two-alias
// floor, "id" bound to identity.id on both, silently replacing the
// resource's own id with the literal "1" from DefaultSettings.
func TestDerive_BareID_SingleGenericAliasIsNotProfileShaped(t *testing.T) {
	t.Run("id+name only (ReminderInfo shape)", func(t *testing.T) {
		doc := baseDoc()
		op := schemaOp(doc, "/api/v1/reminders", "post", 200, map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":            map[string]any{"type": "string", "format": "uuid"},
				"name":          map[string]any{"type": "string"},
				"send_datetime": map[string]any{"type": "string"},
				"status":        map[string]any{"type": "string"},
				"org_id":        map[string]any{"type": "integer"},
			},
		})
		d, res := buildResolver(t, doc)

		prop, err := Derive(res, d, []Operation{op}, testSettings(), time.Now())
		if err != nil {
			t.Fatalf("Derive: %v", err)
		}
		for _, b := range prop.Bindings {
			if b.DataPath == "id" {
				t.Errorf("a reminder's own id must not be overwritten with the caller's identity.id (single generic alias %q is not enough corroboration): %+v", "name", b)
			}
		}
	})

	t.Run("id+roles only (TenantGeneralInvite shape)", func(t *testing.T) {
		doc := baseDoc()
		op := schemaOp(doc, "/api/v1/invites/{inviteId}", "get", 200, map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":      map[string]any{"type": "string", "format": "uuid"},
				"roles":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"type":    map[string]any{"type": "string"},
				"creator": map[string]any{"type": "string"},
			},
		})
		d, res := buildResolver(t, doc)

		prop, err := Derive(res, d, []Operation{op}, testSettings(), time.Now())
		if err != nil {
			t.Fatalf("Derive: %v", err)
		}
		for _, b := range prop.Bindings {
			if b.DataPath == "id" {
				t.Errorf("an invite's own id must not be overwritten with the caller's identity.id (the invite's own %q — roles it GRANTS, not the caller's — is not enough corroboration): %+v", "roles", b)
			}
		}
	})
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !stringsContains(s, sub) {
			return false
		}
	}
	return true
}

func stringsContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// --- the generic name/email family is gated exactly like bare "id" -------
//
// Measured on the real acceptance document: without this gate,
// response.items[*].district.name (a CITY's district — nothing to do with
// the logged-in user) got bound to identity.name right alongside a genuine
// profile's own name field. That is the "operator has to undo it by hand"
// failure DESIGN §10 calls out for "id", just one alias spelling over —
// these two cases pin the fix.

func TestDerive_GatedNameAlias_NotBoundInsideAList(t *testing.T) {
	doc := baseDoc()
	// A LIST of cities, each with its own id+name — the shape of the real
	// bug this test pins: response.items[*].district.name in the
	// acceptance document. Before "name" was gated the same way as bare
	// "id", this bound unconditionally and every row's own name became the
	// SAME logged-in identity's name.
	op := schemaOp(doc, "/api/v1/cities", "get", 200, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"items": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":   map[string]any{"type": "integer"},
						"name": map[string]any{"type": "string"},
					},
				},
			},
		},
	})
	d, res := buildResolver(t, doc)

	prop, err := Derive(res, d, []Operation{op}, testSettings(), time.Now())
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	for _, b := range prop.Bindings {
		if b.DataPath == "items[*].name" || b.DataPath == "items[*].id" {
			t.Errorf("a list item's own id/name must not be rewritten to the logged-in identity's: %+v", b)
		}
	}
}

func TestDerive_GatedNameAlias_BoundOnGenuineProfile(t *testing.T) {
	doc := baseDoc()
	op := schemaOp(doc, "/api/v1/account", "get", 200, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":       map[string]any{"type": "integer"},
			"fullName": map[string]any{"type": "string"},
			"email":    map[string]any{"type": "string"},
		},
	})
	d, res := buildResolver(t, doc)

	prop, err := Derive(res, d, []Operation{op}, testSettings(), time.Now())
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	byPath := map[string]Binding{}
	for _, b := range prop.Bindings {
		byPath[b.DataPath] = b
	}
	if b, ok := byPath["fullName"]; !ok || b.Recipe.Field != "name" {
		t.Errorf("fullName = %+v, ok=%v — want identity.name on a genuine profile shape", b, ok)
	}
	if b, ok := byPath["email"]; !ok || b.Recipe.Field != "email" {
		t.Errorf("email = %+v, ok=%v — want identity.email on a genuine profile shape", b, ok)
	}
	if b, ok := byPath["id"]; !ok || b.Recipe.Field != "id" {
		t.Errorf("id = %+v, ok=%v — want identity.id on a genuine profile shape", b, ok)
	}
}

// --- explicit aliases bind unconditionally, including inside a list ------

func TestDerive_ExplicitAlias_BindsInsideAList(t *testing.T) {
	doc := baseDoc()
	op := schemaOp(doc, "/api/v1/reminders", "get", 200, map[string]any{
		"type": "array",
		"items": map[string]any{
			"type": "object",
			"properties": map[string]any{
				"org_id": map[string]any{"type": "integer"},
			},
		},
	})
	d, res := buildResolver(t, doc)

	prop, err := Derive(res, d, []Operation{op}, testSettings(), time.Now())
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	found := false
	for _, b := range prop.Bindings {
		if b.DataPath == "[*].org_id" {
			found = true
			if b.Recipe.Field != "org.id" {
				t.Errorf("org_id binding field = %q, want org.id", b.Recipe.Field)
			}
		}
	}
	if !found {
		t.Errorf("org_id is an EXPLICIT alias and should bind even inside a list, got %+v", prop.Bindings)
	}
}

// --- type refusals: Coerce is the arbiter ---------------------------------

func TestDerive_TypeRefusals(t *testing.T) {
	t.Run("jwt onto an integer is refused", func(t *testing.T) {
		doc := baseDoc()
		op := schemaOp(doc, "/api/v1/auth/token", "post", 200, map[string]any{
			"type": "object",
			"properties": map[string]any{
				"token": map[string]any{"type": "integer"},
			},
		})
		d, res := buildResolver(t, doc)
		prop, err := Derive(res, d, []Operation{op}, testSettings(), time.Now())
		if err != nil {
			t.Fatalf("Derive: %v", err)
		}
		for _, b := range prop.Bindings {
			if b.DataPath == "token" {
				t.Errorf("jwt onto an integer field must be refused, got %+v", b)
			}
		}
		if len(prop.Notes) == 0 {
			t.Errorf("expected a Note explaining the refusal")
		}
	})

	t.Run("identity.roles (array) onto a string is refused", func(t *testing.T) {
		doc := baseDoc()
		op := schemaOp(doc, "/api/v1/profile", "get", 200, map[string]any{
			"type": "object",
			"properties": map[string]any{
				"roles": map[string]any{"type": "string"},
			},
		})
		d, res := buildResolver(t, doc)
		prop, err := Derive(res, d, []Operation{op}, testSettings(), time.Now())
		if err != nil {
			t.Fatalf("Derive: %v", err)
		}
		for _, b := range prop.Bindings {
			if b.DataPath == "roles" {
				t.Errorf("identity.roles onto a string field must be refused, got %+v", b)
			}
		}
	})

	t.Run("expiry onto a string is refused (must stay numeric)", func(t *testing.T) {
		doc := baseDoc()
		op := schemaOp(doc, "/api/v1/auth/token", "post", 200, map[string]any{
			"type": "object",
			"properties": map[string]any{
				"expiresAt": map[string]any{"type": "string"},
			},
		})
		d, res := buildResolver(t, doc)
		prop, err := Derive(res, d, []Operation{op}, testSettings(), time.Now())
		if err != nil {
			t.Fatalf("Derive: %v", err)
		}
		for _, b := range prop.Bindings {
			if b.DataPath == "expiresAt" {
				t.Errorf("a string-typed expiresAt must be refused (DESIGN requires a NUMERIC property), got %+v", b)
			}
		}
	})
}

// TestDerive_RolesArrayOfObjects_IsRefused is round-1 finding #5:
// identity.roles is a []string (DESIGN §10's alias table), and
// [recipes.Coerce]'s own "array" branch only checks that the SAMPLE is a Go
// slice — it never inspects the TARGET schema's own "items" sub-schema. A
// "roles"/"permissions" property declared as an array of Role OBJECTS (the
// acceptance document's actual shape on 20 of 114 operations) must be
// skipped, with a note, exactly like the scalar-type refusals above —
// binding it produces a body that fails its own response schema on every
// request.
func TestDerive_RolesArrayOfObjects_IsRefused(t *testing.T) {
	doc := baseDoc()
	op := schemaOp(doc, "/api/v1/tenants", "get", 200, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"roles": map[string]any{
				"type": "array",
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id":   map[string]any{"type": "integer"},
						"name": map[string]any{"type": "string"},
					},
				},
			},
		},
	})
	d, res := buildResolver(t, doc)
	prop, err := Derive(res, d, []Operation{op}, testSettings(), time.Now())
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	for _, b := range prop.Bindings {
		if b.DataPath == "roles" {
			t.Errorf("identity.roles onto an array of OBJECTS must be refused, got %+v", b)
		}
	}
	found := false
	for _, n := range prop.Notes {
		if strings.Contains(n, "roles") && strings.Contains(n, "OBJECTS") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a Note explaining the array-of-objects refusal, got %v", prop.Notes)
	}
}

// TestDerive_RolesArrayOfStrings_StillBinds is
// TestDerive_RolesArrayOfObjects_IsRefused's non-regression: the ordinary,
// DESIGN-documented shape (an array of plain strings) must still bind —
// the fix narrows the guard to items.type == "object" specifically, not to
// every array.
func TestDerive_RolesArrayOfStrings_StillBinds(t *testing.T) {
	doc := baseDoc()
	op := schemaOp(doc, "/api/v1/tenants", "get", 200, map[string]any{
		"type": "object",
		"properties": map[string]any{
			"roles": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
	})
	d, res := buildResolver(t, doc)
	prop, err := Derive(res, d, []Operation{op}, testSettings(), time.Now())
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	found := false
	for _, b := range prop.Bindings {
		if b.DataPath == "roles" {
			found = true
			if b.Recipe.Field != "roles" {
				t.Errorf("roles binding field = %q, want roles", b.Recipe.Field)
			}
		}
	}
	if !found {
		t.Errorf("identity.roles onto an array of strings must still bind, got %+v", prop.Bindings)
	}
}

// --- cycle-safe walk: a schema that $refs itself must not hang -----------

func TestDerive_SelfReferencingSchema_DoesNotHang(t *testing.T) {
	doc := baseDoc()
	doc["components"] = map[string]any{
		"schemas": map[string]any{
			"Section": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":    map[string]any{"type": "integer"},
					"name":  map[string]any{"type": "string"},
					"email": map[string]any{"type": "string"},
					"children": map[string]any{
						"type":  "array",
						"items": map[string]any{"$ref": "#/components/schemas/Section"},
					},
				},
			},
		},
	}
	op := schemaOp(doc, "/api/v1/sections/{id}", "get", 200, map[string]any{"$ref": "#/components/schemas/Section"})
	d, res := buildResolver(t, doc)

	done := make(chan struct{})
	var prop *Proposal
	var err error
	go func() {
		prop, err = Derive(res, d, []Operation{op}, testSettings(), time.Now())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Derive did not return within 5s on a self-referencing schema — the walk is not cycle-safe")
	}
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	// The root Section itself IS profile-shaped (id + name + email siblings,
	// not inside an array — two corroborating aliases, the floor
	// hasProfileAlias requires) — its own bare id is legitimately bound.
	// What must NOT happen is an unbounded walk into
	// children[*].children[*]....
	found := false
	for _, b := range prop.Bindings {
		if b.DataPath == "id" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected the root Section's own bare id to be bound, got %+v", prop.Bindings)
	}
}

// --- securitySchemes are always recorded, even with no bindings ----------

func TestDerive_CollectsSecuritySchemes(t *testing.T) {
	doc := baseDoc()
	doc["components"] = map[string]any{
		"securitySchemes": map[string]any{
			"signAuth":   map[string]any{"type": "apiKey", "name": "X-Auth-Sign", "in": "header"},
			"bearerAuth": map[string]any{"type": "http", "scheme": "bearer"},
		},
	}
	d, res := buildResolver(t, doc)

	prop, err := Derive(res, d, nil, testSettings(), time.Now())
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(prop.Schemes) != 2 {
		t.Fatalf("Schemes = %v, want 2 entries", prop.Schemes)
	}
	want := map[string]bool{"signAuth: apiKey": true, "bearerAuth: http bearer": true}
	for _, s := range prop.Schemes {
		if !want[s] {
			t.Errorf("unexpected scheme entry %q", s)
		}
	}
}

// --- SampleJWT mints even when the identity has no id ---------------------

func TestDerive_SampleJWT_MintsWithNoIdentityID(t *testing.T) {
	doc := baseDoc()
	set := testSettings()
	set.Identity.ID = nil

	d, res := buildResolver(t, doc)
	prop, err := Derive(res, d, nil, set, time.Now())
	if err != nil {
		t.Fatalf("Derive must not fail when the identity has no id: %v", err)
	}
	if prop.SampleJWT == "" {
		t.Error("SampleJWT must still be minted even with no identity id")
	}
}

// --- no response schema is a Note, not a crash ----------------------------

func TestDerive_NoSchema_IsANoteNotAFailure(t *testing.T) {
	d, res := buildResolver(t, baseDoc())
	op := Operation{Method: "GET", Path: "/api/v1/ping", Status: 204, SchemaPtr: ""}

	prop, err := Derive(res, d, []Operation{op}, testSettings(), time.Now())
	if err != nil {
		t.Fatalf("Derive: %v", err)
	}
	if len(prop.Bindings) != 0 {
		t.Errorf("Bindings = %v, want none", prop.Bindings)
	}
	if len(prop.Notes) != 1 {
		t.Errorf("Notes = %v, want exactly one explaining the missing schema", prop.Notes)
	}
}
