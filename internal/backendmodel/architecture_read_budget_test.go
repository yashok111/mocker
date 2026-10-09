package backendmodel

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestArchitectureReadCacheRetainsEducationSizedProjection(t *testing.T) {
	var cache architectureReadCache
	p := newArchitectureProjection(&DiagramVersion{})
	for i := range 38106 {
		scope := "unrelated"
		if i < 153 {
			scope = "boundary"
		}
		p.gaps = append(p.gaps, DiagramGap{
			ID:          fmt.Sprintf("20000000-0000-4000-8000-%012d", i),
			SubjectID:   fmt.Sprintf("30000000-0000-4000-8000-%012d", i),
			Code:        "unresolved_membership",
			Explanation: "Source dependency has a missing or ambiguous explicit architecture membership",
			Scope:       scope,
		})
	}
	raw, err := json.Marshal(p.gaps)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) <= 8<<20 || len(raw) >= 24<<20 {
		t.Fatalf("fixture must exercise the observed cache gap, got %d bytes", len(raw))
	}
	t.Logf("qualified gap array bytes: %d", len(raw))
	loads := 0
	build := func() (*architectureProjection, error) {
		loads++
		return p, nil
	}
	for range 2 {
		if _, err := cache.load(t.Context(), "exact-education-projection", build); err != nil {
			t.Fatal(err)
		}
	}
	if loads != 1 {
		t.Fatalf("qualified projection rebuilt on identical read: %d builds", loads)
	}
}

func TestArchitectureReadCacheOversizeEvictsWithoutRetention(t *testing.T) {
	var cache architectureReadCache
	if _, err := cache.load(t.Context(), "small", func() (*architectureProjection, error) {
		return newArchitectureProjection(&DiagramVersion{}), nil
	}); err != nil {
		t.Fatal(err)
	}
	loads := 0
	build := func() (*architectureProjection, error) {
		loads++
		if cache.projection != nil || cache.key != "" {
			t.Fatal("old projection retained while replacement is allocated")
		}
		p := newArchitectureProjection(&DiagramVersion{})
		p.gaps = []DiagramGap{{ID: "oversize", Code: "fixture", Explanation: strings.Repeat("x", 24<<20)}}
		return p, nil
	}
	for range 2 {
		p, err := cache.load(t.Context(), "oversize", build)
		if err != nil || p == nil {
			t.Fatalf("oversize projection should remain readable: %v", err)
		}
		if cache.projection != nil || cache.key != "" {
			t.Fatal("oversize projection was retained")
		}
		page, err := architecturePage(t.Context(), &DiagramVersion{}, p, DiagramQueryInput{Section: "elements", Origin: "all", Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(page)
		if err != nil {
			t.Fatal(err)
		}
		var envelope struct {
			Cache struct {
				Status     string `json:"status"`
				Reason     string `json:"reason"`
				LimitBytes int    `json:"limitBytes"`
			} `json:"cache"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Cache.Status != "not_retained" || envelope.Cache.Reason != "byte_budget" || envelope.Cache.LimitBytes <= 0 {
			t.Fatalf("readable uncached projection lacks retention explanation: %+v", envelope.Cache)
		}
	}
	if loads != 2 {
		t.Fatalf("oversize reads unexpectedly reused retained data: %d builds", loads)
	}
}

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
