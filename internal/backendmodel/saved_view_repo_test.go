package backendmodel

import (
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"math"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testkit"
)

func savedHistoricalBytes(t *testing.T, r *Repo) map[string][]string {
	t.Helper()
	rows, err := r.db.R.QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'backend_%' AND name NOT LIKE 'backend_saved_view%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	_ = rows.Close()
	result := map[string][]string{}
	for _, name := range names {
		query := `SELECT * FROM "` + name + `"`
		if name == "backend_payload_blobs" {
			query += ` WHERE owner NOT LIKE 'backend_saved_view_versions/%'`
		} else if strings.HasPrefix(name, "backend_payload_") {
			query += ` WHERE owner != 'backend_saved_view_versions'`
		}
		if name == "backend_command_receipts" {
			query += ` WHERE scope NOT LIKE 'saved-view-%'`
		}
		query += ` ORDER BY rowid`
		result[name] = append(result[name], savedHistoricalRows(t, r, query)...)
	}
	return result
}

// savedHistoricalRows renders one query's rows as JSON arrays; it is its own
// function so the cursor closes by defer on every path.
func savedHistoricalRows(t *testing.T, r *Repo, query string) []string {
	t.Helper()
	rows, err := r.db.R.QueryContext(t.Context(), query)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		values := make([]any, len(cols))
		pointers := make([]any, len(cols))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(raw))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}
func assertSavedHistoricalBytes(t *testing.T, r *Repo, before map[string][]string) {
	t.Helper()
	after := savedHistoricalBytes(t, r)
	for name, rows := range before {
		if !slices.Equal(rows, after[name]) {
			t.Fatal("saved view rewrote historical bytes", name)
		}
	}
}

