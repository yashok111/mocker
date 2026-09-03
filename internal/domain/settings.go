// Package domain holds the types shared by both planes: workspace settings,
// slugs and the small rules that must not be re-implemented per package.
package domain

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/router"
)

// Settings is the per-workspace configuration blob stored in
// workspaces.settings (DESIGN §13). It is JSON rather than columns because the
// UI edits it as one object and every field is optional to a running mock.
type Settings struct {
	// Seed is the root of the layered generator seed (DESIGN §9). Changing it
	// reshuffles every generated value in the workspace at once.
	Seed int64 `json:"seed"`

	// BasePath is the ONE place where the spec's server prefix is glued onto
	// stored (relative) operation paths. Edits key off the relative path, so
	// changing this must never orphan them (DESIGN §7).
	BasePath string `json:"basePath"`

	// BasePathValues declares which values the {param} segments of BasePath may
	// take. Each element is one base TUPLE: the k raw values joined with "/", in
	// the order the parameters appear in BasePath. Empty when BasePath carries no
	// parameter, which is every workspace before P3h.
	BasePathValues []string `json:"basePathValues,omitempty"`

	ListSize int     `json:"listSize"`
	NullRate float64 `json:"nullRate"`

	// Envelope wraps every response body in {"<envelope>": ...} when set —
	// platform APIs that answer {"response": ...} are common enough to deserve
	// a switch instead of an edit per operation.
	Envelope *string `json:"envelope"`

	Identity Identity     `json:"identity"`
	Auth     AuthSettings `json:"auth"`
	CORS     CORSSettings `json:"cors"`

	ValidateRequests bool `json:"validateRequests"`
	DelayMs          int  `json:"delayMs"`

	// NotFoundBody replaces the default loud 404 body when set.
	NotFoundBody jsonx.RawMessage `json:"notFoundBody,omitempty"`
}

// Identity is who the mock believes the caller is (DESIGN §10). The `identity`
// recipe projects these fields into whatever shape an operation returns.
type Identity struct {
	// ID is deliberately untyped: real specs use both integers and UUIDs, and
	// coercing to one of them makes the other look wrong to the frontend.
	ID    any      `json:"id"`
	Name  string   `json:"name"`
	Email string   `json:"email"`
	Roles []string `json:"roles"`
	Org   *Org     `json:"org,omitempty"`
}

