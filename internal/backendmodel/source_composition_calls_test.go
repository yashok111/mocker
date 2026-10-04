package backendmodel

import (
	"encoding/json/jsontext"
	"testing"
)

func TestSource6CrossRepositoryHTTPCalls(t *testing.T) {
	for _, kind := range []string{"http_operation", "handler", "symbol"} {
		t.Run(kind, func(t *testing.T) { source6CrossRepositoryCall(t, kind) })
	}
}

func source6CrossRepositoryCall(t *testing.T, kind string) {
	t.Helper()
	r, _ := testRepo(t)
	p := createProject(t, r, "http-calls")
	in := source6Input(t, p)
	for i := range in.Inventory {
		if in.Inventory[i].Category == "endpoints" && kind == "http_operation" {
			in.Inventory[i].KnownCount = 1
			in.Inventory[i].Denominator = new(int64(1))
		}
	}
	a, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	commands := fixtureCommands(a)
	commands[0].Node.Kind = kind
	if kind == "http_operation" {
		commands[0].Node.Attributes = map[string]jsontext.Value{"method": jsontext.Value(`"GET"`), "path": jsontext.Value(`"/orders"`)}
	}
	batch := sendCommands(t, r, p, a, 1, "http", commands...)
	base := commitStaged(t, r, p, a, batch.AcceptedVersion, "http")
	p = &base.Project
	graph, err := r.ResolveSourceGraph(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil {
		t.Fatal(err)
	}
	target := sourceAssertionRef(graph.Assertions[0])
	next := source6Input(t, p)
	next.Manifest.RepositoryName = "caller-repository"
	next.IdempotencyKey = "caller"
	b, err := r.BeginImport(t.Context(), p.ID, next)
	if err != nil {
		t.Fatal(err)
	}
	commands = fixtureCommands(b)
	proof := *commands[1].Evidence
	proof.ExternalKey = "call-proof"
	proof.SubjectType = "edge"
	proof.SubjectKey = "calls"
	proof.Source.StartLine = new(int64(1))
	proof.Source.EndLine = new(int64(2))
	commands = append(commands, ImportCommand{Op: "upsert_edge", Edge: &ImportEdge{ExternalKey: "calls", Kind: "calls", FromRef: &ImportRecordRef{LocalKey: "handler"}, ToRef: &ImportRecordRef{Base: &target}, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"call-proof"}}}, ImportCommand{Op: "upsert_evidence", Evidence: &proof})
	batch = sendCommands(t, r, p, b, 1, "caller", commands...)
	preview, err := r.PreviewImport(t.Context(), p.ID, b.ID, PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || preview.State != "ready" {
		t.Fatalf("explicit source6 HTTP call rejected: %+v %v", preview, err)
	}
	loaded, err := loadSession(t.Context(), r.db.R, p.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	prepared, _, err := prepareGraph(t.Context(), r.db.R, loaded)
	if err != nil {
		t.Fatal(err)
	}
	legacy := SourceStructuralGraph{SchemaVersion: EventsSchemaVersion, Nodes: prepared.Nodes, Edges: prepared.Edges}
	diagnostics, err := ValidateSourceStructure(t.Context(), legacy)
	if err != nil || kind == "http_operation" && len(diagnostics) == 0 {
		t.Fatal("legacy endpoint admission was widened")
	}
}
