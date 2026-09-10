package checkpoints

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/workspaces"
)

func TestStringEntityKeysSurviveTransfers(t *testing.T) {
	f := newFixture(t, 10)
	id := f.insertResource(t, resourceFixture{family: "/widgets", name: "widgets", idField: "id", wrapper: strings.Replace(wrapperJSON, `"idType":"integer"`, `"idType":"string"`, 1), seedCount: 1})
	f.writeDecision(t, "/widgets", "confirmed")
	r := resources.NewRepo(f.db, nil, 4<<20, 64<<10, 1000)
	keys := []string{"alice", "550e8400-e29b-41d4-a716-446655440000", "10", "007", "92233720368547758070", "1000000000000000000"}
	for _, key := range keys {
		if _, _, err := r.Set(t.Context(), id, "", "", key, "id", "string", map[string]any{"name": key}); err != nil {
			t.Fatal(err)
		}
	}
	// Simulate a configuration snapshot whose counter predates the data.
	if _, err := f.db.W.ExecContext(t.Context(), "UPDATE resources SET seq = 0 WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	saved := f.create(t, "string keys")
	if _, _, err := r.Set(t.Context(), id, "", "", "alice", "id", "string", map[string]any{"name": "changed"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.repo.Rollback(t.Context(), f.wsID, saved.ID, f.userID, true, "ws-a"); err != nil {
		t.Fatal(err)
	}
	doc, err := f.repo.Export(t.Context(), f.wsID, true, false)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := bundle.EncodeExport(doc)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := bundle.DecodeExport(encoded)
	if err != nil {
		t.Fatal(err)
	}
	core, err := f.repo.readWorkspaceCore(t.Context(), f.wsID)
	if err != nil {
		t.Fatal(err)
	}
	imported, err := f.repo.Import(t.Context(), ImportInput{Name: "import", SpecID: core.specID, Document: decoded, CreatedBy: f.userID, Label: "import"})
	if err != nil {
		t.Fatal(err)
	}
	fork, err := f.repo.Fork(t.Context(), ForkInput{SourceID: f.wsID, Name: "copy", IncludeData: true, CreatedBy: f.userID, Label: "copy"})
	if err != nil {
		t.Fatal(err)
	}
	for _, wsID := range []int64{f.wsID, imported.Workspace.ID, fork.ID} {
		var rid int64
		if err := f.db.R.QueryRowContext(t.Context(), "SELECT id FROM resources WHERE workspace_id = ? AND route_family = '/widgets'", wsID).Scan(&rid); err != nil {
			t.Fatal(err)
		}
		for _, key := range keys {
			row, found, err := r.Get(t.Context(), rid, "", "", key)
			if err != nil || !found || !strings.Contains(string(row.Data), `"name":"`+key+`"`) {
				t.Fatalf("workspace %d key %q: row=%s found=%v err=%v", wsID, key, row.Data, found, err)
			}
		}
		row, err := r.Create(t.Context(), rid, "", "", "id", "string", map[string]any{})
		if err != nil || row.EntityKey != "1000000000000000001" {
			t.Fatalf("workspace %d next key=%q err=%v, want 1000000000000000001", wsID, row.EntityKey, err)
		}
	}
}

func TestEntityWritesCountUTF8Bytes(t *testing.T) {
	f := newFixture(t, 10)
	id := f.insertResource(t, resourceFixture{family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seedCount: 1})
	const byteCap = 1000
	r := resources.NewRepo(f.db, nil, byteCap*2, 64<<10, 1000)
	accepted := 0
	for range 100 {
		_, err := r.Create(t.Context(), id, "", "", "id", "integer", map[string]any{"payload": strings.Repeat("😀", 100)})
		if errors.Is(err, resources.ErrEntityLimit) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		accepted++
	}
	var byteSize, charSize int64
	if err := f.db.R.QueryRowContext(t.Context(), "SELECT SUM(LENGTH(CAST(data AS BLOB))), SUM(LENGTH(data)) FROM entities WHERE resource_id = ?", id).Scan(&byteSize, &charSize); err != nil {
		t.Fatal(err)
	}
	t.Logf("accepted=%d bytes=%d codepoints=%d cap=%d", accepted, byteSize, charSize, byteCap)
	if byteSize > byteCap {
		t.Errorf("stored entity bytes exceed configured cap: %d > %d", byteSize, byteCap)
	}
}

func TestRetiredResourceIDCannotReachReplacementWorkspace(t *testing.T) {
	f := newFixture(t, 10)
	oldID := f.insertResource(t, resourceFixture{family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON, seedCount: 1})
	foreignWS := insertWorkspace(t, f.db, "foreign", nil, domain.DefaultSettings())
	r := resources.NewRepo(f.db, nil, 4<<20, 64<<10, 1000)
	// An already started request keeps oldID in its compiled runtime.
	if err := workspaces.NewRepo(f.db).Delete(t.Context(), f.wsID); err != nil {
		t.Fatal(err)
	}
	other := &fixture{db: f.db, wsID: foreignWS}
	newID := other.insertResource(t, resourceFixture{family: "/secrets", name: "secrets", idField: "id", wrapper: wrapperJSON, seedCount: 1})
	if newID <= oldID {
		t.Errorf("replacement id=%d must exceed retired id=%d", newID, oldID)
	}
	if _, _, err := r.Set(t.Context(), newID, "", "", "1", "id", "integer", map[string]any{"marker": "foreign workspace data"}); err != nil {
		t.Fatal(err)
	}
	got, found, err := r.Get(t.Context(), oldID, "", "", "1")
	if !errors.Is(err, resources.ErrResourceGone) {
		t.Errorf("stale Get: %v, want ErrResourceGone", err)
	}
	if found {
		t.Errorf("old workspace resource handle reads workspace %d row: %s", foreignWS, got.Data)
	}
	deleted, err := r.Delete(t.Context(), oldID, "", "", "1")
	if !errors.Is(err, resources.ErrResourceGone) {
		t.Errorf("stale Delete: %v, want ErrResourceGone", err)
	}
	if deleted {
		t.Errorf("old workspace resource handle deleted workspace %d row", foreignWS)
	}
	if _, _, err := r.Set(t.Context(), oldID, "", "", "1", "id", "integer", map[string]any{}); !errors.Is(err, resources.ErrResourceGone) {
		t.Errorf("stale Set: %v", err)
	}
	if _, err := r.Create(t.Context(), oldID, "", "", "id", "integer", map[string]any{}); !errors.Is(err, resources.ErrResourceGone) {
		t.Errorf("stale Create: %v", err)
	}
	if _, _, err := r.Patch(t.Context(), oldID, "", "", "1", "id", "integer", map[string]any{}); !errors.Is(err, resources.ErrResourceGone) {
		t.Errorf("stale Patch: %v", err)
	}
}

func TestExportReadsOneConfigurationSnapshot(t *testing.T) {
	f := newFixture(t, 10)
	rid := f.insertResource(t, resourceFixture{family: "/widgets", name: "v0", idField: "id", wrapper: wrapperJSON})
	f.writeDecision(t, "/widgets", "confirmed")
	f.pinStatus(t, 200)
	f.createEndpoint(t, f.wsID, "GET", "/extra")
	r := resources.NewRepo(f.db, nil, 4<<20, 64<<10, 1000)
	if _, _, err := r.Set(t.Context(), rid, "", "", "1", "id", "integer", map[string]any{"name": "v0"}); err != nil {
		t.Fatal(err)
	}
	specIDs := [2]int64{}
	for i := range specIDs {
		marker := fmt.Sprintf("v%d", i)
		specIDs[i] = insertSpec(t, f.db, marker, "hash-"+marker)
		if _, err := f.db.W.ExecContext(t.Context(), "UPDATE specs SET raw = ? WHERE id = ?", `{"name":"`+marker+`"}`, specIDs[i]); err != nil {
			t.Fatal(err)
		}
	}
	update := func(ctx context.Context, i int) error {
		marker := fmt.Sprintf("v%d", i)
		return f.db.Write(ctx, func(tx *sql.Tx) error {
			for _, stmt := range []struct {
				query string
				args  []any
			}{
				{"UPDATE workspaces SET name = ?, spec_id = ?, revision = revision + 1 WHERE id = ?", []any{marker, specIDs[i], f.wsID}},
				{"UPDATE resources SET name = ? WHERE id = ?", []any{marker, rid}},
				{"UPDATE op_overrides SET active_status = ? WHERE workspace_id = ?", []any{200 + i, f.wsID}},
				{"UPDATE custom_endpoints SET active_status = ? WHERE workspace_id = ?", []any{200 + i, f.wsID}},
				{"UPDATE entities SET data = ? WHERE resource_id = ?", []any{`{"id":1,"name":"` + marker + `"}`, rid}},
			} {
				if _, err := tx.ExecContext(ctx, stmt.query, stmt.args...); err != nil {
					return err
				}
			}
			return nil
		})
	}
	if err := update(t.Context(), 0); err != nil {
		t.Fatal(err)
	}
	// Open the reader before saturating the writer: connection setup applies
	// schema pragmas and is not the concurrent read-snapshot behavior under test.
	if _, err := f.repo.Export(t.Context(), f.wsID, true, true); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		for i := 1; ; i++ {
			if err := update(ctx, i%2); err != nil {
				done <- err
				return
			}
		}
	}()
	defer func() {
		cancel()
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Errorf("concurrent writer: %v", err)
		}
	}()
	for i := range 500 {
		withData := i%2 == 0
		doc, err := f.repo.Export(t.Context(), f.wsID, withData, true)
		if err != nil {
			t.Fatal(err)
		}
		marker := doc.Workspace.Name
		status := 200
		if marker == "v1" {
			status = 201
		}
		if doc.Resources[0].Name != marker || doc.Spec.Name != marker || doc.Spec.Hash != "hash-"+marker {
			t.Fatalf("mixed workspace=%q resource=%q spec=%+v", marker, doc.Resources[0].Name, doc.Spec)
		}
		var inline string
		if err := jsonx.Unmarshal(doc.Spec.Inline, &inline); err != nil {
			t.Fatal(err)
		}
		if inline != `{"name":"`+marker+`"}` {
			t.Fatalf("workspace=%q inline=%s", marker, inline)
		}
		if doc.Overrides[0].ActiveStatus == nil || *doc.Overrides[0].ActiveStatus != status || doc.Endpoints[0].ActiveStatus != status {
			t.Fatalf("workspace=%q override=%+v endpoint=%+v", marker, doc.Overrides[0], doc.Endpoints[0])
		}
		if withData {
			if doc.Data == nil || len(doc.Data.Families) != 1 || len(doc.Data.Families[0].Rows) != 1 {
				t.Fatalf("missing entity data: %+v", doc.Data)
			}
			if got := string(doc.Data.Families[0].Rows[0].Data); got != `{"id":1,"name":"`+marker+`"}` {
				t.Fatalf("workspace=%q data=%s", marker, got)
			}
		} else if doc.Data != nil {
			t.Fatal("config-only export carries data")
		}
	}
}

func TestEntityCreateCapIncludesAssignedID(t *testing.T) {
	f := newFixture(t, 10)
	rid := f.insertResource(t, resourceFixture{family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON})
	r := resources.NewRepo(f.db, nil, 14, 64<<10, 1000) // Seven bytes cannot hold {"id":1}.
	if _, err := r.Create(t.Context(), rid, "", "", "id", "integer", map[string]any{}); !errors.Is(err, resources.ErrEntityLimit) {
		t.Errorf("Create: %v, want ErrEntityLimit", err)
	}
	var count, seq int64
	if err := f.db.R.QueryRowContext(t.Context(), "SELECT seq, (SELECT COUNT(*) FROM entities WHERE resource_id = resources.id) FROM resources WHERE id = ?", rid).Scan(&seq, &count); err != nil {
		t.Fatal(err)
	}
	if count != 0 || seq != 0 {
		t.Errorf("failed Create changed row count=%d seq=%d", count, seq)
	}
}

func TestEntityReplacementCountsUTF8BytesAtomically(t *testing.T) {
	for _, method := range []string{"Set", "Patch"} {
		t.Run(method, func(t *testing.T) {
			f := newFixture(t, 10)
			rid := f.insertResource(t, resourceFixture{family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON})
			r := resources.NewRepo(f.db, nil, 2000, 64<<10, 1000)
			for _, key := range []string{"1", "2"} {
				if _, _, err := r.Set(t.Context(), rid, "", "", key, "id", "integer", map[string]any{"payload": strings.Repeat("😀", 100)}); err != nil {
					t.Fatal(err)
				}
			}
			before, _, err := r.Get(t.Context(), rid, "", "", "1")
			if err != nil {
				t.Fatal(err)
			}
			write := r.Set
			if method == "Patch" {
				write = r.Patch
			}
			if _, _, err := write(t.Context(), rid, "", "", "1", "id", "integer", map[string]any{"payload": strings.Repeat("😀", 150)}); !errors.Is(err, resources.ErrEntityLimit) {
				t.Errorf("%s over cap: %v", method, err)
			}
			after, _, err := r.Get(t.Context(), rid, "", "", "1")
			if err != nil {
				t.Fatal(err)
			}
			if string(after.Data) != string(before.Data) {
				t.Errorf("failed %s changed row: %s", method, after.Data)
			}
			if _, _, err := write(t.Context(), rid, "", "", "1", "id", "integer", map[string]any{"payload": "small"}); err != nil {
				t.Errorf("%s shrinking row: %v", method, err)
			}
		})
	}
}

func TestEntityCounterOverflowLeavesStoredStateIntact(t *testing.T) {
	f := newFixture(t, 10)
	rid := f.insertResource(t, resourceFixture{family: "/widgets", name: "widgets", idField: "id", wrapper: wrapperJSON})
	r := resources.NewRepo(f.db, nil, 4<<20, 64<<10, 1000)
	const lastKey = "9223372036854775807"
	if _, _, err := r.Set(t.Context(), rid, "", "", lastKey, "id", "integer", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Create(t.Context(), rid, "", "", "id", "integer", map[string]any{}); err == nil {
		t.Fatal("Create allocated a key past int64 capacity")
	}
	var seq string
	var count int
	if err := f.db.R.QueryRowContext(t.Context(), "SELECT seq, (SELECT COUNT(*) FROM entities WHERE resource_id = resources.id) FROM resources WHERE id = ?", rid).Scan(&seq, &count); err != nil {
		t.Fatal(err)
	}
	if seq != lastKey || count != 1 {
		t.Fatalf("overflow changed seq=%q rows=%d", seq, count)
	}
}
