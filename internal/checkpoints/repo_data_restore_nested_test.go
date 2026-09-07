// repo_data_restore_nested_test.go: P3d's data_snap restore over NESTED
// and base-scoped families — depth two, base-scope independence, and a v1
// (pre-nesting) document landing every row in the empty base scope. Sibling
// of repo_data_restore_test.go, split out the same way from the former
// repo_test.go (package checkpoints, not checkpoints_test — see
// helpers_test.go's own comment for why, and why no test in this package
// calls t.Parallel).
package checkpoints

import (
	"testing"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
)

// TestRollback_dataRestoreOfANestedFamilyKeepsRowsUnderTheirOwnScopes is D10
// clause 33 (D10.3): a nested family's two-scope capture and restoreData:true
// rollback comes back scoped, and parent_entity_id stays NULL throughout —
// the shape D9 makes true of every row this build writes, not merely the
// unscoped ones every other fixture in this file exercises.
//
// This slice adds ZERO production lines to internal/checkpoints or
// internal/bundle beyond the comment rewrites D16 names (D10.1): the codec
// already carries ScopeKey, ValidateData rule 4 already keys on the
// compound (scopeKey, entityKey), canonicalizeData already sorts by it, and
// restoreEntitiesTx's INSERT already binds scope_key from the row. This
// test is the proof that those already-shipped mechanisms in fact do the
// job a nested family needs, not a change that makes them do it.
func TestRollback_dataRestoreOfANestedFamilyKeepsRowsUnderTheirOwnScopes(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/orgs/{}/users", name: "users", idField: "id", wrapper: wrapperJSON, seq: 4, seedCount: 4,
	})
	f.writeDecision(t, "/orgs/{}/users", "confirmed")
	f.insertScopedEntity(t, resourceID, "7", "1")
	f.insertScopedEntity(t, resourceID, "7", "2")
	f.insertScopedEntity(t, resourceID, "8", "1")
	f.insertScopedEntity(t, resourceID, "8", "2")

	point := f.create(t, "снимок вложенной семьи")
	before := f.scopedEntityData(t, resourceID)

	// An anonymous client wrote into BOTH scopes since the checkpoint —
	// the fixture a restore is supposed to overwrite, exactly as
	// TestRollback_dataRestoreOverwritesARowCreatedAfterTheCheckpoint does
	// for the unscoped case, now over two scopes at once.
	f.insertScopedEntity(t, resourceID, "7", "3")
	f.insertScopedEntity(t, resourceID, "8", "3")

	f.rollbackData(t, point.ID)

	got := f.scopedEntityData(t, resourceID)
	if !scopedMapsEqual(got, before) {
		t.Fatalf("scoped entities after restoreData:true = %v, want exactly the checkpoint's %v (both scopes restored, the post-checkpoint rows gone)", got, before)
	}
	// Both scopes must be represented — a bug that dropped one scope's
	// rows entirely, rather than merely failing to overwrite them, would
	// still pass a bare length check against the pre-checkpoint set if it
	// happened to drop and keep the same total count.
	sawScopes := map[string]bool{}
	for k := range got {
		sawScopes[k[0]] = true
	}
	if !sawScopes["7"] || !sawScopes["8"] {
		t.Fatalf("scoped entities after restore cover scopes %v, want both \"7\" and \"8\"", sawScopes)
	}
	if !f.entityParentIsAlwaysNull(t, resourceID) {
		t.Fatal("a restored nested family has a non-NULL parent_entity_id — D9 says every row this build writes keeps it NULL")
	}

	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 || len(data.Families[0].Rows) != 4 {
		t.Fatalf("captured data = %+v, want one family with 4 rows across two scopes", data.Families)
	}
	scopesInDoc := map[string]int{}
	for _, r := range data.Families[0].Rows {
		scopesInDoc[r.ScopeKey]++
	}
	if scopesInDoc["7"] != 2 || scopesInDoc["8"] != 2 {
		t.Fatalf("data_snap scope distribution = %v, want 2 rows under each of \"7\" and \"8\"", scopesInDoc)
	}
}

