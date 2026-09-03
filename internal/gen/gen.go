// Package gen is P1b's response generator: it turns a resolved OpenAPI
// response schema plus a request's identity (method, path, path params,
// status) into a deterministic, schema-conformant JSON body and header
// set (DESIGN §9 "Генератор данных"). It is a LEAF — it imports
// internal/openapi, internal/domain, internal/jsonx and internal/recipes
// (P1c's recipe engine, itself a leaf over internal/domain, internal/jsonx
// and the stdlib only — see its own package doc) alongside the standard
// library, so every later phase (mockplane's serving path, the admin API,
// acceptance tests) can still depend on it without pulling in the rest of
// the module or introducing a cycle. It does NOT import internal/jsonpatch
// (P2e): a schema patch is parsed and applied once per runtime, entirely in
// internal/mockplane, and reaches this package only as an already-patched
// map — see Request.PatchedSchema.
//
// Three seams are declared here and in schema.go but implemented by other
// agents in this same package: composeValue (allOf/oneOf/anyOf/
// discriminator), leafValue (example/const/default/enum/format/field-name
// realism), and listBody (the list contract: pagination, stable total,
// item identity, list-row == detail-card). Each ships with a trivial,
// single-purpose stub — stub_compose.go, stub_values.go, stub_list.go —
// that keeps the package building and its own tests green until the real
// implementation lands and deletes the stub.
package gen

import (
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/recipes"
)

// Package defaults — used whenever the corresponding Options field is left
// at its zero value (see Options' own doc comment: zero means "use the
// default", not "use zero").
const (
	// DefaultListSize is the array length used when a schema gives no
	// minItems/maxItems hint and the caller leaves Options.ListSize unset.
	// 20 matches a common REST pagination page size — independent, on
	// purpose, of domain.Settings' own default of 5: that is a per-
	// workspace admin choice about how busy a FIRST screen looks, this is
	// the generator's own fallback for callers that never set ListSize at
	// all (e.g. a raw test).
	DefaultListSize = 20

	// DefaultMaxDepth is the recursion budget for self-referencing or
	// deeply nested schemas. 6 is deep enough for realistic API bodies
	// (object -> object -> array -> object is already 4 hops) while
	// keeping a self-referencing schema's worst case bounded; combined
	// with the package-internal node-visit ceiling (schema.go's
	// maxWalkNodes), termination is guaranteed regardless of shape.
	DefaultMaxDepth = 6

	// DefaultMaxBytes mirrors config.Config's own MOCKER_MAX_RESPONSE
	// default of "4mb" (4*1<<20 bytes) — a caller that forgets to wire
	// Options.MaxBytes through gets exactly the ceiling the server would
	// have enforced on the wire anyway, not an unbounded one.
	DefaultMaxBytes = 4 << 20
)

// Errors Body/Headers can return. NONE is fatal to a request — the serving
// path (a later phase) logs and answers something honest rather than
// 500ing on a bad schema.
var (
	// ErrUnsatisfiable means the schema's own constraints cannot be met by
	// any value — e.g. a plain "minimum": 10, "maximum": 5, or (once the
	// Values agent's allOf merge lands) an allOf narrowing that pushes the
	// effective minimum above the effective maximum. DESIGN §9: the
	// operation is marked with a schema error, visible in the UI — this
	// package only reports it, marking it is the caller's job.
	ErrUnsatisfiable = errors.New("gen: schema constraints cannot be satisfied")
	// ErrNoSchema means a schema pointer resolves to nothing usable: the
	// pointer doesn't exist, its $ref chain is broken/cyclic/budget-
	// exhausted, or what it resolves to isn't a schema object at all.
	ErrNoSchema = errors.New("gen: schema pointer resolves to nothing")
)

