// acceptance_p1c_test.go proves P1c slice 1's phase criterion (DESIGN §19
// line 1111, "фронт логинится") against the REAL 130-operation acceptance
// document, through internal/authpreset.Derive and internal/gen directly —
// no HTTP, no mockplane, no database beyond what loadAcceptance already
// opens to build the fixture. internal/server/p1c_test.go proves the SAME
// criterion end to end, through the whole stack, on a small inline document
// instead: this file is the "does the derivation actually work on the real,
// messy document" half — including the traps the phase digest names (token/
// expiry-shaped property names that sit in a REQUEST body or an orphan
// schema, never reachable from any response, so Derive's walk — which only
// ever sees response schemas — cannot bind them regardless of the auth-path
// gate).
//
// package gen_test, NOT gen: this file needs internal/authpreset, which
// itself sits ABOVE internal/gen (three packages import it — see that
// package's own doc comment), so a package-gen test importing it would be
// the same import cycle acceptance_test.go's header already explains for
// internal/specs. It also needs the real *specs.Repo-backed acceptanceFixture
// that file builds.
//
// Every helper this file reuses — acceptanceFixture, loadAcceptance,
// fixedClock, pickPrimaryVariant, isGenuine2xxSelector, samplePathParams,
// newSchemaOracle, conformanceFailure, goldenCorpus, goldenHashesPath,
// goldenDocument — is declared exactly once, in acceptance_test.go or
// golden_p1b_test.go. Redeclaring any of them here would not compile, and a
// second hand-written copy could silently drift from the original and
// defeat the entire point of "one shared enumeration" both of those files'
// own doc comments explain. Every symbol this file DOES declare is
// prefixed p1c: acceptance_test.go and golden_p1b_test.go belong to earlier
// stages of this same phase and are off limits to edit.
package gen_test

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/authpreset"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
	"github.com/yashok111/mocker/internal/specs"
)

// p1cAuthOps mirrors internal/admin/preset_handlers.go's handleGetAuthPreset
// build loop EXACTLY — one authpreset.Operation per RESPONSE VARIANT, never
// per operation (an operation with six declared statuses contributes six
// entries). It is not imported from internal/admin: that package sits above
// internal/gen (through internal/specs and internal/store), so importing it
// here would be a cycle. Duplicated rather than factored into a shared
// package because it is eleven lines of pure data reshaping, not logic
// worth its own package.
func p1cAuthOps(fx *acceptanceFixture) []authpreset.Operation {
	var ops []authpreset.Operation
	for _, op := range fx.ops {
		for _, v := range fx.variants[op.ID] {
			ops = append(ops, authpreset.Operation{
				Method: op.Method, Path: op.Path, Status: v.HTTPStatus,
				SchemaPtr: v.SchemaPtr, OpPointer: v.OpPointer,
			})
		}
	}
	return ops
}

// p1cSettings is the ONE domain.Settings every test below derives and
// generates against. [authpreset.Derive] needs Identity+Auth to compute the
// jwt ttl and mint its sample token; [gen.Options] needs the SAME two
// fields so a jwt/identity recipe born from THAT derivation resolves
// against the SAME identity and signing key a real runtime would use. Both
// reading from this one function, rather than each constructing its own
// domain.Settings, is what rules out the "adjacent parameter swap" P0's own
// post-mortem warns Settings/AuthSettings invites.
func p1cSettings() domain.Settings {
	return domain.DefaultSettings()
}

// p1cDerive runs authpreset.Derive over fx exactly as internal/admin's
// handleGetAuthPreset does — resolver, doc, one authpreset.Operation per
// response variant, the shared p1cSettings, a frozen clock — and fails the
// test on error rather than returning one, since every assertion below
// needs a *authpreset.Proposal to exist at all.
func p1cDerive(t *testing.T, fx *acceptanceFixture) *authpreset.Proposal {
	t.Helper()
	proposal, err := authpreset.Derive(fx.resolver, fx.doc, p1cAuthOps(fx), p1cSettings(), fixedClock())
	if err != nil {
		t.Fatalf("authpreset.Derive: %v", err)
	}
	return proposal
}

// p1cRecipeKey addresses one compiled *recipes.Set exactly the way
// internal/mockplane/overrides.go's own recipeSetKey does: the operation,
// as [overrides.OpKey] computes it, plus the status ACTUALLY being served,
// as a decimal string.
type p1cRecipeKey struct {
	op     string
	status string
}

