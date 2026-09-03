package auth_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testauth"
)

// testPassword is the shared password every test manager is configured with.
const testPassword = testauth.Password

// newTestDB opens a fresh, migrated SQLite file under t.TempDir() and closes
// it on cleanup.
func newTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.Context(), t.TempDir()+"/mocker.db")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// newTestManager builds a Manager over a fresh DB, wired to a SharedPassword
// provider configured with testPassword. See the config literal comment in
// the task brief: every one of these fields matters — MaxBody:0 would make
// every request 413, ReservedPrefix:"" would make health unmatchable, and
// Dev:false would set a Secure cookie a plain-http test client drops.
func newTestManager(t *testing.T) (*auth.Manager, *store.DB) {
	t.Helper()
	// A pre-minted hash (internal/testauth): under -race auth.HashPassword
	// is ~110 ms per fixture, and this file builds one per test.
	hash := testauth.Hash
	cfg := &config.Config{
		BaseDomain:         "mock.local",
		AdminHost:          "mocker.local",
		Routing:            config.RoutingHost,
		ReservedPrefix:     "/__mocker",
		AuthMode:           config.AuthShared,
		SharedPasswordHash: hash,
		DataDir:            t.TempDir(),
		MaxBody:            10 << 20,
		MaxResponse:        4 << 20,
		RuntimeCache:       32,
		Dev:                true,
	}
	db := newTestDB(t)
	provider := auth.NewSharedPassword(cfg)
	return auth.NewManager(db, cfg, provider), db
}

// loginRequest builds the *http.Request [auth.SharedPassword.Login] expects:
// a JSON body of {name, password}.
func loginRequest(t *testing.T, name, password string) *http.Request {
	t.Helper()
	body, err := json.Marshal(map[string]string{"name": name, "password": password})
	if err != nil {
		t.Fatalf("marshal login body: %v", err)
	}
	r := httptest.NewRequest(http.MethodPost, "http://mocker.local/api/auth/login", bytes.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	return r
}

func TestHashPassword_VerifyPassword(t *testing.T) {
	tests := []struct {
		name  string
		plain string
	}{
		{"ascii", "correct horse battery staple"},
		{"cyrillic", "пароль-с-кириллицей"},
		{"empty", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			encoded, err := auth.HashPassword(tt.plain)
			if err != nil {
				t.Fatalf("HashPassword(): %v", err)
			}
			if !strings.HasPrefix(encoded, "$argon2id$") {
				t.Fatalf("HashPassword() = %q, want $argon2id$ prefix", encoded)
			}

			ok, err := auth.VerifyPassword(encoded, tt.plain)
			if err != nil {
				t.Fatalf("VerifyPassword() round trip: unexpected error: %v", err)
			}
			if !ok {
				t.Error("VerifyPassword() round trip = false, want true for the plaintext it was hashed from")
			}
		})
	}
}

func TestVerifyPassword_wrongPassword(t *testing.T) {
	t.Parallel()
	encoded, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword(): %v", err)
	}

	ok, err := auth.VerifyPassword(encoded, testPassword+"x")
	if err != nil {
		t.Fatalf("VerifyPassword() wrong password: unexpected error: %v", err)
	}
	if ok {
		t.Error("VerifyPassword() wrong password = true, want false")
	}
}

func TestVerifyPassword_tamperedHash(t *testing.T) {
	encoded, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword(): %v", err)
	}

	tests := []struct {
		name    string
		encoded string
	}{
		{"empty string", ""},
		{"not a hash at all", "not-a-hash-at-all"},
		{"wrong algorithm tag", strings.Replace(encoded, "argon2id", "argon2i", 1)},
		{"invalid base64 in digest", encoded[:len(encoded)-1] + "!"},
		{"missing fields", strings.Join(strings.Split(encoded, "$")[:4], "$")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ok, err := auth.VerifyPassword(tt.encoded, testPassword)
			if err == nil {
				t.Fatalf("VerifyPassword(%q) = (%v, <nil>), want a non-nil error", tt.encoded, ok)
			}
			if ok {
				t.Error("VerifyPassword() on a tampered hash reported a match")
			}
		})
	}
}

func TestManager_Login_getOrCreate(t *testing.T) {
	t.Parallel()
	mgr, _ := newTestManager(t)

	sess1, user1, err := mgr.Login(t.Context(), loginRequest(t, "  Alex  ", testPassword))
	if err != nil {
		t.Fatalf("first Login(): %v", err)
	}
	if user1.Name != "Alex" {
		t.Errorf("first login Name = %q, want trimmed %q", user1.Name, "Alex")
	}
	if user1.ID == 0 {
		t.Error("first login User.ID is zero, want an assigned id")
	}

	// Same name, no leading/trailing space this time: must resolve to the
	// SAME user id, never mint a second "Alex" that orphans the first one's
	// workspaces.
	sess2, user2, err := mgr.Login(t.Context(), loginRequest(t, "Alex", testPassword))
	if err != nil {
		t.Fatalf("second Login(): %v", err)
	}
	if user2.ID != user1.ID {
		t.Fatalf("second Login() user id = %d, want the same id %d as the first login", user2.ID, user1.ID)
	}

	if sess1.ID == sess2.ID {
		t.Error("both logins minted the same session id")
	}
	if sess1.CSRFToken == sess2.CSRFToken {
		t.Error("both logins minted the same CSRF token")
	}
	if sess1.CSRFToken == sess1.ID {
		t.Error("session id and CSRF token are equal, want independently generated values")
	}
}