// TestRollback_neverRewindsTheEntityCounterOfANestedFamily is
// TestRollback_neverRewindsTheEntityCounter's nested sibling: D5.5's counter
// is family-wide, not per-scope (`resources.seq` has no scope_key column of
// its own), so the existing MAX rule has to keep holding across a family
// whose rows span more than one scope — this is the fixture that proves it
// still does, rather than assuming a flat-scope fixture generalises.
func TestRollback_neverRewindsTheEntityCounterOfANestedFamily(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/orgs/{}/users", name: "users", idField: "id", wrapper: wrapperJSON, seq: 2, seedCount: 2,
	})
	f.writeDecision(t, "/orgs/{}/users", "confirmed")
	// Two scopes share the family-wide counter: key "1" under scope "7"
	// and key "2" under scope "8" are the two rows seq=2 already accounts
	// for — a per-scope counter would instead start each scope at 1, which
	// is exactly the assumption this test exists to rule out.
	f.insertScopedEntity(t, resourceID, "7", "1")
	f.insertScopedEntity(t, resourceID, "8", "2")

	point := f.create(t, "снимок при seq=2, две области")

	// Anonymous clients wrote three more rows spread across both scopes
	// after the checkpoint — seq moved to 5 exactly as in the flat-scope
	// test, and a restore must raise it to that live value regardless of
	// which scope each new row landed in.
	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE resources SET seq = 5 WHERE id = ?", resourceID); err != nil {
		t.Fatalf("move seq: %v", err)
	}
	f.insertScopedEntity(t, resourceID, "7", "3")
	f.insertScopedEntity(t, resourceID, "8", "4")
	f.insertScopedEntity(t, resourceID, "7", "5")

	f.rollbackData(t, point.ID)

	got := f.readResource(t, "/orgs/{}/users")
	if got == nil {
		t.Fatal("the resources row disappeared across the rollback")
	}
	if got.seq != 5 {
		t.Fatalf("seq after the rollback = %d, want 5 — the live value across BOTH scopes, never the snapshot's 2", got.seq)
	}
	if got.seedCount != 2 {
		t.Fatalf("seed_count = %d, want the snapshot's 2", got.seedCount)
	}
}

