// Package recipes implements DESIGN §9's recipe engine: a small per-field
// rule that overrides how internal/gen would otherwise fill one data path.
// A recipe outranks every other source of a value — a spec-declared example,
// a const, a default — because that is the whole point of an override: the
// operator is overruling the document.
//
// This package is a LEAF: it imports the stdlib, internal/jsonx and
// internal/domain, and never a package above itself — internal/gen,
// internal/openapi, internal/store or internal/specs. Three of those import
// this one, and a leaf that reached back up would make that a cycle. A
// "faker" recipe still reaches a producer that lives one layer up, in
// internal/gen, without breaking this rule: the seam is Faker, a typed func
// value internal/gen implements and hands in as Value's third argument
// (D6), never an import. A "ref" recipe reaches lower still, into
// internal/resources' entity rows, through the identical shape: Ref, a
// second typed func value, handed in as Value's FOURTH argument (P3c D4) —
// internal/resources itself imports internal/gen, so this is not merely the
// tidier of two options, it is the only shape available at either layer.
package recipes

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/yashok111/mocker/internal/jsonx"
)

// Kind names one of the recipe behaviours DESIGN §9 defines. faker,
// template and sequence joined the nine kinds this package already
// implemented; ref (P3c) is the tenth.
type Kind string

const (
	KindConst    Kind = "const"     // always this value
	KindEnum     Kind = "enum"      // one of a list, chosen from the seed
	KindCopy     Kind = "copy"      // the value of a sibling field ("$.id")
	KindIdentity Kind = "identity"  // a field of domain.Identity (DESIGN §10)
	KindJWT      Kind = "jwt"       // a structurally valid compact JWS
	KindNow      Kind = "now"       // real clock, with an offset
	KindNull     Kind = "null"      // force null
	KindOmit     Kind = "omit"      // drop the property entirely
	KindListSize Kind = "listSize"  // the length of the array at this path
	KindFaker    Kind = "faker"     // a token from the published vocabulary (D6)
	KindTemplate Kind = "template"  // a string, {{index}} substituted by array position (D7/D8)
	KindSequence Kind = "sequence"  // start + index*step, by array position (D7)
	KindRef      Kind = "ref"       // a property of an entity of a confirmed family (DESIGN §9)
	KindAssetURL Kind = "asset_url" // the absolute URL of an uploaded asset of this workspace (DESIGN §32.3, A6)
)

// fakerTokens is the published faker vocabulary (D6): the only twelve
// strings a "faker" recipe's field may name. Each exists where the tree
// already produces that kind of value from a SEED ALONE, and where an
// operator would plausibly ask for it by name — see internal/gen/faker.go's
// registry, which this list is the write-side half of. Order here is
// publication order (grouped by namespace), not significant to Validate or
// to FakerTokens' callers, which check SET membership.
var fakerTokens = []string{
	"person.fullName",
	"internet.email",
	"phone.number",
	"datetime.timestamp",
	"datetime.date",
	"lorem.title",
	"lorem.description",
	"status.value",
	"code.value",
	"color.hex",
	"slug.value",
	"string.uuid",
}

// FakerTokens returns the published faker vocabulary (D6), a fresh copy on
// every call so a caller cannot mutate the package's own canonical list.
// Exported because internal/gen's own test asserts SET EQUALITY against
// this exact list in both directions — a test that could not reach it would
// have to retype the twelve strings, and a thirteenth token added with no
// producer would then pass the very check that exists to catch that.
func FakerTokens() []string {
	out := make([]string, len(fakerTokens))
	copy(out, fakerTokens)
	return out
}

// fakerTokenSet backs isFakerToken with an O(1) membership test rather than
// a linear scan of fakerTokens on every "faker" recipe Validate sees.
var fakerTokenSet = func() map[string]struct{} {
	m := make(map[string]struct{}, len(fakerTokens))
	for _, t := range fakerTokens {
		m[t] = struct{}{}
	}
	return m
}()

func isFakerToken(token string) bool {
	_, ok := fakerTokenSet[token]
	return ok
}

