// routes.go wires the per-workspace runtime (runtime.go: route table, plus
// from P1b a generator and its response variants) into Step 5 of
// serveResolved — the point where P0 always answered a bare 404. It owns
// three things: the SpecSource contract a caller must supply to get route
// matching (and, from P1b, response generation) at all, the single-flighted
// LRU cache of compiled runtimes keyed by workspace revision, and the 404
// answer Step 5 gives when nothing matches.
package mockplane

import (
	"container/list"
	"context"
	"fmt"
	"net/http"
	"sync"

	"golang.org/x/sync/singleflight"

	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/workspaces"
)

// SpecSource reads everything a workspace's runtime is built from: the route
// table rows, the stored normalized document, and every response variant.
// [*specs.Repo] satisfies it. The interface is declared over [router.Route]
// and [gen.ResponseVariant] rather than specs.* types specifically so this
// package never has to import internal/specs — workspaces stay unaware of
// specs, and the mock plane only ever asks for rows shaped the way the route
// table and the generator already understand them.
//
// A nil SpecSource is legal: it means "no spec support", and Step 5 falls
// back to the plain 404 it always gave before this file existed.
type SpecSource interface {
	Routes(ctx context.Context, specID int64) ([]router.Route, error)
	Normalized(ctx context.Context, specID int64) ([]byte, error)
	Variants(ctx context.Context, specID int64) (map[int64][]gen.ResponseVariant, error)
}

// OverrideSource reads every op_overrides row of a workspace in one query.
// [*overrides.Repo] satisfies it — declared as an interface for exactly the
// reason SpecSource is above it: this package never has to import the
// concrete overrides.Repo type, and a test can fake the one method the
// serving path actually needs without standing up a database.
//
// A nil OverrideSource is legal, exactly like a nil SpecSource: it means "no
// overrides support", and buildRuntime (runtime.go) leaves every matched
// request answering precisely what it would have before this phase existed
// (HARD RULE 6 — no recipes must mean no change, and "no OverrideSource at
// all" is the degenerate case of "no recipes").
type OverrideSource interface {
	ForWorkspace(ctx context.Context, workspaceID int64) (map[string]*overrides.Row, error)
}

// routeCacheKey identifies one compiled table: a workspace pinned to the
// exact revision it was built from. Folding revision into the key itself —
// rather than caching by workspace id and comparing a stored revision by
// hand — means a revision bump is nothing but an ordinary cache miss; there
// is no separate invalidation step that could fall out of sync with
// workspaces.Repo.Update's bump.
type routeCacheKey struct {
	workspaceID int64
	revision    int64
}

// routeCacheEntry is one LRU node: the key it was filed under (so eviction
// can remove the matching map entry) and the built runtime itself.
type routeCacheEntry struct {
	key routeCacheKey
	rt  *runtime
}

// routeCache is the process's cache of compiled per-workspace runtimes
// (runtime.go), bounded to cfg.RuntimeCache entries and evicting
// least-recently-used. Building one touches the database (SpecSource's three
// methods) and does real work — parsing the normalized document, compiling
// the sorted match structure, joining response variants — so
// [routeCache.getOrBuild] runs it through singleflight: however many
// requests arrive concurrently for the same cold (workspace, revision), only
// the first actually builds — the rest wait for that result instead of each
// repeating the work. DESIGN §20 calls out a stalled event loop under load as
// a real risk, not a hypothetical one.
type routeCache struct {
	capacity int

	mu    sync.RWMutex
	ll    *list.List // front = most recently used
	items map[routeCacheKey]*list.Element

	group singleflight.Group
}

// newRouteCache builds a cache holding at most capacity tables. A
// non-positive capacity is clamped to 1 rather than left to mean "unbounded"
// or "always evict": either of those would make a cache-size misconfig fail
// in a way that also breaks the single-flight guarantee (a table evicted the
// instant it's inserted can never serve a second, slightly-later request from
// cache, forcing every request to pay for at least a singleflight join).
func newRouteCache(capacity int) *routeCache {
	if capacity < 1 {
		capacity = 1
	}
	return &routeCache{
		capacity: capacity,
		ll:       list.New(),
		items:    make(map[routeCacheKey]*list.Element),
	}
}

// getOrBuild returns the runtime cached for key, or calls build to produce
// one on a miss. Concurrent misses for the same key collapse into one build
// via singleflight; every caller — leader and joiners alike — gets the same
// *runtime back.
func (c *routeCache) getOrBuild(key routeCacheKey, build func() (*runtime, error)) (*runtime, error) {
	if rt, ok := c.get(key); ok {
		return rt, nil
	}

	// singleflight keys are strings; this is only that key's wire form for
	// Do, the routeCacheKey struct above remains what the LRU itself indexes.
	sfKey := fmt.Sprintf("%d:%d", key.workspaceID, key.revision)
	v, err, _ := c.group.Do(sfKey, func() (any, error) {
		rt, err := build()
		if err != nil {
			return nil, err
		}
		c.put(key, rt)
		return rt, nil
	})
	if err != nil {
		return nil, err
	}
	return v.(*runtime), nil
}