// TestRollback_dataRestoreOfADepthTwoFamilyKeepsScopesByteIdentical is P3g's
// D10/P14: this package's capture and restore change no production line for
// nesting beyond one level — captureEntitiesTx reads scope_key exactly as
// stored and restoreEntitiesTx writes it back VERBATIM, neither one ever
// interpreting it as a chain of ancestor values (see captureEntitiesTx's and
// restoreEntitiesTx's own doc comments). "No change" and "not covered" are
// indistinguishable from the outside, so this is the test that makes the
// claim provable: a depth-2 scope_key — two ancestor values joined the same
// way resources.EncodeScope joins them ("orgID/teamID", each already
// path-escaped) — must round-trip through a capture and a restore with its
// (scope_key, entity_key) pair byte-identical, exactly as
// TestRollback_dataRestoreOfANestedFamilyKeepsRowsUnderTheirOwnScopes proved
// for one ancestor value. This package has no notion of "family depth" at
// all — a two-segment scope_key is opaque to it in exactly the way a
// one-segment one is — which is the whole point: nothing here needs to
// change for the property to hold, and nothing here does.
func TestRollback_dataRestoreOfADepthTwoFamilyKeepsScopesByteIdentical(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/orgs/{}/teams/{}/users", name: "users", idField: "id", wrapper: wrapperJSON, seq: 4, seedCount: 4,
	})
	f.writeDecision(t, "/orgs/{}/teams/{}/users", "confirmed")
	// Two root scopes, two team scopes under the first root — four
	// depth-2 scope keys, deliberately including two DIFFERENT roots so a
	// restore that flattened to the innermost segment alone would collide
	// two of them into one.
	f.insertScopedEntity(t, resourceID, "7/3", "1")
	f.insertScopedEntity(t, resourceID, "7/4", "1")
	f.insertScopedEntity(t, resourceID, "8/3", "1")
	f.insertScopedEntity(t, resourceID, "8/4", "1")

	point := f.create(t, "снимок семьи глубины 2")
	before := f.scopedEntityData(t, resourceID)

	// Mutate every row's data after the checkpoint — the fixture a
	// restore is supposed to overwrite, mirroring
	// TestRollback_dataRestoreOfANestedFamilyKeepsRowsUnderTheirOwnScopes's
	// own "write since the checkpoint" step, done here by an UPDATE rather
	// than a fresh INSERT so the row COUNT cannot by itself prove the
	// restore did anything — only the scope_key/entity_key pair and the
	// data behind it can.
	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE entities SET data = '{\"mutated\":true}' WHERE resource_id = ?", resourceID); err != nil {
		t.Fatalf("mutate rows before restore: %v", err)
	}

	f.rollbackData(t, point.ID)

	got := f.scopedEntityData(t, resourceID)
	if !scopedMapsEqual(got, before) {
		t.Fatalf("scoped entities after restoreData:true = %v, want exactly the checkpoint's %v (every depth-2 scope_key restored byte-identical, the post-checkpoint mutation gone)", got, before)
	}
	wantScopes := map[string]bool{"7/3": true, "7/4": true, "8/3": true, "8/4": true}
	sawScopes := map[string]bool{}
	for k := range got {
		sawScopes[k[0]] = true
	}
	if len(sawScopes) != len(wantScopes) {
		t.Fatalf("scoped entities after restore cover scopes %v, want exactly %v", sawScopes, wantScopes)
	}
	for scope := range wantScopes {
		if !sawScopes[scope] {
			t.Fatalf("scoped entities after restore = %v, missing depth-2 scope %q", sawScopes, scope)
		}
	}
	if !f.entityParentIsAlwaysNull(t, resourceID) {
		t.Fatal("a restored depth-2 family has a non-NULL parent_entity_id — D9 (re-decided at every depth by P3g) says every row this build writes keeps it NULL")
	}

	// The document itself must carry the same four scope keys — the
	// property is about capture as much as restore, and a capture that
	// silently truncated a multi-segment scope_key would still pass the
	// restore-side assertions above if restoreEntitiesTx wrote the
	// truncated value back just as verbatim.
	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 || len(data.Families[0].Rows) != 4 {
		t.Fatalf("captured data = %+v, want one family with 4 rows across four depth-2 scopes", data.Families)
	}
	scopesInDoc := map[string]bool{}
	for _, r := range data.Families[0].Rows {
		scopesInDoc[r.ScopeKey] = true
	}
	for scope := range wantScopes {
		if !scopesInDoc[scope] {
			t.Fatalf("data_snap scope keys = %v, missing depth-2 scope %q", scopesInDoc, scope)
		}
	}
}

