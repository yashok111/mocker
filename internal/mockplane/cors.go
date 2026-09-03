package mockplane

import (
	"net/http"

	"github.com/yashok111/mocker/internal/domain"
)

// corsMethods is the full method set the mock plane may ever answer for a
// preflight (DESIGN §8). It is the same fixed list for every workspace: which
// methods a given operation actually supports is a route-table decision made
// after preflight has already answered, so the preflight cannot narrow it
// without lying to the browser about a method the real request might still
// use.
const corsMethods = "GET,POST,PUT,PATCH,DELETE,OPTIONS"

// preflightMaxAge is how long a browser may cache a preflight answer before
// asking again (DESIGN §8).
const preflightMaxAge = "600"

// isPreflight reports whether r is a CORS preflight: OPTIONS carrying
// Access-Control-Request-Method is the browser's own signal for one. A bare
// OPTIONS with no such header is a normal request some client happens to
// send and is routed like any other.
func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

// corsActive reports whether CORS headers should be written at all: a
// request with no Origin has no cross-origin caller to answer, and
// settings.cors.mode == "off" is the workspace explicitly opting out.
func corsActive(r *http.Request, cors domain.CORSSettings) bool {
	return r.Header.Get("Origin") != "" && cors.Mode != domain.CORSOff
}

// setCORS sets the CORS headers that belong on every response once a
// workspace's policy is known, preflight or not: the echoed origin (never
// "*" — with credentials on, "*" is invalid, and reflecting is simplest
// either way), Vary: Origin (a cache in front of mocker must not serve one
// origin's response to another), Allow-Credentials when the workspace wants
// cookie-based auth to work cross-origin (DESIGN §10), and
// Expose-Headers so the browser's JS can read whatever headers a mocked
// response sets ("*" is valid here even with credentials on — unlike
// Allow-Origin, the Fetch spec allows a literal "*" for Expose-Headers
// regardless of credentials mode).
func setCORS(w http.ResponseWriter, r *http.Request, cors domain.CORSSettings) {
	if !corsActive(r, cors) {
		return
	}
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", r.Header.Get("Origin"))
	h.Add("Vary", "Origin")
	if cors.Credentials {
		h.Set("Access-Control-Allow-Credentials", "true")
	}
	h.Set("Access-Control-Expose-Headers", "*")
}

// writePreflight answers a CORS preflight with a 204. setCORS must already
// have run on w (DESIGN §6 step 2 runs unconditionally before the preflight
// check in step 3), so this only adds the headers a preflight needs beyond
// that: Allow-Methods (without it the browser fails any PUT/PATCH/DELETE
// before it is even sent), an echo of Access-Control-Request-Headers (so a
// custom header like Authorization survives preflight), and Max-Age (so the
// browser caches the answer instead of preflighting every single request).
func writePreflight(w http.ResponseWriter, r *http.Request, cors domain.CORSSettings) {
	if corsActive(r, cors) {
		h := w.Header()
		h.Set("Access-Control-Allow-Methods", corsMethods)
		if reqHeaders := r.Header.Get("Access-Control-Request-Headers"); reqHeaders != "" {
			h.Set("Access-Control-Allow-Headers", reqHeaders)
		}
		h.Set("Access-Control-Max-Age", preflightMaxAge)
	}
	w.WriteHeader(http.StatusNoContent)
}
