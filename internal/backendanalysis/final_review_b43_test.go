package backendanalysis

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"testing"
	"uuid"

	model "github.com/yashok111/mocker/internal/backendmodel"
)

func TestB43ReviewEndpointIntentCreatedEdgeCorrespondence(t *testing.T) {
	for _, tc := range []struct {
		name  string
		edges int
		extra bool
	}{{"unique imported relationship", 1, false}, {"ambiguous parallel relationships", 2, false}, {"extra imported field", 1, true}} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRebaseAnalysisFixture(t, "6")
			f.withEndpoint = true
			f.importSource("endpoint-base", "Before", false, nil)
			baseID := f.project.CurrentRevisionID
			d := f.proposal()
			desiredEdgeID := uuid.NewV7().String()
			command := model.ChangeProposalCommand{Type: "upsert_edge", CommandID: uuid.NewV7().String(), Reason: "Add intended recursive call", ID: desiredEdgeID, Kind: "calls", From: f.node, To: f.node, Attributes: map[string]jsontext.Value{}}
			preview, err := f.graphs.PreviewChangeProposal(t.Context(), f.project.ID, d.Proposal.ID, model.PreviewChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []model.ChangeProposalCommand{command}})
			if err != nil || preview.CandidateHash == nil {
				t.Fatalf("edge preview %+v %v", preview, err)
			}
			applied, err := f.graphs.ApplyChangeProposal(t.Context(), f.project.ID, d.Proposal.ID, model.ApplyChangeProposalInput{ExpectedVersion: d.Proposal.Version, ProposalRevisionID: d.Revision.ID, Commands: []model.ChangeProposalCommand{command}, CandidateHash: *preview.CandidateHash, IdempotencyKey: "intent-edge"})
			if err != nil {
				t.Fatal(err)
			}
			f.extraEdges = tc.edges
			if tc.extra {
				f.extraEdgeAttributes = map[string]jsontext.Value{"description": []byte(`"additional source field"`)}
			}
			f.importSource("endpoint-result", "Desired", false, nil)
			after, err := f.graphs.ResolveEffectiveGraph(t.Context(), f.project.ID, model.BackendReadTarget{RevisionID: f.project.CurrentRevisionID})
			if err != nil {
				t.Fatal(err)
			}
			root := ""
			actualEdges := map[string]bool{}
			for _, node := range after.State.Nodes {
				if node.Kind == "http_operation" {
					root = node.ID
				}
			}
			for _, edge := range after.State.Edges {
				if edge.Kind == "calls" {
					actualEdges[edge.ID] = true
					if edge.ID == desiredEdgeID {
						t.Fatal("fixture injected desired UUID instead of importer allocation")
					}
				}
			}
			if root == "" || len(actualEdges) != tc.edges {
				t.Fatal("incomplete source fixture")
			}
			start := StartInput{Kind: "endpoint_review", EndpointReview: &StartEndpointReviewInput{StartCommon: StartCommon{Kind: "endpoint_review", Limits: defaultLimits(), ObservationMode: "none", IdempotencyKey: "endpoint-intent-review"}, FromRevisionID: baseID, ToRevisionID: f.project.CurrentRevisionID, BeforeEndpointID: root, AfterEndpointID: &root, ChangeProposal: &model.ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: applied.Revision.ID}}}
			service := NewService(f.jobs, f.graphs, NewEngine(f.graphs, nil))
			job, err := service.Start(t.Context(), f.project.ID, start)
			if err != nil {
				t.Fatal(err)
			}
			input, err := f.jobs.Input(t.Context(), f.project.ID, job.ID)
			if err != nil {
				t.Fatal(err)
			}
			report, err := service.engine.Analyze(t.Context(), input, nil)
			if err != nil {
				t.Fatal(err)
			}
			outside := map[string][]string{}
			for _, chunk := range report.Snapshot.Chunks {
				if chunk.Section != "changes" {
					continue
				}
				var records []ResultRecord
				if err = json.Unmarshal(chunk.ItemsJSON, &records); err != nil {
					t.Fatal(err)
				}
				for _, record := range records {
					var detail OutsideIntentChangeDetail
					if err = json.Unmarshal(record.Detail, &detail); err != nil {
						t.Fatal(err)
					}
					if detail.Type == "outside_intent_change" && actualEdges[detail.Change.Object.ID] {
						outside[detail.Change.Object.ID] = append(outside[detail.Change.Object.ID], detail.Change.Paths...)
					}
				}
			}
			if tc.edges == 2 {
				// Ambiguous parallel edges stay unmatched. Either may be the
				// intended creation, so each is an unverified correspondence
				// gap, not an outside-intent change (review 2026-10-06, F145).
				var unverified []ObjectAddress
				for _, g := range report.Snapshot.Manifest.Gaps {
					if g.Code == "unverified_created_correspondence" {
						unverified = g.Objects
					}
				}
				for id := range actualEdges {
					if len(outside[id]) != 0 || !slices.Contains(unverified, ObjectAddress{RecordType: "edge", ID: id}) {
						t.Fatalf("ambiguous relationship was matched or called outside intent: outside=%+v unverified=%+v", outside, unverified)
					}
				}
				return
			}
			for id := range actualEdges {
				for _, path := range []string{"/kind", "/from", "/to"} {
					if slices.Contains(outside[id], path) {
						t.Errorf("intended imported relationship falsely outside intent: %s", path)
					}
				}
				if tc.extra && !slices.Contains(outside[id], "/attributes/description") {
					t.Error("extra imported field was hidden by relationship correspondence")
				}
			}
		})
	}
}

