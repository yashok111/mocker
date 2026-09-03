package recipes

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/domain"
)

func testAuth() domain.AuthSettings {
	return domain.AuthSettings{JWTTTLSec: 3600, Alg: "HS256", SigningKey: "test-signing-key"}
}

func testIdentity() domain.Identity {
	return domain.Identity{
		ID:    "user-42",
		Name:  "Ada Lovelace",
		Email: "ada@example.com",
		Roles: []string{"admin"},
		Org:   &domain.Org{ID: 7, Name: "Acme", Type: "company"},
	}
}

func decodeSegment(t *testing.T, seg string) map[string]any {
	t.Helper()
	b, err := base64.RawURLEncoding.DecodeString(seg)
	if err != nil {
		t.Fatalf("base64url decode %q: %v", seg, err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("unmarshal segment %q: %v", b, err)
	}
	return m
}

func TestMintJWT_Structure(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	tok, err := MintJWT(testAuth(), testIdentity(), nil, 0, now)
	if err != nil {
		t.Fatalf("MintJWT: %v", err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("token has %d segments, want 3: %q", len(parts), tok)
	}

	header := decodeSegment(t, parts[0])
	if header["alg"] != "HS256" || header["typ"] != "JWT" {
		t.Fatalf("header = %v", header)
	}

	payload := decodeSegment(t, parts[1])
	if payload["sub"] != "user-42" {
		t.Fatalf("sub = %v, want user-42", payload["sub"])
	}
	if payload["email"] != "ada@example.com" {
		t.Fatalf("email = %v, want ada@example.com", payload["email"])
	}
	if payload["name"] != "Ada Lovelace" {
		t.Fatalf("name = %v", payload["name"])
	}

	org, ok := payload["org"].(map[string]any)
	if !ok {
		t.Fatalf("org = %v (%T), want an object", payload["org"], payload["org"])
	}
	if org["id"] != float64(7) || org["name"] != "Acme" || org["type"] != "company" {
		t.Fatalf("org = %v", org)
	}

	if parts[2] == "" {
		t.Fatalf("HS256 token has an empty signature segment")
	}
}

// TestMintJWT_SubMatchesIdentityID is DESIGN §10's non-negotiable rule: the
// token's sub and the id a /me endpoint would serve must come from ONE
// identity, or a frontend that compares them loops on the login screen.
func TestMintJWT_SubMatchesIdentityID(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	id := domain.Identity{ID: float64(99)}
	tok, err := MintJWT(testAuth(), id, nil, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	payload := decodeSegment(t, strings.Split(tok, ".")[1])

	meID, _ := IdentityField(id, "id")
	if payload["sub"] != meID {
		t.Fatalf("token sub = %v, /me id = %v — must be the SAME identity", payload["sub"], meID)
	}
}

func TestMintJWT_ExpiryArithmetic(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		ttlSec     int
		authTTLSec int
		wantTTL    int64
	}{
		{"explicit ttlSec wins over auth default", 120, 3600, 120},
		{"falls back to auth.JWTTTLSec when ttlSec is zero", 0, 900, 900},
		{"falls back to 3600 when both are zero", 0, 0, 3600},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			auth := testAuth()
			auth.JWTTTLSec = tc.authTTLSec
			tok, err := MintJWT(auth, testIdentity(), nil, tc.ttlSec, now)
			if err != nil {
				t.Fatal(err)
			}
			payload := decodeSegment(t, strings.Split(tok, ".")[1])

			iat, iok := payload["iat"].(float64)
			exp, eok := payload["exp"].(float64)
			if !iok || !eok {
				t.Fatalf("iat/exp missing or not numeric: %v", payload)
			}
			if int64(iat) != now.Unix() {
				t.Fatalf("iat = %v, want %v", iat, now.Unix())
			}
			if int64(exp)-int64(iat) != tc.wantTTL {
				t.Fatalf("exp-iat = %d, want %d", int64(exp)-int64(iat), tc.wantTTL)
			}
			// Pin the UNIT, not just the arithmetic: a millisecond value
			// here would overflow a 32-bit setTimeout and refresh in a
			// tight loop (DESIGN §10). exp in seconds for a mock-plausible
			// date is ~10 digits; in milliseconds it would be ~13.
			if exp < 1e9 || exp > 1e10 {
				t.Fatalf("exp = %v does not look like epoch SECONDS", exp)
			}
		})
	}
}

