package apidesign

import (
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/responserules"
)

const executionTestRule = `{"id":"r","name":"Rule","binding":{"method":"GET","path":"/orders"},"nodes":[{"id":"s","type":"start","name":"Start","x":0,"y":0},{"id":"f","type":"fallback","name":"Fallback","x":200,"y":0}],"edges":[{"id":"e","from":"s","port":"next","to":"f"}]}`

func executionTestDocument() string {
	return strings.TrimSuffix(testDocument, "}") + `,"x-mocker-response-rules":{"formatVersion":1,"rules":[` + executionTestRule + `]}}`
}

func TestResponseRuleExecutionLifecycle(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	ctx := t.Context()
	d, err := r.Create(ctx, CreateInput{Name: "Execution", Document: executionTestDocument(), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	status, err := r.ResponseRuleExecution(ctx, d.Design.ID)
	if err != nil || status.Rules == nil || len(status.Rules) != 0 {
		t.Fatalf("initial status: %+v %v", status, err)
	}
	var beforeRevision int64
	if err := db.R.QueryRowContext(ctx, "SELECT revision FROM workspaces WHERE id=?", d.Design.DraftWorkspaceID).Scan(&beforeRevision); err != nil {
		t.Fatal(err)
	}
	applied, err := r.EditResponseRuleExecution(ctx, d.Design.ID, 1, "ui", "r", true)
	if err != nil || applied.Design.Version != 2 {
		t.Fatalf("apply: %+v %v", applied, err)
	}
	var afterRevision int64
	if err := db.R.QueryRowContext(ctx, "SELECT revision FROM workspaces WHERE id=?", d.Design.DraftWorkspaceID).Scan(&afterRevision); err != nil {
		t.Fatal(err)
	}
	if afterRevision != beforeRevision+1 {
		t.Fatalf("revision: %d -> %d", beforeRevision, afterRevision)
	}
	status, err = r.ResponseRuleExecution(ctx, d.Design.ID)
	if err != nil || len(status.Rules) != 1 || status.Rules[0].State != "current" || status.Version != 2 || status.RevisionID != applied.Draft.ID {
		t.Fatalf("applied status: %+v %v", status, err)
	}
	if _, err = r.EditResponseRuleExecution(ctx, d.Design.ID, 1, "ui", "r", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale remove: %v", err)
	}
	unchanged, err := r.EditResponseRuleExecution(ctx, d.Design.ID, 2, "ui", "r", true)
	if err != nil || unchanged.Design.Version != 2 || unchanged.Draft.ID != applied.Draft.ID {
		t.Fatalf("identical apply: %+v %v", unchanged, err)
	}
	var rule responserules.Rule
	if err = jsonx.Unmarshal([]byte(executionTestRule), &rule); err != nil {
		t.Fatal(err)
	}
	rule.Name = "Changed source"
	changed, err := r.EditResponseRule(ctx, d.Design.ID, 2, "ui", "r", "save", &rule, nil)
	if err != nil {
		t.Fatal(err)
	}
	status, err = r.ResponseRuleExecution(ctx, d.Design.ID)
	if err != nil || status.Rules[0].State != "outdated" || status.Rules[0].Name != "Rule" {
		t.Fatalf("copy changed with source: %+v %v", status, err)
	}
	reapplied, err := r.EditResponseRuleExecution(ctx, d.Design.ID, changed.Design.Version, "mcp", "r", true)
	if err != nil {
		t.Fatal(err)
	}
	status, err = r.ResponseRuleExecution(ctx, d.Design.ID)
	if err != nil || status.Rules[0].State != "current" || status.Rules[0].Name != rule.Name {
		t.Fatalf("reapply: %+v %v", status, err)
	}
	orphaned, err := r.EditResponseRule(ctx, d.Design.ID, reapplied.Design.Version, "ui", "r", "delete", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	status, err = r.ResponseRuleExecution(ctx, d.Design.ID)
	if err != nil || status.Rules[0].State != "missing" {
		t.Fatalf("orphan: %+v %v", status, err)
	}
	removed, err := r.EditResponseRuleExecution(ctx, d.Design.ID, orphaned.Design.Version, "ui", "r", false)
	if err != nil {
		t.Fatal(err)
	}
	status, err = r.ResponseRuleExecution(ctx, d.Design.ID)
	if err != nil || len(status.Rules) != 0 {
		t.Fatalf("remove: %+v %v", status, err)
	}
	noop, err := r.EditResponseRuleExecution(ctx, d.Design.ID, removed.Design.Version, "ui", "r", false)
	if err != nil || noop.Design.Version != removed.Design.Version {
		t.Fatalf("missing remove: %+v %v", noop, err)
	}
	if _, err := r.EditResponseRuleExecution(ctx, d.Design.ID, orphaned.Design.Version, "ui", "r", false); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale no-op: %v", err)
	}
	restored, err := r.Restore(ctx, d.Design.ID, applied.Draft.ID, removed.Design.Version, "restore execution", "ui")
	if err != nil || !strings.Contains(restored.Draft.Document, "x-mocker-response-rules-execution") || !strings.Contains(restored.Draft.Document, "9007199254740993") {
		t.Fatalf("restore: %+v %v", restored, err)
	}
}

func TestResponseRuleExecutionAdmissionIsAtomic(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Name: "Execution", Document: executionTestDocument(), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		version int64
		id      string
		want    error
	}{{0, "r", ErrInvalid}, {1, "bad id", ErrInvalid}, {1, "missing", ErrNotFound}} {
		if _, err := r.EditResponseRuleExecution(t.Context(), d.Design.ID, tc.version, "ui", tc.id, true); !errors.Is(err, tc.want) {
			t.Fatalf("bad request: %v", err)
		}
	}
	invalid := strings.TrimSuffix(d.Draft.Document, "}") + `,"x-mocker-response-rules-execution":{"formatVersion":1,"rules":[{"id":"bad","name":"Incomplete","nodes":[],"edges":[]}]}}`
	if _, err = r.Save(t.Context(), d.Design.ID, SaveInput{ExpectedVersion: 1, Document: invalid, Source: "ui"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid source save: %v", err)
	}
	if _, err = db.W.ExecContext(t.Context(), `CREATE TRIGGER reject_execution_projection BEFORE UPDATE OF spec_id ON workspaces BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = r.EditResponseRuleExecution(t.Context(), d.Design.ID, 1, "ui", "r", true); err == nil {
		t.Fatal("projection failure accepted")
	}
	after, err := r.Detail(t.Context(), d.Design.ID)
	if err != nil || after.Design.Version != 1 || after.Draft.ID != d.Draft.ID || len(after.Revisions) != 1 {
		t.Fatalf("partial write: %+v %v", after, err)
	}
}

func TestResponseRuleExecutionApplyValidatesSelectedSourceBeforeNoop(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Name: "Execution", Document: executionTestDocument(), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := r.EditResponseRuleExecution(t.Context(), d.Design.ID, 1, "ui", "r", true)
	if err != nil {
		t.Fatal(err)
	}
	var duplicate responserules.Rule
	if err := jsonx.Unmarshal([]byte(executionTestRule), &duplicate); err != nil {
		t.Fatal(err)
	}
	duplicate.ID = "duplicate"
	changed, err := r.EditResponseRule(t.Context(), d.Design.ID, applied.Design.Version, "ui", duplicate.ID, "create", &duplicate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.EditResponseRuleExecution(t.Context(), d.Design.ID, changed.Design.Version, "ui", "r", true); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid authoring no-op apply: %v", err)
	}
	after, err := r.Detail(t.Context(), d.Design.ID)
	if err != nil || after.Design.Version != changed.Design.Version {
		t.Fatalf("invalid apply mutated: %+v %v", after, err)
	}
}

func TestResponseRuleExecutionContractEditsRequireRemovingCopy(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Name: "Execution", Document: executionTestDocument(), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := r.EditResponseRuleExecution(t.Context(), d.Design.ID, 1, "ui", "r", true)
	if err != nil {
		t.Fatal(err)
	}
	root, err := decodeDocument(applied.Draft.Document)
	if err != nil {
		t.Fatal(err)
	}
	root["paths"] = map[string]any{}
	raw, err := jsonx.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Save(t.Context(), d.Design.ID, SaveInput{ExpectedVersion: 2, Document: string(raw), Source: "ui"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("missing operation admitted: %v", err)
	}
	delete(root, responserules.ExecutionExtension)
	raw, err = jsonx.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Save(t.Context(), d.Design.ID, SaveInput{ExpectedVersion: 2, Document: string(raw), Source: "ui"}); err != nil {
		t.Fatalf("atomic operation/copy removal rejected: %v", err)
	}
}
