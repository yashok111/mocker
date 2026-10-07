package store_test

import (
	"database/sql"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

func TestAPIArtifactContextImmutableAndIndependentOfAPIAvailability(t *testing.T) {
	db := store.OpenInlinePayloadSchema(t)
	err := db.Write(t.Context(), func(tx *sql.Tx) error {
		for _, statement := range []string{
			`INSERT INTO backend_projects(id,name,version,current_revision_id,created_at,updated_at) VALUES('p','Project',1,'r','now','now')`,
			`INSERT INTO backend_revisions(id,project_id,document) VALUES('r','p','{"legacy":true}')`,
			`INSERT INTO backend_revision_api_artifacts(revision_id,source_content_hash,source_semantic_hash,document) VALUES('r','content','semantic','{"bindings":[{"missingArtifact":"999"}]}')`,
		} {
			if _, err := tx.ExecContext(t.Context(), statement); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`UPDATE backend_revision_api_artifacts SET document='{}' WHERE revision_id='r'`,
		`DELETE FROM backend_revision_api_artifacts WHERE revision_id='r'`,
		`DELETE FROM backend_revisions WHERE id='r'`,
		`INSERT INTO backend_revision_api_artifacts(revision_id,source_content_hash,source_semantic_hash,document) VALUES('foreign','content','semantic','{}')`,
	} {
		if _, err := db.W.ExecContext(t.Context(), statement); err == nil {
			t.Fatalf("mutable/foreign context accepted: %s", statement)
		}
	}
	var revision, context string
	if err := db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_revisions WHERE id='r'`).Scan(&revision); err != nil || revision != `{"legacy":true}` {
		t.Fatalf("historical revision rewritten %q %v", revision, err)
	}
	if err := db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_revision_api_artifacts WHERE revision_id='r'`).Scan(&context); err != nil || context != `{"bindings":[{"missingArtifact":"999"}]}` {
		t.Fatalf("context changed %q %v", context, err)
	}
}
