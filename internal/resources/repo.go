// Package resources owns three of the four P3a tables that have existed
// since P0's migration (DESIGN §11, decisions.md — mocker-p3a-resources):
// resources, entities and resource_decisions. resource_suggestions stays
// internal/specs' table (this package only READS it, through
// [specs.Repo.EnsureSuggestions]) — a route family is DERIVED once, at
// import time, and CONFIRMED per workspace here, and the two verbs live in
// separate packages for the same reason [internal/specs]' own doc comment
// gives for keeping derivation out of this package: a second, driftable
// copy of deriveSuggestions would need to import internal/specs while
// Import calls back into internal/resources, which is an import cycle.
//
// The whole package is two verbs and a four-method store:
//
//   - [Repo.Confirm] / [Repo.Decline] — the resource_decisions lifecycle
//     (D4): one transaction per transition, the generation half run
//     OUTSIDE it and fenced against staleness by re-reading the workspace's
//     identity and every input generation used (D4's R36).
//   - [Repo.List] / [Repo.Get] / [Repo.Create] / [Repo.Delete] — the
//     four-method entity store the mock plane (internal/mockplane) holds
//     as an interface (this package never imports mockplane — the
//     dependency runs the other way).
//
// Nothing about a confirmed resource is editable (D4): there is no PUT and
// no repopulate. A resource that is wrong is declined and confirmed again.
package resources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
)

// SpecSource is the read-only slice of *specs.Repo this package needs —
// named as an interface (rather than importing the concrete type at every
// call site) purely so [main_test.go]'s goleak harness and a future fake
// have somewhere to attach; internal/mockplane's runtime builder names an
// identical seam for the same reason. cmd/mocker/main.go always wires the
// real *specs.Repo.
type SpecSource interface {
	Normalized(ctx context.Context, specID int64) ([]byte, error)
	Variants(ctx context.Context, specID int64) (map[int64][]gen.ResponseVariant, error)
	Routes(ctx context.Context, specID int64) ([]router.Route, error)
	EnsureSuggestions(ctx context.Context, specID int64) ([]*specs.ResourceSuggestion, error)
}

// Repo is resources/entities/resource_decisions' data-access layer.
type Repo struct {
	db    *store.DB
	specs SpecSource

	// maxResponseBytes is cfg.MaxResponse — process-level configuration
	// read once at startup, deliberately NOT part of the confirm fence
	// (D4/R36: "it cannot change between the generation and the
	// transaction, and fencing it would cost a capture and a comparison
	// for a value that never moves"). It also sets the per-entity byte
	// cap's other half, entityByteCap = maxResponseBytes/2 (R25).
	maxResponseBytes int64
	// trafficMaxBody is cfg.TrafficMaxBody, MOCKER_TRAFFIC_MAX_BODY — the
	// same number [internal/mockplane]'s own request-body capture caps at,
	// reused here (R25: "one entity at most the capture cap") so a body
	// large enough to store and a body large enough to refuse can never be
	// two different sizes.
	trafficMaxBody int64
	// maxEntityRows is D11's own knob: MOCKER_MAX_ENTITIES, wired through
	// [NewRepo] rather than the package-level constant this field replaces.
	// The grain does NOT move (still per resource row, across every
	// scope/base-scope of a family together) — only the source of the
	// number does, and the number itself drops from the 10000 the
	// documents advertised (and nothing enforced) to the 1000 the code
	// actually enforced, so wiring this field does not silently raise the
	// ceiling tenfold on an upgraded installation (D11).
	maxEntityRows int64
}

