// scenario.go is DESIGN §4's Scenario layer as the mock plane sees it: the
// PURE function that composes an activated scenario's snapshot over the
// workspace's own layer ([composeScenarioLayer]), the [ScenarioSource] seam
// buildRuntime reads that snapshot through, and the conversion from a
// bundle entry back into the [overrides.Row] the serving path already knows
// how to read.
//
// A scenario is composed at REQUEST-RUNTIME, never restored over the
// workspace's rows. DESIGN §4 line 103 calls it «активный именованный набор
// ПОВЕРХ настроек воркспейса», and that word is the whole design:
// activation writes exactly one column (workspaces.scenario_id) plus a
// revision bump, and everything the switch actually MEANS happens here, in
// the build, where this package already composes Spec -> Workspace ->
// Session. Nothing in this file writes to a workspace's stored rows, and
// nothing may start to: an implementation that restored a snapshot over
// op_overrides would pass "deactivate and the old answer returns" while
// having destroyed the operator's own edits on the way in.
//
// Custom endpoints are NOT part of a scenario and never will be, regardless
// of bundle version (§0): runtime.custom is keyed by a custom_endpoints row
// id, and a row living inside a BLOB has no id to be keyed by. That is no
// longer bundle.Validate's rule to enforce, though — P2c's gate (C2)
// reused this same v3 format for checkpoints, which DO legitimately carry
// endpoints, so bundle.Validate now only shape-checks a non-empty Endpoints
// array instead of refusing one outright. The "a SCENARIO specifically
// never has one" guarantee this file's composition relies on instead comes
// from upstream, one layer before the snapshot ever reaches here:
// internal/scenarios' scanScenario (the only place a *Scenario is ever
// built) refuses to decode a stored row whose Endpoints is non-empty, so by
// the time scenarios.Repo's Get/ByName hand this package a Scenario, its
// Bundle.Endpoints is already guaranteed empty — there is still nothing
// here to compose even defensively, just one door further upstream than it
// used to be.
package mockplane

import (
	"context"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/scenarios"
	"github.com/yashok111/mocker/internal/workspaces"
)

// ScenarioSource is *scenarios.Repo as the mock plane needs it. Unlike
// [OverrideSource], [CustomSource] and [SpecSource] — each a single read
// method — this one carries a WRITE, and that is deliberate rather than an
// interface that grew: [LiveStateSource] is the precedent (Apply/Set/List/
// Clear, mutators included), because the plane's own `{prefix}/state`
// endpoint has always been allowed to change the state it also reads. What
// is genuinely new here is only WHERE the write lands: LiveStateSource
// mutates RAM, this one writes the workspaces table.
//
// The three methods are the three things the plane does with a scenario,
// and no more:
//
//   - Get — by id, for [Plane.buildRuntime]: ws.ScenarioID names it, and
//     the build needs the whole decoded snapshot to compose over the
//     workspace's own layer.
//   - ByName — for the `{prefix}/state {"scenario":"<name>"}` switch: that
//     directive names a scenario, never an id (an id is a database detail a
//     test script has no way to know), so a name has to be resolved before
//     SetActive can be called.
//   - SetActive — the ONE activation write, shared verbatim with the admin
//     plane's own activate/deactivate routes. A7's idempotence and A8's
//     ownership check live inside it exactly so they cannot exist in two
//     versions that drift; this plane must never grow its own "is it
//     already active" check beside it.
type ScenarioSource interface {
	Get(ctx context.Context, workspaceID, scenarioID int64) (*scenarios.Scenario, error)
	ByName(ctx context.Context, workspaceID int64, name string) (*scenarios.Scenario, error)
	SetActive(ctx context.Context, workspaceID int64, scenarioID *int64) (revision int64, err error)
}

// scenarioSnapshot returns the decoded snapshot of ws's ACTIVE scenario,
// and whether there is one at all. ok is false when no [ScenarioSource] was
// wired ([Plane.SetScenarios] never called — the same "no source wired, no
// change" contract every other source here follows), when the workspace has
// no active scenario, and — deliberately — when the active one cannot be
// loaded or decoded.
//
// That last case is a decision: a stored snapshot that fails to load must
// NOT take the workspace's whole mock down. It is logged and the build
// carries on with the workspace layer alone, in the spirit of what
// buildRecipeSets (overrides.go) already does for an invalid stored recipe
// — a stored BLOB is data this server wrote but cannot re-verify on the
// unauthenticated path, and a workspace whose every request 500s because
// one row in `scenarios` went bad is strictly worse than one serving its
// own, unmasked layer while an operator works out why the switch stopped
// taking effect. The 500 alternative was considered and rejected for
// exactly that reason: the mock plane's job is to keep answering.
func (p *Plane) scenarioSnapshot(ctx context.Context, ws *workspaces.Workspace) (bundle.Bundle, bool) {
	if p.scenarios == nil || ws.ScenarioID == nil {
		return bundle.Bundle{}, false
	}
	sc, err := p.scenarios.Get(ctx, ws.ID, *ws.ScenarioID)
	if err != nil {
		p.log.Error("load active scenario: serving the workspace layer instead",
			"workspace", ws.Slug, "scenario", *ws.ScenarioID, "err", err)
		return bundle.Bundle{}, false
	}
	return sc.Bundle, true
}

