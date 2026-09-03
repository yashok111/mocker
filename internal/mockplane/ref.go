// ref.go is P3c's resolver for the "ref" recipe (D4/D6 of the
// mocker-p3c-ref-recipe decisions): the per-request closure that lets a
// generated body carry a value a confirmed resource really holds — a
// /quizzes body whose subject_id is the id of a row the confirmed
// /subjects family actually stores — instead of a plausible value that
// matches nothing.
//
// The resolver owns EVERY reason a reference does not resolve: no entity
// store wired, no such family in this request's roster, no rows, no such
// property, a non-scalar value, a failed coercion, a store error. D4's
// round-2 correction is why: [recipes.refValue] knows the recipe's policy
// but cannot reach request state, and this closure reaches request state
// but would never learn about a decline made after it returned. One
// decision point, in the one place that can also mark the traffic (D7).
package mockplane

import (
	"context"
	"errors"
	"net/http"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/recipes"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/workspaces"
)

// newRefResolver builds the closure [Plane.serveGenerated] passes into
// [Plane.assembleResponse] as its ref parameter (D5) — construction lives
// HERE, in serveGenerated's own call, never inside assembleResponse itself:
// assembleResponse is also Preview's shared seam, and building the closure
// inside it would hand Preview a live resolver over real entity rows, the
// exact mistake D5 names and refuses.
//
// roster is rt.resources — the runtime's (workspace_id, revision)-cached,
// RouteFamily-keyed map of confirmed resources (runtime.go) — read once per
// runtime build, long before this request existed. store is p.entities,
// read per request. ctx is r.Context(): every [EntityStore] method takes
// ctx first, so a cancelled request stops its own database reads — a
// closure built with context.Background() would drop the deadline silently
// on a plane that is unauthenticated by design (D4).
//
// The closure MEMOISES: one [EntityStore.List] call per family PER
// REQUEST, never per gen.Body call — [Generator.Headers] is a second
// consumer of the same gen.Request and reaches Recipe.Value through the
// identical chain, so a memo scoped to one Body call would read twice for
// one response (D4). Without the memo, a ref bound at items[*].userId with
// listSize=200 would be 200 SELECTs for one anonymous request, and two
// fields of one body could straddle a concurrent write and disagree about
// what the family holds.
// base is the SERVING request's own base scope (D10, P3h) — computed once
// in serveRoute and threaded through serveGenerated exactly like the ref
// resolver's other request-scoped inputs. D10's argument does not extend to
// the ROUTE scope, which stays empty below: a base scope is workspace-wide
// (D3.2.2), the same value that scopes the served family also scopes the
// referenced one, so leaving it on the empty scope here (as P3c's route
// scope stays) would make ref resolve nothing at all under any workspace
// whose basePath carries a parameter.
func (p *Plane) newRefResolver(ctx context.Context, roster map[string]*resources.Resource, store EntityStore, ws *workspaces.Workspace, r *http.Request, base resources.ScopeKey) recipes.Ref {
	memo := map[string]refOutcome{} // family -> its List outcome (rows or error), read at most once (D4)

	return func(q recipes.RefQuery) (any, bool) {
		v, ok := p.resolveRef(ctx, roster, store, ws, memo, base, q)
		// D7: the mark is set by the resolver, because it is the only
		// thing that can — refValue knows the policy but not request
		// state. "generate" is the only policy that marks: a "set-null"
		// decline did exactly what the operator asked (it emits JSON
		// null, visible in the body already), so marking it too would be
		// noise on a response nothing is actually wrong with.
		if !ok && q.Policy == "generate" {
			markRefUnresolved(r)
		}
		return v, ok
	}
}

// refOutcome is the memoized result of one EntityStore.List call for one
// family: either its rows, or the error that call returned. Caching the
// error alongside the rows (rather than caching only on success) is what
// makes the memo actually bound store reads to one PER FAMILY PER REQUEST —
// a memo that only remembered success would re-issue store.List on every
// later occurrence of a family that failed or that raced a decline
// (resources.ErrResourceGone), which is exactly the "200 SELECTs for one
// anonymous request" amplification D4's memo exists to prevent, and worst
// precisely while the store is struggling or racing.
type refOutcome struct {
	rows []resources.Entity
	err  error
}

