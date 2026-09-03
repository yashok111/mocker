// Package authpreset derives DESIGN §10's auth preset from a document: which
// operations look like login/refresh/profile endpoints, and which recipe
// bindings would make them answer a token a frontend can decode and a
// profile it can trust. It only PROPOSES — Derive never writes anything, and
// the admin handler applies an (edited) list through overrides.Repo.PutMany
// after a human has looked at it.
//
// This package is a LEAF: it imports internal/openapi, internal/recipes,
// internal/domain and the stdlib only — never internal/specs and never
// internal/gen. It takes its operations as a plain slice specifically so it
// stays testable without a database and cannot pull a cycle in behind it:
// three packages above it import this one.
package authpreset

import (
	"cmp"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/recipes"
)

// Operation is what the caller (the admin handler) hands in: one response
// variant of one operation, with the pointer to its already-resolved
// schema.
type Operation struct {
	Method    string // upper case
	Path      string // RELATIVE, as operations.path stores it
	Status    int
	SchemaPtr string // gen.ResponseVariant.SchemaPtr — "" when the response declares no schema
	OpPointer string // operations.pointer, for the security[] of that operation
}

// Binding is ONE proposed recipe, with the reason shown to the operator.
// Binding carries NO opKey — that percent-encoding round-trips through
// overrides.ParseOpKey, which this package must not import (it would drag
// internal/store in behind it). The admin handler computes
// overrides.OpKey(b.Method, b.Path).
type Binding struct {
	Method   string         `json:"method"`
	Path     string         `json:"path"`
	Status   int            `json:"status"`
	DataPath string         `json:"dataPath"` // the pattern the recipe binds to
	Recipe   recipes.Recipe `json:"recipe"`
	Reason   string         `json:"reason"` // human text: "field name \"token\" on an auth path -> jwt"
	Source   string         `json:"source"` // "auth-path" | "identity-alias" | "expiry-field"
}

// Proposal is Derive's whole output: what it found, what it proposes, and
// what it deliberately skipped.
type Proposal struct {
	Bindings  []Binding `json:"bindings"`
	Schemes   []string  `json:"schemes"`   // securitySchemes found, as "name: type scheme"
	AuthPaths []string  `json:"authPaths"` // the paths that matched DESIGN §10's trigger list
	Notes     []string  `json:"notes"`     // what was skipped and why — a silent skip reads as "nothing to do"
	SampleJWT string    `json:"sampleJwt"` // one minted token so the operator sees what will be served
}

// NormalizeForWire replaces every nil slice with an empty one. Go marshals a
// nil slice as `null`, and api/openapi.json declares all four of these as
// REQUIRED arrays — a client generated from that contract types them as
// arrays and calls .map on them, which is a crash rather than an empty list.
// The spec-less branch of GET /auth-preset (preset_handlers.go) and a Derive
// that simply found nothing both produce exactly that shape, so normalizing
// happens once here rather than at each write site.
func (p *Proposal) NormalizeForWire() {
	if p.Bindings == nil {
		p.Bindings = []Binding{}
	}
	if p.Schemes == nil {
		p.Schemes = []string{}
	}
	if p.AuthPaths == nil {
		p.AuthPaths = []string{}
	}
	if p.Notes == nil {
		p.Notes = []string{}
	}
}

// Binding sources, named consistently with Binding.Source above.
const (
	sourceAuthPath      = "auth-path"
	sourceIdentityAlias = "identity-alias"
	sourceExpiryField   = "expiry-field"
)

// jwtProbe is a stand-in JWT-shaped value used only to ask recipes.Coerce
// whether a candidate property's declared type could hold a token string at
// all — "an integer" cannot, no matter what string is tried, and Coerce is
// the single arbiter of that rather than this package reimplementing the
// rule. The literal content is irrelevant; only its Go type (string)
// matters to Coerce.
const jwtProbe = "sample.jwt.value"

// defaultTTLSec is what authTTL falls back to when a workspace's settings
// carry no positive ttl — domain.Settings.Normalize applies the same
// fallback to stored settings, so this only ever fires for a caller that
// handed Derive raw, un-normalized settings.
const defaultTTLSec = 3600