// Options is everything the generator needs that is NOT per-request. It is
// primitives, not domain.Settings, so gen stays a leaf.
//
// ZERO VALUES ARE DEFAULTS, exactly as openapi.NewResolver treats a
// non-positive budget: a caller that leaves a field at 0 gets the package
// default, never a budget of zero. This matters because nothing in
// config.Config or domain.Settings supplies MaxDepth at all — the caller
// has nothing to fill it from, and a literal 0 would omit every optional
// nested property in the whole document. Seed and NullRate are the two
// deliberate exceptions: 0 is a legal seed, and NullRate: 0 ("never null")
// IS the correct default, not a placeholder for one.
type Options struct {
	Seed     int64            // domain.Settings.Seed; 0 is a legal seed, not a default
	ListSize int              // domain.Settings.ListSize; <=0 -> DefaultListSize
	NullRate float64          // domain.Settings.NullRate; 0 means "never null", which IS the default
	MaxBytes int64            // config.MaxResponse; <=0 -> DefaultMaxBytes. Generation stops growing the value past it
	MaxDepth int              // <=0 -> DefaultMaxDepth. Recursion budget for self-referencing schemas
	Now      func() time.Time // injected clock; nil means time.Now

	// Identity and Auth are NEW in P1c: per-workspace, stable for the life
	// of a Generator — exactly like every other Options field, and exactly
	// UNLIKE Request.Recipes below, which is per (operation, status) and
	// therefore lives on Request instead. An identity/jwt recipe reads
	// these; with neither ever populated (every existing caller before this
	// phase), IdentityField/MintJWT just see a zero domain.Identity/
	// domain.AuthSettings — never reached anyway, since recipeValue only
	// consults them when a *recipes.Set is actually bound (HARD RULE 6).
	Identity domain.Identity
	Auth     domain.AuthSettings
}

func (o Options) effListSize() int {
	if o.ListSize <= 0 {
		return DefaultListSize
	}
	return o.ListSize
}

func (o Options) effMaxDepth() int {
	if o.MaxDepth <= 0 {
		return DefaultMaxDepth
	}
	return o.MaxDepth
}

func (o Options) effMaxBytes() int64 {
	if o.MaxBytes <= 0 {
		return DefaultMaxBytes
	}
	return o.MaxBytes
}

func (o Options) clock() func() time.Time {
	if o.Now != nil {
		return o.Now
	}
	return time.Now
}

// Request is what the generator knows about the call being answered.
type Request struct {
	Method        string            // upper case
	CanonicalPath string            // RELATIVE, {param} segments already replaced by {} — router.Route.CanonicalPath
	PathParams    map[string]string // by parameter name, as captured by router.Match
	Query         url.Values
	Status        int // the HTTP status actually being sent

	// ListFamily is the canonical path of the sibling LIST route when this
	// request is a detail route (".../{}" whose prefix is itself a route),
	// otherwise "". The caller computes it — gen must not import router.
	ListFamily string

	// IDParam is the name (a key of PathParams) of the path parameter that
	// identifies THIS detail route's own resource — meaningful only when
	// ListFamily != "". The caller (mockplane) computes it structurally,
	// from the router's real, ORDERED path pattern (the segment
	// router.CanonicalPath's trailing "{}" stands for), the same source
	// ListFamily itself is derived from. gen must not import router, so it
	// cannot recover that ordering from PathParams alone: PathParams is an
	// unordered map, and for a nested route with two or more "id-shaped"
	// parameter names (e.g. GET /tenants/{tenantId}/cohorts/{cohortId},
	// where BOTH names end in "Id") there is no name-based heuristic that
	// reliably picks "cohortId" over "tenantId" — guessing wrong
	// breaks the list-row == detail-card identity contract silently.
	// "" falls back to identifyIDParam's own heuristic, kept only for
	// callers (and tests) that construct a Request directly without router
	// knowledge; every real request mockplane serves sets this field.
	IDParam string

	// Recipes is NEW in P1c: the compiled bindings for THIS variant (one
	// response, i.e. one operation+status — internal/overrides resolves the
	// op_overrides row and compiles it before building a Request, gen must
	// not import that far up the stack). nil means "no recipes bound to
	// this variant" and MUST reproduce P1b's output byte for byte — every
	// seam this field touches (recipeValue, the contentExample gate in
	// Body, arrayLength, listSizeOrDefault, the copy/omit post-pass) checks
	// for nil first and is a documented no-op when it is, which is what
	// makes that invariant an acceptance assertion rather than a hope (see
	// golden_p1b_test.go).
	Recipes *recipes.Set

	// Ref is the per-request resolver for a "ref" recipe (P3c, D5/D6). It is
	// NOT on Options: a *Generator is built once and cached under
	// (workspace_id, revision) (routeCache), so a closure captured on
	// Options would outlive the request that built it and answer every
	// later request against that runtime with the FIRST caller's context —
	// a use-after-cancel, not merely stale data. serveGenerated constructs
	// the live closure from rt.resources/p.entities/r.Context() and hands
	// it to assembleResponse, which only receives and assigns it here;
	// Preview and the two population paths (confirm, reset-data reseed)
	// pass nil. A nil Ref is not a special case for recipeValue/refValue —
	// it is handled exactly like a resolver that declined, so the recipe's
	// own policy (generate/set-null) still governs.
	Ref recipes.Ref

	// AssetBase is the absolute prefix an asset_url recipe writes a name
	// after (A6, DESIGN §32.3): per request, for the reason Ref's own
	// comment gives — a Generator is cached under (workspace_id, revision)
	// and an Options-borne base would serve the first request's scheme and
	// host to every later one. "" where no request exists (population, a
	// tick frame, a caller that constructs a Request directly); the recipe
	// then declines.
	AssetBase string

	// PatchedSchema is the response root with this variant's schemaPatch
	// already applied — built once per runtime, never per request. nil means
	// "no patch, or it did not apply"; Body then resolves v.SchemaPtr as
	// before.
	PatchedSchema map[string]any
}

