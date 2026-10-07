package backendobservations

import (
	"encoding/json/v2"
	"strings"
	"testing"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/testkit"
)

// Review 2026-10-06, F162: a correlate receipt stored the whole snapshot (up
// to 64 MiB) a second time, outside projectQuota and undeletable. The receipt
// now names the immutable correlation version; replay rebuilds the identical
// snapshot from it.
func TestCorrelationReceiptReferencesStoredVersion(t *testing.T) {
	db := testkit.NewDB(t)
	project, e := bm.NewRepo(db).Create(t.Context(), bm.CreateInput{Name: "receipts", IdempotencyKey: "receipts"})
	if e != nil {
		t.Fatal(e)
	}
	repo := NewRepo(db)
	c := testContext()
	first, e := repo.Import(t.Context(), project.ID, ImportInput{Mode: "create", Context: &c, Name: "receipts", BatchID: "a", IdempotencyKey: "a", Records: []Record{testSpan()}})
	if e != nil {
		t.Fatal(e)
	}
	graph := &bm.EffectiveGraphSnapshot{Pins: bm.EffectiveGraphPins{TargetHash: strings.Repeat("a", 64), BaseSemanticHash: strings.Repeat("b", 64)}}
	service := &Service{Repo: repo, Graphs: correlationGraphs{graph: graph}}
	in := CorrelateInput{Observation: *first, RevisionID: project.CurrentRevisionID, SourceHash: graph.Pins.BaseSemanticHash, TargetGraphHash: graph.Pins.TargetHash, ServiceID: c.Source.ServiceID, Policy: CorrelationPolicy, Overrides: []Override{}, IdempotencyKey: "correlate"}
	out, e := service.Correlate(t.Context(), project.ID, first.SetID, in)
	if e != nil {
		t.Fatal(e)
	}
	var receiptBytes, documentBytes int
	if e = db.R.QueryRowContext(t.Context(), `SELECT length(CAST(document AS BLOB)) FROM backend_observation_receipts WHERE project_id=? AND action=?`, project.ID, "correlate/"+first.SetID).Scan(&receiptBytes); e != nil {
		t.Fatal(e)
	}
	if e = db.R.QueryRowContext(t.Context(), `SELECT length(CAST(document AS BLOB)) FROM backend_observation_correlations_documents WHERE project_id=? AND set_id=?`, project.ID, first.SetID).Scan(&documentBytes); e != nil {
		t.Fatal(e)
	}
	if receiptBytes >= documentBytes || receiptBytes > 256 {
		t.Fatalf("receipt holds %d bytes for a %d-byte snapshot", receiptBytes, documentBytes)
	}
	replay, e := service.Correlate(t.Context(), project.ID, first.SetID, in)
	if e != nil {
		t.Fatal(e)
	}
	a, _ := json.Marshal(out)
	b, _ := json.Marshal(replay)
	if string(a) != string(b) {
		t.Fatalf("replay changed:\n%s\n%s", a, b)
	}
}
