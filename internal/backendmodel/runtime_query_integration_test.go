package backendmodel

import (
	"database/sql"
	"encoding/json/v2"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

// The graph and expected path are independently specified in the query fixture.
// These inserts exercise the immutable read repository without depending on the
// runtime import encoder, whose validation is covered by separate domain tests.
func runtimeQueryPersist(t *testing.T, r *Repo, s *RevisionState) {
	t.Helper()
	tx, err := r.db.W.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	b, err := json.Marshal(s.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_revisions(id,project_id,document) VALUES(?,?,?)`, s.Revision.ID, s.Revision.ProjectID, string(b)); err != nil {
		t.Fatal(err)
	}
	for _, n := range s.Nodes {
		b, err := json.Marshal(n)
		if err != nil {
			t.Fatal(err)
		}
		parent := ""
		if n.ParentID != nil {
			parent = *n.ParentID
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,kind,name,parent_id,document) VALUES(?,?,'node',?,?,?,?,?)`, s.Revision.ProjectID, s.Revision.ID, n.ID, n.Kind, n.Name, parent, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range s.Edges {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,kind,from_id,to_id,document) VALUES(?,?,'edge',?,?,?,?,?)`, s.Revision.ProjectID, s.Revision.ID, e.ID, e.Kind, e.From, e.To, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	for _, e := range s.Evidence {
		b, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,subject_id,document) VALUES(?,?,'evidence',?,?,?)`, s.Revision.ProjectID, s.Revision.ID, e.ID, e.SubjectID, string(b)); err != nil {
			t.Fatal(err)
		}
	}
	coverage := RevisionCoverage{Coverage: s.Revision.Coverage, Snapshots: s.Sources, Inventory: s.Inventory, ReconciliationGaps: []string{"Pinned imported scope"}}
	b, err = json.Marshal(coverage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(t.Context(), `INSERT INTO backend_revision_sources(revision_id,document) VALUES(?,?)`, s.Revision.ID, string(b)); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func runtimeQueryAddRelationalFacets(t *testing.T, s *RevisionState) {
	t.Helper()
	known := func(value any) map[string]any { return map[string]any{"status": "known", "value": value} }
	for idx := range s.Nodes {
		n := &s.Nodes[idx]
		if !strings.Contains("|table|column|datastore|", "|"+n.Kind+"|") {
			continue
		}
		facet := map[string]any{"sourceKind": "sql", "dialect": "postgresql", "analysisStatus": "complete", "gaps": []string{}, "evidenceIds": n.EvidenceIDs, "freshness": map[string]any{"status": "current", "confirmedSnapshotId": runtimeQueryID(903), "reasons": []string{}}, "sourceSnapshotId": runtimeQueryID(903)}
		switch n.Kind {
		case "column":
			facet["nativeType"] = known("INTEGER")
			facet["typeFamily"] = known("integer")
			facet["nullable"] = known(false)
			facet["defaultExpression"] = known(nil)
			facet["generatedExpression"] = known(nil)
			facet["identity"] = known(nil)
			facet["ordinal"] = known(1)
		case "table":
			facet["qualifiedName"] = "public.orders"
			facet["nativeDefinition"] = "CREATE TABLE public.orders (id INTEGER NOT NULL);"
			facet["columnsStatus"] = "complete"
			facet["constraintsStatus"] = "complete"
		case "datastore":
			facet["databaseName"] = "orders"
			facet["qualifiedName"] = "orders"
			facet["nativeDefinition"] = nil
		}
		attrs := map[string]any{"facets": map[string]any{"sql": facet}}
		if n.Kind == "datastore" {
			attrs = map[string]any{"relational": attrs}
		}
		n.Attributes = runtimeQueryAttrs(t, attrs)
	}
}

func runtimeQueryDBSnapshot(t *testing.T, db *sql.DB) [][]any {
	t.Helper()
	rows, err := db.QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' AND name LIKE 'backend_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	snapshot := [][]any{}
	for _, name := range names {
		rows, err := db.QueryContext(t.Context(), `SELECT * FROM "`+name+`" ORDER BY rowid`)
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			for i, v := range values {
				if b, ok := v.([]byte); ok {
					values[i] = string(b)
				}
			}
			snapshot = append(snapshot, append([]any{name}, values...))
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return snapshot
}

func TestRuntimeReadPurityPinsErrorsAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-query.db")
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(t.Context(), slog.Default()); err != nil {
		t.Fatal(err)
	}
	r := NewRepo(db)
	project := createProject(t, r, "runtime-query-project")
	other := createProject(t, r, "runtime-query-other")
	s := runtimeQueryFixture(t)
	runtimeQueryAddRelationalFacets(t, s)
	s.Revision.ProjectID = project.ID
	s.Revision.ParentRevisionID = new(project.CurrentRevisionID)
	runtimeQueryPersist(t, r, s)
	before := runtimeQueryDBSnapshot(t, db.R)
	queries := []FlowQueryInput{
		{RevisionID: s.Revision.ID, View: "entrypoints"},
		{RevisionID: s.Revision.ID, View: "steps", FlowID: runtimeQueryID(3), Limit: 2},
		{RevisionID: s.Revision.ID, View: "transitions", FlowID: runtimeQueryID(3), Limit: 2},
		{RevisionID: s.Revision.ID, View: "accesses", EntrypointID: runtimeQueryID(1)},
		{RevisionID: s.Revision.ID, View: "accesses", DataNodeID: runtimeQueryID(11)},
	}
	var pinned *FlowPage
	for _, in := range queries {
		page, err := r.QueryFlow(t.Context(), project.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		if page.RevisionID != s.Revision.ID || page.SemanticHash != s.Revision.SemanticHash || !reflect.DeepEqual(page.Coverage.ReconciliationGaps, []string{"Pinned imported scope"}) {
			t.Fatal("read selected project head instead of source pin", page)
		}
		if !reflect.DeepEqual(before, runtimeQueryDBSnapshot(t, db.R)) {
			t.Fatal("query changed durable records")
		}
		if in.EntrypointID != "" {
			pinned = page
			for _, item := range page.AccessItems {
				for _, id := range item.PathNodeIDs {
					node, err := r.ReadNode(t.Context(), project.ID, BackendReadTarget{RevisionID: s.Revision.ID}, id)
					if err != nil || node.SourceRecord == nil || node.SourceRecord.ID != id {
						t.Fatalf("public pinned node %s: %+v %v", id, node, err)
					}
				}
				for _, id := range item.EvidenceIDs {
					evidence, err := r.Evidence(t.Context(), project.ID, s.Revision.ID, EvidenceQueryInput{EvidenceID: id})
					if err != nil || len(evidence.Items) != 1 || evidence.Items[0].ID != id {
						t.Fatalf("public pinned evidence %s: %+v %v", id, evidence, err)
					}
				}
				for _, id := range item.PathEdgeIDs {
					graph, err := r.QueryGraph(t.Context(), project.ID, GraphQueryInput{RevisionID: s.Revision.ID, RecordType: "edges", ID: id})
					if err != nil || len(graph.Edges) != 1 || graph.Edges[0].ID != id {
						t.Fatalf("public pinned edge %s: %+v %v", id, graph, err)
					}
				}
			}
		}
	}
	for _, tc := range []struct {
		pid    string
		in     FlowQueryInput
		code   string
		status int
	}{
		{other.ID, queries[0], "backend_not_found", 404},
		{project.ID, FlowQueryInput{View: "entrypoints"}, "backend_not_found", 404},
		{project.ID, FlowQueryInput{RevisionID: project.CurrentRevisionID, View: "entrypoints"}, "backend_unsupported_scope", 422},
		{project.ID, FlowQueryInput{RevisionID: s.Revision.ID, View: "accesses", EntrypointID: runtimeQueryID(12)}, "backend_invalid", 400},
		{project.ID, FlowQueryInput{RevisionID: s.Revision.ID, View: "steps", FlowID: runtimeQueryID(99999)}, "backend_not_found", 404},
		{project.ID, FlowQueryInput{RevisionID: s.Revision.ID, View: "entrypoints", Cursor: "bad"}, "backend_invalid", 400},
	} {
		_, err := r.QueryFlow(t.Context(), tc.pid, tc.in)
		if f := assertFault(t, err, tc.code); f.Status != tc.status {
			t.Fatal("wrong scope status", f)
		}
		if !reflect.DeepEqual(before, runtimeQueryDBSnapshot(t, db.R)) {
			t.Fatal("failed query changed durable records")
		}
	}
	// Advancing the source head cannot change old immutable query output.
	newState := runtimeQueryFixture(t)
	runtimeQueryAddRelationalFacets(t, newState)
	newState.Revision.ID = runtimeQueryID(902)
	newState.Revision.ProjectID = project.ID
	newState.Revision.SemanticHash = strings.Repeat("b", 64)
	newState.Revision.ParentRevisionID = new(s.Revision.ID)
	newState.Nodes[0].Name = "New source name"
	runtimeQueryPersist(t, r, newState)
	if _, err := db.W.ExecContext(t.Context(), `UPDATE backend_projects SET current_revision_id=?,version=version+1 WHERE id=?`, newState.Revision.ID, project.ID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	r = NewRepo(db)
	page, err := r.QueryFlow(t.Context(), project.ID, queries[3])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(pinned, page) {
		t.Fatal("head advance/restart changed old pin")
	}
}