// ResponseVariant is one row of operation_responses joined with the two
// fields of its operation the serving path needs. specs.Repo produces
// these; mockplane consumes them. Declared HERE so mockplane never imports
// specs.
type ResponseVariant struct {
	OpRowID    int64
	Selector   string // "200" | "2XX" | "default", exactly as in the spec
	HTTPStatus int    // what is actually sent: 2XX -> 200, default -> 200
	IsDefault  bool
	MediaType  string // "" when the response declares no content
	SchemaPtr  string // JSON pointer to the RESOLVED schema node, "" when there is none
	OpPointer  string // operations.pointer, e.g. "#/paths/~1api~1v1~1x/get"
	Degraded   bool   // operations.parse_error != nil -> answer an empty 200 (DESIGN §7)
}

// Generator builds response bodies and headers over one document. A
// Generator is immutable after construction and safe for concurrent use:
// every request-scoped value lives on a freshly built *walker (see
// newWalker), never on the Generator itself, and *openapi.Resolver is
// already concurrency-safe. mergeCache is the one field that IS shared
// mutable state across requests — deliberately: it memoizes compose.go's
// mergeAllOf, whose result depends only on the (immutable) document, never
// on any one request, so recomputing it per-request is pure waste (P1b
// round-1 review, finding 3). It uses sync.Map internally, matching
// *openapi.Resolver's own concurrency-safe memo.
type Generator struct {
	res        *openapi.Resolver
	opts       Options
	mergeCache *mergeCache
}

// New builds a generator over one document.
func New(res *openapi.Resolver, opts Options) *Generator {
	return &Generator{res: res, opts: opts, mergeCache: &mergeCache{}}
}

// newWalker builds the per-call state Body/Headers thread through the
// schema walk: the seed rooted for THIS request (SeedList — DESIGN §9),
// the frozen clock (DESIGN §9 "Время": ordinary fields derive from the
// seed, deadline-shaped fields derive from THIS now — a frozen exp would
// make the installation stop working the next day; that split by field
// name is the Values agent's leafValue to make, but the clock it splits on
// is captured exactly once, here, per call), and a fresh budget. Nothing
// here is retained past the call that creates it.
func (g *Generator) newWalker(req Request) *walker {
	return &walker{
		res:        g.res,
		opts:       g.opts,
		req:        req,
		seedList:   SeedList(g.opts, req),
		now:        g.opts.clock()(),
		budget:     &walkBudget{remaining: g.opts.effMaxBytes()},
		mergeCache: g.mergeCache,
	}
}

