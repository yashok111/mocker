// Package server holds the one thing P0 exists to prove: the dispatcher that
// decides, for an inbound request, whether the admin plane or the mock plane
// answers it — and answers itself, with a 404 naming both expected shapes,
// when neither claims it.
//
// Everything else in P0 (auth, workspaces, the two planes) is inert without
// this: it is the single place a request's Host header (or, in path mode, its
// URL) turns into "admin" or "workspace X", which is exactly the routing
// DESIGN §19's acceptance criterion checks.
package server

import (
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/mockplane"
)

// workspacePathPrefix is the path-mode (MOCKER_ROUTING=path, DESIGN §16)
// stand-in for a workspace subdomain: /w/{slug}/... on the single shared
// origin — since A6 the one const in internal/httpx, shared with the admin
// plane's workspace URL and the mock plane's asset_url base.
const workspacePathPrefix = httpx.WorkspacePathPrefix

// New builds the host/path dispatcher: cfg.Routing picks which of the two
// mutually exclusive strategies below decides where a request goes. admin and
// mock are held, not copied, so a later config reload is visible to the next
// request without rebuilding the dispatcher.
func New(cfg *config.Config, admin http.Handler, mock *mockplane.Plane, log *slog.Logger) http.Handler {
	d := &dispatcher{cfg: cfg, admin: admin, mock: mock, log: log}
	if cfg.Routing == config.RoutingPath {
		return http.HandlerFunc(d.servePath)
	}
	return http.HandlerFunc(d.serveHost)
}

type dispatcher struct {
	cfg   *config.Config
	admin http.Handler
	mock  *mockplane.Plane
	log   *slog.Logger
}

// serveHost implements the normal mode (DESIGN §2/§6): the Host header alone
// says which plane a request belongs to. This is the routing the P0
// acceptance criterion exercises.
func (d *dispatcher) serveHost(w http.ResponseWriter, r *http.Request) {
	if d.cfg.IsAdminHost(r.Host) {
		d.admin.ServeHTTP(w, r)
		return
	}
	if _, ok := d.cfg.IsWorkspaceHost(r.Host); ok {
		d.mock.ServeHTTP(w, r)
		return
	}
	d.log.Debug("unmatched host", "host", r.Host)
	unknownHost(w, r, d.cfg)
}

// unknownHost answers a request whose Host matches neither plane. The body
// deliberately names BOTH expected host shapes: in a closed contour with no
// public internet reaching this box, a request that got this far already
// cleared DNS and (if applicable) TLS, so the real open question is "which of
// DNS, TLS or my MOCKER_BASE_DOMAIN/MOCKER_ADMIN_HOST config disagrees with
// what I typed" — not "does this route exist", which a bare 404 would imply.
func unknownHost(w http.ResponseWriter, r *http.Request, cfg *config.Config) {
	httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, fmt.Sprintf(
		"host %q matches neither the admin host (%s) nor a workspace host (<slug>.%s)",
		r.Host, cfg.AdminHost, cfg.BaseDomain))
}

