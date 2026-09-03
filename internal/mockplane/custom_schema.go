package mockplane

import (
	"errors"
	"log/slog"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/jsonpatch"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
)

// customSchemaPtr is the sentinel gen.ResponseVariant.SchemaPtr a custom
// endpoint's generated response carries: gen.Body returns nothing for an
// empty pointer, and the inline root arrives through Request.PatchedSchema
// exactly as a stream tick's does (stream.go's "#/stream/tick/schema") —
// the pointer itself is never resolved.
const customSchemaPtr = "#/x-mocker/custom/schema"

// inlineSource is what a custom endpoint's generated variant (P7a, DESIGN
// §34.3) hands assembleResponse in place of a spec variant's resolved
// pointer and an override row's recipe set: the decoded schema, its `$ref`s
// sanitized (below), and the row's own recipes compiled once at runtime
// build — DESIGN §18's "no per-request work" for a schema exactly as
// recipeSets already gives it to an override's recipes.
type inlineSource struct {
	Schema   map[string]any
	Recipes  *recipes.Set
	ListSize *overrides.ListSize
}

// lookupCustomInline returns the compiled source for (custom row id,
// status), or false when the variant at that status carries no schema, is
// pinned, or belongs to a stream row.
func (rt *runtime) lookupCustomInline(rowID int64, status string) (inlineSource, bool) {
	byStatus, ok := rt.customInline[rowID]
	if !ok {
		return inlineSource{}, false
	}
	src, ok := byStatus[status]
	return src, ok
}

// buildCustomInline compiles every custom http row's schema-bearing,
// non-pinned variant into an inlineSource, once per runtime build.
//
// A `$ref` the resolver cannot find — a row written before the spec was
// rebound by a path the admin plane's ValidateRefs does not guard (a
// hand-run UPDATE, a spec row deleted from the file), or simply the
// skeleton document a no-spec workspace resolves into — is decisions.md
// D6's serve-time half: logged ONCE here with the row and the pointer, and
// that node generates as `{}` (an empty object schema), the identical
// tolerance a failed schemaPatch apply already has one function over.
// The plane always answers.
func buildCustomInline(log *slog.Logger, workspaceSlug string, rows map[int64]*customep.Row, resolver *openapi.Resolver) map[int64]map[string]inlineSource {
	if len(rows) == 0 {
		return nil
	}
	out := make(map[int64]map[string]inlineSource)
	for id, row := range rows {
		if row.Kind != "" && row.Kind != customep.KindHTTP {
			continue
		}
		for status, v := range row.Responses {
			if v.Mode == "pinned" || len(v.Schema) == 0 {
				continue
			}
			var schema map[string]any
			if err := jsonx.Unmarshal(v.Schema, &schema); err != nil || schema == nil {
				log.Error("custom endpoint schema: invalid stored document, serving an empty body",
					"workspace", workspaceSlug, "endpoint", id, "status", status, "err", err)
				continue
			}
			schema = chaseRootRef(log, workspaceSlug, id, status, schema, resolver)
			sanitizeRefs(log, workspaceSlug, id, status, schema, resolver)
			set, err := recipes.Compile(v.Recipes)
			if err != nil {
				log.Error("compile recipes: invalid stored recipe, serving without it",
					"workspace", workspaceSlug, "endpoint", id, "status", status, "err", err)
				set = nil
			}
			if out[id] == nil {
				out[id] = map[string]inlineSource{}
			}
			out[id][status] = inlineSource{Schema: schema, Recipes: set, ListSize: row.ListSize}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

var (
	errNonStringRef = errors.New("$ref is not a string")
	errNoResolver   = errors.New("no document to resolve against")
)

// chaseRootRef resolves a `$ref` at the ROOT of the inline schema —
// `schema: {"$ref": "#/components/schemas/User"}`, §34.3's own "reuse the
// base's User in one line" — into a private deep copy of the referenced
// node. gen.Body takes Request.PatchedSchema as the root VERBATIM (it
// never resolves the root the way it resolves v.SchemaPtr; nested `$ref`s
// are chased by walkNode), so an unchased root would generate as an
// untyped schema, i.e. a random string. The copy is what keeps the
// resolver's own document out of a map sanitizeRefs mutates. A root that
// does not resolve is left for sanitizeRefs to log and degrade.
func chaseRootRef(log *slog.Logger, workspaceSlug string, rowID int64, status string, schema map[string]any, resolver *openapi.Resolver) map[string]any {
	if _, ok := schema["$ref"]; !ok || resolver == nil {
		return schema
	}
	resolved, err := resolver.ResolveNode(schema)
	if err != nil {
		return schema
	}
	obj, ok := resolved.(map[string]any)
	if !ok {
		log.Warn("custom endpoint schema: root $ref resolves to a non-object; generating an empty object",
			"workspace", workspaceSlug, "endpoint", rowID, "status", status, "ref", schema["$ref"])
		return map[string]any{"type": "object"}
	}
	copied, cerr := jsonpatch.Apply(jsonpatch.Patch{}, obj)
	if cerr != nil || copied == nil {
		return schema
	}
	return copied
}

// sanitizeRefs walks schema and empties every `$ref` node the resolver
// cannot resolve — the node keeps its map but loses every key, so the
// generator walks `{}` there — logging each once. A resolvable `$ref` is
// left in place for the walker to follow through the same resolver.
func sanitizeRefs(log *slog.Logger, workspaceSlug string, rowID int64, status string, node any, resolver *openapi.Resolver) {
	switch n := node.(type) {
	case map[string]any:
		if raw, ok := n["$ref"]; ok {
			ref, isString := raw.(string)
			var err error
			if !isString {
				err = errNonStringRef
			} else if resolver == nil {
				err = errNoResolver
			} else {
				_, err = resolver.Resolve(ref)
			}
			if err != nil {
				log.Warn("custom endpoint schema: $ref does not resolve against the bound spec; generating {} there",
					"workspace", workspaceSlug, "endpoint", rowID, "status", status, "ref", raw, "err", err)
				// An EMPTY schema map is untyped and the generator picks
				// a string for it; `{}` in the served body means an empty
				// OBJECT, which is what "type: object" and nothing else
				// produces.
				for k := range n {
					delete(n, k)
				}
				n["type"] = "object"
				return
			}
		}
		for _, child := range n {
			sanitizeRefs(log, workspaceSlug, rowID, status, child, resolver)
		}
	case []any:
		for _, child := range n {
			sanitizeRefs(log, workspaceSlug, rowID, status, child, resolver)
		}
	}
}
