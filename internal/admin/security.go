package admin

import (
	"bytes"
	"context"
	"crypto/subtle"
	"io"
	"math"
	"mime"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/httpx"
)

// Error codes local to the admin plane: httpx's shared set (CodeBadRequest,
// CodeForbidden, ...) has no entry for "wrong media type" or "rate limited",
// and inventing one there would be a change to a file this package does not
// own.
const (
	codeUnsupportedMediaType = "unsupported_media_type"
	codeRateLimited          = "rate_limited"
)

// ctxKey is the unexported type behind every value this package stores in a
// request context, so nothing outside admin can collide with — or forge —
// the key.
type ctxKey struct{}

// authContext is what attachSession stores: both the session (enforceCSRF
// needs its csrf_token) and the user (handlers need it, exported only
// through [UserFrom]).
type authContext struct {
	session *auth.Session
	user    *auth.User
}

// withAuthContext returns ctx carrying sess and user for the rest of the
// request's lifetime.
func withAuthContext(ctx context.Context, sess *auth.Session, user *auth.User) context.Context {
	return context.WithValue(ctx, ctxKey{}, authContext{session: sess, user: user})
}

// UserFrom returns the authenticated user attached to ctx by the auth
// middleware, or false if the request carried no valid session. It is the
// ONLY way outside this package to read who is logged in.
func UserFrom(ctx context.Context) (*auth.User, bool) {
	ac, ok := ctx.Value(ctxKey{}).(authContext)
	if !ok || ac.user == nil {
		return nil, false
	}
	return ac.user, true
}

// sessionFrom mirrors [UserFrom] for the session itself. It stays unexported:
// only enforceCSRF and handleMe, both inside this package, need the
// csrf_token; nothing outside admin has a reason to see a session id.
func sessionFrom(ctx context.Context) (*auth.Session, bool) {
	ac, ok := ctx.Value(ctxKey{}).(authContext)
	if !ok || ac.session == nil {
		return nil, false
	}
	return ac.session, true
}

// requireUser resolves the caller from context, answering 401 and reporting
// failure when nobody is logged in.
//
// This is the ONLY authorization check any handler performs. DESIGN §15 is
// explicit that owner_id is a label, not a trusted identity: once a user
// clears this check, every workspace is theirs to read and edit, including
// ones somebody else created. GET /api/workspaces defaults its listing to the
// caller's own workspaces (see [Server.handleListWorkspaces]) purely as a
// convenience filter, not a permission boundary — do not "fix" that into an
// ownership check the design deliberately does not want.
func (s *Server) requireUser(w http.ResponseWriter, r *http.Request) (*auth.User, bool) {
	user, ok := UserFrom(r.Context())
	if !ok {
		httpx.Err(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "authentication required")
		return nil, false
	}
	return user, true
}

// securityHeaders sets the fixed response headers DESIGN §15 requires on
// every admin-plane response, success or error alike.
func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		next.ServeHTTP(w, r)
	})
}

// attachSession looks up the session cookie, if any, and stores it (and its
// user) in the request context.
//
// It never rejects a request itself — a missing or invalid cookie leaves the
// context empty and the request proceeds unauthenticated. GET /healthz and
// POST /api/auth/login must work with no cookie at all, and every protected
// handler already enforces its own 401 via [Server.requireUser]; a hard
// reject here would just duplicate that check one layer up and get the
// public routes wrong in the process.
func (s *Server) attachSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(s.sessions.CookieName())
		if err != nil || c.Value == "" {
			next.ServeHTTP(w, r)
			return
		}
		sess, user, err := s.sessions.Lookup(r.Context(), c.Value)
		if err != nil {
			next.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(withAuthContext(r.Context(), sess, user)))
	})
}

