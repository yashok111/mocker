package backendanalysis

import (
	"fmt"
	"testing"

	bm "github.com/yashok111/mocker/internal/backendmodel"
)

func BenchmarkScenarioMeasurement1000(b *testing.B) {
	data := metricData()
	template := data.Sets[0].Records
	data.Sets[0].Records = nil
	in := measureInput()
	in.ExecutionIDs = nil
	for i := 0; i < 1000; i++ {
		execution := fmt.Sprintf("run-%d", i)
		in.ExecutionIDs = append(in.ExecutionIDs, execution)
		for _, row := range template {
			row.ExecutionID = execution
			row.TraceID = execution
			data.Sets[0].Records = append(data.Sets[0].Records, row)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := MeasureScenario(b.Context(), in, data); err != nil {
			b.Fatal(err)
		}
	}
}

// Raw original durations, never pre-aggregated percentile inputs.
func BenchmarkMeasurementNearestRank100000(b *testing.B) {
	samples := make([]int64, 100000)
	for i := range samples {
		samples[i] = int64((i * 7919) % 100000)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = nearestRank(samples, 95)
	}
}
func BenchmarkSourceDiff10000Nodes30000Edges(b *testing.B) {
	graph := bm.EffectiveGraphSnapshot{}
	for i := 0; i < 10000; i++ {
		graph.State.Nodes = append(graph.State.Nodes, bm.Node{ID: fmt.Sprintf("node-%d", i), Kind: "service", Name: fmt.Sprintf("Service %d", i)})
	}
	for i := 0; i < 30000; i++ {
		graph.State.Edges = append(graph.State.Edges, bm.Edge{ID: fmt.Sprintf("edge-%d", i), Kind: "calls", From: fmt.Sprintf("node-%d", i%10000), To: fmt.Sprintf("node-%d", (i+1)%10000)})
	}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := structuralChanges(b.Context(), &graph, &graph); err != nil {
			b.Fatal(err)
		}
	}
}
