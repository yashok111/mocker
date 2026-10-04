package backendmodel

import (
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"uuid"
)

func annotationCommand(target AnnotationTarget, body string) Command {
	return Command{Type: "create_annotation", AnnotationID: uuid.NewV7().String(), Target: &target, Body: body}
}
func annotationApply(t *testing.T, r *Repo, p *Project, key string, commands ...Command) *Project {
	t.Helper()
	out, err := r.ApplyAs(t.Context(), p.ID, CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: key, Commands: commands}, "reviewer")
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func annotationFixture(t *testing.T) (*Repo, *Project, *ImportSession, AnnotationTarget) {
	t.Helper()
	r, p, s, _ := committedBase(t)
	g, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: p.CurrentRevisionID, RecordType: "nodes"})
	if err != nil || len(g.Nodes) != 1 {
		t.Fatalf("graph: %+v %v", g, err)
	}
	return r, p, s, AnnotationTarget{RecordType: "node", ID: g.Nodes[0].ID}
}
func TestAnnotationStrictCommandCodec(t *testing.T) {
	t.Parallel()
	target := `{"recordType":"node","id":"0197aaf9-5555-7000-8000-000000000098"}`
	good := fmt.Sprintf(`{"type":"create_annotation","annotationId":"0197aaf9-5555-7000-8000-000000000099","target":%s,"body":"note"}`, target)
	cases := []string{
		"null", `{}`, `{"type":"unknown"}`, strings.Replace(good, `"body":"note"`, `"body":null`, 1),
		strings.Replace(good, `"body":"note"`, `"body":"note","author":"forged"`, 1),
		strings.Replace(good, `"body":"note"`, `"body":"note","name":"mixed"`, 1),
		strings.Replace(good, `"body":"note"`, `"body":"note","body":"twice"`, 1),
		strings.Replace(good, `"body":"note"`, `"body":"  \n "`, 1),
		strings.Replace(good, `"target":`+target, `"target":null`, 1),
		strings.Replace(good, `"target":`+target, `"target":{"recordType":"node","id":"0197aaf9-5555-7000-8000-000000000098","revisionId":null}`, 1),
		strings.Replace(good, `"target":`+target, `"target":{"recordType":"node","id":"0197aaf9-5555-7000-8000-000000000098","revisionId":""}`, 1),
		strings.Replace(good, `"target":`+target, `"target":{"recordType":"node","id":"0197aaf9-5555-7000-8000-000000000098","proposalId":"fake"}`, 1),
		strings.Replace(good, `"recordType":"node"`, `"recordType":"evidence"`, 1),
		strings.Replace(good, `0197aaf9-5555-7000-8000-000000000099`, `0197AAF9-5555-7000-8000-000000000099`, 1),
		strings.Replace(good, `"create_annotation"`, `"remove_annotation"`, 1),
		`{"type":"rename_project","name":"a","body":""}`,
	}
	for i, raw := range cases {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			var c Command
			if err := json.Unmarshal([]byte(raw), &c); err == nil {
				t.Fatalf("accepted %s", raw)
			}
		})
	}
	for _, body := range []string{strings.Repeat("界", 5461) + "a", strings.Repeat("a", 16384), " <script>plain text</script> "} {
		raw := strings.Replace(good, `"note"`, fmt.Sprintf("%q", body), 1)
		var c Command
		if err := json.Unmarshal([]byte(raw), &c); err != nil || c.Body != body {
			t.Fatalf("valid body length %d: %v", len(body), err)
		}
	}
	for _, body := range []string{strings.Repeat("a", 16385), strings.Repeat("界", 5462), "\xff"} {
		raw := strings.Replace(good, `note`, body, 1)
		var c Command
		if err := json.Unmarshal([]byte(raw), &c); err == nil {
			t.Fatalf("accepted invalid UTF8/size: %d", len(body))
		}
	}
	var command Command
	if err := json.Unmarshal([]byte(good), &command); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(command)
	if err != nil || string(b) != good {
		t.Fatalf("roundtrip: %s %v", b, err)
	}
}
func TestAnnotationStrictEnvelopeAndLegacyRenameDigest(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`null`, `{}`, `{"expectedVersion":1,"idempotencyKey":"a","commands":null}`, `{"expectedVersion":null,"idempotencyKey":"a","commands":[]}`, `{"expectedVersion":1,"idempotencyKey":"a","commands":[],"author":"x"}`, `{"expectedVersion":1,"expectedVersion":2,"idempotencyKey":"a","commands":[]}`} {
		var in CommandsInput
		if err := json.Unmarshal([]byte(raw), &in); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	in := renameInput(1, "rename", " Orders2 ")
	result, err := r.ApplyAs(t.Context(), p.ID, in, "editor")
	if err != nil {
		t.Fatal(err)
	}
	const old = `{"expectedVersion":1,"idempotencyKey":"rename","commands":[{"type":"rename_project","name":"Orders2"}]}`
	var digest, response string
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT request_hash,response FROM backend_command_receipts WHERE scope=? AND key='rename'`, "project:"+p.ID).Scan(&digest, &response); err != nil {
		t.Fatal(err)
	}
	if digest != hashBytes([]byte(old)) {
		t.Fatalf("legacy digest: %s want %s", digest, hashBytes([]byte(old)))
	}
	replay, err := r.Apply(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(replay)
	if string(b) != response || replay.Version != result.Version {
		t.Fatalf("legacy receipt changed: %s", b)
	}
	if in.Commands[0].Name != " Orders2 " {
		t.Fatal("caller input mutated")
	}
}
func TestAnnotationValidationAndBatchAtomicity(t *testing.T) {
	t.Parallel()
	r, p, _, target := annotationFixture(t)
	valid := annotationCommand(target, "note")
	invalids := []Command{
		{Type: "create_annotation", AnnotationID: uuid.NewV7().String(), Target: &target, Body: "\xff"},
		{Type: "create_annotation", AnnotationID: uuid.NewV7().String(), Target: &target, Body: strings.Repeat("界", 5462)},
		{Type: "create_annotation", AnnotationID: "bad", Target: &target, Body: "note"},
		{Type: "rename_project", Name: "x", Body: "mixed"},
		{Type: "remove_annotation", AnnotationID: valid.AnnotationID, Target: &target},
	}
	for _, c := range invalids {
		_, err := r.Apply(t.Context(), p.ID, CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "invalid", Commands: []Command{valid, c}})
		assertFault(t, err, "backend_invalid")
	}
	missing := annotationCommand(AnnotationTarget{RecordType: "node", ID: uuid.NewV7().String()}, "missing")
	_, err := r.Apply(t.Context(), p.ID, CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "rollback", Commands: []Command{valid, {Type: "rename_project", Name: "Changed"}, missing}})
	assertFault(t, err, "backend_not_found")
	current, err := r.Get(t.Context(), p.ID)
	if err != nil || current.Version != p.Version || current.Name != p.Name {
		t.Fatalf("partial project: %+v %v", current, err)
	}
	page, err := r.ListAnnotations(t.Context(), p.ID, AnnotationListInput{})
	if err != nil || len(page.Items) != 0 {
		t.Fatalf("partial annotations: %+v %v", page, err)
	}
	var receipts int
	if err := r.db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM backend_command_receipts WHERE scope=?`, "project:"+p.ID).Scan(&receipts); err != nil || receipts != 0 {
		t.Fatalf("failed receipt: %d %v", receipts, err)
	}
	p = annotationApply(t, r, p, "batch", valid, Command{Type: "rename_project", Name: "Renamed"})
	if p.Name != "Renamed" || p.Version != current.Version+1 {
		t.Fatalf("batch: %+v", p)
	}
}
func TestAnnotationBatchLimits(t *testing.T) {
	t.Parallel()
	r, p, _, target := annotationFixture(t)
	commands := make([]Command, 101)
	for i := range commands {
		commands[i] = annotationCommand(target, "x")
	}
	_, err := r.Apply(t.Context(), p.ID, CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "101", Commands: commands})
	assertFault(t, err, "backend_invalid")
	p = annotationApply(t, r, p, "100", commands[:100]...)
	large := make([]Command, 64)
	for i := range large {
		large[i] = annotationCommand(target, strings.Repeat("a", 16384))
	}
	_, err = r.Apply(t.Context(), p.ID, CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "large", Commands: large})
	assertFault(t, err, "backend_invalid")
	var in CommandsInput
	raw := `{` + strings.Repeat(" ", 1<<20) + `"expectedVersion":1,"idempotencyKey":"a","commands":[{"type":"rename_project","name":"x"}]}`
	if err := json.Unmarshal([]byte(raw), &in); err == nil {
		t.Fatal("oversize wire accepted")
	}
}
