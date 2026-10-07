package backendmodel

import (
	"encoding/json/jsontext"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/store"
)

func firstImportFixture(p *Project) BeginImportInput {
	items := make([]InventoryItem, 0, 9)
	for _, category := range []string{"files", "endpoints", "datastores", "migrations", "producers", "consumers", "jobs", "contracts", "tests"} {
		items = append(items, InventoryItem{Category: category, Status: "complete", Denominator: new(int64(0)), DiscoverySource: "fixture", Gaps: []string{}})
	}
	return BeginImportInput{ExpectedVersion: p.Version, BaseRevisionID: p.CurrentRevisionID, IdempotencyKey: "begin", Manifest: SourceManifest{RepositoryName: "orders", Provider: SourceProvider{Name: "fixture", Version: "1", Namespace: "fixture", Method: "ast", Profiles: []string{"foundation-graph-v1"}, Limitations: []string{}}, Snapshot: SnapshotManifest{Consistency: "verified", CapturedAt: time.Now().UTC(), Files: []ManifestFile{{Path: "main.go", ContentHash: fixtureHash, FileType: "go", AnalysisStatus: "analyzed"}}}}, Inventory: items}
}

const fixtureHash = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func fixtureCommands(s *ImportSession) []ImportCommand {
	return []ImportCommand{
		{Op: "upsert_node", Node: &ImportNode{ExternalKey: "handler", Kind: "handler", Name: "Handle", Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof"}}},
		{Op: "upsert_evidence", Evidence: &ImportEvidence{ExternalKey: "proof", SubjectType: "node", SubjectKey: "handler", Method: "ast", Status: "explicit", Source: EvidenceSource{RepositoryID: s.RepositoryID, SnapshotID: s.SnapshotID, File: "main.go", ContentHash: fixtureHash}, Explanation: ""}},
	}
}
func putFixture(t *testing.T, r *Repo, p *Project, s *ImportSession) *BatchReceipt {
	t.Helper()
	commands := fixtureCommands(s)
	h, err := ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.PutImportBatch(t.Context(), p.ID, s.ID, "one", ImportBatchInput{ExpectedImportVersion: s.Version, PayloadHash: h, Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	return b
}
func TestImportDurableFirstCommit(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	b := putFixture(t, r, p, s)
	path := db.Path()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Migrate(t.Context(), slog.Default()); err != nil {
		t.Fatal(err)
	}
	r = NewRepo(reopened)
	replay := putFixture(t, r, p, s)
	if replay.AcceptedVersion != b.AcceptedVersion || replay.Identities[0].ID != b.Identities[0].ID {
		t.Fatalf("lost durable receipt: %+v", replay)
	}
	preview, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	if preview.State != "ready" || preview.CandidateHash == nil {
		t.Fatalf("preview %+v", preview)
	}
	in := CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "commit"}
	result, err := r.CommitImport(t.Context(), p.ID, s.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if result.Revision.ID == p.CurrentRevisionID || result.Project.Version != 2 {
		t.Fatalf("commit %+v", result)
	}
	again, err := r.CommitImport(t.Context(), p.ID, s.ID, in)
	if err != nil || again.Revision.ID != result.Revision.ID {
		t.Fatalf("commit replay %+v %v", again, err)
	}
	old, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: p.CurrentRevisionID, RecordType: "nodes"})
	if err != nil || len(old.Nodes) != 0 {
		t.Fatalf("old graph %+v %v", old, err)
	}
	graph, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: result.Revision.ID, RecordType: "nodes"})
	if err != nil || len(graph.Nodes) != 1 {
		t.Fatalf("graph %+v %v", graph, err)
	}
	cov, err := r.RevisionCoverage(t.Context(), p.ID, result.Revision.ID)
	if err != nil || len(cov.Snapshots) != 1 || len(cov.Inventory) != 9 || cov.Coverage.KnownObjects != 1 {
		t.Fatalf("coverage %+v %v", cov, err)
	}
	current, err := r.Get(t.Context(), p.ID)
	if err != nil || len(current.Repositories) != 1 {
		t.Fatalf("repositories %+v %v", current, err)
	}
	next := firstImportFixture(current)
	next.IdempotencyKey = "next"
	_, err = r.BeginImport(t.Context(), p.ID, next)
	assertFault(t, err, "backend_reimport_unsupported")
}