// Derive walks each operation's response schema through res and proposes
// bindings. It NEVER writes anything.
//
// Bindings are ordered deterministically (Method, Path, Status, DataPath) —
// NOT by an opKey, which this package cannot compute: the preview is shown
// to a human and a reshuffling list is unreviewable.
func Derive(res *openapi.Resolver, doc *openapi.Document, ops []Operation, set domain.Settings, now time.Time) (*Proposal, error) {
	p := &Proposal{
		Schemes:   collectSchemes(doc),
		AuthPaths: collectAuthPaths(ops),
	}

	ttl := authTTL(set)
	sampleJWT, err := recipes.MintJWT(set.Auth, set.Identity, nil, 0, now)
	if err != nil {
		// MintJWT only fails on a jsonx.Marshal error over structurally sane
		// inputs this package built itself (never on a missing identity id
		// — identityClaims just omits "sub" for that) — unreachable in
		// practice, but Derive must not panic on an admin request either
		// way.
		return nil, fmt.Errorf("authpreset: mint sample jwt: %w", err)
	}
	p.SampleJWT = sampleJWT

	var bindings []Binding
	var notes []string

	for _, op := range ops {
		if op.SchemaPtr == "" {
			notes = append(notes, notef(op, "no response schema, skipped"))
			continue
		}

		ws := &walkState{res: res}
		ws.walkNode(map[string]any{"$ref": op.SchemaPtr}, "", nil, 0, false, map[string]bool{})
		if ws.hitCycle || ws.hitBudget {
			notes = append(notes, notef(op, "schema walk hit a $ref cycle or its depth/node budget — some deeper fields were not examined"))
		}

		opBindings, opNotes := proposeBindings(op, ws.leaves, IsAuthPath(op.Path), set, ttl)
		bindings = append(bindings, opBindings...)
		notes = append(notes, opNotes...)
	}

	slices.SortFunc(bindings, func(a, b Binding) int {
		if c := cmp.Compare(a.Method, b.Method); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		if c := cmp.Compare(a.Status, b.Status); c != 0 {
			return c
		}
		return cmp.Compare(a.DataPath, b.DataPath)
	})

	p.Bindings = bindings
	p.Notes = notes
	return p, nil
}

// notef formats one Notes entry, tagged with the operation it concerns so
// an operator scanning a long list can tell which skip belongs to which
// endpoint.
func notef(op Operation, format string, args ...any) string {
	return fmt.Sprintf("%s %s [%d]: ", op.Method, op.Path, op.Status) + fmt.Sprintf(format, args...)
}

// authTTL is the ttl (seconds) every "now"/"jwt" binding this package
// proposes is computed against — set.Auth.JWTTTLSec, falling back to
// defaultTTLSec exactly as domain.Settings.Normalize would.
func authTTL(set domain.Settings) int {
	if set.Auth.JWTTTLSec > 0 {
		return set.Auth.JWTTTLSec
	}
	return defaultTTLSec
}

// collectSchemes reads components.securitySchemes off doc and renders each
// as "name: type scheme" (the scheme half only for http-type schemes, which
// is the only place OAS 3.0 declares one). Their presence is what makes the
// preset applicable at all, independent of whether any binding ends up
// using them — DESIGN §10's proof that the document HAS auth to mock.
func collectSchemes(doc *openapi.Document) []string {
	comps, _ := doc.Root()["components"].(map[string]any)
	if comps == nil {
		return nil
	}
	raw, _ := comps["securitySchemes"].(map[string]any)
	out := make([]string, 0, len(raw))
	for name, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, formatScheme(name, m))
	}
	// raw is a decoded JSON object: Go randomizes map range order on
	// purpose. Sorted here so two Derive calls over the same document agree
	// — this is a preview a human reads, not a diff-suppressing detail.
	slices.Sort(out)
	return out
}

func formatScheme(name string, m map[string]any) string {
	typ, _ := m["type"].(string)
	scheme, _ := m["scheme"].(string)
	if scheme != "" {
		return fmt.Sprintf("%s: %s %s", name, typ, scheme)
	}
	return fmt.Sprintf("%s: %s", name, typ)
}