// TestRollback_dataRestoreKeepsRowsUnderTheirOwnBaseScope is P3h's P12: a
// checkpoint captures the base scope alongside scope_key and restores it
// verbatim, keeping two base scopes' rows apart the same way
// TestRollback_dataRestoreOfANestedFamilyKeepsRowsUnderTheirOwnScopes proves
// for two ORDINARY scopes — base_scope_key and scope_key are independent
// columns of the same natural key, and this is the property's own proof for
// the one that slice left uncovered. Mutation this test reddens for: drop
// BaseScopeKey from restoreEntitiesTx's INSERT (D9's own named mutation).
func TestRollback_dataRestoreKeepsRowsUnderTheirOwnBaseScope(t *testing.T) {
	f := newFixture(t, 20)
	f.writeSettings(t, settingsWith(f.settings(t), func(s *domain.Settings) {
		s.BasePath = "/orgs/{orgId}"
		s.BasePathValues = []string{"7", "8"}
	}))
	resourceID := f.insertResource(t, resourceFixture{
		family: "/quizzes", name: "quizzes", idField: "id", wrapper: wrapperJSON, seq: 4, seedCount: 4,
	})
	f.writeDecision(t, "/quizzes", "confirmed")
	// entity_key is family-wide (P3g's rule), so it cannot repeat across
	// rows of one family regardless of which base scope each sits in — the
	// same fact TestValidateData_baseScopeKeyDoesNotWidenUniqueness rests
	// on at the codec layer. Two base scopes, one flat scope_key each.
	f.insertBaseScopedEntity(t, resourceID, "7", "", "1")
	f.insertBaseScopedEntity(t, resourceID, "7", "", "2")
	f.insertBaseScopedEntity(t, resourceID, "8", "", "3")
	f.insertBaseScopedEntity(t, resourceID, "8", "", "4")

	point := f.create(t, "снимок с базовыми областями")
	before := f.baseScopedEntityData(t, resourceID)

	// An anonymous client wrote into both base scopes since the checkpoint
	// — the fixture a restore is supposed to overwrite, mirroring
	// TestRollback_dataRestoreOfANestedFamilyKeepsRowsUnderTheirOwnScopes's
	// own step for ordinary scopes.
	f.insertBaseScopedEntity(t, resourceID, "7", "", "5")
	f.insertBaseScopedEntity(t, resourceID, "8", "", "6")

	f.rollbackData(t, point.ID)

	got := f.baseScopedEntityData(t, resourceID)
	if len(got) != len(before) {
		t.Fatalf("base-scoped entities after restoreData:true = %d rows, want exactly the checkpoint's %d", len(got), len(before))
	}
	for k, v := range before {
		if got[k] != v {
			t.Fatalf("base-scoped entities after restoreData:true = %v, want exactly the checkpoint's %v (base scope %q not restored byte-identical)", got, before, k[0])
		}
	}
	// Both base scopes must be represented — a bug that dropped one base
	// scope's rows entirely, rather than merely failing to overwrite them,
	// would still pass a bare length check against the pre-checkpoint set
	// if it happened to drop and keep the same total count.
	sawBases := map[string]bool{}
	for k := range got {
		sawBases[k[0]] = true
	}
	if !sawBases["7"] || !sawBases["8"] {
		t.Fatalf("base-scoped entities after restore cover base scopes %v, want both \"7\" and \"8\"", sawBases)
	}

	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 || len(data.Families[0].Rows) != 4 {
		t.Fatalf("captured data = %+v, want one family with 4 rows across two base scopes", data.Families)
	}
	basesInDoc := map[string]int{}
	for _, r := range data.Families[0].Rows {
		basesInDoc[r.BaseScopeKey]++
	}
	if basesInDoc["7"] != 2 || basesInDoc["8"] != 2 {
		t.Fatalf("data_snap base scope distribution = %v, want 2 rows under each of \"7\" and \"8\"", basesInDoc)
	}
}

