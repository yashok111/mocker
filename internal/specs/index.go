package specs

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/router"
)

// httpMethods lists the path-item keys that are HTTP operations, in a fixed
// canonical order. Any other key a path item carries — "parameters",
// "servers", "summary", "description", "$ref", a vendor "x-..." extension —
// is simply not in this list, so the loop in Index walks past it without a
// separate skip-list; every one of the 110 path items in the acceptance
// document carries "servers" and this is how it gets ignored.
var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// Index walks doc's paths object into operations + operation_responses rows
// (DESIGN §7 step 5). It never fails: a document with no "paths" at all
// yields zero operations, and one malformed operation is recorded with
// Operation.ParseError plus a Report warning rather than aborting the walk —
// the same "import never falls over" contract Load already keeps for the
// document as a whole (HARD RULE 8).
//
// The returned map is keyed by the INDEX INTO ops, exactly what
// [Repo.ReplaceOperations] expects: an operation's row id does not exist
// until its own INSERT returns, so there is no other value responses could
// have been keyed by before that.
//
// doc.Root() is a plain map[string]any produced by encoding/json, which does
// not preserve a JSON object's original key order (HARD RULE 2 forbids an
// order-preserving decoder too). SourceOrder therefore cannot reproduce the
// document's own byte order — nothing downstream of Load ever could — but it
// is still a total, stable, reproducible order: paths are walked sorted
// lexicographically and methods in the fixed order above, so two Index calls
// over identical bytes (Import, and later Report's re-derivation) always
// agree on it, which is all §8 rule 4's tie-break actually needs.
func Index(doc *openapi.Document, res *openapi.Resolver, rep *openapi.Report) ([]*Operation, map[int][]*Response) {
	pathsRaw, _ := doc.Root()["paths"].(map[string]any)

	paths := make([]string, 0, len(pathsRaw))
	for p := range pathsRaw {
		paths = append(paths, p)
	}
	slices.Sort(paths)

	var ops []*Operation
	resp := make(map[int][]*Response)
	order := int64(0)

	for _, p := range paths {
		item, ok := pathsRaw[p].(map[string]any)
		if !ok {
			pointer := "#/paths/" + escapePointerToken(p)
			rep.Add(pointer, "path-item-not-object", fmt.Sprintf("path item %q is not a JSON object", p))
			continue
		}

		for _, method := range httpMethods {
			opRaw, present := item[method]
			if !present {
				continue
			}

			pointer := "#/paths/" + escapePointerToken(p) + "/" + method
			op := &Operation{
				Method:        strings.ToUpper(method),
				Path:          p,
				CanonicalPath: router.CanonicalPath(p),
				SourceOrder:   order,
				Pointer:       pointer,
			}
			order++
			rep.Operations++

			opObj, ok := opRaw.(map[string]any)
			if !ok {
				msg := fmt.Sprintf("operation at %s is not a JSON object", pointer)
				op.ParseError = &msg
				rep.Degraded++
				rep.Add(pointer, "operation-not-object", msg)
				ops = append(ops, op)
				resp[len(ops)-1] = []*Response{fallbackResponse()}
				continue
			}

			if v, ok := nonEmptyString(opObj, "operationId"); ok {
				op.OperationID = &v
			}
			if v, ok := nonEmptyString(opObj, "summary"); ok {
				op.Summary = &v
			}
			if v, ok := firstTag(opObj); ok {
				op.Tag = &v
			}

			ops = append(ops, op)
			resp[len(ops)-1] = indexResponses(res, rep, opObj, pointer)
		}
	}

	return ops, resp
}

// nonEmptyString reads a string field off m, treating both "absent" and ""
// as not-provided — the same convention [Repo.Import] already uses when an
// empty Name falls back to doc.Title().
func nonEmptyString(m map[string]any, key string) (string, bool) {
	v, ok := m[key].(string)
	if !ok || v == "" {
		return "", false
	}
	return v, true
}

// firstTag returns an operation's first tag. Tag is a single TEXT column
// (DESIGN §13): the acceptance document carries 131 tag entries across 130
// operations, so at least one operation has two, and the first one wins.
func firstTag(op map[string]any) (string, bool) {
	tags, ok := op["tags"].([]any)
	if !ok || len(tags) == 0 {
		return "", false
	}
	t, ok := tags[0].(string)
	if !ok || t == "" {
		return "", false
	}
	return t, true
}

