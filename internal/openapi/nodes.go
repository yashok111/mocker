package openapi

import "slices"

// WalkRefNodes walks a decoded document depth-first and calls visit once
// for every JSON OBJECT in it, in no particular order among siblings (Go
// randomizes map range order and this walk does not sort — every caller
// below either answers a boolean, collects into a set the caller sorts, or
// mutates in place, so none of them depends on sibling order). Arrays are
// descended into but never visited: a `$ref` can only sit on an object.
//
// It exists because three packages had grown their own recursive
// map[string]any/[]any walk looking for `$ref` at any depth — customep's
// containsRef (a boolean), customep.SchemaRefs (collect and validate) and
// mockplane's sanitizeRefs (mutate an unresolvable node into an empty
// object) — three copies of the same traversal, each with its own chance
// of forgetting that a `$ref` can hide inside an array of allOf branches.
// The traversal is one; what differs is only what happens AT a node, which
// is what visit is for.
//
// visit receives the object MAP ITSELF, not a copy, so a caller may rewrite
// it in place; the walk descends into the node's children afterwards, which
// means a visit that empties the map is descended into as the emptied map
// (nothing left to reach) rather than the original — exactly what
// sanitizeRefs wants, and the reason it needs no separate "do not descend"
// signal.
//
// The first error visit returns aborts the whole walk and comes back
// unchanged, so a caller wanting early exit without a real failure passes a
// sentinel and compares against it.
func WalkRefNodes(node any, visit func(obj map[string]any) error) error {
	switch n := node.(type) {
	case map[string]any:
		if err := visit(n); err != nil {
			return err
		}
		for _, child := range n {
			if err := WalkRefNodes(child, visit); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range n {
			if err := WalkRefNodes(child, visit); err != nil {
				return err
			}
		}
	}
	return nil
}

// SelectMediaType picks one media type out of a response's (or request
// body's) "content" map. "application/json" wins outright when present
// (181 of the 232 media-typed responses in the acceptance document declare
// it); otherwise the lexicographically first key wins, purely so that two
// runs over the same document agree — a decoded JSON object is an unordered
// Go map and there is no "first" to prefer.
//
// PANICS on an empty map, deliberately: every caller guards emptiness a few
// lines above its own call (a response with `content: {}` has no media type
// and the caller must decide what that means for it), so an empty map
// reaching here is that caller's bug, not something this function should
// paper over with a fabricated "" or a fabricated "application/json". A
// caller that does not want the panic keeps guarding emptiness itself.
//
// It lives in this leaf (2026-09-07) because it had grown two
// implementations of one rule: specs.SelectMediaType, which the indexer
// applies to every response, and internal/design's own restatement for the
// export — the comment on the latter said out loud what the risk was
// ("a divergence here would silently patch `application/hal+json` while
// the mock serves `application/json`") and then took it anyway, because
// internal/specs is a store package a leaf must not import. This package
// imports nothing from the module, so both can reach it.
func SelectMediaType(content map[string]any) string {
	if _, ok := content["application/json"]; ok {
		return "application/json"
	}
	keys := make([]string, 0, len(content))
	for k := range content {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys[0]
}
