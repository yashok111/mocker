package router_test

import (
	"fmt"
	"net/http"
	"slices"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/router"
)

// route builds a spec-shaped Route (Custom false) with CanonicalPath derived
// the same way the real indexer derives it, so tests exercise the same
// computation the production code path does instead of a hand-typed guess.
func route(method, path string, custom bool, sourceOrder int64) router.Route {
	return router.Route{
		Method:        method,
		Path:          path,
		CanonicalPath: router.CanonicalPath(path),
		Custom:        custom,
		SourceOrder:   sourceOrder,
	}
}

func TestMatch_StaticBeatsParam(t *testing.T) {
	routes := []router.Route{
		route("GET", "/users/{id}", false, 1),
		route("GET", "/users/me", false, 2),
	}
	tbl := router.Build(routes, "")

	m, ok := tbl.Match("GET", []string{"users", "me"})
	if !ok {
		t.Fatalf("expected a match for /users/me")
	}
	if m.Route.Path != "/users/me" {
		t.Errorf("matched %q, want /users/me (static segment must beat a parameter)", m.Route.Path)
	}
	if len(m.Params) != 0 {
		t.Errorf("params = %v, want none", m.Params)
	}

	m2, ok := tbl.Match("GET", []string{"users", "42"})
	if !ok {
		t.Fatalf("expected a match for /users/42")
	}
	if m2.Route.Path != "/users/{id}" {
		t.Errorf("matched %q, want /users/{id}", m2.Route.Path)
	}
	if m2.Params["id"] != "42" {
		t.Errorf("params[id] = %q, want 42", m2.Params["id"])
	}
}

func TestMatch_LeftmostStaticWins(t *testing.T) {
	routes := []router.Route{
		route("GET", "/a/{x}/c", false, 1),
		route("GET", "/a/b/{y}", false, 2),
	}
	tbl := router.Build(routes, "")

	// /a/b/c matches both patterns; they tie on segment count (3) and static
	// count (2 each), so the leftmost differing position — index 1, "b"
	// static vs {x}/{y} param — must decide it, per DESIGN §8 rule 2.
	m, ok := tbl.Match("GET", []string{"a", "b", "c"})
	if !ok {
		t.Fatalf("expected a match for /a/b/c")
	}
	if m.Route.Path != "/a/b/{y}" {
		t.Errorf("matched %q, want /a/b/{y} (static at the leftmost differing position must win)", m.Route.Path)
	}
	if m.Params["y"] != "c" {
		t.Errorf("params[y] = %q, want c", m.Params["y"])
	}
}

func TestMatch_CustomBeatsSpecAtEqualSpecificity(t *testing.T) {
	routes := []router.Route{
		route("GET", "/users/{id}", false, 1), // spec operation
		route("GET", "/users/{id}", true, 2),  // custom override, same canonical shape
	}
	tbl := router.Build(routes, "")

	m, ok := tbl.Match("GET", []string{"users", "42"})
	if !ok {
		t.Fatalf("expected a match")
	}
	if !m.Route.Custom {
		t.Errorf("matched the spec route, want the custom override to win the tie (DESIGN §8 rule 3)")
	}
}

func TestMatch_CustomSpecEqualSpecificityIsOverrideNotConflict(t *testing.T) {
	// A custom route canonically equal to a spec operation must still
	// resolve cleanly to exactly one winner (the custom one) — it is an
	// override, not an ambiguous state, even though Conflicts (tested
	// separately) deliberately does not flag this pairing at all.
	routes := []router.Route{
		route("GET", "/users/{id}", false, 1),
		route("GET", "/users/{id}", true, 2),
	}
	tbl := router.Build(routes, "")
	if tbl.Len() != 2 {
		t.Fatalf("Len() = %d, want 2 (both routes are still present in the table)", tbl.Len())
	}
}