// resolveRef is D6's seven ordered steps. It never marks the traffic
// itself — newRefResolver's wrapper does that once, after this returns —
// so every "decline" here is a plain (nil, false), not a side effect.
func (p *Plane) resolveRef(
	ctx context.Context,
	roster map[string]*resources.Resource,
	store EntityStore,
	ws *workspaces.Workspace,
	memo map[string]refOutcome,
	base resources.ScopeKey,
	q recipes.RefQuery,
) (any, bool) {
	// Step 1: no entity store wired at all. p.entities is nil until
	// SetEntities is called, a documented supported state
	// (plane.go:88-93#SetEntities) resourceBranch already falls through on
	// — the closure inherits that guard rather than inventing a second
	// answer; without it the first ref served by such a Plane is a
	// nil-interface call and a panic.
	if store == nil {
		return nil, false
	}

	// Step 2: the family must be confirmed in THIS workspace, checked by a
	// direct map hit on the verbatim key roster already carries (D3). This
	// is what keeps a ref inside its own workspace: EntityStore.List is not
	// workspace-scoped, so resolving q.Family to a resource id through
	// anything OTHER than this request's own roster would be one forgotten
	// check away from serving another workspace's rows on the
	// unauthenticated mock plane.
	res, ok := roster[q.Family]
	if !ok {
		return nil, false
	}

	// Step 3: read the family's rows once per request, through the memo —
	// caching the OUTCOME (rows or error), not just a success, so a later
	// occurrence of the same family reuses the first attempt's error too
	// instead of re-issuing store.List (see refOutcome).
	outcome, cached := memo[q.Family]
	if !cached {
		// The ROUTE scope stays the empty scope, always — not the serving
		// request's own route scope (P3e D11, unmoved by P3h). q.Family is
		// a DIFFERENT family from the one being served, so this request's
		// route scope means nothing there; a nested family holds no rows
		// under "" and the resolver falls through to the recipe's own
		// declared policy (generate/set-null) exactly as it does for a
		// family with no rows at all — this is not a new CONDITION,
		// EntityStore.List simply needs a value now that it takes a route
		// scope, and resolveRef has none to give (D11: the family being
		// served, if any, is not this one, so there is no
		// *resources.Resource here to compute one from).
		//
		// The BASE scope, by contrast, IS the serving request's own (P3h
		// D10): unlike the route scope, a base scope is workspace-wide —
		// the same value that scopes the served family also scopes the
		// referenced one, so passing the empty base here (as the route
		// scope stays) would make ref resolve NOTHING under any workspace
		// whose basePath carries a parameter.
		rows, err := store.List(ctx, res.ID, base, resources.ScopeKey(""))
		if err != nil {
			// A missing resources row (declined between the roster's
			// caching and this read — an ordinary race on a cache keyed by
			// (workspace_id, revision)) is NOT logged: it is not a
			// failure, and logging it would put anonymous request traffic
			// into the log for a condition D4 calls ordinary. Any other
			// store error is logged — it is the one thing this closure
			// cannot mark for the operator any other way. Logged once per
			// request, on this first attempt, never again for a family
			// this memo has already resolved (successfully or not).
			if !errors.Is(err, resources.ErrResourceGone) {
				p.log.Warn("resolve ref", "workspace", ws.Slug, "routeFamily", q.Family, "err", err)
			}
		}
		outcome = refOutcome{rows: rows, err: err}
		memo[q.Family] = outcome
	}
	if outcome.err != nil {
		return nil, false
	}
	rows := outcome.rows
	if len(rows) == 0 {
		return nil, false
	}

	// Step 4: pick one row from q.Seed — the same per-field seed layer
	// every other recipe value is drawn from (SeedScalar), so a generated
	// list of eight orders references eight different users, not one user
	// eight times, and the same field on the same request always lands on
	// the same row.
	entity := rows[q.Seed%uint64(len(rows))] //nolint:gosec // modulo by a nonzero len, no overflow reachable

	// Step 5: decode EXACTLY the one row the pick landed on — Entity.Data
	// is raw bytes, never round-tripped through a second decode on the
	// read path (repo.go:187-190#round-tripped); indexing the whole family
	// to avoid a second decode elsewhere would turn one bound ref into up
	// to 200 jsonx.Unmarshal calls for a single anonymous request.
	var decoded map[string]any
	if err := jsonx.Unmarshal(entity.Data, &decoded); err != nil {
		p.log.Warn("decode entity for ref", "workspace", ws.Slug, "routeFamily", q.Family, "err", err)
		return nil, false
	}
	raw, ok := decoded[q.Property]
	if !ok {
		return nil, false
	}

	// Step 6: refuse a value that is not a JSON scalar (D10), then coerce
	// it to the target node's declared type/format.
	switch raw.(type) {
	case map[string]any, []any:
		return nil, false
	}
	return recipes.Coerce(raw, q.Type, q.Format)
}
