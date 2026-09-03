// Tests for P3e: nested families (decisions.md, mocker-p3e-nested). package
// resources, same reason repo_test.go gives — several assertions here reach
// for [Repo.List]/[Repo.Get] with an explicit [ScopeKey] and for the
// unexported entity-count helper, which package resources_test could not do.
package resources

import (
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
)

const (
	familyOrgs     = "/orgs"
	familyOrgUsers = "/orgs/{}/users"
)

// nestedFixtureDoc declares ONE level of nesting — this FIXTURE's own
// shape, not a bound on what the package supports (P3g raises the ceiling
// to three, decisions.md D3.1; [testspec.DeepNestingDoc] is what exercises
// depth 2/3): /orgs (a top-level family, id_field "id") and
// /orgs/{orgId}/users (its own family, one outer parameter "orgId" — D5.6's
// example verbatim). Both POST-taking, so the nested write path is
// exercised by the same fixture that proves the read path.
const nestedFixtureDoc = `{
  "openapi": "3.0.3",
  "info": {"title": "nested fixture", "version": "1.0.0"},
  "paths": {
    "/orgs": {
      "get": {
        "operationId": "listOrgs",
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"type": "array", "items": {"$ref": "#/components/schemas/Org"}}
        }}}}
      },
      "post": {
        "operationId": "createOrg",
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Org"}}}},
        "responses": {"201": {"description": "d", "content": {"application/json": {
          "schema": {"$ref": "#/components/schemas/Org"}
        }}}}
      }
    },
    "/orgs/{id}": {
      "get": {
        "operationId": "getOrg",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"$ref": "#/components/schemas/Org"}
        }}}}
      }
    },
    "/orgs/{orgId}/users": {
      "get": {
        "operationId": "listOrgUsers",
        "parameters": [{"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"type": "array", "items": {"$ref": "#/components/schemas/OrgUser"}}
        }}}}
      },
      "post": {
        "operationId": "createOrgUser",
        "parameters": [{"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/OrgUser"}}}},
        "responses": {"201": {"description": "d", "content": {"application/json": {
          "schema": {"$ref": "#/components/schemas/OrgUser"}
        }}}}
      }
    },
    "/orgs/{orgId}/users/{id}": {
      "get": {
        "operationId": "getOrgUser",
        "parameters": [
          {"name": "orgId", "in": "path", "required": true, "schema": {"type": "integer"}},
          {"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}
        ],
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"$ref": "#/components/schemas/OrgUser"}
        }}}}
      }
    }
  },
  "components": {"schemas": {
    "Org": {"type": "object", "required": ["id", "name"], "properties": {
      "id": {"type": "integer"}, "name": {"type": "string"}
    }},
    "OrgUser": {"type": "object", "required": ["id", "name"], "properties": {
      "id": {"type": "integer"}, "name": {"type": "string"}
    }}
  }}
}`

// confirmNested imports nestedFixtureDoc, confirms /orgs then
// /orgs/{}/users at listSize L (so P=L=2 reproduces D5.5 point 4's own
// pinned P8d/P8e/P8g fixture), and returns both resources.
func confirmNested(t *testing.T, listSize int) (repo *Repo, org, user *Resource, wsID int64) {
	t.Helper()
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, []byte(nestedFixtureDoc))
	wsID = insertWorkspace(t, db, "acme", &specID, domain.Settings{Seed: 1, ListSize: listSize})
	repo = newTestRepo(t, db, 4<<20, 64<<10)

	org, err := repo.Confirm(t.Context(), wsID, familyOrgs)
	if err != nil {
		t.Fatalf("confirm parent %q: %v", familyOrgs, err)
	}
	user, err = repo.Confirm(t.Context(), wsID, familyOrgUsers)
	if err != nil {
		t.Fatalf("confirm child %q: %v", familyOrgUsers, err)
	}
	return repo, org, user, wsID
}

