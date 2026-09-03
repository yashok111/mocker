// Package design composes a workspace into ONE OpenAPI 3.1 document — the
// deliverable of DESIGN §34 (v12): the bound spec's normalized document as
// the base, the Workspace layer as the delta over it, written the way a
// backend team reads a contract. A custom endpoint is a new operation, a
// custom endpoint at a base operation's canonical shape REPLACES it (§8's
// rule 3 read as intent), an override's schemaPatch is the response schema
// written inline, a pinned body is an example, routeOff is `deprecated:
// true` and never a deletion (the base is the contract the backend holds;
// a removal is a proposal the reader must see). §34.4's six rules, plus
// what the interview of 2026-09-03 added (decisions.md
// mocker-p7-api-design D7): an overrideOn:false row is omitted, sse and
// ws rows become operations, reqSchema becomes requestBody, undeclared
// path parameters are derived, and `info.version` carries
// `-draft.<revision>` with any earlier draft suffix stripped first.
//
// The package is a LEAF over decoded rows and bytes: it imports
// internal/openapi (Load, the resolver), internal/jsonpatch (the same
// patch primitive the runtime applies), internal/customep and
// internal/overrides (the row types) and internal/router (CanonicalPath),
// and never a store or a plane. The mock plane imports it for
// [Skeleton] — the document a workspace with NO spec serves from and
// exports — so the runtime and the export cannot disagree about what "no
// base" means.
//
// The output is run through openapi.Load and its Normalized() bytes are
// what Compose returns. That is not decoration: Load's dialect normalizer
// (normalize.go) walks EVERY map of a document, example payloads
// included, so an export written raw and then imported would come back
// with a pinned body's own `nullable` or `example` keys rewritten — and
// the round trip DESIGN §34.4 makes the acceptance test would fail on the
// delta's data rather than on its structure. Normalizing here makes the
// export a fixed point of Load: what is exported is byte for byte what a
// re-import stores.
package design

import (
	"bytes"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/jsonpatch"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/router"
)

// Input is everything Compose needs, read by the caller (the admin plane)
// and handed in decoded: this package opens no store.
type Input struct {
	// WorkspaceName titles the skeleton when no base is bound; ignored
	// when Base is present (the base's own info.title stands).
	WorkspaceName string
	// Revision suffixes info.version as `-draft.<revision>`.
	Revision int64
	// Base is the bound spec's NORMALIZED document (specs.Repo.Normalized),
	// nil when the workspace has no spec — then [Skeleton] is the base.
	Base []byte
	// Overrides is the workspace's op_overrides map keyed by
	// overrides.OpKey — the COMPOSED layer when a scenario is active is
	// deliberately NOT what the caller passes: an export is the
	// Workspace layer (§34.2), the scenario is a snapshot of it.
	Overrides map[string]*overrides.Row
	// Endpoints is every custom_endpoints row of the workspace, in
	// source order.
	Endpoints []*customep.Row
}

// ErrCompose wraps every reason a document cannot be composed that is not
// a bug in this package: a base that does not load, a pinned body that is
// not JSON where JSON is declared.
var ErrCompose = errors.New("design: cannot compose the document")

// Skeleton is the empty OpenAPI 3.1 document a workspace with no spec
// serves from and exports: the ONE definition the runtime (buildRuntime's
// generator over it) and the export share. Observed before it was chosen
// (decisions.md D5, §1): openapi.Load accepts it with zero operations, its
// Normalized() bytes are a fixed point of Load, and a `$ref` into it
// answers ErrPointerNotFound.
func Skeleton(title string) []byte {
	if title == "" {
		title = "design"
	}
	b, err := jsonx.Marshal(map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": title, "version": "0.0.0"},
		"paths":   map[string]any{},
	})
	if err != nil {
		// A three-key map of strings cannot fail to marshal; a panic here
		// is a broken build, not a runtime condition.
		panic("design: marshal skeleton: " + err.Error())
	}
	return b
}

// draftSuffixRe matches the `-draft.<n>` tail Compose appends, so a base
// that was itself an accepted draft is re-suffixed rather than
// double-suffixed (`1.2.0-draft.7-draft.9`).
var draftSuffixRe = regexp.MustCompile(`-draft\.\d+$`)

