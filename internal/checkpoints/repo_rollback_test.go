// repo_rollback_test.go: Rollback and Reset restoring the CONFIG layer
// (op_overrides, custom_endpoints, settings) and the all-or-nothing
// atomicity that write carries — the resource/entity-data half of a
// checkpoint lives in repo_resource_test.go and repo_data_restore_test.go
// instead. Split out of the former repo_test.go (package checkpoints, not
// checkpoints_test — see helpers_test.go's own comment for why, and why no
// test in this package calls t.Parallel).
package checkpoints

import (
	"database/sql"
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/overrides"
)

// TestRollback_restoresTheWholeLayerAndBumpsExactlyOnce covers the three
// halves of a restore in one pass — settings wholesale (C10), overrides and
// custom endpoints (C1) — plus DESIGN §12:774-776's max+1 rule. A double
// bump (one in ReplaceAllTx and one here) fails the revision assertion.
func TestRollback_restoresTheWholeLayerAndBumpsExactlyOnce(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	kept := f.createEndpoint(t, f.wsID, "GET", "/kept")
	// A row on a SECOND workspace with a HIGHER rowid: without it a
	// workspace-scoped delete-and-reinsert would hand /kept its own freed
	// id back (custom_endpoints.id has no AUTOINCREMENT, 0001_init.sql:191)
	// and the id-stability assertion below would pass vacuously — §G obs 6
	// spells this out.
	other := insertWorkspace(t, f.db, "ws-b", nil, domain.DefaultSettings())
	f.createEndpoint(t, other, "GET", "/elsewhere")

	point := f.create(t, "точка")

	// Move everything the restore has to put back.
	f.pinStatus(t, 402)
	f.createEndpoint(t, f.wsID, "GET", "/added-later")
	if _, _, err := f.ovr.Put(t.Context(), f.wsID, overrides.OpKey("GET", "/other"), func(row *overrides.Row) error {
		row.OverrideOn = true
		return nil
	}); err != nil {
		t.Fatalf("add a second override: %v", err)
	}

	// Read the revision immediately before the call: the rule is max+1 over
	// whatever the workspace stands at NOW, never a number the snapshot
	// carries (DESIGN §12:774-776 — numbers are never reused).
	revisionBefore := f.revision(t)
	out := f.rollback(t, point.ID)

	if out.Revision != revisionBefore+1 {
		t.Fatalf("revision = %d, want exactly one past %d (max+1, never a reused number)", out.Revision, revisionBefore)
	}
	if got := f.revision(t); got != out.Revision {
		t.Fatalf("stored revision = %d, returned %d", got, out.Revision)
	}
	if !out.Changed {
		t.Fatal("Rollback reported Changed=false")
	}
	if out.ScenarioActive {
		t.Fatal("ScenarioActive=true with no scenario on the workspace")
	}

	if s := f.pinnedStatus(t); s == nil || *s != 418 {
		t.Fatalf("pinned status = %v, want the snapshot's 418", s)
	}
	if _, err := f.ovr.Get(t.Context(), f.wsID, overrides.OpKey("GET", "/other")); !errors.Is(err, overrides.ErrNotFound) {
		t.Fatalf("an override absent from the snapshot survived the restore: %v", err)
	}

	rows := f.endpoints(t)
	if len(rows) != 1 {
		t.Fatalf("custom endpoints after rollback = %d, want 1", len(rows))
	}
	if rows[0].Path != "/kept" {
		t.Fatalf("surviving endpoint = %q, want /kept", rows[0].Path)
	}
	// C1's whole reason for upsert-over-truncate: the UI holds this id.
	if rows[0].ID != kept.ID {
		t.Fatalf("endpoint id changed across the rollback: %d -> %d (truncate-and-reinsert)", kept.ID, rows[0].ID)
	}
}

// TestRollback_restoresSettingsWholesaleNotMerged is C10's half that a
// merge would pass: notFoundBody is the one TOP-LEVEL settings field absent
// from the JSON when unset (domain/settings.go:43, omitempty), so it is the
// only one whose snapshot value can differ from the live value by being
// ABSENT. listSize alone is restored identically by a merge and a replace.
func TestRollback_restoresSettingsWholesaleNotMerged(t *testing.T) {
	f := newFixture(t, 20)
	point := f.create(t, "до правки настроек")
	beforeKey := f.settings(t).Auth.SigningKey

	live := f.settings(t)
	live.ListSize = 42
	live.Auth.SigningKey = "0123456789abcdef"
	live.NotFoundBody = []byte(`{"marker":true}`)
	f.writeSettings(t, live)

	f.rollback(t, point.ID)

	got := f.settings(t)
	if got.ListSize != 5 {
		t.Fatalf("listSize = %d, want the snapshot's 5", got.ListSize)
	}
	if got.Auth.SigningKey != beforeKey {
		t.Fatalf("auth.signingKey = %q, want the snapshot's %q", got.Auth.SigningKey, beforeKey)
	}
	if len(got.NotFoundBody) != 0 {
		t.Fatalf("notFoundBody = %s, want it gone — a MERGE keeps it, a wholesale replace does not", got.NotFoundBody)
	}
}