// TestManager_EnsureUser_idempotent is [auth.Manager.EnsureUser]'s own
// regression of TestManager_Login_getOrCreate above: calling it twice for
// the same name must resolve to the SAME users.id, never mint a second row —
// this is the exact property the MCP endpoint depends on to attribute every
// call across the process's life to one stable identity.
func TestManager_EnsureUser_idempotent(t *testing.T) {
	t.Parallel()
	mgr, _ := newTestManager(t)

	user1, err := mgr.EnsureUser(t.Context(), "mcp", "member")
	if err != nil {
		t.Fatalf("first EnsureUser(): %v", err)
	}
	if user1.ID == 0 {
		t.Error("first EnsureUser() User.ID is zero, want an assigned id")
	}
	if user1.Name != "mcp" || user1.Role != "member" {
		t.Errorf("first EnsureUser() = {Name: %q, Role: %q}, want {mcp, member}", user1.Name, user1.Role)
	}

	user2, err := mgr.EnsureUser(t.Context(), "mcp", "member")
	if err != nil {
		t.Fatalf("second EnsureUser(): %v", err)
	}
	if user2.ID != user1.ID {
		t.Fatalf("second EnsureUser() user id = %d, want the same id %d as the first call", user2.ID, user1.ID)
	}
}

// TestManager_EnsureUser_sharesIdentityWithLogin proves EnsureUser and Login
// upsert into the SAME table under the SAME uniqueness rule: a name Login
// already created is the row EnsureUser finds, not a second one — the two
// entry points (a person logging in, and the MCP endpoint resolving its own
// fixed identity) must never be able to orphan one another's data.
func TestManager_EnsureUser_sharesIdentityWithLogin(t *testing.T) {
	t.Parallel()
	mgr, _ := newTestManager(t)

	_, loginUser, err := mgr.Login(t.Context(), loginRequest(t, "Alex", testPassword))
	if err != nil {
		t.Fatalf("Login(): %v", err)
	}

	ensuredUser, err := mgr.EnsureUser(t.Context(), "Alex", "member")
	if err != nil {
		t.Fatalf("EnsureUser(): %v", err)
	}
	if ensuredUser.ID != loginUser.ID {
		t.Fatalf("EnsureUser() user id = %d, want Login()'s id %d for the same name", ensuredUser.ID, loginUser.ID)
	}
}

func TestManager_Login_wrongPassword(t *testing.T) {
	t.Parallel()
	mgr, db := newTestManager(t)

	_, _, err := mgr.Login(t.Context(), loginRequest(t, "Alex", "not-the-password"))
	if !errors.Is(err, auth.ErrBadPassword) {
		t.Fatalf("Login() with wrong password: error = %v, want ErrBadPassword", err)
	}

	// The password must be verified before the database is ever touched: a
	// failed attempt must not have created a user row for the name it tried.
	var count int
	if err := db.R.QueryRowContext(t.Context(), "SELECT count(*) FROM users WHERE name = ?", "Alex").Scan(&count); err != nil {
		t.Fatalf("count users: %v", err)
	}
	if count != 0 {
		t.Error("Login() with a wrong password created a user row; password must be verified before touching the database")
	}
}

func TestManager_Login_badName(t *testing.T) {
	t.Parallel()
	mgr, _ := newTestManager(t)

	tests := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"whitespace only", "   "},
		{"control character", "al\x00ex"},
		{"too long", strings.Repeat("a", 65)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := mgr.Login(t.Context(), loginRequest(t, tt.in, testPassword))
			if !errors.Is(err, auth.ErrBadName) {
				t.Fatalf("Login() name %q: error = %v, want ErrBadName", tt.in, err)
			}
		})
	}
}

