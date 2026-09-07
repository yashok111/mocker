// repo_nested_test.go: nested families, one level and P3g's deep nesting
// (depth two and three) — ancestor-tuple population, entity-key uniqueness
// across every scope, the depth-three over-cap refusal leaving the rest of
// the tree alone, scope-param names, cascade columns staying null, and an
// ancestor swap caught element-wise. Split out of the former repo_test.go
// (package resources, not resources_test — see helpers_test.go's own
// comment for why).
package resources

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/testkit"
	"github.com/yashok111/mocker/internal/testspec"
)

// TestConfirm_NestedFamily_ZeroLiveParents_PopulatesNothing is the P=0 edge
// of D5.3's "one row set per live ancestor tuple" — a nested family
// confirmed while its already-confirmed parent has ZERO live entities
// (every row deleted through the ordinary Delete route, exactly as an
// operator would) must generate zero row sets, not one implicit unscoped
// batch. [chainScopes] is what has to get this right now: an empty
// keysByParent at the parent's own level makes [extendScopes] fan out to
// zero tuples, so the whole scope list this family populates against is
// empty — never falling back to the single top-level scope [""], which
// would write seedCount rows at scope_key="", a scope no live parent key
// will ever anchor (D6.3): permanent garbage rows counted by
// [Repo.CountEntities] but unreachable through any GET.
// familyOrgs/familyOrgUsers/nestedFixtureDoc are declared in
// nested_test.go, same package.
func TestConfirm_NestedFamily_ZeroLiveParents_PopulatesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := testkit.NewDBAt(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, []byte(nestedFixtureDoc))
	wsID := insertWorkspace(t, db, "acme", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	org, err := repo.Confirm(t.Context(), wsID, familyOrgs)
	if err != nil {
		t.Fatalf("confirm parent %q: %v", familyOrgs, err)
	}
	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("list org entities: %v", err)
	}
	for _, e := range orgEntities {
		if _, derr := repo.Delete(t.Context(), org.ID, "", "", e.EntityKey); derr != nil {
			t.Fatalf("delete org entity %q: %v", e.EntityKey, derr)
		}
	}
	if got := entityCount(t, repo.db, org.ID); got != 0 {
		t.Fatalf("org entity count after deleting every row = %d, want 0", got)
	}

	user, err := repo.Confirm(t.Context(), wsID, familyOrgUsers)
	if err != nil {
		t.Fatalf("confirm child %q over a parent with zero live rows: %v", familyOrgUsers, err)
	}
	if got := entityCount(t, repo.db, user.ID); got != 0 {
		t.Fatalf("user entity count = %d, want 0 (P=0 live parents)", got)
	}
	// D5.5 point 4: Seq is the family-wide TOTAL just inserted (P×L = 0),
	// SeedCount stays the PER-SCOPE count clampSeedCount(listSize) computes
	// from settings alone (L=2) — P=0 changes how many scopes exist, not
	// what a single scope's own count is.
	if user.Seq != 0 {
		t.Fatalf("user.Seq = %d, want 0 (P=0 * L=2)", user.Seq)
	}
	if user.SeedCount != 2 {
		t.Fatalf("user.SeedCount = %d, want 2", user.SeedCount)
	}
	entities, err := repo.List(t.Context(), user.ID, "", "")
	if err != nil {
		t.Fatalf("list user entities: %v", err)
	}
	if len(entities) != 0 {
		t.Fatalf("user entities = %v, want none", entities)
	}
}

