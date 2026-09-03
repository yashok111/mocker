// Scope addressing: a nested family's scope tuple, a parameterised basePath's
// declared values, and the ancestor walk both confirm and serving run over
// them. Split out of repo.go 2026-09-03; the text is unchanged.
package resources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/router"
)

// ScopeKey is entities.scope_key: the ordered outer path-parameter values
// of a nested family, each url.PathEscape'd and joined with "/". The empty
// tuple is "", which is what every row of a family with no outer parameter
// already carries — so no row in the database moves (P3e D3.3).
//
// A defined type, not string: EntityStore.Create otherwise takes three
// adjacent string parameters that swap silently and still compile.
type ScopeKey string

// EncodeScope builds a ScopeKey from values in scope_params order. It is the
// ONE owner of that encoding: confirm, reseed and every request produce a
// scope_key through this function and never through an inline join, because
// three sites joining for themselves are three chances for a read and a write
// to disagree about a string a UNIQUE index compares for equality (P3e D6.1).
//
// The escape is not decoration (D3.2): NormalizeSegments percent-decodes
// each path segment, so a request for /orgs/a%2Fb/users arrives with a
// scope value containing the delimiter this encoding joins on.
// url.PathEscape escapes both "/" and "%", making the join injective.
func EncodeScope(values []string) ScopeKey {
	escaped := make([]string, len(values))
	for i, v := range values {
		escaped[i] = url.PathEscape(v)
	}
	return ScopeKey(strings.Join(escaped, "/"))
}

// --- the base scope, D3/D4/D6 of mocker-p3h-basepath ----------------------

// ErrBaseScopeUndeclared is D6.4's 409 base_scope_undeclared: basePath
// carries a {param} and basePathValues is empty. This is an ABSOLUTE
// property of the current settings, never a before/after comparison — see
// [checkBaseScopeDeclared]'s own doc comment for why it has to be checked
// that way.
var ErrBaseScopeUndeclared = errors.New("resources: base path parameter has no declared values")

// checkBaseScopeDeclared is D6.4's own check, run from inside
// [fenceConfirmTx] against the settings that function ALREADY re-read fresh
// inside the write transaction — never against [confirmPrep]'s own
// pre-transaction snapshot. The condition is a property of the CURRENT
// value alone ("basePath carries a parameter and basePathValues is empty"),
// not a comparison against an earlier read: a workspace whose declared set
// has been empty since basePath's parameter was first added passes
// fenceConfirmTx's before/current EQUALITY check trivially (both ends
// agree — empty), so only an unconditional, freshly-read check catches it.
func checkBaseScopeDeclared(settings domain.Settings) error {
	_, names, valid := router.BaseParamIndexes(domain.NormalizeBasePath(settings.BasePath))
	if valid && len(names) > 0 && len(settings.BasePathValues) == 0 {
		return ErrBaseScopeUndeclared
	}
	return nil
}

// DeclaredBaseScopes returns the workspace's own declared base-scope SET,
// in DECLARED order (D4.1/D6.1): each raw basePathValues element split on
// "/" into its k components and encoded through [EncodeScope] — the SAME
// encoder every other scope tuple goes through (D3.1's own rule: "no second
// encoder"). When basePath declares no parameter at all, the declared set
// is the IMPLICIT singleton holding the one empty tuple (D3.3) — never the
// stored field itself, which domain.ValidateBasePathValues requires to be
// empty in that case — so a workspace with no base-path parameter
// populates and serves exactly one base scope, "", unchanged from every
// workspace before this slice. Returns an empty (non-nil-checked-for,
// just zero-length) slice when the path DOES carry a parameter and the
// declared list is empty — [checkBaseScopeDeclared] is what refuses that
// case; this function only ever reports what is declared.
//
// basePath/values must be the WORKSPACE's own raw settings (D4.5) — never
// [Repo.effectiveSettings]'s scenario-composed result, which does not
// restore either field from the workspace (that function's own doc
// comment says so): passing its output here would silently populate or
// serve whatever declared set the ACTIVE SCENARIO happened to capture,
// exactly the substitution D4.5/P20b exist to refuse.
func DeclaredBaseScopes(basePath string, values []string) []ScopeKey {
	_, names, valid := router.BaseParamIndexes(domain.NormalizeBasePath(basePath))
	if !valid || len(names) == 0 {
		return []ScopeKey{""}
	}
	scopes := make([]ScopeKey, 0, len(values))
	for _, v := range values {
		scopes = append(scopes, EncodeScope(strings.Split(v, "/")))
	}
	return scopes
}

