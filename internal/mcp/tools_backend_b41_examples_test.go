package mcp

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/guide"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testkit"
)

// These protocol examples use the real SDK handler and admin/domain boundary.
// Guide selection is checked against the current server's advertised owners.
type b41SDKExamples struct {
	server     *admin.Server
	db         *store.DB
	cfg        *config.Config
	tools      *toolFixture
	project    backendmodel.Project
	next       int
	transcript []b41SDKTranscript
}

type b41SDKTranscript struct {
	Tool     string `json:"tool"`
	Request  string `json:"request"`
	Response string `json:"response,omitempty"`
	Error    string `json:"error,omitempty"`
}

type b41SDKReceipt struct {
	tool, request string
	response      []byte
}

func newB41SDKExamples(t *testing.T) *b41SDKExamples {
	t.Helper()
	cfg := resourcesTestConfig(t)
	server, db := newResourcesTestServer(t, cfg)
	h := &b41SDKExamples{server: server, db: db, cfg: cfg, tools: newToolFixture(server)}
	h.recordTranscript(t)
	h.negotiate(t)
	h.call(t, "create_backend_project", backendmodel.CreateInput{Name: "B4.1 public examples", IdempotencyKey: "create-project"}, nil, &h.project)
	return h
}

func (h *b41SDKExamples) negotiate(t *testing.T) {
	t.Helper()
	h.readOnly(t, func() {
		h.call(t, "get_server_config", nil, nil, nil)
		var capabilities struct {
			ModelSchemaVersions []string         `json:"modelSchemaVersions"`
			ViewSchemaVersions  []string         `json:"viewSchemaVersions"`
			ProviderProfiles    []string         `json:"providerProfiles"`
			Features            []string         `json:"features"`
			Workflows           []guide.Workflow `json:"workflowVersions"`
		}
		h.call(t, "get_backend_capabilities", nil, nil, &capabilities)
		// The advertised owners and versions are exactly the guide's own
		// (guide.BackendWorkflows): an extra, missing or re-versioned owner
		// fails below, and len(versions)==0 after the loop proves none is
		// missing. A literal owner count and version map stood here before and
		// went stale with every new owner or guide version.
		if !slices.Equal(capabilities.ModelSchemaVersions, []string{"1", "2", "3", "4", "5", "6"}) || !slices.Contains(capabilities.ProviderProfiles, "composed-source-v1") || len(capabilities.Workflows) == 0 {
			t.Fatalf("incomplete source6 discovery: %+v", capabilities)
		}
		versions := map[string]string{}
		for _, w := range guide.BackendWorkflows() {
			versions[w.WorkflowID] = w.WorkflowVersion
		}
		if len(capabilities.Workflows) != len(versions) {
			t.Fatalf("discovery advertises %d owners, the guide declares %d", len(capabilities.Workflows), len(versions))
		}
		selectedSet := ""
		for _, owner := range capabilities.Workflows {
			if versions[owner.WorkflowID] != owner.WorkflowVersion || owner.GuideSetID == "" || owner.ManifestHash != owner.GuideSetID || selectedSet != "" && selectedSet != owner.GuideSetID {
				t.Fatalf("incompatible guide owner: %+v", owner)
			}
			delete(versions, owner.WorkflowID)
			selectedSet = owner.GuideSetID
			for _, requirement := range []struct{ required, available []string }{{owner.RequiredModelSchemaVersions, capabilities.ModelSchemaVersions}, {owner.RequiredViewSchemaVersions, capabilities.ViewSchemaVersions}, {owner.RequiredCapabilities, capabilities.Features}} {
				for _, value := range requirement.required {
					if !slices.Contains(requirement.available, value) {
						t.Fatalf("owner %s requires unadvertised %s", owner.WorkflowID, value)
					}
				}
			}
			for _, topic := range owner.Topics {
				var out GetGuideOutput
				h.call(t, "get_guide", GetGuideInput{GuideSetID: selectedSet, Topic: topic.Topic}, nil, &out)
				if out.GuideSetID != selectedSet || out.ManifestHash != owner.ManifestHash || out.WorkflowID != owner.WorkflowID || out.WorkflowVersion != owner.WorkflowVersion || out.Topic != topic.Topic || out.ContentHash != topic.ContentHash || out.ContentHash != "sha256:"+b41Digest([]byte(out.Markdown)) || len(out.Topics) != len(guide.Topics()) || !slices.Contains(out.Topics, topic.Topic) || !strings.HasPrefix(out.Markdown, "# ") {
					t.Fatalf("served topic does not match its advertised owner/body: %s", topic.Topic)
				}
			}
		}
		if len(versions) != 0 {
			t.Fatalf("missing owners: %v", versions)
		}
		var routing GetGuideOutput
		h.call(t, "get_guide", GetGuideInput{GuideSetID: selectedSet, Topic: "overview"}, nil, &routing)
		if routing.WorkflowID != "mocker-routing" || routing.WorkflowVersion != "2" || routing.GuideSetID != selectedSet || routing.ManifestHash != selectedSet || routing.ContentHash != "sha256:"+b41Digest([]byte(routing.Markdown)) {
			t.Fatal("routing guide is outside the selected set")
		}
		const previous = "sha256:6ba1f13bd68d7c1ffdc3022a548041ed825361f2bc71d44389357483655992e2"
		for _, topic := range []string{"backend-import", "backend-sync", "backend-change-proposals", "backend-annotations"} {
			h.reject(t, "get_guide", GetGuideInput{GuideSetID: previous, Topic: topic}, nil, "unknown guide set")
		}
	})
}

func (h *b41SDKExamples) recordTranscript(t *testing.T) {
	t.Helper()
	path := filepath.Join(t.ArtifactDir(), "sdk-protocol.json")
	t.Cleanup(func() {
		if err := os.WriteFile(path, b41JSON(t, h.transcript), 0o600); err != nil {
			t.Error(err)
		}
	})
	t.Logf("SDK transcript: %s", path)
}

func (h *b41SDKExamples) key(label string) string {
	h.next++
	return fmt.Sprintf("%s-%d", label, h.next)
}

func (h *b41SDKExamples) input(t *testing.T, name string, body any, extra map[string]any) string {
	t.Helper()
	fields := map[string]jsontext.Value{}
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(raw, &fields); err != nil {
			t.Fatal(err)
		}
	}
	global := slices.Contains([]string{"get_server_config", "get_guide", "get_backend_capabilities", "create_backend_project", "list_backend_projects"}, name)
	if h.project.ID != "" && !global {
		fields["projectId"] = b41JSON(t, h.project.ID)
	}
	for key, value := range extra {
		fields[key] = b41JSON(t, value)
	}
	return string(b41JSON(t, fields))
}

func b41JSON(t *testing.T, value any) jsontext.Value {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (h *b41SDKExamples) call(t *testing.T, name string, body any, extra map[string]any, out any) b41SDKReceipt {
	t.Helper()
	request := h.input(t, name, body, extra)
	raw, message := h.tools.Call(t, name, request)
	h.transcript = append(h.transcript, b41SDKTranscript{Tool: name, Request: request, Response: string(raw), Error: message})
	if message != "" {
		t.Fatalf("%s: %s\nrequest=%s", name, message, request)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s response: %v", name, err)
		}
	}
	return b41SDKReceipt{tool: name, request: request, response: bytes.Clone(raw)}
}

func (h *b41SDKExamples) replay(t *testing.T, receipt b41SDKReceipt) {
	t.Helper()
	raw, message := h.tools.Call(t, receipt.tool, receipt.request)
	h.transcript = append(h.transcript, b41SDKTranscript{Tool: receipt.tool, Request: receipt.request, Response: string(raw), Error: message})
	if message != "" || !bytes.Equal(raw, receipt.response) {
		t.Fatalf("%s exact receipt changed: %s\ngot=%s\nwant=%s", receipt.tool, message, raw, receipt.response)
	}
}

func (h *b41SDKExamples) reject(t *testing.T, name string, body any, extra map[string]any, detail string) {
	t.Helper()
	request := h.input(t, name, body, extra)
	_, message := h.tools.Call(t, name, request)
	h.transcript = append(h.transcript, b41SDKTranscript{Tool: name, Request: request, Error: message})
	if message == "" || detail != "" && !strings.Contains(message, detail) {
		t.Fatalf("%s refusal=%q; want %q", name, message, detail)
	}
}

func (h *b41SDKExamples) refresh(t *testing.T) {
	t.Helper()
	h.call(t, "get_backend_project", nil, nil, &h.project)
}

func b41Digest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

func b41SourceInput(project backendmodel.Project, repository, provider, source string, composed bool) backendmodel.BeginImportInput {
	profiles := []string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile, backendmodel.LineageProfile, backendmodel.EventsProfile}
	in := backendmodel.BeginImportInput{
		ExpectedVersion: project.Version, BaseRevisionID: project.CurrentRevisionID,
		Mode: "initial", Profile: backendmodel.EventsProfile,
		Inventory: []backendmodel.InventoryItem{},
		Manifest: backendmodel.SourceManifest{
			RepositoryName: repository,
			Provider:       backendmodel.SourceProvider{Name: "sdk-example-collector", Version: "1", Namespace: provider, Method: "ast", Profiles: profiles, Limitations: []string{}},
			Snapshot:       backendmodel.SnapshotManifest{Consistency: "verified", CapturedAt: time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC), Files: []backendmodel.ManifestFile{{Path: "source.go", ContentHash: b41Digest([]byte(source)), FileType: "go", AnalysisStatus: "analyzed"}}},
		},
	}
	for category := range strings.FieldsSeq("files endpoints datastores migrations producers consumers jobs contracts tests") {
		count := int64(0)
		if category == "files" {
			count = 1
		}
		in.Inventory = append(in.Inventory, backendmodel.InventoryItem{Category: category, Status: "complete", KnownCount: count, Denominator: new(count), DiscoverySource: "inert Go declarations in SDK example", Gaps: []string{}, Reason: ""})
	}
	if composed {
		in.Mode, in.Profile, in.SyncPolicy = "composed", backendmodel.ComposedProfile, backendmodel.WholeSourcePolicy
		in.Manifest.Provider.Profiles = append(profiles, backendmodel.ComposedProfile)
		in.SourceScope = &backendmodel.SourceScope{Kind: "add_repository"}
		in.ScopeStatus = &backendmodel.SourceScopeStatus{Status: "complete", Gaps: []string{}}
	}
	return in
}

