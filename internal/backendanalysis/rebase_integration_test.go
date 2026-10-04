package backendanalysis

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"
	"uuid"

	model "github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/store"
)

// These tests cross the actual import, proposal, rebase, durable job admission
// and engine boundaries. No graph or rebase result is fabricated for the engine.
type rebaseAnalysisFixture struct {
	t       *testing.T
	graphs  *model.Repo
	jobs    *Repo
	db      *store.DB
	project model.Project
	schema  string
	session *model.ImportSession
	node    string
}

func newRebaseAnalysisFixture(t *testing.T, schema string) *rebaseAnalysisFixture {
	t.Helper()
	jobs, db := testRepo(t)
	graphs := model.NewRepo(db)
	p, err := graphs.Create(t.Context(), model.CreateInput{Name: "Rebase integration", IdempotencyKey: "project"})
	if err != nil {
		t.Fatal(err)
	}
	f := &rebaseAnalysisFixture{t: t, graphs: graphs, jobs: jobs, db: db, project: *p, schema: schema}
	f.importSource("initial", "Original", false, nil)
	g, err := graphs.ResolveEffectiveGraph(t.Context(), p.ID, model.BackendReadTarget{RevisionID: f.project.CurrentRevisionID})
	if err != nil || len(g.State.Nodes) != 1 {
		t.Fatalf("initial graph: %v %v", g, err)
	}
	f.node = g.State.Nodes[0].ID
	return f
}

func (f *rebaseAnalysisFixture) importSource(key, name string, remove bool, shared *model.BaseAssertionRef) {
	f.t.Helper()
	hash := strings.Repeat("a", 64)
	profiles := []string{model.GraphProfile, model.RelationalProfile, model.RuntimeProfile, model.LineageProfile, model.EventsProfile}
	in := model.BeginImportInput{ExpectedVersion: f.project.Version, BaseRevisionID: f.project.CurrentRevisionID, IdempotencyKey: key,
		Profile: model.EventsProfile, Manifest: model.SourceManifest{RepositoryName: "orders", Provider: model.SourceProvider{Name: "fixture", Version: "1", Namespace: "provider-a", Method: "ast", Profiles: profiles, Limitations: []string{}}, Snapshot: model.SnapshotManifest{Consistency: "verified", CapturedAt: time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC), Files: []model.ManifestFile{{Path: "main.go", ContentHash: hash, FileType: "go", AnalysisStatus: "analyzed"}}}}}
	for _, category := range []string{"files", "endpoints", "datastores", "migrations", "producers", "consumers", "jobs", "contracts", "tests"} {
		count := int64(0)
		if category == "files" {
			count = 1
		}
		in.Inventory = append(in.Inventory, model.InventoryItem{Category: category, Status: "complete", KnownCount: count, Denominator: new(count), DiscoverySource: "fixture", Gaps: []string{}})
	}
	if f.schema == "6" {
		in.Profile = model.ComposedProfile
		in.Mode = "composed"
		in.SyncPolicy = model.WholeSourcePolicy
		in.Manifest.Provider.Profiles = append(profiles, model.ComposedProfile)
		in.SourceScope = &model.SourceScope{Kind: "add_repository"}
		in.ScopeStatus = &model.SourceScopeStatus{Status: "complete", Gaps: []string{}}
	}
	if f.session != nil {
		if f.schema == "6" {
			in.Manifest.Provider.Namespace = f.session.Manifest.Provider.Namespace
			in.SourceScope = &model.SourceScope{Kind: "reconcile", RepositoryID: f.session.RepositoryID, ProviderNamespace: in.Manifest.Provider.Namespace}
		} else {
			in.Mode = "reconcile"
			in.RepositoryID = new(f.session.RepositoryID)
			in.GraphScope = &model.GraphScope{Profile: in.Profile, Status: "complete", Gaps: []string{}}
		}
	}
	if shared != nil {
		in.Manifest.Provider.Namespace = "provider-b"
		in.SourceScope = &model.SourceScope{Kind: "add_provider", RepositoryID: f.session.RepositoryID}
	}
	session, err := f.graphs.BeginImport(f.t.Context(), f.project.ID, in)
	if err != nil {
		f.t.Fatal(err)
	}
	commands := []model.ImportCommand{
		{Op: "upsert_node", Node: &model.ImportNode{ExternalKey: "handler", Kind: "handler", Name: name, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof"}}},
		{Op: "upsert_evidence", Evidence: &model.ImportEvidence{ExternalKey: "proof", SubjectType: "node", SubjectKey: "handler", Method: "ast", Status: "explicit", Source: model.EvidenceSource{RepositoryID: session.RepositoryID, SnapshotID: session.SnapshotID, File: "main.go", ContentHash: hash}}},
	}
	if shared != nil {
		commands = append([]model.ImportCommand{{Op: "claim_identity", ClaimIdentity: &model.SourceClaimIdentity{DecisionID: uuid.NewV7().String(), RecordType: "node", ExternalKey: "handler", Target: *shared, Reason: "Same explicit source object", EvidenceKeys: []string{"proof"}}}}, commands...)
	}
	if remove {
		commands = []model.ImportCommand{{Op: "delete_assertion", Deletion: &model.ImportDeletion{RecordType: "node", ExternalKey: "handler", ExpectedID: f.node, Reason: "Source removed"}}}
	}
	batchHash, err := model.ImportBatchHash(commands)
	if err != nil {
		f.t.Fatal(err)
	}
	batch, err := f.graphs.PutImportBatch(f.t.Context(), f.project.ID, session.ID, key, model.ImportBatchInput{ExpectedImportVersion: session.Version, PayloadHash: batchHash, Commands: commands})
	if err != nil {
		f.t.Fatal(err)
	}
	preview, err := f.graphs.PreviewImport(f.t.Context(), f.project.ID, session.ID, model.PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: f.project.CurrentRevisionID})
	if err != nil || preview.CandidateHash == nil || preview.State != "ready" {
		f.t.Fatalf("import preview: %+v %v", preview, err)
	}
	out, err := f.graphs.CommitImport(f.t.Context(), f.project.ID, session.ID, model.CommitImportInput{ExpectedVersion: f.project.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: key})
	if err != nil {
		f.t.Fatal(err)
	}
	f.project = out.Project
	f.session = session
}