// TestRollback_dataRestoreOfAV1DocumentLandsEveryRowInTheEmptyBaseScope is
// P3h's other half of P12: a checkpoint whose data_snap predates this slice
// — mockerData: 1, no baseScopeKey on any row — still restores, and every
// row lands at base_scope_key = "". Built with bundle.EncodeData over a
// bundle.DataBundle{MockerData: 1, ...} rather than via captureEntitiesTx
// (which, in this build, only ever writes DataVersion 2): a hand-built v1
// document is byte-identical to what EncodeData produces for MockerData: 1
// with every row's BaseScopeKey left at its zero value, because the field
// is `omitempty` — the same reasoning bundle.data_test.go's own hand-built
// literal test uses at the codec layer, done here through the real restore
// path instead. Mutation this test reddens for: refusing a DataVersion < 2
// document at either DecodeData or ValidateData (D9's other named case).
func TestRollback_dataRestoreOfAV1DocumentLandsEveryRowInTheEmptyBaseScope(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	settings := f.settings(t)
	b := bundle.New("ws-a", settings, bundle.SpecRef{Name: "fixture", Hash: "hash-1"}, nil)
	b.Resources = []bundle.ResourceEntry{{
		RouteFamily: "/widgets", Name: "widgets", IDField: "id", IDStrategy: "seq",
		ScopeParams: []string{}, EntitySchema: "#/components/schemas/Widget",
		Wrapper: jsonx.RawMessage(wrapperJSON), FilterMap: jsonx.RawMessage(`{}`),
		Seq: 1, SeedCount: 1,
	}}
	b.Decisions = []bundle.DecisionEntry{{RouteFamily: "/widgets", State: "confirmed"}}
	b.Endpoints = []bundle.EndpointEntry{}

	v1Data := bundle.DataBundle{
		MockerData: 1, // a pre-P3h document: no baseScopeKey field existed at all
		Families: []bundle.FamilyEntry{
			{RouteFamily: "/widgets", Rows: []bundle.EntityRow{
				{ScopeKey: "", EntityKey: "1", Data: jsonx.RawMessage(`{"id":1,"name":"from a v1 snapshot"}`),
					CreatedAt: 1756370000, UpdatedAt: 1756370000},
			}},
		},
	}
	point := f.insertCraftedWithData(t, b, v1Data)

	f.rollbackData(t, point)

	got := f.baseScopedEntityData(t, resourceID)
	want := map[[3]string]string{
		{"", "", "1"}: `{"id":1,"name":"from a v1 snapshot"}`,
	}
	if len(got) != 1 {
		t.Fatalf("base-scoped entities after restoring a v1 document = %v, want exactly one row", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("row after restoring a v1 document = %v, want %v at (baseScope=%q, scope=%q, key=%q) — every v1 row must land in the empty base scope",
				got, v, k[0], k[1], k[2])
		}
	}
}

// TestRollback_dataRestoreOfANestedFamilyKeepsBaseAndOrdinaryScopesIndependent
// combines P12's own subject with the nested-family fixture
// TestRollback_dataRestoreOfANestedFamilyKeepsRowsUnderTheirOwnScopes already
// pins for ordinary scope_key: a row's base scope and its (ancestor-tuple)
// scope are two independent columns of the natural key, and a restore that
// conflated them — writing one into the other's column, or dropping either
// — must not pass this test's byte-identical comparison across FOUR
// combinations of base scope and ordinary scope.
func TestRollback_dataRestoreOfANestedFamilyKeepsBaseAndOrdinaryScopesIndependent(t *testing.T) {
	f := newFixture(t, 20)
	f.writeSettings(t, settingsWith(f.settings(t), func(s *domain.Settings) {
		s.BasePath = "/tenants/{tenantId}/orgs/{}/users"
		s.BasePathValues = []string{"7", "8"}
	}))
	resourceID := f.insertResource(t, resourceFixture{
		family: "/orgs/{}/users", name: "users", idField: "id", wrapper: wrapperJSON, seq: 4, seedCount: 4,
	})
	f.writeDecision(t, "/orgs/{}/users", "confirmed")
	f.insertBaseScopedEntity(t, resourceID, "7", "100", "1")
	f.insertBaseScopedEntity(t, resourceID, "7", "200", "2")
	f.insertBaseScopedEntity(t, resourceID, "8", "100", "3")
	f.insertBaseScopedEntity(t, resourceID, "8", "200", "4")

	point := f.create(t, "снимок вложенной семьи с базовыми областями")
	before := f.baseScopedEntityData(t, resourceID)

	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE entities SET data = '{\"mutated\":true}' WHERE resource_id = ?", resourceID); err != nil {
		t.Fatalf("mutate rows before restore: %v", err)
	}

	f.rollbackData(t, point.ID)

	got := f.baseScopedEntityData(t, resourceID)
	if len(got) != len(before) {
		t.Fatalf("base-scoped entities after restore = %d rows, want exactly the checkpoint's %d", len(got), len(before))
	}
	for k, v := range before {
		if got[k] != v {
			t.Fatalf("base-scoped entities after restore = %v, want exactly the checkpoint's %v (base=%q scope=%q not restored byte-identical)", got, before, k[0], k[1])
		}
	}
}
