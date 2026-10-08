package mcp

import (
	"encoding/json/v2"
	"log/slog"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/guide"
)

func TestFileImportClientUsesRealMCPAndDurableReplay(t *testing.T) {
	cfg := resourcesTestConfig(t)
	cfg.AdminHost = "127.0.0.1"
	server, db := newResourcesTestServer(t, cfg)
	repo := backendmodel.NewRepo(db)
	p, err := repo.Create(t.Context(), backendmodel.CreateInput{Name: "File import fixture", IdempotencyKey: "file-client"})
	if err != nil {
		t.Fatal(err)
	}
	installation, err := repo.InstallationID(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	endpoint := New(server, testKey, cfg, slog.Default())
	httpServer := httptest.NewServer(endpoint.Handler())
	defer httpServer.Close()
	begin := backendmodel.BeginImportInput{ExpectedVersion: p.Version, BaseRevisionID: p.CurrentRevisionID, IdempotencyKey: "file-begin", Manifest: backendmodel.SourceManifest{RepositoryName: "inert", Provider: backendmodel.SourceProvider{Name: "fixture", Version: "1", Namespace: "fixture", Method: "manual", Profiles: []string{backendmodel.GraphProfile}, Limitations: []string{"Inert unresolved fixture"}}, Snapshot: backendmodel.SnapshotManifest{Consistency: "verified", CapturedAt: time.Now().UTC(), Files: []backendmodel.ManifestFile{}}}}
	for _, category := range strings.Fields("files endpoints datastores migrations producers consumers jobs contracts tests") {
		begin.Inventory = append(begin.Inventory, backendmodel.InventoryItem{Category: category, Status: "unsupported", DiscoverySource: "fixture", Gaps: []string{}, Reason: "Not analyzed"})
	}
	dir := t.TempDir()
	planPath, journal := filepath.Join(dir, "plan.json"), filepath.Join(dir, "journal")
	plan, err := json.Marshal(map[string]any{"format": "mocker-import-transfer-v1", "projectId": p.ID, "installationId": installation, "guideSetId": guide.CurrentGuideSetID(), "begin": begin, "commandsFile": "commands.ndjson"})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(planPath, plan, 0600); err != nil {
		t.Fatal(err)
	}
	command := `{"op":"upsert_node","node":{"externalKey":"unknown","kind":"unresolved_target","name":"Unknown","attributes":{"expectedKind":"symbol","reason":"Not resolved","searchScope":"inert fixture"},"evidenceKeys":[]}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "commands.ndjson"), []byte(command), 0600); err != nil {
		t.Fatal(err)
	}
	script, err := filepath.Abs("../../scripts/backend_import_client.py")
	if err != nil {
		t.Fatal(err)
	}
	run := func(operation string, extra ...string) []byte {
		t.Helper()
		args := append([]string{script, operation, "--plan", planPath, "--journal", journal, "--url", httpServer.URL + "/mcp", "--token-env", "MOCKER_FILE_TEST_KEY"}, extra...)
		cmd := exec.CommandContext(t.Context(), "python3", args...)
		cmd.Env = append(os.Environ(), "MOCKER_FILE_TEST_KEY="+testKey)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("file client %s: %s %v", operation, out, err)
		}
		return out
	}
	run("stage")
	current, err := repo.Get(t.Context(), p.ID)
	if err != nil || current.Version != p.Version {
		t.Fatal("stage published a revision")
	}
	var binding struct {
		JournalHash string `json:"journalHash"`
	}
	if err := json.Unmarshal(run("audit-binding"), &binding); err != nil {
		t.Fatal(err)
	}
	readyBytes, err := os.ReadFile(filepath.Join(journal, "ready.json"))
	if err != nil {
		t.Fatal(err)
	}
	var ready backendmodel.ImportPreview
	if err := json.Unmarshal(readyBytes, &ready); err != nil {
		t.Fatal(err)
	}
	audit, err := json.Marshal(map[string]any{"result": "pass", "projectId": p.ID, "importId": ready.SessionID, "candidateHash": ready.CandidateHash, "previewVersion": ready.Version, "baseRevisionId": p.CurrentRevisionID, "expectedVersion": p.Version, "journalHash": binding.JournalHash, "findings": []map[string]string{{"subjectKey": "unknown", "finding": "Synthetic unresolved target preserved; no source or runtime claim"}}})
	if err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join(dir, "audit.json")
	if err := os.WriteFile(auditPath, audit, 0600); err != nil {
		t.Fatal(err)
	}
	first := run("commit", "--audit", auditPath)
	second := run("commit", "--audit", auditPath)
	if string(first) != string(second) {
		t.Fatalf("commit replay changed progress/receipt: %s %s", first, second)
	}
	current, err = repo.Get(t.Context(), p.ID)
	if err != nil || current.Version != p.Version+1 {
		t.Fatalf("duplicate publication: %+v %v", current, err)
	}
}