// collectAuthPaths returns the distinct RELATIVE paths among ops that match
// DESIGN §10's trigger list, sorted. Distinct by path, not by (method,
// path): the acceptance document's three auth paths are each POST-only, but
// the trigger list is about the URL, not the verb.
func collectAuthPaths(ops []Operation) []string {
	seen := make(map[string]bool)
	var out []string
	for _, op := range ops {
		if !IsAuthPath(op.Path) || seen[op.Path] {
			continue
		}
		seen[op.Path] = true
		out = append(out, op.Path)
	}
	slices.Sort(out)
	return out
}

// --- the schema walk ---------------------------------------------------
//
// Derive needs leaf property paths and, for each, its declared type/format
// and its object-level siblings (for the bare-"id" profile-shape
// judgement) — the same shape of information internal/gen's schema.go
// walks for generation, but this package cannot import gen (it would be
// the exact cycle the package doc warns about), so it walks its own, much
// smaller subset: no seed, no composition-VALUE semantics, just "what leaf
// properties exist here, and what does the schema declare about them".

const (
	// maxWalkDepth bounds recursion depth. The acceptance document's
	// deepest ordinary nesting is well under this; it exists so a
	// pathological document cannot turn an admin preview request into a
	// stack of unbounded size.
	maxWalkDepth = 24
	// maxWalkNodes bounds total resolver lookups across one operation's
	// walk — the second half of "an unbounded walk hangs the admin
	// request", independent of depth: a self-referencing schema increases
	// depth every hop (so maxWalkDepth alone would already stop it), but
	// this is the backstop for a wide-not-deep pathological shape too.
	maxWalkNodes = 4000
)

// schemaLeaf is one property this walk found: its absolute data path
// (gen's dot/bracket grammar — "response.token", "items[*].status"), its
// own name (the last path segment), its declared type/format, the sibling
// property names at the SAME object level (nil at the walk's own root),
// and whether the walk had already descended through at least one array to
// reach it.
type schemaLeaf struct {
	path     string
	name     string
	jsonType string
	format   string
	siblings map[string]bool
	inArray  bool

	// itemsType is the resolved "items" sub-schema's own declared type, set
	// only when jsonType == "array" (empty otherwise — including when the
	// array's items are themselves untyped or unresolvable). proposeIdentity
	// reads this to catch the one shape [recipes.Coerce] cannot see for
	// itself: identity.roles is a []string, and Coerce's own "array" branch
	// only checks that the SAMPLE is a slice, never the TARGET schema's item
	// type — so without this, "roles"/"permissions" bind onto an array of
	// Role OBJECTS exactly as readily as an array of strings (round-1
	// finding #5).
	itemsType string
}

// walkState is the mutable, per-Operation state one schema walk
// accumulates.
type walkState struct {
	res       *openapi.Resolver
	leaves    []schemaLeaf
	nodesSeen int
	hitBudget bool
	hitCycle  bool
}

