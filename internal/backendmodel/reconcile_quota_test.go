package backendmodel

import (
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"testing"
	"uuid"
)

func TestReconcileQuotaIncludesRetainedBaseRecords(t *testing.T) {
	r, p, old, _ := committedBase(t)
	// The base already contains one node. Fill the remainder directly to keep
	// this limit regression independent of batch count and upload throughput.
	err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		insert, err := tx.PrepareContext(t.Context(), `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,document) VALUES(?,?,'node',?,?)`)
		if err != nil {
			return err
		}
		defer insert.Close()
		for i := 1; i < MaxRevisionNodes; i++ {
			node := Node{ID: uuid.NewV7().String(), ExternalKey: fmt.Sprintf("retained-%d", i), Kind: "unresolved_target", Name: "Retained fixture", Attributes: map[string]jsontext.Value{"expectedKind": jsontext.Value(`"handler"`), "reason": jsontext.Value(`"unavailable"`), "searchScope": jsontext.Value(`"fixture"`)}, EvidenceIDs: []string{}}
			doc, err := json.Marshal(node)
			if err != nil {
				return err
			}
			if _, err := insert.ExecContext(t.Context(), p.ID, p.CurrentRevisionID, node.ID, string(doc)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	session := beginRepeat(t, r, p, old)
	command := ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "new-target", Kind: "unresolved_target", Name: "New target", Attributes: map[string]jsontext.Value{"expectedKind": jsontext.Value(`"handler"`), "reason": jsontext.Value(`"unavailable"`), "searchScope": jsontext.Value(`"fixture"`)}, EvidenceKeys: []string{}}}
	batch := sendCommands(t, r, p, session, session.Version, "new-record", command)
	_, err = r.PreviewImport(t.Context(), p.ID, session.ID, PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	assertFault(t, err, "backend_import_limit")
	status, err := r.Import(t.Context(), p.ID, session.ID, ListInput{})
	if err != nil || status.Session.Version != batch.AcceptedVersion || status.Preview != nil || status.Session.CandidateHash != nil {
		t.Fatalf("limit failure changed session: %+v %v", status, err)
	}
	project, err := r.Get(t.Context(), p.ID)
	if err != nil || project.Version != p.Version || project.CurrentRevisionID != p.CurrentRevisionID {
		t.Fatalf("limit failure moved head: %+v %v", project, err)
	}
}
