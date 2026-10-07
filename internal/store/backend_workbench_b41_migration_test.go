package store_test

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

func TestSource6MigrationOwnershipAndImmutability(t *testing.T) {
	db := store.OpenInlinePayloadSchema(t)
	seedB41StorageParents(t, db.W)
	claim := `INSERT INTO backend_revision_assertions
		(project_id,revision_id,record_type,record_id,repository_id,
		 provider_namespace,external_key,assertion_hash,document)
		VALUES (?,?,?,?,?,'provider','service',?,'{}')`
	hash := strings.Repeat("a", 64)
	_, err := db.W.ExecContext(
		t.Context(),
		claim,
		"project-a",
		"revision-a",
		"node",
		"node-a",
		"repository-a",
		hash,
	)
	if err != nil {
		t.Fatalf("insert owned source assertion: %v", err)
	}

	for _, tc := range []struct {
		name       string
		projectID  string
		revisionID string
		recordType string
		recordID   string
		repoID     string
	}{
		{
			name: "foreign repository", projectID: "project-a", revisionID: "revision-a",
			recordType: "node", recordID: "node-a", repoID: "repository-b",
		},
		{
			name: "foreign revision", projectID: "project-b", revisionID: "revision-a",
			recordType: "node", recordID: "node-a", repoID: "repository-b",
		},
		{
			name: "missing subject", projectID: "project-a", revisionID: "revision-a",
			recordType: "node", recordID: "missing", repoID: "repository-a",
		},
		{
			name: "wrong subject type", projectID: "project-a", revisionID: "revision-a",
			recordType: "edge", recordID: "node-a", repoID: "repository-a",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.W.ExecContext(
				t.Context(),
				claim,
				tc.projectID,
				tc.revisionID,
				tc.recordType,
				tc.recordID,
				tc.repoID,
				hash,
			)
			if err == nil || !strings.Contains(err.Error(), "source assertion ownership mismatch") {
				t.Fatalf("ownership guard returned %v", err)
			}
		})
	}

	resolution := `INSERT INTO backend_revision_assertion_resolutions
		(project_id,revision_id,record_type,record_id,property_key,conflict_hash,document)
		VALUES ('project-a','revision-a','node','node-a','name','conflict',?)`
	selected := `{"select":{"repositoryId":"repository-a","providerNamespace":"provider","assertionHash":"` +
		hash + `"}}`
	if _, err := db.W.ExecContext(t.Context(), resolution, selected); err != nil {
		t.Fatalf("insert resolution selecting the owned claim: %v", err)
	}
	wrong := strings.Replace(selected, hash, strings.Repeat("b", 64), 1)
	if _, err := db.W.ExecContext(t.Context(), resolution, wrong); err == nil ||
		!strings.Contains(err.Error(), "source resolution has no selected claim") {
		t.Fatalf("unbacked resolution guard returned %v", err)
	}

	for _, table := range []string{"backend_revision_assertions", "backend_revision_assertion_resolutions"} {
		for _, query := range []string{"UPDATE " + table + " SET document='{}'", "DELETE FROM " + table} {
			if _, err := db.W.ExecContext(t.Context(), query); err == nil || !strings.Contains(err.Error(), "immutable") {
				t.Fatalf("immutable guard for %q returned %v", query, err)
			}
		}
	}
}

func TestAnnotationStorageAllowsOrphansAndRejectsForeignRevision(t *testing.T) {
	db := store.OpenInlinePayloadSchema(t)
	seedB41StorageParents(t, db.W)
	query := `INSERT INTO backend_annotations
		(id,project_id,record_type,target_id,revision_id,body,author,created_at,updated_at)
		VALUES (?,'project-a','node','missing-target',?,'note','author','created','updated')`
	if _, err := db.W.ExecContext(
		t.Context(),
		query,
		"unbound",
		nil,
	); err != nil {
		t.Fatalf("orphan target must remain valid metadata: %v", err)
	}
	if _, err := db.W.ExecContext(
		t.Context(),
		query,
		"historical",
		"revision-a",
	); err != nil {
		t.Fatalf("owned historical binding: %v", err)
	}
	if _, err := db.W.ExecContext(
		t.Context(),
		query,
		"foreign",
		"revision-b",
	); err == nil ||
		!strings.Contains(err.Error(), "FOREIGN KEY constraint failed") {
		t.Fatalf("foreign revision guard returned %v", err)
	}
}

// These minimal documents exercise relational storage guards. Domain tests own
// the JSON contracts; no model codec is invoked by these direct SQL fixtures.
func seedB41StorageParents(t *testing.T, db *sql.DB) {
	t.Helper()
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = tx.Rollback() }()
	for _, suffix := range []string{"a", "b"} {
		projectID, revisionID := "project-"+suffix, "revision-"+suffix
		_, err := tx.ExecContext(
			t.Context(),
			`INSERT INTO backend_projects
				(id,name,version,current_revision_id,created_at,updated_at) VALUES (?,?,1,?,'created','updated')`,
			projectID,
			projectID,
			revisionID,
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(
			t.Context(),
			`INSERT INTO backend_revisions VALUES (?,?,'{}')`,
			revisionID,
			projectID,
		); err != nil {
			t.Fatal(err)
		}
		_, err = tx.ExecContext(
			t.Context(),
			`INSERT INTO backend_repositories (id,project_id,logical_name) VALUES (?,?,?)`,
			"repository-"+suffix,
			projectID,
			"repository-"+suffix,
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = tx.ExecContext(
			t.Context(),
			`INSERT INTO backend_graph_records
				(project_id,revision_id,record_type,id,kind,document) VALUES (?,?,'node',?,'service','{}')`,
			projectID,
			revisionID,
			"node-"+suffix,
		)
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
