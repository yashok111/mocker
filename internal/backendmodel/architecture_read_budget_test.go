package backendmodel

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func TestArchitectureReadAdmissionAndCancellation(t *testing.T) {
	t.Parallel()
	var cache architectureReadCache
	entered, unblock := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var wg sync.WaitGroup
	wg.Go(func() {
		_, err := cache.load(ctx, "one", func() (*architectureProjection, error) {
			close(entered)
			<-unblock
			return nil, ctx.Err()
		})
		if !errors.Is(err, context.Canceled) {
			t.Errorf("canceled read: %v", err)
		}
	})
	<-entered
	_, err := cache.load(t.Context(), "two", func() (*architectureProjection, error) {
		t.Error("busy request allocated a graph")
		return nil, nil
	})
	assertFault(t, err, "backend_projection_busy")
	cancel()
	close(unblock)
	wg.Wait()
	p, err := cache.load(t.Context(), "two", func() (*architectureProjection, error) { return newArchitectureProjection(&DiagramVersion{}), nil })
	if err != nil || p == nil {
		t.Fatalf("cancellation leaked admission: %v", err)
	}
}

func TestArchitectureReadCacheBindsExactContextAndEvicts(t *testing.T) {
	t.Parallel()
	var cache architectureReadCache
	loads := 0
	load := func() (*architectureProjection, error) {
		loads++
		return newArchitectureProjection(&DiagramVersion{}), nil
	}
	for _, key := range []string{"project/diagram1/target1/context/root1", "project/diagram1/target1/context/root1", "project/diagram1/target1/components/root2", "project/diagram1/target1/context/root1"} {
		if _, err := cache.load(t.Context(), key, load); err != nil {
			t.Fatal(err)
		}
	}
	if loads != 3 {
		t.Fatalf("expected hit then eviction, got %d loads", loads)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := cache.load(ctx, "project/diagram1/target1/context/root1", load); !errors.Is(err, context.Canceled) {
		t.Fatalf("cache ignored cancellation: %v", err)
	}
}

func TestArchitectureQueryAndCompareShareAdmission(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "admission")
	v, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: diagramTestDocument(p.CurrentRevisionID), IdempotencyKey: "diagram"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = r.architectureReads.load(t.Context(), "occupied", func() (*architectureProjection, error) {
		_, queryErr := r.QueryDiagram(t.Context(), p.ID, DiagramQueryInput{Pin: v.Pin, Level: "context", RootID: v.Document.Payload.PrimarySystemID, Origin: "all", Section: "elements", Limit: 1})
		assertFault(t, queryErr, "backend_projection_busy")
		_, compareErr := r.CompareDiagrams(t.Context(), p.ID, DiagramCompareInput{Before: v.Pin, After: v.Pin, Limit: 1})
		assertFault(t, compareErr, "backend_projection_busy")
		return newArchitectureProjection(v), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CompareDiagrams(t.Context(), p.ID, DiagramCompareInput{Before: v.Pin, After: v.Pin, Limit: 1}); err != nil {
		t.Fatal(err)
	}
}