// TestRollback_restoresBasePathValuesBesideBasePath is P3h's P12b: a
// wholesale settings restore carries basePathValues alongside basePath, its
// own property distinct from P12 (D9) because writeSettingsTx and
// restoreEntitiesTx are different functions over different tables — a
// mutation of one cannot reach the other, so P12's own mutation
// (restoreEntitiesTx dropping BaseScopeKey from the INSERT) cannot redden
// this test, and this one's own mutation (writeSettingsTx dropping
// basePathValues from the restore) cannot redden P12's. Restoring the
// prefix without the declared values it takes would leave a workspace whose
// every resource-served request refuses with base_scope_undeclared — the
// consequence the checkpoint's own comment names.
func TestRollback_restoresBasePathValuesBesideBasePath(t *testing.T) {
	f := newFixture(t, 20)
	f.writeSettings(t, settingsWith(f.settings(t), func(s *domain.Settings) {
		s.BasePath = "/orgs/{orgId}"
		s.BasePathValues = []string{"7", "8"}
	}))
	point := f.create(t, "до правки basePathValues")

	// A settings edit since the checkpoint drops the declared set and
	// relocates the prefix — the fixture a wholesale restore is supposed to
	// overwrite, mirroring TestRollback_restoresSettingsWholesaleNotMerged's
	// own "edit since the checkpoint" step.
	f.writeSettings(t, settingsWith(f.settings(t), func(s *domain.Settings) {
		s.BasePath = "/tenants/{tenantId}"
		s.BasePathValues = []string{"9"}
	}))

	f.rollback(t, point.ID)

	got := f.settings(t)
	if got.BasePath != "/orgs/{orgId}" {
		t.Fatalf("basePath = %q, want the checkpoint's %q", got.BasePath, "/orgs/{orgId}")
	}
	want := []string{"7", "8"}
	if len(got.BasePathValues) != len(want) || got.BasePathValues[0] != want[0] || got.BasePathValues[1] != want[1] {
		t.Fatalf("basePathValues = %v, want the checkpoint's %v (it must travel WITH basePath, not be dropped by a wholesale restore)", got.BasePathValues, want)
	}
}

// TestRollback_allocatesAFreshEditVersionForSettings is D9's rule stated by
// name for writeSettingsTx: it writes `settings`, a field
// PATCH /api/workspaces/{id} guards, so it must ALLOCATE from
// workspaces.edit_seq rather than leave a pre-rollback token matching the
// just-restored row. A PATCH caller who read edit_version before the
// rollback and would otherwise pass A3's compare-and-swap check against the
// restored row — silently overwriting what the rollback just put back — is
// exactly the failure this allocation exists to close.
func TestRollback_allocatesAFreshEditVersionForSettings(t *testing.T) {
	f := newFixture(t, 20)
	point := f.create(t, "до правки настроек")

	preRollbackToken := f.editVersion(t)

	live := f.settings(t)
	live.ListSize = 42
	f.writeSettings(t, live)

	f.rollback(t, point.ID)

	got := f.editVersion(t)
	if got == preRollbackToken {
		t.Fatalf("edit_version = %d unchanged across rollback, want a fresh value (the rollback writes settings, a guarded field)", got)
	}
	if got <= preRollbackToken {
		t.Fatalf("edit_version = %d, want strictly greater than the pre-rollback token %d (the allocator never hands out a value it has handed out before)", got, preRollbackToken)
	}
}

