package backendmodel

import (
	"encoding/json/v2"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testkit"
)

func testRepo(t *testing.T) (*Repo, *store.DB) {
	t.Helper()
	db := testkit.NewDB(t)
	return NewRepo(db), db
}

func createProject(t *testing.T, repo *Repo, key string) *Project {
	t.Helper()
	p, err := repo.Create(t.Context(), CreateInput{Name: "Orders", IdempotencyKey: key})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func assertFault(t *testing.T, err error, code string) *FaultError {
	t.Helper()
	f, ok := errors.AsType[*FaultError](err)
	if !ok || f.Code != code {
		t.Fatalf("error = %v; want %s", err, code)
	}
	return f
}

func renameInput(version int64, key, name string) CommandsInput {
	return CommandsInput{ExpectedVersion: version, IdempotencyKey: key,
		Commands: []Command{{Type: "rename_project", Name: name}}}
}

func TestInitialRevisionAndMetadataIsolation(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	if !ValidID(p.ID) || !ValidID(p.CurrentRevisionID) || p.Version != 1 || len(p.Repositories) != 0 {
		t.Fatalf("initial project: %+v", p)
	}
	rev, err := r.Revision(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if rev.SchemaVersion != "1" || rev.Coverage.Status != "partial" || rev.Coverage.Denominator != nil || len(rev.Coverage.Gaps) == 0 || len(rev.SemanticHash) != 64 {
		t.Fatalf("initial revision claims coverage or lacks provenance: %+v", rev)
	}
	before, _ := json.Marshal(rev)
	updated, err := r.Apply(t.Context(), p.ID, renameInput(1, "rename", " Shipping "))
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "Shipping" || updated.Version != 2 || updated.CurrentRevisionID != rev.ID {
		t.Fatalf("rename: %+v", updated)
	}
	after, err := r.Revision(t.Context(), p.ID, rev.ID)
	if err != nil {
		t.Fatal(err)
	}
	afterJSON, _ := json.Marshal(after)
	if string(before) != string(afterJSON) {
		t.Fatal("metadata write changed immutable revision")
	}
	other := createProject(t, r, "other")
	otherRev, err := r.Revision(t.Context(), other.ID, other.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	if otherRev.SemanticHash != rev.SemanticHash {
		t.Fatal("empty semantic hash depends on project identity")
	}
	_, err = r.Revision(t.Context(), other.ID, rev.ID)
	assertFault(t, err, "backend_not_found")
}

func TestReceiptsSurviveRestartAndReplayOriginalResponse(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	p := createProject(t, r, "create")
	first, err := r.Apply(t.Context(), p.ID, renameInput(1, "rename", "Shipping"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Apply(t.Context(), p.ID, renameInput(2, "second", "Billing"))
	if err != nil {
		t.Fatal(err)
	}
	path := db.Path()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	r = NewRepo(reopened)
	replay, err := r.Apply(t.Context(), p.ID, renameInput(1, "rename", "Shipping"))
	if err != nil {
		t.Fatal(err)
	}
	if replay.Version != first.Version || replay.Name != first.Name {
		t.Fatalf("replayed current state: %+v", replay)
	}
	created := createProject(t, r, "create")
	if created.ID != p.ID || created.Version != 1 || created.Name != "Orders" {
		t.Fatalf("create replay: %+v", created)
	}
	current, err := r.Get(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Version != 3 || current.Name != "Billing" {
		t.Fatalf("replay wrote state: %+v", current)
	}
	_, err = r.Create(t.Context(), CreateInput{Name: "different", IdempotencyKey: "create"})
	assertFault(t, err, "backend_idempotency_conflict")
	_, err = r.Apply(t.Context(), p.ID, renameInput(2, "rename", "Shipping"))
	assertFault(t, err, "backend_idempotency_conflict")
}

func TestConcurrentMetadataWritesUseCAS(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"Billing", "Shipping"} {
		wg.Go(func() { _, err := r.Apply(t.Context(), p.ID, renameInput(1, name, name)); results <- err })
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
			continue
		}
		f := assertFault(t, err, "backend_version_conflict")
		if f.CurrentVersion != 2 {
			t.Fatalf("current version: %+v", f)
		}
	}
	if success != 1 {
		t.Fatalf("successful concurrent writes = %d", success)
	}
}

func TestPaginationValidationAndScope(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	for _, key := range []string{"a", "b", "c"} {
		createProject(t, r, key)
	}
	first, err := r.List(t.Context(), ListInput{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || first.NextCursor == "" {
		t.Fatalf("first page: %+v", first)
	}
	next, err := r.List(t.Context(), ListInput{Limit: 2, Cursor: first.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Items) != 1 || next.NextCursor != "" || first.Items[1].ID >= next.Items[0].ID {
		t.Fatalf("second page: %+v", next)
	}
	_, err = r.Revisions(t.Context(), first.Items[0].ID, ListInput{Cursor: first.NextCursor})
	assertFault(t, err, "backend_invalid")
	for _, in := range []ListInput{{Limit: -1}, {Limit: 101}, {Cursor: "not-a-cursor"}} {
		_, err = r.List(t.Context(), in)
		assertFault(t, err, "backend_invalid")
	}
	revs, err := r.Revisions(t.Context(), first.Items[0].ID, ListInput{})
	if err != nil || len(revs.Items) != 1 {
		t.Fatalf("revisions: %+v %v", revs, err)
	}
	_, err = r.Get(t.Context(), "bad/id")
	assertFault(t, err, "backend_not_found")
}

func TestInvalidMutationsAndVersionOverflowAreAtomic(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	for _, in := range []CreateInput{{Name: "", IdempotencyKey: "key"}, {Name: strings.Repeat("界", 201), IdempotencyKey: "key"}, {Name: "ok"}, {Name: "ok", IdempotencyKey: "a\nb"}} {
		_, err := r.Create(t.Context(), in)
		assertFault(t, err, "backend_invalid")
	}
	p := createProject(t, r, "create")
	for _, commands := range [][]Command{nil, {{Type: "unknown", Name: "n"}}, {{Type: "rename_project", Name: ""}}, {{Type: "rename_project", Name: "a"}, {Type: "rename_project", Name: "b"}}} {
		_, err := r.Apply(t.Context(), p.ID, CommandsInput{ExpectedVersion: 1, IdempotencyKey: "invalid", Commands: commands})
		assertFault(t, err, "backend_invalid")
	}
	if _, err := db.W.ExecContext(t.Context(), "UPDATE backend_projects SET version=? WHERE id=?", int64(math.MaxInt64), p.ID); err != nil {
		t.Fatal(err)
	}
	_, err := r.Apply(t.Context(), p.ID, renameInput(math.MaxInt64, "overflow", "changed"))
	assertFault(t, err, "backend_version_exhausted")
	current, err := r.Get(t.Context(), p.ID)
	if err != nil || current.Name != p.Name || current.Version != math.MaxInt64 {
		t.Fatalf("overflow changed data: %+v %v", current, err)
	}
}
