// repo_data_restore_test.go: P3d, the entity-data half of a checkpoint
// (D5-D7, acceptance properties 1,3,4,5,7) — Rollback's data_snap restore
// over FLAT (non-nested) families, and Capture's two degrade bands (the
// probe estimate, then compressSnapshot's own write-side ceiling). The
// nested/scoped variants live in repo_data_restore_nested_test.go. Split
// out of the former repo_test.go (package checkpoints, not
// checkpoints_test — see helpers_test.go's own comment for why, and why no
// test in this package calls t.Parallel).
package checkpoints

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
)

// TestRollback_dataRestoreOverwritesARowCreatedAfterTheCheckpoint is
// property 3's declared "FIRST fixture", run under restoreData:true: D6
// step 3 (DELETE) and step 4 (INSERT verbatim) win over anything an
// anonymous POST X wrote to the family after the checkpoint.
func TestRollback_dataRestoreOverwritesARowCreatedAfterTheCheckpoint(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	point := f.create(t, "до новой строки")
	f.insertEntityRange(t, resourceID, 2, 2)

	f.rollbackData(t, point.ID)

	got := f.entityData(t, resourceID)
	want := map[string]string{"1": `{"id":1,"name":"row 1"}`}
	if !mapsEqual(got, want) {
		t.Fatalf("entities after restoreData:true = %v, want exactly the checkpoint's %v (the post-checkpoint row must be gone)", got, want)
	}
}

// TestRollback_falsePathLeavesAPostCheckpointRowUntouched is property 3's
// SAME fixture as the test above, run with restoreData:false — the false
// path's own new guard (D6), and deliberately NOT
// [TestRestore_neverTouchesAnEntityRow]: that test's rows all PREDATE their
// checkpoint, so an unconditional restore re-inserting them verbatim is
// invisible to its map[entity_key]data comparison. This fixture's row
// POSTDATES the checkpoint, which only a false path that never calls
// [restoreEntitiesTx] at all can leave standing.
func TestRollback_falsePathLeavesAPostCheckpointRowUntouched(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	point := f.create(t, "до новой строки")
	f.insertEntityRange(t, resourceID, 2, 2)
	before := f.entityData(t, resourceID)

	f.rollback(t, point.ID) // restoreData:false/absent

	got := f.entityData(t, resourceID)
	if !mapsEqual(got, before) {
		t.Fatalf("entities after restoreData:false = %v, want unchanged %v", got, before)
	}
}

