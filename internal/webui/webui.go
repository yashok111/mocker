// Package webui embeds mocker's admin single-page app and serves it.
//
// It is a LEAF: only the stdlib and internal/config (for the CSP's base-domain
// host sources) may be imported here. internal/admin imports this package to
// mount the SPA in front of its own mux — the dependency runs one way only,
// so a static-asset handler can never end up holding a session, a database
// handle or anything else the plane it fronts owns.
package webui

import (
	"embed"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"github.com/yashok111/mocker/internal/config"
)

// distFS is the build output `make ui` (via web/vite.config.ts's outDir)
// writes directly into this package. all: is load-bearing, not cosmetic: a
// fresh clone's dist/ holds only .gitkeep until someone runs the build, and a
// bare "//go:embed dist" over a directory with no non-dotfile fails to
// compile — "pattern dist: cannot embed directory dist: contains no
// embeddable files" — which would make the whole module unbuildable before
// npm ever runs. all: reaches the dotfile, so the embed always succeeds; the
// resulting FS may or may not hold a real UI, which is exactly what [Built]
// distinguishes at runtime instead of at compile time.
//
//go:embed all:dist
var distFS embed.FS

// Dist is the embedded build output, rooted at the directory index.html sits
// in — every caller opens "index.html" or "assets/x.js" directly rather than
// "dist/index.html". It is the fsys the real admin mount uses; every test
// instead drives [Handler] over its own fstest.MapFS, since a test written
// against Dist could only assert whichever of "built" or "not built" happens
// to be true on the machine running it (see webui_test.go).
var Dist fs.FS = mustSub(distFS, "dist")

func mustSub(fsys embed.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		// fs.Sub only fails on a malformed dir argument, a compile-time
		// constant here — unreachable at runtime on any build that compiled
		// at all.
		panic("webui: " + err.Error())
	}
	return sub
}

// apiPrefix is the one path prefix Handler always leaves for the admin mux:
// every route the panel calls through fetch() lives under it. Defined once so
// the exact-match check below and the prefix check can never drift apart into
// two different strings.
const apiPrefix = "/api"

// healthzPath and readyzPath are exact matches, not prefixes — there is
// nothing nested under either today — so they sit outside apiPrefix rather
// than forcing a false "/healthz/" prefix check that would never fire.
const (
	healthzPath = "/healthz"
	readyzPath  = "/readyz"
)

// isAdminPath reports whether p belongs to the admin plane's own surface,
// which Handler must never intercept: an /api/ path a route doesn't match has
// to keep falling through to net/http's plain-text 404, and a method
// mismatch on a real route has to keep answering 405 with its Allow header —
// both of those live inside mux.ServeHTTP, reachable only by leaving p to
// next untouched.
func isAdminPath(p string) bool {
	// /mcp: an MCP client's streamable-HTTP probe is a GET, and the SPA
	// shell is the wrong answer to it whether or not the endpoint is
	// mounted (unmounted, the mux's own 404 is).
	return p == healthzPath || p == readyzPath || p == mcpPath || p == apiPrefix || strings.HasPrefix(p, apiPrefix+"/")
}

// mcpPath mirrors internal/admin's constant of the same name; the two
// packages may not import each other for one string.
const mcpPath = "/mcp"

// assetsPrefix marks Vite's content-hashed output — a file under it is safe
// to cache forever because a changed file gets a new name, never a changed
// body at the same name.
const assetsPrefix = "assets/"

// Handler serves the built SPA from fsys and defers every other request to
// next UNCHANGED.
//
// It wraps next rather than registering the SPA as a mux pattern. Measured on
// this project's real route set: mux.Handle("/", ui) alongside the admin
// routes turns PUT /api/workspaces from "405 Allow: GET, HEAD, POST" into a
// 404 (a second pattern rooted at "/" wins the mux's longest-match fallback
// for any method the more specific pattern doesn't register), and answers
// POST /healthz with the SPA's HTML and a 200. Probing via mux.Handler(r)
// fails identically — it returns an empty pattern for a method mismatch too,
// so there is no way to ask the mux "would anything have matched this path
// under another method" from outside. Wrapping sidesteps the mux for the one
// decision this handler makes (GET/HEAD outside the admin's own surface) and
// leaves everything else to reach mux.ServeHTTP exactly as if this package
// did not exist.
func Handler(fsys fs.FS, next http.Handler, cfg *config.Config, log *slog.Logger) http.Handler {
	csp := buildCSP(cfg)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if (r.Method != http.MethodGet && r.Method != http.MethodHead) || isAdminPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Content-Security-Policy", csp)

		if !Built(fsys) {
			log.Warn("webui: admin UI not built, answering 503", "path", r.URL.Path)
			http.Error(w, "mocker: admin UI is not built — run `make ui`, then restart", http.StatusServiceUnavailable)
			return
		}

		serveSPA(fsys, w, r, log)
	})
}

// Built reports whether fsys holds a real UI rather than the placeholder
// dist/.gitkeep a clone ships with before anyone runs `make ui`. It is the
// one signal [Handler] needs to tell "not deployed yet" apart from "deployed,
// and the visitor asked for a path the client router owns" — the latter is
// the SPA fallback below, never a 503.
func Built(fsys fs.FS) bool {
	info, err := fs.Stat(fsys, "index.html")
	return err == nil && !info.IsDir()
}

