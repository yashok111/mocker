package backendmodel

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"testing"
)

// This inert size fixture matches the recorded cardinalities, not its semantic
// content. No imported application or confidential evidence is required.
func BenchmarkArchitectureRead(b *testing.B) {
	d := diagramTestDocument("10000000-0000-4000-8000-000000000090")
	v := &DiagramVersion{Document: d, ProjectID: "10000000-0000-4000-8000-000000000001", TargetHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Pin: DiagramPin{ID: "10000000-0000-4000-8000-000000000080", Version: 1, ContentHash: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
	g := &EffectiveGraphSnapshot{Pins: EffectiveGraphPins{TargetHash: v.TargetHash}}
	for i := range 25228 {
		g.State.Nodes = append(g.State.Nodes, Node{ID: fmt.Sprintf("20000000-0000-4000-8000-%012d", i)})
	}
	for i := range 32348 {
		g.State.Edges = append(g.State.Edges, Edge{ID: fmt.Sprintf("30000000-0000-4000-8000-%012d", i), Kind: "calls", From: g.State.Nodes[i%len(g.State.Nodes)].ID, To: g.State.Nodes[(i+1)%len(g.State.Nodes)].ID})
	}
	for i := range 106398 {
		g.State.Evidence = append(g.State.Evidence, Evidence{ID: fmt.Sprintf("40000000-0000-4000-8000-%012d", i), SubjectID: g.State.Edges[i%len(g.State.Edges)].ID})
	}
	q := DiagramQueryInput{Pin: v.Pin, Level: "context", RootID: d.Payload.PrimarySystemID, Origin: "all", Section: "elements", Limit: 2}
	for _, mode := range []string{"legacy", "compact-v1", "cached-compact-v1"} {
		b.Run(mode, func(b *testing.B) {
			in := q
			if mode != "legacy" {
				in.ResponseMode = "compact-v1"
			}
			var cache architectureReadCache
			build := func() (*architectureProjection, error) { return projectArchitecture(context.Background(), v, g, in) }
			if mode == "cached-compact-v1" {
				if _, err := cache.load(b.Context(), "exact", build); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				var page *DiagramPage
				var err error
				if mode == "cached-compact-v1" {
					var projection *architectureProjection
					projection, err = cache.load(b.Context(), "exact", build)
					if err == nil {
						page, err = architecturePage(b.Context(), v, projection, in)
					}
				} else {
					page, err = ProjectArchitecture(b.Context(), v, g, in)
				}
				if err != nil {
					b.Fatal(err)
				}
				raw, err := json.Marshal(page)
				if err != nil {
					b.Fatal(err)
				}
				b.ReportMetric(float64(len(raw)), "response-bytes")
			}
		})
	}
}
