package backendobservations

import (
	"fmt"
	bm "github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/testkit"
	"testing"
)

// A 500-record batch is the public admission unit. The external harness declares
// larger retained set sizes explicitly rather than silently changing import caps.
func BenchmarkObservationImport500(b *testing.B) {
	for b.Loop() {
		b.StopTimer()
		db := testkit.NewDB(b)
		graphs := bm.NewRepo(db)
		project, err := graphs.Create(b.Context(), bm.CreateInput{Name: "benchmark", IdempotencyKey: "benchmark"})
		if err != nil {
			b.Fatal(err)
		}
		repo := NewRepo(db)
		c := testContext()
		records := make([]Record, 500)
		for i := range records {
			records[i] = testSpan()
			records[i].ID = fmt.Sprintf("record-%d", i)
			records[i].SpanID = fmt.Sprintf("%016x", i+1)
		}
		b.StartTimer()
		if _, err = repo.Import(b.Context(), project.ID, ImportInput{Mode: "create", Context: &c, Name: "bench", BatchID: "batch", IdempotencyKey: "key", Records: records}); err != nil {
			b.Fatal(err)
		}
		b.StopTimer()
		db.Close()
		b.StartTimer()
	}
}
func BenchmarkObservationQuery500(b *testing.B) {
	db := testkit.NewDB(b)
	graphs := bm.NewRepo(db)
	project, err := graphs.Create(b.Context(), bm.CreateInput{Name: "benchmark", IdempotencyKey: "benchmark"})
	if err != nil {
		b.Fatal(err)
	}
	repo := NewRepo(db)
	c := testContext()
	records := make([]Record, 500)
	for i := range records {
		records[i] = testSpan()
		records[i].ID = fmt.Sprintf("record-%d", i)
		records[i].SpanID = fmt.Sprintf("%016x", i+1)
	}
	v, err := repo.Import(b.Context(), project.ID, ImportInput{Mode: "create", Context: &c, Name: "bench", BatchID: "batch", IdempotencyKey: "key", Records: records})
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err = repo.Records(b.Context(), project.ID, v.SetID, 1, 500, ""); err != nil {
			b.Fatal(err)
		}
	}
}