// Body generates the response body for one request. It takes the whole
// variant, not just v.SchemaPtr, because DESIGN §9's value-source priority
// starts ABOVE the schema: "example/examples (media type -> named -> схема
// -> const -> default)", and the media-type-level example lives on the
// content object — v.OpPointer + "/responses/" + escaped(v.Selector) +
// "/content/" + escaped(v.MediaType) — which a bare schema pointer cannot
// reach. Passing only the pointer would silently drop the first level of
// the priority order.
//
// Returns (nil, nil) — not an error — when v.SchemaPtr is "" (no declared
// content). That covers MOST 204s but NOT all: measured, 5 of this
// document's 20 chosen-204 variants declare application/json content with
// a schema. That is a spec bug and it is real, so Body WILL return a body
// for them — the 204/205 suppression in the serving layer is what
// guarantees the wire stays empty. Do not "simplify away" either half on
// the strength of the other.
//
// Options.MaxBytes is a ceiling generation actively PRICES ITS WAY TOWARD,
// not merely a number checked after the fact. OPTIONAL content (a property
// the schema does not require, an array item beyond minItems) stops growing
// well before the budget is gone (schema.go's walkObject/walkArray share it
// fairly across sibling optional properties too, rather than letting the
// first one processed spend all of it). REQUIRED content — a required
// property, an item within minItems, a oneOf/allOf branch a required field
// forces — is never simply generated in full regardless of cost: before
// committing to the real, possibly-recursive generation of a required
// subtree, schema.go prices that subtree's own SMALLEST LEGAL
// representation (hardCeilingValue — allOf-merged, unlike the depth/node
// ceiling's own cheaper one-branch approximation, so a fallback can never
// omit a field only a LATER allOf branch requires; and, unlike THAT
// ceiling's own one-level depthLimitValue, recursing into a required
// object's OWN required properties in turn — depth permitting — rather
// than bottoming out at one shallow pass, since these hard-ceiling checks
// run on perfectly ordinary, shallow properties, not at the actual depth
// ceiling where a full recursive price would just reintroduce the
// unbounded recursion that ceiling exists to stop) and compares that price
// to what remains; a required object also prices its OWN combined required
// floor up front (schema.go's floorFrom), so an optional sibling reached
// before enough required ones have run cannot survive on the strength of a
// budget that only looks intact because the real, unavoidable cost has not
// been charged against it yet. Two outcomes follow: if even the minimal
// form does not fit, there is no point recursing for something richer that
// would fit even less — the minimal form is emitted directly, and THIS is
// the one case that stays a documented, honest overshoot (see below); if
// the minimal form WOULD fit, the real walk is attempted, and if it turns
// out to have cost more than what was available anyway, it is replaced
// with that already-known-to-fit minimal form rather than kept oversized.
// A schema constraint still always outranks the size heuristic (P1b
// round-1 review, finding 7: rather than truncating finished JSON into
// something invalid, or omitting a value the schema demands, generation
// prefers the smallest LEGAL representation it can) — this is the same
// rule as before, just enforced by pricing ahead of time instead of only
// noticing after the fact, and "legal" is load-bearing: an early version of
// this pricing priced a required OBJECT property only one level deep
// (mirroring the depth ceiling's own minimalRequiredObject), so a required
// property nested two levels down — GET /api/v1/invites/{inviteId}'s
// creator.id/tenant.id come from exactly this shape — silently
// dropped out of the "smallest legal form" it was supposed to be, and a
// fallback that materializes THAT is not honest overshoot at all, it is the
// truncation this whole mechanism exists to rule out
// (TestAcceptance_Budgets_TightMaxBytesStaysSchemaValid guards this
// specifically, corpus-wide, since badges' own floor happens to be
// flat and could not have caught it).
//
// THE HONEST BOUND, re-measured on the 130-operation acceptance document
// after this pricing landed (Seed 3, ListSize 500, NullRate 0 — the same
// parameters TestAcceptance_Budgets_TightMaxBytesNeverExceeded uses): from
// MaxBytes=512 upward, NOTHING overshoots any more — a large drop from the
// ~1 KB threshold this comment used to cite. GET
// /api/v1/badges/{badgeId} is exactly what closed that gap: its
// response schema (Badge, an allOf of BadgePreview and a second
// branch requiring isBadgeGrantable/isSystem) has a 174-byte floor
// (its output at MaxBytes=8, unchanged by this fix, and proven schema-valid
// — TestAcceptance_Budgets_RequiredSubtreeHardCeiling covers both) and used
// to emit 601 bytes at MaxBytes=512 regardless — the richer real generation
// simply cost more than what remained, and nothing replaced it. It now
// emits exactly 174 bytes at MaxBytes=512: the real generation still costs
// more than fits, but the required-property fallback above now replaces it
// with the schema's own smallest legal form instead of keeping it oversized.
//
// BELOW 512, an overshoot can still happen, but only two ways now, both
// honest and both far tighter than the old "roughly 2x": (1) a single
// object's OWN required floor genuinely exceeds MaxBytes — nothing
// generation does can shrink required content below the schema's own
// minimum, by design; (2) several independently-affordable-looking OPTIONAL
// properties, each evaluated against a budget that still looked positive at
// the moment IT ran, compound past MaxBytes before any one of them
// individually trips the plain "budget exhausted" check — a known,
// documented softness of the per-property, single-pass fair-share algorithm
// (schema.go's capShare), narrowed but not eliminated by floorFrom's
// reserve for required content still to come. At 256 bytes, 7 of the 114
// schema-bearing operations overshoot (was 17 before the hard-ceiling
// pricing landed, unchanged by making the floor recurse — none of those 7
// have a required-object-nested-in-a-required-object shape); the worst
// overshoot ratio measured there is 1.18x, PUT /api/v1/surveys/{surveyId},
// case (2). At 128 bytes, 16 overshoot: 10 of them case (1), the worst
// being GET /api/v1/invites/{inviteId} at 1.63x (209 bytes) — the SAME
// operation the recursive-floor fix above exists for, now correctly
// reporting its true, deeper required floor as a floor rather than hiding
// it behind an under-priced (and invalid) 128-byte fallback; the remaining
// 6 are POST/GET/PUT/DELETE on /tenants/{id}/cohorts*, a known,
// pre-existing, budget-INDEPENDENT oneOf-branch-ambiguity defect in
// TenantCohort's own composition (proven unrelated to this pricing:
// it reproduces, with a different jsonschema message, even at
// MaxBytes=4<<20 where hardCeilingValue is never consulted — see
// cohortOneOfAmbiguity's own doc comment in acceptance_test.go) rather than
// anything this mechanism controls. This matters only for a workspace that
// sets MOCKER_MAX_RESPONSE to something tiny — config's default is 4 MB,
// safely clear of every threshold measured here.
func (g *Generator) Body(v ResponseVariant, req Request) ([]byte, error) {
	if v.SchemaPtr == "" {
		return nil, nil
	}

	var schema map[string]any
	if req.PatchedSchema != nil {
		// D3(1): the already-patched deep copy the runtime built once, used
		// IN PLACE OF resolving v.SchemaPtr — never re-resolved, never
		// re-patched, never touched per request (A13). See PatchedSchema's
		// own doc comment for what nil means here.
		schema = req.PatchedSchema
	} else {
		resolved, err := g.res.Resolve(v.SchemaPtr)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNoSchema, err)
		}
		schema, err = asSchemaObject(resolved)
		if err != nil {
			return nil, err
		}
	}

	w := g.newWalker(req)

	// The list contract is a structural/shape decision, not a value
	// source, so it is tried before the example/schema priority chain:
	// deciding whether this even IS a list request needs router-family
	// knowledge (Request.ListFamily) this package doesn't derive itself.
	if body, ok, lerr := listBody(w, v, req, schema); lerr != nil {
		return nil, lerr
	} else if ok {
		return w.finalize(w.applyRecipePostPass(body), v)
	}

	// DESIGN §9 line 339 ranks a recipe ABOVE even the media-type-level
	// example: "рецепт → example/examples (media type → named → схема →
	// const → default) → enum". contentExample short-circuits BEFORE
	// walkSchema/leafValue ever run, so recipeValue would never get a
	// chance to outrank it — gating the whole branch on "this variant binds
	// no recipe" is what keeps the two in the right order: with no recipes
	// bound this is exactly the branch P1b always took (HARD RULE 6), and
	// with recipes bound it is skipped entirely so the walk below (where
	// recipeValue actually runs) decides instead.
	//
	// P2e widens the same gate by exactly one clause: a patched root ranks
	// above the media-type example for the identical reason a recipe does —
	// the operator's own edit outranks a spec-declared example — so this
	// branch is skipped when req.PatchedSchema != nil too, and the walk
	// below runs against the patched root instead (D3(5); its own
	// "examples" key was already deleted at runtime build).
	if (req.Recipes == nil || req.Recipes.Len() == 0) && req.PatchedSchema == nil {
		if example, ok := w.contentExample(v); ok {
			if b, merr := jsonx.Marshal(example); merr == nil && int64(len(b)) <= w.opts.effMaxBytes() {
				if mt := v.MediaType; mt == "" || strings.Contains(mt, "json") {
					// finalize would marshal the same value a second time.
					return b, nil
				}
				return w.finalize(example, v)
			}
			// Oversized or unmarshalable pinned example: fall through to
			// schema-driven generation, which respects MaxBytes by
			// construction rather than by post-hoc truncation.
		}
	}

	val, werr := w.walkSchema(schema, "", 0)
	if werr != nil {
		return nil, werr
	}
	return w.finalize(w.applyRecipePostPass(val), v)
}