// basePair is one (base, route) scope PAIR — D6.1's own unit of population:
// a confirm (or a reseed) produces one row set per pair, base outer loop,
// route scope inner loop, in the order the declared set is declared then
// the order the ancestor walk visits. A defined struct rather than two
// parallel slices threaded everywhere: [populatePairs] needs both values
// together, in order, to keep entity_key's ONE running counter (D6.2)
// correct across the whole double loop.
type basePair struct {
	Base  ScopeKey
	Scope ScopeKey
}

// chainPairs is D6.1's whole scope ARITHMETIC across the base axis: for
// each declared base value, in order, resolve chain's LIVE route scopes
// WITHIN that base (via [chainScopes]) and pair each one with its base.
// The walk stays INSIDE each base value in turn — never across all of them
// at once — because a nested family's ancestor rows are themselves per
// base scope (D6.1): the live keys of a parent under base "7" say nothing
// about the live keys under base "8". Shared by [Repo.prepareConfirm]
// (over the reader pool, before any transaction) and [Repo.fenceParentTx]
// (over the write transaction itself, authoritatively) — the same
// pre-transaction/in-transaction pairing [chainScopes] already keeps for
// the route axis alone.
func chainPairs(ctx context.Context, q rowQuerier, chain []*Resource, bases []ScopeKey) ([]basePair, error) {
	var pairs []basePair
	for _, base := range bases {
		scopes, err := chainScopes(ctx, q, chain, base)
		if err != nil {
			return nil, err
		}
		for _, s := range scopes {
			pairs = append(pairs, basePair{Base: base, Scope: s})
		}
	}
	return pairs, nil
}

// rowQuerier is D5.3's own read interface for the scope arithmetic's LIVE
// key source ([chainScopes]): satisfied by both the reader pool (r.db.R,
// [Repo.prepareConfirm]'s pre-transaction call) and the write transaction
// itself (*sql.Tx, [Repo.fenceParentTx]'s authoritative in-transaction
// one) — "a query interface both *sql.Tx and the reader pool satisfy," in
// the decision document's own words.
type rowQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// entityKeysScopedTx is [entityKeysTx] (P3e) widened with the scope
// parameter its SQL used to hard-code as ” — a nested family's parent is
// no longer always top-level once nesting reaches depth 2/3 (D3.1), so the
// scope has to be an argument rather than a literal. base joins it (P3h,
// D6.1): a parent's live keys are themselves per base scope, so a walk
// that ignored base would scope a child to a parent row that may not even
// exist in the child's own tenant. KEEPS ORDER BY id ASC: that is where
// the key order [chainScopes]/[extendScopes] and D5.5's family-wide
// counter both depend on comes from. q is [rowQuerier] rather than a
// concrete *sql.Tx so the SAME function serves chainScopes' pre-transaction
// call (over the reader pool) and its authoritative in-transaction one
// (over the write transaction) — D5.3's own "one query interface" rule,
// never two hand-copies that could drift.
func entityKeysScopedTx(ctx context.Context, q rowQuerier, resourceID int64, base, scope ScopeKey) ([]string, error) {
	rows, err := q.QueryContext(ctx,
		"SELECT entity_key FROM entities WHERE resource_id = ? AND base_scope_key = ? AND scope_key = ? ORDER BY id ASC",
		resourceID, string(base), string(scope))
	if err != nil {
		return nil, fmt.Errorf("read entity keys for resource %d base %q scope %q: %w", resourceID, base, scope, err)
	}
	defer func() { _ = rows.Close() }()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("scan entity key for resource %d: %w", resourceID, err)
		}
		keys = append(keys, k)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entity keys for resource %d: %w", resourceID, err)
	}
	return keys, nil
}

// extendScopes is D5.3/D8.3's scope ARITHMETIC and its ONE owner (D3.2's
// "no second encoder" rule, R2): each parent TUPLE extended by each of
// that tuple's own keys, in tuple order then key order. It touches no
// database at all, which is exactly what lets the two key sources — live
// rows ([chainScopes]) and prepared rows ([Repo.prepareGroupPopulation],
// D8.2/D8.3) — share it rather than each restating the arithmetic (D5.3
// rule 3, R4).
//
// parents are RAW tuples, never [ScopeKey], and that is forced rather than
// chosen: extending an ENCODED parent would need either a decode (D3.2
// declines to own one) or a hand-assembled concatenation of the encoded
// parent with an escaped key, which D3.2 bans by name. [EncodeScope] takes
// []string and nothing else, so raw tuples in and encoded scopes out is
// the only shape that obeys the document.
func extendScopes(parents [][]string, keysByParent [][]string) (tuples [][]string, scopes []ScopeKey) {
	for i, parent := range parents {
		for _, key := range keysByParent[i] {
			tuple := make([]string, len(parent)+1)
			copy(tuple, parent)
			tuple[len(parent)] = key
			tuples = append(tuples, tuple)
			scopes = append(scopes, EncodeScope(tuple))
		}
	}
	return tuples, scopes
}