// Compose builds the document. The result is normalized (see the package
// comment) and therefore imports through specs.Repo.Import to a spec whose
// normalized column equals these bytes.
func Compose(in Input) ([]byte, error) {
	base := in.Base
	if len(base) == 0 {
		base = Skeleton(in.WorkspaceName)
	}
	doc, _, err := openapi.Load(base)
	if err != nil {
		return nil, fmt.Errorf("%w: load base: %w", ErrCompose, err)
	}
	// A private copy: Load's root is the resolver's document and must not
	// be mutated underneath it while patches still resolve into it.
	rootAny, err := decodeJSON(doc.Normalized())
	if err != nil {
		return nil, fmt.Errorf("%w: decode base: %w", ErrCompose, err)
	}
	root, _ := rootAny.(map[string]any)
	if root == nil {
		return nil, fmt.Errorf("%w: decode base: the document is not an object", ErrCompose)
	}
	resolver := openapi.NewResolver(doc, openapi.DefaultRefBudget)

	paths, _ := root["paths"].(map[string]any)
	if paths == nil {
		paths = map[string]any{}
		root["paths"] = paths
	}

	c := &composer{root: root, paths: paths, resolver: resolver, workspaceName: in.WorkspaceName}
	c.applyOverrides(in.Overrides)
	if err := c.applyEndpoints(in.Endpoints); err != nil {
		return nil, err
	}
	c.stampVersion(in.Revision)

	out, err := jsonx.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal: %w", ErrCompose, err)
	}
	final, _, err := openapi.Load(out)
	if err != nil {
		// The composed document is built from a document Load accepted plus
		// rows every writer validated; a refusal here is a bug in this
		// package, reported as one rather than hidden.
		return nil, fmt.Errorf("%w: the composed document does not load: %w", ErrCompose, err)
	}
	return final.Normalized(), nil
}

type composer struct {
	root          map[string]any
	paths         map[string]any
	resolver      *openapi.Resolver
	workspaceName string
}

// decodeJSON decodes raw with numbers kept as their LITERALS (jsonx.Number),
// the same way openapi's own decoder reads a document: a plain Unmarshal
// into `any` renders every number through float64, so `1.0` would come
// back as `1` and an integer past 2^53 would be corrupted — and the export
// promises `components` byte-equal to the base's. Every document this
// package decodes — the base, a row's schema, a pinned body — goes
// through this one door for that reason.
func decodeJSON(raw []byte) (any, error) {
	dec := jsonx.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return v, nil
}

// httpMethods is the closed set of path-item keys that are operations —
// the same list the specs indexer walks — so `parameters`, `summary` and
// `x-` keys on a path item are never read as methods.
var httpMethods = []string{"get", "put", "post", "delete", "options", "head", "patch", "trace"}

// operationAt returns the operation object at (path, method), nil when the
// document has none. method is any case.
func (c *composer) operationAt(path, method string) map[string]any {
	item, _ := c.paths[path].(map[string]any)
	if item == nil {
		return nil
	}
	op, _ := item[strings.ToLower(method)].(map[string]any)
	return op
}

// applyOverrides is rules 4 and 5: per override row with OverrideOn, on the
// operation its LITERAL (method, path) names — never through
// router.CanonicalPath, the identical rule the serve path's lookupOverride
// keeps, so the export names exactly the operations the runtime patches.
func (c *composer) applyOverrides(rows map[string]*overrides.Row) {
	for _, row := range rows {
		if row == nil || !row.OverrideOn {
			continue
		}
		op := c.operationAt(row.Path, row.Method)
		if op == nil {
			continue // an orphaned override (the drift report's own signal) exports nothing
		}
		if row.RouteOff {
			op["deprecated"] = true
		}
		for status, v := range row.Responses {
			c.applyOverrideResponse(row, op, status, v)
		}
	}
}

