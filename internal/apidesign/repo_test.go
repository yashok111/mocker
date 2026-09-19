package apidesign

import (
	"database/sql"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/store"
)

const testDocument = `{"openapi":"3.1.0","info":{"title":"Orders","version":"1"},"paths":{"/orders":{"get":{"operationId":"orders","responses":{"200":{"description":"OK","content":{"application/json":{"example":{"n":9007199254740993},"schema":{"type":"object"}}}}}}}},"x-retained":{"value":true}}`

func testRepo(t *testing.T) (*Repo, *store.DB) {
	t.Helper()
	db, err := store.Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(t.Context(), slog.Default()); err != nil {
		t.Fatal(err)
	}
	return NewRepo(db, &config.Config{MaxBody: 1 << 20}), db
}

func TestFailedTransactionsLeaveNoPartialProjection(t *testing.T) {
	r, db := testRepo(t)
	ctx := t.Context()
	d, err := r.Create(ctx, CreateInput{Name: "API", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	countSpecs := func() int {
		var n int
		if err := db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM specs").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := countSpecs()
	err = db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `CREATE TRIGGER reject_draft_projection BEFORE UPDATE OF spec_id ON workspaces BEGIN SELECT RAISE(ABORT,'injected failure'); END`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Save(ctx, d.Design.ID, SaveInput{ExpectedVersion: 1, Document: strings.Replace(testDocument, "Orders", "Fail", 1), Source: "ui"}); err == nil {
		t.Fatal("injected save succeeded")
	}
	after, err := r.Detail(ctx, d.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Design.Version != 1 || len(after.Revisions) != 1 || after.Draft.ID != d.Draft.ID || countSpecs() != before {
		t.Fatalf("partial save survived: %+v", after)
	}
	review, err := r.RequestReview(ctx, d.Design.ID, 1, "ready", "ui")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Publish(ctx, d.Design.ID, review.ID, 1, "ui"); err == nil {
		t.Fatal("injected publish succeeded")
	}
	after, err = r.Detail(ctx, d.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Published != nil || len(after.Releases) != 0 || after.Reviews[0].Status != "pending" {
		t.Fatalf("partial release survived: %+v", after)
	}
}

func TestWorkspaceImportFenceRefusesStaleExport(t *testing.T) {
	r, db := testRepo(t)
	ctx := t.Context()
	original, err := r.Create(ctx, CreateInput{Name: "Source", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	var sourceRevision int64
	if err = db.R.QueryRowContext(ctx, "SELECT revision FROM workspaces WHERE id=?", original.Design.DraftWorkspaceID).Scan(&sourceRevision); err != nil {
		t.Fatal(err)
	}
	if _, err = r.Save(ctx, original.Design.ID, SaveInput{ExpectedVersion: 1, Document: strings.Replace(testDocument, "Orders", "new", 1), Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	_, err = r.Create(ctx, CreateInput{Name: "Torn", Document: testDocument, Source: "ui", WorkspaceID: original.Design.DraftWorkspaceID, WorkspaceRevision: sourceRevision})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale source accepted: %v", err)
	}
	designs, err := r.List(ctx)
	if err != nil || len(designs) != 1 {
		t.Fatalf("partial import: %+v %v", designs, err)
	}
}

func TestRevisionPublicationLifecycle(t *testing.T) {
	r, db := testRepo(t)
	ctx := t.Context()
	d, err := r.Create(ctx, CreateInput{Name: "Orders", Document: testDocument, Source: "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	first := d.Draft
	if d.Published != nil || d.Design.Version != 1 {
		t.Fatalf("initial: %+v", d)
	}
	changed := strings.Replace(testDocument, "Orders", "Orders edited", 1)
	d, err = r.Save(ctx, d.Design.ID, SaveInput{ExpectedVersion: 1, Document: changed, Summary: "edit", Source: "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Draft.Version != 2 || !strings.Contains(d.Draft.Document, "9007199254740993") {
		t.Fatal(d.Draft)
	}
	old, err := r.Revision(ctx, d.Design.ID, first.ID)
	if err != nil || old.Document != first.Document {
		t.Fatalf("old revision changed: %v", err)
	}
	if _, err = r.Save(ctx, d.Design.ID, SaveInput{ExpectedVersion: 1, Document: testDocument, Source: "ui"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale save: %v", err)
	}
	review, err := r.RequestReview(ctx, d.Design.ID, 2, "ready", "mcp")
	if err != nil {
		t.Fatal(err)
	}
	if review.BaseRevisionID != first.ID {
		t.Fatal(review)
	}
	release, err := r.Publish(ctx, d.Design.ID, review.ID, 2, "ui")
	if err != nil {
		t.Fatal(err)
	}
	var specID int64
	if err = db.R.QueryRowContext(ctx, "SELECT spec_id FROM workspaces WHERE id=?", d.Design.PublishedWorkspaceID).Scan(&specID); err != nil || specID == 0 {
		t.Fatalf("publication spec: %d %v", specID, err)
	}
	d, err = r.Save(ctx, d.Design.ID, SaveInput{ExpectedVersion: 2, Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	retry, err := r.Publish(ctx, d.Design.ID, review.ID, 2, "ui")
	if err != nil || retry.ID != release.ID {
		t.Fatalf("retry: %+v %v", retry, err)
	}
	if _, err = r.Publish(ctx, d.Design.ID, review.ID, 3, "mcp"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("MCP publish: %v", err)
	}
	stale, err := r.RequestReview(ctx, d.Design.ID, 3, "ready", "ui")
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.Save(ctx, d.Design.ID, SaveInput{ExpectedVersion: 3, Document: changed, Source: "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = r.Publish(ctx, d.Design.ID, stale.ID, 3, "ui"); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale publish: %v", err)
	}
}

func TestConcurrentSaveAndInvalidDraft(t *testing.T) {
	r, _ := testRepo(t)
	ctx := t.Context()
	d, err := r.Create(ctx, CreateInput{Name: "API", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"A", "B"} {
		wg.Go(func() {
			_, err := r.Save(ctx, d.Design.ID, SaveInput{ExpectedVersion: 1, Document: strings.Replace(testDocument, "Orders", name, 1), Source: "ui"})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	successes, conflicts := 0, 0
	for err := range results {
		if err == nil {
			successes++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("results %d %d", successes, conflicts)
	}
	_, err = r.Save(ctx, d.Design.ID, SaveInput{ExpectedVersion: 2, Document: `{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{},"components":{"schemas":{"A":{"$ref":"#/components/schemas/Missing"}}}}`, Source: "ui"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing ref: %v", err)
	}
	current, err := r.Detail(ctx, d.Design.ID)
	if err != nil || current.Design.Version != 2 || len(current.Revisions) != 2 {
		t.Fatalf("invalid moved draft: %+v %v", current, err)
	}
}