// Recipe is ONE rule. Only the fields its Kind uses are populated; the rest
// stay zero, which is what keeps the stored JSON small and diffable.
//
// DEVIATION from the fixed field list: the raw-payload field is named Data,
// not Value. Go forbids a struct field and a method sharing an identifier,
// and this package also has a fixed method Recipe.Value(env, dataPath,
// faker, ref) that three other packages are written against verbatim — the
// method wins the conflict, so the field yields. The JSON wire tag stays
// "value": nothing that decodes a stored recipe from JSON needs to know
// this field's Go name, so storage and the wire format are unaffected.
type Recipe struct {
	Kind Kind `json:"kind"`
	// Data: const's literal, enum's JSON array, listSize's n / [lo,hi],
	// template's JSON string (D5/D8), sequence's {"start":…,"step":…}
	// object (D5), or ref's {"family":…,"property":…,"policy":…} object
	// (P3c D3) — never a new field, riding the existing wire shape.
	Data jsonx.RawMessage `json:"value,omitempty"`
	// Field: identity's "id"|"name"|"email"|"roles"|"org.id"|"org.name"|
	// "org.type", copy's "$.id"-style sibling reference, or faker's token
	// name from the published vocabulary (D6, FakerTokens).
	Field string `json:"field,omitempty"`
	// Offset: now's "+3600s", "-7d", "" for exactly now.
	Offset string `json:"offset,omitempty"`
	// Format: now's "epoch"|"epoch_ms"|"iso" (default epoch when the target
	// is numeric, iso when it is a string).
	Format string `json:"format,omitempty"`
	// Claims: jwt's claim template, merged OVER the identity-derived claims.
	Claims jsonx.RawMessage `json:"claims,omitempty"`
	// TTLSec: jwt's override of domain.AuthSettings.JWTTTLSec. Zero means
	// "use the workspace default", not "expire immediately".
	TTLSec int `json:"ttlSec,omitempty"`
}

// RefQuery is everything a "ref" recipe asks of the layer above. It is a
// struct rather than a parameter list because the resolver has to make
// EVERY decision a ref can fail on (P3c D4/D6/D15), and each of those needs
// a different field.
type RefQuery struct {
	Family   string // the canonical route family, from Data's own "family" key
	Property string // the bare property name, from Data's own "property" key
	Policy   string // "generate" or "set-null", read from Recipe.Data (D7)
	Type     string // Env.Type — the declared JSON type of the target node
	Format   string // Env.Format
	Seed     uint64 // Env.Seed, the per-data-path scalar
}

// Ref resolves one property of one entity of a confirmed resource family.
// It is internal/recipes' second typed-func seam, after Faker, and exists
// for the identical reason: the value lives behind a package this leaf may
// not import (P3c D4).
//
// ok=false means the reference did not resolve, for ANY reason. The
// resolver owns every one of them (P3c D6/D15), because it is the only
// thing in this chain that can also record the fact — see the traffic mark
// in D7. refValue does not second-guess it: on ok=false it applies the
// policy and nothing else.
type Ref func(q RefQuery) (any, bool)

var (
	// ErrOmit is the condition an "omit" recipe names. Value itself never
	// returns it — omit is Deferred, so Value just declines — but the
	// post-pass in internal/gen that actually deletes the property names
	// the same sentinel, so the two packages talk about one condition.
	ErrOmit = errors.New("recipes: omit this property")
	// ErrRecipe wraps every reason Validate (or Value, for a recipe that
	// slipped past Validate) rejects a recipe as structurally broken.
	ErrRecipe = errors.New("recipes: invalid recipe")
)