func TestMintJWT_AlgNone(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	auth := testAuth()
	auth.Alg = "none"
	tok, err := MintJWT(auth, testIdentity(), nil, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")
	if len(parts) != 3 {
		t.Fatalf("alg none: %d segments, want 3 (header.payload. with an empty sig)", len(parts))
	}
	if parts[2] != "" {
		t.Fatalf("alg none: signature segment = %q, want empty", parts[2])
	}
	header := decodeSegment(t, parts[0])
	if header["alg"] != "none" {
		t.Fatalf("header alg = %v, want none", header["alg"])
	}
}

func TestMintJWT_Deterministic(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	extra := map[string]any{"scope": "admin"}
	a, err1 := MintJWT(testAuth(), testIdentity(), extra, 0, now)
	b, err2 := MintJWT(testAuth(), testIdentity(), extra, 0, now)
	if err1 != nil || err2 != nil {
		t.Fatalf("errors: %v, %v", err1, err2)
	}
	if a != b {
		t.Fatalf("MintJWT is not deterministic for identical inputs:\n%s\n%s", a, b)
	}
}

func TestMintJWT_ClaimsCannotForgeExpiry(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	tok, err := MintJWT(testAuth(), testIdentity(), map[string]any{
		"exp":  1,
		"iat":  1,
		"role": "override-me",
	}, 60, now)
	if err != nil {
		t.Fatal(err)
	}
	payload := decodeSegment(t, strings.Split(tok, ".")[1])

	exp, _ := payload["exp"].(float64)
	if int64(exp) != now.Unix()+60 {
		t.Fatalf("exp was forgeable via the claims template: got %v, want %d", exp, now.Unix()+60)
	}
	iat, _ := payload["iat"].(float64)
	if int64(iat) != now.Unix() {
		t.Fatalf("iat was forgeable via the claims template: got %v, want %d", iat, now.Unix())
	}
	// The rest of the template DOES merge — only iat/exp are protected.
	if payload["role"] != "override-me" {
		t.Fatalf("claims template's other fields did not merge: %v", payload)
	}
}

func TestMintJWT_ClaimsOverrideIdentityDerivedFields(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	tok, err := MintJWT(testAuth(), testIdentity(), map[string]any{"name": "Override"}, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	payload := decodeSegment(t, strings.Split(tok, ".")[1])
	if payload["name"] != "Override" {
		t.Fatalf("name = %v, want the claims template to win", payload["name"])
	}
	// sub is untouched by the template in this case — it comes from the
	// identity either way, proving "merged OVER" is additive, not a reset.
	if payload["sub"] != "user-42" {
		t.Fatalf("sub = %v, want user-42", payload["sub"])
	}
}

func TestMintJWT_SignatureVerifies(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	auth := testAuth()
	tok, err := MintJWT(auth, testIdentity(), nil, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(tok, ".")

	mac := hmac.New(sha256.New, []byte(auth.SigningKey))
	mac.Write([]byte(parts[0] + "." + parts[1]))
	want := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	if parts[2] != want {
		t.Fatalf("signature = %q, want %q", parts[2], want)
	}
}

func TestMintJWT_NoPadding(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	tok, err := MintJWT(testAuth(), testIdentity(), map[string]any{"scope": "x"}, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(tok, "=") {
		t.Fatalf("token contains base64 padding: %q", tok)
	}
}

// topLevelObjectKeys walks raw with a json.Decoder, tracking nesting depth,
// and returns the keys of the OUTERMOST object in the order they appear on
// the wire. A flat regex scan over `"key":` would also catch a nested
// object's own (independently sorted) keys and splice them into the
// sequence at the wrong point — org's "id"/"name"/"type" interleave with
// the top-level keys around wherever "org" itself sorts — so this walks the
// token stream and only records keys while depth==1.
func topLevelObjectKeys(t *testing.T, raw []byte) []string {
	t.Helper()
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	depth := 0
	// wantKey is true when the next token at depth 1 is a key rather than a
	// value: true right after '{' opens the outer object, and again right
	// after we consume a scalar value at depth 1 (a value that isn't
	// itself an object/array, which instead re-opens as a nested Delim).
	wantKey := false
	var keys []string
	for {
		tok, err := dec.Token()
		if err != nil {
			break // io.EOF: normal end of the token stream
		}
		switch v := tok.(type) {
		case json.Delim:
			switch v {
			case '{', '[':
				depth++
				wantKey = depth == 1 && v == '{'
			case '}', ']':
				depth--
				wantKey = depth == 1
			}
		default:
			if depth == 1 {
				if wantKey {
					keys = append(keys, fmt.Sprint(v))
				}
				wantKey = !wantKey
			}
		}
	}
	return keys
}

// TestMintJWT_ClaimsKeyOrderIsSorted asserts the documented property
// directly on the raw bytes rather than assuming it: "JSON claim
// marshalling must therefore be key-ordered (encoding/json sorts map keys
// already — assert it rather than assume it)."
func TestMintJWT_ClaimsKeyOrderIsSorted(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	tok, err := MintJWT(testAuth(), testIdentity(), map[string]any{
		"scope": "admin",
		"aaa":   1,
		"zzz":   2,
	}, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.Split(tok, ".")[1])
	if err != nil {
		t.Fatal(err)
	}

	keys := topLevelObjectKeys(t, raw)
	if len(keys) == 0 {
		t.Fatalf("found no top-level keys in %s", raw)
	}
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	for i := range keys {
		if keys[i] != sorted[i] {
			t.Fatalf("top-level payload keys not sorted: %v", keys)
		}
	}
}

func TestMintJWT_DefaultAlgIsHS256(t *testing.T) {
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	auth := testAuth()
	auth.Alg = ""
	tok, err := MintJWT(auth, testIdentity(), nil, 0, now)
	if err != nil {
		t.Fatal(err)
	}
	header := decodeSegment(t, strings.Split(tok, ".")[0])
	if header["alg"] != "HS256" {
		t.Fatalf("header alg = %v, want HS256 default", header["alg"])
	}
}