// TestConfirm_DepthTwo_ImmediateParentNotConfirmed_Refuses is P4: confirming
// a depth-2 family (users) whose IMMEDIATE parent (teams) is not confirmed
// answers 409 parent_not_confirmed and writes no resources row — even
// though the ROOT (orgs) two levels up IS confirmed. Mutation this fails
// under: deleting the CONFIRMED predicate from the parent read in
// [Repo.prepareConfirm] (D5.1's own single-hop check).
func TestConfirm_DepthTwo_ImmediateParentNotConfirmed_Refuses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := testkit.NewDBAt(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, testspec.DeepNestingDoc())
	wsID := insertWorkspace(t, db, "acme", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	if _, err := repo.Confirm(t.Context(), wsID, testspec.FamilyDeepOrgs); err != nil {
		t.Fatalf("confirm root: %v", err)
	}
	// FamilyDeepTeams (the immediate parent of users) is deliberately NOT
	// confirmed.
	if _, err := repo.Confirm(t.Context(), wsID, testspec.FamilyDeepUsers); !errors.Is(err, ErrParentNotConfirmed) {
		t.Fatalf("confirm depth-2 family with root confirmed but immediate parent not = %v, want ErrParentNotConfirmed", err)
	}
	if n := resourceRowCount(t, db, wsID, testspec.FamilyDeepUsers); n != 0 {
		t.Errorf("resource row written on a refused confirm, want none")
	}
	// P4's own second assertion is deliberately NOT a row COUNT: with no
	// confirmed parent there are no scopes and the population is empty
	// under both a correct and a wrong implementation.
}

// TestConfirm_DepthTwo_PopulatesOneRowSetPerAncestorTuple is P5: a depth-2
// family's population is one row set per LIVE ancestor TUPLE, not per
// immediate-parent key alone (D5.3). At L=2, orgs=2, teams=2 per org (4
// total, over 2 one-value scopes), users=2 per team over 4 DISTINCT
// two-value scopes — never all users collapsing onto one scope keyed by
// the innermost value alone, which is the mutation P5 names
// (EncodeScope([]string{key}), the deleted P3e line).
func TestConfirm_DepthTwo_PopulatesOneRowSetPerAncestorTuple(t *testing.T) {
	t.Parallel()
	repo, org, teams, users, _ := confirmDeepChain(t, 2)

	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("list orgs: %v", err)
	}
	if len(orgEntities) != 2 {
		t.Fatalf("orgs = %d, want 2", len(orgEntities))
	}

	seenScopes := map[ScopeKey]bool{}
	totalTeams, totalUsers := 0, 0
	for _, orgE := range orgEntities {
		teamEntities, terr := repo.List(t.Context(), teams.ID, "", EncodeScope([]string{orgE.EntityKey}))
		if terr != nil {
			t.Fatalf("list teams under org %q: %v", orgE.EntityKey, terr)
		}
		if len(teamEntities) != 2 {
			t.Fatalf("teams under org %q = %d, want 2", orgE.EntityKey, len(teamEntities))
		}
		totalTeams += len(teamEntities)

		for _, teamE := range teamEntities {
			scope := EncodeScope([]string{orgE.EntityKey, teamE.EntityKey})
			if seenScopes[scope] {
				t.Fatalf("scope %q seen under two different teams — tuples are not distinct", scope)
			}
			seenScopes[scope] = true

			userEntities, uerr := repo.List(t.Context(), users.ID, "", scope)
			if uerr != nil {
				t.Fatalf("list users under scope %q: %v", scope, uerr)
			}
			if len(userEntities) != 2 {
				t.Fatalf("users under scope %q = %d, want 2 (D5.3: one row set per live ancestor tuple)", scope, len(userEntities))
			}
			totalUsers += len(userEntities)
		}
	}
	if totalTeams != 4 {
		t.Fatalf("total teams = %d, want 4 (2 orgs * 2 teams)", totalTeams)
	}
	if len(seenScopes) != 4 {
		t.Fatalf("distinct user scopes = %d, want 4", len(seenScopes))
	}
	if totalUsers != 8 {
		t.Fatalf("total users = %d, want 8 (4 team scopes * 2 users)", totalUsers)
	}
}