// TestConfirm_NestedFamily_PopulatesPerScope is D5.3/D5.5 end to end: P=2
// live orgs, L=2 users each, so the child gets 4 rows over two scopes,
// keyed "1","2" then "3","4" (D5.5's own worked example) — never restarting
// per scope.
func TestConfirm_NestedFamily_PopulatesPerScope(t *testing.T) {
	t.Parallel()
	repo, org, user, _ := confirmNested(t, 2)

	if got := entityCount(t, repo.db, org.ID); got != 2 {
		t.Fatalf("org entity count = %d, want 2", got)
	}
	if got := entityCount(t, repo.db, user.ID); got != 4 {
		t.Fatalf("user entity count = %d, want 4 (P=2 * L=2)", got)
	}

	// D5.5 point 4: seq is the family-wide TOTAL (4), seed_count the
	// PER-SCOPE count (2) — the two quantities a wrong implementation
	// conflates.
	if user.Seq != 4 {
		t.Errorf("child Seq = %d, want 4 (P*L, D5.5 point 4)", user.Seq)
	}
	if user.SeedCount != 2 {
		t.Errorf("child SeedCount = %d, want 2 (per-scope L)", user.SeedCount)
	}
	if len(user.ScopeParams) != 1 || user.ScopeParams[0] != "orgId" {
		t.Errorf("child ScopeParams = %v, want [\"orgId\"] (D5.6)", user.ScopeParams)
	}

	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("List orgs: %v", err)
	}
	if len(orgEntities) != 2 {
		t.Fatalf("org entities = %d, want 2", len(orgEntities))
	}

	// Every scope is exactly one org's own EntityKey, and inside it the
	// child's own entity_key numbering continues family-wide rather than
	// restarting: scope orgEntities[0].EntityKey holds "1","2", scope
	// orgEntities[1].EntityKey holds "3","4".
	wantKeysByScope := map[string][]string{
		orgEntities[0].EntityKey: {"1", "2"},
		orgEntities[1].EntityKey: {"3", "4"},
	}
	for scopeVal, wantKeys := range wantKeysByScope {
		scoped, err := repo.List(t.Context(), user.ID, "", EncodeScope([]string{scopeVal}))
		if err != nil {
			t.Fatalf("List scope %q: %v", scopeVal, err)
		}
		if len(scoped) != len(wantKeys) {
			t.Fatalf("scope %q entities = %d, want %d", scopeVal, len(scoped), len(wantKeys))
		}
		for i, e := range scoped {
			if e.EntityKey != wantKeys[i] {
				t.Errorf("scope %q entity %d key = %q, want %q", scopeVal, i, e.EntityKey, wantKeys[i])
			}
		}
	}

	if got, err := repo.CountEntities(t.Context(), user.ID); err != nil || got != 4 {
		t.Errorf("CountEntities(child) = %d, %v, want 4, nil (D6.2)", got, err)
	}
}