// TestRollback_dataRestorePreservesTheCapturedKeySet is property 3's
// non-contiguous-key fixture: a restore replays STORED keys, never
// re-derived positionally the way reseed does.
func TestRollback_dataRestorePreservesTheCapturedKeySet(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 10, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	now := time.Now().UnixMilli()
	for _, key := range []string{"2", "7", "10"} {
		if _, err := f.db.W.ExecContext(t.Context(),
			`INSERT INTO entities (resource_id, entity_key, data, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
			resourceID, key, fmt.Sprintf(`{"id":%s}`, key), now, now); err != nil {
			t.Fatalf("insert entity %s: %v", key, err)
		}
	}
	point := f.create(t, "непрерывный набор")

	// Replace with a DIFFERENT, contiguous set, so a restore that
	// re-derives keys positionally is indistinguishable from one that does
	// not unless the captured keys survive verbatim.
	if _, err := f.db.W.ExecContext(t.Context(), "DELETE FROM entities WHERE resource_id = ?", resourceID); err != nil {
		t.Fatalf("clear entities: %v", err)
	}
	f.insertEntities(t, resourceID, 2)

	f.rollbackData(t, point.ID)

	got := f.entityData(t, resourceID)
	gotKeys := make([]string, 0, len(got))
	for k := range got {
		gotKeys = append(gotKeys, k)
	}
	// Sorted as DECIMAL INTEGERS, not lexically — "2" < "7" < "10" is not
	// the string order, and this assertion is about which KEYS survived,
	// not about string collation.
	slices.SortFunc(gotKeys, func(a, b string) int {
		an, _ := strconv.Atoi(a)
		bn, _ := strconv.Atoi(b)
		return an - bn
	})
	wantKeys := []string{"2", "7", "10"}
	if !slices.Equal(gotKeys, wantKeys) {
		t.Fatalf("restored keys = %v, want the captured, non-contiguous %v", gotKeys, wantKeys)
	}
}

// TestRollback_dataRestoreEmptiesAFamilyPopulatedSinceTheCheckpoint is
// property 3's "carried family POPULATED at rollback time whose relation in
// the DOCUMENT is empty" fixture: the document's own empty relation wins,
// exactly as a non-empty one would.
func TestRollback_dataRestoreEmptiesAFamilyPopulatedSinceTheCheckpoint(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 0, seedCount: 0,
	})
	f.writeDecision(t, "/widgets", "confirmed")

	point := f.create(t, "пусто на слепке")

	// Populated by an anonymous POST X after the (empty) checkpoint.
	f.insertEntities(t, resourceID, 3)

	f.rollbackData(t, point.ID)

	if got := f.entityData(t, resourceID); len(got) != 0 {
		t.Fatalf("entities after restoreData:true over an empty-relation checkpoint = %v, want none", got)
	}
}

// TestRollback_dataRestoresMultipleCarriedFamilies is property 3's
// "more than one carried family" fixture.
func TestRollback_dataRestoresMultipleCarriedFamilies(t *testing.T) {
	f := newFixture(t, 20)
	widgetsID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 2, seedCount: 2,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, widgetsID, 2)
	quizzesID := f.insertResource(t, resourceFixture{
		family: "/quizzes", name: "quizzes", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/quizzes", "confirmed")
	f.insertEntities(t, quizzesID, 1)

	point := f.create(t, "два семейства")

	f.insertEntityRange(t, widgetsID, 3, 3)
	if _, err := f.db.W.ExecContext(t.Context(), "DELETE FROM entities WHERE resource_id = ?", quizzesID); err != nil {
		t.Fatalf("clear quizzes: %v", err)
	}

	f.rollbackData(t, point.ID)

	if got := f.entityData(t, widgetsID); len(got) != 2 {
		t.Fatalf("/widgets after restore = %v, want the checkpoint's 2 rows", got)
	}
	if got := f.entityData(t, quizzesID); len(got) != 1 {
		t.Fatalf("/quizzes after restore = %v, want the checkpoint's 1 row", got)
	}
}

// TestRollback_dataLeavesALiveFamilyAbsentFromTheDocumentUntouched is
// property 3's own "a live family absent from the document is untouched
// under restoreData:true" subject — the restoreData:true sibling of
// [TestRestore_neverTouchesAnEntityRow]'s restoreData:false coverage of the
// identical shape (a family confirmed AFTER the checkpoint, so neither
// config_snap nor data_snap names it at all: restoreEntitiesTx's step 2,
// D6, treats the resolve-by-route_family miss as SIG-AUTO, not an error).
//
// Verified by MUTATION: making restoreEntitiesTx additionally DELETE every
// live confirmed family the document does not carry (a plausible wrong
// implementation that strands a post-checkpoint confirm) reds only this
// test in the package.
func TestRollback_dataLeavesALiveFamilyAbsentFromTheDocumentUntouched(t *testing.T) {
	f := newFixture(t, 20)
	widgetsID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 2, seedCount: 2,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, widgetsID, 2)

	point := f.create(t, "до второго ресурса")

	// Confirmed AFTER the checkpoint: absent from both halves of the
	// document, exactly TestRestore_neverTouchesAnEntityRow's own setup for
	// the restoreData:false path.
	quizzesID := f.insertResource(t, resourceFixture{
		family: "/quizzes", name: "quizzes", idField: "id", wrapper: wrapperJSON, seq: 3, seedCount: 3,
	})
	f.writeDecision(t, "/quizzes", "confirmed")
	f.insertEntities(t, quizzesID, 3)

	before := f.entityData(t, quizzesID)

	f.rollbackData(t, point.ID)

	if got := f.entityData(t, quizzesID); !mapsEqual(got, before) {
		t.Fatalf("a live family absent from the document changed under restoreData:true: %v -> %v", before, got)
	}
	if f.readResource(t, "/quizzes") == nil {
		t.Fatal("the family confirmed after the checkpoint lost its resources row under restoreData:true")
	}
	// The carried family really did restore under restoreData:true —
	// otherwise this test would be green over a restore that touches
	// nothing at all.
	if got := f.entityData(t, widgetsID); len(got) != 2 {
		t.Fatalf("/widgets after restore = %v, want the checkpoint's 2 rows", got)
	}
}

// TestRollback_dataRestoreBringsBackRowsOfAFamilyDeclinedSince is property
// 3's "family declined after the checkpoint, which comes back confirmed AND
// populated" fixture — the sibling, under restoreData:true, of
// [TestRollback_restoresAResourceDeclinedAwaySinceTheSnapshot]'s
// restoreData:false result (entityCount == 0): this is what P3b's own
// "a rollback cannot bring back what a decline destroyed" reads the other
// way round, since P3d's data half now can.
func TestRollback_dataRestoreBringsBackRowsOfAFamilyDeclinedSince(t *testing.T) {
	f := newFixture(t, 20)
	bare := "bare"
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id",
		wrapper: wrapperJSON, writeForm: &bare, seq: 4, seedCount: 4,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 4)

	point := f.create(t, "до отказа")

	if _, err := f.db.W.ExecContext(t.Context(), "DELETE FROM resources WHERE id = ?", resourceID); err != nil {
		t.Fatalf("decline the family: %v", err)
	}
	f.writeDecision(t, "/widgets", "declined")

	f.rollbackData(t, point.ID)

	restored := f.readResource(t, "/widgets")
	if restored == nil {
		t.Fatal("restoreData:true did not restore the resources row")
	}
	var entityCount int
	if err := f.db.R.QueryRowContext(t.Context(), `
		SELECT COUNT(*) FROM entities WHERE resource_id IN
			(SELECT id FROM resources WHERE workspace_id = ?)`, f.wsID).Scan(&entityCount); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	if entityCount != 4 {
		t.Fatalf("entity rows after restoreData:true = %d, want the checkpoint's 4", entityCount)
	}
}

// TestRollback_dataRestoreResolvesByFamilyAcrossADeclineAndReconfirm is
// property 3's last fixture: a decline-and-reconfirm whose id is FORCED to
// differ from the declined row's — D4's whole reason a document addresses a
// family by route_family, never by resources.id, which is not stable across
// exactly this sequence.
func TestRollback_dataRestoreResolvesByFamilyAcrossADeclineAndReconfirm(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 2, seedCount: 2,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 2)

	point := f.create(t, "до отказа и переподтверждения")

	if _, err := f.db.W.ExecContext(t.Context(), "DELETE FROM resources WHERE id = ?", resourceID); err != nil {
		t.Fatalf("decline: %v", err)
	}
	f.writeDecision(t, "/widgets", "declined")

	// A decoy row FORCES the reconfirm below to mint a different id than
	// the declined one — SQLite is free to reuse a deleted rowid with no
	// AUTOINCREMENT on the column when the table would otherwise be empty,
	// and this fixture must not rely on that NOT happening.
	f.insertResource(t, resourceFixture{
		family: "/decoy", name: "decoy", idField: "id", wrapper: wrapperJSON, seq: 0, seedCount: 0,
	})

	reconfirmedID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	if reconfirmedID == resourceID {
		t.Fatalf("reconfirmed id = %d, same as the declined row's %d; the fixture failed to force the gap", reconfirmedID, resourceID)
	}
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, reconfirmedID, 1)

	f.rollbackData(t, point.ID)

	got := f.entityData(t, reconfirmedID)
	want := map[string]string{"1": `{"id":1,"name":"row 1"}`, "2": `{"id":2,"name":"row 2"}`}
	if !mapsEqual(got, want) {
		t.Fatalf("entities after restore across a decline/reconfirm = %v, want the checkpoint's %v", got, want)
	}
}

// TestRollback_dataRestoreRaisesSeqOverATornCheckpoint is property 4's own
// scenario and D5.1a's own math: config_snap's `resources.seq` (T1,
// pre-transaction) and data_snap's rows (T2, inside it) are captured at TWO
// instants, so a row minted in the window between them is in the rows but
// not reflected by the captured seq — a TORN checkpoint. Without
// [restoreEntitiesTx]'s own MAX-over-restored-keys term, the pre-existing
// `MAX(excluded.seq, resources.seq)` would leave seq at the checkpoint's
// stale value while the extra key sits in the table, and the next
// allocation would collide on it.
func TestRollback_dataRestoreRaisesSeqOverATornCheckpoint(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	restore := capturePreWriteHook
	t.Cleanup(func() { capturePreWriteHook = restore })
	capturePreWriteHook = func() {
		// An anonymous POST X landing in D5.1a's window: after
		// config_snap's seq was already read (T1), before data_snap's rows
		// are read inside the transaction (T2).
		if _, err := f.db.W.ExecContext(t.Context(),
			"UPDATE resources SET seq = 2 WHERE id = ?", resourceID); err != nil {
			t.Fatalf("bump seq in the capture window: %v", err)
		}
		f.insertEntityRange(t, resourceID, 2, 2)
	}
	point := f.create(t, "рваный слепок")
	capturePreWriteHook = restore // do not let the tear leak into the rollback below

	// Confirm the tear: config_snap's own captured seq is the STALE 1.
	stored, err := f.repo.Get(t.Context(), f.wsID, point.ID)
	if err != nil {
		t.Fatalf("get checkpoint: %v", err)
	}
	if len(stored.Bundle.Resources) != 1 || stored.Bundle.Resources[0].Seq != 1 {
		t.Fatalf("captured config seq = %+v, want 1 (the tear this fixture is built to produce)", stored.Bundle.Resources)
	}
	data := f.decodedData(t, point.ID)
	if len(data.Families) != 1 || len(data.Families[0].Rows) != 2 {
		t.Fatalf("captured rows = %+v, want 2 (key \"2\" landed inside the transaction)", data.Families)
	}

	// Roll the LIVE seq back down, as a fresh reseed might, so the
	// restore's own MAX-over-restored-keys term has something to raise.
	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE resources SET seq = 1 WHERE id = ?", resourceID); err != nil {
		t.Fatalf("reset live seq: %v", err)
	}

	f.rollbackData(t, point.ID)

	got := f.readResource(t, "/widgets")
	if got == nil {
		t.Fatal("resources row disappeared")
	}
	if got.seq < 2 {
		t.Fatalf("seq after restore = %d, want at least 2 (the largest restored key) — a torn checkpoint's stale captured seq must not win", got.seq)
	}
}

// TestRollback_refusesAHandBuiltDataDocumentOutsideValidateDatasDomain is
// property 4's other half: [bundle.ValidateData] runs once, on the WHOLE
// document, before anything is written — observed with a fixture no capture
// this build produces could ever create (a non-decimal entityKey), because
// every document this package's own capture writes already satisfies the
// domain by construction (D6).
func TestRollback_refusesAHandBuiltDataDocumentOutsideValidateDatasDomain(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	// A VALID data document to encode from — EncodeData itself calls
	// ValidateData and would refuse the corrupt shape directly, so the
	// corruption below is applied to the already-encoded BYTES, after the
	// one call this fixture is allowed to make to a validating encoder.
	doc, err := bundle.EncodeData(bundle.DataBundle{
		MockerData: bundle.DataVersion,
		Families: []bundle.FamilyEntry{{
			RouteFamily: "/widgets",
			Rows: []bundle.EntityRow{
				{ScopeKey: "", EntityKey: "1", Data: jsonx.RawMessage(`{"id":1}`), CreatedAt: 1, UpdatedAt: 1},
			},
		}},
	})
	if err != nil {
		t.Fatalf("encode a valid data document to craft from: %v", err)
	}
	corrupted := bytes.Replace(doc, []byte(`"entityKey":"1"`), []byte(`"entityKey":"abc"`), 1)
	if bytes.Equal(corrupted, doc) {
		t.Fatal("the entityKey replacement did not match; fixture is stale")
	}
	dataBlob, err := compressSnapshot(corrupted)
	if err != nil {
		t.Fatalf("compress the crafted data document: %v", err)
	}

	settings := f.settings(t)
	b := bundle.New("ws-a", settings, bundle.SpecRef{Name: "fixture", Hash: "hash-1"}, nil)
	configDoc, err := bundle.Encode(b)
	if err != nil {
		t.Fatalf("encode config bundle: %v", err)
	}
	configBlob, err := compressSnapshot(configDoc)
	if err != nil {
		t.Fatalf("compress config bundle: %v", err)
	}
	res, err := f.db.W.ExecContext(t.Context(), `
		INSERT INTO checkpoints (workspace_id, kind, label, config_snap, data_snap, created_at, created_by)
		VALUES (?, ?, 'подготовленный со сломанным data_snap', ?, ?, unixepoch(), ?)`,
		f.wsID, KindManual, configBlob, dataBlob, f.userID)
	if err != nil {
		t.Fatalf("insert crafted checkpoint: %v", err)
	}
	bad, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("crafted checkpoint id: %v", err)
	}

	beforeRevision := f.revision(t)
	beforeCheckpoints := len(f.list(t))
	before := f.entityData(t, resourceID)

	_, err = f.repo.Rollback(t.Context(), f.wsID, bad, f.userID, true, f.slug(t))
	if !errors.Is(err, ErrCorruptSnapshot) {
		t.Fatalf("rollback over a hand-built out-of-domain data document = %v, want ErrCorruptSnapshot", err)
	}

	if got := f.revision(t); got != beforeRevision {
		t.Fatalf("revision moved despite the refusal: %d -> %d", beforeRevision, got)
	}
	if got := len(f.list(t)); got != beforeCheckpoints {
		t.Fatalf("a checkpoint committed despite the refusal: %d -> %d", beforeCheckpoints, got)
	}
	if got := f.entityData(t, resourceID); !mapsEqual(got, before) {
		t.Fatalf("entities changed despite the refusal: %v -> %v", before, got)
	}
	if s := f.pinnedStatus(t); s == nil || *s != 418 {
		t.Fatalf("op_overrides changed despite the refusal: %v", s)
	}
}

// TestRollback_dataRestoreIsInsideTheOneTransaction is property 5: a
// restoreData:true rollback's entity write SHARES rollbackTx's single
// transaction. Injected the same way
// [TestRollback_isAtomicWhenTheApplyFailsMidway] does — a crafted snapshot
// whose second endpoint fails customep's own validatePath, inside
// customep.ReplaceAllTx, which [restoreEntitiesTx] runs BEFORE (D6) — so a
// failure there must leave the entity rows exactly as they were, proving
// the write shares the transaction rather than having already committed on
// its own.
func TestRollback_dataRestoreIsInsideTheOneTransaction(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	// Move the live row away from what the crafted checkpoint's data_snap
	// carries, so "unchanged after the injected failure" is not true
	// vacuously — if the restore ran, this value would become the
	// checkpoint's own.
	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE entities SET data = ? WHERE resource_id = ? AND entity_key = '1'",
		`{"id":1,"name":"changed after the checkpoint"}`, resourceID); err != nil {
		t.Fatalf("mutate the live entity: %v", err)
	}
	before := f.entityData(t, resourceID)

	settings := f.settings(t)
	b := bundle.New("ws-a", settings, bundle.SpecRef{Name: "fixture", Hash: "hash-1"}, nil)
	b.Resources = []bundle.ResourceEntry{{
		RouteFamily: "/widgets", Name: "widgets", IDField: "id", IDStrategy: "seq",
		ScopeParams: []string{}, EntitySchema: "#/components/schemas/Widget",
		Wrapper: jsonx.RawMessage(wrapperJSON), FilterMap: jsonx.RawMessage(`{}`),
		Seq: 1, SeedCount: 1,
	}}
	b.Decisions = []bundle.DecisionEntry{{RouteFamily: "/widgets", State: "confirmed"}}
	b.Endpoints = []bundle.EndpointEntry{
		{Method: "GET", Path: "/aaa", OverrideOn: true, ActiveStatus: 200, Responses: map[string]overrides.Variant{}},
		{Method: "GET", Path: "/bbb?x", OverrideOn: true, ActiveStatus: 200, Responses: map[string]overrides.Variant{}},
	}
	if err := bundle.Validate(b); err != nil {
		t.Fatalf("the fixture must pass bundle.Validate to reach the write loop at all: %v", err)
	}
	d := bundle.DataBundle{
		MockerData: bundle.DataVersion,
		Families: []bundle.FamilyEntry{{
			RouteFamily: "/widgets",
			Rows: []bundle.EntityRow{
				{ScopeKey: "", EntityKey: "1", Data: jsonx.RawMessage(`{"id":1,"name":"from the checkpoint"}`), CreatedAt: 1, UpdatedAt: 1},
			},
		}},
	}
	bad := f.insertCraftedWithData(t, b, d)

	_, err := f.repo.Rollback(t.Context(), f.wsID, bad, f.userID, true, f.slug(t))
	if !errors.Is(err, customep.ErrInvalidRow) {
		t.Fatalf("rollback error = %v, want customep.ErrInvalidRow from the write loop's second row", err)
	}

	got := f.entityData(t, resourceID)
	if !mapsEqual(got, before) {
		t.Fatalf("entities after the injected failure = %v, want unchanged %v (the restore must have shared the failed transaction)", got, before)
	}
}

// TestCapture_degradesWhenTheProbeRefuses is property 7's first band: over
// [maxDataProbeBytes], [captureEntitiesTx] reads NO entity row at all and
// the checkpoint is still written, with data_snap NULL — never an error,
// because entity rows are minted by an unauthenticated POST X (D5.2).
func TestCapture_degradesWhenTheProbeRefuses(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 1)

	restoreProbe := maxDataProbeBytes
	t.Cleanup(func() { maxDataProbeBytes = restoreProbe })
	maxDataProbeBytes = 1 // any real population is over this

	s := f.create(t, "проба выше бюджета")
	if s.HasData {
		t.Fatal("HasData = true over the probe's own ceiling, want a degrade (data_snap NULL)")
	}
	var dataSnap []byte
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT data_snap FROM checkpoints WHERE id = ?", s.ID).Scan(&dataSnap); err != nil {
		t.Fatalf("read the stored row: %v", err)
	}
	if dataSnap != nil {
		t.Fatal("data_snap is non-NULL despite the probe refusing")
	}
}

// TestCapture_degradesWhenCompressSnapshotRefusesThePassingProbe is
// property 7's second band: the probe (over [maxDataProbeBytes], a
// generous ESTIMATE) can pass while the actual encoded document still
// exceeds [maxSnapshotBytes] — [compressSnapshot]'s write-side ceiling. The
// swallow this test observes is the deliberate INVERSION of
// [compressSnapshot]'s own config-side policy: [Repo.captureSnapshot]
// propagates the identical error, [captureEntitiesTx] degrades instead
// (D5.2).
func TestCapture_degradesWhenCompressSnapshotRefusesThePassingProbe(t *testing.T) {
	f := newFixture(t, 20)
	resourceID := f.insertResource(t, resourceFixture{
		family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seq: 1, seedCount: 1,
	})
	f.writeDecision(t, "/widgets", "confirmed")
	f.insertEntities(t, resourceID, 20)

	// Measure both documents at the default ceilings, to place
	// maxSnapshotBytes strictly between them: above config_snap's own size
	// (which must still succeed) and below data_snap's (which must not).
	probe := f.create(t, "измерение")
	configDoc, err := decompressSnapshot(f.storedBlob(t, probe.ID))
	if err != nil {
		t.Fatalf("decompress config_snap: %v", err)
	}
	var rawDataSnap []byte
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT data_snap FROM checkpoints WHERE id = ?", probe.ID).Scan(&rawDataSnap); err != nil {
		t.Fatalf("read data_snap: %v", err)
	}
	dataDoc, err := decompressSnapshot(rawDataSnap)
	if err != nil {
		t.Fatalf("decompress data_snap: %v", err)
	}
	if len(dataDoc) <= len(configDoc) {
		t.Fatalf("data document (%d bytes) is not larger than config (%d) — fixture needs more entity rows", len(dataDoc), len(configDoc))
	}

	restoreCeiling := maxSnapshotBytes
	t.Cleanup(func() { maxSnapshotBytes = restoreCeiling })
	maxSnapshotBytes = len(configDoc) + 1 // config still fits; data does not

	s := f.create(t, "проба между границами")
	if s.HasData {
		t.Fatal("HasData = true despite compressSnapshot refusing the entity document")
	}
	var dataSnap []byte
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT data_snap FROM checkpoints WHERE id = ?", s.ID).Scan(&dataSnap); err != nil {
		t.Fatalf("read the stored row: %v", err)
	}
	if dataSnap != nil {
		t.Fatal("data_snap is non-NULL despite compressSnapshot refusing")
	}
	var configSnap []byte
	if err := f.db.R.QueryRowContext(t.Context(),
		"SELECT config_snap FROM checkpoints WHERE id = ?", s.ID).Scan(&configSnap); err != nil {
		t.Fatalf("read the stored row: %v", err)
	}
	if configSnap == nil {
		t.Fatal("config_snap is NULL — Create failed entirely instead of degrading only the data half")
	}
}