// TestConfirm_DepthTwo_EntityKeyUniqueAcrossEveryScope is P6: entity_key
// stays unique FAMILY-WIDE across every one of a depth-2 family's scopes —
// keys "1".."8" with no repeat across the 4 scopes — and the next POST,
// into any scope, mints the family-wide next key "9", never restarting per
// scope. Mutation this fails under: restarting the per-scope counter at 1
// (every scope would then hold "1","2", and the UNIQUE index — which
// includes scope_key — would not complain).
func TestConfirm_DepthTwo_EntityKeyUniqueAcrossEveryScope(t *testing.T) {
	t.Parallel()
	repo, org, teams, users, _ := confirmDeepChain(t, 2)

	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("list orgs: %v", err)
	}
	seenKeys := map[string]bool{}
	var firstScope ScopeKey
	for _, orgE := range orgEntities {
		teamEntities, terr := repo.List(t.Context(), teams.ID, "", EncodeScope([]string{orgE.EntityKey}))
		if terr != nil {
			t.Fatalf("list teams under org %q: %v", orgE.EntityKey, terr)
		}
		for _, teamE := range teamEntities {
			scope := EncodeScope([]string{orgE.EntityKey, teamE.EntityKey})
			if firstScope == "" {
				firstScope = scope
			}
			userEntities, uerr := repo.List(t.Context(), users.ID, "", scope)
			if uerr != nil {
				t.Fatalf("list users under scope %q: %v", scope, uerr)
			}
			for _, u := range userEntities {
				if seenKeys[u.EntityKey] {
					t.Fatalf("entity_key %q repeated across scopes — not unique family-wide", u.EntityKey)
				}
				seenKeys[u.EntityKey] = true
			}
		}
	}
	for _, want := range []string{"1", "2", "3", "4", "5", "6", "7", "8"} {
		if !seenKeys[want] {
			t.Errorf("entity_key %q missing from the family-wide key set %v", want, seenKeys)
		}
	}
	if len(seenKeys) != 8 {
		t.Fatalf("distinct entity_key count = %d, want 8", len(seenKeys))
	}

	created, err := repo.Create(t.Context(), users.ID, "", firstScope, users.IDField, users.Wrapper.IDType, map[string]any{"name": "extra"})
	if err != nil {
		t.Fatalf("create into scope %q: %v", firstScope, err)
	}
	if created.EntityKey != "9" {
		t.Fatalf("POST after a depth-2 confirm minted entityKey %q, want \"9\" (family-wide next key)", created.EntityKey)
	}
}

// TestConfirm_DepthThree_OverCap_RefusesDeepestFamily_LeavesRestAlone is
// P7: a confirm whose prepared population exceeds the row cap answers 409
// entity_limit and writes nothing — for the FAMILY THAT ACTUALLY EXCEEDS
// IT, not the whole chain. At L=6 (D2's own table), depths 0..3 hold 6,
// 36, 216 and 1296 rows — only the deepest (badges, depth 3) crosses
// defaultMaxEntityRows (1000); orgs/teams/users must stay confirmed and untouched
// (D5.4: "the refusal lands on the DEEPEST family... nothing rolls back").
func TestConfirm_DepthThree_OverCap_RefusesDeepestFamily_LeavesRestAlone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := testkit.NewDBAt(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, testspec.DeepNestingDoc())
	wsID := insertWorkspace(t, db, "acme", &specID, domain.Settings{Seed: 1, ListSize: 6})
	// Generous byte caps: only the ROW-COUNT cap (1000) is meant to fire
	// here, not either byte cap incidentally.
	repo := newTestRepo(t, db, 64<<20, 64<<10)

	for _, fam := range []string{testspec.FamilyDeepOrgs, testspec.FamilyDeepTeams, testspec.FamilyDeepUsers} {
		if _, err := repo.Confirm(t.Context(), wsID, fam); err != nil {
			t.Fatalf("confirm %q: %v", fam, err)
		}
	}

	if _, err := repo.Confirm(t.Context(), wsID, testspec.FamilyDeepBadges); !errors.Is(err, ErrEntityLimit) {
		t.Fatalf("confirm badges (1296 rows over the 1000 cap) = %v, want ErrEntityLimit", err)
	}
	if n := resourceRowCount(t, db, wsID, testspec.FamilyDeepBadges); n != 0 {
		t.Errorf("badges resources row written on a refused confirm, want none")
	}
	if got := entityCount(t, db, mustResourceID(t, db, wsID, testspec.FamilyDeepUsers)); got != 216 {
		t.Errorf("users entity count changed by an unrelated refusal deeper in the chain, got %d, want 216", got)
	}
}