// TestConfirm_NestedFamily_AncestorWalkWithinBaseValue is P3h's P5: a
// nested family's ancestor walk reads rows WITHIN the base value, never
// across all of them. Two declared base values, P=2 orgs (L=2) confirmed
// under each: entity_key is ONE counter across the whole /orgs family
// (D6.2), so base "7" mints org keys "1"/"2" and base "8" mints "3"/"4" —
// disjoint by construction. The wrong implementation D6.1 names as most
// likely (building the child's ancestor tuples from the parent's rows read
// ACROSS all base scopes) would scope /orgs/{}/users under base "7" to
// keys "1","2","3","4" instead of just "1","2" — this test asserts the
// child's scope keys under base "7" are built from base "7"'s own org
// keys ONLY, and likewise for base "8".
func TestConfirm_NestedFamily_AncestorWalkWithinBaseValue(t *testing.T) {
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, []byte(nestedFixtureDoc))
	wsID := insertWorkspace(t, db, "acme", &specID, domain.Settings{
		Seed: 1, ListSize: 2, BasePath: "/tenants/{tenantId}", BasePathValues: []string{"7", "8"},
	})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	org, err := repo.Confirm(t.Context(), wsID, familyOrgs)
	if err != nil {
		t.Fatalf("confirm parent %q: %v", familyOrgs, err)
	}
	user, err := repo.Confirm(t.Context(), wsID, familyOrgUsers)
	if err != nil {
		t.Fatalf("confirm child %q: %v", familyOrgUsers, err)
	}

	base7, base8 := ScopeKey("7"), ScopeKey("8")

	orgsUnder7, err := repo.List(t.Context(), org.ID, base7, "")
	if err != nil {
		t.Fatalf("List orgs under base 7: %v", err)
	}
	orgsUnder8, err := repo.List(t.Context(), org.ID, base8, "")
	if err != nil {
		t.Fatalf("List orgs under base 8: %v", err)
	}
	if len(orgsUnder7) != 2 || len(orgsUnder8) != 2 {
		t.Fatalf("orgs under base7=%d base8=%d, want 2 and 2", len(orgsUnder7), len(orgsUnder8))
	}
	// entity_key is family-wide (D6.2): base 7's org keys and base 8's
	// must be disjoint, which is what makes the property observable at
	// all — see TestList_Get_Create_Delete_DisjointAcrossBaseValues's own
	// comment on why the "same key under two base scopes" fixture is
	// unwritable through any path.
	keys7 := map[string]bool{orgsUnder7[0].EntityKey: true, orgsUnder7[1].EntityKey: true}
	for _, o := range orgsUnder8 {
		if keys7[o.EntityKey] {
			t.Fatalf("org key %q present under BOTH base 7 and base 8 — the fixture cannot distinguish the property", o.EntityKey)
		}
	}

	// The child under base 7 must be scoped ONLY to base 7's own org keys.
	for _, o := range orgsUnder7 {
		scoped, err := repo.List(t.Context(), user.ID, base7, EncodeScope([]string{o.EntityKey}))
		if err != nil {
			t.Fatalf("List child under base7 scope %q: %v", o.EntityKey, err)
		}
		if len(scoped) != 2 {
			t.Errorf("child rows under base7/org %q = %d, want listSize 2", o.EntityKey, len(scoped))
		}
	}
	// And NEVER scoped to base 8's org keys while addressed under base 7 —
	// the wrong implementation's own symptom.
	for _, o := range orgsUnder8 {
		scoped, err := repo.List(t.Context(), user.ID, base7, EncodeScope([]string{o.EntityKey}))
		if err != nil {
			t.Fatalf("List child under base7 scope %q (base8's own org key): %v", o.EntityKey, err)
		}
		if len(scoped) != 0 {
			t.Fatalf("child rows under base7/org %q (a base8 org key) = %d, want 0 — the ancestor walk crossed base scopes", o.EntityKey, len(scoped))
		}
	}
	// The mirror image under base 8.
	for _, o := range orgsUnder8 {
		scoped, err := repo.List(t.Context(), user.ID, base8, EncodeScope([]string{o.EntityKey}))
		if err != nil {
			t.Fatalf("List child under base8 scope %q: %v", o.EntityKey, err)
		}
		if len(scoped) != 2 {
			t.Errorf("child rows under base8/org %q = %d, want listSize 2", o.EntityKey, len(scoped))
		}
	}
	if got := entityCount(t, repo.db, user.ID); got != 8 {
		t.Fatalf("child entity count = %d, want 8 (2 base values * 2 orgs * listSize 2)", got)
	}
}

// TestConfirm_NestedFamily_ParentNotConfirmed is D5.1/D5.2: confirming the
// child before the parent refuses, and writes nothing.
func TestConfirm_NestedFamily_ParentNotConfirmed(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, []byte(nestedFixtureDoc))
	wsID := insertWorkspace(t, db, "acme", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	if _, err := repo.Confirm(t.Context(), wsID, familyOrgUsers); !errors.Is(err, ErrParentNotConfirmed) {
		t.Fatalf("Confirm child with no parent = %v, want ErrParentNotConfirmed", err)
	}
	if n := resourceRowCount(t, db, wsID, familyOrgUsers); n != 0 {
		t.Errorf("resource row written on a refused confirm, want none")
	}
}