// walkNode resolves node (following any $ref chain, exactly like
// [openapi.Resolver.Resolve]) and, if path names a property (i.e. node is
// not an array element — see lastPropertyName), records it as a candidate
// leaf using siblings, the property names ws's caller already knows at
// this level. It then recurses into an object's properties (its own, plus
// one level of allOf/oneOf/anyOf composition — see mergedProperties) or an
// array's items.
//
// ancestry is the set of $ref pointers already on THIS DFS branch — a COPY
// is made before adding a hop, never the caller's own map mutated in
// place, so two sibling branches that happen to $ref the same schema each
// see their own chain rather than one polluted by whichever branch ran
// first. Revisiting a pointer already in ancestry is a genuine cycle
// (FeedbackSection's self-reference in the acceptance document is exactly
// this), not merely two independent reaches of the same shared schema.
func (ws *walkState) walkNode(node any, path string, siblings map[string]bool, depth int, inArray bool, ancestry map[string]bool) {
	// hitBudget is a GLOBAL resource cap — once tripped, no further work is
	// worth doing anywhere in this operation's walk. hitCycle is NOT
	// checked here: it is branch-local (set right below, immediately
	// followed by returning from just that call), and a cycle discovered
	// down one property's subtree must not silently cancel an unrelated
	// SIBLING property's leaves — exactly the bug a self-referencing
	// "children" property one alphabetical position before "id" would
	// otherwise cause.
	if ws.hitBudget {
		return
	}
	ws.nodesSeen++
	if depth > maxWalkDepth || ws.nodesSeen > maxWalkNodes {
		ws.hitBudget = true
		return
	}

	ptr, resolved, err := ws.res.ResolveNodePointer(node)
	if err != nil {
		// The resolver's own $ref-budget/cycle guard tripped (ErrCycle or
		// ErrBudgetExhausted). This walk doesn't need to tell those apart
		// from its own — either way the branch cannot be finished safely.
		ws.hitBudget = true
		return
	}
	if ptr != "" {
		if ancestry[ptr] {
			ws.hitCycle = true
			return
		}
		next := make(map[string]bool, len(ancestry)+1)
		for k := range ancestry {
			next[k] = true
		}
		next[ptr] = true
		ancestry = next
	}

	obj, ok := resolved.(map[string]any)
	if !ok {
		return
	}

	if name, ok := lastPropertyName(path); ok {
		leafType := schemaType(obj)
		ws.leaves = append(ws.leaves, schemaLeaf{
			path:      path,
			name:      name,
			jsonType:  leafType,
			format:    schemaFormat(obj),
			siblings:  siblings,
			inArray:   inArray,
			itemsType: ws.arrayItemsType(leafType, obj),
		})
	}

	props := ws.mergedProperties(obj)
	if len(props) > 0 {
		childSiblings := make(map[string]bool, len(props))
		for n := range props {
			childSiblings[n] = true
		}
		names := make([]string, 0, len(props))
		for n := range props {
			names = append(names, n)
		}
		slices.Sort(names) // deterministic emission order; matters for tests, not for correctness
		for _, n := range names {
			ws.walkNode(props[n], joinPath(path, n), childSiblings, depth+1, inArray, ancestry)
		}
		return
	}

	if items, ok := obj["items"]; ok {
		ws.walkNode(items, path+"[*]", nil, depth+1, true, ancestry)
	}
}

// arrayItemsType resolves obj's own "items" sub-schema (following one $ref
// hop, same as every other node this walk touches) and reports its declared
// type — "" when leafType isn't "array" at all, obj carries no "items", or
// the items node doesn't resolve to a schema object. This is intentionally
// ONE hop, not a full walk: schemaLeaf.itemsType only needs to answer
// "object or not", the one distinction proposeIdentity's roles/permissions
// guard cares about (round-1 finding #5) — not to enumerate the item
// schema's own properties, which the recursive walkNode call two lines up
// already does for its own, separate purpose.
func (ws *walkState) arrayItemsType(leafType string, obj map[string]any) string {
	if leafType != "array" {
		return ""
	}
	items, ok := obj["items"]
	if !ok {
		return ""
	}
	_, resolved, err := ws.res.ResolveNodePointer(items)
	if err != nil {
		return ""
	}
	itemsObj, ok := resolved.(map[string]any)
	if !ok {
		return ""
	}
	return schemaType(itemsObj)
}

// mergedProperties gathers obj's own "properties" plus, one level deep,
// the "properties" of every allOf/oneOf/anyOf member — an approximation of
// internal/gen's exact composition semantics (compose.go), which this
// package deliberately does not reimplement: a preview only needs "what
// leaf names could plausibly appear here", not the generator's precise
// merge/discriminator rules. A member's own nested allOf is not chased
// further; this catches the common "allOf: [base, {additional
// properties}]" inheritance shape without walking arbitrarily deep
// composition trees.
func (ws *walkState) mergedProperties(obj map[string]any) map[string]any {
	out := map[string]any{}
	if p, ok := obj["properties"].(map[string]any); ok {
		maps.Copy(out, p)
	}
	for _, key := range [...]string{"allOf", "oneOf", "anyOf"} {
		arr, ok := obj[key].([]any)
		if !ok {
			continue
		}
		for _, member := range arr {
			if ws.hitBudget {
				return out
			}
			ws.nodesSeen++
			if ws.nodesSeen > maxWalkNodes {
				ws.hitBudget = true
				return out
			}
			_, resolved, err := ws.res.ResolveNodePointer(member)
			if err != nil {
				ws.hitBudget = true
				continue
			}
			mobj, ok := resolved.(map[string]any)
			if !ok {
				continue
			}
			if mp, ok := mobj["properties"].(map[string]any); ok {
				maps.Copy(out, mp)
			}
		}
	}
	return out
}

