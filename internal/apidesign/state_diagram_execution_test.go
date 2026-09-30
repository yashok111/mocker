package apidesign

import (
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/statediagram"
)

const stateExecutionDiagram = `{"id":"lifecycle","name":"Lifecycle","entity":{"family":"/orders","keyParam":"orderId","stateField":"status"},"initialStateId":"created","states":[{"id":"created","name":"Created","x":0,"y":0,"terminal":false},{"id":"paid","name":"Paid","x":200,"y":0,"terminal":true}],"transitions":[{"id":"pay","name":"Pay","from":"created","to":"paid","binding":{"method":"post","path":"/orders/{orderId}/pay"},"patchJSON":"{}","responseStatus":200}]}`

func stateExecutionDocument(t *testing.T, extension, family, field string) string {
	t.Helper()
	root, err := decodeDocument(managedEntityContract)
	if err != nil {
		t.Fatal(err)
	}
	path, parameter := "/orders/{orderId}/pay", "orderId"
	if family == "/orders/{}/items" {
		path, parameter = "/orders/{orderId}/items/{itemId}/pay", "itemId"
	}
	paths := root["paths"].(map[string]any)
	paths[path] = map[string]any{"parameters": []any{map[string]any{"name": "orderId", "in": "path", "required": true, "schema": map[string]any{"type": "integer"}}}, "post": map[string]any{"responses": map[string]any{"200": map[string]any{"description": "OK", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"$ref": "#/components/schemas/Order"}}}}}}}
	if parameter == "itemId" {
		paths[path].(map[string]any)["parameters"] = append(paths[path].(map[string]any)["parameters"].([]any), map[string]any{"name": "itemId", "in": "path", "required": true, "schema": map[string]any{"type": "integer"}})
	}
	var diagram map[string]any
	if err := jsonx.Unmarshal([]byte(stateExecutionDiagram), &diagram); err != nil {
		t.Fatal(err)
	}
	diagram["entity"] = map[string]any{"family": family, "keyParam": parameter, "stateField": field}
	diagram["transitions"].([]any)[0].(map[string]any)["binding"] = map[string]any{"method": "post", "path": path}
	root[extension] = map[string]any{"formatVersion": 1, "diagrams": []any{diagram}}
	root["x-exact"] = jsonx.Number("9007199254740993")
	raw, err := jsonx.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestStateDiagramExecutionLifecycle(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	ctx := t.Context()
	d, err := r.Create(ctx, CreateInput{Name: "State execution", Document: stateExecutionDocument(t, statediagram.Extension, "/orders", "status"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	status, err := r.StateDiagramExecution(ctx, d.Design.ID)
	if err != nil || status.Diagrams == nil || len(status.Diagrams) != 0 {
		t.Fatalf("initial status: %+v %v", status, err)
	}
	var beforeRevision int64
	if err := db.R.QueryRowContext(ctx, "SELECT revision FROM workspaces WHERE id=?", d.Design.DraftWorkspaceID).Scan(&beforeRevision); err != nil {
		t.Fatal(err)
	}
	applied, err := r.EditStateDiagramExecution(ctx, d.Design.ID, d.Design.Version, "ui", "lifecycle", true)
	if err != nil || applied.Design.Version != 2 {
		t.Fatalf("apply: %+v %v", applied, err)
	}
	var afterRevision int64
	if err := db.R.QueryRowContext(ctx, "SELECT revision FROM workspaces WHERE id=?", d.Design.DraftWorkspaceID).Scan(&afterRevision); err != nil {
		t.Fatal(err)
	}
	if afterRevision != beforeRevision+1 {
		t.Fatalf("revision %d -> %d", beforeRevision, afterRevision)
	}
	status, err = r.StateDiagramExecution(ctx, d.Design.ID)
	if err != nil || len(status.Diagrams) != 1 || status.Diagrams[0].State != "current" || status.Diagrams[0].OperationCount != 1 || status.Diagrams[0].Entity.StateField != "status" || status.Version != 2 || status.RevisionID != applied.Draft.ID {
		t.Fatalf("applied status: %+v %v", status, err)
	}
	if _, err = r.EditStateDiagramExecution(ctx, d.Design.ID, 1, "ui", "lifecycle", true); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale no-op apply: %v", err)
	}
	noop, err := r.EditStateDiagramExecution(ctx, d.Design.ID, 2, "ui", "lifecycle", true)
	if err != nil || noop.Design.Version != 2 || noop.Draft.ID != applied.Draft.ID {
		t.Fatalf("no-op: %+v %v", noop, err)
	}
	authoring, err := r.StateDiagrams(ctx, d.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	diagram := authoring.Diagrams[0]
	// Complete-copy equality includes layout and labels, even though runtime
	// selection would have the same result after this edit.
	diagram.States[0].X++
	diagram.Name = "Changed source"
	changed, err := r.EditStateDiagram(ctx, d.Design.ID, 2, "ui", "lifecycle", "save", &diagram, nil)
	if err != nil {
		t.Fatal(err)
	}
	status, err = r.StateDiagramExecution(ctx, d.Design.ID)
	if err != nil || status.Diagrams[0].State != "outdated" || status.Diagrams[0].Name != "Lifecycle" {
		t.Fatalf("source changed applied copy: %+v %v", status, err)
	}
	reapplied, err := r.EditStateDiagramExecution(ctx, d.Design.ID, changed.Design.Version, "mcp", "lifecycle", true)
	if err != nil {
		t.Fatal(err)
	}
	status, err = r.StateDiagramExecution(ctx, d.Design.ID)
	if err != nil || status.Diagrams[0].State != "current" || status.Diagrams[0].Name != diagram.Name {
		t.Fatalf("reapply: %+v %v", status, err)
	}
	orphaned, err := r.EditStateDiagram(ctx, d.Design.ID, reapplied.Design.Version, "ui", "lifecycle", "delete", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	status, err = r.StateDiagramExecution(ctx, d.Design.ID)
	if err != nil || status.Diagrams[0].State != "missing" {
		t.Fatalf("orphan: %+v %v", status, err)
	}
	if _, err = r.EditStateDiagramExecution(ctx, d.Design.ID, orphaned.Design.Version, "ui", "lifecycle", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("apply missing source: %v", err)
	}
	removed, err := r.EditStateDiagramExecution(ctx, d.Design.ID, orphaned.Design.Version, "ui", "lifecycle", false)
	if err != nil {
		t.Fatal(err)
	}
	noop, err = r.EditStateDiagramExecution(ctx, d.Design.ID, removed.Design.Version, "ui", "lifecycle", false)
	if err != nil || noop.Design.Version != removed.Design.Version || noop.Draft.ID != removed.Draft.ID {
		t.Fatalf("no-op remove: %+v %v", noop, err)
	}
	if _, err = r.EditStateDiagramExecution(ctx, d.Design.ID, orphaned.Design.Version, "ui", "lifecycle", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale no-op remove: %v", err)
	}
	status, err = r.StateDiagramExecution(ctx, d.Design.ID)
	if err != nil || len(status.Diagrams) != 0 {
		t.Fatalf("remove: %+v %v", status, err)
	}
	restored, err := r.Restore(ctx, d.Design.ID, applied.Draft.ID, removed.Design.Version, "restore execution", "ui")
	if err != nil || !strings.Contains(restored.Draft.Document, "x-mocker-state-diagrams-execution") || !strings.Contains(restored.Draft.Document, "9007199254740993") {
		t.Fatalf("restore: %+v %v", restored, err)
	}
}

func TestStateDiagramExecutionAdmissionAndProjectionFailureAreAtomic(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Name: "State execution", Document: stateExecutionDocument(t, statediagram.Extension, "/orders", "status"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name       string
		version    int64
		id, source string
		want       error
	}{
		{"missing version", 0, "lifecycle", "ui", ErrInvalid}, {"bad id", 1, "bad id", "ui", ErrInvalid}, {"missing source", 1, "missing", "ui", ErrNotFound}, {"bad actor", 1, "lifecycle", "other", ErrInvalid},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.EditStateDiagramExecution(t.Context(), d.Design.ID, tc.version, tc.source, tc.id, true); !errors.Is(err, tc.want) {
				t.Fatalf("bad request: %v; want %v", err, tc.want)
			}
		})
	}
	if _, err := db.W.ExecContext(t.Context(), `CREATE TRIGGER reject_state_projection BEFORE INSERT ON resources BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.EditStateDiagramExecution(t.Context(), d.Design.ID, 1, "ui", "lifecycle", true); err == nil {
		t.Fatal("projection failure accepted")
	}
	after, err := r.Detail(t.Context(), d.Design.ID)
	if err != nil || after.Design.Version != 1 || after.Draft.ID != d.Draft.ID || len(after.Revisions) != 1 {
		t.Fatalf("partial write: %+v %v", after, err)
	}
	var count int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM resources").Scan(&count); err != nil || count != 0 {
		t.Fatalf("partial resource projection: count=%d err=%v", count, err)
	}
}

func TestStateDiagramExecutionSelectedAdmissionIgnoresIncompleteAuthoring(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	detail, err := r.Create(t.Context(), CreateInput{Name: "Selected source", Document: stateExecutionDocument(t, statediagram.Extension, "/orders", "status"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	incomplete := statediagram.Diagram{ID: "incomplete", Name: "Incomplete", States: []statediagram.State{}, Transitions: []statediagram.Transition{}}
	detail, err = r.EditStateDiagram(t.Context(), detail.Design.ID, detail.Design.Version, "ui", incomplete.ID, "create", &incomplete, nil)
	if err != nil {
		t.Fatal(err)
	}
	detail, err = r.EditStateDiagramExecution(t.Context(), detail.Design.ID, detail.Design.Version, "ui", "lifecycle", true)
	if err != nil {
		t.Fatalf("unrelated incomplete source blocked apply: %v", err)
	}
	authoring, err := r.StateDiagrams(t.Context(), detail.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	diagram := authoring.Diagrams[0]
	diagram.InitialStateID = ""
	detail, err = r.EditStateDiagram(t.Context(), detail.Design.ID, detail.Design.Version, "ui", diagram.ID, "save", &diagram, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.EditStateDiagramExecution(t.Context(), detail.Design.ID, detail.Design.Version, "ui", diagram.ID, true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid selected source accepted: %v", err)
	}
	after, err := r.Detail(t.Context(), detail.Design.ID)
	if err != nil || after.Design.Version != detail.Design.Version || after.Draft.ID != detail.Draft.ID {
		t.Fatalf("invalid apply changed revision: %+v %v", after, err)
	}
	status, err := r.StateDiagramExecution(t.Context(), detail.Design.ID)
	if err != nil || status.Diagrams[0].State != "outdated" {
		t.Fatalf("invalid source changed applied copy: %+v %v", status, err)
	}
}
