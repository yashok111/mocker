package recipes

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"math/big"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/domain"
)

// Env is everything a recipe needs that is not in the recipe itself.
type Env struct {
	Identity domain.Identity     // DESIGN §10's dictionary
	Auth     domain.AuthSettings // signing key, alg, ttl
	Now      time.Time           // the REAL clock, frozen once per response
	Seed     uint64              // gen.SeedScalar for this data path — enum picks from it
	Type     string              // the declared JSON type of the target node: "string"|"integer"|"number"|"boolean"|"array"|"object"|"" when unknown
	Format   string              // the declared format, "" when none
	// AssetBase is the absolute prefix an asset_url recipe writes a name
	// after — "<scheme>://<host>[:port][/w/<slug>]<reserved>/assets/" —
	// computed per REQUEST by the mock plane (A6 D7) from the two guarded
	// reads httpx.WorkspaceURL makes, and carried here through gen.Request
	// the way Ref is, never through gen.Options (a cached Generator would
	// otherwise serve the first request's scheme and host to every later
	// one). Empty where no request exists (population, a tick frame): the
	// recipe then DECLINES rather than emit a relative URL.
	AssetBase string
}

// Faker produces the value for one faker token. Declared here because the
// token vocabulary is here; implemented in internal/gen, which owns the
// producers. ok=false means "this token produced nothing" and the recipe
// declines, exactly as an uncoercible value does.
type Faker func(token string, seed uint64) (any, bool)

// Value evaluates r. ok=false means "decline — fall through to the next value
// source", which is what a recipe returns when its value cannot be coerced to
// Env.Type without lying about the schema, and also what it returns for every
// Deferred kind. err is reserved for a recipe that is structurally broken.
//
// faker is the seam a "faker" recipe reaches into internal/gen through
// (D6) — every other kind ignores it. It is a required parameter rather
// than a field on Env because Env is pure data built at one production site
// and fifteen test sites (see Env's own doc), so a func FIELD's zero value
// would be nil at every one of them; a nil faker here is instead a WIRING
// BUG for a faker recipe specifically (fakerValue's own doc explains why it
// errors rather than silently declining).
//
// ref is the second such seam (P3c D4): a "ref" recipe reaches into
// internal/resources' entity rows through it, and every other kind ignores
// it. Unlike faker, a nil ref is NOT a wiring bug — D15's refusal matrix
// lists three ordinary callers (confirm, reseed, preview) that pass one on
// purpose, because none of the three serves a real request for a resolver
// to read against; refValue treats a nil ref as an ordinary decline, per
// policy, exactly like a ref that resolved nothing.
//
// Value NEVER returns ErrOmit: omit is a Deferred kind, so Value declines and
// internal/gen's post-pass deletes the property. The sentinel lives here only
// so that the post-pass and this package name the same condition.
func (r Recipe) Value(env Env, dataPath string, faker Faker, ref Ref) (any, bool, error) {
	if r.Kind.Deferred() {
		// copy needs a sibling, omit needs the parent object, listSize needs
		// the array — none of that is reachable from a Recipe and an Env
		// alone. Never guess: a value invented here would be wrong twice
		// over, once for being a guess and once for hiding the real bug.
		return nil, false, nil
	}

	// Every case is a one-line delegation to a same-file helper (below), on
	// purpose: (Recipe).Value measured 19 against golangci-lint's
	// min-complexity of 20 before this slice (F32) — three new kinds added
	// as inline case bodies, the way the nine before them are written,
	// measured 22, over the line. Rather than spend a new lint exemption on
	// a function that can honestly stay under the line, every arm — old and
	// new alike — was pulled out here, which is the same helpers-not-an-
	// exemption call this slice's own three new kinds already had to make,
	// applied one arm further than first planned.
	switch r.Kind {
	case KindConst:
		return constValue(r, env)

	case KindEnum:
		return enumRecipeValue(r, env)

	case KindIdentity:
		return identityRecipeValue(r, env)

	case KindJWT:
		return jwtRecipeValue(r, env)

	case KindNow:
		return nowRecipeValue(r, env)

	case KindNull:
		// Forcing null is unconditional — DESIGN §9 does not gate it on
		// Env.Type, and Coerce's own nil branch would say the same thing
		// this shortcut does.
		return nil, true, nil

	case KindFaker:
		return fakerValue(r, env, faker)

	case KindTemplate:
		return templateValue(r, env, dataPath)

	case KindSequence:
		return sequenceValue(r, env, dataPath)

	case KindRef:
		return refValue(r, env, ref)

	case KindAssetURL:
		return assetURLValue(r, env)

	default:
		// Unreachable through a Set (Compile ran Validate on every recipe
		// it holds), but Value is also called directly in tests and could
		// be called on a Recipe nobody validated — decline loudly rather
		// than silently, matching Validate's own "unknown kind" rejection.
		return nil, false, fmt.Errorf("%w: unknown kind %q", ErrRecipe, r.Kind)
	}
}