// Validate rejects a recipe whose Kind is unknown or whose required field for
// that Kind is missing or malformed. Called on every decode path: an override
// row is user input, and a malformed one must never panic the mock plane,
// which is reachable unauthenticated.
func (r Recipe) Validate() error { //nolint:gocyclo // one arm per recipe kind
	switch r.Kind {
	case KindConst:
		if len(r.Data) == 0 {
			return fmt.Errorf("%w: const recipe needs a value", ErrRecipe)
		}
		if !jsonx.Valid(r.Data) {
			return fmt.Errorf("%w: const recipe value is not valid JSON", ErrRecipe)
		}

	case KindEnum:
		var members []jsonx.RawMessage
		if err := jsonx.Unmarshal(r.Data, &members); err != nil {
			return fmt.Errorf("%w: enum recipe value must be a JSON array: %w", ErrRecipe, err)
		}
		if len(members) == 0 {
			return fmt.Errorf("%w: enum recipe needs a non-empty array", ErrRecipe)
		}

	case KindCopy:
		if r.Field == "" {
			return fmt.Errorf("%w: copy recipe needs a field", ErrRecipe)
		}

	case KindIdentity:
		if !identityFieldOK(r.Field) {
			return fmt.Errorf("%w: identity recipe has unknown field %q", ErrRecipe, r.Field)
		}

	case KindJWT:
		if len(r.Claims) > 0 {
			var obj map[string]jsonx.RawMessage
			if err := jsonx.Unmarshal(r.Claims, &obj); err != nil {
				return fmt.Errorf("%w: jwt recipe claims must be a JSON object: %w", ErrRecipe, err)
			}
		}
		if r.TTLSec < 0 {
			return fmt.Errorf("%w: jwt recipe ttlSec must not be negative, got %d", ErrRecipe, r.TTLSec)
		}

	case KindNow:
		switch r.Format {
		case "", "epoch", "epoch_ms", "iso":
			// ok
		default:
			return fmt.Errorf("%w: now recipe has unknown format %q", ErrRecipe, r.Format)
		}
		if r.Offset != "" {
			if _, err := parseOffset(r.Offset); err != nil {
				return fmt.Errorf("%w: now recipe offset %q: %w", ErrRecipe, r.Offset, err)
			}
		}

	case KindNull, KindOmit:
		// No required fields — the kind itself is the whole instruction.

	case KindListSize:
		lo, hi, err := parseListSize(r.Data)
		if err != nil {
			return fmt.Errorf("%w: listSize recipe: %w", ErrRecipe, err)
		}
		if lo < 0 || hi < lo {
			return fmt.Errorf("%w: listSize recipe has an inverted or negative range [%d,%d]", ErrRecipe, lo, hi)
		}

	case KindFaker:
		// Empty and unknown are the SAME rejection (C3/D5): isFakerToken("")
		// is false exactly like isFakerToken("bogus.token") is, so there is
		// no separate "field is empty" branch to keep in sync with the list.
		if !isFakerToken(r.Field) {
			return fmt.Errorf("%w: faker recipe has unknown or empty field %q", ErrRecipe, r.Field)
		}

	case KindTemplate:
		var tmpl string
		if err := jsonx.Unmarshal(r.Data, &tmpl); err != nil {
			return fmt.Errorf("%w: template recipe value must be a JSON string: %w", ErrRecipe, err)
		}
		if _, err := validateTemplate(tmpl); err != nil {
			return fmt.Errorf("%w: template recipe: %w", ErrRecipe, err)
		}

	case KindSequence:
		if _, _, err := parseSequence(r.Data); err != nil {
			return fmt.Errorf("%w: sequence recipe: %w", ErrRecipe, err)
		}

	case KindRef:
		if err := validateRef(r.Data); err != nil {
			return fmt.Errorf("%w: ref recipe: %w", ErrRecipe, err)
		}

	case KindAssetURL:
		if _, err := parseAssetNames(r.Data); err != nil {
			return fmt.Errorf("%w: asset_url recipe: %w", ErrRecipe, err)
		}
		// §32.3: policy words do not apply — there is nothing to resolve
		// at generation time — so a Field/Offset/Format on this kind is a
		// misread of another kind's shape and is refused by name rather
		// than ignored.
		if r.Field != "" || r.Offset != "" || r.Format != "" {
			return fmt.Errorf("%w: asset_url recipe takes only a value (a name or a list of names)", ErrRecipe)
		}

	default:
		return fmt.Errorf("%w: unknown kind %q", ErrRecipe, r.Kind)
	}
	return nil
}

// maxRefFamilyLen bounds a "ref" recipe's family string (P3c D9 rule 3).
// internal/recipes is a LEAF and may not import internal/livestate (D4), so
// this is its OWN const — copied rather than shared, citing the one
// existing bound on a route path in this tree as its precedent:
// internal/livestate/livestate.go:277#maxTargetPathLen. Nothing at import or
// in storage bounds a path at all (route_family is bare TEXT,
// internal/store/migrations/0001_init.sql:134#route_family).
const maxRefFamilyLen = 2 << 10 // 2 KiB

// refPolicies is the closed set of tokens Data.policy may hold once
// "restrict" (rejected separately, and by name — P3c D7/D9 rule 6) is
// excluded from it, so that a typo like "setnull" is rejected rather than
// silently validating and behaving as "generate" (D9 rule 7).
var refPolicies = map[string]struct{}{
	"":         {}, // absent — defaults to "generate" (D7)
	"generate": {},
	"set-null": {},
}

