package backendanalysis

import (
	"encoding/json/jsontext"
	"slices"
	"testing"

	model "github.com/yashok111/mocker/internal/backendmodel"
)

func endpointReviewFixture() (*model.EffectiveGraphSnapshot, *model.EffectiveGraphSnapshot) {
	nodes := []model.Node{{ID: "endpoint", Kind: "http_operation"}, {ID: "handler", Kind: "handler"}, {ID: "step", Kind: "flow_step"}, {ID: "table", Kind: "table"}, {ID: "error", Kind: "flow_step"}, {ID: "message", Kind: "message"}, {ID: "consumer", Kind: "consumer", Attributes: map[string]jsontext.Value{"dispatchStatus": []byte(`"complete"`)}}, {ID: "unrelated", Kind: "handler"}}
	edges := []model.Edge{{ID: "handles", Kind: "handles", From: "endpoint", To: "handler"}, {ID: "calls", Kind: "calls", From: "handler", To: "step"}, {ID: "write", Kind: "writes", From: "step", To: "table"}, {ID: "error-branch", Kind: "error", From: "step", To: "error"}, {ID: "emit", Kind: "emits", From: "step", To: "message", Attributes: map[string]jsontext.Value{"deliveryStatus": []byte(`"declared"`)}}, {ID: "deliver", Kind: "delivered_to", From: "message", To: "consumer", Attributes: map[string]jsontext.Value{"deliveryStatus": []byte(`"declared"`)}}, {ID: "noise", Kind: "calls", From: "unrelated", To: "handler"}}
	return supportedGraph(nodes, edges), supportedGraph(slices.Clone(nodes), slices.Clone(edges))
}
func TestB43EndpointUnchangedRootAndRemoval(t *testing.T) {
	before, after := endpointReviewFixture()
	in := engineInput()
	in.Kind = "endpoint_review"
	in.RuleSetVersion = "b43-rules/v1"
	for _, removed := range []bool{false, true} {
		p := &EndpointReviewPayload{BeforeEndpointID: "endpoint", AfterEndpointID: new("endpoint")}
		if removed {
			p.AfterEndpointID = nil
		}
		out, err := analyzeEndpointReview(t.Context(), in, p, before, after, nil, nil)
		if err != nil {
			t.Fatal(err)
		}
		rows := readRecords[EndpointItemDetail](t, out.Snapshot, "findings")
		found := map[string]bool{}
		for _, row := range rows {
			found[row.Side+"/"+row.Category] = true
			if removed && row.Side == "after" {
				t.Fatal("null removal acquired after witness")
			}
			if row.Witness.Seed.ID != "endpoint" || row.Witness.Side != row.Side || row.BehaviorStatus != "unverified" {
				t.Fatalf("invalid witness %+v", row)
			}
			for _, step := range row.Witness.Steps {
				for _, proof := range step.Evidence {
					if proof.Side != row.Side {
						t.Fatal("mixed witness side")
					}
				}
			}
			if row.Object.ID == "unrelated" {
				t.Fatal("walked reverse caller")
			}
		}
		for _, category := range []string{"write", "error_branch", "emitted_event", "affected_consumer"} {
			if !found["before/"+category] || !removed && !found["after/"+category] {
				t.Fatalf("missing endpoint category %+v", found)
			}
		}
	}
}
func TestB43EndpointUnknownDeliveryAndLimits(t *testing.T) {
	before, after := endpointReviewFixture()
	after.State.Edges[4].Attributes = map[string]jsontext.Value{"deliveryStatus": []byte(`"unknown"`)}
	after.Source.State = after.State
	in := engineInput()
	in.Kind = "endpoint_review"
	p := &EndpointReviewPayload{BeforeEndpointID: "endpoint", AfterEndpointID: new("endpoint")}
	report, err := analyzeEndpointReview(t.Context(), in, p, before, after, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := readRecords[EndpointItemDetail](t, report.Snapshot, "findings")
	for _, row := range rows {
		if row.Side == "after" && row.Category == "affected_consumer" {
			t.Fatal("invented delivery beyond unknown emission")
		}
	}
	in.Limits.States = 2
	report, err = analyzeEndpointReview(t.Context(), in, p, before, after, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if report.Snapshot.Manifest.Complete || len(report.Snapshot.Manifest.TruncationReasons) == 0 {
		t.Fatal("state limit not retained")
	}
}

func TestB43EndpointSavedAdmissionAndHistoricalWorker(t *testing.T) {
	f := newRebaseAnalysisFixture(t, "6")
	f.withEndpoint = true
	f.importSource("endpoint-before", "Before", false, nil)
	before := f.project.CurrentRevisionID
	graph, err := f.graphs.ResolveEffectiveGraph(t.Context(), f.project.ID, model.BackendReadTarget{RevisionID: before})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := ""
	for _, node := range graph.State.Nodes {
		if node.Kind == "http_operation" {
			endpoint = node.ID
		}
	}
	if endpoint == "" {
		t.Fatal("missing fixture endpoint")
	}
	f.importSource("endpoint-after", "After", false, nil)
	after := f.project.CurrentRevisionID
	s := NewService(f.jobs, f.graphs, NewEngine(f.graphs, nil))
	start := StartInput{Kind: "endpoint_review", EndpointReview: &StartEndpointReviewInput{StartCommon: StartCommon{Kind: "endpoint_review", Limits: defaultLimits(), ObservationMode: "none", IdempotencyKey: "endpoint-review"}, FromRevisionID: before, ToRevisionID: after, BeforeEndpointID: endpoint, AfterEndpointID: nil}}
	job, err := s.Start(t.Context(), f.project.ID, start)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := f.jobs.Input(t.Context(), f.project.ID, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	report, err := s.engine.Analyze(t.Context(), saved, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(report.Snapshot.Manifest.Gaps, func(g Diagnostic) bool { return g.Code == "endpoint_removal_unverified" }) {
		t.Fatal("null alone claimed proven removal")
	}
	f.importSource("endpoint-later", "Later", false, nil)
	again, err := s.engine.Analyze(t.Context(), saved, nil)
	if err != nil || integrationBytes(t, again) != integrationBytes(t, report) {
		t.Fatalf("endpoint input moved %v", err)
	}
	invalid := start
	changed := *start.EndpointReview
	invalid.EndpointReview = &changed
	changed.IdempotencyKey = "invalid-endpoint"
	changed.BeforeEndpointID = f.node
	if _, err = s.Start(t.Context(), f.project.ID, invalid); err == nil {
		t.Fatal("non-HTTP explicit root accepted")
	}
	claim, err := f.jobs.Claim(t.Context(), "endpoint-worker")
	if err != nil || claim == nil {
		t.Fatalf("claim %v", err)
	}
	if err = s.execute(t.Context(), claim); err != nil {
		t.Fatal(err)
	}
	complete, err := f.jobs.Get(t.Context(), f.project.ID, job.ID)
	if err != nil || complete.Status != "completed" {
		t.Fatalf("endpoint worker %+v %v", complete, err)
	}
}

func TestB43EndpointWriteSurvivesEarlierReadToSameTable(t *testing.T) {
	before, _ := endpointReviewFixture()
	edges := slices.Clone(before.State.Edges)
	edges = append(edges, model.Edge{ID: "aaa-read", Kind: "reads", From: "step", To: "table"})
	graph := supportedGraph(slices.Clone(before.State.Nodes), edges)
	in := engineInput()
	in.Kind = "endpoint_review"
	report, err := analyzeEndpointReview(t.Context(), in, &EndpointReviewPayload{BeforeEndpointID: "endpoint", AfterEndpointID: new("endpoint")}, graph, graph, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := readRecords[EndpointItemDetail](t, report.Snapshot, "findings")
	if !slices.ContainsFunc(rows, func(row EndpointItemDetail) bool { return row.Category == "write" && row.Object.ID == "table" }) {
		t.Fatal("read to same table masked the write")
	}
}

func TestB43EndpointClosingErrorCyclesRemainVisible(t *testing.T) {
	for _, target := range []string{"step", "handler"} {
		t.Run(target, func(t *testing.T) {
			before, _ := endpointReviewFixture()
			edges := slices.Clone(before.State.Edges)
			edges = append(edges, model.Edge{ID: "error-cycle", Kind: "error", From: "step", To: target})
			graph := supportedGraph(slices.Clone(before.State.Nodes), edges)
			in := engineInput()
			in.Kind = "endpoint_review"
			report, err := analyzeEndpointReview(t.Context(), in, &EndpointReviewPayload{BeforeEndpointID: "endpoint", AfterEndpointID: new("endpoint")}, graph, graph, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			found := map[string]bool{}
			for _, row := range readRecords[EndpointItemDetail](t, report.Snapshot, "findings") {
				if row.Category != "error_branch" {
					continue
				}
				for _, step := range row.Witness.Steps {
					if step.ID == "error-cycle" {
						found[row.Side] = true
					}
				}
			}
			if !found["before"] || !found["after"] {
				t.Fatalf("closing cycle missing %+v", found)
			}
			if report.Snapshot.Progress.States > 30 {
				t.Fatal("cycle was enqueued indefinitely")
			}
		})
	}
}