// p1cCompileBindings groups bindings by (operation, status) and compiles
// each group — the same two steps internal/mockplane/overrides.go's
// buildRecipeSets performs against a stored op_overrides row, run here
// directly against a freshly derived proposal instead of a database round
// trip: this file's job is proving the derivation and internal/gen agree
// with each other, not re-proving internal/overrides' own storage (that
// package has its own tests).
func p1cCompileBindings(t *testing.T, bindings []authpreset.Binding) map[p1cRecipeKey]*recipes.Set {
	t.Helper()
	grouped := map[p1cRecipeKey]map[string]recipes.Recipe{}
	for _, b := range bindings {
		key := p1cRecipeKey{op: overrides.OpKey(b.Method, b.Path), status: strconv.Itoa(b.Status)}
		m, ok := grouped[key]
		if !ok {
			m = map[string]recipes.Recipe{}
			grouped[key] = m
		}
		m[b.DataPath] = b.Recipe
	}
	compiled := make(map[p1cRecipeKey]*recipes.Set, len(grouped))
	for key, m := range grouped {
		set, err := recipes.Compile(m)
		if err != nil {
			t.Fatalf("compile recipes for %+v: %v", key, err)
		}
		compiled[key] = set
	}
	return compiled
}

// p1cRecipesFor looks up op's compiled recipe set for the given status —
// nil when nothing is bound there, exactly [gen.Request.Recipes]'s own "nil
// means no recipes bound" contract, so a caller can assign the result
// straight into a Request literal with no extra nil check of its own.
func p1cRecipesFor(compiled map[p1cRecipeKey]*recipes.Set, op *specs.Operation, status int) *recipes.Set {
	return compiled[p1cRecipeKey{op: overrides.OpKey(op.Method, op.Path), status: strconv.Itoa(status)}]
}

// p1cContainsPath reports whether path is one of paths — a plain linear
// scan (paths is always exactly 3 long here) rather than a set, since
// building a map for three elements would be pure ceremony.
func p1cContainsPath(paths []string, path string) bool {
	return slices.Contains(paths, path)
}

// --------------------------------------------------------------------------
// Assertion 1: the derivation itself — exactly the three DESIGN §10 auth
// paths, bound the way the digest's measured facts say they must be.
// --------------------------------------------------------------------------

// TestP1cAcceptance_DeriveBindsTheThreeAuthPaths is item 2's first bullet:
// the three auth paths are found and only those three, POST
// /api/v1/auth/token binds jwt on token/refresh_token and now on
// token_expires_at, and POST /api/v1/auth/userinfo binds identity on
// app_id. The document's other token/expiry-shaped property names —
// measured (see this repo's own working notes): "token" on
// PreSignOrganizationStatisticsToken, "refresh_token" on RefreshBodyData,
// "expire_count"/"expire_time" on CreateGeneralInviteBodyData and
// OrganizationGeneralInvite, "expiresAt" on generatedId — sit either in a
// REQUEST body schema or on a component never $ref'd from any operation's
// RESPONSE, so Derive's walk (op.SchemaPtr is always a response pointer)
// cannot reach any of them regardless of the isAuthPath gate. The jwt-kind
// assertion below is what would catch it if that ever stopped being true.
func TestP1cAcceptance_DeriveBindsTheThreeAuthPaths(t *testing.T) {
	fx := loadAcceptance(t)
	proposal := p1cDerive(t, fx)

	wantPaths := []string{"/api/v1/auth/refresh", "/api/v1/auth/token", "/api/v1/auth/userinfo"}
	if strings.Join(proposal.AuthPaths, ",") != strings.Join(wantPaths, ",") {
		t.Fatalf("AuthPaths = %v, want exactly %v", proposal.AuthPaths, wantPaths)
	}

	for _, b := range proposal.Bindings {
		if b.Recipe.Kind == recipes.KindJWT && !p1cContainsPath(wantPaths, b.Path) {
			t.Errorf("jwt binding on %s %s %s — not one of the three auth paths %v", b.Method, b.Path, b.DataPath, wantPaths)
		}
	}

	bindingsAt := func(method, path string, status int) []authpreset.Binding {
		var out []authpreset.Binding
		for _, b := range proposal.Bindings {
			if b.Method == method && b.Path == path && b.Status == status {
				out = append(out, b)
			}
		}
		return out
	}

	tokenBindings := bindingsAt("POST", "/api/v1/auth/token", 200)
	wantKinds := map[string]recipes.Kind{
		"response.token":            recipes.KindJWT,
		"response.refresh_token":    recipes.KindJWT,
		"response.token_expires_at": recipes.KindNow,
	}
	if len(tokenBindings) != len(wantKinds) {
		t.Fatalf("POST /api/v1/auth/token [200] bindings = %d, want %d: %+v", len(tokenBindings), len(wantKinds), tokenBindings)
	}
	for _, b := range tokenBindings {
		want, ok := wantKinds[b.DataPath]
		if !ok {
			t.Errorf("POST /api/v1/auth/token [200]: unexpected binding at %q", b.DataPath)
			continue
		}
		if b.Recipe.Kind != want {
			t.Errorf("POST /api/v1/auth/token [200] %s: kind = %s, want %s", b.DataPath, b.Recipe.Kind, want)
		}
	}

	userinfoBindings := bindingsAt("POST", "/api/v1/auth/userinfo", 200)
	if len(userinfoBindings) != 1 {
		t.Fatalf("POST /api/v1/auth/userinfo [200] bindings = %d, want 1: %+v", len(userinfoBindings), userinfoBindings)
	}
	if ub := userinfoBindings[0]; ub.DataPath != "response.app_id" || ub.Recipe.Kind != recipes.KindIdentity || ub.Recipe.Field != "org.id" {
		t.Fatalf("POST /api/v1/auth/userinfo [200] binding = %+v, want identity binding at response.app_id -> org.id", ub)
	}

	t.Logf("MEASUREMENTS derive: authPaths=%v totalBindings=%d schemes=%v", proposal.AuthPaths, len(proposal.Bindings), proposal.Schemes)
}