// joinPath appends name to a dataPath, dot-separated — the same grammar
// internal/gen's schema.go and internal/recipes' pattern matcher both use.
func joinPath(base, name string) string {
	if base == "" {
		return name
	}
	return base + "." + name
}

// lastPropertyName returns path's final property-name segment, and false
// when path is the walk's own root ("") or names an array element ("...[*]"
// or "...[N]") rather than a property — an array element has no field name
// of its own for the name heuristics below to test.
func lastPropertyName(path string) (string, bool) {
	if path == "" {
		return "", false
	}
	if strings.HasSuffix(path, "]") {
		return "", false
	}
	if i := strings.LastIndexByte(path, '.'); i >= 0 {
		return path[i+1:], true
	}
	return path, true
}

func schemaType(obj map[string]any) string {
	t, _ := obj["type"].(string)
	return t
}

func schemaFormat(obj map[string]any) string {
	f, _ := obj["format"].(string)
	return f
}

// --- turning leaves into bindings ---------------------------------------

// proposeBindings applies DESIGN §10's three binding rules to one
// operation's leaves, in a fixed order per leaf (jwt, then expiry, then
// identity) — the three name tables are disjoint by construction, so which
// one is tried first only matters for a leaf name nobody expects to be in
// two tables at once.
func proposeBindings(op Operation, leaves []schemaLeaf, isAuth bool, set domain.Settings, ttl int) ([]Binding, []string) {
	var bindings []Binding
	var notes []string

	for _, leaf := range leaves {
		if isAuth {
			if isRefresh, ok := tokenFieldName(leaf.name); ok {
				b, note, bound := proposeJWT(op, leaf, isRefresh, ttl)
				if bound {
					bindings = append(bindings, b)
				} else if note != "" {
					notes = append(notes, note)
				}
				continue
			}
		}

		if isExpiry, isDuration := expiryFieldName(leaf.name); isExpiry {
			b, note, bound := proposeExpiry(op, leaf, isDuration, ttl)
			if bound {
				bindings = append(bindings, b)
			} else if note != "" {
				notes = append(notes, note)
			}
			continue
		}

		if field, ok := gatedIdentityAliasField[leaf.name]; ok {
			profileShaped := !leaf.inArray && hasProfileAlias(leaf.siblings)
			if !isAuth && !profileShaped {
				notes = append(notes, notef(op, "property %q at %q skipped — not an auth path and not a profile-shaped object (would rewrite an unrelated entity's own %s)", leaf.name, leaf.path, field))
				continue
			}
			b, note, bound := proposeIdentity(op, leaf, field, set)
			if bound {
				bindings = append(bindings, b)
			} else if note != "" {
				notes = append(notes, note)
			}
			continue
		}

		if field, ok := explicitIdentityAliasField[leaf.name]; ok {
			b, note, bound := proposeIdentity(op, leaf, field, set)
			if bound {
				bindings = append(bindings, b)
			} else if note != "" {
				notes = append(notes, note)
			}
			continue
		}
	}

	return bindings, notes
}