// composeScenarioLayer overlays an activated scenario's snapshot onto the
// workspace's own layer and returns exactly what the runtime must then be
// built from: the composed settings and the composed override map.
//
// It is a PURE function — no database, no HTTP, no clock, not even the
// logger — and that is a decision, not an accident (A5). buildRuntime is
// already the third-longest function in this package and carries no gocyclo
// suppression while the two longer ones do; golangci-lint is held at zero
// here, and a fourth layer's branching inlined into that function is how it
// acquires the suppression. But the stronger reason is that the whole
// slice's correctness lives in this one function: a layer whose only virtue
// is that it is testable in isolation must not be reachable only through a
// runtime build that needs a spec document, a fake SpecSource and a cache
// key just to observe which of two rows won.
//
// # MEMBERSHIP decides the layer, the FLAG decides the effect (A1, A2)
//
// The composed map is built BY KEY: a key present in the snapshot takes the
// SCENARIO's row whatever its flags say, and a key absent from the snapshot
// keeps the WORKSPACE's row. Membership in snap.Overrides is the only
// signal — there is no "was this key touched" marker in the format, by
// design (bundle.Bundle's own field comment).
//
// This works because a row with OverrideOn=false STAYS in the map:
// respond.go computes `overrideActive := hasRow && row.OverrideOn` and
// gates every branch below it on overrideActive, never on hasRow. So a
// scenario row with OverrideOn=false is a third state neither layer can
// express alone — "under this scenario that operation answers as the SPEC
// says", masking whatever edit the workspace has for it. Collapse
// membership into the flag (skip OverrideOn=false entries on the way in,
// say) and a scenario can no longer mask a workspace edit at all, which is
// precisely what makes DESIGN §12's «пустой список» and «всё падает»
// scenarios expressible over a workspace that has its own edits.
//
// The key is overrides.OpKey(method, path), computed from the entry's own
// method/path exactly as [OverrideSource.ForWorkspace] already keyed the
// workspace's map (overrides.go's lookupOverride derives the same key from
// route.Path, and NEVER route.CanonicalPath — see its doc comment for why
// the distinction is load-bearing). Nothing here rekeys the workspace's
// half; it is copied under the keys it arrived with.
//
// # Which settings compose (A3, A3a)
//
// A setting is scenario-effective if and only if it is read from INSIDE the
// runtime, plus one deliberate exception (now two, joined by P3h — D4.5).
// The returned domain.Settings therefore starts as the snapshot's and has
// exactly four fields put back from the workspace:
//
//   - BasePath — the deliberate exception. It IS read inside the build, so
//     this function could supply it; it must not. A scenario snapshotted
//     at another prefix would silently relocate every route, and DESIGN
//     §12:530-531 justifies scenarios over forks precisely because «форк …
//     меняет URL, а URL прошит в конфиге фронта». A scenario that moved
//     the URL would be a fork with worse ergonomics.
//   - BasePathValues (P3h D4.5) — joins BasePath for the amplified version
//     of the same reason: it is never read inside the build either (the
//     declared set is checked per request, in resourceBranch, off the
//     workspace's own settings — see internal/mockplane/resource.go), but
//     if a scenario's own captured set won here, activating it would not
//     relocate routes, it would make every row under a value the scenario
//     does not declare unreachable — the one thing the Scenario layer is
//     built never to do to stored state.
//   - CORS — structurally impossible: plane.go reads ws.Settings.CORS per
//     request, and the preflight path short-circuits before any runtime is
//     built at all. A scenario value here could not take effect.
//   - NotFoundBody — structurally impossible for the same reason:
//     routes.go's serveNoRoute reads it from ws.Settings directly.
//
// Everything else the runtime reads is replaced wholesale — Seed, ListSize,
// NullRate, Identity and Auth (they fill gen.Options), DelayMs and Envelope
// (rt.settings, read by respond.go and custom.go). ValidateRequests is
// carried from the snapshot with no special case: it has no server consumer
// anywhere in this tree, so which layer it came from is unobservable, and
// inventing a rule for it would be inventing a rule nothing can check.
//
// The three restored fields are not a nicety. rt.settings is documented as
// "the settings snapshot the runtime was built from"; if the scenario's
// basePath/cors/notFoundBody landed there, that sentence would be false for
// three fields, the route table (built from ws.Settings.BasePath) would
// disagree with the settings sitting beside it, and the next reader to
// reach for the obvious one would silently get the wrong value.
//
// Starting from the SNAPSHOT and restoring three fields — rather than
// starting from the workspace and copying the seven effective ones over —
// is the deliberate direction. DESIGN §12 has a scenario replace settings
// wholesale, so a field added to domain.Settings later and read inside the
// runtime becomes scenario-effective by default, which is right; the
// opposite direction would leave such a field silently pinned to the
// workspace's value with no bar going red. The cost is that a NEW setting
// read only OUTSIDE the runtime has to be added to the restore list here,
// and that is a visible, greppable omission rather than an invisible one.
//
// Neither input is mutated: the workspace's map is copied into a fresh one
// and the returned settings is a value copy. buildRuntime hands this
// function the exact map [OverrideSource.ForWorkspace] returned, which a
// caching source is free to keep reusing, and the runtime it builds is
// immutable and shared unlocked across concurrent requests — a composition
// that wrote into either input would be a data race and a corrupted cache
// at once.
func composeScenarioLayer(
	wsSettings domain.Settings,
	wsRows map[string]*overrides.Row,
	snap bundle.Bundle,
) (domain.Settings, map[string]*overrides.Row) {
	settings := snap.Workspace.Settings
	settings.BasePath = wsSettings.BasePath
	settings.BasePathValues = wsSettings.BasePathValues // P3h D4.5: joins basePath as the fourth restored field
	settings.CORS = wsSettings.CORS
	settings.NotFoundBody = wsSettings.NotFoundBody

	// Deliberately NOT settings.Normalize(): the snapshot was normalized on
	// the way in (scenarios' create path parses through domain.ParseSettings
	// before encoding) and ws.Settings was normalized when the workspace row
	// was read, so there is nothing left to clamp — while Normalize would
	// MINT A FRESH RANDOM SIGNING KEY for an empty one, on every runtime
	// build, handing out tokens that change identity at every cache miss.

	rows := make(map[string]*overrides.Row, len(wsRows)+len(snap.Overrides))
	for key, row := range wsRows {
		rows[key] = row
	}
	for i := range snap.Overrides {
		e := snap.Overrides[i]
		rows[overrides.OpKey(e.Method, e.Path)] = rowFromEntry(e)
	}
	return settings, rows
}