// TestDecline_Root_WithConfirmedGrandchild_RefusesNamingMiddle is P11: a
// three-level chain confirmed (orgs/teams/users), declining the ROOT
// refuses with 409 child_confirmed and the message names the MIDDLE family
// (teams — orgs' own DIRECT child), never the grandchild two levels down.
// D5.2's induction proof is what makes the existing SINGLE-HOP check
// (unchanged by this slice) correct at this depth: declining leaf, then
// middle, then root then succeeds in that order.
func TestDecline_Root_WithConfirmedGrandchild_RefusesNamingMiddle(t *testing.T) {
	t.Parallel()
	repo, _, _, _, wsID := confirmDeepChain(t, 2)

	err := repo.Decline(t.Context(), wsID, testspec.FamilyDeepOrgs, "acme")
	if !errors.Is(err, ErrChildConfirmed) {
		t.Fatalf("decline root with a depth-2 grandchild confirmed = %v, want ErrChildConfirmed", err)
	}
	if !strings.Contains(err.Error(), testspec.FamilyDeepTeams) {
		t.Errorf("ErrChildConfirmed message = %q, want it to name the MIDDLE family %q", err, testspec.FamilyDeepTeams)
	}
	if strings.Contains(err.Error(), testspec.FamilyDeepUsers) {
		t.Errorf("ErrChildConfirmed message = %q, names the GRANDCHILD too — D5.1 stays single-hop", err)
	}

	if err := repo.Decline(t.Context(), wsID, testspec.FamilyDeepUsers, "acme"); err != nil {
		t.Fatalf("decline leaf: %v", err)
	}
	if err := repo.Decline(t.Context(), wsID, testspec.FamilyDeepTeams, "acme"); err != nil {
		t.Fatalf("decline middle: %v", err)
	}
	if err := repo.Decline(t.Context(), wsID, testspec.FamilyDeepOrgs, "acme"); err != nil {
		t.Fatalf("decline root after both descendants are gone: %v", err)
	}
}

// TestConfirm_DepthTwo_ScopeParamsHoldDetailRouteNames is P15: scope_params
// holds the DETAIL route's own outer parameter NAMES, in order —
// ["organizationId", "team"] on [testspec.DeepNestingDoc]'s depth-2 family,
// whose detail route spells BOTH outer parameters differently from its
// collection route ("orgId"/"teamId"). Asserted by VALUE, deliberately not
// by length: [outerParamNames] drops the last path SEGMENT by position, so
// both the collection and the detail route give exactly two names at this
// depth — a length assertion is green under a mutation that reads the
// WRONG route's spelling (D13's own warning: an earlier draft of this
// property asserted the length and was green against the very
// implementation it names).
func TestConfirm_DepthTwo_ScopeParamsHoldDetailRouteNames(t *testing.T) {
	t.Parallel()
	_, _, _, users, _ := confirmDeepChain(t, 2)
	want := []string{"organizationId", "team"}
	if !slices.Equal(users.ScopeParams, want) {
		t.Fatalf("users.ScopeParams = %v, want %v (the DETAIL route's own spelling)", users.ScopeParams, want)
	}
}