func proposeJWT(op Operation, leaf schemaLeaf, isRefresh bool, ttl int) (b Binding, note string, ok bool) {
	if _, coerceOK := recipes.Coerce(jwtProbe, leaf.jsonType, leaf.format); !coerceOK {
		return Binding{}, notef(op, "property %q looks like a token but is declared %q, not string — skipped", leaf.name, leaf.jsonType), false
	}

	rec := recipes.Recipe{Kind: recipes.KindJWT}
	reason := fmt.Sprintf("property %q on auth path %s %s -> jwt", leaf.name, op.Method, op.Path)
	if isRefresh {
		rec.TTLSec = ttl * refreshTTLMultiplier
		reason = fmt.Sprintf("property %q on auth path %s %s -> jwt (refresh token, ttl %ds = %dx the access-token ttl)",
			leaf.name, op.Method, op.Path, rec.TTLSec, refreshTTLMultiplier)
	}
	if err := rec.Validate(); err != nil {
		return Binding{}, notef(op, "jwt recipe for %q rejected: %v", leaf.path, err), false
	}
	return Binding{
		Method: op.Method, Path: op.Path, Status: op.Status, DataPath: leaf.path,
		Recipe: rec, Reason: reason, Source: sourceAuthPath,
	}, "", true
}

func proposeExpiry(op Operation, leaf schemaLeaf, isDuration bool, ttl int) (b Binding, note string, ok bool) {
	if leaf.jsonType != "integer" && leaf.jsonType != "number" {
		return Binding{}, notef(op, "property %q looks like an expiry field but is declared %q, not numeric — skipped", leaf.name, leaf.jsonType), false
	}

	var rec recipes.Recipe
	var reason string
	if isDuration {
		raw, err := jsonx.Marshal(ttl)
		if err != nil {
			return Binding{}, notef(op, "expiry recipe for %q: %v", leaf.path, err), false
		}
		rec = recipes.Recipe{Kind: recipes.KindConst, Data: raw}
		reason = fmt.Sprintf("property %q is a duration (expires_in), not a timestamp -> const %d (the auth ttl in seconds)", leaf.name, ttl)
	} else {
		rec = recipes.Recipe{Kind: recipes.KindNow, Offset: fmt.Sprintf("+%ds", ttl)}
		reason = fmt.Sprintf("property %q looks like an expiry timestamp -> now +%ds (the auth ttl)", leaf.name, ttl)
	}
	if err := rec.Validate(); err != nil {
		return Binding{}, notef(op, "expiry recipe for %q rejected: %v", leaf.path, err), false
	}
	return Binding{
		Method: op.Method, Path: op.Path, Status: op.Status, DataPath: leaf.path,
		Recipe: rec, Reason: reason, Source: sourceExpiryField,
	}, "", true
}

func proposeIdentity(op Operation, leaf schemaLeaf, field string, set domain.Settings) (b Binding, note string, ok bool) {
	if sample, sampleOK := recipes.IdentityField(set.Identity, field); sampleOK {
		if _, coerceOK := recipes.Coerce(sample, leaf.jsonType, leaf.format); !coerceOK {
			return Binding{}, notef(op, "identity.%s does not fit declared type %q at %q — skipped", field, leaf.jsonType, leaf.path), false
		}
		// Coerce's own "array" branch only checks that the SAMPLE is a Go
		// slice — it has no visibility into the TARGET schema's own "items"
		// sub-schema, so identity.roles ([]string) "fits" a field declared
		// as an array of Role OBJECTS exactly as readily as one declared as
		// an array of strings. field == "roles" is the only identity alias
		// that is ever array-shaped (DESIGN §10's table has no other), so
		// this is the one place that mismatch can actually occur — caught
		// here, where leaf.itemsType is still in scope, rather than shipped
		// as a binding that fails its own response schema on every request
		// (round-1 finding #5: 20 of 114 acceptance-document operations,
		// every failure the identical ".../rolesN: got string, want
		// object").
		if field == "roles" && leaf.itemsType == "object" {
			return Binding{}, notef(op, "property %q is an array of OBJECTS, not identity.roles ([]string) — skipped", leaf.name), false
		}
	}
	rec := recipes.Recipe{Kind: recipes.KindIdentity, Field: field}
	if err := rec.Validate(); err != nil {
		return Binding{}, notef(op, "identity recipe for %q rejected: %v", leaf.path, err), false
	}
	return Binding{
		Method: op.Method, Path: op.Path, Status: op.Status, DataPath: leaf.path,
		Recipe: rec, Reason: fmt.Sprintf("property %q -> identity.%s", leaf.name, field), Source: sourceIdentityAlias,
	}, "", true
}
