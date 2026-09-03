// Package auth implements mocker's admin-plane identity (DESIGN §15):
// argon2id password hashing, session issuance and lookup, and the Provider
// seam a later phase drops OIDC into. The mock plane itself stays
// unauthenticated by design — nothing in this package runs on that path.
package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/store"
)

// cookieName is the one cookie the admin plane sets.
const cookieName = "mocker_session"

// sessionTTL bounds how long a session survives without a fresh login
// (DESIGN §15): long enough nobody re-authenticates mid-task, short enough
// that a leaked cookie does not stay valid forever.
const sessionTTL = 14 * 24 * time.Hour

// ErrBadName is returned when a Provider-supplied name fails the shape
// mocker requires. The name becomes the users.name UNIQUE key, so letting
// garbage through here mints a garbage identity permanently, not just a
// rejected request.
var ErrBadName = errors.New("auth: bad name")

// ErrNoSession is returned by [Manager.Lookup] when id names no live
// session — whether it never existed, was already logged out, or just
// expired. Callers cannot and must not be able to tell those apart.
var ErrNoSession = errors.New("auth: no session")

// User is a row of the users table (DESIGN §13).
type User struct {
	ID        int64
	Name      string
	Role      string
	CreatedAt time.Time
}

// Session is a row of the sessions table (DESIGN §13).
type Session struct {
	ID        string
	UserID    int64
	CSRFToken string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// Manager owns session lifecycle — minting, looking up, expiring — and the
// cookie that carries a session id between requests. It delegates credential
// checks to a [Provider] so this file never hard-codes "shared password".
type Manager struct {
	db       *store.DB
	cfg      *config.Config // carried for parity with the Provider seam; no P0 use yet
	provider Provider
}

// NewManager builds a Manager over db, backed by provider for credential
// checks.
func NewManager(db *store.DB, cfg *config.Config, provider Provider) *Manager {
	return &Manager{db: db, cfg: cfg, provider: provider}
}

// CookieName returns the session cookie's name.
func (m *Manager) CookieName() string { return cookieName }

// Login authenticates r through the configured Provider, then applies DESIGN
// §15's login policy — get-or-create a user by name — and mints a session.
//
// The get-or-create runs as one write transaction, INSERT ... ON CONFLICT DO
// NOTHING immediately followed by SELECT: two requests racing on a brand-new
// name must not both decide "no such user" and each insert one, which would
// trip the UNIQUE constraint on users.name for the loser and could otherwise
// mint a second user for one name, orphaning whatever the first one already
// owns.
func (m *Manager) Login(ctx context.Context, r *http.Request) (*Session, *User, error) {
	result, err := m.provider.Login(ctx, r)
	if err != nil {
		return nil, nil, err
	}
	if result.Principal == nil {
		// A Challenge with no Principal means the provider wants a redirect
		// (the OIDC seam) — this call signature has nowhere to put that.
		return nil, nil, fmt.Errorf("auth: provider %s returned a challenge, which Login cannot complete", m.provider.Mode())
	}
	name, err := validateName(result.Principal.Name)
	if err != nil {
		return nil, nil, err
	}

	sessionID, err := randomToken()
	if err != nil {
		return nil, nil, err
	}
	csrfToken, err := randomToken()
	if err != nil {
		return nil, nil, err
	}

	now := time.Now().UTC()
	sess := Session{
		ID:        sessionID,
		CSRFToken: csrfToken,
		CreatedAt: now,
		ExpiresAt: now.Add(sessionTTL),
	}
	var user User

	err = m.db.Write(ctx, func(tx *sql.Tx) error {
		u, err := ensureUserTx(ctx, tx, name, "member")
		if err != nil {
			return err
		}
		user = *u
		sess.UserID = user.ID

		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sessions (id, user_id, csrf_token, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
			sess.ID, sess.UserID, sess.CSRFToken, sess.CreatedAt.Unix(), sess.ExpiresAt.Unix(),
		); err != nil {
			return fmt.Errorf("insert session: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("auth: login: %w", err)
	}
	return &sess, &user, nil
}

// ensureUserTx is the get-or-create upsert [Manager.Login] and
// [Manager.EnsureUser] both run: INSERT ... ON CONFLICT(name) DO NOTHING
// immediately followed by SELECT, inside a caller-supplied write
// transaction. Factored into one function rather than each caller carrying
// its own copy of this SQL — a second spelling immediately drifts on the
// next migration nobody remembers to apply twice. See [Manager.Login]'s own
// doc comment for why the two-statement shape (rather than a single
// UPSERT ... RETURNING) matters: two callers racing on a brand-new name must
// both land on the SAME row, never each insert their own and trip the
// UNIQUE constraint on users.name for the loser.
func ensureUserTx(ctx context.Context, tx *sql.Tx, name, role string) (*User, error) {
	now := time.Now().UTC()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO users (name, role, created_at) VALUES (?, ?, ?) ON CONFLICT(name) DO NOTHING`,
		name, role, now.Unix(),
	); err != nil {
		return nil, fmt.Errorf("insert user: %w", err)
	}

	var user User
	var createdAt int64
	if err := tx.QueryRowContext(ctx,
		`SELECT id, name, role, created_at FROM users WHERE name = ?`, name,
	).Scan(&user.ID, &user.Name, &user.Role, &createdAt); err != nil {
		return nil, fmt.Errorf("select user: %w", err)
	}
	user.CreatedAt = time.Unix(createdAt, 0).UTC()
	return &user, nil
}

// EnsureUser returns the users row named name, creating it with role if it
// does not exist yet. It is the same upsert Login performs, exposed for the
// one caller that has no credentials to present: the MCP endpoint, whose
// identity is a key in the environment rather than a person at a keyboard.
//
// Unlike Login, name and role here come from the caller, not untyped
// provider/request input — [validateName]'s free-text rules (trim, control
// characters, a 64-rune cap meant for what someone typed at a login screen)
// do not apply. The MCP endpoint always calls this with the same two fixed
// literals ("mcp", "member"), so there is nothing here to validate that the
// UNIQUE constraint on users.name does not already enforce.
//
// Why a real row and not a synthetic &User{ID: 0}: workspaces.owner_id is
// INTEGER REFERENCES users(id) with foreign_keys=ON, so owner_id = 0 would
// fail that constraint on the very first workspace the MCP identity tries to
// create.
func (m *Manager) EnsureUser(ctx context.Context, name, role string) (*User, error) {
	var user *User
	err := m.db.Write(ctx, func(tx *sql.Tx) error {
		u, err := ensureUserTx(ctx, tx, name, role)
		if err != nil {
			return err
		}
		user = u
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("auth: ensure user: %w", err)
	}
	return user, nil
}

// Lookup resolves a session id to its session and user, rejecting — and
// deleting — an expired session.
func (m *Manager) Lookup(ctx context.Context, sessionID string) (*Session, *User, error) {
	if sessionID == "" {
		return nil, nil, ErrNoSession
	}

	var (
		sess                                    Session
		user                                    User
		createdAtSess, expiresAt, createdAtUser int64
	)
	err := m.db.R.QueryRowContext(ctx, `
		SELECT s.id, s.user_id, s.csrf_token, s.created_at, s.expires_at,
		       u.id, u.name, u.role, u.created_at
		FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id = ?`, sessionID).Scan(
		&sess.ID, &sess.UserID, &sess.CSRFToken, &createdAtSess, &expiresAt,
		&user.ID, &user.Name, &user.Role, &createdAtUser,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil, ErrNoSession
		}
		return nil, nil, fmt.Errorf("auth: lookup session: %w", err)
	}
	sess.CreatedAt = time.Unix(createdAtSess, 0).UTC()
	sess.ExpiresAt = time.Unix(expiresAt, 0).UTC()
	user.CreatedAt = time.Unix(createdAtUser, 0).UTC()

	if !time.Now().Before(sess.ExpiresAt) {
		if err := m.db.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
			return err
		}); err != nil {
			return nil, nil, fmt.Errorf("auth: purge expired session: %w", err)
		}
		return nil, nil, ErrNoSession
	}
	return &sess, &user, nil
}

// Logout invalidates sessionID: first at the Provider (a no-op for
// [SharedPassword], but the hook a future OIDC provider needs to revoke
// upstream state), then in the session table.
func (m *Manager) Logout(ctx context.Context, sessionID string) error {
	if err := m.provider.Logout(ctx, sessionID); err != nil {
		return fmt.Errorf("auth: provider logout: %w", err)
	}
	return m.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, sessionID)
		return err
	})
}

// PurgeExpired deletes every session past its expiry and reports how many
// rows it removed, for a periodic cleanup job.
func (m *Manager) PurgeExpired(ctx context.Context) (int64, error) {
	var n int64
	err := m.db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now().Unix())
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("auth: purge expired sessions: %w", err)
	}
	return n, nil
}

// SetCookie writes the session cookie. secure is passed in rather than read
// from config so the caller — which already knows the scheme it answered on
// — decides. There is deliberately no Domain attribute: setting one would
// hand this admin-plane cookie to every workspace subdomain (DESIGN §15).
func (m *Manager) SetCookie(w http.ResponseWriter, s *Session, secure bool) {
	// G124: Secure is a parameter, not a literal true, and that is
	// deliberate — MOCKER_DEV=1 serves the admin UI over plain http on
	// localhost, where a Secure cookie would never be stored. HttpOnly and
	// SameSite are unconditional below; the caller decides Secure from the
	// scheme it actually answered on.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec
		Name:     cookieName,
		Value:    s.ID,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// ClearCookie expires the session cookie. secure must match how it was set —
// a mismatched Secure attribute produces a cookie the browser silently
// refuses to overwrite, leaving the old one live.
func (m *Manager) ClearCookie(w http.ResponseWriter, secure bool) {
	// G124: Secure is a parameter, not a literal true, and that is
	// deliberate — MOCKER_DEV=1 serves the admin UI over plain http on
	// localhost, where a Secure cookie would never be stored. HttpOnly and
	// SameSite are unconditional below; the caller decides Secure from the
	// scheme it actually answered on.
	http.SetCookie(w, &http.Cookie{ //nolint:gosec
		Name:     cookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
	})
}

// validateName trims and checks a Provider-supplied name against the
// get-or-create key contract (DESIGN §15): 1..64 runes, no control
// characters. Cyrillic passes through untouched — logins are typed in
// Russian as often as English.
func validateName(name string) (string, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "", ErrBadName
	}
	n := 0
	for _, r := range trimmed {
		if unicode.IsControl(r) {
			return "", ErrBadName
		}
		n++
	}
	if n > 64 {
		return "", ErrBadName
	}
	return trimmed, nil
}

// randomToken returns a 256-bit value as unpadded base64url. Session ids and
// CSRF tokens each come from their own independent call, so neither can be
// derived from the other.
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: crypto/rand unavailable: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
