package recipes

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"maps"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/domain"
)

// jwtHeader is a compact JWS header. A struct rather than a map keeps the
// two keys in the declared order — DESIGN §10's own example writes
// {"alg":...,"typ":"JWT"} — a map would happen to sort the same way today
// (both keys are already alphabetical), but explicit beats coincidental.
type jwtHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ"`
}

// MintJWT builds a compact JWS: base64url(header).base64url(payload).base64url(sig),
// no padding, HS256 over auth.SigningKey — or the literal empty third segment
// when auth.Alg is "none". Claims from the identity (sub, name, email, roles,
// org) are computed first, then extra is merged OVER them, then iat and exp
// are stamped last so a template cannot accidentally forge its own expiry.
//
// exp and iat are epoch SECONDS. DESIGN §10 is explicit about the unit and
// about why: a client scheduling setTimeout((exp-now)*1000) on a millisecond
// value overflows the 32-bit timer and refreshes in a tight loop.
func MintJWT(auth domain.AuthSettings, id domain.Identity, extra map[string]any, ttlSec int, now time.Time) (string, error) {
	ttl := ttlSec
	if ttl == 0 {
		ttl = auth.JWTTTLSec
	}
	if ttl == 0 {
		ttl = 3600
	}

	alg := auth.Alg
	if alg == "" {
		alg = "HS256"
	}

	headerJSON, err := jsonx.Marshal(jwtHeader{Alg: alg, Typ: "JWT"})
	if err != nil {
		return "", fmt.Errorf("recipes: marshal jwt header: %w", err)
	}

	claims := identityClaims(id)
	maps.Copy(claims, extra)
	// Stamped LAST, after the template is merged, so a claims template can
	// never forge its own expiry — the whole reason DESIGN §10 orders it
	// this way.
	claims["iat"] = now.Unix()
	claims["exp"] = now.Unix() + int64(ttl)

	// encoding/json sorts map[string]any keys before marshalling, which is
	// what makes two calls with the same inputs byte-identical — no
	// separate ordering step needed here.
	payloadJSON, err := jsonx.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("recipes: marshal jwt claims: %w", err)
	}

	signingInput := base64.RawURLEncoding.EncodeToString(headerJSON) + "." +
		base64.RawURLEncoding.EncodeToString(payloadJSON)

	if alg == "none" {
		// The three-segment shape is the format's requirement even with no
		// signature: a decoder handed a garbage third segment throws, but
		// an empty one is exactly what "none" means.
		return signingInput + ".", nil
	}

	mac := hmac.New(sha256.New, []byte(auth.SigningKey))
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig, nil
}

// identityClaims computes the identity-derived claims BEFORE the caller's
// template is merged over them: sub from identity.id — the ONE identity a
// token's sub and the profile endpoint's id must agree on, DESIGN §10 — then
// name, email, roles, and org when the identity carries them. A field the
// identity doesn't carry is left out rather than written as null, so a
// template that wants to add it isn't fighting a null placeholder.
func identityClaims(id domain.Identity) map[string]any {
	claims := map[string]any{}
	if id.ID != nil {
		claims["sub"] = id.ID
	}
	if id.Name != "" {
		claims["name"] = id.Name
	}
	if id.Email != "" {
		claims["email"] = id.Email
	}
	if id.Roles != nil {
		claims["roles"] = id.Roles
	}
	if id.Org != nil {
		org := map[string]any{}
		if id.Org.ID != nil {
			org["id"] = id.Org.ID
		}
		if id.Org.Name != "" {
			org["name"] = id.Org.Name
		}
		if id.Org.Type != "" {
			org["type"] = id.Org.Type
		}
		claims["org"] = org
	}
	return claims
}