// TestDecline_ChildConfirmed is D7.1/D7.2: a parent with a confirmed child
// refuses, deleting neither family's rows; declining the child first makes
// the parent's decline succeed (D7.3's ordering, seen from the other end
// of D5.1's).
func TestDecline_ChildConfirmed(t *testing.T) {
	t.Parallel()
	repo, org, user, wsID := confirmNested(t, 2)

	err := repo.Decline(t.Context(), wsID, familyOrgs, "acme")
	if !errors.Is(err, ErrChildConfirmed) {
		t.Fatalf("Decline parent with confirmed child = %v, want ErrChildConfirmed", err)
	}
	if !strings.Contains(err.Error(), familyOrgUsers) {
		t.Errorf("ErrChildConfirmed message = %q, want it to name %q", err, familyOrgUsers)
	}
	if n := resourceRowCount(t, repo.db, wsID, familyOrgs); n != 1 {
		t.Errorf("parent resource row deleted on a refused decline, want it to stay")
	}
	if got := entityCount(t, repo.db, user.ID); got != 4 {
		t.Errorf("child entities changed by a refused parent decline, count = %d, want 4", got)
	}

	if err := repo.Decline(t.Context(), wsID, familyOrgUsers, "acme"); err != nil {
		t.Fatalf("decline child: %v", err)
	}
	if err := repo.Decline(t.Context(), wsID, familyOrgs, "acme"); err != nil {
		t.Fatalf("decline parent after its child is gone: %v", err)
	}
	if n := resourceRowCount(t, repo.db, wsID, familyOrgs); n != 0 {
		t.Errorf("parent resource row survived its own decline")
	}
	_ = org
}

// TestCreate_NestedFamily_ScopedReadBack is P8c: a successful nested POST
// stores its row in the REQUEST's own scope and can be read back through
// that same scope — the property that fails silently (INSERT commits, then
// the transaction rolls back on the read-back miss) if D17.4 site 6
// ([lastEntityID]'s own scope predicate) is ever dropped.
func TestCreate_NestedFamily_ScopedReadBack(t *testing.T) {
	t.Parallel()
	repo, org, user, _ := confirmNested(t, 2)

	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("List orgs: %v", err)
	}
	scopeA := EncodeScope([]string{orgEntities[0].EntityKey})
	scopeB := EncodeScope([]string{orgEntities[1].EntityKey})

	created, err := repo.Create(t.Context(), user.ID, "", scopeA, user.IDField, user.Wrapper.IDType, map[string]any{"name": "posted"})
	if err != nil {
		t.Fatalf("Create into scope A: %v", err)
	}

	if _, ok, err := repo.Get(t.Context(), user.ID, "", scopeA, created.EntityKey); err != nil || !ok {
		t.Fatalf("Get back in the SAME scope = ok=%v err=%v, want true, nil", ok, err)
	}
	if _, ok, err := repo.Get(t.Context(), user.ID, "", scopeB, created.EntityKey); err != nil || ok {
		t.Fatalf("Get in a DIFFERENT scope = ok=%v err=%v, want false, nil (rows do not leak across scopes)", ok, err)
	}

	scopedList, err := repo.List(t.Context(), user.ID, "", scopeA)
	if err != nil {
		t.Fatalf("List scope A: %v", err)
	}
	if len(scopedList) != 3 {
		t.Fatalf("scope A entities after Create = %d, want 3 (2 confirmed + 1 posted)", len(scopedList))
	}

	if deleted, err := repo.Delete(t.Context(), user.ID, "", scopeA, created.EntityKey); err != nil || !deleted {
		t.Fatalf("Delete in scope A = %v, %v, want true, nil", deleted, err)
	}
	if _, ok, err := repo.Get(t.Context(), user.ID, "", scopeA, created.EntityKey); err != nil || ok {
		t.Fatalf("Get after Delete = ok=%v err=%v, want false, nil", ok, err)
	}
}