// Org is the organisation the identity belongs to. Screens that filter the
// user's orgs by type and role render blank without a matching one, so it is
// part of the default identity rather than an afterthought.
type Org struct {
	ID   any    `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// AuthSettings drives the minted token (DESIGN §10).
type AuthSettings struct {
	// JWTTTLSec is a TTL in SECONDS. The unit is load-bearing: clients that
	// schedule a refresh at (exp - now) * 1000 overflow the 32-bit timer on a
	// millisecond value and refresh in a tight loop.
	JWTTTLSec int    `json:"jwtTtlSec"`
	Alg       string `json:"alg"`
	// SigningKey is generated per workspace. The mock never verifies incoming
	// tokens; this only makes minted ones structurally believable.
	SigningKey string `json:"signingKey"`
	// RequireHeader makes the mock answer 401 without an Authorization header.
	// Off by default — a mock that 401s is a mock nobody can start against.
	RequireHeader bool `json:"requireHeader"`
}

// CORSSettings drives preflight and response headers (DESIGN §8).
type CORSSettings struct {
	// Mode is "reflect" (echo the Origin) or "off".
	Mode string `json:"mode"`
	// Credentials adds Access-Control-Allow-Credentials, which cookie-based
	// auth needs; with it, Allow-Origin must be the echoed origin, never "*".
	Credentials bool `json:"credentials"`
}

// CORS modes.
const (
	CORSReflect = "reflect"
	CORSOff     = "off"
)

// DefaultSettings returns the settings a fresh workspace starts with. The
// signing key is random per workspace, so two workspaces never mint identical
// tokens.
func DefaultSettings() Settings {
	return Settings{
		Seed:     1,
		BasePath: "",
		ListSize: 5,
		NullRate: 0,
		Envelope: nil,
		Identity: Identity{
			ID:    1,
			Name:  "Test Testov",
			Email: "test@example.com",
			Roles: []string{"user"},
			Org:   &Org{ID: 1, Name: "Test Org", Type: "school"},
		},
		Auth: AuthSettings{
			JWTTTLSec:     3600,
			Alg:           "HS256",
			SigningKey:    NewSigningKey(),
			RequireHeader: false,
		},
		CORS:             CORSSettings{Mode: CORSReflect, Credentials: true},
		ValidateRequests: false,
		DelayMs:          0,
	}
}

// NewSigningKey returns a fresh random key for minted tokens.
func NewSigningKey() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand.Read never returns an error on any supported platform;
		// if it somehow did, minting predictable tokens is worse than dying.
		panic(fmt.Sprintf("mocker: crypto/rand unavailable: %v", err))
	}
	return hex.EncodeToString(b)
}

// ParseSettings decodes stored settings on top of the defaults, so a row
// written by an older version gains new fields instead of zero values.
func ParseSettings(raw []byte) (Settings, error) {
	s := DefaultSettings()
	if len(raw) > 0 {
		if err := jsonx.Unmarshal(raw, &s); err != nil {
			return Settings{}, fmt.Errorf("parse workspace settings: %w", err)
		}
	}
	s.Normalize()
	return s, nil
}

// Normalize clamps values into ranges the rest of the code may assume.
func (s *Settings) Normalize() {
	s.BasePath = NormalizeBasePath(s.BasePath)

	if s.ListSize < 0 {
		s.ListSize = 0
	}
	if s.ListSize > 1000 {
		s.ListSize = 1000
	}
	if s.NullRate < 0 {
		s.NullRate = 0
	}
	if s.NullRate > 1 {
		s.NullRate = 1
	}
	if s.DelayMs < 0 {
		s.DelayMs = 0
	}
	if s.Auth.JWTTTLSec <= 0 {
		s.Auth.JWTTTLSec = 3600
	}
	if s.Auth.Alg == "" {
		s.Auth.Alg = "HS256"
	}
	if s.Auth.SigningKey == "" {
		s.Auth.SigningKey = NewSigningKey()
	}
	if s.CORS.Mode != CORSOff {
		s.CORS.Mode = CORSReflect
	}
	if s.Envelope != nil && *s.Envelope == "" {
		s.Envelope = nil
	}
}

// NormalizeBasePath returns a prefix that is either empty or starts with "/"
// and does not end with one, so gluing is a plain concatenation everywhere.
func NormalizeBasePath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || p == "/" {
		return ""
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return strings.TrimRight(p, "/")
}

// ValidateBasePath refuses a basePath whose {param} shape is not one
// router.compilePattern would compile the same way (an unbalanced brace, a
// brace that does not span a whole segment, or an empty parameter name), or
// that declares the same parameter name twice — two base parameters sharing
// a name would collapse to one entry in router.Match.Params (D4.3, half
// one). It reads the shape through router.BaseParamIndexes rather than
// scanning braces itself: a second local reader of "which segments are
// parameters" is exactly the defect the one-owner rule (D3.1, D4.3) exists
// to prevent, and router is a leaf package that cannot cycle back here. On
// the wire this answers 400 invalid_base_path (mapped by the admin
// handler, half two of D4.3, which additionally refuses a base parameter
// name colliding with a route parameter — this function does not have the
// bound spec to check that against).
func ValidateBasePath(basePath string) error {
	_, names, valid := router.BaseParamIndexes(NormalizeBasePath(basePath))
	if !valid {
		return fmt.Errorf("basePath: every { must be balanced and wrap a whole non-empty segment name")
	}
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if seen[name] {
			return fmt.Errorf("basePath: parameter %q is declared more than once", name)
		}
		seen[name] = true
	}
	return nil
}

// ValidateBasePathValues refuses a basePathValues list that is not a legal
// declared set for basePath's own parameter count k (D4.3, half one, the
// per-element rules). It assumes basePath's own shape already passed
// ValidateBasePath — call that first — and returns nil without opinion when
// the shape is invalid, so the two refusals never collide on one message.
// On the wire this answers 400 invalid_base_path_values.
//
// An element is k RAW values joined with "/", in parameter order; the
// component-count rule is what makes that join reversible by
// strings.Split, which is the split resources.EncodeScope itself performs
// when a declared element becomes a stored entities.base_scope_key (D4.1,
// D3.1) — this function does not call EncodeScope (internal/resources
// imports internal/domain already, so the reverse import would cycle), it
// only validates the shape that split depends on.
// MaxBasePathValues caps the declared set. Every declared value is one
// more row set a confirm and a reseed populate, so the set is bounded by
// the entity row cap in practice (1000 rows by default, and a confirm
// seeds at least one row per value); a ceiling here keeps a 10 MB PATCH
// from declaring a hundred thousand values that a confirm would then fan
// out over before any row cap could refuse it.
const MaxBasePathValues = 1000

func ValidateBasePathValues(basePath string, values []string) error {
	_, names, valid := router.BaseParamIndexes(NormalizeBasePath(basePath))
	if !valid {
		return nil
	}
	k := len(names)

	if k == 0 {
		if len(values) > 0 {
			return fmt.Errorf("basePathValues: must be empty because basePath declares no parameter")
		}
		return nil
	}

	if len(values) > MaxBasePathValues {
		return fmt.Errorf("basePathValues: at most %d elements may be declared, got %d", MaxBasePathValues, len(values))
	}
	seen := make(map[string]bool, len(values))
	for _, element := range values {
		parts := strings.Split(element, "/")
		if len(parts) != k {
			return fmt.Errorf("basePathValues: element %q must split into exactly %d component(s) separated by \"/\", has %d", element, k, len(parts))
		}
		for _, part := range parts {
			if part == "" {
				return fmt.Errorf("basePathValues: element %q has an empty component", element)
			}
		}
		if seen[element] {
			return fmt.Errorf("basePathValues: element %q is declared more than once", element)
		}
		seen[element] = true
	}
	return nil
}

// MarshalJSONStable encodes settings for storage. encoding/json orders struct
// fields by declaration, so the output is byte-stable and a bundle diff stays
// readable (DESIGN §17).
func (s Settings) MarshalJSONStable() ([]byte, error) {
	return jsonx.Marshal(s)
}