// constValue evaluates a "const" recipe: r.Data decoded as-is, then Coerce'd
// to the declared type.
func constValue(r Recipe, env Env) (any, bool, error) {
	var v any
	if err := jsonx.Unmarshal(r.Data, &v); err != nil {
		return nil, false, fmt.Errorf("%w: const value: %w", ErrRecipe, err)
	}
	out, ok := Coerce(v, env.Type, env.Format)
	return out, ok, nil
}

// enumRecipeValue evaluates an "enum" recipe: one member of r.Data's JSON
// array, picked deterministically from env.Seed — the same seed layer as
// every other per-field value (SeedScalar, computed by the caller into
// Env.Seed), so the same field on the same request always lands on the same
// member.
func enumRecipeValue(r Recipe, env Env) (any, bool, error) {
	var members []jsonx.RawMessage
	if err := jsonx.Unmarshal(r.Data, &members); err != nil {
		return nil, false, fmt.Errorf("%w: enum value: %w", ErrRecipe, err)
	}
	if len(members) == 0 {
		return nil, false, fmt.Errorf("%w: enum value is empty", ErrRecipe)
	}
	idx := int(env.Seed % uint64(len(members)))
	var v any
	if err := jsonx.Unmarshal(members[idx], &v); err != nil {
		return nil, false, fmt.Errorf("%w: enum member: %w", ErrRecipe, err)
	}
	out, ok := Coerce(v, env.Type, env.Format)
	return out, ok, nil
}

// identityRecipeValue evaluates an "identity" recipe: r.Field projected out
// of env.Identity via IdentityField, then Coerce'd.
func identityRecipeValue(r Recipe, env Env) (any, bool, error) {
	v, ok := IdentityField(env.Identity, r.Field)
	if !ok {
		return nil, false, nil
	}
	out, ok := Coerce(v, env.Type, env.Format)
	return out, ok, nil
}

// jwtRecipeValue evaluates a "jwt" recipe: MintJWT over env.Identity/Auth,
// with r.Claims merged over the identity-derived claims.
func jwtRecipeValue(r Recipe, env Env) (any, bool, error) {
	var extra map[string]any
	if len(r.Claims) > 0 {
		if err := jsonx.Unmarshal(r.Claims, &extra); err != nil {
			return nil, false, fmt.Errorf("%w: jwt claims: %w", ErrRecipe, err)
		}
	}
	tok, err := MintJWT(env.Auth, env.Identity, extra, r.TTLSec, env.Now)
	if err != nil {
		return nil, false, fmt.Errorf("%w: mint jwt: %w", ErrRecipe, err)
	}
	out, ok := Coerce(tok, env.Type, env.Format)
	return out, ok, nil
}

// nowRecipeValue evaluates a "now" recipe: real clock plus offset, rendered
// per r.Format. format=="" defaults to epoch seconds — the type-aware half
// of that default ("iso" for a string target) lives here, where env.Type is
// visible, not in NowValue, which isn't given one.
func nowRecipeValue(r Recipe, env Env) (any, bool, error) {
	format := r.Format
	if format == "" {
		if env.Type == "string" {
			format = "iso"
		}
	}
	v, err := NowValue(r.Offset, format, env.Now)
	if err != nil {
		return nil, false, fmt.Errorf("%w: now: %w", ErrRecipe, err)
	}
	out, ok := Coerce(v, env.Type, env.Format)
	return out, ok, nil
}