// servePath implements the emergency mode (DESIGN §16) for contours without
// wildcard DNS: both planes share one origin, distinguished by path instead
// of Host. IsWorkspaceHost cannot help here — path mode does not even require
// MOCKER_BASE_DOMAIN to be set — so the workspace slug is read out of the URL
// itself and handed to [mockplane.Plane.ServeSlug] directly.
//
// Everything that is neither /api/*, /healthz, /readyz nor a /w/{slug}/...
// workspace path falls to d.admin — the SPA and its client-side routes
// (webui.Handler wraps d.admin, so a GET without a matching admin API route
// gets the SPA shell; every other method still reaches the admin mux with
// its own 404/405 behaviour intact, see webui.Handler's own doc comment).
// This is what makes the admin UI reachable at all under MOCKER_ROUTING=path:
// before this, the default branch answered pathModeNotFound unconditionally,
// so "/", "/login", "/workspaces/7" and every asset the SPA fetches 404'd.
//
// Two consequences of routing the default branch to d.admin instead of
// pathModeNotFound, worth knowing before debugging either:
//   - "/api" and "/w" WITHOUT a trailing slash no longer match the first two
//     cases above (they require "/api/" and workspacePathPrefix "/w/") and so
//     no longer get the diagnostic pathModeNotFound body either — they now
//     fall through to d.admin like any other unrecognized path.
//   - A non-GET request to a path the admin mux does not itself route now
//     answers 415 from enforceCSRF (Server.Handler's own httpx.Chain runs
//     CSRF enforcement before the mux dispatches, and it checks
//     Content-Type before the mux gets a chance to 404) rather than the 404
//     pathModeNotFound used to give it.
//
// The /w/ empty-slug case is NOT part of this change: cutWorkspacePath
// returning !ok still answers pathModeNotFound below, because "/w/" (or
// "/w/" with no further segment) is a malformed WORKSPACE url, not a client
// route the SPA could ever own — routing it to d.admin would have the SPA
// silently swallow what is actually a workspace-slug typo.
//
// What this mode breaks, and does not try to fix: an absolute path emitted by
// mocked JSON or a Location header points at the origin root, not back under
// /w/{slug}/, and a cookie a mocked response sets with Path=/ leaks across
// every workspace sharing this origin instead of staying scoped to one. Only
// a deployment with no other option should ever set MOCKER_ROUTING=path.
func (d *dispatcher) servePath(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch {
	case path == "/healthz", path == "/readyz", strings.HasPrefix(path, "/api/"):
		d.admin.ServeHTTP(w, r)
		return
	case strings.HasPrefix(path, workspacePathPrefix):
		slug, rest, ok := cutWorkspacePath(path)
		if !ok {
			d.log.Debug("path mode: empty workspace slug", "path", path)
			pathModeNotFound(w, r)
			return
		}
		// Clone rather than mutate r in place: the outer httpx.RequestLog
		// middleware (wrapped around this whole dispatcher in main) logs
		// r.URL.Path AFTER this handler returns, and it should show the
		// request as it actually arrived, not the rewritten form the mock
		// plane saw. Request.Clone deep-copies URL, so mutating the clone's
		// Path cannot leak back into r.
		//
		// RawPath is cleared rather than trimmed in step with Path: the mock
		// plane's own NormalizePath (DESIGN §8) relies on EscapedPath() to
		// tell an encoded "%2F" apart from a literal "/" segment boundary,
		// and re-deriving RawPath's prefix by byte-length would silently
		// break that the moment the slug or an earlier segment contained a
		// percent-encoded character. Clearing it makes EscapedPath() fall
		// back to re-escaping the already-decoded Path — correct for the
		// overwhelming majority of paths, and simply one more entry on path
		// mode's known-broken list (see the doc comment above) for the rest.
		r2 := r.Clone(r.Context())
		r2.URL.Path = rest
		r2.URL.RawPath = ""
		d.mock.ServeSlug(w, r2, slug)
		return
	default:
		// Everything else — "/", "/login", "/workspaces/7", "/assets/app.js"
		// and any other client route or asset the SPA fetches — is the admin
		// plane's to answer, GET or not; see this function's own doc comment
		// for exactly what that means and the two shapes it stops
		// diagnosing.
		d.admin.ServeHTTP(w, r)
	}
}

// cutWorkspacePath splits a path-mode workspace path ("/w/{slug}/rest...")
// into the slug and the path [mockplane.Plane.ServeSlug] should see with the
// "/w/{slug}" prefix stripped off — the same request that arrives on
// <slug>.BaseDomain as just "/rest...". ok is false for a bare "/w/" or
// "/w" with no slug at all.
func cutWorkspacePath(path string) (slug, rest string, ok bool) {
	trimmed, found := strings.CutPrefix(path, workspacePathPrefix)
	if !found {
		return "", "", false
	}
	slug, rest, _ = strings.Cut(trimmed, "/")
	if slug == "" {
		return "", "", false
	}
	return slug, "/" + rest, true
}

// pathModeNotFound answers ONLY the one shape servePath still refuses
// outright: a /w/ (or /w) path with no workspace slug after it — a malformed
// workspace URL, not a client route. Since servePath's own default branch
// now hands everything else to d.admin (this file's biggest change this
// phase; see servePath's doc comment), this function is no longer path
// mode's general 404 — it survives only for cutWorkspacePath's !ok case.
func pathModeNotFound(w http.ResponseWriter, r *http.Request) {
	httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, fmt.Sprintf(
		"path %q is missing a workspace slug; a workspace path looks like %s{slug}/...",
		r.URL.Path, workspacePathPrefix))
}
