// repo_create_test.go: Create/Confirm's own storage properties (D13
// clauses 1, 2, 4, 15, 17, 18, 27, 28, 32) — it stores and survives a
// restart, a client-supplied id is overwritten (on "id" and on a non-"id"
// field), seq allocates under concurrency, and the row/byte-total caps hold
// on both the single-request and concurrent paths. Split out of the former
// repo_test.go (package resources, not resources_test — see
// helpers_test.go's own comment for why).
package resources

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testkit"
)

// TestNewRepo_MaxEntityRowsBoundary is D11's own boundary: config.Load's
// count() validator (internal/config/config.go) accepts MOCKER_MAX_ENTITIES
// values n >= 0, so an explicit zero is a legitimate operator choice
// ("confirm nothing, ever") and must reach the cap AS zero, not be silently
// coerced into the pre-P3h constant the way a genuinely never-set field
// (a hand-built *config.Config struct literal, or a caller that reached
// this constructor before config.MaxEntities existed) still is. Before this
// fix NewRepo's `maxEntityRows <= 0` fallback could not tell the two apart;
// the fallback now triggers only on a NEGATIVE value, which config.Load's
// validator never lets an operator produce.
func TestNewRepo_MaxEntityRowsBoundary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := testkit.NewDBAt(t, dir+"/mocker.db")
	sr := specs.NewRepo(db, testSpecConfig(t))

	if got := NewRepo(db, sr, 4<<20, 64<<10, 0).maxEntityRows; got != 0 {
		t.Fatalf("maxEntityRows for an explicit MOCKER_MAX_ENTITIES=0 = %d, want 0 (an operator's own zero must not be overridden)", got)
	}
	if got := NewRepo(db, sr, 4<<20, 64<<10, -1).maxEntityRows; got != defaultMaxEntityRows {
		t.Fatalf("maxEntityRows for a negative value (never produced by config.Load) = %d, want the fallback %d", got, defaultMaxEntityRows)
	}
	if got := NewRepo(db, sr, 4<<20, 64<<10, defaultMaxEntityRows).maxEntityRows; got != defaultMaxEntityRows {
		t.Fatalf("maxEntityRows for an explicit positive value = %d, want %d unchanged", got, defaultMaxEntityRows)
	}
}