// The graph evaluator accepts prepared snapshots; this reader deliberately has
// no deletion evidence so the stress fixture measures report budgets only.
type noDeletionForBudget struct{}

func (noDeletionForBudget) AnalysisEvidenceInputFootprint(context.Context, string, model.AnalysisEvidenceRequest) (model.AnalysisInputFootprint, error) {
	panic("budget evaluator must not prepare model input")
}
func (noDeletionForBudget) ReadAnalysisDeletionEvidence(context.Context, string, string, string, []model.AnalysisEvidenceDocumentPin) (*model.AnalysisDeletionEvidence, error) {
	return &model.AnalysisDeletionEvidence{Pins: []model.AnalysisEvidenceDocumentPin{}, Deletions: []model.AnalysisDeletionProof{}}, nil
}
func (noDeletionForBudget) ReadAnalysisAttachmentEvidence(context.Context, string, model.TestAttachmentRef) (*model.AnalysisAttachmentEvidence, error) {
	panic("budget fixture has no attachment criteria")
}

func TestB43ReviewConformanceManifestBudget(t *testing.T) {
	nodes := make([]model.Node, 1500)
	for i := range nodes {
		nodes[i] = model.Node{ID: uuid.NewV7().String(), Kind: "handler", Name: "Imported handler"}
	}
	base := supportedGraph(nil, nil)
	result := supportedGraph(nodes, nil)
	result.Pins.StructuralSchemaVersion = "6"
	proposal := model.ProposalReadTarget{ProposalID: uuid.NewV7().String(), ProposalRevisionID: uuid.NewV7().String()}
	payload := &ConformancePayload{PackagePayload: PackagePayload{ChangeProposal: proposal, BaseRevisionID: revisionID, BasePins: base.Pins, DraftPins: base.Pins, BaseSource: sourcePins(base), DraftSource: sourcePins(base), EvidencePins: []model.AnalysisEvidenceDocumentPin{}}, ResultRevisionID: revisionID, ResultPins: result.Pins, ResultSource: sourcePins(result), IdentityMap: []IdentityMapEntry{}, TestAttachments: []CriterionAttachment{}}
	input := budgetB43Input("conformance", payload)
	report, err := analyzeConformance(t.Context(), input, payload, base, base, result, noDeletionForBudget{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertB43PartialManifestPersists(t, input, report)
}
func TestB43ReviewEndpointManifestBudget(t *testing.T) {
	root := uuid.NewV7().String()
	rootNode := model.Node{ID: root, Kind: "http_operation", Name: "Endpoint"}
	edges := make([]model.Edge, 1500)
	nodes := make([]model.Node, 1, 1+len(edges))
	nodes[0] = rootNode
	for i := range edges {
		id := uuid.NewV7().String()
		nodes = append(nodes, model.Node{ID: id, Kind: "handler", Name: "Handler"})
		edges[i] = model.Edge{ID: uuid.NewV7().String(), Kind: "handles", From: root, To: id}
	}
	before := supportedGraph([]model.Node{rootNode}, nil)
	after := supportedGraph(nodes, edges)
	payload := &EndpointReviewPayload{FromRevisionID: revisionID, ToRevisionID: revisionID, BeforeEndpointID: root, AfterEndpointID: &root, BeforePins: before.Pins, AfterPins: after.Pins, BeforeSource: sourcePins(before), AfterSource: sourcePins(after), EvidencePins: []model.AnalysisEvidenceDocumentPin{}}
	input := budgetB43Input("endpoint_review", payload)
	report, err := analyzeEndpointReview(t.Context(), input, payload, before, after, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertB43PartialManifestPersists(t, input, report)
}
func budgetB43Input(kind string, payload any) *ImmutableInput {
	limits := defaultLimits()
	return &ImmutableInput{Kind: kind, ProjectID: projectID, Limits: limits, Scope: normalizedScope(Scope{}), RuleSetVersion: "b43-rules/v1", TraversalVersion: "b42-traversal/v1", ObservationMode: "none", V2: &ImmutableInputV2{DocumentVersion: "backend-analysis-input/v2", Kind: kind, ProjectID: projectID, Payload: payload, Limits: limits, RuleSetVersion: "b43-rules/v1", TraversalVersion: "b42-traversal/v1", ObservationMode: "none"}}
}
func assertB43PartialManifestPersists(t *testing.T, input *ImmutableInput, report *TerminalSnapshot) {
	t.Helper()
	if report.Status != "completed" || report.Snapshot.Manifest.Complete || !slices.ContainsFunc(report.Snapshot.Manifest.TruncationReasons, func(d Diagnostic) bool { return d.Code == "manifest_byte_limit" }) {
		t.Fatalf("expected completed partial report: %+v", report.Snapshot.Manifest)
	}
	prepared, err := prepareSnapshot(report.Snapshot)
	if err != nil {
		t.Fatalf("bounded partial report must prepare, not fail413: %v", err)
	}
	if len(prepared.ManifestJSON) > maxManifestBytes {
		t.Fatal("prepared manifest exceeds64KiB")
	}
	for _, covered := range prepared.Manifest.CoveredChangedIDs {
		if !slices.Contains(prepared.Manifest.ChangedIDs, covered) {
			t.Fatalf("covered address not admitted: %+v", covered)
		}
	}
	repo, db := testRepo(t)
	raw, err := canonical(input)
	if err != nil {
		t.Fatal(err)
	}
	job, err := repo.Start(t.Context(), PreparedStart{ProjectID: projectID, InputJSON: raw, InputHash: digest(raw), RequestHash: digest([]byte(input.Kind)), Key: input.Kind, OutputReservation: input.Limits.ResultBytes}, nil)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := repo.Claim(t.Context(), "bounded-manifest")
	if err != nil || claim == nil {
		t.Fatalf("claim %v", err)
	}
	report.Snapshot.Manifest.JobID = job.ID
	report.Snapshot.Manifest.AnalysisInputHash = job.AnalysisInputHash
	report.Snapshot.Manifest.ResultVersion = 1
	if _, err = repo.Finalize(t.Context(), projectID, job.ID, claim.Token, *report); err != nil {
		t.Fatalf("partial report must persist: %v", err)
	}
	saved, err := repo.Get(t.Context(), projectID, job.ID)
	if err != nil || saved.Status != "completed" {
		t.Fatalf("job %+v %v", saved, err)
	}
	var rawManifest []byte
	if err = db.R.QueryRowContext(t.Context(), `SELECT document FROM backend_analysis_manifests_documents WHERE job_id=? AND result_version=1`, job.ID).Scan(&rawManifest); err != nil {
		t.Fatal(err)
	}
	var manifest ResultManifest
	if err = json.Unmarshal(rawManifest, &manifest); err != nil {
		t.Fatal(err)
	}
	if len(rawManifest) > maxManifestBytes || manifest.Complete {
		t.Fatal("persisted manifest is not bounded partial")
	}
	for _, covered := range manifest.CoveredChangedIDs {
		if !slices.Contains(manifest.ChangedIDs, covered) {
			t.Fatal("persisted covered address escapes changed set")
		}
	}
}