// asSchemaObject normalizes what a resolved schema pointer can legally be:
// a schema object, or a JSON Schema boolean subschema (2020-12 permits
// bare `true`/`false`).
func asSchemaObject(resolved any) (map[string]any, error) {
	switch v := resolved.(type) {
	case map[string]any:
		return v, nil
	case bool:
		if !v {
			return nil, ErrUnsatisfiable
		}
		return map[string]any{}, nil
	default:
		return nil, fmt.Errorf("%w: not a schema object", ErrNoSchema)
	}
}

// finalize serializes val according to v.MediaType (DESIGN §9 "Тело, тип и
// статус": "200 может быть JSON, а 409 — text/plain, и переключение
// статуса не должно отправлять чужой Content-Type"). JSON media types
// marshal val as-is; text/* returns a raw (non-JSON-quoted) string when
// val already is one; anything else — a binary/unknown media type — gets
// a minimal deterministic placeholder, since a full binary-format
// generator is out of P1b's scope (DESIGN §9: "для не-JSON — плейсхолдер
// по типу").
func (w *walker) finalize(val any, v ResponseVariant) ([]byte, error) {
	mt := v.MediaType
	switch {
	case mt == "" || strings.Contains(mt, "json"):
		b, err := jsonx.Marshal(val)
		if err != nil {
			return nil, fmt.Errorf("gen: marshal body: %w", err)
		}
		return b, nil
	case strings.HasPrefix(mt, "text/"):
		if s, ok := val.(string); ok {
			return []byte(s), nil
		}
		b, err := jsonx.Marshal(val)
		if err != nil {
			return nil, fmt.Errorf("gen: marshal body: %w", err)
		}
		return b, nil
	default:
		return []byte("mock-" + mt), nil
	}
}

