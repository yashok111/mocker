// populate.go is D5's generation half: for each id 1..seed_count, generate
// the DETAIL route's body through the SAME [gen.Generator]/[gen.Body] path
// that serves a detail route today, then FORCE the id field — the
// generator's own identity write-back only ever writes a property literally
// named "id", so a family whose id_field is something else would otherwise
// store a generated value and GET X/{i} would miss every row (R35).
//
// Everything here runs OUTSIDE [Repo.Confirm]'s write transaction (R11):
// building a generator over a 347 KB document and walking N schemas would
// otherwise hold the single writer connection for the duration, the exact
// serialization internal/specs/repo.go's own Index avoids for the identical
// reason.
package resources

import (
	"bytes"
	"context"
	"fmt"
	"strconv"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/openapi"
)

// buildGenerator builds a [gen.Generator] over specID's normalized document,
// mirroring internal/mockplane/runtime.go's buildRuntime step 1-4 exactly:
// Normalized -> Load -> NewResolver -> gen.New, with Seed/ListSize/NullRate/
// Identity/Auth from settings (the caller's already-composed EFFECTIVE
// settings — scenario overlay included, D5) and MaxBytes from this
// package's own maxResponseBytes (cfg.MaxResponse). Returns the resolver
// too: [computeWriteForm]'s two-hop $ref walk needs it directly, and
// [gen.Generator] does not expose the one it holds.
func (r *Repo) buildGenerator(ctx context.Context, specID int64, settings domain.Settings) (*gen.Generator, *openapi.Resolver, error) {
	normalized, err := r.specs.Normalized(ctx, specID)
	if err != nil {
		return nil, nil, fmt.Errorf("load normalized document for spec %d: %w", specID, err)
	}
	doc, _, err := openapi.Load(normalized)
	if err != nil {
		return nil, nil, fmt.Errorf("re-load normalized document for spec %d: %w", specID, err)
	}
	resolver := openapi.NewResolver(doc, openapi.DefaultRefBudget)
	generator := gen.New(resolver, gen.Options{
		Seed:     settings.Seed,
		ListSize: settings.ListSize,
		NullRate: settings.NullRate,
		MaxBytes: r.maxResponseBytes,
		Identity: settings.Identity,
		Auth:     settings.Auth,
	})
	return generator, resolver, nil
}

// populateEntities generates seedCount detail bodies for family, ids
// startID..startID+seedCount-1, forcing idField to the allocated id on
// every one (R35). startID is 1 for a top-level family's only call; a
// nested family's population (see [populateScoped]) calls this once per
// scope with startID continuing from the previous scope's own count, so
// data[idField] always equals the entity_key the caller is about to insert
// it under — entity_key stays unique FAMILY-WIDE, across every scope
// (D5.5 point 4), never restarting at 1 per scope.
//
// Any error from gen.Body, or a generated body that does not decode into a
// JSON object, ABORTS the whole batch — wrapped in [ErrPopulationFailed]
// and naming the entity index and the family, per R13/D13 clause 14: the
// caller (Confirm) must write NOTHING when this returns an error.
func populateEntities(generator *gen.Generator, detailVariant gen.ResponseVariant, family, idParam string, seedCount, startID int, idField, idType string) ([][]byte, error) {
	bodies := make([][]byte, 0, seedCount)
	for i := 1; i <= seedCount; i++ {
		id := startID + i - 1
		req := gen.Request{
			Method:        "GET",
			CanonicalPath: family + "/{}",
			PathParams:    map[string]string{idParam: strconv.Itoa(id)},
			ListFamily:    family,
			IDParam:       idParam,
			Status:        200,
		}
		raw, err := generator.Body(detailVariant, req)
		if err != nil {
			return nil, fmt.Errorf("%w: generate entity %d/%d for %q: %w", ErrPopulationFailed, i, seedCount, family, err)
		}
		body, ferr := forceID(raw, idField, idType, id)
		if ferr != nil {
			return nil, fmt.Errorf("%w: entity %d/%d for %q: %w", ErrPopulationFailed, i, seedCount, family, ferr)
		}
		bodies = append(bodies, body)
	}
	return bodies, nil
}

// populatePairs is [populateEntities] run once per (base, route-scope)
// PAIR — D6.1's own unit, and P3h's widening of what P3e/P3g shipped as
// populateScoped (one dimension, route scope alone). Factored out once
// here rather than copied between [Repo.prepareConfirm] (pairs computed
// from LIVE ancestor keys within each declared base value, via
// [chainPairs]) and [Repo.prepareGroupPopulation] (pairs computed from the
// group's own PREPARED keys, D8.2 rule 1, via the same [extendScopes]
// arithmetic, looped once per declared base). It no longer decides
// anything about scopes itself — that was P3e's own one-element
// [EncodeScope] call, deleted by that slice (D5.3): it just receives the
// already-paired (base, scope) list and mints seedCount bodies per pair,
// continuing ONE running id counter across ALL of them — across every
// base value AND every route scope alike (D6.2) — so entity_key stays
// unique family-wide (D5.5 point 4/D6.2 — see [populateEntities]'s own doc
// comment for why): resources.seq and a checkpoint restore's MAX rule both
// assume one counter per family, not one per base or one per route scope.
// A top-level, unparameterised-base-path family is the SAME code path with
// one pair, {"", ""} — not a separate branch, which is exactly what would
// let that case drift out of agreement with a nested and/or
// base-parameterised one with more shapes to keep in sync than one.
// Returns the whole population concatenated in pair order, and two
// same-length []ScopeKey slices naming each body's own base and route
// scope.
func populatePairs(generator *gen.Generator, detailVariant gen.ResponseVariant, family, idParam string, seedCount int, pairs []basePair, idField, idType string) ([][]byte, []ScopeKey, []ScopeKey, error) {
	var bodies [][]byte
	var baseKeys, scopeKeys []ScopeKey
	start := 1
	for _, pair := range pairs {
		pairBodies, err := populateEntities(generator, detailVariant, family, idParam, seedCount, start, idField, idType)
		if err != nil {
			return nil, nil, nil, err
		}
		for range pairBodies {
			baseKeys = append(baseKeys, pair.Base)
			scopeKeys = append(scopeKeys, pair.Scope)
		}
		bodies = append(bodies, pairBodies...)
		start += len(pairBodies)
	}
	return bodies, baseKeys, scopeKeys, nil
}

// forceID decodes raw (UseNumber, so an integer id above 2^53 survives the
// round trip — D13 clause 38) into a map, sets data[idField] to seq shaped
// by idType via [gen.CoerceIDValue], and re-marshals. A raw that does not
// decode into a JSON object — nil bytes (gen.Body's SchemaPtr=="" branch),
// a JSON array, a JSON scalar, or JSON null — is exactly the failure R13
// says must abort the whole confirm, surfaced here as a plain error for
// [populateEntities] to wrap.
func forceID(raw []byte, idField, idType string, seq int) ([]byte, error) {
	dec := jsonx.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var data map[string]any
	if err := dec.Decode(&data); err != nil {
		return nil, fmt.Errorf("decode generated body: %w", err)
	}
	if data == nil {
		return nil, fmt.Errorf("generated body did not decode into a JSON object")
	}
	data[idField] = gen.CoerceIDValue(strconv.Itoa(seq), idType, "")
	out, err := jsonx.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("marshal forced-id body: %w", err)
	}
	return out, nil
}