func TestConfirm_StoresEntities(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := testkit.NewDBAt(t, dir+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 5})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	revBefore := workspaceRevision(t, db, wsID)
	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if got := entityCount(t, db, res.ID); got != 5 {
		t.Fatalf("entity count = %d, want 5 (seed_count)", got)
	}
	if got := workspaceRevision(t, db, wsID); got != revBefore+1 {
		t.Fatalf("revision after Confirm = %d, want %d (D4: the decision transitions bump revision)", got, revBefore+1)
	}

	// A POST makes it +1, and — D13 clause 23 — does NOT bump revision:
	// an entity write changes nothing the runtime cache keys on.
	revAfterConfirm := workspaceRevision(t, db, wsID)
	if _, err := repo.Create(t.Context(), res.ID, "", "", res.IDField, res.Wrapper.IDType, map[string]any{"name": "extra"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := entityCount(t, db, res.ID); got != 6 {
		t.Fatalf("entity count after Create = %d, want 6", got)
	}
	if got := workspaceRevision(t, db, wsID); got != revAfterConfirm {
		t.Fatalf("revision after Create = %d, want unchanged %d", got, revAfterConfirm)
	}

	// A DELETE makes it -1, and likewise does not bump revision.
	entities, err := repo.List(t.Context(), res.ID, "", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	deleted, err := repo.Delete(t.Context(), res.ID, "", "", entities[0].EntityKey)
	if err != nil || !deleted {
		t.Fatalf("Delete = %v, %v, want true, nil", deleted, err)
	}
	if got := entityCount(t, db, res.ID); got != 5 {
		t.Fatalf("entity count after Delete = %d, want 5", got)
	}
	if got := workspaceRevision(t, db, wsID); got != revAfterConfirm {
		t.Fatalf("revision after Delete = %d, want unchanged %d", got, revAfterConfirm)
	}

	// And the OTHER decision transition. Clause 23 says revision moves on
	// BOTH, and until this block only the Confirm half was held: the
	// runtime-invalidation test in internal/mockplane forces its cache to
	// miss by incrementing ws.Revision in test code rather than re-reading
	// the column, so it passes identically whether or not Decline bumps
	// anything, and no other test read revision after a Decline at all.
	// Delete Repo.Decline's own bumpRevisionTx call and this is the
	// assertion that goes red.
	revBeforeDecline := workspaceRevision(t, db, wsID)
	if err := repo.Decline(t.Context(), wsID, familyWidgets, "alpha"); err != nil {
		t.Fatalf("Decline with the correct slug: %v", err)
	}
	if got := workspaceRevision(t, db, wsID); got != revBeforeDecline+1 {
		t.Fatalf("revision after Decline = %d, want %d (D4: BOTH decision transitions bump revision)", got, revBeforeDecline+1)
	}
}

func TestConfirm_SurvivesRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := dir + "/mocker.db"

	var (
		wsID       int64
		resourceID int64
		created    Entity
	)
	func() {
		db, err := store.Open(t.Context(), dbPath)
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		if err := db.Migrate(t.Context(), nil); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		specID := importFixtureSpec(t, db)
		wsID = insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 7, ListSize: 3})
		repo := newTestRepo(t, db, 4<<20, 64<<10)
		res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
		if err != nil {
			t.Fatalf("Confirm: %v", err)
		}
		resourceID = res.ID
		created, err = repo.Create(t.Context(), res.ID, "", "", res.IDField, res.Wrapper.IDType, map[string]any{"name": "posted"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()

	// A second store.Open over the SAME file returns the same ids and
	// bodies, including the one Create wrote.
	db2, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	t.Cleanup(func() {
		if err := db2.Close(); err != nil {
			t.Errorf("close db2: %v", err)
		}
	})
	repo2 := newTestRepo(t, db2, 4<<20, 64<<10)
	entities, err := repo2.List(t.Context(), resourceID, "", "")
	if err != nil {
		t.Fatalf("List after restart: %v", err)
	}
	if len(entities) != 4 { // 3 populated + 1 created
		t.Fatalf("entity count after restart = %d, want 4", len(entities))
	}
	got, ok, err := repo2.Get(t.Context(), resourceID, "", "", created.EntityKey)
	if err != nil || !ok {
		t.Fatalf("Get after restart = %v, %v, want ok", ok, err)
	}
	if string(got.Data) != string(created.Data) {
		t.Fatalf("body after restart = %s, want %s", got.Data, created.Data)
	}
}

func TestCreate_OverwritesClientSuppliedID(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	resourceID := insertResourceRow(t, db, wsID, familyWidgets, "id", "integer")
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	e, err := repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"id": 999, "name": "spoofed"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if e.EntityKey == "999" {
		t.Fatalf("EntityKey = %q, the client-sent id must be overwritten by the allocated seq", e.EntityKey)
	}
	if _, ok, err := repo.Get(t.Context(), resourceID, "", "", "999"); err != nil || ok {
		t.Fatalf("Get(999) = ok=%v err=%v, want not found — the client's id must never be honoured", ok, err)
	}
	if _, ok, err := repo.Get(t.Context(), resourceID, "", "", e.EntityKey); err != nil || !ok {
		t.Fatalf("Get(%s) = ok=%v err=%v, want found under the ALLOCATED key", e.EntityKey, ok, err)
	}
}

func TestCreate_OverwritesNonIDField(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	resourceID := insertResourceRow(t, db, wsID, familyUsers, "userId", "")
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	e, err := repo.Create(t.Context(), resourceID, "", "", "userId", "", map[string]any{"userId": "zzz", "name": "spoofed"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var data map[string]any
	if err := jsonx.Unmarshal(e.Data, &data); err != nil {
		t.Fatalf("decode stored data: %v", err)
	}
	if data["userId"] == "zzz" {
		t.Fatalf("data[userId] = %v, want the allocated seq overwriting the client value", data["userId"])
	}
	if _, ok, err := repo.Get(t.Context(), resourceID, "", "", "zzz"); err != nil || ok {
		t.Fatalf("Get(zzz) = ok=%v err=%v, want not found", ok, err)
	}
}

func TestCreate_ConcurrentSeqAllocation(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	resourceID := insertResourceRow(t, db, wsID, familyWidgets, "id", "integer")
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	const n = 20
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		keys = make(map[string]bool, n)
		errs []error
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e, err := repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": fmt.Sprintf("w%d", i)})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			keys[e.EntityKey] = true
		}(i)
	}
	wg.Wait()

	if len(errs) != 0 {
		t.Fatalf("Create errors: %v", errs)
	}
	if len(keys) != n {
		t.Fatalf("distinct entity keys = %d, want %d (collision on seq allocation)", len(keys), n)
	}
	if got := entityCount(t, db, resourceID); got != n {
		t.Fatalf("entity row count = %d, want %d", got, n)
	}
}

func TestCreate_RowCapExactly1000(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	resourceID := insertResourceRow(t, db, wsID, familyWidgets, "id", "integer")
	repo := newTestRepo(t, db, 64<<20, 64<<10) // generous byte cap: this test is about the ROW cap only

	for i := 0; i < defaultMaxEntityRows; i++ {
		if _, err := repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": "w"}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	if got := entityCount(t, db, resourceID); got != defaultMaxEntityRows {
		t.Fatalf("entity count = %d, want %d (row 1000 must be accepted)", got, defaultMaxEntityRows)
	}

	// Row 1001 refuses with ErrEntityLimit and the count is unchanged
	// (clause 18: a refusal never leaves a row).
	_, err := repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": "w"})
	if !errors.Is(err, ErrEntityLimit) {
		t.Fatalf("Create row 1001 error = %v, want ErrEntityLimit", err)
	}
	if got := entityCount(t, db, resourceID); got != defaultMaxEntityRows {
		t.Fatalf("entity count after refused row 1001 = %d, want unchanged %d", got, defaultMaxEntityRows)
	}
}

func TestCreate_ByteTotalCap(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	resourceID := insertResourceRow(t, db, wsID, familyWidgets, "id", "integer")
	// A tiny maxResponseBytes makes the byte-total cap (MaxResponse/2) the
	// one that bites, well before the row cap could.
	repo := newTestRepo(t, db, 400, 64<<10)

	var lastErr error
	accepted := 0
	for i := 0; i < 50; i++ {
		_, err := repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": "widget-name-padding"})
		if err != nil {
			lastErr = err
			break
		}
		accepted++
	}
	if !errors.Is(lastErr, ErrEntityLimit) {
		t.Fatalf("final Create error = %v, want ErrEntityLimit (byte total cap)", lastErr)
	}
	if got := entityCount(t, db, resourceID); got != accepted {
		t.Fatalf("entity count = %d, want %d (a refusal must leave no row)", got, accepted)
	}
}

func TestCreate_CapsHoldUnderConcurrency(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	resourceID := insertResourceRow(t, db, wsID, familyWidgets, "id", "integer")

	// Pre-fill to one below the row cap, then fire N goroutines at the
	// boundary: the row count read INSIDE Create's own transaction (not the
	// reader pool) is what must keep the resource exactly at the cap.
	repo := newTestRepo(t, db, 64<<20, 64<<10)
	for i := 0; i < defaultMaxEntityRows-5; i++ {
		if _, err := repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": "w"}); err != nil {
			t.Fatalf("prefill Create %d: %v", i, err)
		}
	}

	const n = 20 // more goroutines than the 5 remaining slots
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": "w"})
		}()
	}
	wg.Wait()

	if got := entityCount(t, db, resourceID); got != defaultMaxEntityRows {
		t.Fatalf("entity count after concurrent burst = %d, want exactly %d", got, defaultMaxEntityRows)
	}
}