// TestResetData_Reseed_NestedGroup is D8.2's ordinary (non-failing) case: a
// reseed regenerates the parent AND the child, re-scoping the child to the
// parent's own freshly-reseeded keys ("1".."P", never the pre-reseed live
// ones — D8.2 rule 1), and writes the child's TOTAL row count into seq
// (D8.2 rule 3, reset.go's own second writer of that column).
func TestResetData_Reseed_NestedGroup(t *testing.T) {
	t.Parallel()
	repo, org, user, wsID := confirmNested(t, 2)

	outcome, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "acme")
	if err != nil {
		t.Fatalf("ResetData reseed: %v", err)
	}
	if !outcome.Changed {
		t.Fatalf("outcome.Changed = false, want true")
	}
	if len(outcome.Skipped) != 0 {
		t.Fatalf("outcome.Skipped = %v, want none", outcome.Skipped)
	}

	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("List orgs after reseed: %v", err)
	}
	if len(orgEntities) != 2 {
		t.Fatalf("org entities after reseed = %d, want 2", len(orgEntities))
	}
	// A reseed always renumbers a family's own keys "1".."N" (reset.go's
	// own pre-P3e behaviour, unchanged by this slice) — so the child's
	// scopes must be exactly those two strings.
	for i, want := range []string{"1", "2"} {
		if orgEntities[i].EntityKey != want {
			t.Fatalf("org entity %d key = %q, want %q", i, orgEntities[i].EntityKey, want)
		}
	}

	total := 0
	for _, orgEntity := range orgEntities {
		scoped, err := repo.List(t.Context(), user.ID, "", EncodeScope([]string{orgEntity.EntityKey}))
		if err != nil {
			t.Fatalf("List child scope %q: %v", orgEntity.EntityKey, err)
		}
		if len(scoped) != 2 {
			t.Fatalf("child scope %q entities = %d, want 2", orgEntity.EntityKey, len(scoped))
		}
		total += len(scoped)
	}
	if total != 4 {
		t.Fatalf("child total entities after reseed = %d, want 4", total)
	}

	// D8.2 rule 3: seq is the reseeded TOTAL (4), not the per-scope count
	// (2) — read back the row directly since ResetOutcome carries no
	// per-family Resource.
	var seq int64
	if err := repo.db.R.QueryRowContext(t.Context(), "SELECT seq FROM resources WHERE id = ?", user.ID).Scan(&seq); err != nil {
		t.Fatalf("read back child seq: %v", err)
	}
	if seq != 4 {
		t.Errorf("child seq after reseed = %d, want 4 (P8g)", seq)
	}

	// The next POST must mint a key no scope already holds — the P8d/P8g
	// symptom of writing the wrong quantity into seq.
	created, err := repo.Create(t.Context(), user.ID, "", EncodeScope([]string{"1"}), user.IDField, user.Wrapper.IDType, map[string]any{"name": "extra"})
	if err != nil {
		t.Fatalf("Create after reseed: %v", err)
	}
	if created.EntityKey != "5" {
		t.Errorf("entityKey after reseed = %q, want \"5\"", created.EntityKey)
	}
}