func (h *b41SDKExamples) begin(t *testing.T, in backendmodel.BeginImportInput) backendmodel.ImportSession {
	t.Helper()
	in.IdempotencyKey = h.key("begin")
	var session backendmodel.ImportSession
	h.call(t, "begin_backend_import", in, nil, &session)
	return session
}

type b41DeclaredNode struct {
	key, name string
	line      int64
}

func b41NodeCommands(session backendmodel.ImportSession, declarations ...b41DeclaredNode) []backendmodel.ImportCommand {
	commands := make([]backendmodel.ImportCommand, 0, len(declarations)*2)
	for _, node := range declarations {
		proofKey := "proof-" + node.key
		commands = append(commands,
			backendmodel.ImportCommand{Op: "upsert_node", Node: &backendmodel.ImportNode{ExternalKey: node.key, Kind: "handler", Name: node.name, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{proofKey}}},
			backendmodel.ImportCommand{Op: "upsert_evidence", Evidence: &backendmodel.ImportEvidence{ExternalKey: proofKey, SubjectType: "node", SubjectKey: node.key, Method: "ast", Status: "explicit", Explanation: "The named function is declared at this exact source line", Source: backendmodel.EvidenceSource{RepositoryID: session.RepositoryID, SnapshotID: session.SnapshotID, File: "source.go", ContentHash: session.Manifest.Snapshot.Files[0].ContentHash, StartLine: new(node.line), EndLine: new(node.line)}}},
		)
	}
	return commands
}

func (h *b41SDKExamples) batch(t *testing.T, session *backendmodel.ImportSession, commands []backendmodel.ImportCommand) backendmodel.BatchReceipt {
	t.Helper()
	hash, err := backendmodel.ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	var receipt backendmodel.BatchReceipt
	h.call(t, "put_backend_import_batch", backendmodel.ImportBatchInput{ExpectedImportVersion: session.Version, PayloadHash: hash, Commands: commands}, map[string]any{"importId": session.ID, "batchId": h.key("batch")}, &receipt)
	session.Version = receipt.AcceptedVersion
	return receipt
}

func (h *b41SDKExamples) preview(t *testing.T, session *backendmodel.ImportSession) backendmodel.ImportPreview {
	t.Helper()
	var preview backendmodel.ImportPreview
	h.call(t, "preview_backend_import", backendmodel.PreviewImportInput{ExpectedImportVersion: session.Version, BaseRevisionID: session.BaseRevisionID}, map[string]any{"importId": session.ID}, &preview)
	session.Version, session.State, session.CandidateHash = preview.Version, preview.State, preview.CandidateHash
	return preview
}

func (h *b41SDKExamples) commit(t *testing.T, session *backendmodel.ImportSession, preview backendmodel.ImportPreview) b41SDKReceipt {
	t.Helper()
	if preview.State != "ready" || preview.CandidateHash == nil {
		t.Fatalf("expected ready candidate: %+v", preview)
	}
	var result backendmodel.ImportCommitResult
	receipt := h.call(t, "commit_backend_import", backendmodel.CommitImportInput{ExpectedVersion: h.project.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: h.key("commit")}, map[string]any{"importId": session.ID}, &result)
	h.project = result.Project
	return receipt
}

func b41Identity(t *testing.T, receipt backendmodel.BatchReceipt, recordType, key string) string {
	t.Helper()
	for _, identity := range receipt.Identities {
		if identity.RecordType == recordType && identity.ExternalKey == key {
			return identity.ID
		}
	}
	t.Fatalf("no returned identity for %s/%s", recordType, key)
	return ""
}

func (h *b41SDKExamples) coverage(t *testing.T, target backendmodel.BackendReadTarget) *backendmodel.SourceVector {
	t.Helper()
	var response struct {
		Source *backendmodel.SourceReadContext `json:"source"`
	}
	h.call(t, "get_backend_coverage", target, nil, &response)
	if response.Source == nil || response.Source.SourceVector == nil {
		t.Fatal("source6 coverage omitted its exact vector")
	}
	return response.Source.SourceVector
}

func (h *b41SDKExamples) assertions(t *testing.T, target backendmodel.BackendReadTarget, recordType, id string) backendmodel.BackendAssertionsPage {
	t.Helper()
	var page backendmodel.BackendAssertionsPage
	h.call(t, "get_backend_assertions", target, map[string]any{"recordType": recordType, "id": id, "limit": 100}, &page)
	if page.NextCursor != "" {
		t.Fatal("small fixture unexpectedly exceeded one assertion page")
	}
	return page
}

func b41Partition(t *testing.T, vector *backendmodel.SourceVector, repository, provider string) backendmodel.SourcePartition {
	t.Helper()
	for _, partition := range vector.Partitions {
		if partition.RepositoryID == repository && partition.ProviderNamespace == provider {
			return partition
		}
	}
	t.Fatalf("missing partition %s/%s", repository, provider)
	return backendmodel.SourcePartition{}
}

func (h *b41SDKExamples) rows(t *testing.T) map[string]string {
	t.Helper()
	result := map[string]string{}
	tables, err := h.db.R.QueryContext(t.Context(), `SELECT name FROM sqlite_schema WHERE type='table' AND name LIKE 'backend_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for tables.Next() {
		var name string
		if err := tables.Scan(&name); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	if err := tables.Err(); err != nil {
		t.Fatal(err)
	}
	if err := tables.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range names {
		result[name] = h.tableDigest(t, name)
	}
	return result
}

func (h *b41SDKExamples) tableDigest(t *testing.T, name string) string {
	t.Helper()
	columns, entries := h.tableRows(t, name)
	return b41Digest(b41JSON(t, []any{columns, entries}))
}

func (h *b41SDKExamples) tableRows(t *testing.T, name string) ([]string, []string) {
	t.Helper()
	rows, err := h.db.R.QueryContext(t.Context(), `SELECT * FROM "`+strings.ReplaceAll(name, `"`, `""`)+`"`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var entries []string
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, string(b41JSON(t, values)))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	slices.Sort(entries)
	return columns, entries
}

func (h *b41SDKExamples) readOnly(t *testing.T, read func()) {
	t.Helper()
	before := h.rows(t)
	read()
	if after := h.rows(t); !maps.Equal(before, after) {
		t.Fatalf("public read group mutated backend model/session/receipt tables: before=%v after=%v", before, after)
	}
}

func TestBackendB41SDKRetainedPartitionExamples(t *testing.T) {
	t.Parallel()
	h := newB41SDKExamples(t)
	const original = "package example\n\nfunc Handle() {}\nfunc Spare() {}\n"
	legacy := h.begin(t, b41SourceInput(h.project, "primary", "provider-a", original, false))
	legacyBatch := h.batch(t, &legacy, b41NodeCommands(legacy, b41DeclaredNode{"handler-a", "Handle", 3}, b41DeclaredNode{"spare", "Spare", 4}))
	h.commit(t, &legacy, h.preview(t, &legacy))
	oldRevision := h.project.CurrentRevisionID
	mainID := b41Identity(t, legacyBatch, "node", "handler-a")
	spareID := b41Identity(t, legacyBatch, "node", "spare")
	oldNode := h.call(t, "get_backend_node", map[string]any{"revisionId": oldRevision, "nodeId": mainID}, nil, nil)
	h.reject(t, "get_backend_assertions", map[string]any{"revisionId": oldRevision}, nil, "HTTP 422")

	addition := b41SourceInput(h.project, "remote", "provider-remote", "package remote\n\nfunc Remote() {}\n", true)
	addition.ProfileExtension = &backendmodel.ImportProfileExtension{FromProfile: backendmodel.EventsProfile, ToProfile: backendmodel.ComposedProfile}
	remote := h.begin(t, addition)
	h.batch(t, &remote, b41NodeCommands(remote, b41DeclaredNode{"remote", "Remote", 3}))
	h.commit(t, &remote, h.preview(t, &remote))
	vector := h.coverage(t, backendmodel.BackendReadTarget{RevisionID: h.project.CurrentRevisionID})
	retained := b41Partition(t, vector, legacy.RepositoryID, "provider-a")
	if len(retained.Provider.Profiles) != 5 || retained.SnapshotID != legacy.SnapshotID {
		t.Fatalf("bootstrap silently extended old provider: %+v", retained)
	}
	remotePartition := b41Partition(t, vector, remote.RepositoryID, "provider-remote")
	if len(remotePartition.Provider.Profiles) != 6 {
		t.Fatal("new provider did not publish composed profile")
	}

	extend := b41SourceInput(h.project, "primary", "provider-a", original, true)
	extend.SourceScope = &backendmodel.SourceScope{Kind: "reconcile", RepositoryID: legacy.RepositoryID, ProviderNamespace: "provider-a"}
	extend.IdempotencyKey = h.key("implicit-extension")
	h.reject(t, "begin_backend_import", extend, nil, "backend_unsupported_scope")
	extend.ProfileExtension = addition.ProfileExtension
	current := h.begin(t, extend)
	h.batch(t, &current, b41NodeCommands(current, b41DeclaredNode{"handler-a", "Handle", 3}, b41DeclaredNode{"spare", "Spare", 4}))
	h.commit(t, &current, h.preview(t, &current))
	vector = h.coverage(t, backendmodel.BackendReadTarget{RevisionID: h.project.CurrentRevisionID})
	if got := b41Partition(t, vector, current.RepositoryID, "provider-a"); len(got.Provider.Profiles) != 6 || got.SnapshotID != current.SnapshotID {
		t.Fatalf("selected partition not extended: %+v", got)
	}
	if got := b41Partition(t, vector, remote.RepositoryID, "provider-remote"); !bytes.Equal(b41JSON(t, got), b41JSON(t, remotePartition)) {
		t.Fatal("selected extension changed unrelated partition")
	}
	h.readOnly(t, func() { h.replay(t, oldNode) })
	var oldBaseProposal backendmodel.ChangeProposalDetail
	h.call(t, "create_backend_change_proposal", backendmodel.CreateChangeProposalInput{Name: "Exact source5 baseline", BaseRevisionID: oldRevision, IdempotencyKey: h.key("source5-proposal")}, nil, &oldBaseProposal)
	h.readOnly(t, func() {
		h.call(t, "get_backend_node", b41FullTarget(oldBaseProposal), map[string]any{"nodeId": mainID}, nil)
		h.reject(t, "get_backend_assertions", b41FullTarget(oldBaseProposal), nil, "HTTP 422")
	})
	b41SDKAnnotationsAndOrphan(t, h, current, mainID, spareID)
}