func previewFixture(t *testing.T, r *Repo, p *Project, s *ImportSession) *ImportPreview {
	t.Helper()
	b := putFixture(t, r, p, s)
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	return v
}
func commitFixture(t *testing.T, r *Repo, p *Project, s *ImportSession, v *ImportPreview, key string) (*ImportCommitResult, error) {
	t.Helper()
	return r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: key})
}
func TestImportCommitRollbackCompetingAndRename(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	p := createProject(t, r, "create")
	in := firstImportFixture(p)
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	otherIn := in
	otherIn.IdempotencyKey = "other"
	other, err := r.BeginImport(t.Context(), p.ID, otherIn)
	if err != nil {
		t.Fatal(err)
	}
	v := previewFixture(t, r, p, s)
	ov := previewFixture(t, r, p, other)
	if _, err := db.W.ExecContext(t.Context(), `CREATE TRIGGER fail_graph BEFORE INSERT ON backend_graph_records BEGIN SELECT RAISE(ABORT,'forced graph failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := commitFixture(t, r, p, s, v, "commit"); err == nil {
		t.Fatal("forced failure committed")
	}
	current, err := r.Get(t.Context(), p.ID)
	if err != nil || current.Version != p.Version || len(current.Repositories) != 0 {
		t.Fatalf("partial project: %+v %v", current, err)
	}
	revisions, err := r.Revisions(t.Context(), p.ID, ListInput{})
	if err != nil || len(revisions.Items) != 1 {
		t.Fatalf("partial revisions: %+v %v", revisions, err)
	}
	status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{})
	if err != nil || status.Session.State != "ready" {
		t.Fatalf("partial session %+v %v", status, err)
	}
	if _, err := db.W.ExecContext(t.Context(), `DROP TRIGGER fail_graph`); err != nil {
		t.Fatal(err)
	}
	renamed, err := r.Apply(t.Context(), p.ID, renameInput(p.Version, "rename", "Shipping"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = commitFixture(t, r, p, s, v, "commit")
	assertFault(t, err, "backend_version_conflict")
	result, err := commitFixture(t, r, renamed, s, v, "commit")
	if err != nil {
		t.Fatal(err)
	}
	if result.Project.Name != "Shipping" {
		t.Fatal("commit lost rename")
	}
	_, err = commitFixture(t, r, renamed, other, ov, "loser")
	assertFault(t, err, "backend_version_conflict")
	_, err = r.PreviewImport(t.Context(), p.ID, other.ID, PreviewImportInput{ExpectedImportVersion: ov.Version, BaseRevisionID: result.Revision.ID})
	assertFault(t, err, "backend_reimport_unsupported")
	replay, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil || replay.ID != s.ID || replay.State != "collecting" {
		t.Fatalf("begin receipt after commit %+v %v", replay, err)
	}
}
func TestImportBatchRepairsInvalidationAndStableIDs(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	command := fixtureCommands(s)[0]
	batch := func(version int64, id string, cs []ImportCommand) *BatchReceipt {
		t.Helper()
		hash, err := ImportBatchHash(cs)
		if err != nil {
			t.Fatal(err)
		}
		b, err := r.PutImportBatch(t.Context(), p.ID, s.ID, id, ImportBatchInput{ExpectedImportVersion: version, PayloadHash: hash, Commands: cs})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	b := batch(1, "node", []ImportCommand{command})
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || v.State != "needs_resolution" || v.CandidateHash != nil || len(v.Diagnostics) == 0 {
		t.Fatalf("forward reference %+v %v", v, err)
	}
	b = batch(v.Version, "proof", fixtureCommands(s)[1:])
	v, err = r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || v.State != "ready" {
		t.Fatalf("repair %+v %v", v, err)
	}
	b = batch(v.Version, "remove", []ImportCommand{{Op: "remove", Remove: &ImportRemove{RecordType: "node", ExternalKey: "handler"}}})
	status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{})
	if err != nil || status.Session.State != "collecting" || status.Session.CandidateHash != nil {
		t.Fatalf("not invalidated %+v %v", status, err)
	}
	restored := batch(b.AcceptedVersion, "restore", []ImportCommand{command})
	if b.Identities[0].ID != restored.Identities[0].ID {
		t.Fatal("identity changed after removal")
	}
	changed := command
	copyNode := *command.Node
	copyNode.Name = "Changed"
	changed.Node = &copyNode
	hash, _ := ImportBatchHash([]ImportCommand{changed})
	_, err = r.PutImportBatch(t.Context(), p.ID, s.ID, "node", ImportBatchInput{ExpectedImportVersion: 1, PayloadHash: hash, Commands: []ImportCommand{changed}})
	assertFault(t, err, "backend_import_batch_conflict")
	aborted, err := r.AbortImport(t.Context(), p.ID, s.ID, AbortImportInput{ExpectedImportVersion: restored.AcceptedVersion, IdempotencyKey: "abort"})
	if err != nil || aborted.State != "aborted" {
		t.Fatalf("abort %+v %v", aborted, err)
	}
	replay := batch(1, "node", []ImportCommand{command})
	if replay.AcceptedVersion != 2 {
		t.Fatal("batch did not replay after abort")
	}
	again, err := r.AbortImport(t.Context(), p.ID, s.ID, AbortImportInput{ExpectedImportVersion: restored.AcceptedVersion, IdempotencyKey: "abort"})
	if err != nil || again.Version != aborted.Version {
		t.Fatalf("abort replay %+v %v", again, err)
	}
}
func TestImportStrictValidationAtomicBatches(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		mutate func([]ImportCommand)
	}{
		{"kind", func(c []ImportCommand) { c[0].Node.Kind = "table" }},
		{"unknown attribute", func(c []ImportCommand) { c[0].Node.Attributes["sql"] = jsontext.Value(`"DROP TABLE x"`) }},
		{"attribute type", func(c []ImportCommand) { c[0].Node.Attributes["description"] = jsontext.Value(`123`) }},
		{"attribute float", func(c []ImportCommand) { c[0].Node.Attributes["description"] = jsontext.Value(`1.5`) }},
		{"source hash", func(c []ImportCommand) { c[1].Evidence.Source.ContentHash = "bad" }},
		{"source excluded", func(c []ImportCommand) { c[1].Evidence.Source.File = "missing.go" }},
		{"inferred explanation", func(c []ImportCommand) { c[1].Evidence.Status = "inferred" }},
		{"range", func(c []ImportCommand) {
			c[1].Evidence.Source.StartLine = new(int64(2))
			c[1].Evidence.Source.EndLine = new(int64(1))
		}},
		{"half range", func(c []ImportCommand) { c[1].Evidence.Source.StartLine = new(int64(1)) }},
		{"union", func(c []ImportCommand) { c[0].Remove = &ImportRemove{RecordType: "node", ExternalKey: "handler"} }},
		{"duplicate", func(c []ImportCommand) { c[1] = c[0] }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cs := fixtureCommands(s)
			tc.mutate(cs)
			h, err := ImportBatchHash(cs)
			if err != nil {
				t.Fatal(err)
			}
			_, err = r.PutImportBatch(t.Context(), p.ID, s.ID, "invalid", ImportBatchInput{ExpectedImportVersion: 1, PayloadHash: h, Commands: cs})
			assertFault(t, err, "backend_import_invalid")
			status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{})
			if err != nil || status.Session.Version != 1 || status.Session.AcceptedBatchCount != 0 {
				t.Fatalf("invalid batch wrote state %+v %v", status, err)
			}
		})
	}
	for _, file := range []string{"../main.go", "/main.go", "a//b", "a/../b", "a\\b", ".env.local", "cert.pem"} {
		in := firstImportFixture(p)
		in.IdempotencyKey = "path"
		in.Manifest.Snapshot.Files[0].Path = file
		_, err = r.BeginImport(t.Context(), p.ID, in)
		assertFault(t, err, "backend_import_invalid")
	}
}
func TestImportPropertyMembershipHierarchy(t *testing.T) {
	for _, mode := range []string{"wrong evidence", "bad property", "parent without contains", "two parents", "cycle", "invalid endpoints"} {
		t.Run(mode, func(t *testing.T) {
			r, _ := testRepo(t)
			p := createProject(t, r, "create")
			s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
			if err != nil {
				t.Fatal(err)
			}
			cs := fixtureCommands(s)
			addNode := func(key string) *ImportNode {
				n := &ImportNode{ExternalKey: key, Kind: "module", Name: key, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{key + "-proof"}}
				cs = append(cs, ImportCommand{Op: "upsert_node", Node: n}, ImportCommand{Op: "upsert_evidence", Evidence: &ImportEvidence{ExternalKey: key + "-proof", SubjectType: "node", SubjectKey: key, Method: "ast", Status: "explicit", Source: cs[1].Evidence.Source}})
				return n
			}
			addEdge := func(key, from, to, kind string) {
				e := &ImportEdge{ExternalKey: key, Kind: kind, FromKey: from, ToKey: to, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{key + "-proof"}}
				cs = append(cs, ImportCommand{Op: "upsert_edge", Edge: e}, ImportCommand{Op: "upsert_evidence", Evidence: &ImportEvidence{ExternalKey: key + "-proof", SubjectType: "edge", SubjectKey: key, Method: "ast", Status: "explicit", Source: cs[1].Evidence.Source}})
			}
			switch mode {
			case "wrong evidence":
				addNode("other")
				cs[0].Node.EvidenceKeys = []string{"other-proof"}
			case "bad property":
				cs[1].Evidence.PropertyPath = new("/attributes/missing")
			case "parent without contains":
				addNode("parent")
				cs[0].Node.ParentKey = new("parent")
			case "two parents":
				addNode("a")
				addNode("b")
				addEdge("a-child", "a", "handler", "contains")
				addEdge("b-child", "b", "handler", "contains")
			case "cycle":
				addNode("a")
				addNode("b")
				addEdge("ab", "a", "b", "contains")
				addEdge("ba", "b", "a", "contains")
			case "invalid endpoints":
				addNode("a")
				addEdge("bad", "handler", "a", "contains")
			}
			hash, _ := ImportBatchHash(cs)
			b, err := r.PutImportBatch(t.Context(), p.ID, s.ID, "batch", ImportBatchInput{ExpectedImportVersion: 1, PayloadHash: hash, Commands: cs})
			if err != nil {
				t.Fatal(err)
			}
			v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
			if err != nil || v.State != "needs_resolution" || len(v.Diagnostics) == 0 || v.CandidateHash != nil {
				t.Fatalf("invalid graph %+v %v", v, err)
			}
		})
	}
}
func TestImportHashPythonSpecialStringsAndNull(t *testing.T) {
	commands := []ImportCommand{{Op: "upsert_node", Node: &ImportNode{ExternalKey: "é<>&\u2028\u2029", Kind: "module", Name: "雪", Attributes: map[string]jsontext.Value{"description": jsontext.Value(`null`)}, EvidenceKeys: []string{}}}}
	hash, err := ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	if hash != "cb8b0c906a16b77cf5a19c442bed44fde63edb56755a2a053067a428073bd7c5" {
		t.Fatalf("hash %s", hash)
	}
	absent := []ImportCommand{{Op: "upsert_node", Node: &ImportNode{ExternalKey: commands[0].Node.ExternalKey, Kind: "module", Name: "雪", Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{}}}}
	other, _ := ImportBatchHash(absent)
	if hash == other {
		t.Fatal("null attribute collapsed into absent")
	}
}

func TestImportCapabilitiesAndReplayPriority(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	found := false
	for _, feature := range p.Capabilities {
		if feature == "backend-source-import" {
			found = true
		}
	}
	if !found {
		t.Fatal("source import capability missing")
	}
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	putFixture(t, r, p, s)
	bad := ImportBatchInput{ExpectedImportVersion: 999, PayloadHash: "bad", Commands: fixtureCommands(s)}
	_, err = r.PutImportBatch(t.Context(), p.ID, s.ID, "one", bad)
	assertFault(t, err, "backend_import_batch_conflict")
}

func TestImportQueriesPaginationAndCoverage(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	cs := fixtureCommands(s)
	cs[0].Node.Attributes["description"] = jsontext.Value(`null`)
	cs[1].Evidence.PropertyPath = new("/attributes/description")
	for _, key := range []string{"b", "c", "d"} {
		cs = append(cs, ImportCommand{Op: "upsert_node", Node: &ImportNode{ExternalKey: key, Kind: "unresolved_target", Name: key, Attributes: map[string]jsontext.Value{"expectedKind": jsontext.Value(`"handler"`), "reason": jsontext.Value(`"not located"`), "searchScope": jsontext.Value(`"main.go"`)}, EvidenceKeys: []string{}}})
	}
	hash, _ := ImportBatchHash(cs)
	b, err := r.PutImportBatch(t.Context(), p.ID, s.ID, "one", ImportBatchInput{ExpectedImportVersion: 1, PayloadHash: hash, Commands: cs})
	if err != nil {
		t.Fatal(err)
	}
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: b.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || v.State != "ready" || v.Summary.Unresolved != 3 {
		t.Fatalf("preview %+v %v", v, err)
	}
	result, err := commitFixture(t, r, p, s, v, "commit")
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: result.Revision.ID, RecordType: "nodes", Limit: 2})
	if err != nil || len(first.Nodes) != 2 || first.NextCursor == "" {
		t.Fatalf("first %+v %v", first, err)
	}
	next, err := r.QueryGraph(t.Context(), p.ID, GraphQueryInput{RevisionID: result.Revision.ID, RecordType: "nodes", Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(next.Nodes) != 2 || next.NextCursor != "" || first.Nodes[1].ID >= next.Nodes[0].ID {
		t.Fatalf("next %+v %v", next, err)
	}
	for _, q := range []GraphQueryInput{{RevisionID: p.CurrentRevisionID, RecordType: "nodes", Cursor: first.NextCursor}, {RevisionID: result.Revision.ID, RecordType: "nodes", Kind: "handler", Cursor: first.NextCursor}, {RevisionID: result.Revision.ID, RecordType: "edges", Cursor: first.NextCursor}} {
		_, err := r.QueryGraph(t.Context(), p.ID, q)
		assertFault(t, err, "backend_invalid")
	}
	for _, q := range []GraphQueryInput{{RevisionID: result.Revision.ID, RecordType: "nodes", From: first.Nodes[0].ID}, {RevisionID: result.Revision.ID, RecordType: "edges", Search: "bad"}, {RevisionID: result.Revision.ID, RecordType: "nodes", Kind: "table"}} {
		_, err := r.QueryGraph(t.Context(), p.ID, q)
		assertFault(t, err, "backend_import_invalid")
	}
	other := createProject(t, r, "other")
	_, err = r.QueryGraph(t.Context(), other.ID, GraphQueryInput{RevisionID: result.Revision.ID, RecordType: "nodes", Cursor: first.NextCursor})
	assertFault(t, err, "backend_not_found")
	n, err := r.Node(t.Context(), p.ID, result.Revision.ID, first.Nodes[0].ID)
	if err != nil || string(n.Attributes["description"]) != "null" {
		t.Fatalf("node %+v %v", n, err)
	}
	e, err := r.Evidence(t.Context(), p.ID, result.Revision.ID, EvidenceQueryInput{SubjectID: n.ID})
	if err != nil || len(e.Items) != 1 || e.Items[0].SubjectID != n.ID {
		t.Fatalf("evidence %+v %v", e, err)
	}
	coverage, err := r.RevisionCoverage(t.Context(), p.ID, result.Revision.ID)
	if err != nil || coverage.Coverage.Status != "partial" || coverage.Coverage.Denominator != nil || coverage.Coverage.KnownObjects != 4 {
		t.Fatalf("coverage %+v %v", coverage, err)
	}
	old, err := r.RevisionCoverage(t.Context(), p.ID, p.CurrentRevisionID)
	if err != nil || len(old.Snapshots) != 0 || len(old.Inventory) != 0 {
		t.Fatalf("initial coverage %+v %v", old, err)
	}
}
func TestImportLimitsAndHashBaseConflicts(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	sessions := []*ImportSession{}
	for i := 0; i < MaxOpenImportSessions; i++ {
		in := firstImportFixture(p)
		in.IdempotencyKey = string(rune('a' + i))
		s, err := r.BeginImport(t.Context(), p.ID, in)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, s)
	}
	in := firstImportFixture(p)
	in.IdempotencyKey = "six"
	_, err := r.BeginImport(t.Context(), p.ID, in)
	if f := assertFault(t, err, "backend_import_limit"); f.Status != 413 {
		t.Fatalf("status %+v", f)
	}
	s := sessions[0]
	cs := fixtureCommands(s)
	cs[1].Evidence.Snippet = new(strings.Repeat("x", MaxEvidenceSnippetBytes+1))
	h, _ := ImportBatchHash(cs)
	_, err = r.PutImportBatch(t.Context(), p.ID, s.ID, "snippet", ImportBatchInput{ExpectedImportVersion: 1, PayloadHash: h, Commands: cs})
	assertFault(t, err, "backend_import_limit")
	big := make([]ImportCommand, MaxImportCommands+1)
	for i := range big {
		big[i] = fixtureCommands(s)[0]
	}
	h, _ = ImportBatchHash(big)
	_, err = r.PutImportBatch(t.Context(), p.ID, s.ID, "many", ImportBatchInput{ExpectedImportVersion: 1, PayloadHash: h, Commands: big})
	assertFault(t, err, "backend_import_limit")
	v := previewFixture(t, r, p, s)
	_, err = r.CommitImport(t.Context(), p.ID, s.ID, CommitImportInput{ExpectedVersion: 1, ExpectedImportVersion: v.Version, CandidateHash: fixtureHash, IdempotencyKey: "wrong-hash"})
	assertFault(t, err, "backend_import_hash_conflict")
	other := createProject(t, r, "other")
	_, err = r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: v.Version, BaseRevisionID: other.CurrentRevisionID})
	assertFault(t, err, "backend_import_base_conflict")
	result, err := commitFixture(t, r, p, s, v, "commit")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.AbortImport(t.Context(), p.ID, s.ID, AbortImportInput{ExpectedImportVersion: v.Version + 1, IdempotencyKey: "cannot-abort"}); err == nil {
		t.Fatal("committed session aborted")
	}
	if result.Revision.ID == p.CurrentRevisionID {
		t.Fatal("commit did not advance")
	}
}

func TestImportBatchFullByteLimit(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	commands := fixtureCommands(s)[:1]
	commands[0].Node.Name = strings.Repeat("x", MaxImportBatchBytes-200)
	payload, err := canonicalJSON(commands)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) >= MaxImportBatchBytes {
		t.Fatalf("fixture commands must fit batch bound: %d", len(payload))
	}
	h, _ := ImportBatchHash(commands)
	in := ImportBatchInput{ExpectedImportVersion: 1, PayloadHash: h, Commands: commands}
	request, err := canonicalJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(request) <= MaxImportBatchBytes {
		t.Fatalf("fixture request must exceed batch bound: %d", len(request))
	}
	_, err = r.PutImportBatch(t.Context(), p.ID, s.ID, "large", in)
	assertFault(t, err, "backend_import_limit")
}

func TestImportConcurrentCommitReceipts(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	s, err := r.BeginImport(t.Context(), p.ID, firstImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	v := previewFixture(t, r, p, s)
	in := CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: v.Version, CandidateHash: *v.CandidateHash, IdempotencyKey: "commit"}
	var wg sync.WaitGroup
	results := make(chan *ImportCommitResult, 2)
	failures := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			result, err := r.CommitImport(t.Context(), p.ID, s.ID, in)
			if err != nil {
				failures <- err
			} else {
				results <- result
			}
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	id := ""
	count := 0
	for result := range results {
		if id != "" && result.Revision.ID != id {
			t.Fatal("same commit produced two revisions")
		}
		id = result.Revision.ID
		count++
	}
	if count != 2 {
		t.Fatalf("responses %d", count)
	}
	revisions, err := r.Revisions(t.Context(), p.ID, ListInput{})
	if err != nil || len(revisions.Items) != 2 {
		t.Fatalf("revisions %+v %v", revisions, err)
	}
}
func TestImportStatusAndEvidencePages(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "create")
	in := firstImportFixture(p)
	s, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	in.IdempotencyKey = "second"
	other, err := r.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	imports, err := r.Imports(t.Context(), p.ID, ListInput{Limit: 1})
	if err != nil || len(imports.Items) != 1 || imports.NextCursor == "" {
		t.Fatalf("imports %+v %v", imports, err)
	}
	second, err := r.Imports(t.Context(), p.ID, ListInput{Limit: 1, Cursor: imports.NextCursor})
	if err != nil || len(second.Items) != 1 || second.Items[0].ID == imports.Items[0].ID {
		t.Fatalf("second %+v %v", second, err)
	}
	commands := fixtureCommands(s)
	commands[0].Node.EvidenceKeys = []string{"proof", "proof2", "proof3"}
	for _, key := range []string{"proof2", "proof3"} {
		e := *commands[1].Evidence
		e.ExternalKey = key
		commands = append(commands, ImportCommand{Op: "upsert_evidence", Evidence: &e})
	}
	h, _ := ImportBatchHash(commands)
	version := int64(1)
	for _, bid := range []string{"c", "a", "b"} {
		b, err := r.PutImportBatch(t.Context(), p.ID, s.ID, bid, ImportBatchInput{ExpectedImportVersion: version, PayloadHash: h, Commands: commands})
		if err != nil {
			t.Fatal(err)
		}
		version = b.AcceptedVersion
	}
	status, err := r.Import(t.Context(), p.ID, s.ID, ListInput{Limit: 2})
	if err != nil || len(status.AcceptedBatches) != 2 || status.NextCursor == "" || status.AcceptedBatches[0].BatchID != "c" || status.AcceptedBatches[1].BatchID != "a" { // acceptance order (F89)
		t.Fatalf("status %+v %v", status, err)
	}
	next, err := r.Import(t.Context(), p.ID, s.ID, ListInput{Limit: 2, Cursor: status.NextCursor})
	if err != nil || len(next.AcceptedBatches) != 1 || next.AcceptedBatches[0].BatchID != "b" {
		t.Fatalf("batch next %+v %v", next, err)
	}
	_, err = r.Import(t.Context(), p.ID, other.ID, ListInput{Cursor: status.NextCursor})
	assertFault(t, err, "backend_invalid")
	v, err := r.PreviewImport(t.Context(), p.ID, s.ID, PreviewImportInput{ExpectedImportVersion: version, BaseRevisionID: p.CurrentRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	result, err := commitFixture(t, r, p, s, v, "commit")
	if err != nil {
		t.Fatal(err)
	}
	e, err := r.Evidence(t.Context(), p.ID, result.Revision.ID, EvidenceQueryInput{Limit: 2})
	if err != nil || len(e.Items) != 2 || e.NextCursor == "" {
		t.Fatalf("evidence %+v %v", e, err)
	}
	enext, err := r.Evidence(t.Context(), p.ID, result.Revision.ID, EvidenceQueryInput{Limit: 2, Cursor: e.NextCursor})
	if err != nil || len(enext.Items) != 1 || enext.Items[0].ID <= e.Items[1].ID {
		t.Fatalf("evidence next %+v %v", enext, err)
	}
	_, err = r.Evidence(t.Context(), p.ID, result.Revision.ID, EvidenceQueryInput{SubjectID: e.Items[0].SubjectID, Cursor: e.NextCursor})
	assertFault(t, err, "backend_invalid")
}