// TestCreate_CapsHoldUnderConcurrency_ByteTotal is D13 clause 32's OTHER
// half: TestCreate_CapsHoldUnderConcurrency above only ever fires goroutines
// at the ROW-count boundary (a generous byte cap, tiny bodies). This one
// keeps the row cap generous and drives the BYTE-total cap (entityByteCap,
// MaxResponse/2) to its boundary instead — an implementation that moved
// only that check onto the reader pool (a stale snapshot under concurrent
// writers) while leaving the row-count check on the writer tx would pass
// every other test in this file and still let two concurrent POSTs push
// the stored byte total past the cap, which is exactly what clause 33 says
// must not happen (a resource over that line bricks GET X).
func TestCreate_CapsHoldUnderConcurrency_ByteTotal(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	resourceID := insertResourceRow(t, db, wsID, familyWidgets, "id", "integer")

	// trafficMaxBody stays generous (floors to 64KiB) so only the
	// byte-TOTAL cap — MaxResponse/2 — can bite in this test.
	repo := newTestRepo(t, db, 4000, 64<<10)
	totalCap := repo.entityByteCap()

	pad := strings.Repeat("x", 200)
	// Measure one entity's ACTUAL stored size (id overwrite included)
	// rather than guessing at JSON-encoding overhead, then delete it again
	// so the prefill loop below starts from zero.
	probe, err := repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": pad})
	if err != nil {
		t.Fatalf("probe Create: %v", err)
	}
	oneSize := int64(len(probe.Data))
	if _, err := repo.Delete(t.Context(), resourceID, "", "", probe.EntityKey); err != nil {
		t.Fatalf("delete probe: %v", err)
	}

	// Prefill sequentially until only room for a handful more entities
	// remains, then fire far more goroutines than that at the boundary —
	// the write-tx-internal re-read of SUM(LENGTH(data)) (clause 32's own
	// text: "read inside Create's own transaction") is what must keep the
	// final total at or under the cap, since the single writer connection
	// serializes every one of these transactions regardless of how many
	// goroutines race to start one.
	room := oneSize * 3
	for {
		var total sql.NullInt64
		if err := db.R.QueryRowContext(t.Context(), "SELECT SUM(LENGTH(data)) FROM entities WHERE resource_id = ?", resourceID).Scan(&total); err != nil {
			t.Fatalf("sum bytes: %v", err)
		}
		if totalCap-total.Int64 <= room {
			break
		}
		if _, err := repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": pad}); err != nil {
			t.Fatalf("prefill Create: %v", err)
		}
	}

	const n = 20 // far more goroutines than the ~3 remaining slots
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": pad})
		}()
	}
	wg.Wait()

	var finalTotal sql.NullInt64
	if err := db.R.QueryRowContext(t.Context(), "SELECT SUM(LENGTH(data)) FROM entities WHERE resource_id = ?", resourceID).Scan(&finalTotal); err != nil {
		t.Fatalf("sum bytes: %v", err)
	}
	if finalTotal.Int64 > totalCap {
		t.Fatalf("stored byte total after concurrent burst = %d, want <= cap %d (a check on the reader pool — a pre-writer snapshot — would let this pass over)", finalTotal.Int64, totalCap)
	}
}