// fakerValue evaluates a "faker" recipe (D6): faker looks up r.Field's
// producer in the registry internal/gen owns and, only when the token
// exists there, produces a value from env.Seed. A nil faker means the
// caller wired nothing here at all — a WIRING BUG, not "no faker
// configured" — so this errors rather than silently declining the way an
// unknown token does; recipeValue (internal/gen) turns any error into a
// decline anyway, so the only place the difference is visible is a test
// that supplies a real faker and can therefore tell the two apart.
func fakerValue(r Recipe, env Env, faker Faker) (any, bool, error) {
	if faker == nil {
		return nil, false, fmt.Errorf("%w: faker recipe called with a nil Faker", ErrRecipe)
	}
	v, ok := faker(r.Field, env.Seed)
	if !ok {
		return nil, false, nil
	}
	out, ok := Coerce(v, env.Type, env.Format)
	return out, ok, nil
}

// templateValue evaluates a "template" recipe (D7/D8): a template WITHOUT
// {{index}} always produces (D8(2)); one that contains it substitutes the
// position indexFromPath derives from dataPath, or DECLINES when dataPath
// carries no array index at all (D7(3)) — a detail body's top level, or any
// field outside an array, has no position to substitute.
func templateValue(r Recipe, env Env, dataPath string) (any, bool, error) {
	var tmpl string
	if err := jsonx.Unmarshal(r.Data, &tmpl); err != nil {
		return nil, false, fmt.Errorf("%w: template value must be a JSON string: %w", ErrRecipe, err)
	}
	hasIndex, err := validateTemplate(tmpl)
	if err != nil {
		return nil, false, fmt.Errorf("%w: template value: %w", ErrRecipe, err)
	}
	if hasIndex {
		idx, ok := indexFromPath(dataPath)
		if !ok {
			return nil, false, nil
		}
		tmpl = strings.ReplaceAll(tmpl, "{{index}}", strconv.FormatInt(idx, 10))
	}
	out, ok := Coerce(tmpl, env.Type, env.Format)
	return out, ok, nil
}

// sequenceValue evaluates a "sequence" recipe (D7): start + index*step,
// index taken from dataPath's own innermost array position. It DECLINES —
// exactly like an index-bearing template — wherever dataPath carries no
// "[N]" at all; there is no counter anywhere in this package or its caller
// (D7(4)), so this is the only place a sequence's position ever comes from.
func sequenceValue(r Recipe, env Env, dataPath string) (any, bool, error) {
	start, step, err := parseSequence(r.Data)
	if err != nil {
		return nil, false, fmt.Errorf("%w: sequence value: %w", ErrRecipe, err)
	}
	idx, ok := indexFromPath(dataPath)
	if !ok {
		return nil, false, nil
	}
	out, ok := Coerce(saturatingSequence(start, step, idx), env.Type, env.Format)
	return out, ok, nil
}

