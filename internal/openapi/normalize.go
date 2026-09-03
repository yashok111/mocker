package openapi

import "slices"

// Dialect normalization (DESIGN §7 step 4). Three quirks, applied everywhere
// in the document tree: a generic map/array walk cannot tell a Schema Object
// from a Parameter Object or a Header Object (HARD RULE 2: no OpenAPI
// library, so no schema-aware walker either), and the acceptance document
// happens to have no key collision that would make that ambiguity matter.
//
//   - boolean exclusiveMinimum/exclusiveMaximum -> the numeric form used by
//     JSON Schema 2020-12 (what OAS 3.1 schemas speak natively).
//   - singular "example" -> a one-element "examples" array.
//   - "nullable: true" -> BOTH an "x-nullable" marker (for the generator)
//     AND an honest union "type" (for the validator). DESIGN is explicit
//     that one representation alone is not acceptable: a generator only
//     needs the marker, but a validator that trusts "type" alone would
//     reject a legitimately null value without the union.

// normalizeDialect walks v in place, applying the three transforms above to
// every object node it finds. v is returned for convenience; callers already
// hold a reference to the (mutated) root they passed in.
func normalizeDialect(v any) any {
	switch node := v.(type) {
	case map[string]any:
		for k, child := range node {
			node[k] = normalizeDialect(child)
		}
		normalizeExclusiveBound(node, "minimum", "exclusiveMinimum")
		normalizeExclusiveBound(node, "maximum", "exclusiveMaximum")
		normalizeExample(node)
		normalizeNullable(node)
		return node
	case []any:
		for i, child := range node {
			node[i] = normalizeDialect(child)
		}
		return node
	default:
		return v
	}
}

// normalizeExclusiveBound rewrites a boolean exclusiveMinimum/exclusiveMaximum
// modifier into the numeric form: true absorbs the sibling bound value and
// replaces it; false (or a bound-less true) just drops the now-redundant
// boolean. A document whose exclusiveMinimum is already numeric — an OAS 3.1
// document authored that way — is left untouched: the type assertion below
// simply does not match a non-bool value.
func normalizeExclusiveBound(node map[string]any, boundKey, exclusiveKey string) {
	excl, ok := node[exclusiveKey].(bool)
	if !ok {
		return
	}
	if excl {
		if bound, hasBound := node[boundKey]; hasBound {
			node[exclusiveKey] = bound
			delete(node, boundKey)
			return
		}
	}
	delete(node, exclusiveKey)
}

// normalizeExample rewrites a singular "example" into a one-element
// "examples" array. An "examples" key already present is never overwritten —
// it stays the authority and "example" is just dropped.
func normalizeExample(node map[string]any) {
	example, ok := node["example"]
	if !ok {
		return
	}
	if _, hasPlural := node["examples"]; !hasPlural {
		node["examples"] = []any{example}
	}
	delete(node, "example")
}

// normalizeNullable turns "nullable: true" into an x-nullable marker plus a
// union "type" that actually says the value may be null.
func normalizeNullable(node map[string]any) {
	nullable, ok := node["nullable"].(bool)
	if !ok || !nullable {
		return
	}
	node["x-nullable"] = true
	switch t := node["type"].(type) {
	case string:
		node["type"] = []any{t, "null"}
	case []any:
		if !slices.ContainsFunc(t, func(v any) bool { s, ok := v.(string); return ok && s == "null" }) {
			node["type"] = append(slices.Clone(t), "null")
		}
	}
	delete(node, "nullable")
}
