package admin

import (
	"context"
	"errors"
	"net/http"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/httpx"
)

// userView is the wire shape of an [auth.User].
type userView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Role      string `json:"role"`
	CreatedAt int64  `json:"createdAt"`
}

func newUserView(u *auth.User) userView {
	return userView{ID: u.ID, Name: u.Name, Role: u.Role, CreatedAt: u.CreatedAt.Unix()}
}

// serverConfigView is the wire shape of the deployment facts the browser
// cannot derive by inspecting the page it's running on: config.Load rejects
// any MOCKER_ADMIN_HOST that sits under MOCKER_BASE_DOMAIN precisely so a
// workspace subdomain can never look like the admin host, which also means
// window.location tells the panel nothing about either value.
type serverConfigView struct {
	ReservedPrefix string `json:"reservedPrefix"`
	BaseDomain     string `json:"baseDomain"`
	Routing        string `json:"routing"`
	// Limits (A9): the same config.Limits projection get_server_config
	// hands an agent, so the panel's caps strip shows the effective numbers
	// rather than a copy of the validator's constants.
	Limits config.Limits `json:"limits"`
}

func newServerConfigView(cfg *config.Config) serverConfigView {
	return serverConfigView{
		ReservedPrefix: cfg.ReservedPrefix,
		BaseDomain:     cfg.BaseDomain,
		Routing:        string(cfg.Routing),
		Limits:         cfg.Limits(),
	}
}

// authResponse is the body of both POST /api/auth/login and GET /api/me: the
// caller's identity, the CSRF token every subsequent state-changing request
// must echo back in X-CSRF-Token (DESIGN §15), and the deployment config the
// panel has no other way to learn.
type authResponse struct {
	User      userView         `json:"user"`
	CSRFToken string           `json:"csrfToken"`
	Config    serverConfigView `json:"config"`
}

// newAuthResponse is the ONE place authResponse gets built. handleLogin and
// handleMe must answer the identical shape — a client that bootstraps its
// config from one and logs in through the other would see the two drift the
// first time either handler's literal changes and the other doesn't.
func newAuthResponse(cfg *config.Config, u *auth.User, csrfToken string) authResponse {
	return authResponse{User: newUserView(u), CSRFToken: csrfToken, Config: newServerConfigView(cfg)}
}

// handleLogin authenticates the request through [auth.Manager.Login], sets
// the session cookie and returns the caller's identity and CSRF token.
//
// Body parsing happens entirely inside Login/[auth.SharedPassword.Login] —
// this handler never touches r.Body — because the password must be verified
// before the get-or-create-by-name step touches the database, an ordering
// [auth.Manager.Login] already guarantees structurally.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	sess, user, err := s.sessions.Login(r.Context(), r)
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrBadPassword), errors.Is(err, auth.ErrBadName):
			// Both take the same response on purpose (mirroring
			// [auth.ErrBadPassword]'s own doc comment): a name-validation
			// failure is only ever reached AFTER the password check
			// succeeds, so giving it a different status than a wrong
			// password would leak "your password guess was right" through
			// the HTTP status code alone.
			httpx.Err(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "invalid credentials")
		case isMalformedJSON(err):
			httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid request body")
		default:
			s.log.Error("login failed", "err", err)
			httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "login failed")
		}
		return
	}
	s.ensureDefaultWorkspace(r.Context(), user)

	s.sessions.SetCookie(w, sess, s.cookieSecure(r))
	httpx.JSON(w, http.StatusOK, newAuthResponse(s.cfg, user, sess.CSRFToken))
}