// contentExample looks up the media-type-level example DESIGN §9 ranks
// above schema-driven generation: the Media Type Object at
// OpPointer/responses/{selector}/content/{mediaType}. Dialect
// normalization (internal/openapi/normalize.go) rewrites a singular
// "example" into a one-element "examples" ARRAY on every object it walks,
// while a native OAS "examples" MAP (multiple named examples) is left as a
// map — firstExample handles both shapes.
func (w *walker) contentExample(v ResponseVariant) (any, bool) {
	if v.MediaType == "" || v.OpPointer == "" {
		return nil, false
	}
	ptr := v.OpPointer + "/responses/" + escapePointerToken(v.Selector) + "/content/" + escapePointerToken(v.MediaType)
	resolved, err := w.res.Resolve(ptr)
	if err != nil {
		return nil, false
	}
	mto, ok := resolved.(map[string]any)
	if !ok {
		return nil, false
	}
	examples, ok := mto["examples"]
	if !ok {
		return nil, false
	}
	return firstExample(examples)
}

// firstExample picks one example value out of either normalized shape:
// the one-element array normalizeExample produces from a singular
// "example", or a native named-examples map (each entry an Example Object
// wrapping its payload under "value"). Map entries are chosen by the
// lexicographically first name, purely for determinism — same reasoning as
// specs.selectMediaType picking a stable media type out of an unordered
// decoded-JSON map.
func firstExample(examples any) (any, bool) {
	switch ex := examples.(type) {
	case []any:
		if len(ex) == 0 {
			return nil, false
		}
		return ex[0], true
	case map[string]any:
		if len(ex) == 0 {
			return nil, false
		}
		names := make([]string, 0, len(ex))
		for k := range ex {
			names = append(names, k)
		}
		sort.Strings(names)
		entry, ok := ex[names[0]].(map[string]any)
		if !ok {
			return nil, false
		}
		val, ok := entry["value"]
		return val, ok
	}
	return nil, false
}

