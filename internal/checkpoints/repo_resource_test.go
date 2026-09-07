// repo_resource_test.go: the resource layer inside a checkpoint (P3b) —
// config_snap's own resources/decisions half: what Create captures, what
// Restore/Rollback/Reset do and do not touch on it, and the basic Capture
// properties (call sites, workspace scoping, canonical key order). The
// entity-DATA half (data_snap) lives in repo_data_restore_test.go and its
// nested-family sibling. Split out of the former repo_test.go (package
// checkpoints, not checkpoints_test — see helpers_test.go's own comment for
// why, and why no test in this package calls t.Parallel).
package checkpoints

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
)

// TestCreate_configSnapCarriesResourcesAndDecisions is D10 clause 21: a
// checkpoint's config_snap carries the `resources` rows AND the
// `resource_decisions` rows. Under P3d it ALSO carries the workspace's
// entity rows — but in data_snap, a separate column and a separate
// document (D5's always-capture); config_snap's own Bundle.Entities stays
// JSON null regardless (D3's refusal, unweakened).
//
// The two config tables travel together or not at all. A snapshot carrying
// one without the other restores a workspace whose decision row says
// `declined` beside a live `resources` row — a state the confirm path
// answers `already_confirmed` for while the screen renders it as declined —
// which is why the fixture declares a DECLINED family with no resource row
// alongside the confirmed one: the decisions array is the only place that
// family exists at all.
//
// Verified by MUTATION: deleting the resource_decisions read from
// captureSnapshot reds this test and
// TestRollback_restoresAResourceDeclinedAwaySinceTheSnapshot with it.
func TestCreate_configSnapCarriesResourcesAndDecisions(t *testing.T) {
	f := newFixture(t, 20)
	bare := "bare"
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id",
		wrapper: wrapperJSON, writeForm: &bare, seq: 3, seedCount: 3,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.writeDecision(t, "/gadgets", "declined")
	f.insertEntities(t, resourceID, 3)

	point := f.create(t, "с ресурсами")

	stored, err := f.repo.Get(t.Context(), f.wsID, point.ID)
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}

	if len(stored.Bundle.Resources) != 1 {
		t.Fatalf("config_snap carries %d resources, want 1: %+v", len(stored.Bundle.Resources), stored.Bundle.Resources)
	}
	got := stored.Bundle.Resources[0]
	if got.RouteFamily != "/widgets" || got.Name != "widgets" || got.IDField != "id" || got.IDStrategy != "seq" {
		t.Fatalf("resource entry identity = %+v", got)
	}
	if got.Seq != 3 || got.SeedCount != 3 {
		t.Fatalf("resource entry counters = seq %d, seedCount %d, want 3 and 3", got.Seq, got.SeedCount)
	}
	if got.WriteForm == nil || *got.WriteForm != "bare" {
		t.Fatalf("resource entry writeForm = %v, want \"bare\"", got.WriteForm)
	}
	if string(got.Wrapper) != wrapperJSON {
		t.Fatalf("resource entry wrapper = %s, want %s", got.Wrapper, wrapperJSON)
	}
	if string(got.FilterMap) != "{}" {
		t.Fatalf("resource entry filterMap = %s, want {}", got.FilterMap)
	}
	if got.ParentFamily != nil {
		t.Fatalf("resource entry parentFamily = %v, want null in this build", *got.ParentFamily)
	}

	wantDecisions := map[string]string{"/gadgets": "declined", "/widgets": "confirmed"}
	gotDecisions := map[string]string{}
	for _, d := range stored.Bundle.Decisions {
		gotDecisions[d.RouteFamily] = d.State
	}
	if len(gotDecisions) != len(wantDecisions) {
		t.Fatalf("config_snap carries decisions %+v, want %+v", gotDecisions, wantDecisions)
	}
	for family, state := range wantDecisions {
		if gotDecisions[family] != state {
			t.Fatalf("decision for %q = %q, want %q", family, gotDecisions[family], state)
		}
	}

	// The other half of the clause, inverted by P3d: entity DATA is now
	// captured, but into data_snap ONLY — config_snap's own Bundle.Entities
	// stays JSON null regardless (D3's refusal is unweakened).
	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 || data.Families[0].RouteFamily != "/widgets" {
		t.Fatalf("data_snap families = %+v, want one entry for /widgets", data.Families)
	}
	if len(data.Families[0].Rows) != 3 {
		t.Fatalf("data_snap rows for /widgets = %d, want 3", len(data.Families[0].Rows))
	}
	if !isJSONNullBytes(stored.Bundle.Entities) {
		t.Fatalf("config_snap carries entities = %s, want null", stored.Bundle.Entities)
	}
}