// refValue evaluates a "ref" recipe (P3c D3/D4/D7): it converts r.Data into
// a RefQuery and asks ref to resolve it. The DEFAULTING of an absent policy
// key to "generate" happens HERE, not in the resolver — Data's own "policy"
// key is DESIGN §9's default, and this is the one place in the chain that
// reads Data at all, so an empty string must never leave this function: the
// resolver marks the traffic only when Policy IS "generate" (D7), and an
// empty string would silently disable that mark for every ref written the
// documented default way.
//
// The RESOLVER owns every reason a reference does not resolve — the scalar
// check and the coercion included (D10) — so on ok=true refValue returns
// ref's own value as-is, with no second Coerce; on ok=false it applies the
// policy and nothing else, never second-guessing why the resolver declined.
//
// ref==nil is NOT the resolver declining; there is no resolver to ask at
// all (confirm, reseed, preview — D15's own refusal matrix). refValue
// treats that exactly like an ordinary decline, per policy, without calling
// through a nil func value.
func refValue(r Recipe, env Env, ref Ref) (any, bool, error) {
	family, property, policy, err := parseRef(r.Data)
	if err != nil {
		return nil, false, fmt.Errorf("%w: ref value: %w", ErrRecipe, err)
	}

	var (
		v  any
		ok bool
	)
	if ref != nil {
		v, ok = ref(RefQuery{
			Family:   family,
			Property: property,
			Policy:   policy,
			Type:     env.Type,
			Format:   env.Format,
			Seed:     env.Seed,
		})
	}

	if ok {
		return v, true, nil
	}
	if policy == "set-null" {
		return nil, true, nil
	}
	// policy == "generate" (the only other value Validate admits, D9): an
	// ordinary decline — fall through to the next value source.
	return nil, false, nil
}

// parseRef decodes a "ref" recipe's Data (P3c D3) into (family, property,
// policy), defaulting an absent policy key to "generate" — see refValue's
// own doc for why the default is minted HERE and never left as "". Called
// only from refValue: Validate's own shape check (validateRef, recipes.go)
// duplicates the parse rather than sharing it, the same defensive posture
// every other kind in this file already takes (parseSequence's own doc), so
// that Value staying reachable without a prior Validate (tests, or any
// caller that bypassed it) never trusts an unchecked Data blindly.
func parseRef(data jsonx.RawMessage) (family, property, policy string, err error) {
	var obj map[string]jsonx.RawMessage
	if uerr := jsonx.Unmarshal(data, &obj); uerr != nil {
		return "", "", "", fmt.Errorf("value must be a JSON object: %w", uerr)
	}
	if raw, present := obj["family"]; present {
		if uerr := jsonx.Unmarshal(raw, &family); uerr != nil {
			return "", "", "", fmt.Errorf("family must be a JSON string: %w", uerr)
		}
	}
	if raw, present := obj["property"]; present {
		if uerr := jsonx.Unmarshal(raw, &property); uerr != nil {
			return "", "", "", fmt.Errorf("property must be a JSON string: %w", uerr)
		}
	}
	if raw, present := obj["policy"]; present {
		if uerr := jsonx.Unmarshal(raw, &policy); uerr != nil {
			return "", "", "", fmt.Errorf("policy must be a JSON string: %w", uerr)
		}
	}
	if policy == "" {
		policy = "generate"
	}
	return family, property, policy, nil
}