// NewRepo builds a Repo over db, reading specs through source and enforcing
// the entity caps against maxResponseBytes/trafficMaxBody/maxEntityRows —
// the three config.Config fields the caps are derived from (cfg.MaxResponse,
// cfg.TrafficMaxBody, cfg.MaxEntities). Plain int64 parameters rather than
// *config.Config: this package needs exactly three numbers out of it, and a
// fake config in a test is more ceremony than three ints.
func NewRepo(db *store.DB, source SpecSource, maxResponseBytes, trafficMaxBody, maxEntityRows int64) *Repo {
	if maxEntityRows < 0 {
		// config.Load's own count() validator (internal/config/config.go)
		// accepts n >= 0, so MOCKER_MAX_ENTITIES=0 is a legitimate operator
		// choice ("confirm nothing, ever") that reaches this constructor as
		// int64(cfg.MaxEntities) == 0 — and it must land in r.maxEntityRows
		// as zero, not get silently coerced into the pre-P3h constant.
		// Only a NEGATIVE value falls back, because Load's validator never
		// produces one: this branch exists for a caller that built
		// *config.Config as a struct literal rather than through Load and
		// passed a genuinely negative number, not for the zero every such
		// fixture's own zero-valued MaxEntities field also produces. Zero
		// and "never set" are indistinguishable at this boundary by
		// construction — D11 resolves that ambiguity in the operator's
		// favor (zero means zero), which means a caller that wants the old
		// advertised default has to say so explicitly, the same way
		// [newTestRepo] (repo_test.go) already does by always passing
		// [defaultMaxEntityRows] rather than relying on this fallback.
		maxEntityRows = defaultMaxEntityRows
	}
	return &Repo{db: db, specs: source, maxResponseBytes: maxResponseBytes, trafficMaxBody: trafficMaxBody, maxEntityRows: maxEntityRows}
}

// --- errors --------------------------------------------------------------

// ErrWorkspaceNotFound mirrors internal/checkpoints' identical sentinel:
// the workspace named by a call's workspaceID does not exist.
var ErrWorkspaceNotFound = errors.New("resources: workspace not found")

// ErrUnknownFamily is D4's 404 unknown_family: neither a resource_suggestions
// row of the workspace's current spec NOR a resources row names routeFamily.
var ErrUnknownFamily = errors.New("resources: unknown route family")

// ErrAlreadyConfirmed is D4's 409 already_confirmed: routeFamily already has
// a resources row for this workspace, including one the CURRENT spec no
// longer suggests (D9.4 — an orphaned resource is still declinable, never
// re-confirmable under this error).
var ErrAlreadyConfirmed = errors.New("resources: route family already confirmed")

// ErrStaleConfig is D4/R36's 409 stale_config: the workspace's identity
// (created_at, slug) or one of the inputs generation read (spec_id,
// scenario_id, settings.seed, the effective settings.listSize) moved
// between the pre-transaction read and the write transaction.
var ErrStaleConfig = errors.New("resources: workspace config changed before commit")

// ErrPopulationFailed is D5's 409 population_failed: gen.Body returned an
// error, or the body it returned did not decode into a JSON object. Wraps
// the underlying cause and names the family so a caller can report which
// operation failed without re-deriving it.
var ErrPopulationFailed = errors.New("resources: population failed")

// ErrEntityLimit is R25's 409 entity_limit: the row cap (1000), the total
// byte cap (MaxResponse/2) or the per-entity byte cap
// (max(TrafficMaxBody, 64KiB)) was exceeded. Returned by both Confirm and
// Create, on the SAME three caps (D13 clause 17).
var ErrEntityLimit = errors.New("resources: entity storage limit exceeded")

// ErrConfirmSlugRequired is D4/R10's refusal: declining a CONFIRMED
// resource without a confirmSlug.
var ErrConfirmSlugRequired = errors.New("resources: confirm slug required")

// ErrConfirmSlugMismatch is D4/R10's refusal: declining a CONFIRMED
// resource with a confirmSlug that does not match the workspace's own slug.
var ErrConfirmSlugMismatch = errors.New("resources: confirm slug mismatch")

// ErrResourceGone is returned by Create/Delete when resourceID no longer
// names a row — the family was declined out from under an in-flight
// request (D13 clause 34: "a vanished resource falls through", never a
// foreign-key error).
var ErrResourceGone = errors.New("resources: resource no longer exists")

// ErrWriteBusy is R34's 409 write_busy: Create or Delete could not obtain
// the single writer connection within writeDeadline. Claimed ONLY when
// THIS package's own bound is the cause — see [Repo.Create]'s doc comment.
var ErrWriteBusy = errors.New("resources: write timed out")