// rawBodyRoute is the ONE route whose state-changing body is not JSON: A6's
// PUT /api/workspaces/{id}/assets/{name}, where the body IS the uploaded
// file and Content-Type is its media type (DESIGN §32.5, decisions.md
// mocker-a6-assets D3). enforceCSRF swaps check 1 for "parseable and not
// browser-executable" on exactly this predicate and keeps checks 2 and 3
// unchanged (matched on the ESCAPED path, the same string the mux matches,
// so a name carrying %2F misses here exactly as it 404s there) — the X-CSRF-Token header is what makes the request non-simple
// and preflighted, so the raw body does not reopen the hole check 1 exists
// to close. The handler repeats the media-type refusal (two gates, like
// every media type in this tree), because the MCP loopback bypasses this
// chain by construction. A regexp rather than r.Pattern: this middleware
// runs BEFORE the mux, when the pattern is not yet known.
var rawBodyRoute = regexp.MustCompile(`^/api/workspaces/[0-9]+/assets/[^/]+$`)

func isRawBodyRequest(r *http.Request) bool {
	return r.Method == http.MethodPut && rawBodyRoute.MatchString(r.URL.EscapedPath())
}

// isStateChanging reports whether r is one enforceCSRF must guard: a
// state-changing METHOD, or — P6d (decisions.md mocker-p6d-websocket D11;
// DESIGN §30.10) — a GET that asks for a WebSocket upgrade. A WebSocket
// handshake is a GET that CORS does not cover and whose frames a hostile
// page can READ, so it is state-changing for this guard's purposes; no
// admin route upgrades today, and this is the one function that owns the
// predicate, so the door is locked before any route behind it exists rather
// than in a second copy inside a handler.
func isStateChanging(r *http.Request) bool {
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	case http.MethodGet:
		return isUpgradeRequest(r)
	default:
		return false
	}
}

// isUpgradeRequest is the WebSocket handshake's own signature: a Connection
// header carrying the token "upgrade" and an Upgrade header naming
// "websocket", both case-insensitive as RFC 6455 §4.2.1 reads them.
func isUpgradeRequest(r *http.Request) bool {
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return false
	}
	for tok := range strings.SplitSeq(r.Header.Get("Connection"), ",") {
		if strings.EqualFold(strings.TrimSpace(tok), "upgrade") {
			return true
		}
	}
	return false
}

