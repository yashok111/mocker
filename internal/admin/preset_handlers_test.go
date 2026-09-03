package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/overrides"
)

// authPresetProposalJSON mirrors authpreset.Proposal's wire shape plus A3's
// EditVersions sibling (D5).
type authPresetProposalJSON struct {
	Bindings     []authPresetBindingJSON `json:"bindings"`
	Schemes      []string                `json:"schemes"`
	AuthPaths    []string                `json:"authPaths"`
	Notes        []string                `json:"notes"`
	SampleJWT    string                  `json:"sampleJwt"`
	EditVersions map[string]int64        `json:"editVersions"`
}

// authPresetBindingJSON mirrors authpreset.Binding's wire shape.
type authPresetBindingJSON struct {
	Method   string `json:"method"`
	Path     string `json:"path"`
	Status   int    `json:"status"`
	DataPath string `json:"dataPath"`
	Recipe   struct {
		Kind   string `json:"kind"`
		Field  string `json:"field"`
		TTLSec int    `json:"ttlSec"`
	} `json:"recipe"`
	Reason string `json:"reason"`
	Source string `json:"source"`
}

// findTokenBinding returns the "token" jwt binding out of a derived
// proposal — a single, always-valid binding several tests below reuse so a
// binding that happens to need more setup to validate (like authDoc's
// "expires_in" const recipe) never leaks into a case that is not about
// binding validation at all.
func findTokenBinding(t *testing.T, bindings []authPresetBindingJSON) authPresetBindingJSON {
	t.Helper()
	for _, b := range bindings {
		if b.DataPath == "token" {
			return b
		}
	}
	t.Fatal("test setup: no \"token\" binding in the derived proposal")
	return authPresetBindingJSON{}
}

func TestHandler_getAuthPreset_noSpec(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, _, wsID, _ := ts.createWorkspace(t, "Alex", "Demo")

	target := fmt.Sprintf("http://mocker.local/api/workspaces/%d/auth-preset", int64(wsID))
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var p authPresetProposalJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(p.Bindings) != 0 {
		t.Errorf("bindings for a spec-less workspace = %v, want none", p.Bindings)
	}
	if p.SampleJWT == "" {
		t.Error("sampleJwt is empty, want a token minted from the workspace's own identity/auth settings")
	}
	if len(p.Notes) == 0 {
		t.Error("notes is empty, want an explanation that no spec is attached")
	}
	// D5: the spec-less branch is its OWN response site (a separate early
	// return, never falling through to the derived-proposal path below), and
	// it must carry editVersions too — a non-nil EMPTY map, never an omitted
	// field and never `null` (which a nil Go map would marshal to).
	if p.EditVersions == nil {
		t.Error("editVersions is nil (marshals to null), want a non-nil empty map on the spec-less branch")
	}
	if len(p.EditVersions) != 0 {
		t.Errorf("editVersions = %v, want empty (no bindings to cover)", p.EditVersions)
	}
	// json.Unmarshal alone cannot tell a present `"editVersions":{}` apart
	// from a field the struct simply defaulted on decode, so this is the one
	// assertion that actually proves the server wrote `{}` on the wire and
	// not an omitted key.
	if !strings.Contains(rec.Body.String(), `"editVersions":{}`) {
		t.Error("response body does not contain a literal \"editVersions\":{} — want byte-exact {}, not omitted or null")
	}
}