// ensureDefaultWorkspace implements DESIGN §14 screen 2 ("Первый вход"): if
// MOCKER_DEFAULT_SPEC is set and this user has zero workspaces, one gets
// created for them automatically, against that spec.
//
// It needs NO new route and no login-response field to satisfy "the chosen
// slug is shown, not silent" (DESIGN's own requirement on the slug
// [workspaces.Repo.EnsureDefault] picks): the workspace exists, committed,
// before this handler answers 200, and the panel's very next call after
// login is always GET /api/workspaces (the "Первый вход"/workspace-list
// screen) — a call that already renders every workspace's slug. Threading
// the slug through the login response too would just be a second place for
// the two to drift.
//
// Called unconditionally on every successful login, not only ones that
// "look new": [workspaces.Repo.EnsureDefault] itself is the zero-workspaces
// check, and running it here is cheap (one indexed SELECT) for every OTHER
// login where the user already owns a workspace, next to the read-modify
// round trip a login already does.
//
// Any failure here — spec not found (deleted after the startup check in
// cmd/mocker/main.go passed, an operator error but not this handler's to
// invent a new status code for) or a database error — is logged and
// swallowed rather than failing the login: MOCKER_DEFAULT_SPEC unset is
// specified to leave login exactly as it is today, and a user whose
// auto-provisioning is broken should still be able to log in and create a
// workspace by hand.
func (s *Server) ensureDefaultWorkspace(ctx context.Context, user *auth.User) {
	if s.cfg.DefaultSpecID == 0 {
		return
	}
	sp, err := s.specsRepo.ByID(ctx, s.cfg.DefaultSpecID)
	if err != nil {
		s.log.Error("auto-create workspace: load MOCKER_DEFAULT_SPEC", "err", err, "spec_id", s.cfg.DefaultSpecID)
		return
	}
	// Mirrors handleCreateWorkspace's own spec-attach behavior (DESIGN §7
	// step 3's "где его можно править" hint): a freshly auto-created
	// workspace starts with the spec's base path, not an empty one.
	settings := domain.DefaultSettings()
	settings.BasePath = sp.BasePath

	ws, err := s.ws.EnsureDefault(ctx, user.ID, user.Name, sp.ID, &settings)
	if err != nil {
		s.log.Error("auto-create workspace", "err", err, "user_id", user.ID)
		return
	}
	if ws != nil {
		s.log.Info("auto-created workspace on first login", "user_id", user.ID, "workspace_slug", ws.Slug)
	}
}

// handleLogout clears the session cookie and invalidates the session
// server-side. It is idempotent: a request with no cookie, or one naming a
// session that is already gone, still answers 204 — the caller's goal ("this
// session must not work") already holds either way.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(s.sessions.CookieName()); err == nil && c.Value != "" {
		if err := s.sessions.Logout(r.Context(), c.Value); err != nil {
			// Still clear the cookie below: a database hiccup on logout must
			// not leave the browser holding a cookie it thinks is live.
			s.log.Error("logout", "err", err)
		}
	}
	s.sessions.ClearCookie(w, s.cookieSecure(r))
	httpx.NoContent(w)
}

// handleMe answers the caller's own identity and CSRF token, or 401 when no
// valid session is attached.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	user, ok := s.requireUser(w, r)
	if !ok {
		return
	}
	sess, ok := sessionFrom(r.Context())
	if !ok {
		// Cannot happen in practice: attachSession sets user and session
		// together. Fail the same way as "not logged in" rather than panic
		// on a nil CSRFToken.
		httpx.Err(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "authentication required")
		return
	}
	httpx.JSON(w, http.StatusOK, newAuthResponse(s.cfg, user, sess.CSRFToken))
}

// isMalformedJSON reports whether err looks like a request-body decoding
// failure rather than a downstream (database) failure, so [Server.handleLogin]
// can answer 400 instead of 500 for a client mistake.
//
// [auth.SharedPassword.Login] decodes the body itself — this package never
// sees r.Body on the login path — so the only signal available is the error
// value's shape. That shape is decoder-specific (sonic and encoding/json name
// a type mismatch differently), which is exactly why the question is asked of
// [jsonx.Malformed] rather than answered here with an errors.As that would
// keep compiling after a backend change and quietly stop matching.
func isMalformedJSON(err error) bool {
	return jsonx.Malformed(err)
}
