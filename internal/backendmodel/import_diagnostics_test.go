package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
)

func TestImportDiagnosticsIdentifyRecordFieldAndControlCharacters(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "diagnostics")
	s, err := r.BeginImport(t.Context(), p.ID, eventsProfileInput(firstImportFixture(p), false))
	if err != nil {
		t.Fatal(err)
	}
	command := ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "consumer/test", Kind: "consumer", Name: "Consumer", Attributes: map[string]jsontext.Value{"analysisStatus": jsontext.Value(`"complete"`), "gaps": jsontext.Value(`[]`), "dispatchStatus": jsontext.Value(`"complete"`), "description": jsontext.Value(`"line one\nline two"`)}, EvidenceKeys: []string{}}}
	commands := []ImportCommand{command}
	hash, err := ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	input := ImportBatchInput{ExpectedImportVersion: s.Version, PayloadHash: hash, Commands: commands}
	_, err = r.PutImportBatch(t.Context(), p.ID, s.ID, "invalid", input)
	fault := assertFault(t, err, "backend_import_invalid")
	if fault.Details["path"] != "/commands/0/node/attributes/description" || fault.Details["externalKey"] != "consumer/test" || !strings.Contains(strings.ToLower(fault.Message), "control") {
		t.Fatalf("opaque import diagnostic: %+v", fault)
	}
	input.Commands[0].Node.Attributes["description"] = jsontext.Value(`"Safe description"`)
	input.PayloadHash, err = ImportBatchHash(input.Commands)
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.ValidateImportBatch(t.Context(), p.ID, s.ID, ImportBatchValidationInput{ExpectedImportVersion: input.ExpectedImportVersion, PayloadHash: input.PayloadHash, Commands: input.Commands})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Valid || result.Version != s.Version || result.Scope != "record-validation-v1" {
		t.Fatalf("validation response: %+v", result)
	}
	status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{})
	if err != nil {
		t.Fatal(err)
	}
	if status.Session.Version != s.Version || len(status.AcceptedBatches) != 0 {
		t.Fatal("read-only validation changed staging")
	}
}

func TestImportDiagnosticsBoundUntrustedFieldsAndUseJSONPointers(t *testing.T) {
	t.Parallel()
	command := ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: strings.Repeat("private", 10000), Kind: strings.Repeat("bad-kind", 10000)}}
	d := importRecordDiagnostic(0, command, semantic("node.parentKey", "Invalid source6 member"))
	raw, err := json.Marshal(d)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > 4096 || d.Path != "/commands/0/node/parentKey" {
		t.Fatalf("unbounded or malformed diagnostic (%d bytes): %q", len(raw), d.Path)
	}
	err = validateAttributes("system", map[string]jsontext.Value{"a/b~c": jsontext.Value(`"value"`)}, false)
	command.Node.ExternalKey = "valid"
	command.Node.Kind = "system"
	d = importRecordDiagnostic(0, command, err)
	if d.Path != "/commands/0/node/attributes/a~1b~0c" {
		t.Fatalf("attribute key not JSON-Pointer escaped: %q", d.Path)
	}
}

func TestImportValidationPagesBindTheExactBatch(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "diagnostic-pages")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	commands := make([]ImportCommand, 26)
	for i := range commands {
		commands[i] = ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: fmt.Sprint("node/", i), Kind: "handler", Name: "", Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{}}}
	}
	hash, err := ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	in := ImportBatchValidationInput{ExpectedImportVersion: s.Version, PayloadHash: hash, Commands: commands}
	first, err := r.ValidateImportBatch(t.Context(), p.ID, s.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if first.Valid || first.TotalErrors != 26 || len(first.Diagnostics) != 25 || first.NextCursor == "" {
		t.Fatalf("unbounded diagnostics: %+v", first)
	}
	in.Cursor = first.NextCursor
	second, err := r.ValidateImportBatch(t.Context(), p.ID, s.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Diagnostics) != 1 || second.Diagnostics[0].CommandIndex != 25 || second.NextCursor != "" {
		t.Fatalf("diagnostic union incomplete: %+v", second)
	}
	in.Commands[0].Node.Name = "Repaired"
	in.PayloadHash, err = ImportBatchHash(in.Commands)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.ValidateImportBatch(t.Context(), p.ID, s.ID, in); err == nil {
		t.Fatal("cursor accepted another batch")
	}
}

func TestImportDiagnosticDetailReassemblesWithoutTruncation(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "diagnostic-detail")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("long-key/", 700)
	commands := []ImportCommand{{Op: "upsert_node", Node: &ImportNode{ExternalKey: key, Kind: "handler", Name: "", Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{}}}}
	hash, _ := ImportBatchHash(commands)
	in := ImportBatchValidationInput{ExpectedImportVersion: s.Version, PayloadHash: hash, Commands: commands, ResponseMode: "json-chunks-v1"}
	var complete strings.Builder
	for {
		page, err := r.ValidateImportBatch(t.Context(), p.ID, s.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Diagnostics) != 0 || len(page.DetailChunk) > 2048 {
			t.Fatal("unbounded detail response")
		}
		complete.WriteString(page.DetailChunk)
		if page.NextCursor == "" {
			break
		}
		in.Cursor = page.NextCursor
	}
	var diagnostics []ImportRecordDiagnostic
	if err := json.Unmarshal([]byte(complete.String()), &diagnostics); err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 1 || diagnostics[0].ExternalKey != key || diagnostics[0].Truncated {
		t.Fatal("full diagnostic lost text")
	}
}

func TestImportDiagnosticFirstFailureIsDeterministic(t *testing.T) {
	command := ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: "node", Kind: "handler", Name: "Handler", Attributes: map[string]jsontext.Value{"z_invalid": jsontext.Value(`true`), "a_invalid": jsontext.Value(`false`)}, EvidenceKeys: []string{}}}
	for range 100 {
		diagnostic := importRecordDiagnostic(0, command, validateAttributes("handler", command.Node.Attributes, false))
		if diagnostic.Path != "/commands/0/node/attributes/a_invalid" {
			t.Fatalf("unstable first error: %s", diagnostic.Path)
		}
	}
}