// TestResetData_Reseed_NestedGroup_ChildFollowsPostReseedParentKeys is P8a
// (decisions.md's own acceptance property): a reseed re-scopes the child to
// the parent's own NEWLY reseeded keys, never to the parent's LIVE keys
// just before the DELETE step removes them. A row POSTed into the parent
// AFTER the last confirm (and before the reseed) makes the two key sets
// diverge — live keys are "1","2","3"; the parent's own fresh reseed keys
// are "1","2" — so a child scoped off the wrong set would carry rows under
// scope "3", which the parent's own DELETE-then-INSERT has already made
// orphaned. Mutation: read the child's scopes from the parent's LIVE rows
// (a fresh r.List call) instead of the prepared "1".."P" key set
// (reset.go's own comment names this exact mutation) — under it this test
// reds on the population under scope "3".
func TestResetData_Reseed_NestedGroup_ChildFollowsPostReseedParentKeys(t *testing.T) {
	t.Parallel()
	repo, org, user, wsID := confirmNested(t, 2)

	extra, err := repo.Create(t.Context(), org.ID, "", ScopeKey(""), org.IDField, org.Wrapper.IDType, map[string]any{"name": "extra org"})
	if err != nil {
		t.Fatalf("POST extra org row: %v", err)
	}
	if extra.EntityKey != "3" {
		t.Fatalf("extra org EntityKey = %q, want %q (test precondition — seq must continue from the 2 confirmed rows)", extra.EntityKey, "3")
	}
	if got := entityCount(t, repo.db, org.ID); got != 3 {
		t.Fatalf("test precondition broken: want 3 live org rows before reseed, got %d", got)
	}

	if _, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "acme"); err != nil {
		t.Fatalf("ResetData reseed: %v", err)
	}

	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("List orgs after reseed: %v", err)
	}
	if len(orgEntities) != 2 {
		t.Fatalf("org entities after reseed = %d, want 2 (the POSTed row must not survive a reseed, which always renumbers a family's own keys \"1\"..\"N\")", len(orgEntities))
	}

	// The child must be scoped to EXACTLY the parent's post-reseed keys —
	// never to "3", the POSTed row's now-deleted live key.
	total := 0
	for _, orgEntity := range orgEntities {
		scoped, err := repo.List(t.Context(), user.ID, "", EncodeScope([]string{orgEntity.EntityKey}))
		if err != nil {
			t.Fatalf("List child scope %q: %v", orgEntity.EntityKey, err)
		}
		if len(scoped) != 2 {
			t.Fatalf("child scope %q entities = %d, want 2", orgEntity.EntityKey, len(scoped))
		}
		total += len(scoped)
	}
	if total != 4 {
		t.Fatalf("child total entities after reseed = %d, want 4", total)
	}
	if orphaned, err := repo.List(t.Context(), user.ID, "", EncodeScope([]string{"3"})); err != nil || len(orphaned) != 0 {
		t.Fatalf(`child rows under scope "3" (the POSTed row's now-deleted key) = %d, %v, want 0, nil`, len(orphaned), err)
	}
}