// ErrParentNotConfirmed is D5.1's 409 parent_not_confirmed: confirming a
// nested family (router.ParentFamily(routeFamily) != "") whose parent has
// no CONFIRMED resources row in this workspace. D5.2 states the invariant
// this buys: a confirmed nested family always has a confirmed parent, and
// this sentinel is the half of it Confirm enforces (D7's ErrChildConfirmed
// is the other half, on Decline).
var ErrParentNotConfirmed = errors.New("resources: parent family is not confirmed")

// ErrChildConfirmed is D7.1's 409 child_confirmed: declining a family that
// is router.ParentFamily of some OTHER confirmed family in this workspace.
// A cascade through resources.parent_id would blow past the confirmSlug the
// operator actually typed (D7.2) — this sentinel is what keeps the decline
// scoped to exactly the family named, same as it always was.
var ErrChildConfirmed = errors.New("resources: a child family is still confirmed")

// --- shared types ----------------------------------------------------------

// Resource is one resources row: a confirmed, populated route family.
// Nothing here is editable after Confirm writes it (D4) — a wrong resource
// is declined and confirmed again, never patched.
type Resource struct {
	ID          int64
	WorkspaceID int64
	RouteFamily string
	Name        string
	IDField     string
	IDStrategy  string // always "seq" (R15) — no other strategy exists yet
	ParentID    *int64
	// ScopeParams is the ordered outer path-parameter NAMES of a nested
	// family (D3.1/D5.6), written once at confirm from the detail route's
	// own templated Path — []string{} for a top-level family, which is
	// every row before P3e and most rows since. Serving never resolves a
	// scope VALUE through this column (D5.6) — it is read only for its
	// LENGTH, cross-checked against the matched route's own outer count.
	ScopeParams  []string
	EntitySchema string
	// Wrapper reuses [specs.Wrapper]: the exact four-key shape
	// resource_suggestions.wrapper already carries, copied verbatim at
	// Confirm rather than re-declared as a second, driftable type.
	Wrapper   specs.Wrapper
	FilterMap map[string]any // always {} this slice (D5) — persisted, never read
	// WriteForm is nil for a read-only resource (R12): POST keeps
	// answering from the generator. "bare" means POST is taken over.
	WriteForm *string
	Seq       int64
	SeedCount int64
}

// --- ForWorkspace (mockplane.ResourceSource) ------------------------------

const selectResource = `
	SELECT id, workspace_id, route_family, name, id_field, id_strategy, parent_id,
	       scope_params, entity_schema, wrapper, filter_map, write_form, seq, seed_count
	FROM resources`

// scanResource scans one resources row, decoding its three JSON columns
// (scope_params, wrapper, filter_map) through internal/jsonx — the same
// backend-swap seam every production decode in this tree goes through
// (internal/jsonx/boundary_test.go's AST walk forbids a direct
// encoding/json import here).
func scanResource(row interface{ Scan(dest ...any) error }) (*Resource, error) {
	var (
		res                         Resource
		parentID                    sql.NullInt64
		scopeParamsJSON, filterJSON string
		wrapperJSON, writeForm      sql.NullString
	)
	if err := row.Scan(
		&res.ID, &res.WorkspaceID, &res.RouteFamily, &res.Name, &res.IDField, &res.IDStrategy,
		&parentID, &scopeParamsJSON, &res.EntitySchema, &wrapperJSON, &filterJSON, &writeForm,
		&res.Seq, &res.SeedCount,
	); err != nil {
		return nil, err
	}
	if parentID.Valid {
		v := parentID.Int64
		res.ParentID = &v
	}
	if writeForm.Valid {
		v := writeForm.String
		res.WriteForm = &v
	}

	if err := jsonx.Unmarshal([]byte(scopeParamsJSON), &res.ScopeParams); err != nil {
		return nil, fmt.Errorf("decode scope_params for resource %d: %w", res.ID, err)
	}
	// wrapper has no NOT NULL constraint (0001_init.sql); Confirm always
	// writes a marshalled specs.Wrapper (D4's Materialization: "copies ...
	// wrapper from the suggestion"), so NULL is not a case Confirm itself
	// produces — decoding only when present just avoids a panic on a row
	// this package did not write.
	if wrapperJSON.Valid {
		if err := jsonx.Unmarshal([]byte(wrapperJSON.String), &res.Wrapper); err != nil {
			return nil, fmt.Errorf("decode wrapper for resource %d: %w", res.ID, err)
		}
	}
	if err := jsonx.Unmarshal([]byte(filterJSON), &res.FilterMap); err != nil {
		return nil, fmt.Errorf("decode filter_map for resource %d: %w", res.ID, err)
	}
	return &res, nil
}

