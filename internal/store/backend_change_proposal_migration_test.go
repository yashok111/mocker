package store_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/testkit"
)

func TestChangeProposalStorageOwnedBaselineAndRequiredBatch(t *testing.T) {
	for _, tc := range []struct {
		name         string
		baseRevision string
		includeBatch bool
		wantError    bool
	}{
		{name: "owned baseline", baseRevision: "revision-a", includeBatch: true},
		{name: "foreign baseline", baseRevision: "revision-b", includeBatch: true, wantError: true},
		{name: "missing immutable batch", baseRevision: "revision-a", wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := testkit.NewDB(t)
			seedB41StorageParents(t, db.W)
			err := insertStorageChangeProposal(t, db.W, tc.baseRevision, tc.includeBatch)
			if tc.wantError {
				if err == nil || !strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
					t.Fatalf("invalid draft must fail ownership/ledger guard: %v", err)
				}
				var count int
				if err := db.R.QueryRowContext(t.Context(), "SELECT count(*) FROM backend_change_proposals").Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("failed draft transaction left %d proposals", count)
				}
			} else if err != nil {
				t.Fatalf("owned draft and batch must commit: %v", err)
			}
		})
	}
}

func TestChangeProposalStorageCommandLedgerAndImmutability(t *testing.T) {
	db := testkit.NewDB(t)
	seedB41StorageParents(t, db.W)
	if err := insertStorageChangeProposal(t, db.W, "revision-a", true); err != nil {
		t.Fatal(err)
	}
	command := `INSERT INTO backend_change_proposal_commands
		(proposal_id,command_id,revision_id,position,document) VALUES ('proposal','command','draft',?,?)`
	for _, tc := range []struct {
		name     string
		position int
		document string
	}{
		{name: "wrong position", position: 1, document: `{"commandId":"command","kind":"rename_node"}`},
		{name: "changed payload", document: `{"commandId":"command","kind":"remove_node"}`},
		{name: "wrong command identity", document: `{"commandId":"other","kind":"rename_node"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.W.ExecContext(t.Context(), command, tc.position, tc.document)
			if err == nil || !strings.Contains(err.Error(), "change command must match immutable batch") {
				t.Fatalf("mismatched ledger command accepted: %v", err)
			}
		})
	}
	if _, err := db.W.ExecContext(t.Context(), command, 0, `{"commandId":"command","kind":"rename_node"}`); err != nil {
		t.Fatalf("matching command must insert: %v", err)
	}
	if _, err := db.W.ExecContext(t.Context(), command, 0, `{"commandId":"command","kind":"rename_node"}`); err == nil {
		t.Fatal("permanent command identity was allocated twice")
	}
	for _, table := range []string{
		"backend_change_proposal_revisions", "backend_change_proposal_events",
		"backend_change_proposal_identities", "backend_change_proposal_batches",
		"backend_change_proposal_commands",
	} {
		for _, query := range []string{"UPDATE " + table + " SET document='{}'", "DELETE FROM " + table} {
			if _, err := db.W.ExecContext(t.Context(), query); err == nil || !strings.Contains(err.Error(), "immutable") {
				t.Fatalf("immutable guard for %q returned %v", query, err)
			}
		}
	}
}

// Storage fixtures deliberately avoid domain codecs: these are guards against
// malformed direct writes, while command semantics are tested by backendmodel.
func insertStorageChangeProposal(t *testing.T, db *sql.DB, baseRevision string, includeBatch bool) error {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_change_proposals
		(id,project_id,version,name,status,current_draft_revision_id,current_draft_hash,created_at,updated_at)
		VALUES ('proposal','project-a',1,'Proposal','draft','draft','hash','created','updated')`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_change_proposal_revisions
		(id,project_id,proposal_id,base_revision_id,document) VALUES ('draft','project-a','proposal',?,'{}')`, baseRevision); err != nil {
		return err
	}
	if includeBatch {
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_change_proposal_batches
			(project_id,proposal_id,revision_id,action,commands,commands_hash,document)
			VALUES ('project-a','proposal','draft','apply','[{"commandId":"command","kind":"rename_node"}]','hash','{}')`); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_change_proposal_events
		(project_id,proposal_id,revision_id,version,document) VALUES ('project-a','proposal','draft',1,'{}')`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_change_proposal_identities
		(project_id,proposal_id,id,record_type,kind,first_revision_id,document)
		VALUES ('project-a','proposal','node','node','service','draft','{}')`); err != nil {
		return err
	}
	return tx.Commit()
}