// --------------------------------------------------------------------------
// Assertion 2: applying the whole proposal — every schema-bearing variant
// of all 130 operations still generates and validates.
// --------------------------------------------------------------------------

// TestP1cAcceptance_ApplyWholeProposal_SchemaConformance mirrors
// TestAcceptance_SchemaConformance's own sweep (acceptance_test.go) EXACTLY
// — same selector/schema/media-type filter — with one difference: every
// [gen.Request] carries whatever *recipes.Set the derived-and-compiled
// proposal binds to its (operation, status), so this measures P1b's own
// 114-of-114 claim under P1c's recipes actually wired in, not merely
// P1b's original, recipe-free sweep run again.
func TestP1cAcceptance_ApplyWholeProposal_SchemaConformance(t *testing.T) {
	fx := loadAcceptance(t)
	proposal := p1cDerive(t, fx)
	compiled := p1cCompileBindings(t, proposal.Bindings)

	oracle := newSchemaOracle(t, fx.doc.Root())
	settings := p1cSettings()
	g := gen.New(fx.resolver, gen.Options{
		Seed: 777, ListSize: 8, NullRate: 0.15, MaxBytes: 4 << 20,
		Identity: settings.Identity, Auth: settings.Auth, Now: fixedClock,
	})

	var (
		opsAttempted   = map[int64]bool{}
		variantsTried  int
		generated      int
		validated      int
		recipeVariants int
		failures       []conformanceFailure
	)

	for _, op := range fx.ops {
		for _, v := range fx.variants[op.ID] {
			if !isGenuine2xxSelector(v.Selector) || v.SchemaPtr == "" || v.MediaType == "" {
				continue
			}
			opsAttempted[op.ID] = true
			variantsTried++

			req := gen.Request{
				Method: strings.ToUpper(op.Method), CanonicalPath: op.CanonicalPath,
				PathParams: samplePathParams(op.Path), Query: url.Values{}, Status: v.HTTPStatus,
				Recipes: p1cRecipesFor(compiled, op, v.HTTPStatus),
			}
			if req.Recipes != nil {
				recipeVariants++
			}

			body, err := g.Body(v, req)
			if err != nil {
				failures = append(failures, conformanceFailure{op.Method, op.Path, v.HTTPStatus, v.MediaType, "generation error", err.Error()})
				continue
			}
			generated++

			sch, cerr := oracle.compile(v.SchemaPtr)
			if cerr != nil {
				failures = append(failures, conformanceFailure{op.Method, op.Path, v.HTTPStatus, v.MediaType, "schema compile error",
					fmt.Sprintf("compile %s: %v", v.SchemaPtr, cerr)})
				continue
			}

			var instance any
			if strings.Contains(v.MediaType, "json") {
				if uerr := json.Unmarshal(body, &instance); uerr != nil {
					failures = append(failures, conformanceFailure{op.Method, op.Path, v.HTTPStatus, v.MediaType, "generated body is not valid JSON",
						fmt.Sprintf("%v (%s)", uerr, truncateForLog(body))})
					continue
				}
			} else {
				instance = string(body)
			}

			if verr := sch.Validate(instance); verr != nil {
				failures = append(failures, conformanceFailure{op.Method, op.Path, v.HTTPStatus, v.MediaType, "schema validation failed", verr.Error()})
				continue
			}
			validated++
		}
	}

	for _, f := range failures {
		t.Errorf("%s %s [%d, %s]: %s: %s", f.method, f.path, f.status, f.mediaType, f.reason, f.detail)
	}

	t.Logf("MEASUREMENTS apply-whole-proposal: operations-with-schema-2xx=%d variants-attempted=%d generated=%d validated=%d recipe-bound-variants=%d failures=%d",
		len(opsAttempted), variantsTried, generated, validated, recipeVariants, len(failures))

	if len(opsAttempted) == 0 {
		t.Fatal("no operation had a 2xx response with a schema — the assertion did not actually run")
	}
	if recipeVariants == 0 {
		t.Fatal("no attempted variant had a recipe bound — the applied proposal never touched anything this sweep covers")
	}
}