// ForWorkspace returns every CONFIRMED resource of workspaceID, in one
// query — the shape [internal/overrides.Repo.ForWorkspace] and
// [internal/customep.Repo.ForWorkspace] already give, and the one method
// [mockplane.ResourceSource] declares against this package (D6 R17):
// buildRuntime (internal/mockplane/runtime.go) calls this once per
// (workspace, revision) cache build and keys the result by RouteFamily
// itself — this method does no keying of its own, only the row scan.
func (r *Repo) ForWorkspace(ctx context.Context, workspaceID int64) ([]*Resource, error) {
	rows, err := r.db.R.QueryContext(ctx, selectResource+" WHERE workspace_id = ?", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list resources for workspace %d: %w", workspaceID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []*Resource
	for rows.Next() {
		res, serr := scanResource(rows)
		if serr != nil {
			return nil, fmt.Errorf("resources for workspace %d: %w", workspaceID, serr)
		}
		out = append(out, res)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resources for workspace %d: %w", workspaceID, err)
	}
	return out, nil
}

// --- caps (R25, D13 clauses 17/28/32/33/36; D11 for the row cap's source) --

// defaultMaxEntityRows is the row cap this package enforced as a hard
// constant from P3a through P3g — 1000 per resource, across every scope.
// D11 makes the CAP configurable (Repo.maxEntityRows, sourced from
// config.Config.MaxEntities), but this number stays as [NewRepo]'s own
// fallback for a caller that passes <= 0 (D11's own new config default is
// this same 1000, so any real deployment through config.Load never
// triggers the fallback at all — it exists for a hand-built
// *config.Config literal, the shape every test fixture across this tree
// that predates this field uses). [newTestRepo] (repo_test.go) reaches it
// through this same constant rather than redeclaring a second one that
// could drift from it.
const defaultMaxEntityRows = 1000

// minEntityBodyCap mirrors internal/mockplane/reqbody.go's
// minRequestBodyCap verbatim — R25's own text: "one entity at most the
// capture cap, max(MOCKER_TRAFFIC_MAX_BODY, 64 KiB) — deliberately the
// same number as the request-body capture, so a body large enough to store
// and a body large enough to refuse can never be two different sizes."
// This package cannot import internal/mockplane (the dependency runs the
// other way: mockplane will hold this package's store behind an
// interface), so the formula is duplicated here rather than shared — the
// CONSTANT is what must agree, not the source location.
const minEntityBodyCap = 64 << 10

// perEntityByteCap is the per-entity cap: max(trafficMaxBody, 64KiB).
func (r *Repo) perEntityByteCap() int64 {
	if r.trafficMaxBody > minEntityBodyCap {
		return r.trafficMaxBody
	}
	return minEntityBodyCap
}

// entityByteCap is the resource-wide stored-byte cap: MaxResponse/2 (R25).
func (r *Repo) entityByteCap() int64 {
	return r.maxResponseBytes / 2
}

// checkBatchCaps applies all three caps (D13 clause 17) to a whole
// generated population BEFORE Confirm opens its write transaction, so a
// confirm that would exceed any of them writes nothing at all rather than
// committing a partial resource.
func (r *Repo) checkBatchCaps(bodies [][]byte) error {
	if int64(len(bodies)) > r.maxEntityRows {
		return ErrEntityLimit
	}
	perCap := r.perEntityByteCap()
	totalCap := r.entityByteCap()
	var total int64
	for _, b := range bodies {
		if int64(len(b)) > perCap {
			return ErrEntityLimit
		}
		total += int64(len(b))
	}
	if total > totalCap {
		return ErrEntityLimit
	}
	return nil
}

// resourceByFamily looks up workspaceID's resources row for family, over q
// (the reader pool for [Repo.prepareConfirm]'s pre-transaction read, or the
// write transaction itself for [fenceParentTx]'s authoritative one) —
// [resourceExists] alone answers only whether a row exists, not which one.
func (r *Repo) resourceByFamily(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, workspaceID int64, family string) (*Resource, error) {
	res, err := scanResource(q.QueryRowContext(ctx, selectResource+" WHERE workspace_id = ? AND route_family = ?", workspaceID, family))
	switch {
	case err == nil:
		return res, nil
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	default:
		return nil, fmt.Errorf("read resource %q for workspace %d: %w", family, workspaceID, err)
	}
}

// OrphanedFamilies is the ONE implementation of "a confirmed family the
// bound spec's newest suggestion generation does not name"
// (mocker-p4a-triage decisions.md D5/R1) — every reader of that predicate
// (this package's own [Repo.prepareReset], [admin.Server.buildFamiliesView]
// and the GET .../drift handler) calls THIS function rather than
// recomputing the check against a suggestion set it fetched itself, so a
// `MAX(gen)` filter drifting out of sync between two independent copies is
// not a case that can arise.
//
// The signature is SET-WISE by design, not per-family: it reads specID's
// newest generation via [SpecSource.EnsureSuggestions] exactly ONCE and
// answers over the WHOLE families slice, so a caller with N confirmed
// families pays one round trip rather than N — the shape
// [Repo.prepareReset]'s own untouched loop already had, and the regression
// a per-family spelling would reintroduce despite satisfying every call-site
// count on its own (D5's own worked example: [admin.Server.buildFamiliesView]
// calling a per-family form once per confirmed row would turn a single
// EnsureSuggestions round trip into one per row).
//
// A nil specID (an unbound workspace, D4.3) answers an empty map WITHOUT
// touching the database — [SpecSource.EnsureSuggestions] is never called at
// all — so a caller resolving an unbound workspace's report reads nothing
// rather than running a query that would return nothing anyway (P27).
func (r *Repo) OrphanedFamilies(ctx context.Context, specID *int64, families []string) (map[string]bool, error) {
	if specID == nil {
		return map[string]bool{}, nil
	}
	suggestions, err := r.specs.EnsureSuggestions(ctx, *specID)
	if err != nil {
		return nil, fmt.Errorf("load suggestions for spec %d: %w", *specID, err)
	}
	return OrphanedIn(suggestions, families), nil
}

// OrphanedIn is THE implementation of D5's predicate — "a confirmed family the
// bound spec's newest suggestion generation does not name" — and it is pure:
// no context, no database, no error. It exists as a second door beside
// [Repo.OrphanedFamilies] because the two readers differ in exactly one way,
// whether they already hold the generation's suggestions. [Repo.OrphanedFamilies]
// does not and fetches; admin's buildFamiliesView does, because its primary loop
// emits one row per SUGGESTION and it fetched the list to write that loop.
//
// Before this split, buildFamiliesView called the fetching wrapper over a list
// it already had, so every request read resource_suggestions TWICE — and the
// second read was a LATER snapshot, so a rederive committing between them could
// make one family read as suggested by the primary loop and orphaned by the
// leftover loop inside a single response. The first launch of this slice shipped
// exactly that; the pure function removes the second read rather than the first.
func OrphanedIn(suggestions []*specs.ResourceSuggestion, families []string) map[string]bool {
	named := make(map[string]bool, len(suggestions))
	for _, s := range suggestions {
		named[s.RouteFamily] = true
	}
	orphaned := make(map[string]bool, len(families))
	for _, family := range families {
		if !named[family] {
			orphaned[family] = true
		}
	}
	return orphaned
}
