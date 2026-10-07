package backendobservations

import (
	"testing"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/testkit"
)

func TestObservationImmutableReceiptAndConflict(t *testing.T) {
	db := testkit.NewDB(t)
	project, e := bm.NewRepo(db).Create(t.Context(), bm.CreateInput{Name: "observations", IdempotencyKey: "create"})
	if e != nil {
		t.Fatal(e)
	}
	r := NewRepo(db)
	c := testContext()
	in := ImportInput{Mode: "create", Context: &c, Name: "first", BatchID: "batch1", IdempotencyKey: "key1", Records: []Record{testSpan()}}
	first, e := r.Import(t.Context(), project.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	next := ImportInput{Mode: "append", SetID: first.SetID, ExpectedVersion: 1, BatchID: "batch2", IdempotencyKey: "key2", Records: in.Records}
	second, e := r.Import(t.Context(), project.ID, next)
	if e != nil || second.RecordCount != 1 {
		t.Fatal(second, e)
	}
	replay, e := r.Import(t.Context(), project.ID, in)
	if e != nil || *replay != *first {
		t.Fatal("receipt replay changed", e)
	}
	next.IdempotencyKey = "different"
	next.BatchID = "different"
	next.ExpectedVersion = 2
	next.Records[0].Status = "error"
	if _, e = r.Import(t.Context(), project.ID, next); e == nil {
		t.Fatal("conflicting record replaced")
	}
	page, e := r.Records(t.Context(), project.ID, first.SetID, 1, 100, "")
	if e != nil || len(page.Items) != 1 || page.Items[0].Status != "ok" {
		t.Fatal(page, e)
	}
	if _, e = db.W.ExecContext(t.Context(), `UPDATE backend_observation_versions SET document='{}'`); e == nil {
		t.Fatal("immutable row update allowed")
	}
}