// get returns the cached runtime for key, if any, marking it most-recently-used.
func (c *routeCache) get(key routeCacheKey) (*runtime, bool) {
	// Read lock first: every request of every workspace passes through
	// here, and the common case — the hot workspace already at the front
	// of the list — needs no list mutation at all. Only a hit that is NOT
	// at the front takes the write lock to move it, re-checking under it
	// because the entry may have been evicted in between.
	c.mu.RLock()
	el, ok := c.items[key]
	if ok && c.ll.Front() == el {
		rt := el.Value.(*routeCacheEntry).rt
		c.mu.RUnlock()
		return rt, true
	}
	c.mu.RUnlock()
	if !ok {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok = c.items[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*routeCacheEntry).rt, true
}

// put inserts rt under key, evicting the least-recently-used entry if the
// cache is now over capacity. A second insert under a key already present
// (which getOrBuild's singleflight makes impossible in practice — only one
// build per key ever reaches here — but which costs nothing to handle
// correctly anyway) refreshes it in place instead of duplicating it.
func (c *routeCache) put(key routeCacheKey, rt *runtime) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[key]; ok {
		el.Value.(*routeCacheEntry).rt = rt
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&routeCacheEntry{key: key, rt: rt})
	c.items[key] = el
	if c.ll.Len() > c.capacity {
		oldest := c.ll.Back()
		c.ll.Remove(oldest)
		delete(c.items, oldest.Value.(*routeCacheEntry).key)
	}
}

// serveRoute is Step 5 of serveResolved (DESIGN §6/§8): match segments
// against ws's compiled route table, if it has one, and hand a match to the
// response generator.
//
//   - No spec attached AND no [CustomSource] wired: the same 404 Step 5 has
//     always given, unchanged — buildRuntime is never even called, so a
//     workspace that uses neither feature pays nothing extra (P1c2).
//   - No spec attached but custom endpoints exist: a runtime IS built —
//     table entries from [CustomSource] alone — so those endpoints still
//     serve and everything else still 404s (see buildRuntime's own doc
//     comment in runtime.go for the spec-less build path).
//   - A runtime fails to build (a database error, not a bad spec — a spec
//     that merely parses badly was already handled at import time): a
//     logged 500, same shape P1a already gave for the equivalent failure.
//   - A table exists but nothing in it matches method+segments: a 404 in the
//     same shape.
//   - A route matches: [Plane.serveGenerated] answers a spec operation,
//     [Plane.serveCustom] (custom.go, P1c2) answers a custom endpoint —
//     router.Route.Custom is what tells the two apart.
func (p *Plane) serveRoute(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, segments []string) {
	hasSpec := p.specs != nil && ws.SpecID != nil
	if !hasSpec && p.custom == nil {
		p.serveNoRoute(w, r, ws, segments)
		return
	}

	rt, err := p.runtimeFor(r.Context(), ws)
	if err != nil {
		p.log.Error("build runtime", "workspace", ws.Slug, "spec", specIDForLog(ws), "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
		return
	}

	match, ok := rt.table.Match(r.Method, segments)
	if !ok {
		p.serveNoRoute(w, r, ws, segments)
		return
	}
	// Routing decided WHAT matched; record that now, before serveGenerated/
	// serveCustom run any override logic that might turn this into a 404 of
	// its own (route_off) — see markTrafficMatch's own doc comment
	// (traffic.go) for why that 404 must still count as a match, not "none".
	markTrafficMatch(r, match.Route)
	if match.Route.Custom {
		p.serveCustom(w, r, ws, rt, match)
		return
	}

	// D7.1/D7.2: the base scope is computed HERE, once, next to the Match
	// that produced it — POSITIONALLY, off the segments the match already
	// succeeded against, through router.BaseValues (the one owner of "which
	// segments of a base path are parameters," D18.1) — never by name out
	// of match.Params (D4.4). router.BaseValues only ever answers false
	// when segments is shorter than basePath's own segment count, a state
	// Match having just succeeded makes impossible, so the false branch is
	// an unreachable-in-practice guard against an index panic, not a real
	// case to serve differently. serveCustom above does not receive one: a
	// custom endpoint is never a resource.
	values, _ := router.BaseValues(ws.Settings.BasePath, segments)
	base := resources.EncodeScope(values)
	p.serveGenerated(w, r, ws, rt, match, base)
}

// specIDForLog renders ws.SpecID for a log line without dereferencing a nil
// pointer: P1c2 lets serveRoute reach the runtime-build error path for a
// spec-less workspace (one with custom endpoints and nothing else), so the
// old `*ws.SpecID` here would be a nil-pointer panic on an unauthenticated
// request the very first time a build actually failed for such a workspace.
func specIDForLog(ws *workspaces.Workspace) any {
	if ws.SpecID == nil {
		return nil
	}
	return *ws.SpecID
}

// serveNoRoute is the exact 404 shape Step 5 answers when nothing in ws's
// table matches — factored into one function so the "no spec, no custom
// endpoints" branch, the "table built, nothing matched" branch of
// serveRoute, and route_off (respond.go's serveGenerated, and custom.go's
// serveCustom) all give byte-identical bodies for the same workspace: DESIGN
// §8 wants a disabled route indistinguishable from one that was never
// declared, and that only holds if every caller answers through here.
//
// ws.Settings.NotFoundBody, when set, replaces ONLY the body — same 404
// status, same CORS headers (already on w from Step 2 of serveResolved,
// long before this runs) — with whatever JSON an operator configured
// instead of the default machine-readable error shape. httpx.JSON is reused
// rather than a second raw writer: json.RawMessage's own MarshalJSON hands
// the stored bytes back verbatim, and NotFoundBody was already proven valid
// JSON at write time (it went through the SAME decoder that parsed the rest
// of the settings payload, whose scanner cannot hand a struct field a
// syntactically-invalid RawMessage — see internal/workspaces).
func (p *Plane) serveNoRoute(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, segments []string) {
	if len(ws.Settings.NotFoundBody) > 0 {
		httpx.JSON(w, http.StatusNotFound, ws.Settings.NotFoundBody)
		return
	}
	httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, fmt.Sprintf(
		"no route for %s %s in workspace %q", r.Method, joinForDisplay(segments), ws.Slug))
}