// TestRollback_protectsWhatItDestroys is §G obs 5's unit half: the
// pre-destructive checkpoint holds the state being DESTROYED, not the one
// being restored. An implementation that snapshots AFTER the apply stores
// 418 here and the second rollback becomes a no-op.
func TestRollback_protectsWhatItDestroys(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	point := f.create(t, "418")

	f.pinStatus(t, 402)
	f.rollback(t, point.ID)

	if s := f.pinnedStatus(t); s == nil || *s != 418 {
		t.Fatalf("after the first rollback the status is %v, want 418", s)
	}

	machine := f.machineMade(t)
	if len(machine) != 1 {
		t.Fatalf("machine-made checkpoints = %d, want 1", len(machine))
	}
	protective, err := f.repo.Get(t.Context(), f.wsID, machine[0])
	if err != nil {
		t.Fatalf("get the pre-destructive checkpoint: %v", err)
	}
	if protective.Kind != KindPreDestructive {
		t.Fatalf("kind = %q, want %q", protective.Kind, KindPreDestructive)
	}
	if want := rollbackLabel(point.ID); protective.Label != want {
		t.Fatalf("label = %q, want %q (C14, Russian and server-generated)", protective.Label, want)
	}
	if len(protective.Bundle.Overrides) != 1 || protective.Bundle.Overrides[0].ActiveStatus == nil ||
		*protective.Bundle.Overrides[0].ActiveStatus != 402 {
		t.Fatalf("the protective snapshot does not hold the DESTROYED state (402): %+v", protective.Bundle.Overrides)
	}

	// And it is usable: rolling back to it returns the state the first
	// rollback threw away.
	f.rollback(t, machine[0])
	if s := f.pinnedStatus(t); s == nil || *s != 402 {
		t.Fatalf("rolling back to the protective checkpoint gave %v, want 402", s)
	}
}

// TestRollback_reportsAnActiveScenario is C8: the operation is ALLOWED
// while a scenario is active, and the flag §C's body carries says so, so
// the screen can warn that part of the restored layer is masked. A 409 here
// would refuse a demonstrably visible operation.
func TestRollback_reportsAnActiveScenario(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	point := f.create(t, "точка")
	f.pinStatus(t, 402)

	res, err := f.db.W.ExecContext(t.Context(),
		"INSERT INTO scenarios (workspace_id, name, snapshot, created_at) VALUES (?, 'S', X'7B7D', unixepoch())", f.wsID)
	if err != nil {
		t.Fatalf("insert scenario: %v", err)
	}
	scenarioID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("scenario id: %v", err)
	}
	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE workspaces SET scenario_id = ?, revision = revision + 1 WHERE id = ?", scenarioID, f.wsID); err != nil {
		t.Fatalf("activate scenario: %v", err)
	}

	out := f.rollback(t, point.ID)
	if !out.ScenarioActive {
		t.Fatal("ScenarioActive=false while a scenario is active")
	}
	reset := f.reset(t)
	if !reset.ScenarioActive {
		t.Fatal("Reset's ScenarioActive=false while a scenario is active")
	}

	var active sql.NullInt64
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT scenario_id FROM workspaces WHERE id = ?", f.wsID).Scan(&active); err != nil {
		t.Fatalf("read scenario_id: %v", err)
	}
	if !active.Valid || active.Int64 != scenarioID {
		t.Fatalf("the scenario was deactivated by the restore: %v", active)
	}
}

// TestReset_deletesBothKindsOfEditAndKeepsSettings is C9: «сбросить всё к
// спеке» reaches custom endpoints too — a custom endpoint is not in the
// spec — while settings survive, because they are the workspace's identity
// and resetting basePath would move every route.
func TestReset_deletesBothKindsOfEditAndKeepsSettings(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	f.createEndpoint(t, f.wsID, "GET", "/custom")
	before := f.revision(t)
	beforeSettings := f.settings(t)

	out := f.reset(t)

	if !out.Changed {
		t.Fatal("Changed=false for a reset that deleted two rows")
	}
	if out.Revision != before+1 {
		t.Fatalf("revision = %d, want %d", out.Revision, before+1)
	}
	if s := f.pinnedStatus(t); s != nil {
		t.Fatalf("the override survived the reset: %v", s)
	}
	if rows := f.endpoints(t); len(rows) != 0 {
		t.Fatalf("custom endpoints survived the reset: %d", len(rows))
	}
	if got := f.settings(t); got.ListSize != beforeSettings.ListSize || got.Auth.SigningKey != beforeSettings.Auth.SigningKey {
		t.Fatalf("settings changed on a reset: %+v", got)
	}

	machine := f.machineMade(t)
	if len(machine) != 1 {
		t.Fatalf("machine-made checkpoints = %d, want the one protecting the reset", len(machine))
	}
	protective, err := f.repo.Get(t.Context(), f.wsID, machine[0])
	if err != nil {
		t.Fatalf("get the protective checkpoint: %v", err)
	}
	if protective.Label != resetLabel {
		t.Fatalf("label = %q, want %q", protective.Label, resetLabel)
	}
	if len(protective.Bundle.Overrides) != 1 || len(protective.Bundle.Endpoints) != 1 {
		t.Fatalf("the protective checkpoint captured only one table: %d overrides, %d endpoints",
			len(protective.Bundle.Overrides), len(protective.Bundle.Endpoints))
	}

	// And the reset is undoable — the point of taking the checkpoint first.
	f.rollback(t, machine[0])
	if s := f.pinnedStatus(t); s == nil || *s != 418 {
		t.Fatalf("the override did not come back: %v", s)
	}
	if rows := f.endpoints(t); len(rows) != 1 {
		t.Fatalf("the custom endpoint did not come back: %d rows", len(rows))
	}
}