// rowFromEntry converts one bundle entry back into the [overrides.Row] the
// serving path reads. It is the inverse of bundle.NewOverrideEntry, and
// lives here rather than in internal/bundle because it is this package's
// need: bundle is a format, and the format deliberately does not carry the
// four DB-only columns a Row has (ID, WorkspaceID, OperationID, UpdatedAt).
//
// Those four stay at their zero values, and that is safe rather than
// merely tolerable: nothing in this package's serving path reads any of
// them for a spec operation. respond.go and overrides.go together touch
// only OverrideOn, RouteOff, ActiveStatus, Responses, ListSize, DelayMs and
// (for messages) Method/Path. The one place a row id IS load-bearing —
// router.Route.CustomRowID — belongs to custom_endpoints, which a scenario
// cannot carry at all (§0). FailDirective and ValidateReq are copied even
// though this plane does not interpret either: they are preserved-only
// columns, and a conversion that quietly dropped them would make a
// scenario's rows differ from the workspace's rows in a field somebody
// eventually starts reading.
//
// Responses (and the maps nested inside it) is shared, not deep-copied: it
// comes from a bundle decoded fresh for this build out of stored bytes, so
// there is no other holder to alias, and the runtime it lands in is
// immutable once published.
func rowFromEntry(e bundle.OverrideEntry) *overrides.Row {
	return &overrides.Row{
		Method:        e.Method,
		Path:          e.Path,
		OverrideOn:    e.OverrideOn,
		RouteOff:      e.RouteOff,
		ActiveStatus:  e.ActiveStatus,
		Responses:     e.Responses,
		ListSize:      e.ListSize,
		DelayMs:       e.DelayMs,
		FailDirective: e.FailDirective,
		ValidateReq:   e.ValidateReq,
	}
}