func (c *composer) applyOverrideResponse(row *overrides.Row, op map[string]any, status string, v overrides.Variant) {
	responses, _ := op["responses"].(map[string]any)
	if responses == nil {
		return
	}
	respKey, ok := responseKeyFor(responses, status)
	if !ok {
		return
	}
	resp, ok := c.resolvedObject(responses, respKey)
	if !ok {
		return
	}
	content, _ := resp["content"].(map[string]any)
	if content == nil {
		return
	}
	mediaType := v.MediaType
	if mediaType == "" {
		mediaType = selectMediaType(content)
	}
	mto, ok := c.resolvedObject(content, mediaType)
	if !ok {
		return
	}

	if v.Mode == "pinned" {
		if example, ok := pinnedExample(v); ok {
			mto["examples"] = []any{example}
		}
		return
	}
	if len(v.SchemaPatch) == 0 {
		return
	}
	patch, err := jsonpatch.Parse(v.SchemaPatch)
	if err != nil || patch.Empty() {
		return
	}
	schemaNode, ok := mto["schema"]
	if !ok {
		return
	}
	resolved, err := c.resolver.ResolveNode(schemaNode)
	if err != nil {
		return
	}
	schema, ok := resolved.(map[string]any)
	if !ok {
		return
	}
	patched, err := jsonpatch.Apply(patch, schema)
	if err != nil {
		// The runtime serves this variant unpatched and logs once at
		// build (mockplane/overrides.go); the export writes the base's
		// schema for the same reason — the patch never took effect.
		return
	}
	// The runtime's buildPatchedSchemas drops the schema root's own
	// `examples` after a patch — a document example of the UNPATCHED shape
	// would be served over the patched one — and the export mirrors that
	// deletion, so a reader is not handed an example the mock never
	// answers. This IS a second apply site of the same primitive
	// (CARVE-OUTS.md, P7a): the runtime's lives in internal/mockplane,
	// which imports this package for Skeleton, so the two cannot share a
	// function without a cycle.
	delete(patched, "examples")
	mto["schema"] = patched
}

// responseKeyFor picks the response object a status selects: the exact
// key first, then the range and default selectors the specs indexer
// classifies onto the same HTTP status — classifySelector maps "2XX" AND
// "default" to 200 only, so neither answers for a 4xx/5xx status here
// either, or the export would patch a response the runtime never serves.
// false when the operation declares none of them.
func responseKeyFor(responses map[string]any, status string) (string, bool) {
	if _, ok := responses[status]; ok {
		return status, true
	}
	if !strings.HasPrefix(status, "2") {
		return "", false
	}
	if _, ok := responses["2XX"]; ok {
		return "2XX", true
	}
	if _, ok := responses["default"]; ok {
		return "default", true
	}
	return "", false
}

// resolvedObject returns m[key] as an object, following a `$ref` at that
// node through the base (a response or media-type object may itself be a
// reference into components), and — because the export MUTATES what it
// returns — replaces the reference with the resolved copy first, so the
// change lands on this operation alone and never on the shared component.
func (c *composer) resolvedObject(m map[string]any, key string) (map[string]any, bool) {
	node, ok := m[key]
	if !ok {
		return nil, false
	}
	if obj, isObj := node.(map[string]any); isObj {
		if _, isRef := obj["$ref"]; !isRef {
			return obj, true
		}
	}
	resolved, err := c.resolver.ResolveNode(node)
	if err != nil {
		return nil, false
	}
	obj, isObj := resolved.(map[string]any)
	if !isObj {
		return nil, false
	}
	copied, _ := jsonpatch.Apply(jsonpatch.Patch{}, obj)
	if copied == nil {
		return nil, false
	}
	m[key] = copied
	return copied, true
}