func TestMatch_SourceOrderTiebreak(t *testing.T) {
	routes := []router.Route{
		{OpRowID: 1, Method: "GET", Path: "/x/{id}", CanonicalPath: "/x/{}", SourceOrder: 5},
		{OpRowID: 2, Method: "GET", Path: "/x/{id}", CanonicalPath: "/x/{}", SourceOrder: 1},
	}
	tbl := router.Build(routes, "")

	m, ok := tbl.Match("GET", []string{"x", "7"})
	if !ok {
		t.Fatalf("expected a match")
	}
	if m.Route.OpRowID != 2 {
		t.Errorf("matched OpRowID = %d, want 2 (lower SourceOrder must win the final tie-break)", m.Route.OpRowID)
	}
}

func TestMatch_HeadResolvesToGetRoute(t *testing.T) {
	routes := []router.Route{route("GET", "/ping", false, 1)}
	tbl := router.Build(routes, "")

	m, ok := tbl.Match("HEAD", []string{"ping"})
	if !ok {
		t.Fatalf("expected HEAD to match the GET route")
	}
	if m.Route.Method != http.MethodGet {
		t.Errorf("matched route method = %q, want GET (no route is ever stored twice for HEAD)", m.Route.Method)
	}

	if _, ok := tbl.Match("head", []string{"ping"}); !ok {
		t.Fatalf("expected lower-case \"head\" to match too")
	}

	if _, ok := tbl.Match("GET", []string{"missing"}); ok {
		t.Fatalf("did not expect a match for an unregistered path")
	}
}

func TestMatch_ParamNeverSpansASlash(t *testing.T) {
	routes := []router.Route{route("GET", "/files/{name}", false, 1)}
	tbl := router.Build(routes, "")

	if _, ok := tbl.Match("GET", []string{"files", "a", "b"}); ok {
		t.Fatalf("a single {param} segment must not match two path segments")
	}
	if _, ok := tbl.Match("GET", []string{"files"}); ok {
		t.Fatalf("a required {param} segment must not match zero segments")
	}
	if _, ok := tbl.Match("GET", []string{"files", "a"}); !ok {
		t.Fatalf("expected exactly one segment to match {name}")
	}
}

func TestMatch_ParamsPassedThroughAsGiven(t *testing.T) {
	// Percent-decoding is the caller's job (mockplane.NormalizeSegments), not
	// this package's — router is a leaf and never imports mockplane. This
	// asserts Match performs no re-encoding/decoding of its own: whatever
	// value the caller hands in for a param segment comes back verbatim.
	routes := []router.Route{route("GET", "/items/{name}", false, 1)}
	tbl := router.Build(routes, "")

	m, ok := tbl.Match("GET", []string{"items", "café au lait"})
	if !ok {
		t.Fatalf("expected a match")
	}
	if m.Params["name"] != "café au lait" {
		t.Errorf("params[name] = %q, want the decoded value verbatim", m.Params["name"])
	}
}

func TestBuild_BasePathAppliedAtBuildNotBakedIntoRoute(t *testing.T) {
	routes := []router.Route{route("GET", "/users/{id}", false, 1)}

	tblRoot := router.Build(routes, "")
	tblAPI := router.Build(routes, "/api/v1")

	if _, ok := tblRoot.Match("GET", []string{"users", "1"}); !ok {
		t.Fatalf("expected a match against the empty-base-path table")
	}
	if _, ok := tblRoot.Match("GET", []string{"api", "v1", "users", "1"}); ok {
		t.Fatalf("empty-base-path table must not match a /api/v1-prefixed path")
	}

	if _, ok := tblAPI.Match("GET", []string{"api", "v1", "users", "1"}); !ok {
		t.Fatalf("expected a match against the /api/v1-base-path table")
	}
	if _, ok := tblAPI.Match("GET", []string{"users", "1"}); ok {
		t.Fatalf("/api/v1-base-path table must not match the unprefixed path")
	}

	// The very same Route value fed both Build calls: Path was never mutated
	// to carry a base path baked in.
	if routes[0].Path != "/users/{id}" {
		t.Errorf("Route.Path was mutated by Build: %q, want unchanged /users/{id}", routes[0].Path)
	}
}

func TestCanonicalPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
	}{
		{"no params", "/users", "/users"},
		{"one param", "/users/{id}", "/users/{}"},
		{"two params", "/users/{id}/posts/{postId}", "/users/{}/posts/{}"},
		{"root", "/", "/"},
		{"brace not spanning the whole segment stays literal", "/files/prefix{id}", "/files/prefix{id}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := router.CanonicalPath(tc.path); got != tc.want {
				t.Errorf("CanonicalPath(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestConflicts(t *testing.T) {
	routes := []router.Route{
		{Method: "GET", Path: "/users/{id}", CanonicalPath: "/users/{}", Custom: true, SourceOrder: 1},
		{Method: "GET", Path: "/users/{userId}", CanonicalPath: "/users/{}", Custom: true, SourceOrder: 2}, // clashes with the row above
		{Method: "GET", Path: "/users/{id}", CanonicalPath: "/users/{}", Custom: false, SourceOrder: 3},    // spec: override, not a conflict
		{Method: "POST", Path: "/users/{id}", CanonicalPath: "/users/{}", Custom: true, SourceOrder: 4},    // different method: no clash
		{Method: "GET", Path: "/orgs/{id}", CanonicalPath: "/orgs/{}", Custom: true, SourceOrder: 5},       // alone: no clash
	}

	got := router.Conflicts(routes)
	want := []string{"GET /users/{}"}
	if !slices.Equal(got, want) {
		t.Errorf("Conflicts = %v, want %v", got, want)
	}
}

func TestConflicts_NoCustomRoutesNoConflicts(t *testing.T) {
	routes := []router.Route{
		route("GET", "/users/{id}", false, 1),
		route("GET", "/users/{userId}", false, 2),
	}
	if got := router.Conflicts(routes); len(got) != 0 {
		t.Errorf("Conflicts = %v, want none (only custom routes can conflict)", got)
	}
}

func TestHasCanonical(t *testing.T) {
	routes := []router.Route{
		route("GET", "/users", false, 1),
		route("GET", "/users/{id}", false, 2),
		route("POST", "/users", false, 3),
	}
	// Build against a non-empty base path so a test bug that compares against
	// the GLUED pattern instead of the relative Route.CanonicalPath would show
	// up as an unexpected false, not accidentally pass by coincidence.
	tbl := router.Build(routes, "/api/v1")

	if !tbl.HasCanonical("GET", "/users") {
		t.Errorf(`HasCanonical("GET", "/users") = false, want true`)
	}
	if !tbl.HasCanonical("get", "/users/{}") {
		t.Errorf(`HasCanonical("get", "/users/{}") = false, want true (method is case-insensitive)`)
	}
	if !tbl.HasCanonical("POST", "/users") {
		t.Errorf(`HasCanonical("POST", "/users") = false, want true`)
	}
	if tbl.HasCanonical("DELETE", "/users") {
		t.Errorf(`HasCanonical("DELETE", "/users") = true, want false (no DELETE route exists)`)
	}
	if tbl.HasCanonical("GET", "/orgs") {
		t.Errorf(`HasCanonical("GET", "/orgs") = true, want false (no such canonical path)`)
	}
	if tbl.HasCanonical("GET", "/api/v1/users") {
		t.Errorf(`HasCanonical("GET", "/api/v1/users") = true, want false (compares the RELATIVE canonical path, not the glued match pattern)`)
	}
	if !tbl.HasCanonical("HEAD", "/users") {
		t.Errorf(`HasCanonical("HEAD", "/users") = false, want true (HEAD resolves to the GET bucket, same as Match)`)
	}
}

// TestListFamily_DetailRouteWithSibling proves the family predicate over a
// table built the way P3a's spec-import-time caller builds it —
// router.Build(routes, "") with an empty base path — since the family key
// is the RELATIVE canonical path, not one glued to a workspace's basePath.
func TestListFamily_DetailRouteWithSibling(t *testing.T) {
	routes := []router.Route{
		route("GET", "/users", false, 1),
		route("GET", "/users/{id}", false, 2),
	}
	tbl := router.Build(routes, "")
	detail := &routes[1]

	got := router.ListFamily(tbl, detail)
	if got != "/users" {
		t.Errorf("ListFamily(detail /users/{id}) = %q, want %q", got, "/users")
	}
}

// TestListFamily_NoSiblingCollectionRoute is R2's second half: a detail-
// shaped path whose collection route does not exist in the table is not a
// family, however it looks on its own.
func TestListFamily_NoSiblingCollectionRoute(t *testing.T) {
	routes := []router.Route{
		route("GET", "/users/{id}", false, 1),
	}
	tbl := router.Build(routes, "")
	detail := &routes[0]

	if got := router.ListFamily(tbl, detail); got != "" {
		t.Errorf("ListFamily(no sibling /users) = %q, want \"\"", got)
	}
}

// TestListFamily_NotADetailRoute proves ListFamily declines a route whose
// canonical path does not end in the {} segment CanonicalPath leaves for a
// path parameter — the collection route itself, asked about its own
// family, must answer "" rather than (wrongly) treating itself as a detail
// route of some further-nested family.
func TestListFamily_NotADetailRoute(t *testing.T) {
	routes := []router.Route{
		route("GET", "/users", false, 1),
		route("GET", "/users/{id}", false, 2),
	}
	tbl := router.Build(routes, "")
	collection := &routes[0]

	if got := router.ListFamily(tbl, collection); got != "" {
		t.Errorf("ListFamily(collection route itself) = %q, want \"\"", got)
	}
}

// TestListFamily_BareParamPrefixIsDeclined covers the "prefix == \"\""
// guard: a top-level "/{id}" route strips to a prefix of "", which is never
// treated as a family even if (pathologically) an empty-canonical-path
// route existed in the table — a family key can never be empty.
func TestListFamily_BareParamPrefixIsDeclined(t *testing.T) {
	routes := []router.Route{
		route("GET", "/{id}", false, 1),
	}
	tbl := router.Build(routes, "")
	detail := &routes[0]

	if got := router.ListFamily(tbl, detail); got != "" {
		t.Errorf("ListFamily(/{id}) = %q, want \"\"", got)
	}
}

// TestDetailIDParam_TrailingSegmentName proves the id is read off the
// route's real, ORDERED path segments — never the unordered PathParams map
// a caller might otherwise reach for — using a route with two path
// parameters that both look like an id by name, so a name-only heuristic
// would not be able to tell them apart the way position can.
func TestDetailIDParam_TrailingSegmentName(t *testing.T) {
	r := route("GET", "/tenants/{tenantId}/cohorts/{cohortId}", false, 1)

	got := router.DetailIDParam(&r)
	if got != "cohortId" {
		t.Errorf("DetailIDParam(.../{tenantId}/cohorts/{cohortId}) = %q, want %q", got, "cohortId")
	}
}

// TestDetailIDParam_NotATrailingParam proves "" when the route's last path
// segment is not a {param} at all — the collection route itself has no
// trailing id to report.
func TestDetailIDParam_NotATrailingParam(t *testing.T) {
	r := route("GET", "/users", false, 1)

	if got := router.DetailIDParam(&r); got != "" {
		t.Errorf("DetailIDParam(/users) = %q, want \"\"", got)
	}
}

// TestParentFamily proves D4.1's rule directly: the prefix ending
// immediately before family's LAST "{}" segment, or "" when family carries
// none — including the two-level case, which this slice never derives (D3.4
// bounds derivation at one level) but which ParentFamily itself, being a
// pure string operation, still computes correctly.
func TestParentFamily(t *testing.T) {
	tests := []struct {
		family string
		want   string
	}{
		{"/orgs/{}/users", "/orgs"},
		{"/orgs/{}/teams/{}/users", "/orgs/{}/teams"},
		{"/items", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := router.ParentFamily(tt.family); got != tt.want {
			t.Errorf("ParentFamily(%q) = %q, want %q", tt.family, got, tt.want)
		}
	}
}

// TestBaseParamIndexes covers D18.1's own list of shapes: no parameter (the
// empty tuple every workspace's basePath has today), one, two, a brace that
// does not span a whole segment, an unbalanced brace, an empty name, a
// trailing slash and a doubled slash — the last two prove the split agrees
// with what Build actually glues and compiles.
func TestBaseParamIndexes(t *testing.T) {
	tests := []struct {
		name     string
		basePath string
		wantIdx  []int
		wantName []string
		wantOK   bool
	}{
		{"no parameter", "/orgs", nil, nil, true},
		{"root only", "/", nil, nil, true},
		{"one parameter", "/orgs/{orgId}", []int{1}, []string{"orgId"}, true},
		{
			"two parameters",
			"/orgs/{orgId}/teams/{teamId}",
			[]int{1, 3},
			[]string{"orgId", "teamId"},
			true,
		},
		{"brace does not span whole segment", "/v{n}", nil, nil, false},
		{"unbalanced brace, missing close", "/orgs/{orgId", nil, nil, false},
		{"unbalanced brace, missing open", "/orgs/orgId}", nil, nil, false},
		{"empty name", "/orgs/{}", nil, nil, false},
		{"trailing slash", "/orgs/{orgId}/", []int{1}, []string{"orgId"}, true},
		{"doubled slash", "//orgs/{orgId}", []int{1}, []string{"orgId"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			idx, names, valid := router.BaseParamIndexes(tt.basePath)
			if valid != tt.wantOK {
				t.Fatalf("BaseParamIndexes(%q) valid = %v, want %v", tt.basePath, valid, tt.wantOK)
			}
			if !tt.wantOK {
				return // idx/names are declared meaningless on an invalid shape
			}
			if !slices.Equal(idx, tt.wantIdx) {
				t.Errorf("BaseParamIndexes(%q) idx = %v, want %v", tt.basePath, idx, tt.wantIdx)
			}
			if !slices.Equal(names, tt.wantName) {
				t.Errorf("BaseParamIndexes(%q) names = %v, want %v", tt.basePath, names, tt.wantName)
			}
		})
	}
}

// TestBaseValues checks the positional read itself, and the short-segments
// refusal that guards the index arithmetic against a state Match's own
// success already rules out.
func TestBaseValues(t *testing.T) {
	t.Run("no parameter", func(t *testing.T) {
		got, ok := router.BaseValues("/orgs", []string{"orgs", "quizzes"})
		if !ok || !slices.Equal(got, []string{}) {
			t.Fatalf("BaseValues = %v, %v, want [], true", got, ok)
		}
	})

	t.Run("one parameter, positional off the matched segments", func(t *testing.T) {
		got, ok := router.BaseValues("/orgs/{orgId}", []string{"orgs", "7", "quizzes"})
		if !ok || !slices.Equal(got, []string{"7"}) {
			t.Fatalf("BaseValues = %v, %v, want [7], true", got, ok)
		}
	})

	t.Run("two parameters, in basePath order", func(t *testing.T) {
		got, ok := router.BaseValues(
			"/orgs/{orgId}/teams/{teamId}",
			[]string{"orgs", "7", "teams", "3", "users"},
		)
		if !ok || !slices.Equal(got, []string{"7", "3"}) {
			t.Fatalf("BaseValues = %v, %v, want [7 3], true", got, ok)
		}
	})

	t.Run("trailing slash in basePath", func(t *testing.T) {
		got, ok := router.BaseValues("/orgs/{orgId}/", []string{"orgs", "7", "quizzes"})
		if !ok || !slices.Equal(got, []string{"7"}) {
			t.Fatalf("BaseValues = %v, %v, want [7], true", got, ok)
		}
	})

	t.Run("doubled slash in basePath", func(t *testing.T) {
		got, ok := router.BaseValues("//orgs/{orgId}", []string{"orgs", "7", "quizzes"})
		if !ok || !slices.Equal(got, []string{"7"}) {
			t.Fatalf("BaseValues = %v, %v, want [7], true", got, ok)
		}
	})

	t.Run("segments shorter than basePath's own segment count", func(t *testing.T) {
		// A state a successful Match makes impossible — checked because the
		// alternative is an index panic on a plane that is unauthenticated
		// by design.
		got, ok := router.BaseValues("/orgs/{orgId}/teams/{teamId}", []string{"orgs", "7"})
		if ok || got != nil {
			t.Fatalf("BaseValues = %v, %v, want nil, false", got, ok)
		}
	})

	t.Run("invalid basePath shape refuses rather than guesses", func(t *testing.T) {
		got, ok := router.BaseValues("/v{n}", []string{"v7"})
		if ok || got != nil {
			t.Fatalf("BaseValues = %v, %v, want nil, false", got, ok)
		}
	})
}

// TestMatch_ConcurrentReadsAreRaceFree proves the table's read path needs no
// mutex: run with -race, many goroutines hammering Match on a shared *Table
// built once must never trip the race detector, because nothing in Table is
// ever written again after Build returns.
func TestMatch_ConcurrentReadsAreRaceFree(t *testing.T) {
	routes := []router.Route{
		route("GET", "/users/{id}", false, 1),
		route("GET", "/users/me", false, 2),
		route("POST", "/users", false, 3),
		route("GET", "/a/{x}/c", false, 4),
		route("GET", "/a/b/{y}", false, 5),
		route("GET", "/users/{id}", true, 6),
	}
	tbl := router.Build(routes, "/api")

	const goroutines = 32
	const iterations = 200

	var wg sync.WaitGroup
	wg.Add(goroutines)
	for g := range goroutines {
		go func(g int) {
			defer wg.Done()
			for i := range iterations {
				tbl.Match("GET", []string{"api", "users", "me"})
				tbl.Match("GET", []string{"api", "users", fmt.Sprintf("%d-%d", g, i)})
				tbl.Match("HEAD", []string{"api", "users", "me"})
				tbl.Match("GET", []string{"api", "a", "b", "c"})
				tbl.Match("POST", []string{"api", "users"})
				if tbl.Len() != len(routes) {
					t.Errorf("Len() = %d, want %d", tbl.Len(), len(routes))
					return
				}
			}
		}(g)
	}
	wg.Wait()
}

// TestMatch_CustomRowIDSurvivesBuildAndMatch guards the P2 wiring Build
// copies a Route by value into: a field Build forgot to carry across that
// copy would silently zero out at serve time, and every custom endpoint
// would report matched_id 0 to traffic instead of its real
// custom_endpoints.id.
func TestMatch_CustomRowIDSurvivesBuildAndMatch(t *testing.T) {
	routes := []router.Route{
		{Method: "GET", Path: "/widgets", CanonicalPath: "/widgets", Custom: false, OpRowID: 7, SourceOrder: 1},
		{Method: "GET", Path: "/gadgets", CanonicalPath: "/gadgets", Custom: true, CustomRowID: 42, SourceOrder: 2},
	}
	tbl := router.Build(routes, "")

	m, ok := tbl.Match("GET", []string{"widgets"})
	if !ok {
		t.Fatalf("expected a match for /widgets")
	}
	if m.Route.CustomRowID != 0 {
		t.Errorf("spec route CustomRowID = %d, want 0", m.Route.CustomRowID)
	}
	if m.Route.OpRowID != 7 {
		t.Errorf("spec route OpRowID = %d, want 7", m.Route.OpRowID)
	}

	m2, ok := tbl.Match("GET", []string{"gadgets"})
	if !ok {
		t.Fatalf("expected a match for /gadgets")
	}
	if m2.Route.CustomRowID != 42 {
		t.Errorf("custom route CustomRowID = %d, want 42 (Build must copy it, Match must return it)", m2.Route.CustomRowID)
	}
	if m2.Route.OpRowID != 0 {
		t.Errorf("custom route OpRowID = %d, want 0", m2.Route.OpRowID)
	}
}