// serveSPA answers a GET or HEAD request Handler decided belongs to the SPA:
// an existing file under fsys, or the app shell for anything else (client
// routing means /w/7 is a real address a person can reload, not a 404).
func serveSPA(fsys fs.FS, w http.ResponseWriter, r *http.Request, log *slog.Logger) {
	name := strings.TrimPrefix(r.URL.Path, "/")

	if name != "" {
		switch {
		case !isSafe(name):
			// Never reaches fsys.Open: fs.ValidPath alone would already
			// reject "../" and a doubled leading slash, but a NUL byte is
			// valid UTF-8 and would sail through that check — logged as a
			// probe, then answered exactly like any other path this SPA
			// doesn't recognize, one line down.
			log.Warn("webui: rejected unsafe request path", "path", r.URL.Path)
		case fileExists(fsys, name):
			if strings.HasPrefix(name, assetsPrefix) {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			serveFile(fsys, name, w, r)
			return
		}
	}

	w.Header().Set("Cache-Control", "no-cache")
	serveFile(fsys, "index.html", w, r)
}

// fileExists reports whether name is a regular file under fsys — called only
// after [isSafe], so name is already known to be a valid, non-traversing
// fs.FS path.
func fileExists(fsys fs.FS, name string) bool {
	info, err := fs.Stat(fsys, name)
	return err == nil && !info.IsDir()
}

// isSafe reports whether name — already stripped of its leading "/" — may be
// looked up in fsys at all. fs.ValidPath rejects "../" traversal, a leading
// "/" (a doubled slash in the request) and an empty element, but treats a NUL
// byte as just another valid UTF-8 rune inside a path segment; nothing in the
// fs.FS contract promises every implementation rejects that on its own, so it
// is checked explicitly here rather than borrowed from whichever fsys this
// handler happens to be given.
func isSafe(name string) bool {
	return fs.ValidPath(name) && !strings.ContainsRune(name, 0)
}

// serveFile writes name's content from fsys, or a 500 if fsys cannot produce
// it — which [serveSPA]'s callers only reach after already confirming the
// file exists (via [fileExists] or, for the fallback, [Built]), so this is a
// defensive last line, not an expected path.
//
// http.ServeContent is used instead of http.ServeFileFS deliberately:
// ServeFileFS rejects any request whose r.URL.Path contains a ".." element —
// checking the ORIGINAL request path, not the name argument being served —
// which would 400 the very index.html fallback [serveSPA] falls back to for
// a rejected traversal attempt. ServeContent has no such check; it only reads
// name to pick a Content-Type and serve Range/If-Modified-Since correctly.
func serveFile(fsys fs.FS, name string, w http.ResponseWriter, r *http.Request) {
	info, err := fs.Stat(fsys, name)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	f, err := fsys.Open(name)
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	defer func() { _ = f.Close() }()

	rs, ok := f.(io.ReadSeeker)
	if !ok {
		// embed.FS and fstest.MapFS both hand back a seekable file; one that
		// doesn't cannot serve Range requests correctly, and pretending
		// otherwise would silently corrupt a resumed asset download rather
		// than fail loudly here.
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.ServeContent(w, r, name, info.ModTime(), rs)
}

// buildCSP renders the Content-Security-Policy Handler sets on every response
// it serves. Every directive is fixed except connect-src: the panel's
// cross-origin fetch targets a workspace host under cfg.BaseDomain and
// nowhere else, and cfg.BaseDomain is legally empty in path mode (DESIGN
// §16) — where the panel never leaves its own origin — so the two host
// sources are omitted rather than emitting the meaningless "http://*.:*" a
// naive format string would produce for an empty domain. ":*" on each is
// load-bearing: a CSP host-source with no port matches only the scheme's
// default port, and mocker runs on 8080.
func buildCSP(cfg *config.Config) string {
	directives := make([]string, 0, 8)
	directives = append(directives,
		"default-src 'self'",
		"script-src 'self'", // spelled out despite default-src covering it, so tests have a directive to assert on directly
		"base-uri 'none'",
		"frame-ancestors 'none'",
		"object-src 'none'",
		"img-src 'self' data:",
		"style-src 'self' 'unsafe-inline'",
	)

	connect := "connect-src 'self'"
	if cfg.BaseDomain != "" {
		connect += " http://*." + cfg.BaseDomain + ":* https://*." + cfg.BaseDomain + ":*"
		// P6d (decisions.md mocker-p6d-websocket D11; DESIGN §30.14): the
		// WebSocket schemes named explicitly rather than left to the
		// specification's scheme-upgrade rule, a bet across a browser
		// fleet this project cannot see — for P6e's browser test client.
		connect += " ws://*." + cfg.BaseDomain + ":* wss://*." + cfg.BaseDomain + ":*"
	} else if cfg.AdminHost != "" {
		// Path mode: a workspace lives on the admin host itself, so the
		// socket does too. 'self' is not relied on for ws: for the same
		// reason as above; ":*" for the reason the http sources carry it —
		// a host-source with no port matches only the scheme's default
		// port, and mocker listens on 8080 (diff-review finding 4).
		connect += " ws://" + cfg.AdminHost + ":* wss://" + cfg.AdminHost + ":*"
	}
	directives = append(directives, connect)

	return strings.Join(directives, "; ")
}
