package mcp

import (
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
)

// bearerPrefix is the exact form MOCKER_MCP_KEY must arrive wrapped in, per
// RFC 6750: "Authorization: Bearer <token>".
const bearerPrefix = "Bearer "

// guard applies §A2's whole defense in front of next (Handler's
// context-placing wrapper around the SDK's streamable HTTP handler):
//
//  1. the Authorization header must carry the configured bearer key,
//     compared in constant time — like enforceCSRF already compares the
//     CSRF token (internal/admin/security.go);
//  2. Origin, when the request carries one at all, must name cfg.AdminHost.
//     A real MCP client is not a browser and sends no Origin, so this never
//     rejects one — it only rejects an Origin naming somewhere else. This is
//     not a weakening of the bearer check: /mcp never reads the session
//     cookie (§A2), so there is no ambient credential for a forged
//     cross-origin page to ride on, and a page that could set Authorization
//     already had the key.
//
// A rejection at either step answers a plain, small text body — never
// mocker's HTML shell (this branch runs entirely outside webui.Handler,
// internal/admin/server.go's Handler doc comment explains why), never the
// configured key, and never a hint about the key's length: both failure
// paths use a short fixed string, not one derived from what was wrong.
func (e *Endpoint) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !validBearer(r.Header.Get("Authorization"), e.key) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" && !originMatchesAdminHost(origin, e.cfg.AdminHost) {
			http.Error(w, "origin not allowed", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// validBearer reports whether header is exactly "Bearer <key>", comparing
// the token half in constant time so a wrong guess cannot be narrowed down
// byte by byte through response-time measurement.
// crypto/subtle.ConstantTimeCompare itself returns 0 immediately for
// mismatched lengths, in constant time relative to that comparison — the
// same guarantee internal/admin/security.go's enforceCSRF already relies on
// for its own X-CSRF-Token check, so no separate length pre-check is added
// here either.
func validBearer(header, key string) bool {
	token, ok := strings.CutPrefix(header, bearerPrefix)
	if !ok {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(token), []byte(key)) == 1
}

// originMatchesAdminHost mirrors internal/admin/security.go's
// hostMatchesAdmin rather than silently forking a second same-origin rule:
// both parse rawURL and compare only its Hostname() (port and scheme
// stripped) against adminHost, case-insensitively. admin's version is
// unexported, so this is a second copy of the same one-line comparison —
// not a divergent rule invented for this package — because internal/mcp
// does not import internal/admin at all (§B4).
func originMatchesAdminHost(rawURL, adminHost string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return false
	}
	return strings.EqualFold(u.Hostname(), adminHost)
}