func TestSavedViewQuotaBoundariesReceiptFirstAndOverflow(t *testing.T) {
	for _, quota := range []string{"views", "versions", "bytes", "overflow"} {
		t.Run(quota, func(t *testing.T) {
			r, db := testRepo(t)
			source, ids := commitRelationalFixture(t, r, "sqlite", "v1")
			in := savedDatabaseInput(source, ids, "create")
			v, err := r.CreateSavedView(t.Context(), source.Project.ID, in)
			if err != nil {
				t.Fatal(err)
			}
			original := savedViewBytes(t, v)
			switch quota {
			case "views":
				err = db.Write(t.Context(), func(tx *sql.Tx) error {
					for i := range 998 {
						id := fmt.Sprintf("%08x-0000-4000-8000-%012x", i+1, i+1)
						if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_saved_views(id,project_id,version,name,kind,target_json,created_at,updated_at) SELECT ?,project_id,1,name,kind,target_json,created_at,updated_at FROM backend_saved_views WHERE id=?`, id, v.ID); err != nil {
							return err
						}
						if _, err := testkit.ExecBackendOwner(t.Context(), tx, `INSERT INTO backend_saved_view_versions(view_id,version,document) VALUES(?,1,?)`, id, original); err != nil {
							return err
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				in.IdempotencyKey = "thousandth"
				if _, err := r.CreateSavedView(t.Context(), source.Project.ID, in); err != nil {
					t.Fatal("1000th view refused", err)
				}
				in.IdempotencyKey = "over-views"
				_, err = r.CreateSavedView(t.Context(), source.Project.ID, in)
				assertFault(t, err, "backend_saved_view_quota")
			case "versions":
				err = db.Write(t.Context(), func(tx *sql.Tx) error {
					for i := 2; i <= 999; i++ {
						doc := *v
						doc.receiptJSON = ""
						doc.Version = int64(i)
						raw, err := json.Marshal(doc)
						if err != nil {
							return err
						}
						if _, err := testkit.ExecBackendOwner(t.Context(), tx, `INSERT INTO backend_saved_view_versions(view_id,version,document) VALUES(?,?,?)`, v.ID, i, string(raw)); err != nil {
							return err
						}
					}
					_, err := tx.ExecContext(t.Context(), `UPDATE backend_saved_views SET version=999 WHERE id=?`, v.ID)
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				save := SaveSavedViewInput{Name: "1000", State: in.State, ExpectedVersion: 999, IdempotencyKey: "thousandth"}
				v1000, err := r.SaveSavedView(t.Context(), source.Project.ID, v.ID, save)
				if err != nil || v1000.Version != 1000 {
					t.Fatal("1000th version refused", err)
				}
				// A real change: an identical save answers v1000 without a new
				// version since review 2026-10-06, F185.
				save.ExpectedVersion = 1000
				save.Name = "1001"
				save.IdempotencyKey = "over-versions"
				_, err = r.SaveSavedView(t.Context(), source.Project.ID, v.ID, save)
				assertFault(t, err, "backend_saved_view_quota")
			case "bytes":
				var used int64
				if err := db.R.QueryRowContext(t.Context(), `SELECT sum(length(CAST(document AS BLOB))) FROM backend_saved_view_versions_documents`).Scan(&used); err != nil {
					t.Fatal(err)
				}
				used *= 2
				if _, err := db.W.ExecContext(t.Context(), `INSERT INTO backend_command_receipts(scope,key,request_hash,response) VALUES(?,?,?,CAST(zeroblob(?) AS TEXT))`, "saved-view-save:"+source.Project.ID+":"+v.ID, "fill", "filler", int64(MaxSavedViewBytes)-used); err != nil {
					t.Fatal(err)
				}
				in.IdempotencyKey = "over-bytes"
				_, err = r.CreateSavedView(t.Context(), source.Project.ID, in)
				assertFault(t, err, "backend_saved_view_quota")
				_, err = r.SaveSavedView(t.Context(), source.Project.ID, v.ID, SaveSavedViewInput{Name: "Over", State: in.State, ExpectedVersion: 1, IdempotencyKey: "over-bytes"})
				assertFault(t, err, "backend_saved_view_quota")
			case "overflow":
				doc := *v
				doc.receiptJSON = ""
				doc.Version = math.MaxInt64
				raw, err := json.Marshal(doc)
				if err != nil {
					t.Fatal(err)
				}
				err = db.Write(t.Context(), func(tx *sql.Tx) error {
					if _, err := testkit.ExecBackendOwner(t.Context(), tx, `INSERT INTO backend_saved_view_versions(view_id,version,document) VALUES(?,?,?)`, v.ID, math.MaxInt64, string(raw)); err != nil {
						return err
					}
					_, err := tx.ExecContext(t.Context(), `UPDATE backend_saved_views SET version=? WHERE id=?`, math.MaxInt64, v.ID)
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				_, err = r.SaveSavedView(t.Context(), source.Project.ID, v.ID, SaveSavedViewInput{Name: "Overflow", State: in.State, ExpectedVersion: math.MaxInt64, IdempotencyKey: "overflow"})
				assertFault(t, err, "backend_version_exhausted")
			}
			in.IdempotencyKey = "create"
			replay, err := r.CreateSavedView(t.Context(), source.Project.ID, in)
			if err != nil || savedViewBytes(t, replay) != original {
				t.Fatal("quota/overflow blocked replay", err)
			}
		})
	}
}

func openSavedViewDB(t *testing.T, path string) *store.DB {
	t.Helper()
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func savedDatabaseInput(out *ImportCommitResult, ids map[string]string, key string) CreateSavedViewInput {
	return CreateSavedViewInput{Name: "Orders", Target: BackendReadTarget{RevisionID: out.Revision.ID}, State: SavedViewState{Database: &SavedDatabaseViewState{Kind: "database", Scope: SavedDatabaseViewScope{DatastoreID: ids["database:orders"], FacetKey: "sql"}, Filters: SavedDatabaseViewFilters{Search: " orders ", RelationshipTableID: ids["table:orders"]}, Selection: &SavedViewSelection{RecordType: "node", ID: ids["column:orders:user_id"]}, Positions: []SavedViewPosition{{NodeID: ids["table:orders"], X: 12.25, Y: -88}}, CollapsedGroupIDs: []string{}}}, IdempotencyKey: key}
}

func TestSavedViewNormalizationPagingAndUnsupportedTargets(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	source, ids := commitRelationalFixture(t, r, "postgresql", "v1")
	in := savedDatabaseInput(source, ids, "normal")
	in.Name = "  Orders  "
	in.State.Database.Positions = []SavedViewPosition{{NodeID: ids["table:users"], X: 10, Y: 20}, {NodeID: ids["table:orders"], X: 30, Y: 40}}
	in.State.Database.CollapsedGroupIDs = []string{ids["schema:public"]}
	before := slices.Clone(in.State.Database.Positions)
	first, err := r.CreateSavedView(t.Context(), source.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.Name != "Orders" || !slices.Equal(before, in.State.Database.Positions) {
		t.Fatal("normalization mutated caller")
	}
	in.Name = "Orders"
	slices.Reverse(in.State.Database.Positions)
	replay, err := r.CreateSavedView(t.Context(), source.Project.ID, in)
	if err != nil || savedViewBytes(t, replay) != savedViewBytes(t, first) {
		t.Fatal("position-order replay failed", err)
	}
	in.IdempotencyKey = "second"
	if _, err := r.CreateSavedView(t.Context(), source.Project.ID, in); err != nil {
		t.Fatal(err)
	}
	page, err := r.ListSavedViews(t.Context(), source.Project.ID, SavedViewListInput{Kind: "database", Limit: 1})
	if err != nil || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal(page, err)
	}
	next, err := r.ListSavedViews(t.Context(), source.Project.ID, SavedViewListInput{Kind: "database", Limit: 1, Cursor: page.NextCursor})
	if err != nil || len(next.Items) != 1 || next.Items[0].ID == page.Items[0].ID || next.NextCursor != "" {
		t.Fatal(next, err)
	}
	for _, query := range []SavedViewListInput{{Kind: "flow", Limit: 1, Cursor: page.NextCursor}, {Kind: "database", Limit: 2, Cursor: page.NextCursor}, {Limit: 101}, {Limit: -1}, {Kind: "overview"}, {Cursor: "invalid"}} {
		_, err := r.ListSavedViews(t.Context(), source.Project.ID, query)
		assertFault(t, err, "backend_invalid")
	}
	other := createProject(t, r, "other")
	_, err = r.ListSavedViews(t.Context(), other.ID, SavedViewListInput{Kind: "database", Limit: 1, Cursor: page.NextCursor})
	assertFault(t, err, "backend_invalid")
	_, err = r.GetSavedView(t.Context(), other.ID, first.ID, GetSavedViewInput{})
	assertFault(t, err, "backend_not_found")
	unsupported := in
	unsupported.Target = BackendReadTarget{RevisionID: other.CurrentRevisionID}
	unsupported.IdempotencyKey = "schema1"
	_, err = r.CreateSavedView(t.Context(), other.ID, unsupported)
	assertFault(t, err, "backend_unsupported_scope")
	flow := SavedViewState{Flow: &SavedFlowViewState{Kind: "flow", Positions: []SavedViewPosition{}, CollapsedGroupIDs: []string{}}}
	_, err = r.SaveSavedView(t.Context(), source.Project.ID, first.ID, SaveSavedViewInput{Name: "Flow", State: flow, ExpectedVersion: 1, IdempotencyKey: "wrong-kind"})
	assertFault(t, err, "backend_invalid")
}
func savedViewBytes(t *testing.T, v *SavedView) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSavedViewReplayRejectsNilRequiredArrays(t *testing.T) {
	r, source, ids := savedRuntimeFixture(t)
	for _, kind := range []string{"flow", "database"} {
		t.Run(kind, func(t *testing.T) {
			state := SavedViewState{Flow: &SavedFlowViewState{Kind: "flow", Positions: []SavedViewPosition{}, CollapsedGroupIDs: []string{}}}
			if kind == "database" {
				state = SavedViewState{Database: &SavedDatabaseViewState{Kind: "database", Scope: SavedDatabaseViewScope{DatastoreID: ids["database:orders"], FacetKey: "sql"}, Positions: []SavedViewPosition{}, CollapsedGroupIDs: []string{}}}
			}
			create := CreateSavedViewInput{Name: "Required arrays", Target: BackendReadTarget{RevisionID: source.Revision.ID}, State: state, IdempotencyKey: kind + "-create"}
			view, err := r.CreateSavedView(t.Context(), source.Project.ID, create)
			if err != nil {
				t.Fatal(err)
			}
			save := SaveSavedViewInput{Name: "Saved arrays", State: state, ExpectedVersion: 1, IdempotencyKey: kind + "-save"}
			saved, err := r.SaveSavedView(t.Context(), source.Project.ID, view.ID, save)
			if err != nil {
				t.Fatal(err)
			}
			for _, operation := range []string{"create", "save"} {
				for _, field := range []string{"positions", "collapsedGroupIds"} {
					t.Run(operation+"/"+field, func(t *testing.T) {
						malformed := normalizeSavedState(state)
						if malformed.Flow != nil {
							if field == "positions" {
								malformed.Flow.Positions = nil
							} else {
								malformed.Flow.CollapsedGroupIDs = nil
							}
						} else {
							if field == "positions" {
								malformed.Database.Positions = nil
							} else {
								malformed.Database.CollapsedGroupIDs = nil
							}
						}
						if operation == "create" {
							bad := create
							bad.State = malformed
							_, err = r.CreateSavedView(t.Context(), source.Project.ID, bad)
						} else {
							bad := save
							bad.State = malformed
							_, err = r.SaveSavedView(t.Context(), source.Project.ID, view.ID, bad)
						}
						fault := assertFault(t, err, "backend_invalid")
						if fault.Details["field"] != "state."+field {
							t.Fatal("wrong validation path", fault)
						}
					})
				}
			}
			replay, err := r.CreateSavedView(t.Context(), source.Project.ID, create)
			if err != nil || savedViewBytes(t, replay) != savedViewBytes(t, view) {
				t.Fatal("valid create replay changed", err)
			}
			replay, err = r.SaveSavedView(t.Context(), source.Project.ID, view.ID, save)
			if err != nil || savedViewBytes(t, replay) != savedViewBytes(t, saved) {
				t.Fatal("valid save replay changed", err)
			}
		})
	}
}

func TestSavedViewHistoryCASReplayAndPurity(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "sqlite", "v1")
	before := savedHistoricalBytes(t, r)
	in := savedDatabaseInput(out, ids, "create")
	first, err := r.CreateSavedView(t.Context(), out.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	raw := savedViewBytes(t, first)
	if first.Version != 1 || first.Name != "Orders" || first.Pins.RevisionID != out.Revision.ID || first.Pins.SemanticHash != out.Revision.SemanticHash || first.Pins.Proposal != nil {
		t.Fatal(first)
	}
	save := SaveSavedViewInput{Name: "Second", State: in.State, ExpectedVersion: 1, IdempotencyKey: "save"}
	second, err := r.SaveSavedView(t.Context(), out.Project.ID, first.ID, save)
	if err != nil {
		t.Fatal(err)
	}
	secondRaw := savedViewBytes(t, second)
	_, err = r.SaveSavedView(t.Context(), out.Project.ID, first.ID, SaveSavedViewInput{Name: "Third", State: in.State, ExpectedVersion: 2, IdempotencyKey: "third"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := r.GetSavedView(t.Context(), out.Project.ID, first.ID, GetSavedViewInput{Version: 1})
	if err != nil || savedViewBytes(t, old) != raw {
		t.Fatalf("old changed %v", err)
	}
	replay, err := r.SaveSavedView(t.Context(), out.Project.ID, first.ID, save)
	if err != nil || savedViewBytes(t, replay) != secondRaw {
		t.Fatalf("receipt lost to CAS %v", err)
	}
	replay, err = r.CreateSavedView(t.Context(), out.Project.ID, in)
	if err != nil || savedViewBytes(t, replay) != raw {
		t.Fatalf("create replay %v", err)
	}
	save.Name = "Changed"
	_, err = r.SaveSavedView(t.Context(), out.Project.ID, first.ID, save)
	assertFault(t, err, "backend_idempotency_conflict")
	save.IdempotencyKey = "stale"
	_, err = r.SaveSavedView(t.Context(), out.Project.ID, first.ID, save)
	f := assertFault(t, err, "backend_version_conflict")
	if f.CurrentVersion != 3 {
		t.Fatal(f)
	}
	assertSavedHistoricalBytes(t, r, before)
	for _, q := range []string{`UPDATE backend_saved_view_versions SET payload_key=payload_key WHERE view_id=?`, `DELETE FROM backend_saved_view_versions WHERE view_id=?`, `UPDATE backend_saved_views SET kind='flow' WHERE id=?`, `UPDATE backend_saved_views SET target_json='{}' WHERE id=?`, `UPDATE backend_saved_views SET version=999 WHERE id=?`} {
		if _, err := db.W.ExecContext(t.Context(), q, first.ID); err == nil {
			t.Fatal("accepted immutable/invalid write", q)
		}
	}
	page, err := r.ListSavedViews(t.Context(), out.Project.ID, SavedViewListInput{Kind: "database", Limit: 1})
	if err != nil || len(page.Items) != 1 || page.Items[0].Version != 3 {
		t.Fatal(page, err)
	}
	advanced, _ := databaseCommitV2(t, r, out, ids, "sqlite")
	if advanced.Revision.ID == out.Revision.ID {
		t.Fatal("source fixture did not advance")
	}
	save.Name = "Second"
	save.IdempotencyKey = "save"
	replay, err = r.SaveSavedView(t.Context(), out.Project.ID, first.ID, save)
	if err != nil || savedViewBytes(t, replay) != secondRaw {
		t.Fatal("source advance changed save receipt", err)
	}
	before = savedHistoricalBytes(t, r)
	path := db.Path()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openSavedViewDB(t, path)
	r = NewRepo(reopened)
	assertSavedHistoricalBytes(t, r, before)
	old, err = r.GetSavedView(t.Context(), out.Project.ID, first.ID, GetSavedViewInput{Version: 1})
	if err != nil || savedViewBytes(t, old) != raw {
		t.Fatal("restart old version", err)
	}
	replay, err = r.CreateSavedView(t.Context(), out.Project.ID, in)
	if err != nil || savedViewBytes(t, replay) != raw {
		t.Fatal("restart replay", err)
	}
}

func TestSavedViewConcurrentCASAndRollback(t *testing.T) {
	r, db := testRepo(t)
	out, ids := commitRelationalFixture(t, r, "postgresql", "v1")
	in := savedDatabaseInput(out, ids, "create")
	v, err := r.CreateSavedView(t.Context(), out.Project.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := range 2 {
		wg.Go(func() {
			_, err := r.SaveSavedView(t.Context(), out.Project.ID, v.ID, SaveSavedViewInput{Name: "Concurrent", State: in.State, ExpectedVersion: 1, IdempotencyKey: fmt.Sprint(i)})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	wins := 0
	for err := range results {
		if err == nil {
			wins++
		} else {
			assertFault(t, err, "backend_version_conflict")
		}
	}
	if wins != 1 {
		t.Fatal("CAS winners", wins)
	}
	if _, err := db.W.ExecContext(t.Context(), `CREATE TRIGGER fail_saved BEFORE INSERT ON backend_command_receipts WHEN NEW.scope LIKE 'saved-view-%' BEGIN SELECT RAISE(ABORT,'test'); END`); err != nil {
		t.Fatal(err)
	}
	_, err = r.SaveSavedView(t.Context(), out.Project.ID, v.ID, SaveSavedViewInput{Name: "Rollback", State: in.State, ExpectedVersion: 2, IdempotencyKey: "rollback"})
	if err == nil {
		t.Fatal("ignored receipt insert failure")
	}
	latest, err := r.GetSavedView(t.Context(), out.Project.ID, v.ID, GetSavedViewInput{})
	if err != nil || latest.Version != 2 {
		t.Fatal("partial version survived", latest, err)
	}
}