// responseEntry pairs one entry of an operation's "responses" object with
// the pointer to where it was written in the document — before any $ref on
// it is followed.
type responseEntry struct {
	selector string
	node     any
	pointer  string
}

// indexResponses turns one operation's "responses" object into
// operation_responses rows (DESIGN §7 step 5): one row per recognized
// selector, plus exactly one is_default row per the selection rule below. A
// responses object with nothing usable in it — missing, empty, or every
// entry using a selector this phase does not recognize — still returns
// exactly one row: a synthesized 200/"fallback", so the runtime always has
// something to answer with.
func indexResponses(res *openapi.Resolver, rep *openapi.Report, opObj map[string]any, opPointer string) []*Response {
	responsesRaw, _ := opObj["responses"].(map[string]any)

	entries := make([]responseEntry, 0, len(responsesRaw))
	for sel, node := range responsesRaw {
		entries = append(entries, responseEntry{
			selector: sel,
			node:     node,
			pointer:  opPointer + "/responses/" + escapePointerToken(sel),
		})
	}
	// responsesRaw is a map[string]any: Go randomizes its range order on
	// purpose, it is not the document's own order (see Index's doc comment).
	// Sorting here is what makes two Index calls over identical bytes agree
	// on emission order.
	slices.SortFunc(entries, func(a, b responseEntry) int {
		return cmp.Compare(selectorSortKey(a.selector), selectorSortKey(b.selector))
	})

	out := make([]*Response, 0, len(entries))
	for _, e := range entries {
		httpStatus, origin, ok := classifySelector(e.selector)
		if !ok {
			rep.Add(e.pointer, "unrecognized-response-selector",
				fmt.Sprintf("response selector %q is neither a numeric status, \"2XX\" nor \"default\"", e.selector))
			continue
		}
		mediaType, schemaPtr := resolveResponseContent(res, rep, e.node, e.pointer)
		out = append(out, &Response{
			Selector:     e.selector,
			HTTPStatus:   httpStatus,
			StatusOrigin: origin,
			MediaType:    mediaType,
			SchemaPtr:    schemaPtr,
		})
	}

	if len(out) == 0 {
		return []*Response{fallbackResponse()}
	}
	markDefault(out)
	return out
}

// classifySelector turns a responses-map key into the http_status this
// service would actually send and where that status came from (DESIGN §7
// step 5: "2XX" and "default" are not sendable codes on their own). A
// selector this phase does not recognize — anything but a 3-digit numeric
// code, "2XX", or "default" — reports ok=false: the acceptance document uses
// none of those, and a response this indexer cannot classify is better
// dropped with a warning than stored under a guessed status.
func classifySelector(sel string) (httpStatus int, origin string, ok bool) {
	switch sel {
	case "default":
		return 200, "default", true
	case "2XX":
		return 200, "2XX", true
	}
	if n, err := strconv.Atoi(sel); err == nil && n >= 100 && n <= 599 {
		return n, "numeric", true
	}
	return 0, "", false
}

// selectorSortKey orders responseEntry values for deterministic emission:
// numeric selectors ascending by their own value, "2XX" alongside the 2xx
// numerics it stands in for, "default" last, and anything unrecognized
// (destined to be dropped with a warning anyway) sorted just ahead of it.
func selectorSortKey(sel string) int {
	switch sel {
	case "default":
		return 1000
	case "2XX":
		return 200
	}
	if n, err := strconv.Atoi(sel); err == nil {
		return n
	}
	return 999
}

// markDefault applies DESIGN §7 step 5's selection rule to rs, which must be
// non-empty: the lowest numeric 2xx wins; absent that, the "2XX" row; absent
// that, the "default" row; absent all three, the lowest http_status of any
// kind — and THAT row's StatusOrigin is overwritten to "fallback", so the
// admin UI can show it was never really meant to be the default; this
// indexer just had nothing better to offer.
func markDefault(rs []*Response) {
	best := -1
	for i, r := range rs {
		if r.StatusOrigin == "numeric" && r.HTTPStatus >= 200 && r.HTTPStatus < 300 {
			if best == -1 || r.HTTPStatus < rs[best].HTTPStatus {
				best = i
			}
		}
	}
	if best == -1 {
		for i, r := range rs {
			if r.StatusOrigin == "2XX" {
				best = i
				break
			}
		}
	}
	if best == -1 {
		for i, r := range rs {
			if r.StatusOrigin == "default" {
				best = i
				break
			}
		}
	}
	if best == -1 {
		for i, r := range rs {
			if best == -1 || r.HTTPStatus < rs[best].HTTPStatus {
				best = i
			}
		}
		rs[best].StatusOrigin = "fallback"
	}
	rs[best].IsDefault = true
}