// --------------------------------------------------------------------------
// Assertion 3: HARD RULE 6 — with no recipes bound, every body must still
// match the pre-P1c golden.
// --------------------------------------------------------------------------

// TestP1cAcceptance_NoRecipesMatchesGolden is this file's own, self-
// contained proof of HARD RULE 6: it calls [goldenCorpus] — the Engine
// agent's own enumeration, declared once in golden_p1b_test.go and reused
// here rather than re-implemented, since a second hand-written walk of
// fx.ops/fx.variants could silently diverge from the one the golden file
// was actually captured with — and compares it against
// testdata/p1b_body_hashes.json byte for byte. TestGoldenP1bBodyHashes
// (golden_p1b_test.go) already asserts this same property; this is a
// second, independent caller of the same shared enumeration, which is
// exactly what that helper's own doc comment says it exists to support.
func TestP1cAcceptance_NoRecipesMatchesGolden(t *testing.T) {
	fx := loadAcceptance(t)
	corpus := goldenCorpus(t, fx)

	raw, err := os.ReadFile(goldenHashesPath)
	if err != nil {
		t.Fatalf("read golden %s: %v", goldenHashesPath, err)
	}
	var want goldenDocument
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatalf("decode golden %s: %v", goldenHashesPath, err)
	}

	mismatches := 0
	for key, gotHash := range corpus {
		wantHash, ok := want.Bodies[key]
		if !ok {
			t.Errorf("golden %s: variant %q is new — not present in the golden captured before this phase", goldenHashesPath, key)
			mismatches++
			continue
		}
		if wantHash != gotHash {
			t.Errorf("HARD RULE 6 violation: variant %q body hash changed (want %s, got %s) — a no-recipes request must produce byte-identical output to P1b",
				key, wantHash, gotHash)
			mismatches++
		}
	}
	for key := range want.Bodies {
		if _, ok := corpus[key]; !ok {
			t.Errorf("golden %s: variant %q from the golden is missing from this run's corpus", goldenHashesPath, key)
			mismatches++
		}
	}

	t.Logf("MEASUREMENTS p1c hard-rule-6 recheck: %d hashes compared, %d mismatches", len(corpus), mismatches)
	if len(corpus) == 0 {
		t.Fatal("golden corpus is empty — the assertion did not actually run")
	}
}

// --------------------------------------------------------------------------
// Assertion 4: determinism WITH recipes bound.
// --------------------------------------------------------------------------

// p1cBlankDotPath sets the leaf named by a plain dot-separated path (no
// "[*]"/"[N]" array segments) to a fixed placeholder, in place, inside a
// decoded JSON value. Measured on this document: every jwt/now binding the
// derived proposal produces is a flat top-level property under "response"
// ("response.token" and siblings) — never inside an array — so this
// deliberately does NOT reimplement internal/gen's whole path grammar. A
// path this cannot resolve (the wrong shape, an unexpected array segment)
// is silently left alone; the byte comparison this feeds then reports a
// mismatch instead of a panic, which is the right failure mode for an
// assumption about the document's shape that turned out to be wrong.
func p1cBlankDotPath(v any, path string) {
	segs := strings.Split(path, ".")
	cur := v
	for i, seg := range segs {
		m, ok := cur.(map[string]any)
		if !ok {
			return
		}
		if i == len(segs)-1 {
			if _, present := m[seg]; present {
				m[seg] = "<time-derived>"
			}
			return
		}
		cur = m[seg]
	}
}