func b41SDKAnnotationsAndOrphan(t *testing.T, h *b41SDKExamples, prior backendmodel.ImportSession, mainID, spareID string) {
	t.Helper()
	annotationID, orphanID := uuid.NewV7().String(), uuid.NewV7().String()
	body := "<em>Literal metadata</em>\nKeep exact whitespace.  "
	created := h.call(t, "apply_backend_project_commands", backendmodel.CommandsInput{ExpectedVersion: h.project.Version, IdempotencyKey: h.key("notes"), Commands: []backendmodel.Command{{Type: "create_annotation", AnnotationID: annotationID, Target: &backendmodel.AnnotationTarget{RecordType: "node", ID: mainID}, Body: body}, {Type: "create_annotation", AnnotationID: orphanID, Target: &backendmodel.AnnotationTarget{RecordType: "node", ID: spareID}, Body: "Retain after proved deletion"}}}, nil, &h.project)
	var notes backendmodel.AnnotationPage
	h.call(t, "list_backend_annotations", map[string]any{"limit": 1}, nil, &notes)
	if len(notes.Items) != 1 || notes.NextCursor == "" {
		t.Fatal("annotation example lacks a real cursor")
	}
	oldCursor := notes.NextCursor
	head := h.project.CurrentRevisionID
	updated := h.call(t, "apply_backend_project_commands", backendmodel.CommandsInput{ExpectedVersion: h.project.Version, IdempotencyKey: h.key("update-note"), Commands: []backendmodel.Command{{Type: "update_annotation", AnnotationID: annotationID, Target: &backendmodel.AnnotationTarget{RecordType: "node", ID: mainID}, Body: body + "\nReviewed"}}}, nil, &h.project)
	if h.project.CurrentRevisionID != head {
		t.Fatal("annotation changed source head")
	}
	h.reject(t, "list_backend_annotations", map[string]any{"limit": 1, "cursor": oldCursor}, nil, "HTTP 409")
	h.call(t, "list_backend_annotations", map[string]any{"annotationId": annotationID}, nil, &notes)
	if len(notes.Items) != 1 || notes.Items[0].Body != body+"\nReviewed" {
		t.Fatal("annotation replacement changed literal body")
	}

	const nextSource = "package example\n\nfunc Handle() {}\n"
	in := b41SourceInput(h.project, "primary", "provider-a", nextSource, true)
	in.SourceScope = &backendmodel.SourceScope{Kind: "reconcile", RepositoryID: prior.RepositoryID, ProviderNamespace: "provider-a"}
	session := h.begin(t, in)
	commands := b41NodeCommands(session, b41DeclaredNode{"handler-a", "Handle", 3})
	commands = append(commands, backendmodel.ImportCommand{Op: "delete_assertion", Deletion: &backendmodel.ImportDeletion{RecordType: "node", ExternalKey: "spare", ExpectedID: spareID, Reason: "The complete captured source no longer declares Spare"}})
	h.batch(t, &session, commands)
	preview := h.preview(t, &session)
	if preview.CandidateHash == nil {
		t.Fatalf("deletion is not ready: %+v", preview)
	}
	oldCAS := h.project.Version
	removed := h.call(t, "apply_backend_project_commands", backendmodel.CommandsInput{ExpectedVersion: oldCAS, IdempotencyKey: h.key("remove-note"), Commands: []backendmodel.Command{{Type: "remove_annotation", AnnotationID: annotationID}}}, nil, &h.project)
	h.reject(t, "commit_backend_import", backendmodel.CommitImportInput{ExpectedVersion: oldCAS, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: h.key("old-project-cas")}, map[string]any{"importId": session.ID}, "HTTP 409")
	h.commit(t, &session, h.preview(t, &session))
	h.readOnly(t, func() {
		h.call(t, "list_backend_annotations", map[string]any{"orphaned": true}, nil, &notes)
		if len(notes.Items) != 1 || notes.Items[0].ID != orphanID || notes.Items[0].TargetStatus != "orphaned" || notes.Items[0].Target.ID != spareID {
			t.Fatalf("deleted source note was retargeted: %+v", notes.Items)
		}
	})
	for _, receipt := range []b41SDKReceipt{created, updated, removed} {
		h.replay(t, receipt)
	}
	h.refresh(t)
}

func (h *b41SDKExamples) importHandler(t *testing.T, repository, provider, source, key, name string) (backendmodel.ImportSession, string) {
	t.Helper()
	session := h.begin(t, b41SourceInput(h.project, repository, provider, source, true))
	batch := h.batch(t, &session, b41NodeCommands(session, b41DeclaredNode{key, name, 3}))
	h.commit(t, &session, h.preview(t, &session))
	return session, b41Identity(t, batch, "node", key)
}

func b41BaseRef(assertion backendmodel.ProviderAssertion) backendmodel.BaseAssertionRef {
	return backendmodel.BaseAssertionRef{RepositoryID: assertion.Owner.RepositoryID, ProviderNamespace: assertion.Owner.ProviderNamespace, RecordType: assertion.RecordType, ExternalKey: assertion.ExternalKey, ExpectedID: assertion.RecordID, AssertionHash: assertion.AssertionHash}
}

func (h *b41SDKExamples) addSharedProvider(t *testing.T, original backendmodel.ImportSession, id, name string) backendmodel.ImportSession {
	t.Helper()
	claims := h.assertions(t, backendmodel.BackendReadTarget{RevisionID: h.project.CurrentRevisionID}, "node", id)
	if len(claims.Items) != 1 {
		t.Fatal("expected one exact starting claim")
	}
	in := b41SourceInput(h.project, original.Manifest.RepositoryName, "provider-b", "package example\n\nfunc "+name+"() {}\n", true)
	in.SourceScope = &backendmodel.SourceScope{Kind: "add_provider", RepositoryID: original.RepositoryID}
	session := h.begin(t, in)
	command := backendmodel.ImportCommand{Op: "claim_identity", ClaimIdentity: &backendmodel.SourceClaimIdentity{DecisionID: uuid.NewV7().String(), RecordType: "node", ExternalKey: "handler-b", Target: b41BaseRef(claims.Items[0].Assertion), Reason: "Independent provider identifies this exact source declaration", EvidenceKeys: []string{"proof-handler-b"}}}
	batch := h.batch(t, &session, append([]backendmodel.ImportCommand{command}, b41NodeCommands(session, b41DeclaredNode{"handler-b", name, 3})...))
	if b41Identity(t, batch, "node", "handler-b") != id {
		t.Fatal("claim did not retain the offered graph UUID")
	}
	return session
}

func b41Candidate(session backendmodel.ImportSession, preview backendmodel.ImportPreview) backendmodel.BackendReadTarget {
	return backendmodel.BackendReadTarget{ImportCandidate: &backendmodel.ImportCandidateReadTarget{ImportID: session.ID, ImportVersion: preview.Version, CandidateHash: *preview.CandidateHash}}
}

