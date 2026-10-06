package backendmaterialize

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/apidesign"
	bm "github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/testkit"
)

func fixture(t *testing.T) (*Service, string, PreviewInput) {
	t.Helper()
	db := testkit.NewDB(t)
	cfg := &config.Config{MaxBody: 2 << 20}
	models := bm.NewRepo(db)
	apis := apidesign.NewRepo(db, cfg)
	s := NewService(db, models, apis, designscenario.NewRepo(db, cfg, apis))
	p, err := models.Create(t.Context(), bm.CreateInput{Name: "Materialize", IdempotencyKey: "create"})
	must(t, err)
	inventory := []bm.InventoryItem{}
	for _, kind := range []string{"files", "endpoints", "datastores", "migrations", "producers", "consumers", "jobs", "contracts", "tests"} {
		inventory = append(inventory, bm.InventoryItem{Category: kind, Status: "complete", Denominator: new(int64(0)), DiscoverySource: "fixture", Gaps: []string{}})
	}
	h := strings.Repeat("a", 64)
	session, err := models.BeginImport(t.Context(), p.ID, bm.BeginImportInput{ExpectedVersion: p.Version, BaseRevisionID: p.CurrentRevisionID, IdempotencyKey: "begin", Profile: bm.EventsProfile, Inventory: inventory, Manifest: bm.SourceManifest{RepositoryName: "fixture", Provider: bm.SourceProvider{Name: "fixture", Version: "1", Namespace: "fixture", Method: "ast", Profiles: []string{bm.GraphProfile, bm.RelationalProfile, bm.RuntimeProfile, bm.LineageProfile, bm.EventsProfile}, Limitations: []string{}}, Snapshot: bm.SnapshotManifest{Consistency: "verified", CapturedAt: time.Now().UTC(), Files: []bm.ManifestFile{{Path: "main.go", ContentHash: h, FileType: "go", AnalysisStatus: "analyzed"}}}}})
	must(t, err)
	commands := []bm.ImportCommand{{Op: "upsert_node", Node: &bm.ImportNode{ExternalKey: "handler", Kind: "handler", Name: "Handler", Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof"}}}, {Op: "upsert_evidence", Evidence: &bm.ImportEvidence{ExternalKey: "proof", SubjectType: "node", SubjectKey: "handler", Method: "ast", Status: "explicit", Source: bm.EvidenceSource{RepositoryID: session.RepositoryID, SnapshotID: session.SnapshotID, File: "main.go", ContentHash: h}}}}
	hash, err := bm.ImportBatchHash(commands)
	must(t, err)
	batch, err := models.PutImportBatch(t.Context(), p.ID, session.ID, "one", bm.ImportBatchInput{ExpectedImportVersion: session.Version, PayloadHash: hash, Commands: commands})
	must(t, err)
	preview, err := models.PreviewImport(t.Context(), p.ID, session.ID, bm.PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	must(t, err)
	if preview.CandidateHash == nil {
		t.Fatalf("import: %+v", preview)
	}
	imported, err := models.CommitImport(t.Context(), p.ID, session.ID, bm.CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "commit"})
	must(t, err)
	proposal, err := models.CreateChangeProposal(t.Context(), p.ID, bm.CreateChangeProposalInput{Name: "Draft", BaseRevisionID: imported.Revision.ID, IdempotencyKey: "proposal"})
	must(t, err)
	target := bm.BackendReadTarget{ChangeProposal: &bm.ProposalReadTarget{ProposalID: proposal.Proposal.ID, ProposalRevisionID: proposal.Revision.ID}}
	graph, err := models.ResolveEffectiveGraph(t.Context(), p.ID, target)
	must(t, err)
	in := PreviewInput{ProfileVersion: ProfileVersion, Target: target, TargetHash: graph.Pins.TargetHash, SourceScope: []string{graph.State.Nodes[0].ID}, Targets: []Target{{Key: "api", Kind: "api_design", Name: "Draft", Commands: []Command{{Type: "replace_api_document", APIDocument: apiDoc}}}}, Translations: []Translation{{SourceID: graph.State.Nodes[0].ID, TargetKey: "api", Selector: "/paths/~1orders/get", Reason: "Authored HTTP intent"}}, ExcludedIDs: []string{}, Reason: "Draft only"}
	return s, p.ID, in
}

const apiDoc = `{"openapi":"3.1.0","info":{"title":"Orders","version":"1"},"paths":{"/orders":{"get":{"x-mocker-canvas-operation-id":"orders-get","responses":{"200":{"description":"OK"}}}}}}`

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func applyInput(t *testing.T, s *Service, pid string, in PreviewInput, key string) ApplyInput {
	t.Helper()
	p, err := s.Preview(t.Context(), pid, in)
	must(t, err)
	if !p.CanApply {
		t.Fatal(p.Diagnostics)
	}
	return ApplyInput{PreviewInput: in, CandidateHash: p.CandidateHash, IdempotencyKey: key}
}
func counts(t *testing.T, s *Service) []int {
	t.Helper()
	out := []int{}
	for _, table := range []string{"api_designs", "api_design_revisions", "design_scenarios", "design_scenario_revisions", "workspaces", "specs", "backend_materializations", "backend_materialization_receipts"} {
		var n int
		must(t, s.db.R.QueryRow("SELECT count(*) FROM "+table).Scan(&n))
		out = append(out, n)
	}
	return out
}

func TestMaterializationPureReplayAndScope(t *testing.T) {
	t.Parallel()
	s, pid, in := fixture(t)
	before := counts(t, s)
	a, err := s.Preview(t.Context(), pid, in)
	must(t, err)
	b, err := s.Preview(t.Context(), pid, in)
	must(t, err)
	if a.CandidateHash != b.CandidateHash || !reflect.DeepEqual(before, counts(t, s)) {
		t.Fatal("preview changed state or identity")
	}
	request := ApplyInput{PreviewInput: in, CandidateHash: a.CandidateHash, IdempotencyKey: "apply"}
	receipt, err := s.Apply(t.Context(), pid, request)
	must(t, err)
	after := counts(t, s)
	replay, err := s.Apply(t.Context(), pid, request)
	must(t, err)
	x, _ := json.Marshal(receipt)
	y, _ := json.Marshal(replay)
	if string(x) != string(y) || !reflect.DeepEqual(after, counts(t, s)) {
		t.Fatal("duplicate replay")
	}
	request.Reason = "different"
	if _, err := s.Apply(t.Context(), pid, request); err == nil {
		t.Fatal("key reuse")
	}
	in.Translations = nil
	unsupported, err := s.Preview(t.Context(), pid, in)
	must(t, err)
	if unsupported.CanApply {
		t.Fatal("implicit translation")
	}
	in.PartialSimulation = true
	in.ExcludedIDs = in.SourceScope
	in.Reason = "Excluded opaque source"
	partial, err := s.Preview(t.Context(), pid, in)
	must(t, err)
	if !partial.CanApply || partial.Equivalence != "partial_simulation" || partial.Coverage[0].Status != "excluded" {
		t.Fatal(partial)
	}
}

func linkedInput(t *testing.T, s *Service, in PreviewInput) (PreviewInput, *apidesign.Detail) {
	t.Helper()
	api, err := s.apis.Create(t.Context(), apidesign.CreateInput{Name: "Existing", Document: apiDoc, Source: "ui"})
	must(t, err)
	installation, err := s.models.InstallationID(t.Context())
	must(t, err)
	pin := &bm.NamespacedArtifactPin{Namespace: bm.ArtifactNamespace{Scope: "local", InstallationID: installation}, Pin: bm.ArtifactPin{Kind: "api_design", ID: strconv.FormatInt(api.Design.ID, 10), RevisionID: strconv.FormatInt(api.Draft.ID, 10), ContentHash: api.Draft.Hash}}
	changed := strings.ReplaceAll(api.Draft.Document, "Orders", "Changed")
	in.Targets[0].Pin = pin
	in.Targets[0].ExpectedVersion = api.Design.Version
	in.Targets[0].Commands[0].APIDocument = changed
	doc := designscenario.Document{FormatVersion: 1, Title: "Linked", Participants: []designscenario.Participant{{ID: "caller", Name: "Caller", Kind: "service"}, {ID: "api", Name: "API", Kind: "service"}}, Messages: []designscenario.Message{{ID: "request", FromID: "caller", ToID: "api", Kind: "request", Operation: &designscenario.OperationBinding{ContractID: "api", OperationKey: "orders-get"}}}, Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{{ID: "api", Name: "API", Mode: "linked", Source: &designscenario.ContractSource{DesignID: api.Design.ID, RevisionID: api.Draft.ID, Version: api.Draft.Version}, Document: jsonx.RawMessage(changed)}}}
	in.Targets = append(in.Targets, Target{Key: "scenario", Kind: "design_scenario", Name: "Linked", Commands: []Command{{Type: "replace_scenario", Scenario: &doc}}})
	return in, api
}
func TestMaterializationLinkedRollbackAndSubstitution(t *testing.T) {
	for _, phase := range []string{"api", "scenario"} {
		t.Run(phase, func(t *testing.T) {
			s, pid, in := fixture(t)
			in, api := linkedInput(t, s, in)
			request := applyInput(t, s, pid, in, "linked")
			before := counts(t, s)
			sentinel := errors.New("injected after " + phase)
			s.afterWrite = func(at string) error {
				if at == phase {
					return sentinel
				}
				return nil
			}
			if _, err := s.Apply(t.Context(), pid, request); !errors.Is(err, sentinel) {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, counts(t, s)) {
				t.Fatal("partial rows committed")
			}
			detail, err := s.apis.Detail(t.Context(), api.Design.ID)
			must(t, err)
			if !reflect.DeepEqual(api, detail) {
				t.Fatal("API/mock state escaped rollback")
			}
			s.afterWrite = nil
			receipt, err := s.Apply(t.Context(), pid, request)
			must(t, err)
			scenario, err := s.scenarios.Detail(t.Context(), ownerID(receipt.Owners[1].Pin.Pin.ID))
			must(t, err)
			if scenario.Draft.Document.Contracts[0].Source.RevisionID != ownerID(receipt.Owners[0].Pin.Pin.RevisionID) {
				t.Fatal("linked revision not substituted")
			}
			// A later owner edit cannot invalidate exact receipt replay.
			_, err = s.apis.Save(t.Context(), api.Design.ID, apidesign.SaveInput{ExpectedVersion: 2, Document: strings.ReplaceAll(detail.Draft.Document, "Orders", "Later"), Source: "ui"})
			must(t, err)
			_, err = s.Apply(t.Context(), pid, request)
			must(t, err)
		})
	}
}
func TestMaterializationRejectsUnplannedAndStale(t *testing.T) {
	t.Parallel()
	s, pid, in := fixture(t)
	in, api := linkedInput(t, s, in)
	missing := in
	missing.Targets = in.Targets[1:]
	if _, err := s.Preview(t.Context(), pid, missing); err == nil {
		t.Fatal("unplanned linked API accepted")
	}
	request := applyInput(t, s, pid, in, "stale")
	_, err := s.apis.Save(t.Context(), api.Design.ID, apidesign.SaveInput{ExpectedVersion: 1, Document: strings.ReplaceAll(api.Draft.Document, "Orders", "Concurrent"), Source: "ui"})
	must(t, err)
	if _, err := s.Apply(t.Context(), pid, request); err == nil {
		t.Fatal("stale draft accepted")
	}
}