// TestResetData_Reseed_NestedGroupAtomicity_ChildOverCaps is D8.2 rule 2 /
// P8b (decisions.md's own acceptance property): a group is repopulated
// atomically or skipped atomically. This fixture makes the CHILD's own
// population (P parents x L per scope) exceed the entity byte cap while the
// PARENT's own (P rows) does not, by giving the child a much larger
// ListSize at ITS OWN confirm time than the parent had at its confirm time
// — SeedCount is frozen per-family at confirm (reset.go's own comment), so
// the two families regenerate very different row counts on reseed even
// though the workspace has one ListSize setting at any given moment.
// Mutation: skip only the family that failed, leaving the rest of its
// group repopulated (reset.go's own groupFailed comment names this exact
// mutation) — under it the parent's freshly reseeded 2 rows would be left
// in place beside a child still reporting failure, resurrecting a parent
// unpaired with its own child generation.
func TestResetData_Reseed_NestedGroupAtomicity_ChildOverCaps(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, []byte(nestedFixtureDoc))
	wsID := insertWorkspace(t, db, "acme", &specID, domain.Settings{Seed: 1, ListSize: 2})
	generous := newTestRepo(t, db, 64<<20, 64<<10)

	org, err := generous.Confirm(t.Context(), wsID, familyOrgs)
	if err != nil {
		t.Fatalf("confirm parent: %v", err)
	}
	// The child's own SeedCount is frozen at ITS OWN confirm time, not the
	// parent's — raising ListSize here before confirming the child is what
	// makes the child's population (2 orgs x 50 users = 100 rows) large
	// next to the parent's own (2 rows), so a tight total-byte cap at
	// reset time can fail one without failing the other.
	mustSetSettings(t, db, wsID, domain.Settings{Seed: 1, ListSize: 50})
	user, err := generous.Confirm(t.Context(), wsID, familyOrgUsers)
	if err != nil {
		t.Fatalf("confirm child: %v", err)
	}
	if got := entityCount(t, db, org.ID); got != 2 {
		t.Fatalf("test precondition broken: want 2 org entities, got %d", got)
	}
	if got := entityCount(t, db, user.ID); got != 100 {
		t.Fatalf("test precondition broken: want 100 user entities, got %d", got)
	}

	// A second Repo over the SAME db, with a total-byte cap that fits the
	// parent's own 2-row reseed but not the child's 100-row one — the same
	// technique TestResetData_Reseed_OverCapsSkip (reset_test.go) uses for
	// its own single-family case.
	tight := newTestRepo(t, db, 500, 64<<10)
	out, err := tight.ResetData(t.Context(), wsID, ResetModeReseed, "acme")
	if err != nil {
		t.Fatalf("ResetData(reseed) over the tight cap: %v", err)
	}
	if out.Changed {
		t.Errorf("Changed = true, want false — the whole group was skipped, nothing deleted or inserted")
	}

	want := map[string]string{familyOrgs: skipReasonGroupSkipped, familyOrgUsers: skipReasonOverCaps}
	if len(out.Skipped) != len(want) {
		t.Fatalf("Skipped = %+v, want %d entries", out.Skipped, len(want))
	}
	for _, sk := range out.Skipped {
		wantReason, ok := want[sk.RouteFamily]
		if !ok {
			t.Errorf("Skipped names unexpected family %q", sk.RouteFamily)
			continue
		}
		if sk.Reason != wantReason {
			t.Errorf("Skipped[%q].Reason = %q, want %q (the PARENT is group_skipped, the CHILD keeps its own over_caps — D8.2 rule 2)", sk.RouteFamily, sk.Reason, wantReason)
		}
	}

	if got := entityCount(t, db, org.ID); got != 2 {
		t.Errorf("parent rows changed by a skipped group, count = %d, want 2 (untouched)", got)
	}
	if got := entityCount(t, db, user.ID); got != 100 {
		t.Errorf("child rows changed by a skipped group, count = %d, want 100 (untouched)", got)
	}
}

// TestEncodeScope_InjectiveOverOwnDelimiter is P2 (decisions.md's own
// acceptance property): the scope encoder must stay injective over a value
// that contains its own "/" delimiter. Mutation: replace
// url.PathEscape(v) with v inside EncodeScope; the two calls below become
// byte-identical and this test reds. Deliberately a UNIT-level observation,
// not an end-to-end one — EncodeScope's own doc comment explains why: every
// entity_key this build ever writes is a decimal string, and D6.3's anchor
// check refuses a scope no parent key anchors, so no request that actually
// stores or reads a row can carry a scope value needing the escape; an
// end-to-end version of this property would be unbuildable.
func TestEncodeScope_InjectiveOverOwnDelimiter(t *testing.T) {
	t.Parallel()
	collapsed := EncodeScope([]string{"a/b"})
	separate := EncodeScope([]string{"a", "b"})
	if collapsed == separate {
		t.Fatalf(`EncodeScope([]string{"a/b"}) = %q, EncodeScope([]string{"a","b"}) = %q, want them to differ`, collapsed, separate)
	}
	if collapsed != "a%2Fb" {
		t.Errorf(`EncodeScope([]string{"a/b"}) = %q, want "a%%2Fb"`, collapsed)
	}
	if separate != "a/b" {
		t.Errorf(`EncodeScope([]string{"a","b"}) = %q, want "a/b"`, separate)
	}
}

