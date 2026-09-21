package apidesign

import (
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestCreateTxRollsBackWithOuterAggregate(t *testing.T) {
	r, db := testRepo(t)
	failure := errors.New("scenario revision failed")
	var createdID int64
	err := db.Write(t.Context(), func(tx *sql.Tx) error {
		created, err := r.CreateTx(t.Context(), tx, CreateInput{Name: "Atomic", Document: testDocument, Source: "mcp"})
		if err != nil {
			return err
		}
		createdID = created.Design.ID
		if _, err = r.DetailTx(t.Context(), tx, createdID); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) || createdID == 0 {
		t.Fatalf("outer transaction did not run: id=%d, err=%v", createdID, err)
	}
	for _, table := range []string{"api_designs", "api_design_revisions", "api_design_workspaces", "workspaces", "specs"} {
		var count int
		if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s retained %d rows after rollback", table, count)
		}
	}
}

func TestSaveTxRollsBackRevisionAndMockProjection(t *testing.T) {
	r, db := testRepo(t)
	detail, err := r.Create(t.Context(), CreateInput{Name: "Atomic", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	var initialSpecID int64
	if err := db.R.QueryRowContext(t.Context(), "SELECT spec_id FROM workspaces WHERE id=?", detail.Design.DraftWorkspaceID).Scan(&initialSpecID); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("scenario CAS failed")
	err = db.Write(t.Context(), func(tx *sql.Tx) error {
		updated, err := r.SaveTx(t.Context(), tx, detail.Design.ID, SaveInput{
			ExpectedVersion: 1,
			Document:        strings.Replace(detail.Draft.Document, "Orders", "Updated", 1),
			Source:          "mcp",
		})
		if err != nil {
			return err
		}
		if updated.Design.Version != 2 {
			t.Fatalf("new revision missing inside transaction: %+v", updated.Design)
		}
		if _, err := r.RevisionTx(t.Context(), tx, detail.Design.ID, updated.Draft.ID); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatal(err)
	}
	current, err := r.Detail(t.Context(), detail.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Design.Version != 1 || len(current.Revisions) != 1 || current.Draft.Document != detail.Draft.Document {
		t.Fatalf("outer failure retained API change: %+v", current)
	}
	var specID int64
	if err := db.R.QueryRowContext(t.Context(), "SELECT spec_id FROM workspaces WHERE id=?", detail.Design.DraftWorkspaceID).Scan(&specID); err != nil {
		t.Fatal(err)
	}
	if specID != initialSpecID {
		t.Fatalf("draft mock changed despite rollback: %d != %d", specID, initialSpecID)
	}
}
