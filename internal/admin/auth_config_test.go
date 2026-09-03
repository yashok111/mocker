package admin_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"

	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testauth"
	"github.com/yashok111/mocker/internal/workspaces"
)

// authConfigBody decodes just the piece of authResponse this file cares
// about: the new config object, plus enough of the rest to confirm the two
// pre-existing fields are still there under their original names.
type authConfigBody struct {
	User struct {
		Name string `json:"name"`
	} `json:"user"`
	CSRFToken string `json:"csrfToken"`
	Config    struct {
		ReservedPrefix string `json:"reservedPrefix"`
		BaseDomain     string `json:"baseDomain"`
		Routing        string `json:"routing"`
	} `json:"config"`
}

// customConfigServer mirrors newTestServer (admin_test.go) but takes cfg as
// a parameter instead of building testConfig()'s fixed one. Every test below
// exists specifically to prove the config object in the auth response
// carries whatever cfg ACTUALLY holds — a handler that hard-coded the exact
// literals testConfig() happens to use would still pass a test built only
// against those literals, which is exactly the "before and after look
// identical" trap the project's own review gate has been bitten by before.
func customConfigServer(t *testing.T, cfg *config.Config) *testServer {
	t.Helper()
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
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	return &testServer{handler: admin.New(cfg, sessions, ws, db, log).Handler(), db: db, cfg: cfg}
}

// customCfg builds a config differing from testConfig()'s defaults in
// exactly the three fields the config object exposes, while keeping
// AdminHost at "mocker.local" — adminOrigin and jsonRequest (admin_test.go)
// are both hard-wired to that host for the Origin/CSRF check, and this file
// reuses them rather than inventing a third way to build a request.
func customCfg(t *testing.T, reservedPrefix, baseDomain string, routing config.Routing) *config.Config {
	t.Helper()
	// A pre-minted hash (internal/testauth): under -race auth.HashPassword
	// is ~110 ms per fixture, and this file builds one per test.
	hash := testauth.Hash
	return &config.Config{
		BaseDomain:         baseDomain,
		AdminHost:          "mocker.local",
		Routing:            routing,
		ReservedPrefix:     reservedPrefix,
		AuthMode:           config.AuthShared,
		SharedPasswordHash: hash,
		DataDir:            t.TempDir(),
		MaxBody:            10 << 20,
		MaxResponse:        4 << 20,
		RuntimeCache:       32,
		Dev:                true,
	}
}

func TestHandler_login_ConfigMatchesActualCfg(t *testing.T) {
	t.Parallel()
	cfg := customCfg(t, "/__custom-admin", "", config.RoutingPath)
	ts := customConfigServer(t, cfg)

	req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/auth/login",
		map[string]string{"name": "Alex", "password": testPassword}, nil, "")
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var body authConfigBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if body.User.Name != "Alex" {
		t.Errorf("user.name = %q, want %q (existing field must survive the new one)", body.User.Name, "Alex")
	}
	if body.CSRFToken == "" {
		t.Error("csrfToken is empty (existing field must survive the new one)")
	}
	if body.Config.ReservedPrefix != "/__custom-admin" {
		t.Errorf("config.reservedPrefix = %q, want %q", body.Config.ReservedPrefix, "/__custom-admin")
	}
	if body.Config.BaseDomain != "" {
		t.Errorf("config.baseDomain = %q, want empty (this cfg runs path routing)", body.Config.BaseDomain)
	}
	if body.Config.Routing != "path" {
		t.Errorf("config.routing = %q, want %q", body.Config.Routing, "path")
	}
}

func TestHandler_me_ConfigMatchesActualCfg(t *testing.T) {
	t.Parallel()
	// Deliberately the OTHER shape from the login test above (host routing,
	// a real base domain): if either handler special-cased one config shape,
	// running both tests against opposite shapes is what would expose it.
	cfg := customCfg(t, "/__other-prefix", "mocks.example.org", config.RoutingHost)
	ts := customConfigServer(t, cfg)
	cookie, _ := ts.login(t, "Alex")

	req := jsonRequest(t, http.MethodGet, "http://mocker.local/api/me", nil, cookie, "")
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/me: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	var body authConfigBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode /api/me response: %v", err)
	}
	if body.Config.ReservedPrefix != "/__other-prefix" {
		t.Errorf("config.reservedPrefix = %q, want %q", body.Config.ReservedPrefix, "/__other-prefix")
	}
	if body.Config.BaseDomain != "mocks.example.org" {
		t.Errorf("config.baseDomain = %q, want %q", body.Config.BaseDomain, "mocks.example.org")
	}
	if body.Config.Routing != "host" {
		t.Errorf("config.routing = %q, want %q", body.Config.Routing, "host")
	}
}

// TestHandler_login_and_me_ReturnIdenticalConfig pins the ONE constructor
// helper requirement directly: two independently hand-written literals are
// exactly how the login and /api/me answers would drift the day only one of
// them gains a field, so this compares the actual JSON both handlers send
// for the SAME server rather than trusting that two green single-handler
// tests imply agreement.
func TestHandler_login_and_me_ReturnIdenticalConfig(t *testing.T) {
	t.Parallel()
	cfg := customCfg(t, "/__shared-prefix", "shared.example.net", config.RoutingHost)
	ts := customConfigServer(t, cfg)

	loginReq := jsonRequest(t, http.MethodPost, "http://mocker.local/api/auth/login",
		map[string]string{"name": "Alex", "password": testPassword}, nil, "")
	loginRec := ts.do(loginReq)
	if loginRec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, want 200; body = %s", loginRec.Code, loginRec.Body.String())
	}
	var loginBody authConfigBody
	if err := json.Unmarshal(loginRec.Body.Bytes(), &loginBody); err != nil {
		t.Fatalf("decode login response: %v", err)
	}

	cookie := loginRec.Result().Cookies()[0]
	meReq := jsonRequest(t, http.MethodGet, "http://mocker.local/api/me", nil, cookie, "")
	meRec := ts.do(meReq)
	if meRec.Code != http.StatusOK {
		t.Fatalf("GET /api/me: status = %d, want 200; body = %s", meRec.Code, meRec.Body.String())
	}
	var meBody authConfigBody
	if err := json.Unmarshal(meRec.Body.Bytes(), &meBody); err != nil {
		t.Fatalf("decode /api/me response: %v", err)
	}

	if loginBody.Config != meBody.Config {
		t.Errorf("login config = %+v, /api/me config = %+v, want identical", loginBody.Config, meBody.Config)
	}
}
