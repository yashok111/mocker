package specs_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
)

// minimalOAS30 is a valid, tiny OAS 3.0 document whose root servers[] give it
// an unambiguous base path (DESIGN §7 step 3: root servers[] win, first one).
const minimalOAS30 = `{
  "openapi": "3.0.3",
  "info": { "title": "Test API", "version": "1.0.0" },
  "servers": [ { "url": "https://api.example.com/api/v1" } ],
  "paths": {}
}`

// swagger2Doc is a Swagger 2.0 document: Detect recognizes it, but Load
// refuses it outright (conversion is P1b), so Import must surface
// ErrUnsupportedFormat through it, never a parse_error.
const swagger2Doc = `{
  "swagger": "2.0",
  "info": { "title": "Legacy", "version": "1.0.0" },
  "paths": {}
}`

// newTestDB opens a fresh, migrated SQLite file under t.TempDir() and closes
// it on cleanup. Mirrors the identical helper in internal/workspaces —
// there is no shared test-support package to reuse it from.
func newTestDB(t *testing.T, path string) *store.DB {
	t.Helper()
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

// testConfig returns a Config with every field the TEST CONFIG trap list
// warns about actually set: a zero MaxBody would reject every document
// before Import even hashes it.
func testConfig(t *testing.T, maxBody int64) *config.Config {
	t.Helper()
	return &config.Config{
		BaseDomain:     "mock.local",
		AdminHost:      "mocker.local",
		Routing:        config.RoutingHost,
		ReservedPrefix: "/__mocker",
		AuthMode:       config.AuthShared,
		DataDir:        t.TempDir(),
		MaxBody:        maxBody,
		MaxResponse:    4 << 20,
		RuntimeCache:   32,
		Dev:            true,
	}
}

// newRepo wires a Repo over a freshly migrated database with a generous
// MaxBody, for tests that are not themselves about the size limit.
func newRepo(t *testing.T) (*specs.Repo, *store.DB) {
	t.Helper()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	return specs.NewRepo(db, testConfig(t, 10<<20)), db
}

// attachWorkspace inserts a minimal workspaces row pointing spec_id at
// specID, directly by SQL: this package deliberately does not import
// internal/workspaces (see AttachedWorkspaces's doc comment), so its own
// tests reach for the table the same way the repo itself does.
func attachWorkspace(t *testing.T, db *store.DB, slug string, specID int64) {
	t.Helper()
	now := time.Now().Unix()
	_, err := db.W.ExecContext(t.Context(), `
		INSERT INTO workspaces (slug, name, spec_id, revision, settings, created_at, updated_at)
		VALUES (?, ?, ?, 1, '{}', ?, ?)`, slug, slug, specID, now, now)
	if err != nil {
		t.Fatalf("attach workspace %q to spec %d: %v", slug, specID, err)
	}
}

// replaceOps runs r.ReplaceOperations inside its own write transaction, the
// way the (not-yet-written) operation indexer eventually will.
func replaceOps(t *testing.T, db *store.DB, r *specs.Repo, specID int64, ops []*specs.Operation, resp map[int][]*specs.Response) {
	t.Helper()
	err := db.Write(t.Context(), func(tx *sql.Tx) error {
		return r.ReplaceOperations(t.Context(), tx, specID, ops, resp)
	})
	if err != nil {
		t.Fatalf("replace operations for spec %d: %v", specID, err)
	}
}

func countRows(t *testing.T, db *store.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.R.QueryRowContext(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count query %q: %v", query, err)
	}
	return n
}

func TestRepo_Import_storesRawAndNormalizedUnchanged(t *testing.T) {
	r, db := newRepo(t)
	raw := []byte(minimalOAS30)

	res, err := r.Import(t.Context(), specs.ImportInput{Name: "Test API", Source: "upload", Document: raw})
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	wantDoc, _, err := openapi.Load(raw)
	if err != nil {
		t.Fatalf("reference load: %v", err)
	}

	var gotRaw, gotNormalized []byte
	err = db.R.QueryRowContext(t.Context(), "SELECT raw, normalized FROM specs WHERE id = ?", res.Spec.ID).
		Scan(&gotRaw, &gotNormalized)
	if err != nil {
		t.Fatalf("select stored bytes: %v", err)
	}

	if string(gotRaw) != string(raw) {
		t.Errorf("stored raw = %q, want %q", gotRaw, raw)
	}
	if string(gotNormalized) != string(wantDoc.Normalized()) {
		t.Errorf("stored normalized = %q, want %q", gotNormalized, wantDoc.Normalized())
	}
	if !json.Valid(gotNormalized) {
		t.Errorf("stored normalized is not valid JSON: %q", gotNormalized)
	}
}

func TestRepo_Import_hashDedup(t *testing.T) {
	r, _ := newRepo(t)
	raw := []byte(minimalOAS30)
	in := specs.ImportInput{Name: "Test API", Source: "upload", Document: raw}

	first, err := r.Import(t.Context(), in)
	if err != nil {
		t.Fatalf("first import: %v", err)
	}

	second, err := r.Import(t.Context(), in)
	if !errors.Is(err, specs.ErrDuplicate) {
		t.Fatalf("second import error = %v, want ErrDuplicate", err)
	}
	if second == nil || second.Spec == nil {
		t.Fatal("second import: nil result on ErrDuplicate")
	}
	if second.Spec.ID != first.Spec.ID {
		t.Errorf("duplicate spec id = %d, want %d", second.Spec.ID, first.Spec.ID)
	}

	all, err := r.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 1 {
		t.Errorf("specs after duplicate import = %d, want 1", len(all))
	}
}

func TestRepo_Import_basePathFromReport(t *testing.T) {
	r, _ := newRepo(t)
	res, err := r.Import(t.Context(), specs.ImportInput{
		Name: "Test API", Source: "upload", Document: []byte(minimalOAS30),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	const wantBasePath = "/api/v1"
	if res.Spec.BasePath != wantBasePath {
		t.Errorf("spec.BasePath = %q, want %q", res.Spec.BasePath, wantBasePath)
	}
	if res.Report.BasePath != wantBasePath {
		t.Errorf("report.BasePath = %q, want %q", res.Report.BasePath, wantBasePath)
	}
	if res.Report.BasePathOrigin != openapi.BaseFromRootServers {
		t.Errorf("report.BasePathOrigin = %q, want %q", res.Report.BasePathOrigin, openapi.BaseFromRootServers)
	}

	stored, err := r.ByID(t.Context(), res.Spec.ID)
	if err != nil {
		t.Fatalf("by id: %v", err)
	}
	if stored.BasePath != wantBasePath {
		t.Errorf("stored base_path = %q, want %q", stored.BasePath, wantBasePath)
	}
}

func TestRepo_Import_tooLarge(t *testing.T) {
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	r := specs.NewRepo(db, testConfig(t, 16)) // smaller than minimalOAS30

	_, err := r.Import(t.Context(), specs.ImportInput{
		Name: "Test API", Source: "upload", Document: []byte(minimalOAS30),
	})
	if !errors.Is(err, specs.ErrTooLarge) {
		t.Fatalf("import error = %v, want ErrTooLarge", err)
	}

	all, err := r.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 0 {
		t.Errorf("specs after refused import = %d, want 0", len(all))
	}
}

// manyPathsDoc builds a valid OAS 3.0 document with n trivial "GET" paths —
// enough to drive Index's operation count past whatever ceiling a test
// wants to probe. Mirrors internal/admin's manyOpsDoc helper, which this
// package cannot import (different test package, no shared test-support
// package to reuse it from — the same reason newTestDB is duplicated too).
func manyPathsDoc(t *testing.T, n int) []byte {
	t.Helper()
	paths := make(map[string]any, n)
	for i := range n {
		paths[fmt.Sprintf("/p%d", i)] = map[string]any{"get": map[string]any{}}
	}
	doc := map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": "Many Paths", "version": "1.0.0"},
		"paths":   paths,
	}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal many-paths document: %v", err)
	}
	return b
}

// TestRepo_Import_rejectsTooManyOperations guards finding 4's operation-
// count ceiling: without it, a single in-limit document that indexes into a
// huge number of operations can hold store.DB.W's one writer connection for
// tens of seconds, blocking every other write in the process. 5001 is one
// past the ceiling (see maxIndexedOperations in repo.go, currently 5000) —
// this pins the exact boundary, not just "some huge count gets rejected".
func TestRepo_Import_rejectsTooManyOperations(t *testing.T) {
	r, db := newRepo(t)

	_, err := r.Import(t.Context(), specs.ImportInput{
		Name: "TooBig", Source: "upload", Document: manyPathsDoc(t, 5001),
	})
	if !errors.Is(err, specs.ErrTooManyOperations) {
		t.Fatalf("import error = %v, want ErrTooManyOperations", err)
	}

	// The rejection must happen BEFORE the write transaction opens: nothing
	// should have been inserted at all, not even a spec row later rolled
	// back.
	if got := countRows(t, db, "SELECT COUNT(*) FROM specs"); got != 0 {
		t.Errorf("specs rows after a rejected import = %d, want 0", got)
	}
}

func TestRepo_Import_swagger2UnsupportedFormat(t *testing.T) {
	r, _ := newRepo(t)
	_, err := r.Import(t.Context(), specs.ImportInput{
		Name: "Legacy", Source: "upload", Document: []byte(swagger2Doc),
	})
	if !errors.Is(err, specs.ErrUnsupportedFormat) {
		t.Fatalf("import error = %v, want ErrUnsupportedFormat", err)
	}
	if !errors.Is(err, openapi.ErrUnsupportedFormat) {
		t.Fatalf("import error = %v, want to also match openapi.ErrUnsupportedFormat", err)
	}
}

func TestRepo_Import_notADocument(t *testing.T) {
	r, _ := newRepo(t)
	_, err := r.Import(t.Context(), specs.ImportInput{
		Name: "Garbage", Source: "upload", Document: []byte(`{"hello":"world"}`),
	})
	if !errors.Is(err, specs.ErrNotADocument) {
		t.Fatalf("import error = %v, want ErrNotADocument", err)
	}
}

func TestRepo_ByID_notFound(t *testing.T) {
	r, _ := newRepo(t)
	_, err := r.ByID(t.Context(), 999)
	if !errors.Is(err, specs.ErrNotFound) {
		t.Fatalf("by id error = %v, want ErrNotFound", err)
	}
}

func TestRepo_OperationByID_notFound(t *testing.T) {
	r, _ := newRepo(t)
	_, err := r.OperationByID(t.Context(), 999)
	if !errors.Is(err, specs.ErrNotFound) {
		t.Fatalf("operation by id error = %v, want ErrNotFound", err)
	}
}

func TestRepo_OperationByID(t *testing.T) {
	r, db := newRepo(t)
	res, err := r.Import(t.Context(), specs.ImportInput{
		Name: "Templated", Source: "upload", Document: []byte(minimalOAS30),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := res.Spec.ID

	// The template path — WITH the {param} placeholder, never the concrete
	// path a client would request — is exactly what the to-override
	// conversion this method exists for needs back (see the method's own
	// doc comment: stripping a traffic row's concrete path can never
	// reconstruct this).
	replaceOps(t, db, r, specID, []*specs.Operation{
		{Method: "GET", Path: "/widgets/{widgetId}", CanonicalPath: "/widgets/{}", SourceOrder: 0, Pointer: "#/paths"},
	}, nil)

	ops, err := r.Operations(t.Context(), specID, 0, 0)
	if err != nil {
		t.Fatalf("operations: %v", err)
	}
	if len(ops) != 1 {
		t.Fatalf("operations count = %d, want 1", len(ops))
	}

	got, err := r.OperationByID(t.Context(), ops[0].ID)
	if err != nil {
		t.Fatalf("operation by id: %v", err)
	}
	if got.Method != http.MethodGet || got.Path != "/widgets/{widgetId}" {
		t.Errorf("operation by id = %+v, want method GET path /widgets/{widgetId}", got)
	}
	if got.SpecID != specID {
		t.Errorf("operation by id SpecID = %d, want %d", got.SpecID, specID)
	}
}

func TestRepo_List(t *testing.T) {
	r, _ := newRepo(t)
	if _, err := r.Import(t.Context(), specs.ImportInput{
		Name: "A", Source: "upload", Document: []byte(minimalOAS30),
	}); err != nil {
		t.Fatalf("import A: %v", err)
	}

	other := `{"openapi":"3.0.3","info":{"title":"B","version":"2.0.0"},"paths":{}}`
	if _, err := r.Import(t.Context(), specs.ImportInput{
		Name: "B", Source: "upload", Document: []byte(other),
	}); err != nil {
		t.Fatalf("import B: %v", err)
	}

	all, err := r.List(t.Context())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("specs count = %d, want 2", len(all))
	}
	if all[0].ID >= all[1].ID {
		t.Errorf("list not ordered by id ascending: %d, %d", all[0].ID, all[1].ID)
	}
}

func TestRepo_Operations_paging(t *testing.T) {
	r, db := newRepo(t)
	res, err := r.Import(t.Context(), specs.ImportInput{
		Name: "Paged", Source: "upload", Document: []byte(minimalOAS30),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := res.Spec.ID

	ops := make([]*specs.Operation, 5)
	for i := range ops {
		ops[i] = &specs.Operation{
			Method:        "GET",
			Path:          "/things/" + string(rune('a'+i)),
			CanonicalPath: "/things/" + string(rune('a'+i)),
			SourceOrder:   int64(i),
			Pointer:       "#/paths",
		}
	}
	replaceOps(t, db, r, specID, ops, nil)

	tests := []struct {
		name       string
		limit      int
		offset     int
		wantOrders []int64
	}{
		{name: "all via limit<=0", limit: 0, offset: 0, wantOrders: []int64{0, 1, 2, 3, 4}},
		{name: "negative limit means all too", limit: -1, offset: 0, wantOrders: []int64{0, 1, 2, 3, 4}},
		{name: "first page", limit: 2, offset: 0, wantOrders: []int64{0, 1}},
		{name: "second page", limit: 2, offset: 2, wantOrders: []int64{2, 3}},
		{name: "last partial page", limit: 2, offset: 4, wantOrders: []int64{4}},
		{name: "offset past end", limit: 2, offset: 10, wantOrders: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := r.Operations(t.Context(), specID, tt.limit, tt.offset)
			if err != nil {
				t.Fatalf("operations: %v", err)
			}
			if len(got) != len(tt.wantOrders) {
				t.Fatalf("got %d operations, want %d", len(got), len(tt.wantOrders))
			}
			for i, want := range tt.wantOrders {
				if got[i].SourceOrder != want {
					t.Errorf("operations[%d].SourceOrder = %d, want %d", i, got[i].SourceOrder, want)
				}
			}
		})
	}
}

func TestRepo_Delete_unattachedCascadesOperations(t *testing.T) {
	r, db := newRepo(t)
	res, err := r.Import(t.Context(), specs.ImportInput{
		Name: "ToDelete", Source: "upload", Document: []byte(minimalOAS30),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := res.Spec.ID

	summary := "list things"
	replaceOps(t, db, r, specID, []*specs.Operation{
		{Method: "GET", Path: "/things", CanonicalPath: "/things", SourceOrder: 0, Pointer: "#/paths", Summary: &summary},
	}, nil)

	if got := countRows(t, db, "SELECT COUNT(*) FROM operations WHERE spec_id = ?", specID); got != 1 {
		t.Fatalf("operations before delete = %d, want 1", got)
	}

	if err := r.Delete(t.Context(), specID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	if _, err := r.ByID(t.Context(), specID); !errors.Is(err, specs.ErrNotFound) {
		t.Errorf("by id after delete = %v, want ErrNotFound", err)
	}
	if got := countRows(t, db, "SELECT COUNT(*) FROM operations WHERE spec_id = ?", specID); got != 0 {
		t.Errorf("operations after delete = %d, want 0 (cascade)", got)
	}
}

func TestRepo_Delete_attachedReturnsErrAttachedAndDeletesNothing(t *testing.T) {
	r, db := newRepo(t)
	res, err := r.Import(t.Context(), specs.ImportInput{
		Name: "Attached", Source: "upload", Document: []byte(minimalOAS30),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := res.Spec.ID
	attachWorkspace(t, db, "alex", specID)

	err = r.Delete(t.Context(), specID)
	if !errors.Is(err, specs.ErrAttached) {
		t.Fatalf("delete error = %v, want ErrAttached", err)
	}

	if _, err := r.ByID(t.Context(), specID); err != nil {
		t.Errorf("spec was deleted despite attachment: %v", err)
	}

	slugs, err := r.AttachedWorkspaces(t.Context(), specID)
	if err != nil {
		t.Fatalf("attached workspaces: %v", err)
	}
	if len(slugs) != 1 || slugs[0] != "alex" {
		t.Errorf("attached workspaces = %v, want [alex]", slugs)
	}
}

func TestRepo_ReplaceOperations_idempotent(t *testing.T) {
	r, db := newRepo(t)
	res, err := r.Import(t.Context(), specs.ImportInput{
		Name: "Idempotent", Source: "upload", Document: []byte(minimalOAS30),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := res.Spec.ID

	ops := []*specs.Operation{
		{Method: "GET", Path: "/a", CanonicalPath: "/a", SourceOrder: 0, Pointer: "#/paths/~1a"},
		{Method: "POST", Path: "/a", CanonicalPath: "/a", SourceOrder: 1, Pointer: "#/paths/~1a"},
	}
	resp := map[int][]*specs.Response{
		0: {{Selector: "200", HTTPStatus: 200, IsDefault: true, StatusOrigin: "numeric"}},
	}

	replaceOps(t, db, r, specID, ops, resp)
	replaceOps(t, db, r, specID, ops, resp) // same input again, on purpose

	if got := countRows(t, db, "SELECT COUNT(*) FROM operations WHERE spec_id = ?", specID); got != 2 {
		t.Errorf("operations after two ReplaceOperations calls = %d, want 2", got)
	}

	gotOps, err := r.Operations(t.Context(), specID, 0, 0)
	if err != nil {
		t.Fatalf("operations: %v", err)
	}
	if len(gotOps) != 2 {
		t.Fatalf("operations = %d, want 2", len(gotOps))
	}

	responses, err := r.Responses(t.Context(), gotOps[0].ID)
	if err != nil {
		t.Fatalf("responses: %v", err)
	}
	if len(responses) != 1 {
		t.Errorf("responses for first operation = %d, want 1 (not doubled)", len(responses))
	}
}

// TestRepo_ReplaceOperations_batchedInsertOrder pins the correctness
// assumption ReplaceOperations's batching relies on (finding 4): a
// multi-row INSERT ... RETURNING assigns and echoes ids in the SAME order
// as the VALUES clause, so a batch's Nth response row can be linked to the
// Nth freshly inserted operation id without re-querying. n is set well past
// two multiples of the internal batch size so this exercises AT LEAST three
// separate batched INSERT statements, not just one — a bug that only shows
// up at a batch boundary would not be caught by a smaller n.
func TestRepo_ReplaceOperations_batchedInsertOrder(t *testing.T) {
	r, db := newRepo(t)
	res, err := r.Import(t.Context(), specs.ImportInput{
		Name: "Batched", Source: "upload", Document: []byte(minimalOAS30),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := res.Spec.ID

	const n = 450 // > 2 * the 200-row internal batch size
	ops := make([]*specs.Operation, n)
	resp := make(map[int][]*specs.Response, n)
	for i := range n {
		path := fmt.Sprintf("/op%04d", i)
		ops[i] = &specs.Operation{
			Method: "GET", Path: path, CanonicalPath: path,
			SourceOrder: int64(i), Pointer: "#/paths/" + path,
		}
		// MediaType carries this operation's own index, so a response
		// attached to the WRONG operation (e.g. because a batch's ids got
		// mismatched to its rows) is directly visible in the assertion below.
		mt := fmt.Sprintf("application/op-%04d", i)
		resp[i] = []*specs.Response{
			{Selector: "200", HTTPStatus: 200, IsDefault: true, StatusOrigin: "numeric", MediaType: &mt},
		}
	}

	replaceOps(t, db, r, specID, ops, resp)

	gotOps, err := r.Operations(t.Context(), specID, 0, 0)
	if err != nil {
		t.Fatalf("operations: %v", err)
	}
	if len(gotOps) != n {
		t.Fatalf("operations = %d, want %d", len(gotOps), n)
	}

	for i, op := range gotOps {
		wantPath := fmt.Sprintf("/op%04d", i)
		if op.Path != wantPath {
			t.Fatalf("operations[%d].Path = %q, want %q (order not preserved across batches)", i, op.Path, wantPath)
		}
		responses, err := r.Responses(t.Context(), op.ID)
		if err != nil {
			t.Fatalf("responses for op %d (%s): %v", op.ID, op.Path, err)
		}
		if len(responses) != 1 {
			t.Fatalf("responses for %s = %d, want 1", op.Path, len(responses))
		}
		wantMT := fmt.Sprintf("application/op-%04d", i)
		if responses[0].MediaType == nil || *responses[0].MediaType != wantMT {
			t.Fatalf("responses[0].MediaType for %s = %v, want %q (response attached to the wrong operation across a batch boundary)",
				op.Path, responses[0].MediaType, wantMT)
		}
	}
}

func TestRepo_Report_survivesReopeningTheDatabase(t *testing.T) {
	dbPath := t.TempDir() + "/mocker.db"
	db := newTestDB(t, dbPath)
	r := specs.NewRepo(db, testConfig(t, 10<<20))

	res, err := r.Import(t.Context(), specs.ImportInput{
		Name: "Reopened", Source: "upload", Document: []byte(minimalOAS30),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := res.Spec.ID

	parseErr := "unresolved $ref"
	replaceOps(t, db, r, specID, []*specs.Operation{
		{Method: "GET", Path: "/broken", CanonicalPath: "/broken", SourceOrder: 0, Pointer: "#/paths/~1broken", ParseError: &parseErr},
		{Method: "GET", Path: "/ok", CanonicalPath: "/ok", SourceOrder: 1, Pointer: "#/paths/~1ok"},
	}, nil)

	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	reopened, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}

	r2 := specs.NewRepo(reopened, testConfig(t, 10<<20))
	report, err := r2.Report(t.Context(), specID)
	if err != nil {
		t.Fatalf("report after reopen: %v", err)
	}

	if report.Format != openapi.FormatOAS30 {
		t.Errorf("report.Format = %q, want %q", report.Format, openapi.FormatOAS30)
	}
	if report.BasePath != "/api/v1" {
		t.Errorf("report.BasePath = %q, want /api/v1", report.BasePath)
	}
	if report.Operations != 2 {
		t.Errorf("report.Operations = %d, want 2", report.Operations)
	}
	if report.Degraded != 1 {
		t.Errorf("report.Degraded = %d, want 1", report.Degraded)
	}
	found := false
	for _, w := range report.Warnings {
		if w.Message == parseErr {
			found = true
		}
	}
	if !found {
		t.Errorf("report.Warnings = %+v, want one containing %q", report.Warnings, parseErr)
	}
}

// oas31OneOpDoc is a valid OAS 3.1 document with exactly one operation —
// deliberately different from minimalOAS30 in both format and operation
// count, so a test can tell whether a *Report reflects THIS document or a
// stale one.
const oas31OneOpDoc = `{
  "openapi": "3.1.0",
  "info": { "title": "One Op", "version": "1.0.0" },
  "paths": {
    "/x": { "get": { "operationId": "getX", "responses": {"200": {"description": "ok"}} } }
  }
}`

// TestRepo_Report_returnsIndependentCopies guards finding 3's cache: Report
// now hands back a memoized *openapi.Report on a repeat call, so a caller
// mutating what it got back (as Report's own doc comment notes callers may,
// via *Report.Add) must never be able to corrupt the cached copy other
// callers see next.
func TestRepo_Report_returnsIndependentCopies(t *testing.T) {
	r, _ := newRepo(t)
	res, err := r.Import(t.Context(), specs.ImportInput{
		Name: "Test", Source: "upload", Document: []byte(minimalOAS30),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := res.Spec.ID

	rep1, err := r.Report(t.Context(), specID)
	if err != nil {
		t.Fatalf("report 1: %v", err)
	}
	rep1.Add("#/mutated", "test-mutation", "must not leak into the cache")

	rep2, err := r.Report(t.Context(), specID)
	if err != nil {
		t.Fatalf("report 2: %v", err)
	}
	for _, w := range rep2.Warnings {
		if w.Code == "test-mutation" {
			t.Fatalf("report 2 saw a mutation made to report 1's own copy: the cache is not isolated")
		}
	}
}

// TestRepo_Report_notStaleAfterIDReuse guards finding 3's cache against the
// one case that makes it more than a hygiene optimization: specs.id has no
// AUTOINCREMENT (0001_init.sql), so deleting the spec with the highest id
// and then importing a new one is documented SQLite behavior that hands the
// new spec that SAME id back. Without [Repo.Delete] evicting the cache
// entry, the new spec would silently answer Report with the deleted spec's
// memoized result.
func TestRepo_Report_notStaleAfterIDReuse(t *testing.T) {
	r, _ := newRepo(t)

	res1, err := r.Import(t.Context(), specs.ImportInput{
		Name: "First", Source: "upload", Document: []byte(minimalOAS30),
	})
	if err != nil {
		t.Fatalf("import first: %v", err)
	}
	id1 := res1.Spec.ID

	// Warm the cache for id1 before deleting it.
	rep1, err := r.Report(t.Context(), id1)
	if err != nil {
		t.Fatalf("report (warm cache): %v", err)
	}
	if rep1.Format != openapi.FormatOAS30 {
		t.Fatalf("rep1.Format = %q, want %q", rep1.Format, openapi.FormatOAS30)
	}

	if err := r.Delete(t.Context(), id1); err != nil {
		t.Fatalf("delete: %v", err)
	}

	res2, err := r.Import(t.Context(), specs.ImportInput{
		Name: "Second", Source: "upload", Document: []byte(oas31OneOpDoc),
	})
	if err != nil {
		t.Fatalf("import second: %v", err)
	}
	if res2.Spec.ID != id1 {
		t.Skipf("SQLite did not reuse id %d for the new spec (got %d); id-reuse precondition not met on this build", id1, res2.Spec.ID)
	}

	rep2, err := r.Report(t.Context(), res2.Spec.ID)
	if err != nil {
		t.Fatalf("report after reuse: %v", err)
	}
	if rep2.Format != openapi.FormatOAS31 {
		t.Errorf("rep2.Format = %q, want %q (stale cache from the deleted spec)", rep2.Format, openapi.FormatOAS31)
	}
	if rep2.Operations != 1 {
		t.Errorf("rep2.Operations = %d, want 1 (stale cache from the deleted spec)", rep2.Operations)
	}
}

func TestRepo_Routes_fillsEveryField(t *testing.T) {
	r, db := newRepo(t)
	res, err := r.Import(t.Context(), specs.ImportInput{
		Name: "Routes", Source: "upload", Document: []byte(minimalOAS30),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := res.Spec.ID

	opID := "listThings"
	replaceOps(t, db, r, specID, []*specs.Operation{
		{Method: "get", Path: "/things", CanonicalPath: "/things", SourceOrder: 7, Pointer: "#/paths/~1things", OperationID: &opID},
		{Method: "delete", Path: "/things/{id}", CanonicalPath: "/things/{}", SourceOrder: 8, Pointer: "#/paths/~1things~1{id}"},
	}, nil)

	routes, err := r.Routes(t.Context(), specID)
	if err != nil {
		t.Fatalf("routes: %v", err)
	}
	if len(routes) != 2 {
		t.Fatalf("routes = %d, want 2", len(routes))
	}

	ops, err := r.Operations(t.Context(), specID, 0, 0)
	if err != nil {
		t.Fatalf("operations: %v", err)
	}
	byOrder := make(map[int64]*specs.Operation, len(ops))
	for _, op := range ops {
		byOrder[op.SourceOrder] = op
	}

	for _, route := range routes {
		op, ok := byOrder[route.SourceOrder]
		if !ok {
			t.Fatalf("route with source order %d has no matching operation row", route.SourceOrder)
		}
		if route.OpRowID != op.ID {
			t.Errorf("route.OpRowID = %d, want %d", route.OpRowID, op.ID)
		}
		if route.Method != op.Method {
			t.Errorf("route.Method = %q, want %q", route.Method, op.Method)
		}
		if route.Path != op.Path {
			t.Errorf("route.Path = %q, want %q", route.Path, op.Path)
		}
		if route.CanonicalPath != op.CanonicalPath {
			t.Errorf("route.CanonicalPath = %q, want %q", route.CanonicalPath, op.CanonicalPath)
		}
		if route.Custom {
			t.Errorf("route.Custom = true, want false (no custom endpoints until P2)")
		}
		wantLabel := ""
		if op.OperationID != nil {
			wantLabel = *op.OperationID
		}
		if route.OperationLabel != wantLabel {
			t.Errorf("route.OperationLabel = %q, want %q", route.OperationLabel, wantLabel)
		}
	}
}

func TestRepo_Normalized_returnsStoredBytes(t *testing.T) {
	r, db := newRepo(t)
	res, err := r.Import(t.Context(), specs.ImportInput{
		Name: "Norm", Source: "upload", Document: []byte(minimalOAS30),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	var want []byte
	if err := db.R.QueryRowContext(t.Context(), "SELECT normalized FROM specs WHERE id = ?", res.Spec.ID).Scan(&want); err != nil {
		t.Fatalf("select normalized column directly: %v", err)
	}

	got, err := r.Normalized(t.Context(), res.Spec.ID)
	if err != nil {
		t.Fatalf("Normalized: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("Normalized() = %q, want %q", got, want)
	}
	if !json.Valid(got) {
		t.Errorf("Normalized() is not valid JSON: %q", got)
	}
}

// TestRepo_Normalized_isIdempotentThroughLoad pins the assumption the whole
// P1b runtime build leans on (per the phase digest): re-running
// [openapi.Load] over an already-normalized document must reproduce the
// SAME normalized bytes, not a second, subtly different pass of dialect
// normalization. If this ever broke, every runtime rebuild (routes.go's
// cache, on a cold (workspace, revision)) would silently diverge from what
// Import itself stored and hashed.
func TestRepo_Normalized_isIdempotentThroughLoad(t *testing.T) {
	r, _ := newRepo(t)
	res, err := r.Import(t.Context(), specs.ImportInput{
		Name: "Idem", Source: "upload", Document: []byte(minimalOAS30),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	normalized, err := r.Normalized(t.Context(), res.Spec.ID)
	if err != nil {
		t.Fatalf("Normalized: %v", err)
	}

	doc, _, err := openapi.Load(normalized)
	if err != nil {
		t.Fatalf("re-Load(normalized): %v", err)
	}
	if string(doc.Normalized()) != string(normalized) {
		t.Errorf("Load(normalized).Normalized() = %q, want the same bytes fed in (Load must be idempotent on already-normalized input)",
			doc.Normalized())
	}
}

func TestRepo_Normalized_notFound(t *testing.T) {
	r, _ := newRepo(t)
	if _, err := r.Normalized(t.Context(), 999); !errors.Is(err, specs.ErrNotFound) {
		t.Errorf("Normalized(missing) error = %v, want ErrNotFound", err)
	}
}

// TestRepo_Variants_joinsOperationsAndResponses is Variants' field-by-field
// proof: every gen.ResponseVariant field traces back to a real column on
// EITHER side of the join (operation_responses for the response-shaped
// fields, operations for OpPointer and Degraded), across two operations so a
// mismatched join (a variant attributed to the wrong operation) would show
// up as a wrong OpRowID or OpPointer, not just a wrong count.
func TestRepo_Variants_joinsOperationsAndResponses(t *testing.T) {
	r, db := newRepo(t)
	res, err := r.Import(t.Context(), specs.ImportInput{
		Name: "Variants", Source: "upload", Document: []byte(minimalOAS30),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := res.Spec.ID

	mt := "application/json"
	schemaPtr := "#/components/schemas/Thing"
	parseErr := "boom"

	ops := []*specs.Operation{
		{Method: "GET", Path: "/a", CanonicalPath: "/a", SourceOrder: 0, Pointer: "#/paths/~1a/get"},
		{Method: "GET", Path: "/b", CanonicalPath: "/b", SourceOrder: 1, Pointer: "#/paths/~1b/get", ParseError: &parseErr},
	}
	resp := map[int][]*specs.Response{
		0: {
			{Selector: "200", HTTPStatus: 200, IsDefault: false, StatusOrigin: "numeric", MediaType: &mt, SchemaPtr: &schemaPtr},
			{Selector: "default", HTTPStatus: 200, IsDefault: true, StatusOrigin: "default"},
		},
		1: {
			{Selector: "200", HTTPStatus: 200, IsDefault: true, StatusOrigin: "numeric"},
		},
	}
	replaceOps(t, db, r, specID, ops, resp)

	gotOps, err := r.Operations(t.Context(), specID, 0, 0)
	if err != nil {
		t.Fatalf("operations: %v", err)
	}
	byPath := make(map[string]*specs.Operation, len(gotOps))
	for _, op := range gotOps {
		byPath[op.Path] = op
	}

	variants, err := r.Variants(t.Context(), specID)
	if err != nil {
		t.Fatalf("Variants: %v", err)
	}

	opA := byPath["/a"]
	vs := variants[opA.ID]
	if len(vs) != 2 {
		t.Fatalf("variants for /a = %d, want 2", len(vs))
	}
	var v200, vDefault gen.ResponseVariant
	for _, v := range vs {
		switch v.Selector {
		case "200":
			v200 = v
		case "default":
			vDefault = v
		default:
			t.Fatalf("unexpected selector %q", v.Selector)
		}
	}
	if v200.OpRowID != opA.ID {
		t.Errorf("v200.OpRowID = %d, want %d", v200.OpRowID, opA.ID)
	}
	if v200.HTTPStatus != 200 || v200.IsDefault {
		t.Errorf("v200 = %+v, want HTTPStatus 200, IsDefault false", v200)
	}
	if v200.MediaType != mt {
		t.Errorf("v200.MediaType = %q, want %q", v200.MediaType, mt)
	}
	if v200.SchemaPtr != schemaPtr {
		t.Errorf("v200.SchemaPtr = %q, want %q", v200.SchemaPtr, schemaPtr)
	}
	if v200.OpPointer != opA.Pointer {
		t.Errorf("v200.OpPointer = %q, want %q", v200.OpPointer, opA.Pointer)
	}
	if v200.Degraded {
		t.Errorf("v200.Degraded = true, want false (operation /a has no parse_error)")
	}

	if !vDefault.IsDefault {
		t.Errorf("vDefault.IsDefault = false, want true")
	}
	if vDefault.MediaType != "" {
		t.Errorf("vDefault.MediaType = %q, want empty (no media_type stored)", vDefault.MediaType)
	}
	if vDefault.SchemaPtr != "" {
		t.Errorf("vDefault.SchemaPtr = %q, want empty (no schema_ptr stored)", vDefault.SchemaPtr)
	}

	opB := byPath["/b"]
	vsB := variants[opB.ID]
	if len(vsB) != 1 {
		t.Fatalf("variants for /b = %d, want 1", len(vsB))
	}
	if !vsB[0].Degraded {
		t.Errorf("vsB[0].Degraded = false, want true (operation /b has a parse_error)")
	}
}

func TestRepo_Variants_emptyForSpecWithNoResponses(t *testing.T) {
	r, db := newRepo(t)
	res, err := r.Import(t.Context(), specs.ImportInput{
		Name: "NoResponses", Source: "upload", Document: []byte(minimalOAS30),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	specID := res.Spec.ID

	replaceOps(t, db, r, specID, []*specs.Operation{
		{Method: "GET", Path: "/a", CanonicalPath: "/a", SourceOrder: 0, Pointer: "#/paths/~1a/get"},
	}, nil)

	variants, err := r.Variants(t.Context(), specID)
	if err != nil {
		t.Fatalf("Variants: %v", err)
	}
	if len(variants) != 0 {
		t.Errorf("Variants = %v, want empty map (operation has no responses)", variants)
	}
}