// TestRestore_neverTouchesAnEntityRow is D10 clause 22, and it is the
// whole reason P3b exists. entities.resource_id is ON DELETE CASCADE and
// the restore beside this one is DELETE-then-UPSERT by natural key, so a
// resource half written in that same shape destroys every entity row of
// every family the snapshot names — silently, in a transaction the operator
// asked to restore CONFIGURATION.
//
// The fixture holds two families, one the target snapshot names and one it
// does not, both carrying rows: a restore that deleted only what it was
// about to write back would pass over the second, and one that deleted the
// workspace's resources wholesale fails on both.
//
// BOTH verbs are driven here, because the clause is about both and because
// the same injection into resetTx must red this test as well as
// TestReset_leavesResourcesAndEntitiesIntact below.
//
// Verified by MUTATION: `DELETE FROM resources WHERE workspace_id = ?`
// injected into rollbackTx AFTER the three resource statements reds this
// test, and co-reds the three clauses below it — at that position both
// UPSERTs have already run, so every row of the workspace is simply gone
// with its entities by cascade.
func TestRestore_neverTouchesAnEntityRow(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	namedID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 2, seedCount: 2,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, namedID, 2)

	point := f.create(t, "до второго ресурса")

	// Confirmed AFTER the checkpoint: the snapshot names it nowhere.
	unnamedID := f.insertResource(t, resourceFixture{
		family: "/quizzes", name: "quizzes", idField: "id", wrapper: wrapperJSON, seq: 3, seedCount: 3,
	})
	f.writeDecision(t, "/quizzes", "confirmed")
	f.insertEntities(t, unnamedID, 3)

	namedBefore := f.entityData(t, namedID)
	unnamedBefore := f.entityData(t, unnamedID)

	f.pinStatus(t, 402)
	f.rollback(t, point.ID)

	// The configuration half really did roll back — otherwise this test
	// would be green over a restore that does nothing at all.
	if s := f.pinnedStatus(t); s == nil || *s != 418 {
		t.Fatalf("the override did not roll back: %v", s)
	}
	if got := f.entityData(t, namedID); !mapsEqual(got, namedBefore) {
		t.Fatalf("the named family's entities changed: %v -> %v", namedBefore, got)
	}
	if got := f.entityData(t, unnamedID); !mapsEqual(got, unnamedBefore) {
		t.Fatalf("the unnamed family's entities changed: %v -> %v", unnamedBefore, got)
	}
	// And the family confirmed AFTER the checkpoint survives as a row, not
	// only as data: R16's UPSERT-only rule leaves rows the snapshot does
	// not name standing.
	if f.readResource(t, "/quizzes") == nil {
		t.Fatal("the family confirmed after the checkpoint lost its resources row")
	}

	// The other verb, same rule. Reset deletes the workspace layer down to
	// the spec and has no resource statements at all — R16 covers it by
	// what resetTx does NOT contain.
	f.pinStatus(t, 409)
	f.reset(t)
	if s := f.pinnedStatus(t); s != nil {
		t.Fatalf("the reset did not delete the override; the fixture is not the state it claims: %v", s)
	}
	if got := f.entityData(t, namedID); !mapsEqual(got, namedBefore) {
		t.Fatalf("the reset changed the named family's entities: %v -> %v", namedBefore, got)
	}
	if got := f.entityData(t, unnamedID); !mapsEqual(got, unnamedBefore) {
		t.Fatalf("the reset changed the unnamed family's entities: %v -> %v", unnamedBefore, got)
	}
}

