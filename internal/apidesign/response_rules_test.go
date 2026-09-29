package apidesign

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/responserules"
)

func TestResponseRulePersistenceCASAndAtomicCommands(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	ctx := t.Context()
	d, err := r.Create(ctx, CreateInput{Name: "Rules", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	rule := responserules.Rule{ID: "r", Name: "Rule", Nodes: []responserules.Node{}, Edges: []responserules.Edge{}}
	next, err := r.EditResponseRule(ctx, d.Design.ID, 1, "mcp", "r", "create", &rule, nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.Design.Version != 2 || next.Draft.Source != "mcp" || !strings.Contains(next.Draft.Document, "9007199254740993") || !strings.Contains(next.Draft.Document, "x-retained") {
		t.Fatalf("contract lost: %+v", next)
	}
	if _, err := r.EditResponseRule(ctx, d.Design.ID, 1, "ui", "r", "delete", nil, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale: %v", err)
	}
	if _, err := r.EditResponseRule(ctx, d.Design.ID, 0, "ui", "r", "delete", nil, nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("version missing: %v", err)
	}
	changed := "changed"
	_, err = r.EditResponseRule(ctx, d.Design.ID, 2, "ui", "r", "commands", nil, []responserules.Command{{Type: "set_rule", Name: &changed}, {Type: "remove_node", NodeID: "missing"}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("failed commands: %v", err)
	}
	list, err := r.ResponseRules(ctx, d.Design.ID)
	if err != nil || list.Version != 2 || len(list.Rules) != 1 || list.Rules[0].Name != "Rule" {
		t.Fatalf("partial write: %+v %v", list, err)
	}
	if _, err := r.Save(ctx, d.Design.ID, SaveInput{ExpectedVersion: 2, Document: strings.Replace(next.Draft.Document, `"formatVersion": 1`, `"formatVersion": 999`, 1), Source: "ui"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("source admission bypass: %v", err)
	}
	rule.Name = "Replacement"
	next, err = r.EditResponseRule(ctx, d.Design.ID, 2, "ui", "r", "save", &rule, nil)
	if err != nil || next.Design.Version != 3 {
		t.Fatalf("save: %v", err)
	}
	next, err = r.EditResponseRule(ctx, d.Design.ID, 3, "ui", "r", "delete", nil, nil)
	if err != nil || next.Design.Version != 4 {
		t.Fatalf("delete: %v", err)
	}
	restored, err := r.Restore(ctx, d.Design.ID, list.RevisionID, 4, "restore rules", "ui")
	if err != nil || !strings.Contains(restored.Draft.Document, `"name": "Rule"`) || !strings.Contains(restored.Draft.Document, "9007199254740993") {
		t.Fatalf("restore: %v", err)
	}
}

func TestResponseRuleSourceIdentityAndProposalPresence(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	ctx := t.Context()
	d, err := r.Create(ctx, CreateInput{Name: "Rules", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	proposal := strings.TrimSuffix(testDocument, "}") + `,"x-mocker-response-rules":{"formatVersion":1,"rules":[{"id":"unsaved","name":"Rule","nodes":[],"edges":[]}]}}`
	resolved, err := r.ResolveResponseRule(ctx, d.Design.ID, "unsaved", ResponseRuleProposal{Document: &proposal})
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(proposal)))
	if resolved.Source.Kind != "proposal" || resolved.Source.DocumentHash != hash || resolved.Source.Version != nil || resolved.Source.RevisionID != nil {
		t.Fatalf("proposal provenance: %+v", resolved.Source)
	}
	raw, err := jsonx.Marshal(resolved.Root)
	if err != nil || !strings.Contains(string(raw), "9007199254740993") {
		t.Fatalf("number lost: %s %v", raw, err)
	}
	empty := ""
	for _, tc := range []struct {
		id       int64
		rid      string
		document *string
		want     error
	}{{d.Design.ID, "unsaved", &empty, ErrInvalid}, {d.Design.ID, "missing", &proposal, ErrNotFound}, {99999, "unsaved", &proposal, ErrNotFound}, {d.Design.ID, "unsaved", nil, ErrNotFound}} {
		_, err = r.ResolveResponseRule(ctx, tc.id, tc.rid, ResponseRuleProposal{Document: tc.document})
		if !errors.Is(err, tc.want) {
			t.Fatalf("resolve: %v want %v", err, tc.want)
		}
	}
	saved, err := r.Save(ctx, d.Design.ID, SaveInput{ExpectedVersion: 1, Document: proposal, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err = r.ResolveResponseRule(ctx, d.Design.ID, "unsaved", ResponseRuleProposal{})
	if err != nil || resolved.Source.Kind != "saved" || resolved.Source.Version == nil || *resolved.Source.Version != 2 || resolved.Source.RevisionID == nil || *resolved.Source.RevisionID != saved.Draft.ID || resolved.Source.DocumentHash != fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(saved.Draft.Document))) {
		t.Fatalf("saved provenance: %+v %v", resolved.Source, err)
	}
	after, err := r.Detail(ctx, d.Design.ID)
	if err != nil || after.Design.Version != 2 || len(after.Revisions) != 2 {
		t.Fatalf("evaluation writes: %+v %v", after, err)
	}
}

func TestResponseRuleSourceSaveChecksUnsafePayloads(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	d, err := r.Create(t.Context(), CreateInput{Name: "Rules", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	node := `{"id":"r","type":"response","name":"Response","x":0,"y":0,"response":{"status":200,"mediaType":"application/json","headers":[]}}`
	for _, unsafe := range []string{strings.Replace(node, "application/json", "text/html", 1), strings.Replace(node, `"headers":[]`, `"headers":[{"name":"Set-Cookie","value":"sid=x"}]`, 1), strings.Replace(node, `"headers":[]`, `"headers":[{"name":"X-Test","value":"a\nb"}]`, 1), strings.Replace(node, `"status":200,`, "", 1)} {
		document := strings.TrimSuffix(testDocument, "}") + `,"x-mocker-response-rules":{"formatVersion":1,"rules":[{"id":"r","name":"Rule","nodes":[` + unsafe + `],"edges":[]}]}}`
		if _, err := r.Save(t.Context(), d.Design.ID, SaveInput{ExpectedVersion: 1, Document: document, Source: "ui"}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unsafe save admitted: %s %v", unsafe, err)
		}
	}
	after, err := r.Detail(t.Context(), d.Design.ID)
	if err != nil || after.Design.Version != 1 {
		t.Fatalf("invalid save wrote: %+v %v", after, err)
	}
}