// selectMediaType is specs.SelectMediaType's rule, restated: the exact
// `application/json` key when the response declares it, else the first
// key in sorted order — so the export patches the SAME media-type object
// the runtime serves (the indexer picks a variant's MediaType by that
// rule). Restated rather than imported because internal/specs is a store
// package and this one is a leaf; a divergence here would silently patch
// `application/hal+json` while the mock serves `application/json`.
func selectMediaType(content map[string]any) string {
	if _, ok := content["application/json"]; ok {
		return "application/json"
	}
	keys := make([]string, 0, len(content))
	for k := range content {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	if len(keys) > 0 {
		return keys[0]
	}
	return "application/json"
}

// pinnedExample turns a pinned variant's body into an example value: a
// JSON body as its decoded value, a text body as a string, nothing for a
// base64 body or a bodyRef (bytes never travel, DESIGN §32.4).
func pinnedExample(v overrides.Variant) (any, bool) {
	if v.BodyRef != "" || v.BodyEncoding != "" || len(v.Body) == 0 {
		return nil, false
	}
	val, err := decodeJSON(v.Body)
	if err != nil {
		// Every writer validates Body as JSON, so this is a row written
		// behind them; exported as the text it is rather than dropped —
		// D7.2's `examples: ["<text>"]` for a body that is not JSON.
		return string(v.Body), true
	}
	return val, true
}

// applyEndpoints is rules 2, 3 and 6 plus the interview's additions: every
// custom row with OverrideOn becomes an operation under its OWN path
// spelling; a base operation at the same canonical shape is removed first
// (rule 3 read as intent — one entry, never two); an overrideOn:false row
// is omitted.
func (c *composer) applyEndpoints(rows []*customep.Row) error {
	for _, row := range rows {
		if row == nil || !row.OverrideOn {
			continue
		}
		c.removeCanonicalTwin(row)
		op, err := c.endpointOperation(row)
		if err != nil {
			return err
		}
		item, _ := c.paths[row.Path].(map[string]any)
		if item == nil {
			item = map[string]any{}
			c.paths[row.Path] = item
		}
		item[strings.ToLower(row.Method)] = op
	}
	return nil
}

// removeCanonicalTwin deletes the base operation whose canonical shape
// equals row's under a DIFFERENT spelling (`/users/{id}` for a row at
// `/users/{userId}`); the same spelling is simply overwritten by the
// caller. A path item left with no operation is dropped with it.
func (c *composer) removeCanonicalTwin(row *customep.Row) {
	canonical := row.CanonicalPath
	if canonical == "" {
		canonical = router.CanonicalPath(row.Path)
	}
	method := strings.ToLower(row.Method)
	for path, raw := range c.paths {
		if path == row.Path {
			continue
		}
		if router.CanonicalPath(path) != canonical {
			continue
		}
		item, _ := raw.(map[string]any)
		if item == nil {
			continue
		}
		if _, ok := item[method]; !ok {
			continue
		}
		delete(item, method)
		if !hasOperation(item) {
			delete(c.paths, path)
		}
	}
}

func hasOperation(item map[string]any) bool {
	for _, m := range httpMethods {
		if _, ok := item[m]; ok {
			return true
		}
	}
	return false
}

// endpointOperation renders one custom row as an operation object.
func (c *composer) endpointOperation(row *customep.Row) (map[string]any, error) {
	op := map[string]any{}
	meta := row.Operation
	if meta != nil {
		if meta.Summary != "" {
			op["summary"] = meta.Summary
		}
		if meta.Description != "" {
			op["description"] = meta.Description
		}
		if len(meta.Tags) > 0 {
			tags := make([]any, len(meta.Tags))
			for i, t := range meta.Tags {
				tags[i] = t
			}
			op["tags"] = tags
		}
		if meta.OperationID != "" {
			op["operationId"] = meta.OperationID
		}
		if meta.Deprecated {
			op["deprecated"] = true
		}
	}
	if row.RouteOff {
		op["deprecated"] = true
	}
	params, err := parametersFor(row)
	if err != nil {
		return nil, err
	}
	if len(params) > 0 {
		op["parameters"] = params
	}

	switch row.Kind {
	case customep.KindSSE:
		op["responses"] = map[string]any{
			"200": map[string]any{
				"description": "Server-sent event stream",
				"content":     map[string]any{"text/event-stream": map[string]any{}},
			},
		}
		return op, nil
	case customep.KindWS:
		op["x-websocket"] = true
		op["responses"] = map[string]any{
			"101": map[string]any{"description": "WebSocket upgrade"},
		}
		return op, nil
	}

	if len(row.ReqSchema) > 0 {
		schema, err := decodeJSON(row.ReqSchema)
		if err != nil {
			return nil, fmt.Errorf("%w: %s %s reqSchema: %w", ErrCompose, row.Method, row.Path, err)
		}
		op["requestBody"] = map[string]any{
			"required": true,
			"content":  map[string]any{"application/json": map[string]any{"schema": schema}},
		}
	}

	responses := map[string]any{}
	statuses := make([]string, 0, len(row.Responses))
	for status := range row.Responses {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	for _, status := range statuses {
		resp, err := endpointResponse(row, status, row.Responses[status])
		if err != nil {
			return nil, err
		}
		responses[status] = resp
	}
	active := strconv.Itoa(row.ActiveStatus)
	if _, ok := responses[active]; !ok {
		responses[active] = map[string]any{"description": statusDescription(row.ActiveStatus)}
	}
	op["responses"] = responses
	return op, nil
}

// parametersFor is the declared parameters plus a derived, required string
// parameter for every `{name}` segment the row's Operation does not
// declare — a path template with an undeclared parameter is not a valid
// OpenAPI document for most tools (decisions.md D3, the owner's "both").
func parametersFor(row *customep.Row) ([]any, error) {
	var out []any
	declared := map[string]bool{}
	if row.Operation != nil {
		for _, prm := range row.Operation.Parameters {
			p := map[string]any{"name": prm.Name, "in": prm.In}
			if prm.Required || prm.In == "path" {
				p["required"] = true
			}
			if prm.Description != "" {
				p["description"] = prm.Description
			}
			if len(prm.Schema) > 0 {
				schema, err := decodeJSON(prm.Schema)
				if err != nil {
					return nil, fmt.Errorf("%w: %s %s parameter %q schema: %w", ErrCompose, row.Method, row.Path, prm.Name, err)
				}
				p["schema"] = schema
			} else {
				p["schema"] = map[string]any{"type": "string"}
			}
			if prm.In == "path" {
				declared[prm.Name] = true
			}
			out = append(out, p)
		}
	}
	for _, seg := range strings.Split(row.Path, "/") {
		if len(seg) > 2 && seg[0] == '{' && seg[len(seg)-1] == '}' {
			name := seg[1 : len(seg)-1]
			if declared[name] {
				continue
			}
			declared[name] = true
			out = append(out, map[string]any{
				"name": name, "in": "path", "required": true,
				"schema": map[string]any{"type": "string"},
			})
		}
	}
	return out, nil
}

// endpointResponse renders one responses[status] entry: the schema when the
// variant declares one, the pinned body as an example, both when both. The
// description is the row's own summary when it has one (D7.2: "the summary
// or the status text"), the reason phrase otherwise.
func endpointResponse(row *customep.Row, status string, v overrides.Variant) (map[string]any, error) {
	code, _ := strconv.Atoi(status)
	description := statusDescription(code)
	if row.Operation != nil && row.Operation.Summary != "" {
		description = row.Operation.Summary
	}
	resp := map[string]any{"description": description}
	mediaType := v.MediaType
	if mediaType == "" {
		mediaType = "application/json"
	}
	mto := map[string]any{}
	if len(v.Schema) > 0 {
		schema, err := decodeJSON(v.Schema)
		if err != nil {
			return nil, fmt.Errorf("%w: %s %s responses[%s].schema: %w", ErrCompose, row.Method, row.Path, status, err)
		}
		mto["schema"] = schema
	}
	if v.Mode == "pinned" {
		if example, ok := pinnedExample(v); ok {
			mto["examples"] = []any{example}
		}
	}
	if len(mto) > 0 {
		resp["content"] = map[string]any{mediaType: mto}
	}
	return resp, nil
}

// statusDescription is the reason phrase an exported response object gets
// when the row carries no summary of its own — a `description` is required
// on every response object, so an empty one would make the document
// invalid for a reader that validates.
func statusDescription(code int) string {
	if text := statusText(code); text != "" {
		return text
	}
	return "Response " + strconv.Itoa(code)
}

func statusText(code int) string {
	switch code {
	case 200:
		return "OK"
	case 201:
		return "Created"
	case 202:
		return "Accepted"
	case 204:
		return "No Content"
	case 400:
		return "Bad Request"
	case 401:
		return "Unauthorized"
	case 403:
		return "Forbidden"
	case 404:
		return "Not Found"
	case 409:
		return "Conflict"
	case 422:
		return "Unprocessable Entity"
	case 500:
		return "Internal Server Error"
	}
	return ""
}

// stampVersion is rule 6 with the interview's strip: `-draft.<revision>`
// on info.version, an earlier `-draft.<n>` removed first.
func (c *composer) stampVersion(revision int64) {
	info, _ := c.root["info"].(map[string]any)
	if info == nil {
		// Load requires no info object; a base without one is titled the
		// way the skeleton is — with the workspace's own name.
		title := c.workspaceName
		if title == "" {
			title = "design"
		}
		info = map[string]any{"title": title}
		c.root["info"] = info
	}
	version, _ := info["version"].(string)
	if version == "" {
		version = "0.0.0"
	}
	version = draftSuffixRe.ReplaceAllString(version, "")
	info["version"] = version + "-draft." + strconv.FormatInt(revision, 10)
}