func (h *b41SDKExamples) resolveName(t *testing.T, session *backendmodel.ImportSession, id string) backendmodel.ImportPreview {
	t.Helper()
	priorHash := ""
	for range 3 {
		preview := h.preview(t, session)
		if preview.State == "ready" {
			return preview
		}
		var changes backendmodel.ImportChangesPage
		h.call(t, "get_backend_import_changes", map[string]any{"importId": session.ID, "previewVersion": preview.Version, "recordType": "assertion_conflict"}, nil, &changes)
		if len(changes.Items) != 1 || changes.Items[0].AssertionConflict == nil {
			t.Fatalf("expected one saved name conflict: %+v", changes)
		}
		conflict := changes.Items[0].AssertionConflict
		if conflict.ID != id || conflict.Property.Kind != "name" || conflict.ConflictHash == priorHash {
			t.Fatalf("wrong or unreconfirmed conflict: %+v", conflict)
		}
		values := map[string]string{}
		var chosen backendmodel.SourceAssertionSelection
		for _, contender := range conflict.Contenders {
			var value string
			if !contender.Value.Present || json.Unmarshal(contender.Value.Value, &value) != nil {
				t.Fatal("name contender is not a declared string")
			}
			values[contender.Owner.ProviderNamespace] = value
			if contender.Owner.ProviderNamespace == "provider-b" {
				chosen = backendmodel.SourceAssertionSelection{RepositoryID: contender.Owner.RepositoryID, ProviderNamespace: contender.Owner.ProviderNamespace, AssertionHash: contender.AssertionHash}
			}
		}
		if !maps.Equal(values, map[string]string{"provider-a": "Handle", "provider-b": "Alternate"}) || chosen.AssertionHash == "" {
			t.Fatalf("offered claims differ from independent source declarations: %v", values)
		}
		priorHash = conflict.ConflictHash
		h.batch(t, session, []backendmodel.ImportCommand{{Op: "resolve_assertion", Resolution: &backendmodel.SourceAssertionResolution{DecisionID: uuid.NewV7().String(), RecordType: "node", ID: id, Property: conflict.Property, ConflictHash: conflict.ConflictHash, Select: chosen, Reason: "Reviewed both declarations and chose provider-b for this exact conflict"}}})
	}
	t.Fatal("name conflict did not converge after explicitly reviewed pins")
	return backendmodel.ImportPreview{}
}

func TestBackendB41SDKProviderAndDependencyExamples(t *testing.T) {
	t.Parallel()
	h := newB41SDKExamples(t)
	a, sharedID := h.importHandler(t, "primary", "provider-a", "package example\n\nfunc Handle() {}\n", "handler-a", "Handle")
	base := h.project.CurrentRevisionID
	oldProof := h.call(t, "get_backend_evidence", map[string]any{"revisionId": base, "subjectId": sharedID}, nil, nil)
	b := h.addSharedProvider(t, a, sharedID, "Alternate")
	preview := h.resolveName(t, &b, sharedID)
	staged := h.assertions(t, b41Candidate(b, preview), "node", sharedID)
	if len(staged.Items) != 2 || staged.Items[0].Assertion.EvidenceIDs[0] == staged.Items[1].Assertion.EvidenceIDs[0] {
		t.Fatal("providers lost independent proof or losing claim")
	}
	h.commit(t, &b, preview)
	claims := h.assertions(t, backendmodel.BackendReadTarget{RevisionID: h.project.CurrentRevisionID}, "node", sharedID)
	if len(claims.Items) != 2 {
		t.Fatal("committed shared identity lost a provider")
	}

	migration := b41SourceInput(h.project, "primary", "provider-c", "package example\n\nfunc Migrated() {}\n", true)
	migration.SourceScope = &backendmodel.SourceScope{Kind: "migrate_provider", RepositoryID: a.RepositoryID, FromProviderNamespace: "provider-b", FromSnapshotID: b.SnapshotID, Reason: "Reviewed collector migration; old claims stay visible"}
	migration.Manifest.Provider.Name, migration.Manifest.Provider.Version = "replacement-collector", "2"
	c := h.begin(t, migration)
	h.batch(t, &c, b41NodeCommands(c, b41DeclaredNode{"migration-record", "Migrated", 3}))
	preview = h.preview(t, &c)
	var decisions backendmodel.ImportChangesPage
	h.call(t, "get_backend_import_changes", map[string]any{"importId": c.ID, "previewVersion": preview.Version, "recordType": "migration"}, nil, &decisions)
	if len(decisions.Items) != 1 || decisions.Items[0].Migration == nil || decisions.Items[0].Migration.FromSnapshotID != b.SnapshotID {
		t.Fatal("migration decision lost exact old snapshot")
	}
	h.commit(t, &c, preview)
	vector := h.coverage(t, backendmodel.BackendReadTarget{RevisionID: h.project.CurrentRevisionID})
	if len(vector.Partitions) != 3 || b41Partition(t, vector, a.RepositoryID, "provider-b").SnapshotID != b.SnapshotID {
		t.Fatal("migration retired or rewrote the original provider")
	}
	h.readOnly(t, func() { h.replay(t, oldProof) })
	b41SDKCrossRepositoryCurrentness(t, h)
}