// TestReset_noOpWritesNothing is C9: a reset that would delete nothing
// writes no checkpoint, bumps no revision and succeeds. The decision comes
// from the pre-transaction read, never from a count returned by the apply
// (ReplaceAllTx deliberately returns none), and Changed is the only carrier
// the handler's `changed` field has.
func TestReset_noOpWritesNothing(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	f.reset(t)

	revisionAfterFirst := f.revision(t)
	checkpointsAfterFirst := len(f.list(t))

	out := f.reset(t)

	if out.Changed {
		t.Fatal("Changed=true for a reset with nothing to delete (C9)")
	}
	if out.Revision != revisionAfterFirst {
		t.Fatalf("revision moved on a no-op reset: %d -> %d", revisionAfterFirst, out.Revision)
	}
	if got := f.revision(t); got != revisionAfterFirst {
		t.Fatalf("stored revision moved on a no-op reset: %d -> %d", revisionAfterFirst, got)
	}
	if got := len(f.list(t)); got != checkpointsAfterFirst {
		t.Fatalf("a no-op reset wrote a checkpoint: %d -> %d", checkpointsAfterFirst, got)
	}
}

// TestRollback_isAtomicWhenTheApplyFailsMidway is §G obs 15. The injection
// point is the one that matters: the SECOND endpoint of the snapshot, a row
// whose shape passes bundle.Validate (non-empty method, leading-slash path)
// and fails customep's own validatePath, which rejects "?" because it would
// desync from router.CanonicalPath's segment splitting. C4 duty 4 validates
// per row INSIDE the write loop, so the first endpoint really reaches the
// table before the second one fails — which is what makes "atomic" and
// "already half-applied" distinguishable at all. Injecting immediately
// after the checkpoint insert would NOT fail a split implementation.
func TestRollback_isAtomicWhenTheApplyFailsMidway(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	liveEndpoint := f.createEndpoint(t, f.wsID, "GET", "/live")

	beforeRevision := f.revision(t)
	beforeSettings := f.settings(t)

	// A snapshot that differs from the live workspace in every table, so
	// "nothing committed" is observable in all three.
	snapSettings := beforeSettings
	snapSettings.ListSize = 77
	b := bundle.New("ws-a", snapSettings, bundle.SpecRef{Name: "fixture", Hash: "hash-1"}, nil)
	b.Endpoints = []bundle.EndpointEntry{
		{Method: "GET", Path: "/aaa", OverrideOn: true, ActiveStatus: 200, Responses: map[string]overrides.Variant{}},
		{Method: "GET", Path: "/bbb?x", OverrideOn: true, ActiveStatus: 200, Responses: map[string]overrides.Variant{}},
	}
	if err := bundle.Validate(b); err != nil {
		t.Fatalf("the fixture must pass bundle.Validate to reach the write loop at all: %v", err)
	}
	bad := f.insertCrafted(t, b)
	// Counted AFTER the crafted row is stored: what must not appear is the
	// PRE-DESTRUCTIVE checkpoint this rollback would write.
	beforeCheckpoints := len(f.list(t))

	_, err := f.repo.Rollback(t.Context(), f.wsID, bad, f.userID, false, "")
	if err == nil {
		t.Fatal("rollback over a snapshot whose second endpoint is invalid succeeded")
	}
	if !errors.Is(err, customep.ErrInvalidRow) {
		t.Fatalf("rollback error = %v, want customep.ErrInvalidRow from the write loop's second row", err)
	}

	if got := f.revision(t); got != beforeRevision {
		t.Fatalf("revision moved despite the failed apply: %d -> %d", beforeRevision, got)
	}
	if got := f.settings(t); got.ListSize != beforeSettings.ListSize {
		t.Fatalf("settings committed despite the failed apply: listSize %d", got.ListSize)
	}
	if got := len(f.list(t)); got != beforeCheckpoints {
		t.Fatalf("a checkpoint committed despite the failed apply: %d -> %d", beforeCheckpoints, got)
	}
	if s := f.pinnedStatus(t); s == nil || *s != 418 {
		t.Fatalf("op_overrides was modified despite the failed apply: %v", s)
	}
	rows := f.endpoints(t)
	if len(rows) != 1 || rows[0].ID != liveEndpoint.ID || rows[0].Path != "/live" {
		t.Fatalf("custom_endpoints was modified despite the failed apply: %+v", rows)
	}
}
