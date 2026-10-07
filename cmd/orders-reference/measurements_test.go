package main

import (
	"fmt"
	"slices"
	"testing"

	o "github.com/yashok111/mocker/internal/backendobservations"
	"github.com/yashok111/mocker/internal/testleak"
)

func TestMain(m *testing.M) { testleak.VerifyTestMain(m) }

// review 2026-10-06, F166: the import batches came out in map order, so two
// runs over identical records printed them differently. Twenty traces make a
// map order that happens to match first-seen order practically impossible.
func TestTraceBatchesFollowFirstSeenOrder(t *testing.T) {
	records := make([]o.Record, 0, 41)
	want := make([]string, 0, 21)
	for i := range 20 {
		trace := fmt.Sprintf("t%02d", 19-i)
		want = append(want, "v/"+trace)
		records = append(records, o.Record{TraceID: trace, SpanID: "a"}, o.Record{TraceID: trace, SpanID: "b"})
	}
	records = append(records, o.Record{SpanID: "m"})
	want = append(want, "v/measurements")
	batches := traceBatches(records, &o.ObservationContext{}, "v", "e")
	got := make([]string, 0, len(batches))
	for _, b := range batches {
		got = append(got, b.Name)
		if len(b.Records) == 0 {
			t.Fatalf("%s has no records", b.Name)
		}
	}
	if !slices.Equal(got, want) {
		t.Fatalf("batch order %v, want %v", got, want)
	}
	if len(batches[0].Records) != 2 || batches[0].Records[1].SpanID != "b" {
		t.Fatalf("records within a trace reordered: %+v", batches[0].Records)
	}
}