// chainScopes is D5.3's LIVE key source: walks chain TOP DOWN (chain[0] is
// the depth-0 root, chain[len(chain)-1] is the family's own immediate
// parent — D3.3), reading each level's live entity keys through q, WITHIN
// base (P3h D6.1 — the walk never crosses base scopes), and handing them
// to [extendScopes] — never restating the arithmetic itself.
// [Repo.prepareConfirm]/[chainPairs] call this over the reader pool before
// any transaction opens; [Repo.fenceParentTx] calls it again over the
// transaction itself, authoritatively, once chain is re-resolved inside
// that same transaction ([Repo.ancestorChainTx]). An empty chain (a
// top-level family) answers the single implicit scope [""] without
// touching the database at all — D5.3's "a top-level family is the same
// code path with one scope," true regardless of base.
func chainScopes(ctx context.Context, q rowQuerier, chain []*Resource, base ScopeKey) ([]ScopeKey, error) {
	tuples := [][]string{{}}
	scopes := []ScopeKey{""}
	for _, res := range chain {
		keysByParent := make([][]string, len(tuples))
		for i, t := range tuples {
			keys, err := entityKeysScopedTx(ctx, q, res.ID, base, EncodeScope(t))
			if err != nil {
				return nil, err
			}
			keysByParent[i] = keys
		}
		tuples, scopes = extendScopes(tuples, keysByParent)
	}
	return scopes, nil
}

// rowScanQueryer is the read interface [Repo.resourceByFamily] and
// [Repo.ancestorChainTx] both need — QueryRowContext alone, satisfied by
// both the reader pool and the write transaction, so the same helper
// serves the pre-transaction read and the authoritative in-transaction one.
type rowScanQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// ancestorChainTx resolves routeFamily's whole ancestor CHAIN, TOP DOWN
// (D3.3: chain[0] the depth-0 root, chain[len(chain)-1] the immediate
// parent) — a DATA FETCH for [chainScopes] (D5.3), not a confirmation
// check. The immediate parent's confirmation is the single hop D5.1 checks
// explicitly (once outside the transaction in [Repo.prepareConfirm], once
// inside it in [Repo.fenceParentTx]), and D5.2's induction proof is what
// lets every ancestor ABOVE it be resolved here with no confirmation
// re-check of its own: "the two single-hop rules together hold the whole
// chain, at every depth, with no walk anywhere." A miss here can only mean
// that invariant is broken; wrapped as an ordinary error rather than
// reused as [ErrParentNotConfirmed], which names the specific, expected
// 409 the single-hop check already owns.
func (r *Repo) ancestorChainTx(ctx context.Context, q rowScanQueryer, workspaceID int64, routeFamily string) ([]*Resource, error) {
	var families []string
	for f := router.ParentFamily(routeFamily); f != ""; f = router.ParentFamily(f) {
		families = append(families, f)
	}
	slices.Reverse(families) // innermost-first -> root-first (D3.3)

	chain := make([]*Resource, 0, len(families))
	for _, f := range families {
		res, err := r.resourceByFamily(ctx, q, workspaceID, f)
		if err != nil {
			return nil, err
		}
		if res == nil {
			return nil, fmt.Errorf("ancestor family %q of %q has no confirmed resource, violating D5.2's chain invariant", f, routeFamily)
		}
		chain = append(chain, res)
	}
	return chain, nil
}

// outerParamNames returns the ordered names of every whole-segment
// {name} parameter in path EXCEPT the last (D5.6): for
// /orgs/{orgId}/users/{id} that is ["orgId"] — the last segment's own
// {id} is never an outer parameter, the identical premise
// [router.DetailIDParam] already reads positionally rather than by name.
// []string{} (never nil) for a top-level family, so a caller can marshal
// it straight to JSON "[]" without a nil check.
func outerParamNames(path string) []string {
	segs := strings.Split(path, "/")
	names := []string{}
	for i, seg := range segs {
		if i == len(segs)-1 {
			continue
		}
		if len(seg) < 2 || seg[0] != '{' || seg[len(seg)-1] != '}' {
			continue
		}
		names = append(names, seg[1:len(seg)-1])
	}
	return names
}
