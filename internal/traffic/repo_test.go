package traffic_test

import (
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/traffic"
)

// TestRepo_List_newestFirst covers List's documented order.
func TestRepo_List_newestFirst(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	rec := traffic.NewRecorder(db, nil, traffic.Options{})
	repo := traffic.NewRepo(db)

	for _, p := range []string{"/a", "/b", "/c"} {
		rec.Record(testEvent(wsID, p))
	}
	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush(): %v", err)
	}

	rows, err := repo.List(t.Context(), wsID, 10)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("List() = %d rows, want 3", len(rows))
	}
	want := []string{"/c", "/b", "/a"}
	for i, w := range want {
		if rows[i].Path != w {
			t.Errorf("rows[%d].Path = %q, want %q", i, rows[i].Path, w)
		}
	}
}

// TestRepo_List_limitHonoured proves List never returns more than limit.
func TestRepo_List_limitHonoured(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	rec := traffic.NewRecorder(db, nil, traffic.Options{})
	repo := traffic.NewRepo(db)
	for i := range 10 {
		rec.Record(testEvent(wsID, "/x/"+strconv.Itoa(i)))
	}
	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush(): %v", err)
	}

	rows, err := repo.List(t.Context(), wsID, 3)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("List(limit=3) = %d rows, want 3", len(rows))
	}
}

// TestRepo_Since_strictlyGreaterOldestFirstLimitHonoured covers all three of
// Since's documented properties in one pass: ids strictly greater than the
// cursor, oldest first, limit honoured.
func TestRepo_Since_strictlyGreaterOldestFirstLimitHonoured(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	rec := traffic.NewRecorder(db, nil, traffic.Options{})
	repo := traffic.NewRepo(db)
	for _, p := range []string{"/a", "/b", "/c", "/d", "/e"} {
		rec.Record(testEvent(wsID, p))
	}
	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush(): %v", err)
	}

	all, err := repo.List(t.Context(), wsID, 10)
	if err != nil {
		t.Fatalf("List(): %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("seed List() = %d rows, want 5", len(all))
	}
	// all is newest-first (/e, /d, /c, /b, /a); cursor on the 3rd-oldest ("/c").
	cursorID := all[2].ID // "/c"

	got, err := repo.Since(t.Context(), wsID, cursorID, 10)
	if err != nil {
		t.Fatalf("Since(): %v", err)
	}
	// Strictly greater than "/c"'s id, oldest first: "/d" then "/e".
	if len(got) != 2 {
		t.Fatalf("Since() = %d rows, want 2", len(got))
	}
	if got[0].Path != "/d" || got[1].Path != "/e" {
		t.Errorf("Since() paths = [%q, %q], want [/d, /e] (oldest first)", got[0].Path, got[1].Path)
	}
	for _, r := range got {
		if r.ID <= cursorID {
			t.Errorf("Since() returned id %d, want strictly > cursor %d", r.ID, cursorID)
		}
	}

	limited, err := repo.Since(t.Context(), wsID, cursorID, 1)
	if err != nil {
		t.Fatalf("Since(limit=1): %v", err)
	}
	if len(limited) != 1 || limited[0].Path != "/d" {
		t.Fatalf("Since(limit=1) = %+v, want just [/d]", limited)
	}
}

// TestRepo_Get_missing proves Get reports ErrNotFound rather than a bare
// sql.ErrNoRows or a panic.
func TestRepo_Get_missing(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	repo := traffic.NewRepo(db)

	_, err := repo.Get(t.Context(), wsID, 999999)
	if !errors.Is(err, traffic.ErrNotFound) {
		t.Fatalf("Get() error = %v, want ErrNotFound", err)
	}
}

// TestRepo_Get_found round-trips one row's fields.
func TestRepo_Get_found(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	rec := traffic.NewRecorder(db, nil, traffic.Options{})
	repo := traffic.NewRepo(db)

	rec.Record(testEvent(wsID, "/one"))
	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush(): %v", err)
	}
	rows, err := repo.List(t.Context(), wsID, 1)
	if err != nil || len(rows) != 1 {
		t.Fatalf("List(): rows=%v err=%v", rows, err)
	}

	got, err := repo.Get(t.Context(), wsID, rows[0].ID)
	if err != nil {
		t.Fatalf("Get(): %v", err)
	}
	if got.Path != "/one" || got.Method != http.MethodGet || got.Status != 200 {
		t.Errorf("Get() = %+v, want Path=/one Method=GET Status=200", got)
	}
}

