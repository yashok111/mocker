package designscenario

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestFoundationOwnerTxRollback(t *testing.T) {
	r := newTestRepo(t)
	injected := errors.New("after owner write")
	err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		if _, err := r.CreateTx(t.Context(), tx, CreateInput{Document: validDocument("rolled back"), Source: "mcp"}); err != nil {
			return err
		}
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatal(err)
	}
	rows, err := r.List(t.Context())
	if err != nil || len(rows) != 0 {
		t.Fatal(rows, err)
	}
	api, err := r.designs.Create(t.Context(), apidesign.CreateInput{Name: "API", Document: apiDocument("orders"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	doc := validDocument("Linked")
	doc.Contracts = []Contract{{ID: "api", Name: "API", Mode: "linked", Source: &ContractSource{DesignID: api.Design.ID, RevisionID: api.Draft.ID, Version: api.Draft.Version}, Document: jsonx.RawMessage(api.Draft.Document)}}
	scenario, err := r.Create(t.Context(), CreateInput{Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	var beforeSpec, beforeRevision int64
	if err := r.db.R.QueryRow("SELECT spec_id,revision FROM workspaces WHERE id=?", api.Design.DraftWorkspaceID).Scan(&beforeSpec, &beforeRevision); err != nil {
		t.Fatal(err)
	}
	doc.Contracts[0].Document = jsonx.RawMessage(apiDocument("changed"))
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		saved, err := r.SaveTx(t.Context(), tx, scenario.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: doc, Source: "mcp"})
		if err != nil {
			return err
		}
		if saved.Draft.Document.Contracts[0].Source.RevisionID == api.Draft.ID {
			t.Fatal("linked revision not substituted")
		}
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatal(err)
	}
	after, err := r.designs.Detail(t.Context(), api.Design.ID)
	if err != nil || !reflect.DeepEqual(api, after) {
		t.Fatal("API escaped rollback", err)
	}
	var afterSpec, afterRevision int64
	if err := r.db.R.QueryRow("SELECT spec_id,revision FROM workspaces WHERE id=?", api.Design.DraftWorkspaceID).Scan(&afterSpec, &afterRevision); err != nil || beforeSpec != afterSpec || beforeRevision != afterRevision {
		t.Fatal("draft mock escaped rollback", err)
	}
	s, err := r.Detail(t.Context(), scenario.Scenario.ID)
	if err != nil || !reflect.DeepEqual(scenario, s) {
		t.Fatal("scenario escaped rollback", err)
	}
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := r.SaveTx(t.Context(), tx, scenario.Scenario.ID, SaveInput{ExpectedVersion: 99, Document: doc, Source: "mcp"})
		return err
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatal("missing CAS", err)
	}
	err = r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := r.CreateTx(t.Context(), tx, CreateInput{Document: Document{FormatVersion: 99}, Source: "mcp"})
		return err
	})
	if err == nil {
		t.Fatal("missing owner validation")
	}
}