// TestConfirm_DeepChain_CascadeColumnsStayNull is P23: both
// resources.parent_id and entities.parent_entity_id stay NULL on every row
// of a three-level confirmed chain, including a row written through the
// ordinary POST path afterward (D9 — a decision, not a deferral: see
// "Architecture" in CLAUDE.md for the argument in full). Mutation this
// fails under, one at a time: writing the parent's resources.id into
// resources.parent_id at confirm ([insertConfirmedResourceTx], which
// hard-codes NULL today), or writing the anchoring parent row's entities.id
// into entities.parent_entity_id at population (Confirm's own entity
// INSERT, which never names that column at all).
func TestConfirm_DeepChain_CascadeColumnsStayNull(t *testing.T) {
	t.Parallel()
	repo, org, teams, users, _ := confirmDeepChain(t, 2)

	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("list orgs: %v", err)
	}
	teamEntities, err := repo.List(t.Context(), teams.ID, "", EncodeScope([]string{orgEntities[0].EntityKey}))
	if err != nil {
		t.Fatalf("list teams: %v", err)
	}
	scope := EncodeScope([]string{orgEntities[0].EntityKey, teamEntities[0].EntityKey})
	if _, err := repo.Create(t.Context(), users.ID, "", scope, users.IDField, users.Wrapper.IDType, map[string]any{"name": "extra"}); err != nil {
		t.Fatalf("create extra user via the ordinary POST path: %v", err)
	}

	var resourcesWithParent, entitiesWithParent int
	if err := repo.db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM resources WHERE parent_id IS NOT NULL").Scan(&resourcesWithParent); err != nil {
		t.Fatalf("count resources.parent_id: %v", err)
	}
	if err := repo.db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entities WHERE parent_entity_id IS NOT NULL").Scan(&entitiesWithParent); err != nil {
		t.Fatalf("count entities.parent_entity_id: %v", err)
	}
	if resourcesWithParent != 0 {
		t.Errorf("resources.parent_id non-null rows = %d, want 0 (D9)", resourcesWithParent)
	}
	if entitiesWithParent != 0 {
		t.Errorf("entities.parent_entity_id non-null rows = %d, want 0 (D9)", entitiesWithParent)
	}
}

// TestConfirm_NestedFamily_AncestorSwap_CaughtElementWise is P24: the
// confirm fence ([Repo.fenceParentTx]) compares the recomputed scope list
// ELEMENT-WISE, not by length. Swapping the root's SECOND row for a fresh
// one (delete, then create — never reusing the deleted key, since
// [allocateSeq] never reissues) keeps the scope COUNT at 2 but changes the
// tuple at that position; a length-only fence would miss it. The swap
// lands inside [confirmPreWriteHook]'s own window — after
// [Repo.prepareConfirm]'s generation half reads the live keys, before the
// write transaction re-reads them — so it races [Repo.fenceParentTx]
// itself, not a bespoke stand-in for it.
func TestConfirm_NestedFamily_AncestorSwap_CaughtElementWise(t *testing.T) {
	dir := t.TempDir()
	db := testkit.NewDBAt(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, testspec.DeepNestingDoc())
	wsID := insertWorkspace(t, db, "acme", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	org, err := repo.Confirm(t.Context(), wsID, testspec.FamilyDeepOrgs)
	if err != nil {
		t.Fatalf("confirm root: %v", err)
	}

	confirmPreWriteHook = func() {
		if _, derr := repo.Delete(t.Context(), org.ID, "", "", "2"); derr != nil {
			t.Errorf("swap: delete org entity 2: %v", derr)
		}
		if _, cerr := repo.Create(t.Context(), org.ID, "", "", org.IDField, org.Wrapper.IDType, map[string]any{"name": "swapped org"}); cerr != nil {
			t.Errorf("swap: create replacement org entity: %v", cerr)
		}
	}
	t.Cleanup(func() { confirmPreWriteHook = confirmPreWriteHookNoop })

	if _, err := repo.Confirm(t.Context(), wsID, testspec.FamilyDeepTeams); !errors.Is(err, ErrStaleConfig) {
		t.Fatalf("confirm over a swapped ancestor row = %v, want ErrStaleConfig", err)
	}
	if n := resourceRowCount(t, db, wsID, testspec.FamilyDeepTeams); n != 0 {
		t.Errorf("teams resources row written despite the swap being refused")
	}
}