// fallbackResponse is the synthesized row for an operation whose responses
// object yielded nothing usable (missing, empty, or every selector
// unrecognized). Selector is left "" to record plainly that nothing in the
// document produced this row.
func fallbackResponse() *Response {
	return &Response{HTTPStatus: 200, IsDefault: true, StatusOrigin: "fallback"}
}

// resolveResponseContent follows node's $ref chain, if it has one, and
// extracts the media type and schema pointer of the response object it
// resolves to. It never fails the caller: a resolve error becomes a Report
// warning and a (nil, nil) result — the same "degrade, don't abort"
// treatment DESIGN §7 gives everything else in the indexer — and a
// legitimately content-less response (a 204, or the one 200 in the
// acceptance document with no content at all) is (nil, nil) with no warning,
// since there was nothing wrong, just nothing to report.
//
// basePointer is where schema_ptr gets anchored: for an inline response it
// is pointer itself, but for a $ref'd one (with or without ignored siblings,
// OAS 3.0 §4.8.23.1) it is the LAST hop of node's $ref chain — "the RESOLVED
// location, not the referring site" per this phase's requirements. That is
// exactly what [openapi.Resolver.ResolveNodePointer] reports: for a chained
// response $ref (A -> B, B carries "content"), anchoring at A instead would
// name a location that is itself just {"$ref": "...B"} and has no "content"
// key to navigate into at all — schema_ptr must name B, not A.
func resolveResponseContent(res *openapi.Resolver, rep *openapi.Report, node any, pointer string) (mediaType, schemaPtr *string) {
	lastPointer, resolved, err := res.ResolveNodePointer(node)
	if err != nil {
		rep.Add(pointer, "unresolved-response-ref", fmt.Sprintf("response at %s: %v", pointer, err))
		return nil, nil
	}
	basePointer := pointer
	if lastPointer != "" {
		basePointer = lastPointer
	}

	respObj, ok := resolved.(map[string]any)
	if !ok {
		return nil, nil
	}
	content, ok := respObj["content"].(map[string]any)
	if !ok || len(content) == 0 {
		return nil, nil
	}

	mt := SelectMediaType(content)
	mediaType = &mt

	if entry, ok := content[mt].(map[string]any); ok {
		if _, hasSchema := entry["schema"]; hasSchema {
			ptr := basePointer + "/content/" + escapePointerToken(mt) + "/schema"
			schemaPtr = &ptr
		}
	}
	return mediaType, schemaPtr
}

// SelectMediaType picks one media type out of a response's "content" map.
// "application/json" wins outright when present (181 of the 232 media-typed
// response entries in the acceptance document are exactly that); otherwise
// the lexicographically first key wins, purely for determinism — content is
// a decoded JSON object, and Go's map range order is randomized, not the
// document's own order.
//
// content must be non-empty: this indexes keys[0] and PANICS on an empty
// map. That is deliberate, not an oversight — the one existing call site
// guards len(content) == 0 four lines above its own call, so an empty map
// reaching here is that caller's bug, not something this function should
// paper over with a fabricated "" result. Exported (P3a) so resource
// derivation (this same package, deriveSuggestions) can pick a detail
// variant's media type with the identical rule the indexer already applies
// to every response — a second implementation would only be able to drift
// from this one, never improve on it. A caller that does not want the
// panic must keep guarding emptiness itself, exactly as this one does.
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

// escapePointerToken RFC-6901-escapes one segment for embedding in a JSON
// pointer: "~" first (to "~0"), then "/" (to "~1"). That order is the
// reverse of unescaping on purpose — escaping "/" first would let the "~"
// just inserted as part of "~1" get mangled by the very next replacement.
func escapePointerToken(tok string) string {
	tok = strings.ReplaceAll(tok, "~", "~0")
	tok = strings.ReplaceAll(tok, "/", "~1")
	return tok
}