// indexFromPath returns the integer inside dataPath's INNERMOST "[N]"
// segment — the position sequence and an {{index}}-bearing template derive
// from (D7(1)). "Innermost" means RIGHTMOST in the string: walkArray and
// generateItems both format a child's own index as the LAST "[N]" appended
// to the parent's own (possibly already-bracketed) path, so the right-most
// bracket is always the one nearest dataPath's own leaf — the position of
// THIS element, not of some ancestor array it happens to sit inside.
// ok=false when the path carries no array index at all — a detail body's
// top level, or any field outside an array — which is exactly when
// sequence/an index-bearing template must decline rather than guess
// (D7(3)). There is deliberately no field on Env for this (D7's own "why
// derived and not carried": a zero value that is also a legal position, and
// an Env built from the RAW walk path, which restarts at "" inside a
// generated list item and would carry no index there at all).
func indexFromPath(dataPath string) (int64, bool) {
	open := strings.LastIndexByte(dataPath, '[')
	if open < 0 {
		return 0, false
	}
	relClose := strings.IndexByte(dataPath[open:], ']')
	if relClose < 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(dataPath[open+1:open+relClose], 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// bigMinInt64/bigMaxInt64 back saturatingSequence's clamp.
var (
	bigMinInt64 = big.NewInt(math.MinInt64)
	bigMaxInt64 = big.NewInt(math.MaxInt64)
)

// saturatingSequence computes start + index*step in int64 with a
// SATURATING guard (D5/C3's overflow rule): a result that would overflow
// int64 is CLAMPED to math.MinInt64/MaxInt64, never wrapped, because a
// wrapped sequence is a silently wrong number and a clamped one is a
// visibly stuck one. big.Int does the arithmetic exactly — no manual
// overflow bookkeeping to get subtly wrong — since index and step here are
// always human-sized recipe/path values, never a hot-loop quantity.
func saturatingSequence(start, step, index int64) int64 {
	v := new(big.Int).Mul(big.NewInt(step), big.NewInt(index))
	v.Add(v, big.NewInt(start))
	switch {
	case v.Cmp(bigMinInt64) < 0:
		return math.MinInt64
	case v.Cmp(bigMaxInt64) > 0:
		return math.MaxInt64
	default:
		return v.Int64()
	}
}

// parseSequence decodes a sequence recipe's Data (D5) into (start, step). It
// is called from both Recipe.Validate (structure only, at ingress) and
// sequenceValue (the same check again — Value is also called directly by
// tests and by any caller that bypassed Validate, the same defensive
// posture every other kind in this file already takes). Numbers are decoded
// through jsonx with UseNumber, then Int64, so "1.5" is rejected as
// non-integer rather than silently truncated (C3).
func parseSequence(data jsonx.RawMessage) (start, step int64, err error) {
	dec := jsonx.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var obj map[string]any
	if derr := dec.Decode(&obj); derr != nil {
		return 0, 0, fmt.Errorf("value must be a JSON object: %w", derr)
	}
	if len(obj) != 2 {
		return 0, 0, fmt.Errorf("value must have exactly the keys \"start\" and \"step\", got %d", len(obj))
	}
	start, err = sequenceMember(obj, "start")
	if err != nil {
		return 0, 0, err
	}
	step, err = sequenceMember(obj, "step")
	if err != nil {
		return 0, 0, err
	}
	return start, step, nil
}

// sequenceMember reads one of sequence's two required members out of an
// already-length-checked (==2) object, so a present-but-wrong-named third
// key surfaces here as "start"/"step" missing — still a rejection, just not
// one that names the intruder by its own key.
func sequenceMember(obj map[string]any, key string) (int64, error) {
	raw, ok := obj[key]
	if !ok {
		return 0, fmt.Errorf("value is missing %q (or an unrecognized key stands in its place)", key)
	}
	n, ok := raw.(jsonx.Number)
	if !ok {
		return 0, fmt.Errorf("%q must be a JSON integer", key)
	}
	i, err := n.Int64()
	if err != nil {
		return 0, fmt.Errorf("%q must be a JSON integer: %w", key, err)
	}
	return i, nil
}

// validateTemplate scans tmpl for "{{…}}" placeholders (D8(1)): the closed
// set is exactly {{index}}, so any other placeholder — and an unmatched
// "{{" — is rejected rather than passed through as literal text, which
// would ship a typo'd placeholder ({{idex}}) straight into a response body.
// A bare "}}" with no opener is ordinary text and is never inspected here.
// hasIndex reports whether {{index}} appears; templateValue uses it to
// decide whether this recipe is position-only (D7(3)).
func validateTemplate(tmpl string) (hasIndex bool, err error) {
	rest := tmpl
	for {
		open := strings.Index(rest, "{{")
		if open < 0 {
			return hasIndex, nil
		}
		relClose := strings.Index(rest[open:], "}}")
		if relClose < 0 {
			return false, errors.New(`template has an unmatched "{{"`)
		}
		closeAt := open + relClose
		placeholder := rest[open+2 : closeAt]
		if placeholder != "index" {
			return false, fmt.Errorf("template has unknown placeholder %q", "{{"+placeholder+"}}")
		}
		hasIndex = true
		rest = rest[closeAt+2:]
	}
}

// identityFieldOK reports whether field is one of DESIGN §10's known
// identity field names, independent of whether any particular identity
// happens to carry a value for it. Validate uses this because a stored
// recipe outlives any one request and must be rejected by NAME, not by
// what today's workspace identity looks like.
func identityFieldOK(field string) bool {
	switch field {
	case "id", "name", "email", "roles", "org.id", "org.name", "org.type":
		return true
	default:
		return false
	}
}

// IdentityField projects one dotted field of the identity ("id", "org.id",
// "roles"). ok is false for a field the identity does not carry.
func IdentityField(id domain.Identity, field string) (any, bool) {
	switch field {
	case "id":
		return id.ID, id.ID != nil
	case "name":
		return id.Name, true
	case "email":
		return id.Email, true
	case "roles":
		return id.Roles, true
	case "org.id":
		if id.Org == nil {
			return nil, false
		}
		return id.Org.ID, id.Org.ID != nil
	case "org.name":
		if id.Org == nil {
			return nil, false
		}
		return id.Org.Name, true
	case "org.type":
		if id.Org == nil {
			return nil, false
		}
		return id.Org.Type, true
	default:
		return nil, false
	}
}

// parseOffset parses a signed offset like "+3600s", "-7d", "+90m" into a
// time.Duration. An empty offset means exactly now (zero duration). "d" has
// no calendar meaning here — a day is always exactly 24h, which is what a
// mock's synthetic clock needs, not what a real calendar would give you
// across a DST transition.
func parseOffset(offset string) (time.Duration, error) {
	if offset == "" {
		return 0, nil
	}
	sign := int64(1)
	i := 0
	switch offset[0] {
	case '+':
		i = 1
	case '-':
		sign = -1
		i = 1
	}
	if i >= len(offset) {
		return 0, fmt.Errorf("%w: offset %q has no digits", ErrRecipe, offset)
	}
	unit := offset[len(offset)-1]
	digits := offset[i : len(offset)-1]
	if digits == "" {
		return 0, fmt.Errorf("%w: offset %q has no digits", ErrRecipe, offset)
	}
	n, err := strconv.ParseInt(digits, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: offset %q: %w", ErrRecipe, offset, err)
	}

	var unitDur time.Duration
	switch unit {
	case 's':
		unitDur = time.Second
	case 'm':
		unitDur = time.Minute
	case 'h':
		unitDur = time.Hour
	case 'd':
		unitDur = 24 * time.Hour
	default:
		return 0, fmt.Errorf("%w: offset %q has an unknown unit %q", ErrRecipe, offset, string(unit))
	}
	return time.Duration(sign*n) * unitDur, nil
}

// NowValue evaluates a "now" recipe: offset parsed as a signed Go-ish
// duration with day support ("+3600s", "-7d", "+2h"), rendered per format.
//
// format=="" defaults to epoch seconds — the type-aware half of the default
// ("iso" for a string target) is Recipe.Value's job, since only it sees
// Env.Type; NowValue itself has no idea what the target field's declared
// type is.
func NowValue(offset, format string, now time.Time) (any, error) {
	d, err := parseOffset(offset)
	if err != nil {
		return nil, err
	}
	t := now.Add(d)
	switch format {
	case "", "epoch":
		return t.Unix(), nil
	case "epoch_ms":
		return t.UnixMilli(), nil
	case "iso":
		return t.UTC().Format(time.RFC3339), nil
	default:
		return nil, fmt.Errorf("%w: unknown now format %q", ErrRecipe, format)
	}
}

// asFloat reports whether v is some Go numeric kind (or a jsonx.Number) and,
// if so, its value as a float64. Coerce's "string" and "number" targets
// accept any numeric input this way, not just the one concrete type a
// caller happens to hand it — IdentityField.ID, for instance, is declared
// `any` and can be an int (DefaultSettings), a float64 (decoded JSON), or a
// non-numeric string (a UUID, which correctly fails this and falls through
// to the string branch instead).
func asFloat(v any) (float64, bool) {
	if n, ok := v.(jsonx.Number); ok {
		f, err := n.Float64()
		return f, err == nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return float64(rv.Int()), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return float64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		return rv.Float(), true
	default:
		return 0, false
	}
}

// asInt64 is asFloat's integer-only sibling: it additionally refuses a
// fractional float, because coercing 3.5 into an integer field would lie
// about the schema exactly the way coercing an array would.
func asInt64(v any) (int64, bool) {
	if n, ok := v.(jsonx.Number); ok {
		i, err := n.Int64()
		return i, err == nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), true
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(rv.Uint()), true
	case reflect.Float32, reflect.Float64:
		f := rv.Float()
		if f != math.Trunc(f) {
			return 0, false
		}
		return int64(f), true
	default:
		return 0, false
	}
}

// formatNumber renders a float64 the way it would look as a JSON literal —
// no exponent for ordinary magnitudes, no trailing zeros — which is what
// "its JSON form" means for a number coerced into a string field.
func formatNumber(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

// Coerce adapts v to the declared type. ok=false means the value would have
// to lie about the schema to fit — the caller then declines the recipe.
//
// format is accepted for symmetry with Env and is reserved for a future
// format-specific rule; today only the declared type constrains the result.
func Coerce(v any, jsonType, format string) (any, bool) { //nolint:gocyclo // one arm per (jsonType, format) pair
	if v == nil {
		// Forcing null is always legitimate — it is what the "null" kind
		// does unconditionally, and a const recipe whose literal value is
		// JSON null should behave the same way rather than declining on a
		// type it was never trying to lie about in the first place.
		return nil, true
	}

	switch jsonType {
	case "":
		// No declared type: nothing to lie about, so the value goes through
		// as-is.
		return v, true

	case "string":
		switch t := v.(type) {
		case string:
			return t, true
		case bool:
			return strconv.FormatBool(t), true
		}
		if f, ok := asFloat(v); ok {
			return formatNumber(f), true
		}
		return nil, false

	case "integer":
		if s, ok := v.(string); ok {
			i, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
			if err != nil {
				return nil, false
			}
			return i, true
		}
		if i, ok := asInt64(v); ok {
			return i, true
		}
		return nil, false

	case "number":
		if s, ok := v.(string); ok {
			f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
			if err != nil {
				return nil, false
			}
			return f, true
		}
		if f, ok := asFloat(v); ok {
			return f, true
		}
		return nil, false

	case "boolean":
		if b, ok := v.(bool); ok {
			return b, true
		}
		return nil, false

	case "array":
		rv := reflect.ValueOf(v)
		if rv.Kind() != reflect.Slice && rv.Kind() != reflect.Array {
			return nil, false
		}
		// Copied element-by-element rather than type-asserted to []any:
		// IdentityField's "roles" is a []string, not a []any, and it must
		// coerce into an array-typed field exactly as a JSON-sourced one
		// would.
		out := make([]any, rv.Len())
		for i := range out {
			out[i] = rv.Index(i).Interface()
		}
		return out, true

	case "object":
		if reflect.ValueOf(v).Kind() != reflect.Map {
			return nil, false
		}
		return v, true

	default:
		// An unrecognized declared type is not this function's problem to
		// flag — leafValue's own schema handling already decided what
		// "type" means for this node.
		return v, true
	}
}

// assetURLValue writes AssetBase + the (escaped) asset name — one name, or
// one of a list picked from the seed exactly as enumRecipeValue picks, so
// the same seed at the same path names the same file on every request.
// An empty AssetBase (no request behind this generation) declines: a
// relative URL is precisely the "valid but meaningless" value DESIGN §9
// lists, and falling through to the generator is honest where inventing a
// scheme would not be. A name that does not exist STILL produces the URL
// (§32.3) — nothing here can or should look it up.
func assetURLValue(r Recipe, env Env) (any, bool, error) {
	names, err := parseAssetNames(r.Data)
	if err != nil {
		return nil, false, fmt.Errorf("%w: asset_url value: %w", ErrRecipe, err)
	}
	if env.AssetBase == "" {
		return nil, false, nil
	}
	name := names[int(env.Seed%uint64(len(names)))] // G115 is not counted in this package (.golangci.yml): a modulo by the list's own length, the pick enum makes
	return env.AssetBase + url.PathEscape(name), true, nil
}