// TestRepo_Clear_touchesOnlyItsWorkspace proves Clear returns the deleted
// count and never removes another workspace's rows.
func TestRepo_Clear_touchesOnlyItsWorkspace(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsA := insertWorkspace(t, db, "workspace-a")
	wsB := insertWorkspace(t, db, "workspace-b")
	rec := traffic.NewRecorder(db, nil, traffic.Options{})
	repo := traffic.NewRepo(db)

	for _, p := range []string{"/a1", "/a2", "/a3"} {
		rec.Record(testEvent(wsA, p))
	}
	rec.Record(testEvent(wsB, "/b1"))
	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush(): %v", err)
	}

	n, err := repo.Clear(t.Context(), wsA)
	if err != nil {
		t.Fatalf("Clear(): %v", err)
	}
	if n != 3 {
		t.Errorf("Clear() = %d, want 3", n)
	}

	gotA, err := repo.List(t.Context(), wsA, 10)
	if err != nil {
		t.Fatalf("List(a): %v", err)
	}
	if len(gotA) != 0 {
		t.Errorf("List(a) after Clear = %d rows, want 0", len(gotA))
	}

	gotB, err := repo.List(t.Context(), wsB, 10)
	if err != nil {
		t.Fatalf("List(b): %v", err)
	}
	if len(gotB) != 1 {
		t.Errorf("List(b) after Clear(a) = %d rows, want 1 (untouched)", len(gotB))
	}
}

// TestRepo_Rate1m_injectedNow proves the count is scoped to the minute
// ending at the caller-supplied now, not wall-clock time — using an injected
// now is what keeps this test from being flaky under load.
func TestRepo_Rate1m_injectedNow(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	rec := traffic.NewRecorder(db, nil, traffic.Options{})
	repo := traffic.NewRepo(db)

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	recent := testEvent(wsID, "/recent-a")
	recent.TS = now.Add(-30 * time.Second)
	rec.Record(recent)

	recent2 := testEvent(wsID, "/recent-b")
	recent2.TS = now.Add(-59 * time.Second)
	rec.Record(recent2)

	old := testEvent(wsID, "/old")
	old.TS = now.Add(-90 * time.Second)
	rec.Record(old)

	if err := rec.Flush(t.Context()); err != nil {
		t.Fatalf("Flush(): %v", err)
	}

	got, err := repo.Rate1m(t.Context(), wsID, now)
	if err != nil {
		t.Fatalf("Rate1m(): %v", err)
	}
	if got != 2 {
		t.Errorf("Rate1m() = %d, want 2 (only the two events within the trailing minute)", got)
	}
}

// TestRepo_scan_malformedReqHeadersIsAnErrorNotAPanic covers the decode
// path directly, mirroring internal/overrides/repo_test.go's guard for its
// own frozen table: a row that reached the table some other way (a hand
// INSERT, standing in for data no valid Recorder path could have produced)
// must surface as an error, never a panic, three calls up from here in the
// admin API.
func TestRepo_scan_malformedReqHeadersIsAnErrorNotAPanic(t *testing.T) {
	t.Parallel()
	db := newTestDB(t)
	wsID := insertWorkspace(t, db, "alex")
	repo := traffic.NewRepo(db)

	res, err := db.W.ExecContext(t.Context(), `
		INSERT INTO traffic (workspace_id, ts, method, path, matched_kind, status, duration_ms, req_headers, truncated)
		VALUES (?, unixepoch(), 'GET', '/hand-inserted', 'none', 200, 1.5, 'not json', 0)`,
		wsID,
	)
	if err != nil {
		t.Fatalf("hand-insert malformed row: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("id: %v", err)
	}

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Get() panicked on malformed req_headers: %v", r)
			}
		}()
		if _, err := repo.Get(t.Context(), wsID, id); err == nil {
			t.Fatal("Get() with malformed req_headers returned no error")
		}
	}()

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("List() panicked on malformed req_headers: %v", r)
			}
		}()
		if _, err := repo.List(t.Context(), wsID, 10); err == nil {
			t.Fatal("List() with malformed req_headers returned no error")
		}
	}()
}
