package backendobservations

import (
	"slices"
	"strings"
	"testing"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/testkit"
)

func correlateRecords(t *testing.T, graph *bm.EffectiveGraphSnapshot, settings CorrelationSettings, records []Record) *CorrelationSnapshot {
	t.Helper()
	db := testkit.NewDB(t)
	project, e := bm.NewRepo(db).Create(t.Context(), bm.CreateInput{Name: "provenance", IdempotencyKey: "provenance"})
	if e != nil {
		t.Fatal(e)
	}
	repo := NewRepo(db)
	c := testContext()
	first, e := repo.Import(t.Context(), project.ID, ImportInput{Mode: "create", Context: &c, Name: "provenance", BatchID: "a", IdempotencyKey: "a", Records: records})
	if e != nil {
		t.Fatal(e)
	}
	graph.Pins = bm.EffectiveGraphPins{TargetHash: strings.Repeat("a", 64), BaseSemanticHash: strings.Repeat("b", 64)}
	service := &Service{Repo: repo, Graphs: correlationGraphs{graph: graph}}
	in := CorrelateInput{Observation: *first, RevisionID: project.CurrentRevisionID, SourceHash: graph.Pins.BaseSemanticHash, TargetGraphHash: graph.Pins.TargetHash, ServiceID: c.Source.ServiceID, Policy: CorrelationPolicy, Settings: settings, Overrides: []Override{}, IdempotencyKey: "correlate"}
	out, e := service.Correlate(t.Context(), project.ID, first.SetID, in)
	if e != nil {
		t.Fatal(e)
	}
	return out
}

func provenanceRow(t *testing.T, out *CorrelationSnapshot, id string) CorrelationRow {
	t.Helper()
	i := slices.IndexFunc(out.Rows, func(r CorrelationRow) bool { return r.RecordID == id })
	if i < 0 {
		t.Fatalf("row %s missing", id)
	}
	return out.Rows[i]
}

// Review 2026-10-06, F157: the method names the strategy that produced the
// candidates; a requested strategy that matched nothing never overwrites it.
func TestObservationCorrelationMethodNamesTheMatchingStrategy(t *testing.T) {
	node := "01900000-0000-7000-8000-000000000001"
	line := int64(10)
	graph := &bm.EffectiveGraphSnapshot{State: bm.RevisionState{Nodes: []bm.Node{{ID: node, Kind: "query"}}, Evidence: []bm.Evidence{{ID: "ev", SubjectID: node, Source: bm.EvidenceSource{File: "a.go", StartLine: &line}}}}}
	located := testSpan()
	located.Attrs = &Attributes{SourcePath: "a.go", SourceLine: 10, Fingerprint: strings.Repeat("c", 64)}
	fingerprintOnly := testSpan()
	fingerprintOnly.ID = "span-2"
	fingerprintOnly.SpanID = strings.Repeat("3", 16)
	fingerprintOnly.Attrs = &Attributes{Fingerprint: strings.Repeat("c", 64)}
	unmatched := testSpan()
	unmatched.ID = "span-3"
	unmatched.SpanID = strings.Repeat("4", 16)
	unmatched.Attrs = &Attributes{SourcePath: "b.go", SourceLine: 1}
	out := correlateRecords(t, graph, CorrelationSettings{InferSourceLocator: true, InferFingerprint: true}, []Record{located, fingerprintOnly, unmatched})
	if row := provenanceRow(t, out, "span-1"); row.Method != "source_locator" || row.Outcome != "inferred" || row.Selected == nil || row.Selected.ID != node {
		t.Fatalf("locator match mislabelled: %+v", row)
	}
	if row := provenanceRow(t, out, "span-2"); row.Method != "none" || !slices.Contains(row.Reasons, "query_fingerprint_unsupported") {
		t.Fatalf("unsupported fingerprint inference hidden: %+v", row)
	}
	if row := provenanceRow(t, out, "span-3"); row.Method != "none" {
		t.Fatalf("unmatched locator claimed provenance: %+v", row)
	}
}

// Review 2026-10-06, F187: one stale explicit backendRef leaves its row
// unresolved instead of aborting the whole correlation with 422.
func TestObservationCorrelationStaleBackendRefIsUnresolvedRow(t *testing.T) {
	stale := testSpan()
	stale.BackendRef = &bm.DiagramRef{Kind: "record", RecordType: "node", ID: "01900000-0000-7000-8000-000000000009"}
	other := testSpan()
	other.ID = "span-2"
	other.SpanID = strings.Repeat("3", 16)
	out := correlateRecords(t, &bm.EffectiveGraphSnapshot{}, CorrelationSettings{}, []Record{stale, other})
	row := provenanceRow(t, out, "span-1")
	if row.Outcome != "unresolved" || row.Selected != nil || !slices.Contains(row.Reasons, "stale_backend_ref") {
		t.Fatalf("stale ref row %+v", row)
	}
	if len(out.Rows) != 2 {
		t.Fatalf("rows %d", len(out.Rows))
	}
}