// TestRollback_restoresAResourceDeclinedAwaySinceTheSnapshot is D10 clause
// 23: UPSERT-ONLY means never DELETE, not "never write resources at all".
// A family the snapshot names and the workspace has since declined comes
// BACK — its row and its decision together.
//
// The snapshot is taken by Repo.Create rather than crafted by hand on
// purpose: clause 21's mutation (deleting the resource_decisions read from
// captureSnapshot) co-reds this test only if the capture is the real one —
// insertCrafted is immune to a capture-side edit.
//
// It also documents the divergence R16 accepts rather than hides: the row
// comes back CONFIGURED and EMPTY. The decline's own cascade already
// destroyed its entities and no snapshot in this build holds them.
//
// Verified by MUTATION: breaking the restore's `resources` UPSERT reds this
// test and TestRollback_restoresASnapshotsColumnsOverALiveRow with it.
func TestRollback_restoresAResourceDeclinedAwaySinceTheSnapshot(t *testing.T) {
	f := newFixture(t, 20)
	bare := "bare"
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id",
		wrapper: wrapperJSON, writeForm: &bare, seq: 4, seedCount: 4,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 4)

	point := f.create(t, "до отказа")

	// The decline, as internal/resources performs it: the decision flips
	// and the row goes, taking its entities with it by cascade.
	if _, err := f.db.W.ExecContext(t.Context(), "DELETE FROM resources WHERE id = ?", resourceID); err != nil {
		t.Fatalf("decline the family: %v", err)
	}
	f.writeDecision(t, "/widgets", "declined")
	if got := len(f.entityData(t, resourceID)); got != 0 {
		t.Fatalf("the decline left %d entity rows behind; the fixture is not the state it claims", got)
	}

	f.rollback(t, point.ID)

	restored := f.readResource(t, "/widgets")
	if restored == nil {
		t.Fatal("the rollback did not restore the resources row the snapshot names")
	}
	if restored.name != "widgets" || restored.idField != "id" || restored.seedCount != 4 {
		t.Fatalf("restored row = %+v", restored)
	}
	if restored.writeForm == nil || *restored.writeForm != "bare" {
		t.Fatalf("restored writeForm = %v, want \"bare\"", restored.writeForm)
	}
	if got := f.decisionState(t, "/widgets"); got != "confirmed" {
		t.Fatalf("decision after the rollback = %q, want \"confirmed\"", got)
	}
	// R16's accepted divergence, asserted so it is a decision and not a
	// surprise: the family is back, and it is empty.
	var entityCount int
	if err := f.db.R.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM entities WHERE resource_id IN
			(SELECT id FROM resources WHERE workspace_id = ?)`, f.wsID).Scan(&entityCount); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	if entityCount != 0 {
		t.Fatalf("entity rows = %d, want 0: a rollback cannot bring back what a decline destroyed", entityCount)
	}
}

// TestRollback_restoresASnapshotsColumnsOverALiveRow is D10 clause 23a: the
// UPSERT writes the snapshot's COLUMNS over a live row, not merely its key.
//
// write_form is the sharp one and the reason a clause observes columns at
// all: NULL means the family's POST shape was not recognised, and the mock
// plane answers a GENERATED 201 over a write that stores nothing — a
// silent lost write, not a 500. A restore that wrote the key and left the
// shape would leave that state undetectable by every other clause here.
func TestRollback_restoresASnapshotsColumnsOverALiveRow(t *testing.T) {
	f := newFixture(t, 20)
	f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id",
		wrapper: wrapperJSON, writeForm: nil, seq: 2, seedCount: 2,
	})
	f.writeDecision(t, "/widgets", "confirmed")

	point := f.create(t, "снимок с write_form NULL")

	const liveWrapper = `{"arrayKey":"data","countKey":null,"countType":"","idType":"string"}`
	if _, err := f.db.W.ExecContext(t.Context(), `
		UPDATE resources SET write_form = 'bare', wrapper = ?, id_field = 'uuid'
		WHERE workspace_id = ? AND route_family = '/widgets'`, liveWrapper, f.wsID); err != nil {
		t.Fatalf("move the live row away from the snapshot: %v", err)
	}

	f.rollback(t, point.ID)

	got := f.readResource(t, "/widgets")
	if got == nil {
		t.Fatal("the resources row disappeared across the rollback")
	}
	if got.writeForm != nil {
		t.Fatalf("write_form = %q, want the snapshot's NULL", *got.writeForm)
	}
	if got.wrapper != wrapperJSON {
		t.Fatalf("wrapper = %s, want the snapshot's %s", got.wrapper, wrapperJSON)
	}
	if got.idField != "id" {
		t.Fatalf("id_field = %q, want the snapshot's \"id\"", got.idField)
	}
}

// TestRollback_doesNotDeclineAFamilyConfirmedSinceTheSnapshot is D10 clause
// 24: a snapshot's `declined` decision is NOT applied to a family that has
// a `resources` row right now.
//
// Applying it would recreate the exact state carrying the two tables
// together exists to prevent — `declined` written over a live row R16
// forbids deleting, after which the screen renders the family as declined
// while the confirm path answers `already_confirmed` and the mock plane
// goes on serving it.
func TestRollback_doesNotDeclineAFamilyConfirmedSinceTheSnapshot(t *testing.T) {
	f := newFixture(t, 20)
	f.writeDecision(t, "/widgets", "declined")

	point := f.create(t, "снимок с отказом")

	// Confirmed since: the row and its rows exist now, and the snapshot
	// names the family only in its decisions array.
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 5, seedCount: 5,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 5)
	before := f.entityData(t, resourceID)

	f.rollback(t, point.ID)

	if got := f.decisionState(t, "/widgets"); got != "confirmed" {
		t.Fatalf("decision after the rollback = %q, want \"confirmed\": the snapshot's decline must not land on a live row", got)
	}
	if f.readResource(t, "/widgets") == nil {
		t.Fatal("the live resources row disappeared across the rollback")
	}
	if got := f.entityData(t, resourceID); !mapsEqual(got, before) {
		t.Fatalf("entities changed: %v -> %v", before, got)
	}
}

// TestRollback_neverRewindsTheEntityCounter is D10 clause 25 and R18.
//
// `seq` is restored as max(current, snapshot), computed IN SQL inside the
// transaction. Writing the snapshot's value verbatim sets the counter BELOW
// a live entity key, and the next POST X then mints an id that already
// exists — which does NOT answer 500: the insert violates the UNIQUE, the
// mock plane logs it, declines the takeover and serves a GENERATED 201, so
// the caller is told the write succeeded and no row exists. The counter is
// read off the column rather than driven through a POST for the reason the
// clause gives: that needs internal/mockplane and internal/resources, and
// this package imports neither.
func TestRollback_neverRewindsTheEntityCounter(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 2, seedCount: 2,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 2)

	point := f.create(t, "снимок при seq=2")

	// Anonymous clients wrote three more rows through the mock plane: seq
	// moved, and nothing bumped revision, which is exactly why fenceTx
	// cannot see this and a max taken in Go over the capture would not
	// either.
	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE resources SET seq = 5 WHERE id = ?", resourceID); err != nil {
		t.Fatalf("move seq: %v", err)
	}
	f.insertEntityRange(t, resourceID, 3, 5)

	f.rollback(t, point.ID)

	got := f.readResource(t, "/widgets")
	if got == nil {
		t.Fatal("the resources row disappeared across the rollback")
	}
	if got.seq != 5 {
		t.Fatalf("seq after the rollback = %d, want 5 — the live value, never the snapshot's 2", got.seq)
	}
	// seed_count IS restored verbatim: it is configuration, not a counter.
	if got.seedCount != 2 {
		t.Fatalf("seed_count = %d, want the snapshot's 2", got.seedCount)
	}
}

// TestRollback_resourceHalfIsInsideTheOneTransaction is D10 clause 26: a
// failure injected AFTER the resources UPSERT leaves the workspace exactly
// as it was.
//
// The injection is this package's only technique, and it dictates where the
// three resource statements live: a crafted snapshot whose SECOND endpoint
// fails customep's own validatePath, inside customep.ReplaceAllTx. The
// resource statements therefore run BEFORE that call — placed after it,
// they could never be reached by an injected failure and this clause would
// be green over a resource half running in its own transaction.
func TestRollback_resourceHalfIsInsideTheOneTransaction(t *testing.T) {
	f := newFixture(t, 20)
	f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")

	settings := f.settings(t)
	b := bundle.New("ws-a", settings, bundle.SpecRef{Name: "fixture", Hash: "hash-1"}, nil)
	b.Resources = []bundle.ResourceEntry{{
		RouteFamily: "/quizzes", Name: "quizzes", IDField: "id", IDStrategy: "seq",
		ScopeParams: []string{}, EntitySchema: "#/components/schemas/Quiz",
		Wrapper: jsonx.RawMessage(wrapperJSON), FilterMap: jsonx.RawMessage(`{}`),
		Seq: 9, SeedCount: 9,
	}}
	b.Decisions = []bundle.DecisionEntry{{RouteFamily: "/quizzes", State: "confirmed"}}
	b.Endpoints = []bundle.EndpointEntry{
		{Method: "GET", Path: "/aaa", OverrideOn: true, ActiveStatus: 200, Responses: map[string]overrides.Variant{}},
		{Method: "GET", Path: "/bbb?x", OverrideOn: true, ActiveStatus: 200, Responses: map[string]overrides.Variant{}},
	}
	if err := bundle.Validate(b); err != nil {
		t.Fatalf("the fixture must pass bundle.Validate to reach the write loop at all: %v", err)
	}
	bad := f.insertCrafted(t, b)

	_, err := f.repo.Rollback(t.Context(), f.wsID, bad, f.userID, false, "")
	if !errors.Is(err, customep.ErrInvalidRow) {
		t.Fatalf("rollback error = %v, want customep.ErrInvalidRow from the write loop's second row", err)
	}

	if f.readResource(t, "/quizzes") != nil {
		t.Fatal("the resources UPSERT committed despite the failed apply: the resource half runs in its own transaction")
	}
	if got := f.decisionState(t, "/quizzes"); got != "" {
		t.Fatalf("a resource decision committed despite the failed apply: %q", got)
	}
	if f.readResource(t, "/widgets") == nil {
		t.Fatal("the pre-existing resources row disappeared")
	}
}

// TestReset_leavesResourcesAndEntitiesIntact is D10 clause 27: «сбросить
// всё к спеке» resets the workspace LAYER — op_overrides and
// custom_endpoints — and touches neither the resource roster nor a single
// entity row.
//
// resetTx is the third ReplaceAllTx call site in this package and therefore
// the natural place to add a fourth for a third table; R16's rule covers
// Reset exactly as it covers Rollback, and for the identical reason — that
// fourth call would cascade every entity row away.
func TestReset_leavesResourcesAndEntitiesIntact(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	f.createEndpoint(t, f.wsID, "GET", "/live")
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 3, seedCount: 3,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 3)
	before := f.entityData(t, resourceID)

	out := f.reset(t)
	if !out.Changed {
		t.Fatal("the reset reported no change; the fixture is not the state it claims")
	}
	if s := f.pinnedStatus(t); s != nil {
		t.Fatalf("the reset did not delete the override: %v", s)
	}

	if f.readResource(t, "/widgets") == nil {
		t.Fatal("the reset deleted the resources row")
	}
	if got := f.decisionState(t, "/widgets"); got != "confirmed" {
		t.Fatalf("decision after the reset = %q, want \"confirmed\"", got)
	}
	if got := f.entityData(t, resourceID); !mapsEqual(got, before) {
		t.Fatalf("the reset changed entity rows: %v -> %v", before, got)
	}
}

// TestCapture_allFourCallSitesWriteData is property 1's own enumeration
// taken literally: Create, Auto, Rollback (its own pre-destructive row) and
// Reset (its own pre-destructive row) all write a non-NULL data_snap over a
// workspace holding a confirmed family — D5's always-capture reaches all
// four, not only the two ("Create", "Rollback") an implementer reaching for
// the obvious pair would remember.
func TestCapture_allFourCallSitesWriteData(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	f.createEndpoint(t, f.wsID, "GET", "/live")
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	manual := f.create(t, "manual")
	if !manual.HasData {
		t.Fatal("Create: HasData = false")
	}

	auto := f.auto(t, "auto", 60)
	if auto == nil || !auto.HasData {
		t.Fatalf("Auto: %+v, want a row with HasData=true", auto)
	}

	beforeMachine := len(f.machineMade(t))
	f.rollback(t, manual.ID) // restoreData:false — still writes a pre-destructive row WITH data
	machine := f.machineMade(t)
	if len(machine) != beforeMachine+1 {
		t.Fatalf("machine-made count after Rollback = %d, want %d", len(machine), beforeMachine+1)
	}
	pd, err := f.repo.Get(t.Context(), f.wsID, machine[len(machine)-1])
	if err != nil {
		t.Fatalf("get Rollback's pre-destructive checkpoint: %v", err)
	}
	if !pd.HasData {
		t.Fatal("Rollback's own pre-destructive checkpoint: HasData = false")
	}

	resetOut := f.reset(t)
	if !resetOut.Changed {
		t.Fatal("Reset reported no change; the fixture is not the state it claims")
	}
	resetMachine := f.machineMade(t)
	rd, err := f.repo.Get(t.Context(), f.wsID, resetMachine[len(resetMachine)-1])
	if err != nil {
		t.Fatalf("get Reset's pre-destructive checkpoint: %v", err)
	}
	if !rd.HasData {
		t.Fatal("Reset's own pre-destructive checkpoint: HasData = false")
	}
}

// TestCapture_scopesToTheOwningWorkspace is D5.2's own load-bearing clause,
// observed directly: a SECOND workspace confirming the SAME route_family
// with its OWN rows contributes nothing to this workspace's data_snap. The
// hazard is not a leak into a blob — because D6 step 1 resolves a restore
// by (workspace_id, route_family), a foreign family sharing a route path
// would get the OTHER workspace's rows WRITTEN INTO IT on restore.
func TestCapture_scopesToTheOwningWorkspace(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	otherSettings := domain.DefaultSettings()
	otherSettings.Normalize()
	otherWsID := insertWorkspace(t, f.db, "ws-b", nil, otherSettings)
	otherRes, err := f.db.W.ExecContext(t.Context(), `
		INSERT INTO resources (workspace_id, route_family, name, id_field, id_strategy, scope_params,
			entity_schema, wrapper, filter_map, seq, seed_count)
		VALUES (?, '/widgets', 'widgets', 'id', 'seq', '[]', '#/components/schemas/Widget', ?, '{}', 1, 1)`,
		otherWsID, wrapperJSON)
	if err != nil {
		t.Fatalf("insert the other workspace's resource: %v", err)
	}
	otherResourceID, err := otherRes.LastInsertId()
	if err != nil {
		t.Fatalf("other resource id: %v", err)
	}
	if _, err := f.db.W.ExecContext(t.Context(),
		`INSERT INTO resource_decisions (workspace_id, route_family, state) VALUES (?, '/widgets', 'confirmed')`,
		otherWsID); err != nil {
		t.Fatalf("write the other workspace's decision: %v", err)
	}
	now := time.Now().UnixMilli()
	if _, err := f.db.W.ExecContext(t.Context(),
		`INSERT INTO entities (resource_id, entity_key, data, created_at, updated_at) VALUES (?, '1', ?, ?, ?)`,
		otherResourceID, `{"id":1,"name":"the other workspace's own row"}`, now, now); err != nil {
		t.Fatalf("insert the other workspace's entity: %v", err)
	}

	point := f.create(t, "капчер, две мастерские")

	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 {
		t.Fatalf("families = %+v, want exactly one (this workspace's own)", data.Families)
	}
	if len(data.Families[0].Rows) != 1 {
		t.Fatalf("rows for /widgets = %d, want 1 — the other workspace's row leaked in", len(data.Families[0].Rows))
	}
	if strings.Contains(string(data.Families[0].Rows[0].Data), "other workspace") {
		t.Fatalf("data_snap carries the other workspace's row: %s", data.Families[0].Rows[0].Data)
	}
}

// TestCapture_seesARowCreatedInTheWindowBeforeDbWrite is D5.1's whole
// argument, observed directly: [captureEntitiesTx] runs INSIDE the write
// transaction, never on the reader pool, so a row created in the window
// between [Repo.captureSnapshot] (config, pre-transaction) and db.Write
// (entities, inside it) still lands in the checkpoint's data_snap.
// [capturePreWriteHook] is the seam, fired exactly once per capture call,
// right where D5.1 says the hazard lives — the whole reason a counter-based
// fence was rejected (D5.1) is that this window cannot be closed any other
// way.
func TestCapture_seesARowCreatedInTheWindowBeforeDbWrite(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	restore := capturePreWriteHook
	t.Cleanup(func() { capturePreWriteHook = restore })
	capturePreWriteHook = func() {
		// Simulates an anonymous POST X landing exactly in D5.1's window.
		f.insertEntityRange(t, resourceID, 2, 2)
	}

	point := f.create(t, "капчер в окне")

	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 {
		t.Fatalf("families = %+v, want one", data.Families)
	}
	if len(data.Families[0].Rows) != 2 {
		t.Fatalf("rows for /widgets = %d, want 2 — the row created in the capture window is missing", len(data.Families[0].Rows))
	}
}

// TestCapture_carriesAConfirmedFamilyWithNoRowsAsEmptyNotAbsent is D5.2's
// own promise: a confirmed family holding zero entity rows is present in
// the document with an EMPTY relation, never dropped — the reason
// [captureEntitiesTx]'s own read is a LEFT JOIN, not an INNER one.
func TestCapture_carriesAConfirmedFamilyWithNoRowsAsEmptyNotAbsent(t *testing.T) {
	f := newFixture(t, 20)
	f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 0, seedCount: 0,
	})
	f.writeDecision(t, "/widgets", "confirmed")

	point := f.create(t, "пустое семейство")

	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 || data.Families[0].RouteFamily != "/widgets" {
		t.Fatalf("families = %+v, want one entry for /widgets", data.Families)
	}
	if len(data.Families[0].Rows) != 0 {
		t.Fatalf("rows = %+v, want an empty slice, not dropped", data.Families[0].Rows)
	}
}

// TestCapture_carriesANonContiguousKeySetInCanonicalOrder is
// [bundle.EntityRow]'s own reminder made concrete: a captured key set need
// not be 1..N (Confirm and reseed assign positionally; EntityStore.Create
// allocates from the counter), and the canonical byte order sorts rows by
// DECIMAL value, not lexically — "2" before "10".
func TestCapture_carriesANonContiguousKeySetInCanonicalOrder(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 10, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	now := time.Now().UnixMilli()
	for _, key := range []string{"10", "2", "7"} {
		if _, err := f.db.W.ExecContext(t.Context(),
			`INSERT INTO entities (resource_id, entity_key, data, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			resourceID, key, fmt.Sprintf(`{"id":%s}`, key), now, now); err != nil {
			t.Fatalf("insert entity %s: %v", key, err)
		}
	}

	point := f.create(t, "непрерывные ключи")

	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 || len(data.Families[0].Rows) != 3 {
		t.Fatalf("data_snap = %+v, want one family with 3 rows", data.Families)
	}
	gotKeys := make([]string, 0, len(data.Families[0].Rows))
	for _, r := range data.Families[0].Rows {
		gotKeys = append(gotKeys, r.EntityKey)
	}
	wantKeys := []string{"2", "7", "10"}
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("entityKey order = %v, want %v (decimal, not lexical)", gotKeys, wantKeys)
	}
}