// enforceCSRF implements DESIGN §15's CSRF defense. SameSite=Lax alone does
// not stop a page hosted on a sibling subdomain inside the same contour: that
// page is same-SITE (shares the registrable domain with the admin host) even
// though it is a different ORIGIN, so the browser attaches the session cookie
// to a forged request exactly as it would to a legitimate one. Every
// state-changing request must therefore clear three independent checks:
//
//  1. Content-Type is exactly application/json. A parser tolerant of
//     text/plain or form encodings turns the request into a browser "simple"
//     request, which is exactly the shape a forged cross-origin POST takes.
//  2. Origin (or, absent that, Referer — never neither) names this admin
//     host. The attacker's page cannot spoof this: it is set by the browser
//     from the page's own origin, invisible to and uncontrollable by script.
//  3. X-CSRF-Token matches the session's csrf_token, compared in constant
//     time. The attacker's page cannot read this: it is httpOnly and never
//     reflected anywhere the attacker's origin can see it.
//
// Login is exempt from check 3 only — there is no session yet to compare
// against — never from 1 or 2.
func (s *Server) enforceCSRF(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isStateChanging(r) {
			next.ServeHTTP(w, r)
			return
		}

		mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
		if isRawBodyRequest(r) {
			if err != nil || mediaType == "" || httpx.BrowserExecutableMediaType(mediaType) {
				httpx.Err(w, http.StatusUnsupportedMediaType, codeUnsupportedMediaType,
					"Content-Type must be the file's own media type, and not one a browser executes")
				return
			}
		} else if err != nil || mediaType != "application/json" {
			httpx.Err(w, http.StatusUnsupportedMediaType, codeUnsupportedMediaType, "Content-Type must be application/json")
			return
		}

		if !s.originAllowed(r) {
			httpx.Err(w, http.StatusForbidden, httpx.CodeForbidden, "origin not allowed")
			return
		}

		if r.URL.Path == loginPath {
			next.ServeHTTP(w, r)
			return
		}

		sess, ok := sessionFrom(r.Context())
		if !ok {
			// No session at all: let the handler answer. Every protected
			// handler 401s via requireUser, a more accurate status than the
			// CSRF-shaped 403 below for "you were never logged in".
			next.ServeHTTP(w, r)
			return
		}
		token := r.Header.Get("X-CSRF-Token")
		if token == "" || subtle.ConstantTimeCompare([]byte(token), []byte(sess.CSRFToken)) != 1 {
			httpx.Err(w, http.StatusForbidden, httpx.CodeForbidden, "csrf token mismatch")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// originAllowed implements the Origin/Referer half of [Server.enforceCSRF]:
// Origin wins when present; Referer is consulted only in its absence
// (browsers omit Origin on some same-origin GETs and POSTs originating from a
// clicked link, never on both); and if neither header is present the request
// is rejected outright rather than let through, since that is exactly what a
// non-browser forged request looks like.
func (s *Server) originAllowed(r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" {
		return hostMatchesAdmin(origin, s.cfg.AdminHost)
	}
	if referer := r.Header.Get("Referer"); referer != "" {
		return hostMatchesAdmin(referer, s.cfg.AdminHost)
	}
	return false
}

// hostMatchesAdmin reports whether rawURL's host (port and scheme stripped)
// equals adminHost, case-insensitively.
func hostMatchesAdmin(rawURL, adminHost string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || u.Hostname() == "" {
		return false
	}
	return strings.EqualFold(u.Hostname(), adminHost)
}

// loginKey is the login rate limiter's bucket key: the resolved client
// address AND the name submitted in that attempt.
//
// Keying on address alone collapses every caller behind the same reverse
// proxy into ONE bucket the moment MOCKER_TRUST_PROXY is off — the default
// (round-1 review finding 5): ResolvePeer then falls back to the immediate
// peer for every request, which behind a shared front door is the proxy's
// own address for everybody. One attacker anywhere in the contour then
// exhausts the single shared bucket and locks every colleague's login out,
// repeatedly, indefinitely. Adding the submitted name narrows a flood's
// blast radius to the one name it targets: noise sent under a name nobody
// uses burns only that name's bucket, and a real colleague logging in under
// their own name keeps their own budget regardless of what else is
// happening behind the same proxy.
type loginKey struct {
	addr netip.Addr
	name string
	// addrOnly marks the per-address bucket every attempt from addr shares
	// regardless of name (see rateLimiter): a separate flag rather than
	// name == "" so an attempt that really submits an empty name does not
	// land in it.
	addrOnly bool
}

// maxLoginNameRunes caps the name as the limiter KEYS it — auth's own
// validateName refuses anything longer downstream, so a longer key can only
// ever belong to an attempt that is going to fail anyway, and it must not
// pin up to MaxLoginBody bytes in the map per attempt while it does.
const maxLoginNameRunes = 64

// maxLoginBuckets bounds the map absolutely. allow prunes expired buckets
// on every call, which bounds it by the distinct keys seen inside one
// window — but the caller chooses the name half of the key, so "distinct
// keys per window" is an attacker's number, not ours. Past the cap a NEW
// key is refused outright (429) until a window expires; existing buckets
// keep working.
const maxLoginBuckets = 4096

// addrLimitMultiplier is the per-address ceiling as a multiple of the
// per-(address, name) limit. The name in the key exists so that a flood
// under a name nobody uses cannot lock a colleague out (loginKey's own
// comment) — but on its own it also means a fresh name per attempt is a
// fresh 10/minute budget, and the ONE shared password can be guessed at
// any rate from one address. The per-address bucket closes that: it is
// wide enough that a whole contour behind one proxy (MOCKER_TRUST_PROXY
// off) is not locked out by ordinary mistakes, and narrow enough that a
// brute force is bounded per minute per address.
const addrLimitMultiplier = 10

// rateLimiter is a fixed-window counter per key, sized for exactly one route:
// POST /api/auth/login (DESIGN §15, 10 attempts/minute). A map keyed by
// remote address with no eviction is an unbounded-memory bug the moment
// anyone in the contour keeps hitting it from a rotating source — allow
// prunes every expired bucket on every call, which bounds the map by the
// number of DISTINCT callers seen inside the current window, not by the
// number of requests ever made.
type rateLimiter struct {
	mu      sync.Mutex
	buckets map[loginKey]*rateBucket
	limit   int // per (address, name)
	window  time.Duration
}

// rateBucket is one key's state: how many attempts it has made, and when the
// current window resets.
type rateBucket struct {
	count     int
	windowEnd time.Time
}

// newRateLimiter builds a rateLimiter allowing limit attempts per window.
func newRateLimiter(limit int, window time.Duration) *rateLimiter {
	return &rateLimiter{buckets: make(map[loginKey]*rateBucket), limit: limit, window: window}
}

// allow records one attempt for key at now and reports whether it fits
// inside the limit. A caller already over the limit still gets recorded —
// otherwise racing the window boundary with retries would grant a free pass
// the fixed window never intended.
func (l *rateLimiter) allow(key loginKey, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	for k, b := range l.buckets {
		if now.After(b.windowEnd) {
			delete(l.buckets, k)
		}
	}

	// Both buckets are bumped even when the first already refused: the
	// address bucket must see every attempt, or a caller could spend the
	// per-name budget under many names while the per-address count stayed
	// at zero.
	key.addrOnly = false
	nameOK := l.bump(key, now) <= l.limit
	addrOK := l.bump(loginKey{addr: key.addr, addrOnly: true}, now) <= l.limit*addrLimitMultiplier
	return nameOK && addrOK
}

// bump records one attempt for key and returns its count inside the current
// window. A key not seen yet is created unless the map is at
// maxLoginBuckets, in which case the attempt is reported as over any limit
// without being stored.
func (l *rateLimiter) bump(key loginKey, now time.Time) int {
	b, ok := l.buckets[key]
	if !ok {
		if len(l.buckets) >= maxLoginBuckets {
			return math.MaxInt
		}
		b = &rateBucket{windowEnd: now.Add(l.window)}
		l.buckets[key] = b
	}
	b.count++
	return b.count
}

// rateLimitLogin enforces the login attempt cap before the request reaches
// session lookup or the database: a request already over pace should not pay
// for either. Every other route is untouched (DESIGN §18: only the login
// route is limited).
//
// peekLoginName reads the submitted name out of the body before anything
// downstream does; this happens ahead of [Server.attachSession] and
// [Server.enforceCSRF] in [Server.Handler]'s chain, and well before
// [Server.handleLogin] itself, which per its own doc comment never touches
// r.Body. That ordering is unaffected here: peeking the name is not a
// database access, so it does not disturb [auth.Manager.Login]'s "password
// verified before the database is ever touched" guarantee (DESIGN §15).
func (s *Server) rateLimitLogin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != loginPath {
			next.ServeHTTP(w, r)
			return
		}
		peer := httpx.ResolvePeer(r, s.cfg.TrustProxy).Client()
		if !peer.IsValid() {
			next.ServeHTTP(w, r)
			return
		}
		name := peekLoginName(r)
		if utf8.RuneCountInString(name) > maxLoginNameRunes {
			name = string([]rune(name)[:maxLoginNameRunes])
		}
		key := loginKey{addr: peer, name: name}
		if !s.loginLimiter.allow(key, time.Now()) {
			httpx.Err(w, http.StatusTooManyRequests, codeRateLimited, "too many login attempts, try again later")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// peekLoginName best-effort extracts the "name" field from r's JSON body for
// [Server.rateLimitLogin]'s bucket key, then puts an equivalent reader back
// on r.Body so the real decode downstream ([auth.SharedPassword.Login]) sees
// the same bytes it always would have.
//
// It reads at most [auth.MaxLoginBody] bytes — the same ceiling the real
// decode enforces, so this never consumes more of the body than would have
// been read anyway. Any read or parse failure (oversized, truncated,
// malformed body) yields an empty name rather than aborting rate limiting
// altogether: the request still goes on to fail its real decode downstream
// exactly as before, but it must not dodge the limiter on its way there.
func peekLoginName(r *http.Request) string {
	if r.Body == nil {
		return ""
	}
	raw, err := io.ReadAll(io.LimitReader(r.Body, auth.MaxLoginBody))
	r.Body = io.NopCloser(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	var body struct {
		Name string `json:"name"`
	}
	_ = jsonx.Unmarshal(raw, &body) // best-effort; a parse failure just yields ""
	return body.Name
}