// TestConfirm_ParentDeclinedBetweenTheReadAndTheWrite_CaughtInsideTheTransaction
// closes the one gap the P3e run's own acceptance step reported against
// property P4. D5.1's parent check exists TWICE — once in
// [Repo.prepareConfirm], against the parent read before generation, and once
// in [fenceParentTx], inside the write transaction — and every other
// nested-confirm test arrives with the parent already absent, so
// prepareConfirm's copy refuses first and fenceParentTx's copy is never the
// one that answered. Only the SECOND is authoritative: the window between the
// two reads is exactly where another request declines the parent, and both
// checks return the same ErrParentNotConfirmed, so an error code cannot say
// which of them fired. [confirmPreWriteHook] lands the decline INSIDE that
// window, so prepareConfirm sees a confirmed parent and the transaction does
// not — the same shape [TestConfirm_StaleConfigViaHook] holds for D4/R36's
// settings fence.
func TestConfirm_ParentDeclinedBetweenTheReadAndTheWrite_CaughtInsideTheTransaction(t *testing.T) {
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importSpecDoc(t, db, []byte(nestedFixtureDoc))
	wsID := insertWorkspace(t, db, "acme", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	if _, err := repo.Confirm(t.Context(), wsID, familyOrgs); err != nil {
		t.Fatalf("confirm parent %q: %v", familyOrgs, err)
	}

	// A decline, reduced to the row fenceParentTx actually reads. It fires
	// after prepareConfirm's generation half has already read the parent and
	// its live keys, and before the write transaction opens.
	confirmPreWriteHook = func() {
		if _, err := db.W.ExecContext(t.Context(),
			"DELETE FROM resources WHERE workspace_id = ? AND route_family = ?", wsID, familyOrgs,
		); err != nil {
			t.Errorf("decline the parent inside the window: %v", err)
		}
	}
	t.Cleanup(func() { confirmPreWriteHook = confirmPreWriteHookNoop })

	if _, err := repo.Confirm(t.Context(), wsID, familyOrgUsers); !errors.Is(err, ErrParentNotConfirmed) {
		t.Fatalf("Confirm racing a parent decline = %v, want ErrParentNotConfirmed", err)
	}
	if resourceRowCount(t, db, wsID, familyOrgUsers) != 0 {
		t.Fatalf("a resources row survived a confirm the parent fence refused")
	}
	var total int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entities").Scan(&total); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	if total != 0 {
		t.Fatalf("entity rows survived a confirm the parent fence refused: %d row(s)", total)
	}
}

// TestCreate_NestedFamilyMintsAfterTheFamilyWideTotal is P8d's own literal
// symptom, which the P3e run's acceptance step reported as asserted by no
// test. With P=2 parents and L=2, Confirm writes FOUR child rows keyed
// "1".."4" and stores resources.seq = 4 — the family-wide TOTAL, not the
// per-scope count (D5.5 point 4) — so the next POST into any scope mints
// "5". Writing seedCount (L=2) into seq instead mints "3", a key the family
// already holds in the OTHER scope; [TestCreate_NestedFamily_ScopedReadBack]
// notices that only incidentally, and only because the collision happens to
// cross a scope boundary. This one reads the minted key directly.
func TestCreate_NestedFamilyMintsAfterTheFamilyWideTotal(t *testing.T) {
	t.Parallel()
	repo, org, user, _ := confirmNested(t, 2)

	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("List orgs: %v", err)
	}
	if len(orgEntities) != 2 {
		t.Fatalf("orgs = %d, want 2", len(orgEntities))
	}
	scopeA := EncodeScope([]string{orgEntities[0].EntityKey})

	created, err := repo.Create(t.Context(), user.ID, "", scopeA, user.IDField, user.Wrapper.IDType, map[string]any{"name": "posted"})
	if err != nil {
		t.Fatalf("Create into scope A: %v", err)
	}
	if created.EntityKey != "5" {
		t.Fatalf("first POST after a P=2 x L=2 confirm minted entityKey %q, want \"5\" (seq is the family-wide total, D5.5 point 4)", created.EntityKey)
	}
}