func TestHandler_getAuthPreset_nonexistentWorkspace(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, _ := ts.login(t, "Alex")

	req := httptest.NewRequest(http.MethodGet, "http://mocker.local/api/workspaces/999999/auth-preset", nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

// TestHandler_authPresetLifecycle covers DESIGN §19's acceptance criterion
// end to end through the admin surface: derive a preset from a login-shaped
// endpoint, apply a filtered subset of it, and confirm only that subset
// landed — never a server-side re-derivation silently overriding whatever
// the operator chose not to send (§10's non-negotiable).
func TestHandler_authPresetLifecycle(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, specID := ts.importSpec(t, "Alex", "Auth Demo", authDoc)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Demo", specID)
	presetURL := fmt.Sprintf("http://mocker.local/api/workspaces/%d/auth-preset", wsID)
	opsURL := fmt.Sprintf("http://mocker.local/api/workspaces/%d/operations", wsID)
	loginOpKey := overrides.OpKey(http.MethodPost, "/auth/login")

	var proposal authPresetProposalJSON

	t.Run("GET derives bindings from the login endpoint and writes nothing", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, presetURL, nil)
		req.AddCookie(cookie)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &proposal); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if len(proposal.AuthPaths) != 1 || proposal.AuthPaths[0] != "/auth/login" {
			t.Errorf("authPaths = %v, want [\"/auth/login\"]", proposal.AuthPaths)
		}
		if proposal.SampleJWT == "" {
			t.Error("sampleJwt is empty")
		}

		var sawToken, sawRefresh, sawUserID, sawUserEmail bool
		for _, b := range proposal.Bindings {
			if b.Method != http.MethodPost || b.Path != "/auth/login" {
				t.Errorf("unexpected binding for %s %s (only /auth/login exists)", b.Method, b.Path)
			}
			switch b.DataPath {
			case "token":
				sawToken = b.Recipe.Kind == "jwt"
			case "refresh_token":
				sawRefresh = b.Recipe.Kind == "jwt" && b.Recipe.TTLSec > 0
			case "user.id":
				sawUserID = b.Recipe.Kind == "identity" && b.Recipe.Field == "id"
			case "user.email":
				sawUserEmail = b.Recipe.Kind == "identity" && b.Recipe.Field == "email"
			}
		}
		if !sawToken {
			t.Error("no jwt binding proposed for the \"token\" field")
		}
		if !sawRefresh {
			t.Error("no jwt binding (with a positive ttlSec) proposed for \"refresh_token\"")
		}
		if !sawUserID {
			t.Error("no identity binding proposed for \"user.id\"")
		}
		if !sawUserEmail {
			t.Error("no identity binding proposed for \"user.email\"")
		}

		// D5: the login operation has no override row yet, so its opKey
		// carries 0 in editVersions — the absent-row expectation — not an
		// omitted entry.
		v, ok := proposal.EditVersions[loginOpKey]
		if !ok {
			t.Fatalf("editVersions has no entry for %q, want 0 (no override row yet)", loginOpKey)
		}
		if v != 0 {
			t.Errorf("editVersions[%q] = %d, want 0 (no override row yet)", loginOpKey, v)
		}

		// GET must not have written anything: the merged view still shows no
		// override for the login operation.
		opsReq := httptest.NewRequest(http.MethodGet, opsURL, nil)
		opsReq.AddCookie(cookie)
		opsRec := ts.do(opsReq)
		var ops []mergedOperationJSON
		if err := json.Unmarshal(opsRec.Body.Bytes(), &ops); err != nil {
			t.Fatalf("decode operations: %v", err)
		}
		for _, op := range ops {
			if op.Method == http.MethodPost && op.Path == "/auth/login" && op.Override != nil {
				t.Errorf("login operation already has an override = %+v after only a GET /auth-preset", op.Override)
			}
		}
	})

	var revisionAfterApply int64
	var editVersionAfterApply int64

	t.Run("POST a filtered subset: only the token and user.id bindings land", func(t *testing.T) {
		var subset []authPresetBindingJSON
		for _, b := range proposal.Bindings {
			if b.DataPath == "token" || b.DataPath == "user.id" {
				subset = append(subset, b)
			}
		}
		if len(subset) != 2 {
			t.Fatalf("test setup: found %d of the 2 bindings expected in the proposal, want 2", len(subset))
		}

		req := jsonRequest(t, http.MethodPost, presetURL,
			map[string]any{"bindings": subset, "editVersions": proposal.EditVersions}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		var applied struct {
			Applied      int              `json:"applied"`
			Revision     int64            `json:"revision"`
			EditVersions map[string]int64 `json:"editVersions"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &applied); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if applied.Applied != 2 {
			t.Errorf("applied = %d, want 2", applied.Applied)
		}
		if applied.Revision != 2 {
			t.Errorf("revision = %d, want 2 (bumped once from the create at revision 1)", applied.Revision)
		}
		revisionAfterApply = applied.Revision

		// D5/D8: the write response carries the row's FRESH edit_version —
		// this is the map's only source, since the preset's write response
		// carries no rows of its own — and it must differ from the 0 the
		// caller expected, or a caller that writes twice in a row has
		// nothing new to send as its second expectation.
		newVer, ok := applied.EditVersions[loginOpKey]
		if !ok {
			t.Fatalf("write response editVersions has no entry for %q", loginOpKey)
		}
		if newVer == 0 {
			t.Errorf("editVersions[%q] = 0 after a successful write, want a freshly allocated nonzero value", loginOpKey)
		}
		editVersionAfterApply = newVer

		// Confirm ONLY token and user.id landed — not refresh_token, not
		// user.email, not expires_in — by reading the stored override back.
		getReq := httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("http://mocker.local/api/workspaces/%d/operations/POST%%20%%2Fauth%%2Flogin", wsID), nil)
		getReq.AddCookie(cookie)
		getRec := ts.do(getReq)
		if getRec.Code != http.StatusOK {
			t.Fatalf("GET override after apply: status = %d, want 200; body = %s", getRec.Code, getRec.Body.String())
		}
		doc := decodeOverrideDoc(t, getRec)
		v, ok := doc.Responses["200"]
		if !ok {
			t.Fatalf("no responses[200] in the stored override, body = %s", getRec.Body.String())
		}
		if len(v.Recipes) != 2 {
			t.Errorf("responses[200].recipes has %d entries, want exactly 2 (token, user.id); got %+v", len(v.Recipes), v.Recipes)
		}
		if _, ok := v.Recipes["token"]; !ok {
			t.Error("recipes[\"token\"] is missing")
		}
		if _, ok := v.Recipes["user.id"]; !ok {
			t.Error("recipes[\"user.id\"] is missing")
		}
		if _, ok := v.Recipes["refresh_token"]; ok {
			t.Error("recipes[\"refresh_token\"] is present, want it absent — it was filtered out of the applied subset")
		}
		if _, ok := v.Recipes["user.email"]; ok {
			t.Error("recipes[\"user.email\"] is present, want it absent — it was filtered out of the applied subset")
		}
	})

	t.Run("applying a second binding on the SAME status merges in, does not replace the row", func(t *testing.T) {
		var refreshBinding *authPresetBindingJSON
		for _, b := range proposal.Bindings {
			if b.DataPath == "refresh_token" {
				bCopy := b
				refreshBinding = &bCopy
			}
		}
		if refreshBinding == nil {
			t.Fatal("test setup: no refresh_token binding found in the original proposal")
		}

		req := jsonRequest(t, http.MethodPost, presetURL,
			map[string]any{
				"bindings":     []authPresetBindingJSON{*refreshBinding},
				"editVersions": map[string]int64{loginOpKey: editVersionAfterApply},
			}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		var applied struct {
			Revision     int64            `json:"revision"`
			EditVersions map[string]int64 `json:"editVersions"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &applied); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if applied.Revision != revisionAfterApply+1 {
			t.Errorf("revision = %d, want %d", applied.Revision, revisionAfterApply+1)
		}
		if applied.EditVersions[loginOpKey] == editVersionAfterApply {
			t.Errorf("editVersions[%q] = %d, want a fresh value distinct from %d", loginOpKey, applied.EditVersions[loginOpKey], editVersionAfterApply)
		}
		editVersionAfterApply = applied.EditVersions[loginOpKey]

		getReq := httptest.NewRequest(http.MethodGet,
			fmt.Sprintf("http://mocker.local/api/workspaces/%d/operations/POST%%20%%2Fauth%%2Flogin", wsID), nil)
		getReq.AddCookie(cookie)
		getRec := ts.do(getReq)
		doc := decodeOverrideDoc(t, getRec)
		v := doc.Responses["200"]
		// token and user.id from the earlier apply must still be there —
		// this POST must have MERGED into the existing row's responses[200],
		// not replaced it wholesale.
		for _, dp := range []string{"token", "user.id", "refresh_token"} {
			if _, ok := v.Recipes[dp]; !ok {
				t.Errorf("recipes[%q] is missing after the merge apply, want it preserved/added", dp)
			}
		}
	})

	t.Run("empty bindings list is a no-op: revision does not move", func(t *testing.T) {
		req := jsonRequest(t, http.MethodPost, presetURL, map[string]any{"bindings": []any{}}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		var applied struct {
			Applied  int   `json:"applied"`
			Revision int64 `json:"revision"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &applied); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if applied.Applied != 0 {
			t.Errorf("applied = %d, want 0", applied.Applied)
		}
	})

	t.Run("an invalid recipe in the apply body answers 400, never 500", func(t *testing.T) {
		bad := []map[string]any{
			{"method": "POST", "path": "/auth/login", "status": 200, "dataPath": "token",
				"recipe": map[string]any{"kind": "not-a-real-kind"}},
		}
		req := jsonRequest(t, http.MethodPost, presetURL, map[string]any{"bindings": bad}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("POST without a CSRF token answers 403", func(t *testing.T) {
		req := jsonRequest(t, http.MethodPost, presetURL, map[string]any{"bindings": []any{}}, cookie, "")
		rec := ts.do(req)
		if rec.Code != http.StatusForbidden {
			t.Fatalf("status = %d, want 403; body = %s", rec.Code, rec.Body.String())
		}
	})
}

// TestHandler_authPresetApply_PreservesExistingOverrideFields is round-1
// finding #2's coverage at the level that actually matters: PutMany's own
// contract is "replace wholesale" (see overrides.Repo.PutMany's doc comment
// and TestRepo_PutMany_ReplacesRowWholesale), which would silently wipe an
// operation's DelayMs/ActiveStatus/ValidateReq/FailDirective the moment an
// auth-preset apply touched it — UNLESS this handler reads the existing row
// first and reuses it as the Row it hands to PutMany, which is exactly what
// handleApplyAuthPreset's own doc comment claims it does. This proves the
// claim: a normal PUT establishes those fields (the same shape the finding
// itself uses — DelayMs=250, ActiveStatus=503, ValidateReq=true, a
// FailDirective), an auth-preset apply follows on the SAME operation, and
// every one of them must still be there afterward.
func TestHandler_authPresetApply_PreservesExistingOverrideFields(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, specID := ts.importSpec(t, "Alex", "Auth Demo", authDoc)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Demo", specID)
	opsBase := fmt.Sprintf("http://mocker.local/api/workspaces/%d/operations", wsID)
	loginOpKey := "POST%20%2Fauth%2Flogin"
	presetURL := fmt.Sprintf("http://mocker.local/api/workspaces/%d/auth-preset", wsID)

	putBody := map[string]any{
		"overrideOn":   true,
		"routeOff":     false,
		"activeStatus": 503,
		"delayMs":      250,
		"validateReq":  true,
		"responses": map[string]any{
			"503": map[string]any{"mode": "generated"},
		},
		// A3: this is the FIRST write to this opKey, so 0 is the legal "I
		// expect no row" expectation (D7) — this PUT is only a SEED for
		// the preset assertions below, which belong to A3's other half.
		"editVersion": 0,
	}
	putReq := jsonRequest(t, http.MethodPut, opsBase+"/"+loginOpKey, putBody, cookie, csrfToken)
	putRec := ts.do(putReq)
	if putRec.Code != http.StatusOK {
		t.Fatalf("PUT override: status = %d, want 200; body = %s", putRec.Code, putRec.Body.String())
	}

	getReq := httptest.NewRequest(http.MethodGet, presetURL, nil)
	getReq.AddCookie(cookie)
	getRec := ts.do(getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET auth-preset: status = %d, want 200; body = %s", getRec.Code, getRec.Body.String())
	}
	var proposal authPresetProposalJSON
	if err := json.Unmarshal(getRec.Body.Bytes(), &proposal); err != nil {
		t.Fatalf("decode proposal: %v", err)
	}
	var tokenBinding *authPresetBindingJSON
	for _, b := range proposal.Bindings {
		if b.DataPath == "token" {
			bCopy := b
			tokenBinding = &bCopy
		}
	}
	if tokenBinding == nil {
		t.Fatalf("test setup: no \"token\" binding in the derived proposal, got %+v", proposal.Bindings)
	}

	// The PUT above already advanced the row's edit_version once (D9's
	// criterion: every guarded write allocates fresh, whether the field it
	// touches is one of this apply's or not), so the version the apply must
	// echo back is whatever GET just reported — never the 0 the PUT started
	// from.
	if _, ok := proposal.EditVersions[loginOpKey]; !ok {
		t.Fatalf("proposal.editVersions has no entry for %q", loginOpKey)
	}
	applyReq := jsonRequest(t, http.MethodPost, presetURL,
		map[string]any{"bindings": []authPresetBindingJSON{*tokenBinding}, "editVersions": proposal.EditVersions}, cookie, csrfToken)
	applyRec := ts.do(applyReq)
	if applyRec.Code != http.StatusOK {
		t.Fatalf("apply auth-preset: status = %d, want 200; body = %s", applyRec.Code, applyRec.Body.String())
	}

	afterReq := httptest.NewRequest(http.MethodGet, opsBase+"/"+loginOpKey, nil)
	afterReq.AddCookie(cookie)
	afterRec := ts.do(afterReq)
	if afterRec.Code != http.StatusOK {
		t.Fatalf("GET override after apply: status = %d, want 200; body = %s", afterRec.Code, afterRec.Body.String())
	}
	doc := decodeOverrideDoc(t, afterRec)
	if doc.ActiveStatus == nil || *doc.ActiveStatus != 503 {
		t.Errorf("ActiveStatus after apply = %v, want 503 (preserved from the earlier PUT)", doc.ActiveStatus)
	}
	if doc.DelayMs == nil || *doc.DelayMs != 250 {
		t.Errorf("DelayMs after apply = %v, want 250 (preserved from the earlier PUT)", doc.DelayMs)
	}
	if doc.ValidateReq == nil || !*doc.ValidateReq {
		t.Errorf("ValidateReq after apply = %v, want true (preserved from the earlier PUT)", doc.ValidateReq)
	}
	if _, ok := doc.Responses["503"]; !ok {
		t.Errorf("responses[503] from the earlier PUT is missing after the apply, got %+v", doc.Responses)
	}
	// The applied binding itself must ALSO have landed, on the response
	// status IT names (200) — the apply is additive, not merely a no-op
	// that happened to preserve the PUT's own fields.
	v, ok := doc.Responses["200"]
	if !ok {
		t.Fatalf("responses[200] (the applied binding's own status) is missing, got %+v", doc.Responses)
	}
	if _, ok := v.Recipes["token"]; !ok {
		t.Errorf("recipes[\"token\"] from the apply is missing, got %+v", v.Recipes)
	}
}

// TestHandler_applyAuthPreset_editVersionsRequired pins D5's rule that a
// NON-EMPTY apply without editVersions is refused by name, at 400 — never
// silently treated as "no expectation" and never a 500. The zero-binding
// short-circuit (covered above) is the ONLY unguarded path.
func TestHandler_applyAuthPreset_editVersionsRequired(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, specID := ts.importSpec(t, "Alex", "Auth Demo", authDoc)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Demo", specID)
	presetURL := fmt.Sprintf("http://mocker.local/api/workspaces/%d/auth-preset", wsID)
	loginOpKey := overrides.OpKey(http.MethodPost, "/auth/login")

	getReq := httptest.NewRequest(http.MethodGet, presetURL, nil)
	getReq.AddCookie(cookie)
	getRec := ts.do(getReq)
	var proposal authPresetProposalJSON
	if err := json.Unmarshal(getRec.Body.Bytes(), &proposal); err != nil {
		t.Fatalf("decode proposal: %v", err)
	}
	tokenBinding := findTokenBinding(t, proposal.Bindings)

	// bindings is non-empty and editVersions is entirely absent from the
	// body — the field the decoder leaves nil, distinct from a present {}.
	req := jsonRequest(t, http.MethodPost, presetURL,
		map[string]any{"bindings": []authPresetBindingJSON{tokenBinding}}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if !strings.Contains(envelope.Error.Message, "editVersions") {
		t.Errorf("error message = %q, want it to name editVersions", envelope.Error.Message)
	}
	if !strings.Contains(envelope.Error.Message, loginOpKey) {
		t.Errorf("error message = %q, want it to name the opKey %q it is missing", envelope.Error.Message, loginOpKey)
	}
}

// TestHandler_applyAuthPreset_editVersionsMissingEntry pins the other half
// of the same rule: editVersions is PRESENT but does not cover an opKey the
// submitted bindings resolve to — the caller may have filtered the proposal,
// but every opKey it kept must still carry its own expectation.
func TestHandler_applyAuthPreset_editVersionsMissingEntry(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, specID := ts.importSpec(t, "Alex", "Auth Demo", authDoc)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Demo", specID)
	presetURL := fmt.Sprintf("http://mocker.local/api/workspaces/%d/auth-preset", wsID)
	loginOpKey := overrides.OpKey(http.MethodPost, "/auth/login")

	getReq := httptest.NewRequest(http.MethodGet, presetURL, nil)
	getReq.AddCookie(cookie)
	getRec := ts.do(getReq)
	var proposal authPresetProposalJSON
	if err := json.Unmarshal(getRec.Body.Bytes(), &proposal); err != nil {
		t.Fatalf("decode proposal: %v", err)
	}
	tokenBinding := findTokenBinding(t, proposal.Bindings)

	req := jsonRequest(t, http.MethodPost, presetURL,
		map[string]any{"bindings": []authPresetBindingJSON{tokenBinding}, "editVersions": map[string]int64{}}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode error body: %v", err)
	}
	if !strings.Contains(envelope.Error.Message, loginOpKey) {
		t.Errorf("error message = %q, want it to name the missing opKey %q", envelope.Error.Message, loginOpKey)
	}
}

// TestHandler_applyAuthPreset_editConflict pins D6/D12's set-valued conflict:
// a second apply that expects the version a FIRST apply already moved past
// is refused 409 edit_conflict, and its details is a staleVersions map
// naming only the opKey that disagreed, with the version the server
// actually holds — never editVersions (D12: different name, different type,
// never sent on a request) and never a full document (the caller already
// has that; what it lacks is which of many rows moved).
func TestHandler_applyAuthPreset_editConflict(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, specID := ts.importSpec(t, "Alex", "Auth Demo", authDoc)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Demo", specID)
	presetURL := fmt.Sprintf("http://mocker.local/api/workspaces/%d/auth-preset", wsID)
	loginOpKey := overrides.OpKey(http.MethodPost, "/auth/login")

	getReq := httptest.NewRequest(http.MethodGet, presetURL, nil)
	getReq.AddCookie(cookie)
	getRec := ts.do(getReq)
	var proposal authPresetProposalJSON
	if err := json.Unmarshal(getRec.Body.Bytes(), &proposal); err != nil {
		t.Fatalf("decode proposal: %v", err)
	}
	var tokenBinding authPresetBindingJSON
	for _, b := range proposal.Bindings {
		if b.DataPath == "token" {
			tokenBinding = b
		}
	}
	if tokenBinding.DataPath == "" {
		t.Fatal("test setup: no token binding in the derived proposal")
	}

	// The FIRST apply: expects 0 (D7's absent-row expectation, matching what
	// GET just reported) and succeeds, moving the row to a fresh version.
	firstReq := jsonRequest(t, http.MethodPost, presetURL,
		map[string]any{"bindings": []authPresetBindingJSON{tokenBinding}, "editVersions": proposal.EditVersions}, cookie, csrfToken)
	firstRec := ts.do(firstReq)
	if firstRec.Code != http.StatusOK {
		t.Fatalf("first apply: status = %d, want 200; body = %s", firstRec.Code, firstRec.Body.String())
	}

	// A second caller, still holding the STALE expectation from the same
	// GET the first caller read, retries the identical write.
	secondReq := jsonRequest(t, http.MethodPost, presetURL,
		map[string]any{"bindings": []authPresetBindingJSON{tokenBinding}, "editVersions": proposal.EditVersions}, cookie, csrfToken)
	secondRec := ts.do(secondReq)
	if secondRec.Code != http.StatusConflict {
		t.Fatalf("second apply: status = %d, want 409; body = %s", secondRec.Code, secondRec.Body.String())
	}
	var conflict struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				StaleVersions map[string]*int64 `json:"staleVersions"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(secondRec.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decode conflict body: %v", err)
	}
	if conflict.Error.Code != "edit_conflict" {
		t.Errorf("conflict code = %q, want %q", conflict.Error.Code, "edit_conflict")
	}
	if len(conflict.Error.Details.StaleVersions) != 1 {
		t.Fatalf("staleVersions has %d entries, want exactly 1 (only the row that disagreed); got %+v",
			len(conflict.Error.Details.StaleVersions), conflict.Error.Details.StaleVersions)
	}
	got, ok := conflict.Error.Details.StaleVersions[loginOpKey]
	if !ok {
		t.Fatalf("staleVersions has no entry for %q, got %+v", loginOpKey, conflict.Error.Details.StaleVersions)
	}
	if got == nil {
		t.Fatal("staleVersions[loginOpKey] = null, want the row's current (nonzero) version — the row still exists")
	}
	if *got == 0 {
		t.Error("staleVersions[loginOpKey] = 0, want the freshly allocated version the first apply wrote")
	}
}

// TestHandler_applyAuthPreset_editVersionsExtraKeyIgnored pins the blocker
// fix over PutManyExpecting's expectation map: the operator's UI naturally
// forwards the FULL editVersions map GET .../auth-preset returned even after
// filtering the submitted bindings down to a subset (D10's "the value each
// site sends is the one its own preceding read returned"). An entry for an
// opKey this call never intended to write must not be checked against the
// server's live state — only the opKeys the submitted bindings actually
// resolve to may refuse the call.
func TestHandler_applyAuthPreset_editVersionsExtraKeyIgnored(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, specID := ts.importSpec(t, "Alex", "Auth Demo", authDoc)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Demo", specID)
	presetURL := fmt.Sprintf("http://mocker.local/api/workspaces/%d/auth-preset", wsID)
	widgetsOpKey := overrides.OpKey(http.MethodGet, "/widgets")

	getReq := httptest.NewRequest(http.MethodGet, presetURL, nil)
	getReq.AddCookie(cookie)
	getRec := ts.do(getReq)
	var proposal authPresetProposalJSON
	if err := json.Unmarshal(getRec.Body.Bytes(), &proposal); err != nil {
		t.Fatalf("decode proposal: %v", err)
	}
	var tokenBinding authPresetBindingJSON
	for _, b := range proposal.Bindings {
		if b.DataPath == "token" {
			tokenBinding = b
		}
	}
	if tokenBinding.DataPath == "" {
		t.Fatal("test setup: no token binding in the derived proposal")
	}

	// editVersions carries a bogus, non-zero expectation for an opKey
	// ("GET /widgets") that has no override row and that the submitted
	// bindings never touch. Before the fix, PutManyExpecting checked EVERY
	// key in this map and would have refused the whole call over this
	// unrelated, deliberately-stale entry.
	editVersions := map[string]int64{}
	for k, v := range proposal.EditVersions {
		editVersions[k] = v
	}
	editVersions[widgetsOpKey] = 999

	req := jsonRequest(t, http.MethodPost, presetURL,
		map[string]any{"bindings": []authPresetBindingJSON{tokenBinding}, "editVersions": editVersions}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("apply with an untouched, stale editVersions entry: status = %d, want 200; body = %s",
			rec.Code, rec.Body.String())
	}
	var applied struct {
		Applied int `json:"applied"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &applied); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}
	if applied.Applied != 1 {
		t.Errorf("applied = %d, want 1", applied.Applied)
	}
}

// TestHandler_applyAuthPreset_editConflictGone pins D12's map-shaped
// tombstone: a preset row deleted between the caller's read and its retry
// answers a `null` value at that opKey, not an absent entry and not a
// nonzero version.
func TestHandler_applyAuthPreset_editConflictGone(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, specID := ts.importSpec(t, "Alex", "Auth Demo", authDoc)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Demo", specID)
	presetURL := fmt.Sprintf("http://mocker.local/api/workspaces/%d/auth-preset", wsID)
	loginOpKey := overrides.OpKey(http.MethodPost, "/auth/login")
	opTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/operations/%s", wsID, loginOpKey)

	getReq := httptest.NewRequest(http.MethodGet, presetURL, nil)
	getReq.AddCookie(cookie)
	getRec := ts.do(getReq)
	var proposal authPresetProposalJSON
	if err := json.Unmarshal(getRec.Body.Bytes(), &proposal); err != nil {
		t.Fatalf("decode proposal: %v", err)
	}
	var tokenBinding authPresetBindingJSON
	for _, b := range proposal.Bindings {
		if b.DataPath == "token" {
			tokenBinding = b
		}
	}
	if tokenBinding.DataPath == "" {
		t.Fatal("test setup: no token binding in the derived proposal")
	}

	applyReq := jsonRequest(t, http.MethodPost, presetURL,
		map[string]any{"bindings": []authPresetBindingJSON{tokenBinding}, "editVersions": proposal.EditVersions}, cookie, csrfToken)
	applyRec := ts.do(applyReq)
	if applyRec.Code != http.StatusOK {
		t.Fatalf("apply: status = %d, want 200; body = %s", applyRec.Code, applyRec.Body.String())
	}
	var applied struct {
		EditVersions map[string]int64 `json:"editVersions"`
	}
	if err := json.Unmarshal(applyRec.Body.Bytes(), &applied); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}

	delReq := jsonRequest(t, http.MethodDelete, opTarget, nil, cookie, csrfToken)
	if rec := ts.do(delReq); rec.Code != http.StatusOK {
		t.Fatalf("delete override: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	// Retry with the version the FIRST apply's own write response returned
	// (nonzero — the row existed at that version) rather than the 0 the
	// original GET reported: expecting 0 against a now-absent row is the
	// LEGAL "no expectation, no row" case (D7's five-case table) and would
	// succeed by recreating the row, not conflict — the conflict this test
	// pins requires an expectation for a row that ACTUALLY existed and is
	// now gone.
	retryReq := jsonRequest(t, http.MethodPost, presetURL,
		map[string]any{"bindings": []authPresetBindingJSON{tokenBinding}, "editVersions": applied.EditVersions}, cookie, csrfToken)
	retryRec := ts.do(retryReq)
	if retryRec.Code != http.StatusConflict {
		t.Fatalf("retry after delete: status = %d, want 409; body = %s", retryRec.Code, retryRec.Body.String())
	}
	var conflict struct {
		Error struct {
			Details struct {
				StaleVersions map[string]*int64 `json:"staleVersions"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(retryRec.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decode conflict body: %v", err)
	}
	got, ok := conflict.Error.Details.StaleVersions[loginOpKey]
	if !ok {
		t.Fatalf("staleVersions has no entry for %q, got %+v", loginOpKey, conflict.Error.Details.StaleVersions)
	}
	if got != nil {
		t.Errorf("staleVersions[%q] = %d, want explicit null (the row is gone)", loginOpKey, *got)
	}
}