// Headers generates the response headers the spec declares for this
// variant. HOW TO REACH THEM, and this is not optional detail: resolve the
// RESPONSE OBJECT first — g.res.Resolve(v.OpPointer + "/responses/" +
// escaped(v.Selector)) — and read ["headers"] off the RESOLVED node.
// Resolve chases $refs; plain pointer navigation into ".../headers" does
// NOT. Measured on the acceptance document: 221 of 419 responses are
// $ref'd, and BOTH of the document's header declarations sit in
// components/responses, reachable only through a $ref. Navigating from the
// referring site finds nothing and, since absent headers are not an error,
// §9's "объявленные заголовки ответа проставляются" would be silently
// unimplemented with every test still green.
//
// Absent or unresolvable headers are not an error: the map comes back
// empty.
func (g *Generator) Headers(v ResponseVariant, req Request) map[string]string {
	headers := map[string]string{}
	if v.OpPointer == "" || v.Selector == "" {
		return headers
	}
	ptr := v.OpPointer + "/responses/" + escapePointerToken(v.Selector)
	resolved, err := g.res.Resolve(ptr)
	if err != nil {
		return headers
	}
	respObj, ok := resolved.(map[string]any)
	if !ok {
		return headers
	}
	declared, ok := respObj["headers"].(map[string]any)
	if !ok {
		return headers
	}

	w := g.newWalker(req)
	names := make([]string, 0, len(declared))
	for name := range declared {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		resolvedHeader, err := w.res.ResolveNode(declared[name])
		if err != nil {
			continue
		}
		headerObj, ok := resolvedHeader.(map[string]any)
		if !ok {
			continue
		}
		if val, ok := w.headerValue(headerObj, name); ok {
			headers[name] = val
		}
	}
	return headers
}

// headerValue generates one header's string value: its own example(s)
// first, then its schema (walked like any other node, capped to depth 0 —
// a header value is always a scalar-shaped thing on the wire). A header
// object with neither is skipped, not defaulted to "".
func (w *walker) headerValue(headerObj map[string]any, name string) (string, bool) {
	if examples, ok := headerObj["examples"]; ok {
		if v, ok := firstExample(examples); ok {
			return stringifyHeader(v), true
		}
	}
	schemaNode, ok := headerObj["schema"]
	if !ok {
		return "", false
	}
	val, err := w.walkNode(schemaNode, "header."+name, 0)
	if err != nil || val == nil {
		return "", false
	}
	return stringifyHeader(val), true
}

func stringifyHeader(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		b, err := jsonx.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// escapePointerToken RFC-6901-escapes one JSON pointer segment: "~" first
// (to "~0"), then "/" (to "~1"). Mirrors internal/specs' own helper of the
// same name and behavior; duplicated rather than imported because gen is a
// leaf package (HARD RULE: no dependency beyond internal/openapi and the
// stdlib).
func escapePointerToken(tok string) string {
	tok = strings.ReplaceAll(tok, "~", "~0")
	tok = strings.ReplaceAll(tok, "/", "~1")
	return tok
}
