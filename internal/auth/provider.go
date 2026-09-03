package auth

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/config"
)

// Principal is who logged in — the only identity mocker's own auth has an
// opinion about. It is deliberately not named Identity: that name already
// belongs to [domain.Identity] (DESIGN §10), who the MOCK pretends the caller
// is, an unrelated concept this package never touches.
type Principal struct {
	UserID int64
	Name   string
}

// Challenge asks the caller to complete authentication elsewhere — the OIDC
// redirect seam DESIGN §15 reserves for a later phase. No Provider in P0
// returns one.
type Challenge struct {
	RedirectURL string
}

// Result is what a Provider hands back from Login: a completed [Principal],
// or a [Challenge] to redirect through, never both.
type Result struct {
	Principal *Principal
	Challenge *Challenge
}

// Provider authenticates a caller. [Manager] is written against this
// interface, never against a concrete provider, so DESIGN §15's migration
// path — SharedPassword today, LocalUsers or Oidc later, selected by
// MOCKER_AUTH_MODE — is a provider swap, not a Manager rewrite. Login takes
// the raw *http.Request rather than parsed fields precisely so a future OIDC
// provider can read its own callback (query params, form body, whatever its
// flow needs) here.
type Provider interface {
	Login(ctx context.Context, r *http.Request) (Result, error)
	Logout(ctx context.Context, sessionID string) error
	// Mode names the provider, for logs and cross-checking against the
	// configured [config.AuthMode].
	Mode() string
}

// MaxLoginBody bounds the login request body. A name and a password never
// come close to it; anything larger is either a mistake or an attempt to
// force a large allocation before the request is even parsed. Exported so
// the admin plane's login rate limiter can peek at the submitted name before
// this package's own decode runs (round-1 review finding 5) while reading at
// most as much as that real decode below ever would, instead of guessing at
// a second limit that could drift from this one.
const MaxLoginBody = 4 << 10

// SharedPassword implements [Provider] with the phase-1 scheme from DESIGN
// §15: one password, shared by everyone in the contour, plus a self-declared
// name. It proves nothing about who is actually typing — accepted only
// because the mock plane holds synthetic data inside a closed corporate
// contour (DESIGN §15).
type SharedPassword struct {
	hash string
}

var _ Provider = (*SharedPassword)(nil)

// NewSharedPassword builds a [SharedPassword] provider from the configured
// argon2id hash. [config.Load] already refuses to start without one when
// MOCKER_AUTH_MODE=shared-password, so cfg.SharedPasswordHash is never empty
// here.
func NewSharedPassword(cfg *config.Config) *SharedPassword {
	return &SharedPassword{hash: cfg.SharedPasswordHash}
}

// Mode reports the provider name.
func (s *SharedPassword) Mode() string { return string(config.AuthShared) }

// Logout is a no-op: SharedPassword keeps no per-session state of its own —
// only [Manager]'s session table does. The method exists so a future OIDC
// provider, which DOES need to revoke something upstream, has the same call
// site to plug into.
func (s *SharedPassword) Logout(ctx context.Context, sessionID string) error { return nil }

// Login reads {name, password} from the request body and checks password
// against the configured shared hash. It never touches the database — that
// is deliberate: get-or-create by name is [Manager]'s job, and DESIGN §15
// requires the password to be verified before any database access happens,
// which this ordering guarantees structurally rather than by convention.
func (s *SharedPassword) Login(ctx context.Context, r *http.Request) (Result, error) {
	var body struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	dec := jsonx.NewDecoder(io.LimitReader(r.Body, MaxLoginBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		return Result{}, fmt.Errorf("auth: decode login request: %w", err)
	}

	ok, err := VerifyPassword(s.hash, body.Password)
	if err != nil || !ok {
		// A malformed configured hash and a simply-wrong password take the
		// same path on purpose: nothing here should let a caller distinguish
		// "server misconfigured" from "you typed it wrong" (DESIGN §15).
		return Result{}, ErrBadPassword
	}
	return Result{Principal: &Principal{Name: body.Name}}, nil
}
