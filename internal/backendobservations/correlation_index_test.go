package backendobservations

import (
	"encoding/json/jsontext"
	"strings"
	"testing"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/testkit"
)

// review 2026-10-06, F158/F34: identity, source-locator and fingerprint
// inference now read indexes built once per Correlate call instead of
// scanning the graph per record. Nothing covered those three paths, so pin
// the candidates each one finds, including an evidence row on an edge.
func TestCorrelationInferenceThroughIndexes(t *testing.T) {
	db := testkit.NewDB(t)
	project, e := bm.NewRepo(db).Create(t.Context(), bm.CreateInput{Name: "correlation-index", IdempotencyKey: "correlation-index"})
	if e != nil {
		t.Fatal(e)
	}
	repo := NewRepo(db)
	c := testContext()
	fp := strings.Repeat("f", 64)
	repository := "01900000-0000-7000-8000-0000000000aa"
	loc, fingerprinted, ident := testSpan(), testSpan(), testSpan()
	loc.ID, loc.SpanID, loc.Attrs = "span-loc", strings.Repeat("3", 16), &Attributes{SourcePath: "orders/cancel.go", SourceLine: 7}
	fingerprinted.ID, fingerprinted.SpanID, fingerprinted.Attrs = "span-fp", strings.Repeat("4", 16), &Attributes{Fingerprint: fp}
	ident.ID, ident.SpanID = "span-id", strings.Repeat("5", 16)
	ident.IdentityRef = &IdentityRef{RepositoryID: repository, ProviderNamespace: "ns", ExternalKey: "orders.cancel", RecordType: "node"}
	set, e := repo.Import(t.Context(), project.ID, ImportInput{Mode: "create", Context: &c, Name: "inference", BatchID: "a", IdempotencyKey: "a", Records: []Record{loc, fingerprinted, ident}})
	if e != nil {
		t.Fatal(e)
	}
	line, other := int64(7), int64(8)
	graph := &bm.EffectiveGraphSnapshot{
		Pins: bm.EffectiveGraphPins{TargetHash: strings.Repeat("a", 64), BaseSemanticHash: strings.Repeat("b", 64)},
		State: bm.RevisionState{
			Nodes: []bm.Node{{ID: "node-a", Attributes: map[string]jsontext.Value{"queryFingerprint": jsontext.Value(`"` + fp + `"`)}}, {ID: "node-b", Attributes: map[string]jsontext.Value{"queryFingerprint": jsontext.Value(`1`)}}, {ID: "node-c"}},
			Edges: []bm.Edge{{ID: "edge-a"}},
			Evidence: []bm.Evidence{
				{ID: "ev-1", SubjectID: "node-c", Source: bm.EvidenceSource{File: "orders/cancel.go", StartLine: &line}},
				{ID: "ev-2", SubjectID: "edge-a", Source: bm.EvidenceSource{File: "orders/cancel.go", StartLine: &line}},
				{ID: "ev-3", SubjectID: "node-a", Source: bm.EvidenceSource{File: "orders/cancel.go", StartLine: &other}},
				{ID: "ev-4", SubjectID: "node-b", Source: bm.EvidenceSource{RepositoryID: repository, File: "orders/cancel.go", StartLine: &line}},
			},
		},
		Source: &bm.SourceGraphSnapshot{Identities: []bm.QualifiedSourceIdentity{{RecordType: "node", ID: "node-b", RepositoryID: repository, ProviderNamespace: "ns", ExternalKey: "orders.cancel"}, {RecordType: "node", ID: "node-c", RepositoryID: repository, ProviderNamespace: "other", ExternalKey: "orders.cancel"}}},
	}
	service := &Service{Repo: repo, Graphs: correlationGraphs{graph: graph}}
	out, e := service.Correlate(t.Context(), project.ID, set.SetID, CorrelateInput{Observation: *set, RevisionID: project.CurrentRevisionID, SourceHash: graph.Pins.BaseSemanticHash, TargetGraphHash: graph.Pins.TargetHash, ServiceID: c.Source.ServiceID, Policy: CorrelationPolicy, Overrides: []Override{}, Settings: CorrelationSettings{InferSourceLocator: true, InferFingerprint: true}, IdempotencyKey: "inference"})
	if e != nil {
		t.Fatal(e)
	}
	got := map[string][]string{}
	for _, row := range out.Rows {
		for _, candidate := range row.Candidates {
			got[row.RecordID] = append(got[row.RecordID], candidate.RecordType+":"+candidate.ID)
		}
	}
	want := map[string]string{"span-loc": "edge:edge-a,node:node-c", "span-fp": "node:node-a", "span-id": "node:node-b"}
	for id, candidates := range want {
		if strings.Join(got[id], ",") != candidates {
			t.Fatalf("%s candidates = %v, want %s", id, got[id], candidates)
		}
	}
}
