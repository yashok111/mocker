package server_test

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/authpreset"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/server"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/workspaces"
)

// p1cInlineSpec is a tiny, self-contained OAS 3.0 document — deliberately
// NOT the acceptance corpus, which was a gitignored file absent from a fresh
// clone when this test was written (HARD RULE 4's test-side counterpart: this
// file had to run without it; it is embedded now, and the small fixture is
// still the clearer one). Its one operation sits on a
// DESIGN §10 trigger path ("/auth/login" — segments "auth" and "login" both
// match authTriggerSegments) and its 200 response is exactly JWTInfo-shaped:
// two token fields and a "*_expires_at" field, both DESIGN §10 alias tables
// this phase implements.
const p1cInlineSpec = `{
  "openapi": "3.0.3",
  "info": { "title": "P1c smoke spec", "version": "1.0.0" },
  "paths": {
    "/auth/login": {
      "post": {
        "operationId": "login",
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "token": { "type": "string", "description": "JWT" },
                    "refresh_token": { "type": "string", "description": "JWT" },
                    "token_expires_at": { "type": "integer" }
                  },
                  "required": ["token", "refresh_token", "token_expires_at"]
                }
              }
            }
          }
        }
      }
    }
  }
}`

// buildP1cStack is [buildStackWithSpecs] (p1a_test.go) with ONE addition:
// the mock plane's OverrideSource is wired via [mockplane.Plane.SetOverrides]
// before anything is served — the same call cmd/mocker/main.go itself makes
// at startup, right after mockplane.New (see that line's own comment there).
// Without it, a Plane keeps the nil OverrideSource [mockplane.New] gives it,
// and every op_overrides row this test's auth-preset apply writes would
// silently miss at serve time. buildStackWithSpecs itself cannot be reused
// unchanged for that reason (it predates op_overrides — P1a) and cannot be
// edited (it belongs to that earlier phase), so this slice needs its own
// copy that adds exactly this one call.
func buildP1cStack(t *testing.T) (http.Handler, *config.Config) {
	t.Helper()
	cfg := testConfig(t)

	db, err := store.Open(t.Context(), cfg.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	provider := auth.NewSharedPassword(cfg)
	sessions := auth.NewManager(db, cfg, provider)
	ws := workspaces.NewRepo(db)
	specsRepo := specs.NewRepo(db, cfg)
	log := testLogger()

	adminSrv := admin.New(cfg, sessions, ws, db, log)
	mockPlane := mockplane.New(cfg, ws, specsRepo, log)
	mockPlane.SetOverrides(overrides.NewRepo(db))
	dispatcher := server.New(cfg, adminSrv.Handler(), mockPlane, log)
	handler := httpx.Chain(dispatcher, httpx.Recover(log), httpx.RequestLog(log), httpx.MaxBody(cfg.MaxBody))
	return handler, cfg
}

// p1cWorkspaceView is the sliver of [admin's workspaceView] JSON this file
// actually reads: the identity id the minted token's "sub" must equal
// (DESIGN §10's one-identity rule) and the jwt ttl the expiry checks below
// are computed against. Identity.ID decodes as float64 — encoding/json's
// ordinary behavior for a JSON number landing in an `any`-typed Go field
// (domain.Identity.ID is untyped precisely because real specs use both
// integers and UUIDs) — which is also exactly what a JWT payload's own
// "sub" claim decodes to below, so the two compare equal without any
// int/float coercion of this test's own invention.
type p1cWorkspaceView struct {
	ID       int64  `json:"id"`
	Slug     string `json:"slug"`
	Settings struct {
		Identity struct {
			ID float64 `json:"id"`
		} `json:"identity"`
		Auth struct {
			JWTTTLSec int `json:"jwtTtlSec"`
		} `json:"auth"`
	} `json:"settings"`
}

// p1cLoginResponse is p1cInlineSpec's one operation's 200 body, decoded.
// TokenExpiresAt is int64, not json.Number: DESIGN §10's ten-vs-thirteen-
// digit failure is checked on the JWT's own "exp" claim (p1cAssertToken,
// which decodes with json.Number precision) and independently on this
// field via strconv.FormatInt below, so an ordinary int64 here is enough.
type p1cLoginResponse struct {
	Token          string `json:"token"`
	RefreshToken   string `json:"refresh_token"`
	TokenExpiresAt int64  `json:"token_expires_at"`
}

// p1cRequestLogin hits the mock plane's one generated route and decodes its
// body. mockHost is "<slug>.mock.local" — MOCKER_ROUTING=host, exactly like
// TestP1a_ImportAttachRoute's own mock-plane requests.
func p1cRequestLogin(t *testing.T, handler http.Handler, mockHost string) p1cLoginResponse {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "http://"+mockHost+"/auth/login", nil)
	rec := do(handler, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("POST /auth/login: status = %d, want 200; body = %s", rec.Code, rec.Body)
	}
	var resp p1cLoginResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode login response: %v; body = %s", err, rec.Body)
	}
	return resp
}

