package apidesign

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

const operationIdentityKey = "x-mocker-canvas-operation-id"

func operationIdentity(t *testing.T, document, path, method string) string {
	t.Helper()
	root, err := decodeDocument(document)
	if err != nil {
		t.Fatal(err)
	}
	paths := root["paths"].(map[string]any)
	operation := paths[path].(map[string]any)[method].(map[string]any)
	id, _ := operation[operationIdentityKey].(string)
	return id
}

func TestOperationIdentitySurvivesRenameAndUnannotatedSave(t *testing.T) {
	r, _ := testRepo(t)
	detail, err := r.Create(t.Context(), CreateInput{Name: "Stable", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	id := operationIdentity(t, detail.Draft.Document, "/orders", "get")
	if id == "" {
		t.Fatal("created API operation has no stable identity")
	}
	unchanged, err := r.Save(t.Context(), detail.Design.ID, SaveInput{ExpectedVersion: 1, Document: testDocument, Source: "mcp"})
	if err != nil || unchanged.Design.Version != 1 {
		t.Fatalf("unannotated no-op changed history: %+v, %v", unchanged, err)
	}
	renamed := strings.Replace(detail.Draft.Document, `"/orders"`, `"/checkout"`, 1)
	renamed = strings.Replace(renamed, `"get":`, `"post":`, 1)
	saved, err := r.Save(t.Context(), detail.Design.ID, SaveInput{ExpectedVersion: 1, Document: renamed, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if got := operationIdentity(t, saved.Draft.Document, "/checkout", "post"); got != id {
		t.Fatalf("rename changed identity: %q -> %q", id, got)
	}
	old, err := r.Revision(t.Context(), detail.Design.ID, detail.Draft.ID)
	if err != nil || old.Document != detail.Draft.Document {
		t.Fatalf("historical revision changed: %v", err)
	}
}

func TestDuplicateOperationIdentityRejectsAPIWithoutPartialRows(t *testing.T) {
	r, _ := testRepo(t)
	document := `{"openapi":"3.1.0","info":{"title":"A","version":"1"},"paths":{"/a":{"get":{"x-mocker-canvas-operation-id":"same","responses":{"200":{"description":"OK"}}}},"/b":{"post":{"x-mocker-canvas-operation-id":"same","responses":{"200":{"description":"OK"}}}}}}`
	diagnostics, validationErr := r.Validate(document)
	if validationErr != nil || len(diagnostics) == 0 {
		t.Fatalf("validation accepted ambiguous operation identities: %v, %v", diagnostics, validationErr)
	}
	_, err := r.Create(t.Context(), CreateInput{Name: "Duplicate", Document: document, Source: "mcp"})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("duplicate stable identities accepted: %v", err)
	}
	rows, err := r.List(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatalf("invalid create left API rows: %v, %v", rows, err)
	}
}

func TestLegacyOperationIdentityIsStableWithoutRewritingHistory(t *testing.T) {
	r, db := testRepo(t)
	detail, err := r.Create(t.Context(), CreateInput{Name: "Legacy", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an immutable revision authored before operation keys existed.
	err = db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), "UPDATE api_design_revisions SET document=? WHERE id=?", testDocument, detail.Draft.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.Revision(t.Context(), detail.Design.ID, detail.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Revision(t.Context(), detail.Design.ID, detail.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	id := operationIdentity(t, first.Document, "/orders", "get")
	if id == "" || operationIdentity(t, second.Document, "/orders", "get") != id {
		t.Fatal("legacy identity is missing or unstable")
	}
	var stored string
	if err := db.R.QueryRowContext(t.Context(), "SELECT document FROM api_design_revisions WHERE id=?", detail.Draft.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != testDocument {
		t.Fatal("read rewrote immutable history")
	}
}
