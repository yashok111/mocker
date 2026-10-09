package backendmodel

import (
	"context"
	"encoding/json/v2"
	"errors"
	"sync"
)

const MaxArchitectureReadConcurrency = 1

// The qualified Education Platform gap array alone is 9,221,500 bytes.
// Keep bounded room for its element/link/member indexes while still retaining
// only one projection and releasing it before the next graph build.
const MaxArchitectureCacheBytes = 24 << 20

// One retained projection and one in-flight builder bound the multiplicative
// cost of large source graphs. The cache holds projection data only, never the
// native graph, source snippets or manifests. Cache misses fail fast rather than
// retaining arbitrarily many waiting requests. The wire-size admission is a
// conservative payload budget, not a claim about process RSS.
type architectureReadCache struct {
	mu         sync.Mutex
	active     bool
	key        string
	projection *architectureProjection
}

func architectureBusy() error {
	return &FaultError{Status: 503, Code: "backend_projection_busy", Message: "Architecture projection is busy; retry the same pinned read", Retryable: true, Details: map[string]any{"concurrencyLimit": MaxArchitectureReadConcurrency, "retryAfterSeconds": 1}}
}

func (c *architectureReadCache) admit(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.active {
		return nil, architectureBusy()
	}
	c.active = true
	c.key, c.projection = "", nil
	return c.release, nil
}

func (c *architectureReadCache) release() {
	c.mu.Lock()
	c.active = false
	c.mu.Unlock()
}

func (c *architectureReadCache) load(ctx context.Context, key string, build func() (*architectureProjection, error)) (*architectureProjection, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	c.mu.Lock()
	if c.key == key && c.projection != nil {
		p := c.projection
		c.mu.Unlock()
		return p, nil
	}
	if c.active {
		c.mu.Unlock()
		return nil, architectureBusy()
	}
	c.active = true
	// Release the old projection before allocating another graph.
	c.key, c.projection = "", nil
	c.mu.Unlock()
	defer c.release()
	p, err := build()
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if p == nil {
		return nil, invalid("projection", "Missing architecture projection")
	}
	// These indexes are only needed during construction.
	p.identityKeys, p.memberSeen = nil, nil
	counter := &projectionByteBudget{ctx: ctx, remaining: MaxArchitectureCacheBytes}
	err = json.MarshalWrite(counter, struct {
		Elements map[string]ArchitectureElement
		Links    map[string]ArchitectureLink
		Members  map[string][]DiagramMember
		Gaps     []DiagramGap
	}{p.elements, p.links, p.members, p.gaps})
	if err == nil {
		c.mu.Lock()
		c.key, c.projection = key, p
		c.mu.Unlock()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return p, nil
}

var errProjectionCacheBudget = errors.New("projection exceeds cache budget")

type projectionByteBudget struct {
	ctx       context.Context
	remaining int
}

func (w *projectionByteBudget) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > w.remaining {
		return 0, errProjectionCacheBudget
	}
	w.remaining -= len(p)
	return len(p), nil
}

func (r *Repo) queryArchitecture(ctx context.Context, pid string, v *DiagramVersion, in DiagramQueryInput) (*DiagramPage, error) {
	// Recheck target/lease visibility even on a hit. The diagram itself was read
	// under the requested project before arriving here; cache keys cannot cross
	// project authorization or immutable target/provenance boundaries.
	if err := r.validateAnalysisLeaseTarget(ctx, pid, v.Document.Target); err != nil {
		return nil, err
	}
	key, err := requestDigest(struct {
		ProjectID                                       string
		Pin                                             DiagramPin
		GapScope                                        *ArchitectureGapScope
		TargetHash, ProvenanceHash, Level, Root, Policy string
	}{pid, v.Pin, in.GapScope, v.TargetHash, v.ProvenanceHash, in.Level, in.RootID, "architecture-v1"})
	if err != nil {
		return nil, err
	}
	p, err := r.architectureReads.load(ctx, key, func() (*architectureProjection, error) {
		graph, err := r.readArchitectureGraph(ctx, pid, v.Document.Target)
		if err != nil {
			return nil, err
		}
		if v.TargetHash != graph.Pins.TargetHash {
			return nil, diagramPinMismatch()
		}
		return projectArchitecture(ctx, v, graph, in)
	})
	if err != nil {
		return nil, err
	}
	return architecturePage(ctx, v, p, in)
}