func b41SDKCrossRepositoryCurrentness(t *testing.T, h *b41SDKExamples) {
	t.Helper()
	remote, remoteID := h.importHandler(t, "remote", "provider-remote", "package remote\n\nfunc Remote() {}\n", "remote", "Remote")
	remoteClaims := h.assertions(t, backendmodel.BackendReadTarget{RevisionID: h.project.CurrentRevisionID}, "node", remoteID)
	if len(remoteClaims.Items) != 1 {
		t.Fatal("remote declaration did not have one qualified claim")
	}
	ref := b41BaseRef(remoteClaims.Items[0].Assertion)
	caller := h.begin(t, b41SourceInput(h.project, "caller", "provider-caller", "package caller\nimport \"remote\"\nfunc Caller() { remote.Remote() }\n", true))
	commands := b41NodeCommands(caller, b41DeclaredNode{"caller", "Caller", 3})
	proof := *commands[1].Evidence
	proof.ExternalKey, proof.SubjectType, proof.SubjectKey = "proof-call", "edge", "remote-call"
	commands = append(commands, backendmodel.ImportCommand{Op: "upsert_edge", Edge: &backendmodel.ImportEdge{ExternalKey: "remote-call", Kind: "calls", FromRef: &backendmodel.ImportRecordRef{LocalKey: "caller"}, ToRef: &backendmodel.ImportRecordRef{Base: &ref}, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof-call"}}}, backendmodel.ImportCommand{Op: "upsert_evidence", Evidence: &proof})
	batch := h.batch(t, &caller, commands)
	callerID, edgeID := b41Identity(t, batch, "node", "caller"), b41Identity(t, batch, "edge", "remote-call")
	preview := h.preview(t, &caller)
	if preview.CandidateHash == nil {
		t.Fatalf("cross-repository call failed: %+v", preview)
	}
	pin := b41Candidate(caller, preview)
	b41SDKCandidateReads(t, h, pin, callerID, edgeID)
	h.batch(t, &caller, commands)
	h.reject(t, "get_backend_coverage", pin, nil, "HTTP 409")
	preview = h.preview(t, &caller)
	pin = b41Candidate(caller, preview)
	h.commit(t, &caller, preview)
	h.reject(t, "get_backend_coverage", pin, nil, "HTTP 409")
	before := h.assertions(t, backendmodel.BackendReadTarget{RevisionID: h.project.CurrentRevisionID}, "edge", edgeID)
	proofBytes := h.call(t, "get_backend_evidence", map[string]any{"revisionId": h.project.CurrentRevisionID, "subjectId": edgeID}, nil, nil)
	var oldEvidence struct {
		Items []jsontext.Value `json:"items"`
	}
	if err := json.Unmarshal(proofBytes.response, &oldEvidence); err != nil {
		t.Fatal(err)
	}
	if len(before.Items) != 1 || before.Items[0].Assertion.Payload.To != remoteID {
		t.Fatal("cross-repository target UUID changed")
	}

	change := b41SourceInput(h.project, "remote", "provider-remote", "package remote\n\nfunc RemoteV2() {}\n", true)
	change.SourceScope = &backendmodel.SourceScope{Kind: "reconcile", RepositoryID: remote.RepositoryID, ProviderNamespace: "provider-remote"}
	next := h.begin(t, change)
	h.batch(t, &next, b41NodeCommands(next, b41DeclaredNode{"remote", "RemoteV2", 3}))
	h.commit(t, &next, h.preview(t, &next))
	after := h.assertions(t, backendmodel.BackendReadTarget{RevisionID: h.project.CurrentRevisionID}, "edge", edgeID)
	if len(after.Items) != 1 || after.Items[0].Currentness.Own.Status != "current" || after.Items[0].Currentness.Dependency.Status != "stale" {
		t.Fatalf("dependency change refreshed caller proof: %+v", after.Items)
	}
	staleEndpoints := false
	for _, field := range after.Items[0].Currentness.Fields {
		if field.Property.Kind == "edge_endpoints" && field.Dependency.Status == "stale" {
			staleEndpoints = true
		}
	}
	if !staleEndpoints {
		t.Fatal("changed remote claim did not mark the dependent endpoint field stale")
	}
	h.readOnly(t, func() {
		h.replay(t, proofBytes)
		var currentEvidence struct {
			Items []jsontext.Value `json:"items"`
		}
		h.call(t, "get_backend_evidence", map[string]any{"revisionId": h.project.CurrentRevisionID, "subjectId": edgeID}, nil, &currentEvidence)
		if !bytes.Equal(b41JSON(t, oldEvidence.Items), b41JSON(t, currentEvidence.Items)) || !slices.Equal(before.Items[0].Assertion.EvidenceIDs, after.Items[0].Assertion.EvidenceIDs) {
			t.Fatal("foreign dependency change rewrote retained caller proof")
		}
	})
}

func b41SDKCandidateReads(t *testing.T, h *b41SDKExamples, pin backendmodel.BackendReadTarget, nodeID, edgeID string) {
	t.Helper()
	var foreign backendmodel.Project
	h.call(t, "create_backend_project", backendmodel.CreateInput{Name: "Foreign scope", IdempotencyKey: h.key("foreign-project")}, nil, &foreign)
	h.readOnly(t, func() {
		h.call(t, "query_backend_graph", pin, map[string]any{"recordType": "nodes"}, nil)
		h.call(t, "get_backend_node", pin, map[string]any{"nodeId": nodeID}, nil)
		h.call(t, "get_backend_evidence", pin, map[string]any{"subjectId": edgeID}, nil)
		h.coverage(t, pin)
		h.assertions(t, pin, "edge", edgeID)
		h.reject(t, "get_backend_node", pin, map[string]any{"nodeId": uuid.NewV7().String()}, "HTTP 404")
		h.reject(t, "get_backend_coverage", backendmodel.BackendReadTarget{RevisionID: foreign.CurrentRevisionID}, nil, "HTTP 404")
		h.reject(t, "get_backend_coverage", pin, map[string]any{"projectId": foreign.ID}, "HTTP 404")
		h.reject(t, "get_backend_coverage", pin, map[string]any{"revisionId": h.project.CurrentRevisionID}, "invalid arguments")
		h.reject(t, "query_backend_flow", pin, map[string]any{"view": "entrypoints"}, "HTTP 422")
		h.reject(t, "create_backend_saved_view", map[string]any{"documentVersion": "saved-view-v2", "name": "Staging cannot persist", "target": pin, "state": b41FlowView(), "idempotencyKey": h.key("invalid-saved")}, nil, "invalid arguments")
	})
}

func b41FlowView() backendmodel.SavedViewState {
	return backendmodel.SavedViewState{Flow: &backendmodel.SavedFlowViewState{Kind: "flow", Positions: []backendmodel.SavedViewPosition{}, CollapsedGroupIDs: []string{}}}
}

func (h *b41SDKExamples) apply(t *testing.T, draft *backendmodel.ChangeProposalDetail, commands []backendmodel.ChangeProposalCommand) b41SDKReceipt {
	t.Helper()
	in := backendmodel.PreviewChangeProposalInput{ExpectedVersion: draft.Proposal.Version, ProposalRevisionID: draft.Revision.ID, Commands: commands}
	var preview backendmodel.ChangeProposalCandidate
	h.call(t, "preview_backend_change_proposal_commands", in, map[string]any{"proposalId": draft.Proposal.ID}, &preview)
	if preview.CandidateHash == nil || preview.ProposalRevisionID != draft.Revision.ID || preview.BaseRevisionID != draft.Revision.BaseRevisionID || preview.BaseSemanticHash != draft.Revision.BaseSemanticHash {
		t.Fatalf("full proposal candidate is invalid or changed pins: %+v", preview)
	}
	var applied backendmodel.ChangeProposalApplyResult
	receipt := h.call(t, "apply_backend_change_proposal_commands", backendmodel.ApplyChangeProposalInput{ExpectedVersion: in.ExpectedVersion, ProposalRevisionID: in.ProposalRevisionID, Commands: commands, CandidateHash: *preview.CandidateHash, IdempotencyKey: h.key("apply")}, map[string]any{"proposalId": draft.Proposal.ID}, &applied)
	if applied.Revision.BaseRevisionID != draft.Revision.BaseRevisionID || applied.Revision.BaseSemanticHash != draft.Revision.BaseSemanticHash {
		t.Fatal("full edit changed its immutable baseline")
	}
	draft.Proposal, draft.Revision = applied.Proposal, applied.Revision
	return receipt
}

func b41FullTarget(draft backendmodel.ChangeProposalDetail) backendmodel.BackendReadTarget {
	return backendmodel.BackendReadTarget{ChangeProposal: &backendmodel.ProposalReadTarget{ProposalID: draft.Proposal.ID, ProposalRevisionID: draft.Revision.ID}}
}

func (h *b41SDKExamples) identities(t *testing.T, target backendmodel.BackendReadTarget) []backendmodel.EffectiveIdentity {
	t.Helper()
	var page struct {
		Identities []backendmodel.EffectiveIdentity `json:"identities"`
		NextCursor string                           `json:"nextCursor"`
	}
	h.call(t, "query_backend_graph", target, map[string]any{"recordType": "nodes", "limit": 100}, &page)
	if page.NextCursor != "" {
		t.Fatal("small identity fixture unexpectedly paged")
	}
	return page.Identities
}

func b41DesiredNode(t *testing.T, id, kind, name string, parent *string, attrs map[string]any) backendmodel.ChangeProposalCommand {
	t.Helper()
	attributes := map[string]jsontext.Value{}
	for key, value := range attrs {
		attributes[key] = b41JSON(t, value)
	}
	return backendmodel.ChangeProposalCommand{Type: "create_node", CommandID: uuid.NewV7().String(), Reason: "Desired structure only; no provider proof", ID: id, Kind: kind, Name: name, ParentID: parent, Attributes: attributes}
}

func b41DesiredContains(from, to string) backendmodel.ChangeProposalCommand {
	return backendmodel.ChangeProposalCommand{Type: "upsert_edge", CommandID: uuid.NewV7().String(), Reason: "Exact desired containment", ID: uuid.NewV7().String(), Kind: "contains", From: from, To: to, Attributes: map[string]jsontext.Value{}}
}

func b41Rename(id, name string) backendmodel.ChangeProposalCommand {
	return backendmodel.ChangeProposalCommand{Type: "rename", CommandID: uuid.NewV7().String(), Reason: "Reviewed desired name", RecordType: "node", ID: id, Name: name}
}

func TestBackendB41SDKChangeLedgerAndSavedViewExamples(t *testing.T) {
	t.Parallel()
	h := newB41SDKExamples(t)
	a, sharedID := h.importHandler(t, "primary", "provider-a", "package example\n\nfunc Handle() {}\n", "handler-a", "Handle")
	b := h.addSharedProvider(t, a, sharedID, "Handle")
	h.commit(t, &b, h.preview(t, &b))
	sourceRevision := h.project.CurrentRevisionID
	oldSource := h.call(t, "get_backend_node", map[string]any{"revisionId": sourceRevision, "nodeId": sharedID}, nil, nil)
	var draft backendmodel.ChangeProposalDetail
	created := h.call(t, "create_backend_change_proposal", backendmodel.CreateChangeProposalInput{Name: "Typed desired model", BaseRevisionID: sourceRevision, IdempotencyKey: h.key("proposal")}, nil, &draft)
	initialDraft := draft.Revision.ID
	identities := h.identities(t, b41FullTarget(draft))
	var chosen backendmodel.EffectiveIdentity
	for _, item := range identities {
		if item.Target.Source != nil && item.Target.Source.ProviderNamespace == "provider-a" {
			chosen = item
		}
	}
	if len(identities) != 2 || chosen.Target.Source == nil || chosen.ExternalKey == nil || *chosen.ExternalKey != "handler-a" {
		t.Fatal("qualified source identities were collapsed")
	}
	b41SDKRejectIdentityChoices(t, h, draft, chosen)
	fieldID, moduleID, dtoID := uuid.NewV7().String(), uuid.NewV7().String(), uuid.NewV7().String()
	commands := []backendmodel.ChangeProposalCommand{
		{Type: "map_identity", CommandID: uuid.NewV7().String(), Reason: "Only provider-a gets this intended alias", Target: &chosen.Target, ExpectedExternalKey: chosen.ExternalKey, NewExternalKey: "desired-handler-a"},
		b41DesiredNode(t, moduleID, "module", "Planned module", nil, map[string]any{}),
		b41DesiredNode(t, dtoID, "dto", "Planned DTO", new(moduleID), map[string]any{"qualifiedName": "example.PlannedDTO", "analysisStatus": "complete", "gaps": []string{}}),
		b41DesiredContains(moduleID, dtoID),
		b41DesiredNode(t, fieldID, "representation_field", "value", new(dtoID), map[string]any{"selector": []map[string]string{{"property": "value"}}, "nativeType": map[string]any{"status": "known", "value": "string"}, "nullable": map[string]any{"status": "known", "value": false}, "cardinality": map[string]any{"status": "known", "value": "one"}, "analysisStatus": "complete", "gaps": []string{}}),
		b41DesiredContains(dtoID, fieldID),
		{Type: "map_identity", CommandID: uuid.NewV7().String(), Reason: "First key for a created desired field", Target: &backendmodel.ChangeIdentityTarget{Kind: "intent_identity", RecordType: "node", ID: fieldID}, ExpectedExternalKey: nil, NewExternalKey: "planned.value"},
	}
	h.apply(t, &draft, commands)
	b41SDKCheckDesiredIdentities(t, h, draft, sharedID, fieldID)
	var view backendmodel.SavedView
	viewCreate := h.call(t, "create_backend_saved_view", backendmodel.CreateSavedViewInput{DocumentVersion: backendmodel.SavedViewV2DocumentVersion, Name: "Exact desired presentation", Target: b41FullTarget(draft), State: b41FlowView(), IdempotencyKey: h.key("view")}, nil, &view)
	oldView := h.call(t, "get_backend_saved_view", map[string]any{"viewId": view.ID, "version": view.Version}, nil, nil)
	viewDraft := draft.Revision.ID
	b41SDKLedgerHistory(t, h, &draft, initialDraft, sharedID, fieldID)
	var saved backendmodel.SavedView
	viewSave := h.call(t, "save_backend_saved_view", backendmodel.SaveSavedViewInput{DocumentVersion: backendmodel.SavedViewV2DocumentVersion, Name: "Same immutable desired target", State: view.State, ExpectedVersion: view.Version, IdempotencyKey: h.key("view-save")}, map[string]any{"viewId": view.ID}, &saved)
	if saved.Target.ChangeProposal == nil || saved.Target.ChangeProposal.ProposalRevisionID != viewDraft || saved.Pins.Effective == nil || saved.Pins.Effective.BaseRevisionID != sourceRevision {
		t.Fatal("presentation followed newer draft/source head")
	}
	h.readOnly(t, func() { h.replay(t, oldSource); h.replay(t, oldView) })
	h.replay(t, created)
	h.replay(t, viewCreate)
	h.replay(t, viewSave)
	h.refresh(t)
	if h.project.CurrentRevisionID != sourceRevision {
		t.Fatal("proposal, restore or presentation changed source head")
	}
}

func b41SDKRejectIdentityChoices(t *testing.T, h *b41SDKExamples, draft backendmodel.ChangeProposalDetail, chosen backendmodel.EffectiveIdentity) {
	t.Helper()
	wrong := *chosen.Target.Source
	wrong.ProviderNamespace = "wrong-owner"
	command := backendmodel.ChangeProposalCommand{Type: "map_identity", CommandID: uuid.NewV7().String(), Reason: "Wrong owner must fail", Target: &backendmodel.ChangeIdentityTarget{Kind: "source_identity", Source: &wrong}, ExpectedExternalKey: chosen.ExternalKey, NewExternalKey: "rejected"}
	in := backendmodel.PreviewChangeProposalInput{ExpectedVersion: draft.Proposal.Version, ProposalRevisionID: draft.Revision.ID, Commands: []backendmodel.ChangeProposalCommand{command}}
	h.readOnly(t, func() {
		h.reject(t, "preview_backend_change_proposal_commands", in, map[string]any{"proposalId": draft.Proposal.ID}, "HTTP")
		h.reject(t, "preview_backend_change_proposal_commands", map[string]any{"expectedVersion": draft.Proposal.Version, "proposalRevisionId": draft.Revision.ID, "commands": []any{map[string]any{"type": "map_identity", "commandId": uuid.NewV7().String(), "reason": "No inferred owner", "target": map[string]any{"kind": "source_identity", "source": map[string]any{"recordType": "node", "id": wrong.ID}}, "expectedExternalKey": "handler-a", "newExternalKey": "rejected"}}}, map[string]any{"proposalId": draft.Proposal.ID}, "invalid arguments")
	})
}

func b41SDKCheckDesiredIdentities(t *testing.T, h *b41SDKExamples, draft backendmodel.ChangeProposalDetail, sharedID, fieldID string) {
	t.Helper()
	h.readOnly(t, func() {
		keys := map[string]string{}
		for _, item := range h.identities(t, b41FullTarget(draft)) {
			if item.Target.Source != nil {
				keys[item.Target.Source.ProviderNamespace] = *item.ExternalKey
			} else if item.Target.ID == fieldID {
				if item.ExternalKey == nil || *item.ExternalKey != "planned.value" {
					t.Fatal("first explicit-null identity assignment failed")
				}
			}
		}
		if !maps.Equal(keys, map[string]string{"provider-a": "desired-handler-a", "provider-b": "handler-b"}) {
			t.Fatalf("another source identity changed: %v", keys)
		}
		claims := h.assertions(t, b41FullTarget(draft), "node", sharedID)
		baselineKeys := map[string]string{}
		for _, claim := range claims.Items {
			baselineKeys[claim.Assertion.Owner.ProviderNamespace] = claim.Assertion.ExternalKey
		}
		if claims.Basis != "baseline" || !maps.Equal(baselineKeys, map[string]string{"provider-a": "handler-a", "provider-b": "handler-b"}) {
			t.Fatal("intended key rewrote baseline claims")
		}
		var evidence struct {
			Items    []jsontext.Value                      `json:"items"`
			Baseline []backendmodel.EffectiveEvidenceBasis `json:"baselineEvidence"`
		}
		h.call(t, "get_backend_evidence", b41FullTarget(draft), map[string]any{"subjectId": fieldID}, &evidence)
		if len(evidence.Items) != 0 || len(evidence.Baseline) != 0 {
			t.Fatal("new desired field acquired fabricated provider/baseline evidence")
		}
		h.call(t, "get_backend_node", b41FullTarget(draft), map[string]any{"nodeId": fieldID}, nil)
		h.call(t, "query_backend_graph", b41FullTarget(draft), map[string]any{"recordType": "nodes"}, nil)
		h.coverage(t, b41FullTarget(draft))
	})
}

func b41SDKLedgerHistory(t *testing.T, h *b41SDKExamples, draft *backendmodel.ChangeProposalDetail, initialDraft, sharedID, fieldID string) {
	t.Helper()
	noOp := b41Rename(sharedID, "Handle")
	beforeHash := draft.Revision.SemanticHash
	noOpReceipt := h.apply(t, draft, []backendmodel.ChangeProposalCommand{noOp})
	if draft.Revision.SemanticHash != beforeHash {
		t.Fatal("semantic no-op changed effective meaning")
	}
	criteria := backendmodel.ChangeProposalCommand{Type: "set_criteria", CommandID: uuid.NewV7().String(), Reason: "Runtime behavior still requires separate observation", Criteria: []backendmodel.ChangeCriterion{{Key: "runtime", Kind: "runtime_check", Required: false, Description: "Observe behavior separately", TargetIDs: []string{sharedID}}}}
	criteriaReceipt := h.apply(t, draft, []backendmodel.ChangeProposalCommand{criteria})
	first, last := b41Rename(sharedID, "Intermediate"), b41Rename(sharedID, "Final")
	overwrittenReceipt := h.apply(t, draft, []backendmodel.ChangeProposalCommand{first, last})
	oldDraft := draft.Revision.ID
	var oldNode struct {
		Node struct {
			Name string `json:"name"`
		} `json:"node"`
	}
	oldRead := h.call(t, "get_backend_node", b41FullTarget(*draft), map[string]any{"nodeId": sharedID}, &oldNode)
	if oldNode.Node.Name != "Final" {
		t.Fatal("ordered last write did not determine desired name")
	}
	h.apply(t, draft, []backendmodel.ChangeProposalCommand{b41Rename(fieldID, "Renamed later")})
	var restored backendmodel.ChangeProposalApplyResult
	restoreReceipt := h.call(t, "restore_backend_change_proposal", backendmodel.RestoreChangeProposalInput{ExpectedVersion: draft.Proposal.Version, ProposalRevisionID: draft.Revision.ID, RestoreRevisionID: initialDraft, IdempotencyKey: h.key("restore")}, map[string]any{"proposalId": draft.Proposal.ID}, &restored)
	draft.Proposal, draft.Revision = restored.Proposal, restored.Revision
	if err := h.db.Close(); err != nil {
		t.Fatal(err)
	}
	h.server, h.db = newResourcesTestServer(t, h.cfg)
	h.tools = newToolFixture(h.server)
	h.readOnly(t, func() {
		h.replay(t, oldRead)
		var history backendmodel.ChangeProposalDetail
		h.call(t, "get_backend_change_proposal", map[string]any{"proposalId": draft.Proposal.ID, "proposalRevisionId": oldDraft}, nil, &history)
		if history.Revision.ID != oldDraft || history.Revision.BaseRevisionID != draft.Revision.BaseRevisionID {
			t.Fatal("history followed the new aggregate head")
		}
	})
	for _, command := range []backendmodel.ChangeProposalCommand{noOp, criteria, first, last} {
		// A consumed ID cannot obtain a new Preview. Reuse a real earlier candidate
		// pin and valid current CAS/draft, then require the ledger-specific refusal.
		var original struct {
			CandidateHash string `json:"candidateHash"`
		}
		if err := json.Unmarshal([]byte(noOpReceipt.request), &original); err != nil {
			t.Fatal(err)
		}
		h.reject(t, "apply_backend_change_proposal_commands", backendmodel.ApplyChangeProposalInput{ExpectedVersion: draft.Proposal.Version, ProposalRevisionID: draft.Revision.ID, Commands: []backendmodel.ChangeProposalCommand{command}, CandidateHash: original.CandidateHash, IdempotencyKey: h.key("consumed-command")}, map[string]any{"proposalId": draft.Proposal.ID}, "backend_change_command_conflict")
	}
	for _, receipt := range []b41SDKReceipt{noOpReceipt, criteriaReceipt, overwrittenReceipt, restoreReceipt} {
		h.replay(t, receipt)
	}
	for _, accepted := range []struct {
		receipt  b41SDKReceipt
		commands []backendmodel.ChangeProposalCommand
	}{{noOpReceipt, []backendmodel.ChangeProposalCommand{noOp}}, {criteriaReceipt, []backendmodel.ChangeProposalCommand{criteria}}, {overwrittenReceipt, []backendmodel.ChangeProposalCommand{first, last}}} {
		var applied backendmodel.ChangeProposalApplyResult
		if err := json.Unmarshal(accepted.receipt.response, &applied); err != nil {
			t.Fatal(err)
		}
		var raw string
		// Store27 (48dce80, B6.3): the batch payload is read through the
		// owner's _documents view; the table itself holds only the blob key.
		if err := h.db.R.QueryRowContext(t.Context(), `SELECT commands FROM backend_change_proposal_batches_documents WHERE proposal_id=? AND revision_id=?`, applied.Proposal.ID, applied.Revision.ID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		stored, expected := jsontext.Value(raw), b41JSON(t, accepted.commands)
		if err := stored.Canonicalize(); err != nil {
			t.Fatal(err)
		}
		if err := expected.Canonicalize(); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(stored, expected) {
			t.Fatalf("accepted command history lost original order/content: got=%s want=%s", stored, expected)
		}
	}
}

const b41PriorDatabaseHash = "3b8ff09b51a1deccf64fd0dd67c89f9736c0d715e864e0b400e5e5b258c0f3ee"
const b41PriorBinaryHash = "a5947af3e0104bd43339028022012896390b2a9e79c8c903c9e99efd1b59b558"

type b41PriorProof struct {
	RecordType         string            `json:"recordType"`
	RecordID           string            `json:"recordId"`
	EvidenceID         string            `json:"evidenceId"`
	PropertyPath       *string           `json:"propertyPath"`
	Support            string            `json:"support"`
	OriginalRevisionID string            `json:"originalSeedRevisionId"`
	OriginalHashes     map[string]string `json:"originalSeedRawHashes"`
	BaseHashes         map[string]string `json:"bootstrapBaseRawHashes"`
}

type b41PriorOracle struct {
	BinaryHash       string          `json:"priorBinarySHA256"`
	StoreVersion     int             `json:"storeVersion"`
	DatabaseHash     string          `json:"databaseSHA256"`
	GZIPHash         string          `json:"gzipSHA256"`
	ProjectID        string          `json:"projectId"`
	BaseRevisionID   string          `json:"bootstrapBaseRevisionId"`
	BaseSemanticHash string          `json:"bootstrapBaseSemanticHash"`
	Proofs           []b41PriorProof `json:"proofs"`
	InheritedProofs  []b41PriorProof `json:"inheritedProofs"`
}

func newB41PriorSDKExamples(t *testing.T) (*b41SDKExamples, b41PriorOracle) {
	t.Helper()
	manifest, err := os.ReadFile("testdata/b41/prior-store19.json")
	if err != nil {
		t.Fatal(err)
	}
	var oracle b41PriorOracle
	if err := json.Unmarshal(manifest, &oracle); err != nil {
		t.Fatal(err)
	}
	compressed, err := os.ReadFile("testdata/b41/prior-store19.db.gz")
	if err != nil {
		t.Fatal(err)
	}
	if oracle.BinaryHash != b41PriorBinaryHash || oracle.DatabaseHash != b41PriorDatabaseHash || oracle.StoreVersion != 19 || len(oracle.Proofs) != 7 || b41Digest(compressed) != oracle.GZIPHash {
		t.Fatal("prior-binary oracle/provenance changed")
	}
	reader, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(io.LimitReader(reader, 32<<20))
	if err != nil {
		t.Fatal(err)
	}
	if err := reader.Close(); err != nil {
		t.Fatal(err)
	}
	if b41Digest(data) != b41PriorDatabaseHash {
		t.Fatal("decompressed oracle is not the exact prior Store19 database")
	}
	cfg := resourcesTestConfig(t)
	if err := os.WriteFile(cfg.DBPath(), data, 0o600); err != nil {
		t.Fatal(err)
	}
	server, db := newResourcesTestServer(t, cfg)
	h := &b41SDKExamples{server: server, db: db, cfg: cfg, tools: newToolFixture(server)}
	h.recordTranscript(t)
	h.negotiate(t)
	h.call(t, "get_backend_project", map[string]any{"projectId": oracle.ProjectID}, nil, &h.project)
	if h.project.CurrentRevisionID != oracle.BaseRevisionID {
		t.Fatal("migration changed old project head")
	}
	return h, oracle
}

func b41CheckPriorRaw(t *testing.T, h *b41SDKExamples, oracle b41PriorOracle) {
	t.Helper()
	for _, proof := range append(slices.Clone(oracle.Proofs), oracle.InheritedProofs...) {
		for _, pin := range []struct {
			revision string
			hashes   map[string]string
		}{{proof.OriginalRevisionID, proof.OriginalHashes}, {oracle.BaseRevisionID, proof.BaseHashes}} {
			// Since Store27 (48dce80, B6.3) the payload column of each immutable
			// owner lives in backend_payload_blobs; the owner's _documents view
			// reassembles the exact stored bytes, so the raw-hash pins still
			// compare against what was written by the prior Store19 binary.
			queries := []struct {
				kind, query string
				args        []any
			}{
				{"revision", `SELECT document FROM backend_revisions_documents WHERE project_id=? AND id=?`, []any{oracle.ProjectID, pin.revision}},
				{"source", `SELECT document FROM backend_revision_sources_documents WHERE revision_id=?`, []any{pin.revision}},
				{"subject", `SELECT document FROM backend_graph_records_documents WHERE project_id=? AND revision_id=? AND record_type=? AND id=?`, []any{oracle.ProjectID, pin.revision, proof.RecordType, proof.RecordID}},
				{"evidence", `SELECT document FROM backend_graph_records_documents WHERE project_id=? AND revision_id=? AND record_type='evidence' AND id=?`, []any{oracle.ProjectID, pin.revision, proof.EvidenceID}},
			}
			for _, query := range queries {
				var raw string
				if err := h.db.R.QueryRowContext(t.Context(), query.query, query.args...).Scan(&raw); err != nil {
					t.Fatal(err)
				}
				if b41Digest([]byte(raw)) != pin.hashes[query.kind] {
					t.Fatalf("prior %s raw bytes changed at %s", query.kind, pin.revision)
				}
			}
		}
	}
}

func TestBackendB41SDKPriorBinaryMetadataExamples(t *testing.T) {
	t.Parallel()
	h, oracle := newB41PriorSDKExamples(t)
	b41CheckPriorRaw(t, h, oracle)
	retained := b41PriorRows(t, h)
	input := b41SourceInput(h.project, "sdk-oracle-addition", "sdk-new-provider", "package fresh\n\nfunc Fresh() {}\n", true)
	input.ProfileExtension = &backendmodel.ImportProfileExtension{FromProfile: backendmodel.EventsProfile, ToProfile: backendmodel.ComposedProfile}
	session := h.begin(t, input)
	h.batch(t, &session, b41NodeCommands(session, b41DeclaredNode{"fresh", "Fresh", 3}))
	preview := h.preview(t, &session)
	if preview.CandidateHash == nil {
		t.Fatalf("actual prior-binary bootstrap failed: %+v", preview)
	}
	proofs := b41SDKCheckLegacyProofs(t, h, oracle, b41Candidate(session, preview))
	h.commit(t, &session, preview)
	source := backendmodel.BackendReadTarget{RevisionID: h.project.CurrentRevisionID}
	b41SDKCheckLegacyProofs(t, h, oracle, source)
	var draft backendmodel.ChangeProposalDetail
	h.call(t, "create_backend_change_proposal", backendmodel.CreateChangeProposalInput{Name: "Actual prior proof baseline", BaseRevisionID: h.project.CurrentRevisionID, IdempotencyKey: h.key("prior-proposal")}, nil, &draft)
	b41SDKCheckLegacyProofs(t, h, oracle, b41FullTarget(draft))
	b41CheckPriorRaw(t, h, oracle)
	for table, rows := range retained {
		_, current := h.tableRows(t, table)
		for _, row := range rows {
			if _, found := slices.BinarySearch(current, row); !found {
				t.Fatalf("bootstrap changed or deleted an existing history/receipt row in %s", table)
			}
		}
	}
	metadataIndex := slices.IndexFunc(proofs, func(basis backendmodel.LegacyProofBasis) bool { return basis.Support == "historical_metadata" })
	if metadataIndex < 0 {
		t.Fatal("actual old metadata witness was lost")
	}
	b41SDKFreshProofRefusal(t, h, proofs[metadataIndex])
	// Deliberately corrupt only the temporary upgraded copy. JSON meaning stays
	// equal, but a retained raw byte/hash can no longer verify successfully.
	// Store27 (48dce80, B6.3) moved the payload behind a sealed manifest with an
	// immutable-owner trigger, so the edit runs on the Store26 fixture shape and
	// the production migration republishes the bytes undecoded — the appended
	// space survives, which is the whole point of the corruption.
	_, err := testkit.EditLegacyBackendPayload(t.Context(), h.db, `UPDATE backend_graph_records SET document=document||' ' WHERE revision_id=? AND record_type='evidence' AND id=?`, oracle.BaseRevisionID, oracle.Proofs[0].EvidenceID)
	if err != nil {
		t.Fatal(err)
	}
	h.reject(t, "get_backend_evidence", source, map[string]any{"evidenceId": oracle.Proofs[0].EvidenceID}, "")
}

func b41PriorRows(t *testing.T, h *b41SDKExamples) map[string][]string {
	t.Helper()
	// The project's live CAS/head legitimately advances. Every old immutable,
	// import, proposal and SavedView row must retain its exact encoded bytes.
	retained := map[string][]string{}
	for table := range h.rows(t) {
		if table == "backend_projects" {
			continue
		}
		_, retained[table] = h.tableRows(t, table)
	}
	return retained
}

func b41SDKCheckLegacyProofs(t *testing.T, h *b41SDKExamples, oracle b41PriorOracle, target backendmodel.BackendReadTarget) []backendmodel.LegacyProofBasis {
	t.Helper()
	var page struct {
		Items  []backendmodel.Evidence         `json:"items"`
		Source *backendmodel.SourceReadContext `json:"source"`
	}
	h.readOnly(t, func() {
		h.call(t, "get_backend_evidence", target, map[string]any{"subjectId": oracle.Proofs[0].RecordID}, &page)
		expectedProofs := append(slices.Clone(oracle.Proofs), oracle.InheritedProofs...)
		if page.Source == nil || len(page.Source.LegacyProofBases) != len(expectedProofs) {
			t.Fatalf("prior proof context incomplete: %+v", page.Source)
		}
		for _, expected := range expectedProofs {
			index := slices.IndexFunc(page.Source.LegacyProofBases, func(b backendmodel.LegacyProofBasis) bool { return b.EvidenceID == expected.EvidenceID })
			if index < 0 {
				t.Fatal("missing prior proof", expected.EvidenceID)
			}
			basis := page.Source.LegacyProofBases[index]
			if basis.SourceRevisionID != oracle.BaseRevisionID || basis.SourceSchemaVersion != "5" || basis.ProjectID != oracle.ProjectID || basis.RecordID != expected.RecordID || basis.Support != expected.Support || basis.SourceSemanticHash != oracle.BaseSemanticHash {
				t.Fatalf("legacy support was promoted or retargeted: %+v", basis)
			}
			actual := map[string]string{"revision": basis.RevisionDocumentHash, "source": basis.SourceDocumentHash, "subject": basis.SubjectDocumentHash, "evidence": basis.EvidenceDocumentHash}
			if !maps.Equal(actual, expected.BaseHashes) {
				t.Fatalf("basis lost independent old byte hashes: %v", actual)
			}
			if basis.Support == "historical_metadata" && basis.Property != nil || basis.Support == "legacy_record" && basis.Property != nil {
				t.Fatal("metadata/record proof invented a semantic property")
			}
			if basis.Support == "legacy_semantic" && (basis.Property == nil || basis.Property.Kind != "name") {
				t.Fatal("old name proof lost its typed scope")
			}
		}
		claims := h.assertions(t, target, "node", oracle.Proofs[0].RecordID)
		if len(claims.Items) != 1 {
			t.Fatal("prior service claim changed identity")
		}
		var coverage struct {
			Coverage backendmodel.Coverage `json:"coverage"`
		}
		h.call(t, "get_backend_coverage", target, nil, &coverage)
		// This old service also has genuine broad record proof. Metadata witnesses
		// remain metadata-only, but must not erase that independent semantic support.
		if claims.Items[0].Currentness.Own.Status != "current" {
			t.Fatal("metadata witnesses erased genuine retained broad proof")
		}
	})
	return page.Source.LegacyProofBases
}

func b41SDKFreshProofRefusal(t *testing.T, h *b41SDKExamples, copied backendmodel.LegacyProofBasis) {
	t.Helper()
	session := h.begin(t, b41SourceInput(h.project, "fresh-metadata-rejected", "fresh-invalid", "package fresh\n\nfunc Fresh() {}\n", true))
	commands := b41NodeCommands(session, b41DeclaredNode{"fresh", "Fresh", 3})
	commands[1].Evidence.PropertyPath = new("/id")
	h.batch(t, &session, commands)
	h.readOnly(t, func() {
		h.reject(t, "preview_backend_import", backendmodel.PreviewImportInput{ExpectedImportVersion: session.Version, BaseRevisionID: session.BaseRevisionID}, map[string]any{"importId": session.ID}, "evidence.propertyPath")
	})
	hash, err := backendmodel.ImportBatchHash([]backendmodel.ImportCommand{commands[1]})
	if err != nil {
		t.Fatal(err)
	}
	var forged map[string]any
	if err := json.Unmarshal(b41JSON(t, commands[1].Evidence), &forged); err != nil {
		t.Fatal(err)
	}
	forged["legacyProofBasis"] = copied
	h.readOnly(t, func() {
		h.reject(t, "put_backend_import_batch", map[string]any{"expectedImportVersion": session.Version, "payloadHash": hash, "commands": []any{map[string]any{"op": "upsert_evidence", "evidence": forged}}}, map[string]any{"importId": session.ID, "batchId": h.key("copied-basis")}, "legacyProofBasis")
	})
}

func TestBackendB41SDKIncrementalExample(t *testing.T) {
	t.Parallel()
	h := newB41SDKExamples(t)
	_, remoteID := h.importHandler(t, "incremental-remote", "provider-remote", "package remote\n\nfunc Remote() {}\n", "remote", "Remote")
	remote := h.assertions(t, backendmodel.BackendReadTarget{RevisionID: h.project.CurrentRevisionID}, "node", remoteID)
	if len(remote.Items) != 1 {
		t.Fatal("missing independently owned remote claim")
	}
	remoteRef := b41BaseRef(remote.Items[0].Assertion)
	const oldSource = "package example\nimport \"remote\"\nfunc Handle() { remote.Remote() }\n"
	const newSource = "package example\nimport \"remote\"\nfunc HandleV2() { remote.Remote() }\n"
	const spareSource = "package example\n\nfunc Spare() {}\n"
	input := b41SourceInput(h.project, "incremental", "provider-a", oldSource, true)
	spareFile := backendmodel.ManifestFile{Path: "spare.go", ContentHash: b41Digest([]byte(spareSource)), FileType: "go", AnalysisStatus: "analyzed"}
	input.Manifest.Snapshot.Files = append(input.Manifest.Snapshot.Files, spareFile)
	for i := range input.Inventory {
		if input.Inventory[i].Category == "files" {
			input.Inventory[i].KnownCount = 2
			input.Inventory[i].Denominator = new(int64(2))
		}
	}
	session := h.begin(t, input)
	commands := b41NodeCommands(session, b41DeclaredNode{"handler", "Handle", 3}, b41DeclaredNode{"spare", "Spare", 3})
	commands[3].Evidence.Source.File, commands[3].Evidence.Source.ContentHash = spareFile.Path, spareFile.ContentHash
	proof := *commands[1].Evidence
	proof.ExternalKey, proof.SubjectType, proof.SubjectKey = "proof-call", "edge", "calls"
	commands = append(commands, backendmodel.ImportCommand{Op: "upsert_edge", Edge: &backendmodel.ImportEdge{ExternalKey: "calls", Kind: "calls", FromRef: &backendmodel.ImportRecordRef{LocalKey: "handler"}, ToRef: &backendmodel.ImportRecordRef{Base: &remoteRef}, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof-call"}}}, backendmodel.ImportCommand{Op: "upsert_evidence", Evidence: &proof})
	batch := h.batch(t, &session, commands)
	h.commit(t, &session, h.preview(t, &session))
	mainID, spareID, edgeID := b41Identity(t, batch, "node", "handler"), b41Identity(t, batch, "node", "spare"), b41Identity(t, batch, "edge", "calls")
	base := backendmodel.BackendReadTarget{RevisionID: h.project.CurrentRevisionID}
	spare := h.assertions(t, base, "node", spareID)
	if len(spare.Items) != 1 {
		t.Fatal("spare assertion missing")
	}
	var before struct {
		Items []jsontext.Value `json:"items"`
	}
	h.call(t, "get_backend_evidence", base, map[string]any{"subjectId": spareID}, &before)
	next := b41SourceInput(h.project, "incremental", "provider-a", newSource, true)
	next.Inventory = input.Inventory
	next.Manifest.Snapshot.Files = append(next.Manifest.Snapshot.Files, spareFile)
	next.SourceScope = &backendmodel.SourceScope{Kind: "reconcile", RepositoryID: session.RepositoryID, ProviderNamespace: "provider-a"}
	next.SyncPolicy = backendmodel.IncrementalSourcePolicy
	next.ChangeManifest = &backendmodel.ChangeManifest{Scope: "affected-subgraph", Files: []backendmodel.ChangeManifestFile{{Kind: "modified", Path: "source.go", BeforeHash: b41Digest([]byte(oldSource)), AfterHash: b41Digest([]byte(newSource))}}, AffectedRoots: []backendmodel.SourceSubjectRef{{RecordType: "node", ID: mainID}}}
	bad := next
	bad.IdempotencyKey = h.key("incremental-extension")
	bad.ProfileExtension = &backendmodel.ImportProfileExtension{FromProfile: backendmodel.EventsProfile, ToProfile: backendmodel.ComposedProfile}
	h.reject(t, "begin_backend_import", bad, nil, "")
	current := h.begin(t, next)
	commands = b41NodeCommands(current, b41DeclaredNode{"handler", "HandleV2", 3})
	proof = *commands[1].Evidence
	proof.ExternalKey, proof.SubjectType, proof.SubjectKey = "proof-call", "edge", "calls"
	commands = append(commands, backendmodel.ImportCommand{Op: "upsert_edge", Edge: &backendmodel.ImportEdge{ExternalKey: "calls", Kind: "calls", FromRef: &backendmodel.ImportRecordRef{LocalKey: "handler"}, ToRef: &backendmodel.ImportRecordRef{Base: &remoteRef}, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof-call"}}}, backendmodel.ImportCommand{Op: "upsert_evidence", Evidence: &proof})
	h.batch(t, &current, commands)
	preview := h.preview(t, &current)
	if preview.AffectedScope == nil || !slices.Contains(preview.AffectedScope.Affected, backendmodel.SourceSubjectRef{RecordType: "node", ID: mainID}) || !slices.Contains(preview.AffectedScope.Affected, backendmodel.SourceSubjectRef{RecordType: "edge", ID: edgeID}) || slices.Contains(preview.AffectedScope.Affected, backendmodel.SourceSubjectRef{RecordType: "node", ID: spareID}) {
		t.Fatalf("incremental write boundary is wrong: %+v", preview.AffectedScope)
	}
	if !slices.Contains(preview.AffectedScope.ForeignDependencies, backendmodel.SourceSubjectRef{RecordType: "node", ID: remoteID}) || slices.Contains(preview.AffectedScope.Affected, backendmodel.SourceSubjectRef{RecordType: "node", ID: remoteID}) {
		t.Fatal("foreign target was not retained as a read-only dependency")
	}
	h.commit(t, &current, preview)
	h.readOnly(t, func() {
		var after struct {
			Items []jsontext.Value `json:"items"`
		}
		h.call(t, "get_backend_evidence", backendmodel.BackendReadTarget{RevisionID: h.project.CurrentRevisionID}, map[string]any{"subjectId": spareID}, &after)
		if !bytes.Equal(b41JSON(t, before.Items), b41JSON(t, after.Items)) {
			t.Fatal("untouched proof was rewritten under incoming snapshot")
		}
		claims := h.assertions(t, backendmodel.BackendReadTarget{RevisionID: h.project.CurrentRevisionID}, "node", spareID)
		if len(claims.Items) != 1 || claims.Items[0].Currentness.Own.Status != "current" || claims.Items[0].Currentness.Own.ConfirmedSnapshotID != session.SnapshotID {
			t.Fatal("unchanged-manifest proof lost its original confirmation")
		}
	})
}