// validateRef checks a "ref" recipe's Data SHAPE only, never existence
// (P3c D9): internal/recipes has no database handle at all, and Compile
// runs Validate at a moment with nothing to look a family up in — a check
// made at write time would also be wrong on its own terms, since a family
// confirmed AFTER the recipe was saved would have failed a check that was
// correct when it ran. An unresolvable family is therefore a runtime
// outcome under the policy (D7), never a write-time refusal.
func validateRef(data jsonx.RawMessage) error {
	if len(data) == 0 {
		return errors.New("needs a value")
	}
	var obj map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(data, &obj); err != nil {
		return fmt.Errorf("value must be a JSON object: %w", err)
	}
	for key := range obj {
		switch key {
		case "family", "property", "policy":
			// ok
		default:
			return fmt.Errorf("value has an unrecognized key %q", key)
		}
	}

	var family string
	if raw, ok := obj["family"]; ok {
		if err := jsonx.Unmarshal(raw, &family); err != nil {
			return fmt.Errorf("family must be a JSON string: %w", err)
		}
	}
	if family == "" || family[0] != '/' {
		return fmt.Errorf("family must be non-empty and start with %q, got %q", "/", family)
	}
	if len(family) > maxRefFamilyLen {
		return fmt.Errorf("family is %d bytes, over the %d-byte limit", len(family), maxRefFamilyLen)
	}

	var property string
	if raw, ok := obj["property"]; ok {
		if err := jsonx.Unmarshal(raw, &property); err != nil {
			return fmt.Errorf("property must be a JSON string: %w", err)
		}
	}
	if property == "" {
		return errors.New("property must not be empty")
	}

	var policy string
	if raw, ok := obj["policy"]; ok {
		if err := jsonx.Unmarshal(raw, &policy); err != nil {
			return fmt.Errorf("policy must be a JSON string: %w", err)
		}
	}
	if policy == "restrict" {
		return errors.New(`policy "restrict" is not implemented`)
	}
	if _, ok := refPolicies[policy]; !ok {
		return fmt.Errorf("policy has an unknown value %q", policy)
	}
	return nil
}

// Deferred reports the kinds Value cannot evaluate alone — copy needs a
// sibling, omit needs the parent object, listSize needs the array. The
// caller (internal/gen) handles those three; everything else goes through
// Value.
func (k Kind) Deferred() bool {
	switch k {
	case KindCopy, KindOmit, KindListSize:
		return true
	default:
		return false
	}
}

// assetNameOK mirrors internal/assets.ValidName's rule — one path segment
// of [A-Za-z0-9._-], at most 128 characters, not "." or ".." — rather than
// importing it: internal/recipes is a LEAF (P3c D4) and internal/assets
// imports internal/store, which this package must never reach. The two are
// held together by a test in internal/assets that feeds both the same
// corpus (TestValidName_matchesRecipes).
var assetNameOK = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)

// parseAssetNames reads an asset_url recipe's Data: one JSON string, or a
// non-empty JSON array of strings, every element an acceptable asset name.
// Shape only, never existence (the same reasoning validateRef gives): a
// name uploaded AFTER the recipe was saved would have failed a check that
// was correct when it ran, and §32.3 says a missing name still yields the
// URL — the route answers 404 for it, which is what a real backend with a
// dangling reference does too.
func parseAssetNames(data jsonx.RawMessage) ([]string, error) {
	if len(data) == 0 {
		return nil, errors.New("needs a value: an asset name or a list of names")
	}
	var one string
	if err := jsonx.Unmarshal(data, &one); err == nil {
		if !assetNameOK.MatchString(one) || one == "." || one == ".." {
			return nil, fmt.Errorf("%q is not an asset name", one)
		}
		return []string{one}, nil
	}
	var many []string
	if err := jsonx.Unmarshal(data, &many); err != nil {
		return nil, fmt.Errorf("value must be a JSON string or an array of strings: %w", err)
	}
	if len(many) == 0 {
		return nil, errors.New("needs at least one asset name")
	}
	for _, name := range many {
		if !assetNameOK.MatchString(name) || name == "." || name == ".." {
			return nil, fmt.Errorf("%q is not an asset name", name)
		}
	}
	return many, nil
}