// p1cDecodeJWT splits token into its three dot-separated segments and
// base64url-decodes the first two. payload is decoded with json.Number
// precision (UseNumber) so p1cAssertToken can count "exp"'s digits on its
// EXACT wire representation rather than on a float64 that has already lost
// or gained trailing zeros.
func p1cDecodeJWT(t *testing.T, token string) (header map[string]any, payload map[string]any) {
	t.Helper()
	segs := strings.Split(token, ".")
	if len(segs) != 3 {
		t.Fatalf("token %q has %d dot-separated segments, want exactly 3 (a compact JWS)", token, len(segs))
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(segs[0])
	if err != nil {
		t.Fatalf("token header: base64url decode: %v; segment = %q", err, segs[0])
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		t.Fatalf("token header: not JSON: %v; raw = %s", err, headerJSON)
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(segs[1])
	if err != nil {
		t.Fatalf("token payload: base64url decode: %v; segment = %q", err, segs[1])
	}
	dec := json.NewDecoder(strings.NewReader(string(payloadJSON)))
	dec.UseNumber()
	if err := dec.Decode(&payload); err != nil {
		t.Fatalf("token payload: not JSON: %v; raw = %s", err, payloadJSON)
	}
	return header, payload
}

// p1cAssertToken is DESIGN §10's three checks on one minted token, run
// against a real decoded JWS rather than the string alone: the header
// carries a non-empty alg, the payload's sub matches wantSub (the one-
// identity rule — a mismatch here is exactly what loops a frontend's login
// screen forever), and exp is a ten-digit epoch-SECONDS value strictly
// after nowUnix. It returns exp so the caller can additionally compare it
// against a sibling token (refresh vs access) or a previous request's own
// exp, without decoding twice.
func p1cAssertToken(t *testing.T, label, token string, wantSub float64, nowUnix int64) (exp int64) {
	t.Helper()
	header, payload := p1cDecodeJWT(t, token)

	if alg, _ := header["alg"].(string); alg == "" {
		t.Errorf("%s: header alg is empty or missing: %v", label, header)
	}

	subNum, ok := payload["sub"].(json.Number)
	if !ok {
		t.Fatalf("%s: payload sub missing or not a number: %v", label, payload)
	}
	if sub, err := subNum.Float64(); err != nil {
		t.Fatalf("%s: payload sub %q: %v", label, subNum, err)
	} else if sub != wantSub {
		t.Errorf("%s: sub = %v, want the workspace identity's own id %v — DESIGN §10: the token and the profile endpoint must agree on ONE identity, or a frontend that compares them loops on the login screen forever",
			label, sub, wantSub)
	}

	expNum, ok := payload["exp"].(json.Number)
	if !ok {
		t.Fatalf("%s: payload exp missing or not a number: %v", label, payload)
	}
	// Digit count is asserted on the exact wire string, not on a value this
	// test recomputed — the DESIGN §10 failure this guards is a MILLISECOND
	// value (13 digits) masquerading as a fine-looking number; a check that
	// went through a second int64/float64 round trip could no longer tell
	// the two apart if some future change quietly reformatted only the
	// value and not this test's own arithmetic.
	if digits := len(expNum.String()); digits != 10 {
		t.Errorf("%s: exp has %d digits (%s), want exactly 10 — epoch SECONDS, not milliseconds; a 13-digit value overflows a 32-bit setTimeout and fires an immediate, infinite refresh loop (DESIGN §10)",
			label, digits, expNum.String())
	}
	exp, err := expNum.Int64()
	if err != nil {
		t.Fatalf("%s: payload exp %q: %v", label, expNum, err)
	}
	if d := exp - nowUnix; d <= 0 {
		t.Errorf("%s: exp - now = %d, want > 0 (a token minted just now must not already be expired)", label, d)
	}
	return exp
}

// TestP1c_FrontendLogsIn is DESIGN §19 line 1111's phase criterion,
// "фронт логинится", proved end to end through the real dispatcher: log in
// as an operator, create a workspace, import p1cInlineSpec, attach it,
// preview the derived auth preset, apply it UNCHANGED, then hit the mock
// plane as the frontend would — twice, a second apart, so the expiry
// fields are shown to move with the real clock rather than freeze into the
// seed (DESIGN §9 "Время").
func TestP1c_FrontendLogsIn(t *testing.T) {
	t.Parallel()
	handler, _ := buildP1cStack(t)

	cookie, csrf := login(t, handler, "p1c-admin")

	// 1. Create a workspace with no spec attached yet — DEFAULT settings,
	// which is what fixes the identity every token below is checked
	// against.
	createWSReq := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
		map[string]string{"name": "p1c-login-demo"}, cookie, csrf)
	createWSRec := do(handler, createWSReq)
	if createWSRec.Code != http.StatusCreated {
		t.Fatalf("create workspace: status = %d, want 201; body = %s", createWSRec.Code, createWSRec.Body)
	}
	var ws p1cWorkspaceView
	if err := json.Unmarshal(createWSRec.Body.Bytes(), &ws); err != nil {
		t.Fatalf("decode create workspace response: %v", err)
	}
	wantSub := ws.Settings.Identity.ID
	t.Logf("workspace %d (%s): identity id = %v, jwt ttl = %ds", ws.ID, ws.Slug, wantSub, ws.Settings.Auth.JWTTTLSec)

	// 2. Import the inline document as a new spec.
	importReq := jsonRequest(t, http.MethodPost, "http://mocker.local/api/specs",
		map[string]string{"name": "P1c smoke", "source": "upload", "document": p1cInlineSpec}, cookie, csrf)
	importRec := do(handler, importReq)
	if importRec.Code != http.StatusCreated {
		t.Fatalf("import spec: status = %d, want 201; body = %s", importRec.Code, importRec.Body)
	}
	var imported struct {
		ID     int64 `json:"id"`
		Report struct {
			Operations int `json:"operations"`
			Degraded   int `json:"degraded"`
		} `json:"report"`
	}
	if err := json.Unmarshal(importRec.Body.Bytes(), &imported); err != nil {
		t.Fatalf("decode import spec response: %v", err)
	}
	if imported.Report.Operations != 1 || imported.Report.Degraded != 0 {
		t.Fatalf("import report = %+v, want exactly 1 clean operation", imported.Report)
	}

	// 3. Attach the imported spec to the workspace. editVersion: 1 -- A3/D10
	// requires it, and this workspace was just created, never PATCHed
	// before (workspaces.Repo.Create bootstraps edit_version at 1, not the
	// column's DEFAULT of 0).
	attachReq := jsonRequest(t, http.MethodPatch, "http://mocker.local/api/workspaces/"+strconv.FormatInt(ws.ID, 10),
		map[string]any{"specId": imported.ID, "editVersion": 1}, cookie, csrf)
	attachRec := do(handler, attachReq)
	if attachRec.Code != http.StatusOK {
		t.Fatalf("attach spec: status = %d, want 200; body = %s", attachRec.Code, attachRec.Body)
	}

	presetPath := "http://mocker.local/api/workspaces/" + strconv.FormatInt(ws.ID, 10) + "/auth-preset"

	// 4. GET the proposed auth preset — a preview that writes nothing.
	getPresetRec := do(handler, jsonRequest(t, http.MethodGet, presetPath, nil, cookie, csrf))
	if getPresetRec.Code != http.StatusOK {
		t.Fatalf("get auth preset: status = %d, want 200; body = %s", getPresetRec.Code, getPresetRec.Body)
	}
	// A3/D5: GET .../auth-preset now carries editVersions beside the
	// proposal — one entry per opKey the bindings resolve to, required back
	// on the apply POST below.
	var proposalWithVersions struct {
		authpreset.Proposal
		EditVersions map[string]int64 `json:"editVersions"`
	}
	if err := json.Unmarshal(getPresetRec.Body.Bytes(), &proposalWithVersions); err != nil {
		t.Fatalf("decode auth preset proposal: %v", err)
	}
	proposal := proposalWithVersions.Proposal
	if len(proposal.AuthPaths) != 1 || proposal.AuthPaths[0] != "/auth/login" {
		t.Fatalf("AuthPaths = %v, want exactly [/auth/login]", proposal.AuthPaths)
	}
	if len(proposal.Bindings) != 3 {
		t.Fatalf("proposal.Bindings = %d, want exactly 3 (token, refresh_token, token_expires_at); got %+v", len(proposal.Bindings), proposal.Bindings)
	}
	t.Logf("MEASUREMENTS proposal: %d bindings on %v, sample jwt = %s", len(proposal.Bindings), proposal.AuthPaths, proposal.SampleJWT)

	// 5. POST the proposal back UNMODIFIED — the operator approving exactly
	// what was previewed, never a re-derivation (DESIGN §10's own
	// requirement that an edited/approved list must survive intact).
	// A3/D5: the required expectation is the SAME map the GET above just
	// returned — echoed back unmodified, exactly like a real caller would.
	applyRec := do(handler, jsonRequest(t, http.MethodPost, presetPath,
		map[string]any{"bindings": proposal.Bindings, "editVersions": proposalWithVersions.EditVersions}, cookie, csrf))
	if applyRec.Code != http.StatusOK {
		t.Fatalf("apply auth preset: status = %d, want 200; body = %s", applyRec.Code, applyRec.Body)
	}
	var applied struct {
		Applied  int   `json:"applied"`
		Revision int64 `json:"revision"`
	}
	if err := json.Unmarshal(applyRec.Body.Bytes(), &applied); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}
	if applied.Applied != 3 {
		t.Fatalf("applied = %d, want 3", applied.Applied)
	}
	t.Logf("MEASUREMENTS apply: %d bindings applied, workspace revision now %d", applied.Applied, applied.Revision)

	// 6. Hit the mock plane, twice, more than a second apart.
	mockHost := ws.Slug + ".mock.local"
	first := p1cRequestLogin(t, handler, mockHost)
	time.Sleep(1200 * time.Millisecond)
	second := p1cRequestLogin(t, handler, mockHost)

	ttl := int64(ws.Settings.Auth.JWTTTLSec)
	if ttl <= 0 {
		ttl = 3600
	}

	for i, resp := range []p1cLoginResponse{first, second} {
		label := fmt.Sprintf("request %d", i+1)
		now := time.Now().Unix()

		accessExp := p1cAssertToken(t, label+" token", resp.Token, wantSub, now)
		if d := accessExp - now; d > ttl+60 {
			t.Errorf("%s: token exp - now = %d, want <= %d (workspace jwt ttl + slack)", label, d, ttl+60)
		}

		refreshExp := p1cAssertToken(t, label+" refresh_token", resp.RefreshToken, wantSub, now)
		if refreshExp <= accessExp {
			t.Errorf("%s: refresh_token exp (%d) <= token exp (%d), want the refresh token to outlive the access token it refreshes",
				label, refreshExp, accessExp)
		}

		// The response body's own token_expires_at field — the "now" recipe,
		// independent of the jwt recipe above, but must tell the same story:
		// seconds, not milliseconds, and genuinely in the future.
		if digits := len(strconv.FormatInt(resp.TokenExpiresAt, 10)); digits != 10 {
			t.Errorf("%s: token_expires_at has %d digits (%d), want exactly 10 — epoch SECONDS, not milliseconds",
				label, digits, resp.TokenExpiresAt)
		}
		if d := resp.TokenExpiresAt - now; d <= 0 || d > ttl+60 {
			t.Errorf("%s: token_expires_at - now = %d, want in (0, %d]", label, d, ttl+60)
		}
	}

	// The clock moved between the two requests: a second request's expiry
	// fields must not be frozen copies of the first's (DESIGN §9 "Время" —
	// jwt/now recipes derive from the REAL clock, never the seed).
	if second.TokenExpiresAt <= first.TokenExpiresAt {
		t.Errorf("second token_expires_at (%d) <= first (%d) after a >1s gap — the clock must move between requests, never freeze into the seed",
			second.TokenExpiresAt, first.TokenExpiresAt)
	}

	// 7. An unrelated path still 404s — attaching a preset must not turn
	// every unmatched path into something else (mirrors TestP1a_
	// ImportAttachRoute's own step 5).
	t.Run("unknown path still 404s", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://"+mockHost+"/does-not-exist", nil)
		rec := do(handler, req)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body)
		}
	})
}