func integrationRename(t *testing.T, id, name string) model.ChangeProposalCommand {
	t.Helper()
	raw := fmt.Sprintf(`{"type":"rename","commandId":%q,"reason":"Explicit desired name","recordType":"node","id":%q,"name":%q}`, uuid.NewV7().String(), id, name)
	var command model.ChangeProposalCommand
	if err := json.Unmarshal([]byte(raw), &command); err != nil {
		t.Fatal(err)
	}
	return command
}
func (f *rebaseAnalysisFixture) proposal() *model.ChangeProposalDetail {
	f.t.Helper()
	d, err := f.graphs.CreateChangeProposal(f.t.Context(), f.project.ID, model.CreateChangeProposalInput{Name: "Desired", BaseRevisionID: f.project.CurrentRevisionID, IdempotencyKey: "proposal"})
	if err != nil {
		f.t.Fatal(err)
	}
	in := model.PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []model.ChangeProposalCommand{integrationRename(f.t, f.node, "Desired")}}
	p, err := f.graphs.PreviewChangeProposal(f.t.Context(), f.project.ID, d.Proposal.ID, in)
	if err != nil || p.CandidateHash == nil {
		f.t.Fatalf("rename preview: %+v %v", p, err)
	}
	out, err := f.graphs.ApplyChangeProposal(f.t.Context(), f.project.ID, d.Proposal.ID, model.ApplyChangeProposalInput{ExpectedVersion: in.ExpectedVersion, ProposalRevisionID: in.ProposalRevisionID, Commands: in.Commands, CandidateHash: *p.CandidateHash, IdempotencyKey: "rename"})
	if err != nil {
		f.t.Fatal(err)
	}
	return &model.ChangeProposalDetail{Proposal: out.Proposal, Revision: out.Revision}
}
func (f *rebaseAnalysisFixture) rebase(d *model.ChangeProposalDetail, choice string) *model.ChangeProposalDetail {
	f.t.Helper()
	in := model.PreviewChangeProposalRebaseInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, NewBaseRevisionID: f.project.CurrentRevisionID}
	p, err := f.graphs.PreviewChangeProposalRebase(f.t.Context(), f.project.ID, d.Proposal.ID, in)
	if err != nil {
		f.t.Fatal(err)
	}
	if choice != "" && len(p.Conflicts) != 1 {
		f.t.Fatalf("want one conflict, got %+v", p.Conflicts)
	}
	for _, c := range p.Conflicts {
		r := model.ChangeRebaseResolution{ConflictID: c.ID, Choice: choice, Reason: "Reviewed rebase decision"}
		if choice == "replace" {
			r.Value = jsontext.Value(`"Resolved"`)
		}
		in.Resolutions = append(in.Resolutions, r)
	}
	p, err = f.graphs.PreviewChangeProposalRebase(f.t.Context(), f.project.ID, d.Proposal.ID, in)
	if err != nil || p.CandidateHash == nil {
		f.t.Fatalf("rebase preview: %+v %v", p, err)
	}
	out, err := f.graphs.ApplyChangeProposalRebase(f.t.Context(), f.project.ID, d.Proposal.ID, model.ApplyChangeProposalRebaseInput{PreviewChangeProposalRebaseInput: in, CandidateHash: *p.CandidateHash, IdempotencyKey: uuid.NewV7().String()})
	if err != nil {
		f.t.Fatal(err)
	}
	return &model.ChangeProposalDetail{Proposal: out.Proposal, Revision: out.Revision}
}
func (f *rebaseAnalysisFixture) start(d *model.ChangeProposalDetail, from string, preview bool) (StartInput, *Job, *ImmutableInput, *TerminalSnapshot) {
	f.t.Helper()
	target := AnalysisTarget{ChangeProposal: &model.ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}}
	if preview {
		in := model.PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []model.ChangeProposalCommand{integrationRename(f.t, f.node, "Frozen preview")}}
		p, err := f.graphs.PreviewChangeProposal(f.t.Context(), f.project.ID, d.Proposal.ID, in)
		if err != nil || p.CandidateHash == nil {
			f.t.Fatalf("frozen preview: %+v %v", p, err)
		}
		target = AnalysisTarget{CommandPreview: &CommandPreviewTarget{ChangeProposal: *target.ChangeProposal, ExpectedVersion: d.Proposal.Version, Commands: in.Commands, CandidateHash: *p.CandidateHash}}
	}
	request := StartInput{Kind: "impact", FromRevisionID: from, Target: target, Limits: defaultLimits(), ObservationMode: "none", IdempotencyKey: uuid.NewV7().String()}
	engine := NewEngine(f.graphs, nil)
	job, err := NewService(f.jobs, f.graphs, engine).Start(f.t.Context(), f.project.ID, request)
	if err != nil {
		f.t.Fatal(err)
	}
	input, err := f.jobs.Input(f.t.Context(), f.project.ID, job.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	terminal, err := engine.Analyze(f.t.Context(), input, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	if terminal.Status != "completed" || terminal.Snapshot.Manifest.RuntimeVerified {
		f.t.Fatalf("bad static report: %+v", terminal)
	}
	return request, job, input, terminal
}
func integrationBytes(t *testing.T, v any) string {
	t.Helper()
	raw, err := canonical(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func (f *rebaseAnalysisFixture) checkHistorical(request StartInput, job *Job, input *ImmutableInput, report *TerminalSnapshot) {
	f.t.Helper()
	old, err := f.jobs.Input(f.t.Context(), f.project.ID, job.ID)
	if err != nil {
		f.t.Fatal(err)
	}
	if integrationBytes(f.t, old) != integrationBytes(f.t, input) {
		f.t.Fatal("rebase rewrote admitted job input")
	}
	got, err := NewEngine(f.graphs, nil).Analyze(f.t.Context(), old, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	if integrationBytes(f.t, got) != integrationBytes(f.t, report) {
		f.t.Fatal("historical analysis changed after rebase/restart")
	}
	replay, err := NewService(f.jobs, nil, nil).Start(f.t.Context(), f.project.ID, request)
	if err != nil {
		f.t.Fatal(err)
	}
	if integrationBytes(f.t, replay) != integrationBytes(f.t, job) {
		f.t.Fatal("original start receipt changed")
	}
}
func (f *rebaseAnalysisFixture) reopen() {
	f.t.Helper()
	path := f.db.Path()
	if err := f.db.Close(); err != nil {
		f.t.Fatal(err)
	}
	db, err := store.Open(f.t.Context(), path)
	if err != nil {
		f.t.Fatal(err)
	}
	f.t.Cleanup(func() { _ = db.Close() })
	if err = db.Migrate(f.t.Context(), slog.Default()); err != nil {
		f.t.Fatal(err)
	}
	f.db = db
	f.jobs = NewRepo(db)
	f.graphs = model.NewRepo(db)
}

func TestAnalysisRebaseResolutionAndFrozenHistory(t *testing.T) {
	for _, schema := range []string{"5", "6"} {
		for _, choice := range []string{"replace", "take_source"} {
			t.Run(schema+"/"+choice, func(t *testing.T) {
				f := newRebaseAnalysisFixture(t, schema)
				d := f.proposal()
				oldSource := f.project.CurrentRevisionID
				request, job, input, report := f.start(d, oldSource, true)
				f.importSource("changed", "New source", false, nil)
				rebased := f.rebase(d, choice)
				graph, err := f.graphs.ResolveEffectiveGraph(t.Context(), f.project.ID, model.BackendReadTarget{ChangeProposal: &model.ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: rebased.Revision.ID}})
				if err != nil {
					t.Fatal(err)
				}
				wantName, wantProof := "Resolved", "desired"
				if choice == "take_source" {
					wantName, wantProof = "New source", "explicit"
				}
				if len(graph.State.Nodes) != 1 || graph.State.Nodes[0].Name != wantName {
					t.Fatalf("wrong effective name: %+v", graph.State.Nodes)
				}
				if graph.Pins.BaseRevisionID != f.project.CurrentRevisionID {
					t.Fatal("rebase did not select the exact new source base")
				}
				proof, err := model.EffectivePropertyAnalysisProof(graph, model.ChangeRecordRef{RecordType: "node", ID: f.node}, model.EffectivePropertySelector{Source: &model.TypedSourcePropertySelector{Kind: "name"}})
				if err != nil || proof.Status != wantProof {
					t.Fatalf("proof=%+v err=%v want=%s", proof, err, wantProof)
				}
				if choice == "take_source" {
					if len(proof.EvidenceIDs) == 0 {
						t.Fatal("selected source value lost its evidence")
					}
					if schema == "6" && (len(proof.Assertions) != 1 || proof.Assertions[0].ProviderNamespace != "provider-a" || proof.Assertions[0].AssertionHash != graph.Source.Assertions[0].AssertionHash) {
						t.Fatal("selected source value borrowed an old or foreign assertion", proof)
					}
				}
				if choice == "replace" {
					if len(proof.Assertions) != 0 || len(proof.EvidenceIDs) != 0 {
						t.Fatal("resolution promoted to source proof", proof)
					}
					found := false
					for _, o := range graph.Origins {
						if o.SubjectID == f.node && o.Selector.Source != nil && o.Selector.Source.Kind == "name" {
							found = o.Kind == "intent" && o.RebaseResolution != nil && o.RebaseResolution.ProposalRevisionID == rebased.Revision.ID
						}
					}
					if !found {
						t.Fatal("resolution authorship missing")
					}
				}
				_, _, newInput, _ := f.start(rebased, f.project.CurrentRevisionID, false)
				if newInput.AfterPins.EffectiveSemanticHash != graph.Pins.EffectiveSemanticHash {
					t.Fatal("job lost desired effective hash")
				}
				f.reopen()
				f.checkHistorical(request, job, input, report)
			})
		}
	}
}

func TestAnalysisRebaseCarryAndHistoricalJobs(t *testing.T) {
	for _, providers := range []int{1, 2} {
		t.Run(fmt.Sprint(providers), func(t *testing.T) {
			f := newRebaseAnalysisFixture(t, "6")
			first := f.session
			if providers == 2 {
				g, err := f.graphs.ResolveSourceGraph(t.Context(), f.project.ID, f.project.CurrentRevisionID)
				if err != nil {
					t.Fatal(err)
				}
				a := g.Assertions[0]
				ref := model.BaseAssertionRef{RepositoryID: a.Owner.RepositoryID, ProviderNamespace: a.Owner.ProviderNamespace, RecordType: a.RecordType, ExternalKey: a.ExternalKey, ExpectedID: a.RecordID, AssertionHash: a.AssertionHash}
				f.importSource("share", "Original", false, &ref)
			}
			second := f.session
			d := f.proposal()
			original := f.project.CurrentRevisionID
			request, job, input, report := f.start(d, original, true)
			f.session = first
			f.importSource("delete-a", "", true, nil)
			if providers == 2 {
				f.session = second
				f.importSource("delete-b", "", true, nil)
			}
			carry := f.rebase(d, "keep_proposal")
			target := model.BackendReadTarget{ChangeProposal: &model.ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: carry.Revision.ID}}
			graph, err := f.graphs.ResolveEffectiveGraph(t.Context(), f.project.ID, target)
			if err != nil {
				t.Fatal(err)
			}
			if len(graph.Identities) != providers {
				t.Fatalf("qualified carried identities: %+v", graph.Identities)
			}
			for _, id := range graph.Identities {
				if id.Target.Kind != "carried_source_identity" || id.Target.Basis == nil || id.Target.Basis.RevisionID != original {
					t.Fatalf("carry promoted or retargeted: %+v", id)
				}
			}
			proof, err := model.EffectiveRecordAnalysisProof(graph, model.ChangeRecordRef{RecordType: "node", ID: f.node})
			if err != nil || proof.Status != "desired" || len(proof.Assertions) != 0 || len(proof.EvidenceIDs) != 0 {
				t.Fatalf("carry acquired source proof: %+v %v", proof, err)
			}
			carriedRequest, carriedJob, carriedInput, carriedReport := f.start(carry, f.project.CurrentRevisionID, false)
			changes := readRecords[DiffChange](t, carriedReport.Snapshot, "changes")
			found := false
			for _, change := range changes {
				if change.Object.ID == f.node && change.Operation == "added" && change.Facet == "behavior" {
					found = true
				}
			}
			if !found {
				t.Fatal("engine omitted retained object from new-source→desired diff")
			}
			foundProof := false
			for _, rule := range readRecords[RuleResult](t, carriedReport.Snapshot, "findings") {
				if rule.Object.ID != f.node {
					continue
				}
				for _, evidence := range rule.Evidence {
					if evidence.Side != "after" {
						continue
					}
					foundProof = true
					if evidence.Status != "desired" || len(evidence.Assertions) != 0 || len(evidence.EvidenceIDs) != 0 || evidence.EffectiveSemanticHash != graph.Pins.EffectiveSemanticHash {
						t.Fatal("engine promoted carried intent to current source evidence", evidence)
					}
				}
			}
			if !foundProof {
				t.Fatal("engine report omitted carried desired proof")
			}
			f.rebase(carry, "")
			f.reopen()
			f.checkHistorical(request, job, input, report)
			f.checkHistorical(carriedRequest, carriedJob, carriedInput, carriedReport)
		})
	}
}