// p1cBlankTimeDerivedFields decodes body, blanks every dataPath set bound
// to a jwt or now recipe (the only two kinds this phase derives that read
// the real clock — DESIGN §9 "Время"; const/identity/enum all derive from
// the seed or the fixed identity, never the clock), and re-serializes.
// encoding/json always sorts map keys on Marshal, so two calls over
// otherwise-identical decoded values produce byte-identical output
// regardless of the original bodies' own key order.
func p1cBlankTimeDerivedFields(t *testing.T, body []byte, set *recipes.Set) string {
	t.Helper()
	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode body to blank time-derived fields: %v; body=%s", err, truncateForLog(body))
	}
	for path, r := range set.Bindings() {
		if r.Kind != recipes.KindJWT && r.Kind != recipes.KindNow {
			continue
		}
		p1cBlankDotPath(decoded, path)
	}
	out, err := json.Marshal(decoded)
	if err != nil {
		t.Fatalf("re-marshal after blanking time-derived fields: %v", err)
	}
	return string(out)
}

// TestP1cAcceptance_DeterminismWithRecipes proves DESIGN §9's time split
// holds with recipes actually bound: two Generators sharing every Option
// EXCEPT the clock produce byte-identical primary-variant bodies for every
// operation whose recipes (if any) are all seed/identity-derived, and
// differ ONLY within the jwt/now-bound fields for the operation(s) that do
// carry a time-derived recipe — proving those fields track the real clock
// rather than freezing into the seed, without weakening the "nothing else
// moves" half of the same claim.
func TestP1cAcceptance_DeterminismWithRecipes(t *testing.T) {
	fx := loadAcceptance(t)
	proposal := p1cDerive(t, fx)
	compiled := p1cCompileBindings(t, proposal.Bindings)

	settings := p1cSettings()
	now1 := fixedClock()
	now2 := fixedClock().Add(2 * time.Hour)
	baseOpts := gen.Options{Seed: 4242, ListSize: 6, NullRate: 0.1, MaxBytes: 4 << 20, Identity: settings.Identity, Auth: settings.Auth}
	opts1, opts2 := baseOpts, baseOpts
	opts1.Now = func() time.Time { return now1 }
	opts2.Now = func() time.Time { return now2 }

	g1 := gen.New(fx.resolver, opts1)
	g2 := gen.New(fx.resolver, opts2)

	var (
		compared, identical, differing int
		differingOps                   []string
	)

	for _, op := range fx.ops {
		v, ok := pickPrimaryVariant(fx.variants[op.ID])
		if !ok {
			continue
		}
		req := gen.Request{
			Method: strings.ToUpper(op.Method), CanonicalPath: op.CanonicalPath,
			PathParams: samplePathParams(op.Path), Query: url.Values{}, Status: v.HTTPStatus,
			Recipes: p1cRecipesFor(compiled, op, v.HTTPStatus),
		}
		b1, err1 := g1.Body(v, req)
		b2, err2 := g2.Body(v, req)
		if err1 != nil || err2 != nil {
			continue // TestP1cAcceptance_ApplyWholeProposal_SchemaConformance already reports generation errors by name
		}
		compared++
		if string(b1) == string(b2) {
			identical++
			continue
		}
		differing++
		differingOps = append(differingOps, fmt.Sprintf("%s %s [%d]", op.Method, op.Path, v.HTTPStatus))

		if req.Recipes == nil {
			t.Errorf("%s %s [%d]: bodies differ across two clocks with NO recipe bound — every seed/identity-derived field must be clock-independent",
				op.Method, op.Path, v.HTTPStatus)
			continue
		}
		n1 := p1cBlankTimeDerivedFields(t, b1, req.Recipes)
		n2 := p1cBlankTimeDerivedFields(t, b2, req.Recipes)
		if n1 != n2 {
			t.Errorf("%s %s [%d]: bodies differ beyond their recipe-bound jwt/now fields:\n  g1=%s\n  g2=%s",
				op.Method, op.Path, v.HTTPStatus, truncateForLog([]byte(n1)), truncateForLog([]byte(n2)))
		}
	}

	t.Logf("MEASUREMENTS determinism-with-recipes: compared=%d identical=%d differing=%d differingOps=%v", compared, identical, differing, differingOps)
	if compared == 0 {
		t.Fatal("no operation was compared — the assertion did not actually run")
	}
	if differing == 0 {
		t.Fatal("expected at least one operation's primary variant (an auth path with a jwt/now recipe bound) to differ between two clocks — " +
			"recipes.MintJWT/NowValue derive from the real clock by design (DESIGN §9 \"Время\")")
	}
}