func TestCheckBatchCaps(t *testing.T) {
	t.Parallel()
	repo := &Repo{maxResponseBytes: 1000, trafficMaxBody: 0, maxEntityRows: defaultMaxEntityRows} // perEntityByteCap floors to 64KiB

	t.Run("row cap", func(t *testing.T) {
		t.Parallel()
		bodies := make([][]byte, defaultMaxEntityRows+1)
		for i := range bodies {
			bodies[i] = []byte(`{}`)
		}
		if err := repo.checkBatchCaps(bodies); !errors.Is(err, ErrEntityLimit) {
			t.Fatalf("checkBatchCaps() = %v, want ErrEntityLimit", err)
		}
	})

	t.Run("byte total cap", func(t *testing.T) {
		t.Parallel()
		big := make([]byte, 600)
		bodies := [][]byte{big, big} // 1200 > maxResponseBytes/2 (500)
		if err := repo.checkBatchCaps(bodies); !errors.Is(err, ErrEntityLimit) {
			t.Fatalf("checkBatchCaps() = %v, want ErrEntityLimit", err)
		}
	})

	t.Run("per-entity cap", func(t *testing.T) {
		t.Parallel()
		huge := make([]byte, minEntityBodyCap+1)
		if err := repo.checkBatchCaps([][]byte{huge}); !errors.Is(err, ErrEntityLimit) {
			t.Fatalf("checkBatchCaps() = %v, want ErrEntityLimit", err)
		}
	})

	t.Run("within every cap", func(t *testing.T) {
		t.Parallel()
		if err := repo.checkBatchCaps([][]byte{[]byte(`{"a":1}`)}); err != nil {
			t.Fatalf("checkBatchCaps() = %v, want nil", err)
		}
	})
}