func TestManager_Lookup_expired(t *testing.T) {
	t.Parallel()
	mgr, db := newTestManager(t)

	sess, _, err := mgr.Login(t.Context(), loginRequest(t, "Alex", testPassword))
	if err != nil {
		t.Fatalf("Login(): %v", err)
	}

	// Back-date the session past its TTL directly: Manager has no public knob
	// for "already expired", and the point of this test is what Lookup does
	// once it hits one.
	if _, err := db.W.ExecContext(t.Context(),
		"UPDATE sessions SET expires_at = ? WHERE id = ?", time.Now().Add(-time.Minute).Unix(), sess.ID,
	); err != nil {
		t.Fatalf("back-date session: %v", err)
	}

	if _, _, err := mgr.Lookup(t.Context(), sess.ID); !errors.Is(err, auth.ErrNoSession) {
		t.Fatalf("Lookup() of an expired session: error = %v, want ErrNoSession", err)
	}

	var count int
	if err := db.R.QueryRowContext(t.Context(), "SELECT count(*) FROM sessions WHERE id = ?", sess.ID).Scan(&count); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if count != 0 {
		t.Error("expired session row still present after Lookup(), want it deleted")
	}
}

func TestManager_Lookup_unknown(t *testing.T) {
	t.Parallel()
	mgr, _ := newTestManager(t)

	if _, _, err := mgr.Lookup(t.Context(), "does-not-exist"); !errors.Is(err, auth.ErrNoSession) {
		t.Fatalf("Lookup() of an unknown id: error = %v, want ErrNoSession", err)
	}
	if _, _, err := mgr.Lookup(t.Context(), ""); !errors.Is(err, auth.ErrNoSession) {
		t.Fatalf("Lookup() of an empty id: error = %v, want ErrNoSession", err)
	}
}

func TestManager_Logout(t *testing.T) {
	t.Parallel()
	mgr, _ := newTestManager(t)

	sess, _, err := mgr.Login(t.Context(), loginRequest(t, "Alex", testPassword))
	if err != nil {
		t.Fatalf("Login(): %v", err)
	}

	if err := mgr.Logout(t.Context(), sess.ID); err != nil {
		t.Fatalf("Logout(): %v", err)
	}
	if _, _, err := mgr.Lookup(t.Context(), sess.ID); !errors.Is(err, auth.ErrNoSession) {
		t.Fatalf("Lookup() after Logout(): error = %v, want ErrNoSession", err)
	}

	// Logging out a session that no longer exists is not an error: the
	// caller's goal ("this session id must not work") already holds.
	if err := mgr.Logout(t.Context(), sess.ID); err != nil {
		t.Errorf("Logout() of an already-logged-out session: %v", err)
	}
}

func TestManager_SetCookie(t *testing.T) {
	mgr, _ := newTestManager(t)
	sess := &auth.Session{ID: "test-session-id"}

	tests := []struct {
		name   string
		secure bool
	}{
		{"secure", true},
		{"insecure (dev)", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			mgr.SetCookie(rec, sess, tt.secure)

			cookies := rec.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("SetCookie() wrote %d cookies, want 1", len(cookies))
			}
			c := cookies[0]

			if c.Name != mgr.CookieName() {
				t.Errorf("Name = %q, want %q", c.Name, mgr.CookieName())
			}
			if c.Value != sess.ID {
				t.Errorf("Value = %q, want %q", c.Value, sess.ID)
			}
			if !c.HttpOnly {
				t.Error("HttpOnly = false, want true")
			}
			if c.Secure != tt.secure {
				t.Errorf("Secure = %v, want %v", c.Secure, tt.secure)
			}
			if c.SameSite != http.SameSiteLaxMode {
				t.Errorf("SameSite = %v, want Lax", c.SameSite)
			}
			if c.Path != "/" {
				t.Errorf("Path = %q, want /", c.Path)
			}
			if c.Domain != "" {
				t.Errorf("Domain = %q, want empty: a Domain attribute would hand this admin session to every workspace subdomain", c.Domain)
			}
			if c.MaxAge <= 0 {
				t.Errorf("MaxAge = %d, want a positive value derived from the session TTL", c.MaxAge)
			}
		})
	}
}

func TestManager_ClearCookie(t *testing.T) {
	mgr, _ := newTestManager(t)

	tests := []struct {
		name   string
		secure bool
	}{
		{"secure", true},
		{"insecure (dev)", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rec := httptest.NewRecorder()
			mgr.ClearCookie(rec, tt.secure)

			cookies := rec.Result().Cookies()
			if len(cookies) != 1 {
				t.Fatalf("ClearCookie() wrote %d cookies, want 1", len(cookies))
			}
			c := cookies[0]

			if c.Name != mgr.CookieName() {
				t.Errorf("Name = %q, want %q", c.Name, mgr.CookieName())
			}
			if c.Value != "" {
				t.Errorf("Value = %q, want empty", c.Value)
			}
			if c.Secure != tt.secure {
				t.Errorf("Secure = %v, want %v", c.Secure, tt.secure)
			}
			if c.Domain != "" {
				t.Errorf("Domain = %q, want empty", c.Domain)
			}
			if c.MaxAge >= 0 {
				t.Errorf("MaxAge = %d, want negative so the browser expires the cookie immediately", c.MaxAge)
			}
		})
	}
}
