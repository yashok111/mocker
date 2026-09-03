package gen

import (
	"encoding/json"
	"net/url"
	"strconv"
	"testing"
)

// widgetsDoc mirrors the acceptance document's own list/detail shape (the
// task brief's own fixture, GET /api/v1/bulletins + GET .../{bulletinId}):
// GET /widgets returns {items,total,limit,offset} wrapping an array of
// Widget; GET /widgets/{id} returns one Widget directly. Both share the
// SAME components/schemas entry via $ref — exactly like the real spec's
// list/detail pair — so a row and a card are generated from literally the
// same schema node, never two independently-drifted copies.
func widgetsDoc() map[string]any {
	doc := baseDoc()
	doc["components"] = map[string]any{
		"schemas": map[string]any{
			"Widget": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":     map[string]any{"type": "integer", "format": "uint"},
					"name":   map[string]any{"type": "string"},
					"status": map[string]any{"type": "string"},
				},
				"required": []any{"id", "name"},
			},
		},
	}
	doc["paths"] = map[string]any{
		"/widgets": map[string]any{
			"get": map[string]any{
				"parameters": []any{
					map[string]any{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer"}},
					map[string]any{"name": "offset", "in": "query", "schema": map[string]any{"type": "integer"}},
					map[string]any{"name": "status", "in": "query", "schema": map[string]any{"type": "string"}},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"items":  map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/Widget"}},
										"total":  map[string]any{"type": "integer"},
										"limit":  map[string]any{"type": "integer"},
										"offset": map[string]any{"type": "integer"},
									},
									"required": []any{"items", "total", "limit", "offset"},
								},
							},
						},
					},
				},
			},
		},
		"/widgets/{id}": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/Widget"},
							},
						},
					},
				},
			},
		},
	}
	return doc
}

func widgetsListVariant() ResponseVariant {
	return ResponseVariant{
		Selector:   "200",
		HTTPStatus: 200,
		MediaType:  "application/json",
		SchemaPtr:  "#/paths/~1widgets/get/responses/200/content/application~1json/schema",
		OpPointer:  "#/paths/~1widgets/get",
	}
}

func widgetsDetailVariant() ResponseVariant {
	return ResponseVariant{
		Selector:   "200",
		HTTPStatus: 200,
		MediaType:  "application/json",
		SchemaPtr:  "#/paths/~1widgets~1{id}/get/responses/200/content/application~1json/schema",
		OpPointer:  "#/paths/~1widgets~1{id}/get",
	}
}

// listPage calls g.Body for the GET /widgets list route with the given
// query and decodes the wrapper object.
func listPage(t *testing.T, g *Generator, v ResponseVariant, query url.Values) map[string]any {
	t.Helper()
	req := Request{Method: "GET", CanonicalPath: "/widgets", Status: 200, Query: query}
	b, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal list body: %v\nbody: %s", err, b)
	}
	return out
}

// --- rule 4: global index continuation ----------------------------------

func TestListBodyOffsetContinuation(t *testing.T) {
	res := buildResolver(t, widgetsDoc())
	g := New(res, Options{Seed: 42, ListSize: 20})
	v := widgetsListVariant()

	full := listPage(t, g, v, url.Values{"limit": {"10"}, "offset": {"0"}})
	fullItems, _ := full["items"].([]any)
	if len(fullItems) != 10 {
		t.Fatalf("expected 10 items in a single limit=10 page, got %d", len(fullItems))
	}

	page1 := listPage(t, g, v, url.Values{"limit": {"5"}, "offset": {"0"}})
	page2 := listPage(t, g, v, url.Values{"limit": {"5"}, "offset": {"5"}})
	p1Items, _ := page1["items"].([]any)
	p2Items, _ := page2["items"].([]any)
	if len(p1Items) != 5 || len(p2Items) != 5 {
		t.Fatalf("expected 5+5 items from the split pages, got %d+%d", len(p1Items), len(p2Items))
	}

	got := append(append([]any{}, p1Items...), p2Items...)
	for i := range fullItems {
		want, _ := json.Marshal(fullItems[i])
		gotB, _ := json.Marshal(got[i])
		if string(want) != string(gotB) {
			t.Fatalf("item at global index %d differs between one big page and offset-continued pages:\n  big page:  %s\n  continued: %s", i, want, gotB)
		}
	}
}

// coversDoc mirrors the real acceptance document's GET /api/v1/surveys/
// covers/custom shape — an {items,total,limit,offset} wrapper whose items
// are bare format:uri STRINGS, not objects — the exact shape that exposed
// P1b round-2 review, finding 1 in the real corpus (see
// TestListBodyWrapperFieldsSurviveTightBudget below). Scalar items matter
// here, not just a smaller schema: a plain string item never recurses
// through walkObject's own required-property accounting, so generateItems'
// per-item spend() calls are the ONLY thing standing between a large
// ListSize and running the shared budget down to a razor-thin single-digit
// remainder — exactly the granularity that exposes the missing reservation.
func coversDoc() map[string]any {
	doc := baseDoc()
	doc["paths"] = map[string]any{
		"/covers": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"items":  map[string]any{"type": "array", "items": map[string]any{"type": "string", "format": "uri"}},
										"total":  map[string]any{"type": "integer"},
										"limit":  map[string]any{"type": "integer"},
										"offset": map[string]any{"type": "integer"},
									},
									"required": []any{"items", "total", "limit", "offset"},
								},
							},
						},
					},
				},
			},
		},
	}
	return doc
}

// TestListBodyWrapperFieldsSurviveTightBudget is a regression test for P1b
// round-2 review, finding 1: listPageBody used to run generateItems (filling
// "items" up to a large ListSize) BEFORE buildWrapper ever got a turn to
// walk the wrapper's OTHER required properties (total/limit/offset) — so
// generateItems, having no visibility at all into what the wrapper still
// owed, could legitimately spend the shared budget down to a single-digit
// remainder on items alone. buildWrapper's own walk then found far too
// little left for total/limit/offset — required properties that forceSpend
// regardless of how negative that leaves things — pushing the final body
// over Options.MaxBytes even though the wrapper's own minimal required
// content was only a few dozen bytes. Reproduced against the exact budget
// (4096) the real acceptance corpus uses: without listPageBody reserving
// room for the wrapper before generateItems runs, this body comes back
// over budget with total/limit/offset still present but the response as a
// whole 13 bytes too large.
func TestListBodyWrapperFieldsSurviveTightBudget(t *testing.T) {
	res := buildResolver(t, coversDoc())
	const tightMaxBytes = 4096
	g := New(res, Options{Seed: 7, ListSize: 500, MaxBytes: tightMaxBytes})
	v := ResponseVariant{
		Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1covers/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1covers/get",
	}
	req := Request{Method: "GET", CanonicalPath: "/covers", Status: 200, Query: url.Values{"limit": {"500"}, "offset": {"0"}}}

	b, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatalf("unmarshal wrapper: %v\nbody: %s", err, b)
	}
	for _, field := range []string{"total", "limit", "offset"} {
		if _, ok := out[field]; !ok {
			t.Errorf("wrapper missing required field %q — budget starved by the items array before the wrapper's own walk ran", field)
		}
	}
	if len(b) > tightMaxBytes {
		t.Errorf("list wrapper body = %d bytes, exceeds Options.MaxBytes (%d)", len(b), tightMaxBytes)
	}
}

// --- rule 3: total is stable and independent of page length ------------

func TestListBodyStableTotalAcrossPages(t *testing.T) {
	res := buildResolver(t, widgetsDoc())
	g := New(res, Options{Seed: 1, ListSize: 17})
	v := widgetsListVariant()

	pages := []map[string]any{
		listPage(t, g, v, url.Values{"limit": {"5"}, "offset": {"0"}}),
		listPage(t, g, v, url.Values{"limit": {"3"}, "offset": {"10"}}),
		listPage(t, g, v, url.Values{}),
	}
	for i, p := range pages {
		if total, ok := p["total"].(float64); !ok || total != 17 {
			t.Fatalf("page %d: expected total=17 (Options.ListSize), got %v", i, p["total"])
		}
	}
	if lim, _ := pages[0]["limit"].(float64); lim != 5 {
		t.Fatalf("expected limit echoed back as 5, got %v", pages[0]["limit"])
	}
	if off, _ := pages[1]["offset"].(float64); off != 10 {
		t.Fatalf("expected offset echoed back as 10, got %v", pages[1]["offset"])
	}
}

// --- rule 5: list row == detail card ------------------------------------

func TestListBodyRowEqualsDetailCard(t *testing.T) {
	res := buildResolver(t, widgetsDoc())
	g := New(res, Options{Seed: 99, ListSize: 20})
	lv := widgetsListVariant()
	dv := widgetsDetailVariant()

	page := listPage(t, g, lv, url.Values{"limit": {"20"}, "offset": {"0"}})
	items, _ := page["items"].([]any)
	if len(items) < 4 {
		t.Fatalf("expected at least 4 items, got %d", len(items))
	}
	row, ok := items[3].(map[string]any) // a non-zero index, not just the trivial first row
	if !ok {
		t.Fatalf("row is not an object: %#v", items[3])
	}
	idFloat, ok := row["id"].(float64)
	if !ok {
		t.Fatalf("row id is not a JSON number: %#v", row["id"])
	}
	idStr := strconv.FormatInt(int64(idFloat), 10)

	dreq := Request{
		Method:        "GET",
		CanonicalPath: "/widgets/{}",
		PathParams:    map[string]string{"id": idStr},
		Status:        200,
		ListFamily:    "/widgets",
	}
	b, err := g.Body(dv, dreq)
	if err != nil {
		t.Fatalf("Body (detail): %v", err)
	}
	var card map[string]any
	if err := json.Unmarshal(b, &card); err != nil {
		t.Fatalf("unmarshal card: %v", err)
	}

	rowB, _ := json.Marshal(row)
	cardB, _ := json.Marshal(card)
	if string(rowB) != string(cardB) {
		t.Fatalf("list row != detail card for id %s:\n  row:  %s\n  card: %s", idStr, rowB, cardB)
	}
}

// deepWidgetsDoc mirrors widgetsDoc's list/detail shape, but Widget itself
// nests a required object three levels deep (nested.value.code) — deep
// enough that a small MaxDepth exercises the depth ceiling on exactly one
// of the two paths if they don't reset symmetrically.
func deepWidgetsDoc() map[string]any {
	doc := baseDoc()
	doc["components"] = map[string]any{
		"schemas": map[string]any{
			"DeepWidget": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id":   map[string]any{"type": "integer", "format": "uint"},
					"name": map[string]any{"type": "string"},
					"nested": map[string]any{
						"type":     "object",
						"required": []any{"value"},
						"properties": map[string]any{
							"value": map[string]any{
								"type":     "object",
								"required": []any{"code"},
								"properties": map[string]any{
									"code": map[string]any{"type": "string"},
								},
							},
						},
					},
				},
				"required": []any{"id", "name", "nested"},
			},
		},
	}
	doc["paths"] = map[string]any{
		"/deep-widgets": map[string]any{
			"get": map[string]any{
				"parameters": []any{
					map[string]any{"name": "limit", "in": "query", "schema": map[string]any{"type": "integer"}},
					map[string]any{"name": "offset", "in": "query", "schema": map[string]any{"type": "integer"}},
				},
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "object",
									"properties": map[string]any{
										"items":  map[string]any{"type": "array", "items": map[string]any{"$ref": "#/components/schemas/DeepWidget"}},
										"total":  map[string]any{"type": "integer"},
										"limit":  map[string]any{"type": "integer"},
										"offset": map[string]any{"type": "integer"},
									},
									"required": []any{"items", "total", "limit", "offset"},
								},
							},
						},
					},
				},
			},
		},
		"/deep-widgets/{id}": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{"$ref": "#/components/schemas/DeepWidget"},
							},
						},
					},
				},
			},
		},
	}
	return doc
}

// TestListBodyRowEqualsDetailCardNearDepthCeiling is the P1b round-1
// review's finding 6: generateItems used to walk a list row starting at
// the wrapper's own itemDepth (2 for this wrapper-object shape) while
// detailBody always started the SAME item schema at depth 0 — the
// identical item schema then got a DIFFERENT depth budget depending on
// which route reached it, so a field sitting within itemDepth levels of
// MaxDepth could clear the ceiling on one side (real values) and hit it on
// the other (an empty depth-ceiling stub), breaking row==card even though
// both sides used the identical item seed. MaxDepth=4 here puts
// nested.value.code (item-root depth 3) safely under the ceiling from a
// depth-0 start but over it from the old itemDepth=2 start (2+3=5 > 4) —
// exactly the gap the fix closes.
func TestListBodyRowEqualsDetailCardNearDepthCeiling(t *testing.T) {
	res := buildResolver(t, deepWidgetsDoc())
	g := New(res, Options{Seed: 7, ListSize: 5, MaxDepth: 4})
	lv := ResponseVariant{
		Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1deep-widgets/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1deep-widgets/get",
	}
	dv := ResponseVariant{
		Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1deep-widgets~1{id}/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1deep-widgets~1{id}/get",
	}

	// NOT listPage — that helper hardcodes CanonicalPath "/widgets", which
	// would desync this route's own SeedList from what deepWidgetsDoc's
	// "/deep-widgets" path (and the detail request's ListFamily, below)
	// actually use.
	lreq := Request{
		Method: "GET", CanonicalPath: "/deep-widgets", Status: 200,
		Query: url.Values{"limit": {"5"}, "offset": {"0"}},
	}
	lb, err := g.Body(lv, lreq)
	if err != nil {
		t.Fatalf("Body (list): %v", err)
	}
	var page map[string]any
	if err := json.Unmarshal(lb, &page); err != nil {
		t.Fatalf("unmarshal list body: %v\nbody: %s", err, lb)
	}
	items, _ := page["items"].([]any)
	if len(items) == 0 {
		t.Fatalf("expected at least 1 item, got 0: %#v", page)
	}
	row, ok := items[0].(map[string]any)
	if !ok {
		t.Fatalf("row is not an object: %#v", items[0])
	}
	nested, ok := row["nested"].(map[string]any)
	if !ok {
		t.Fatalf("row.nested is not an object (or is missing): %#v", row)
	}
	value, ok := nested["value"].(map[string]any)
	if !ok {
		t.Fatalf("row.nested.value is not an object (or is missing) — it should still clear MaxDepth=4 from a depth-0 item start: %#v", nested)
	}
	if _, present := value["code"]; !present {
		t.Fatalf("row.nested.value.code is missing — the list row hit the depth ceiling one level earlier than the detail card would (finding 6's exact regression): %#v", value)
	}

	idFloat, ok := row["id"].(float64)
	if !ok {
		t.Fatalf("row id is not a JSON number: %#v", row["id"])
	}
	idStr := strconv.FormatInt(int64(idFloat), 10)

	dreq := Request{
		Method: "GET", CanonicalPath: "/deep-widgets/{}",
		PathParams: map[string]string{"id": idStr}, Status: 200,
		ListFamily: "/deep-widgets",
	}
	b, err := g.Body(dv, dreq)
	if err != nil {
		t.Fatalf("Body (detail): %v", err)
	}
	var card map[string]any
	if err := json.Unmarshal(b, &card); err != nil {
		t.Fatalf("unmarshal card: %v", err)
	}

	rowB, _ := json.Marshal(row)
	cardB, _ := json.Marshal(card)
	if string(rowB) != string(cardB) {
		t.Fatalf("list row != detail card for id %s near the depth ceiling:\n  row:  %s\n  card: %s", idStr, rowB, cardB)
	}
}

// --- the launch identity decision: id is written in, and typed -----------

func TestListBodyDetailIDCoercedToSchemaType(t *testing.T) {
	res := buildResolver(t, widgetsDoc())
	g := New(res, Options{Seed: 5})
	dv := widgetsDetailVariant()
	req := Request{
		Method:        "GET",
		CanonicalPath: "/widgets/{}",
		PathParams:    map[string]string{"id": "42"},
		Status:        200,
		ListFamily:    "/widgets",
	}

	b, err := g.Body(dv, req)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	var card map[string]any
	if err := json.Unmarshal(b, &card); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, isString := card["id"].(string); isString {
		t.Fatalf(`id was written as a JSON string ("42"), want the integer 42: %s`, b)
	}
	if got, _ := card["id"].(float64); got != 42 {
		t.Fatalf("expected id=42, got %v (body: %s)", card["id"], b)
	}
}

// --- rule 6: postfilter + top-up -----------------------------------------

func TestListBodyPostfilterMatchesAndTopsUp(t *testing.T) {
	res := buildResolver(t, widgetsDoc())
	g := New(res, Options{Seed: 123, ListSize: 40})
	v := widgetsListVariant()

	unfiltered := listPage(t, g, v, url.Values{"limit": {"40"}, "offset": {"0"}})
	items, _ := unfiltered["items"].([]any)
	counts := map[string]int{}
	for _, it := range items {
		obj := it.(map[string]any)
		counts[obj["status"].(string)]++
	}
	var target string
	var targetCount int
	for status, n := range counts {
		if n >= 2 {
			target, targetCount = status, n
			break
		}
	}
	if target == "" {
		t.Fatalf("fixture produced no status value with >=2 occurrences across %d items (counts=%v); adjust seed/ListSize", len(items), counts)
	}

	filtered := listPage(t, g, v, url.Values{"limit": {"3"}, "offset": {"0"}, "status": {target}})
	fItems, _ := filtered["items"].([]any)
	want := min(targetCount, 3)
	if len(fItems) != want {
		t.Fatalf("expected %d filtered items (limit 3, %d total matches), got %d", want, targetCount, len(fItems))
	}
	for _, it := range fItems {
		obj := it.(map[string]any)
		if obj["status"].(string) != target {
			t.Fatalf("postfilter leaked a non-matching item: %#v", obj)
		}
	}
	if total, _ := filtered["total"].(float64); total != 40 {
		t.Fatalf("total must stay 40 even under a filter, got %v", filtered["total"])
	}
}

func TestListBodyPostfilterEmptyForImpossibleMatch(t *testing.T) {
	res := buildResolver(t, widgetsDoc())
	g := New(res, Options{Seed: 7, ListSize: 20})
	v := widgetsListVariant()

	page := listPage(t, g, v, url.Values{"limit": {"10"}, "status": {"no-such-status-value"}})
	items, _ := page["items"].([]any)
	if len(items) != 0 {
		t.Fatalf("expected an empty page for a filter that matches nothing, got %d items", len(items))
	}
}

func TestListBodyIgnoresUndeclaredQueryParam(t *testing.T) {
	res := buildResolver(t, widgetsDoc())
	g := New(res, Options{Seed: 3, ListSize: 20})
	v := widgetsListVariant()

	// "name" is an item property but NOT declared as a query parameter on
	// GET /widgets — rule 6 says only spec-declared parameters filter.
	base := listPage(t, g, v, url.Values{"limit": {"10"}})
	withUnknown := listPage(t, g, v, url.Values{"limit": {"10"}, "name": {"definitely-not-a-generated-name"}})

	baseB, _ := json.Marshal(base["items"])
	unkB, _ := json.Marshal(withUnknown["items"])
	if string(baseB) != string(unkB) {
		t.Fatalf("an undeclared query parameter changed the generated page:\n  base:    %s\n  with ?name=...: %s", baseB, unkB)
	}
}

// --- rule 2: clamp against a hostile page size ---------------------------

func TestListBodyClampsHostileLimit(t *testing.T) {
	res := buildResolver(t, widgetsDoc())
	g := New(res, Options{Seed: 1, ListSize: 20})
	v := widgetsListVariant()

	page := listPage(t, g, v, url.Values{"limit": {"1000000"}})
	items, _ := page["items"].([]any)
	if len(items) > maxListPageLimit {
		t.Fatalf("hostile ?limit=1000000 was not clamped: got %d items (hard cap %d)", len(items), maxListPageLimit)
	}
	if len(items) > 20 {
		t.Fatalf("got more items (%d) than the configured universe size (ListSize=20)", len(items))
	}
}

// --- rule 2: limit/offset source priority --------------------------------

func TestListBodyPaginationParamPriority(t *testing.T) {
	res := buildResolver(t, widgetsDoc())
	g := New(res, Options{Seed: 1, ListSize: 20})
	v := widgetsListVariant()

	cases := []struct {
		name       string
		query      url.Values
		wantLimit  float64
		wantOffset float64
	}{
		{"limit wins over per_page and size", url.Values{"limit": {"4"}, "per_page": {"9"}, "size": {"11"}}, 4, 0},
		{"per_page used when limit absent", url.Values{"per_page": {"6"}}, 6, 0},
		{"size used when limit and per_page absent", url.Values{"size": {"7"}}, 7, 0},
		{"offset wins over page and cursor", url.Values{"limit": {"5"}, "offset": {"3"}, "page": {"9"}}, 5, 3},
		{"page computes offset from the resolved limit", url.Values{"limit": {"5"}, "page": {"3"}}, 5, 10},
		{"cursor used when offset and page absent", url.Values{"limit": {"5"}, "cursor": {"8"}}, 5, 8},
		{"unparseable limit falls back to Options.ListSize", url.Values{"limit": {"not-a-number"}}, 20, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			page := listPage(t, g, v, tc.query)
			if lim, _ := page["limit"].(float64); lim != tc.wantLimit {
				t.Fatalf("limit = %v, want %v", page["limit"], tc.wantLimit)
			}
			if off, _ := page["offset"].(float64); off != tc.wantOffset {
				t.Fatalf("offset = %v, want %v", page["offset"], tc.wantOffset)
			}
		})
	}
}

// --- determinism (DESIGN §9's headline invariant, for the list path too) -

func TestListBodyDeterministicAcrossCalls(t *testing.T) {
	res := buildResolver(t, widgetsDoc())
	g := New(res, Options{Seed: 55, ListSize: 12})
	v := widgetsListVariant()
	req := Request{Method: "GET", CanonicalPath: "/widgets", Status: 200, Query: url.Values{"limit": {"6"}, "offset": {"2"}}}

	b1, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body (1): %v", err)
	}
	b2, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body (2): %v", err)
	}
	if string(b1) != string(b2) {
		t.Fatalf("two identical list requests produced different bodies:\n%s\n%s", b1, b2)
	}
}

// --- rule 1: a bare top-level array is also a list ------------------------

func TestListBodyTopLevelArray(t *testing.T) {
	doc := baseDoc()
	doc["paths"] = map[string]any{
		"/gadgets": map[string]any{
			"get": map[string]any{
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema": map[string]any{
									"type": "array",
									"items": map[string]any{
										"type": "object",
										"properties": map[string]any{
											"id":   map[string]any{"type": "integer"},
											"name": map[string]any{"type": "string"},
										},
										"required": []any{"id", "name"},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	res := buildResolver(t, doc)
	g := New(res, Options{Seed: 1, ListSize: 6})
	v := ResponseVariant{
		Selector:  "200",
		MediaType: "application/json",
		SchemaPtr: "#/paths/~1gadgets/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1gadgets/get",
	}
	req := Request{Method: "GET", CanonicalPath: "/gadgets", Status: 200, Query: url.Values{"limit": {"4"}}}

	b, err := g.Body(v, req)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	var items []any
	if err := json.Unmarshal(b, &items); err != nil {
		t.Fatalf("expected a bare JSON array body, got %s (unmarshal err: %v)", b, err)
	}
	if len(items) != 4 {
		t.Fatalf("expected 4 items, got %d", len(items))
	}
}

// --- detectListShape: rule 1's detection logic in isolation --------------

func TestDetectListShape(t *testing.T) {
	w := newTestWalker(Options{Seed: 1}, nil)

	t.Run("top-level array", func(t *testing.T) {
		schema := map[string]any{"type": "array", "items": map[string]any{"type": "string"}}
		shape, ok := detectListShape(w, schema)
		if !ok || !shape.isTopLevelArray {
			t.Fatalf("expected a top-level array shape, got %#v ok=%v", shape, ok)
		}
	})

	t.Run("single array property", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"widgets": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"total":   map[string]any{"type": "integer"},
			},
		}
		shape, ok := detectListShape(w, schema)
		if !ok || shape.arrName != "widgets" || shape.isTopLevelArray {
			t.Fatalf("expected arrName=widgets, got %#v ok=%v", shape, ok)
		}
	})

	t.Run("multiple candidates prefers a canonical name", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"items": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"extra": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		}
		shape, ok := detectListShape(w, schema)
		if !ok || shape.arrName != "items" {
			t.Fatalf("expected the preferred name 'items' to win, got %#v ok=%v", shape, ok)
		}
	})

	t.Run("multiple candidates without a canonical name declines", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"foo": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"bar": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			},
		}
		if _, ok := detectListShape(w, schema); ok {
			t.Fatalf("expected an ambiguous multi-array object (no canonical name) to decline")
		}
	})

	t.Run("no array property declines", func(t *testing.T) {
		schema := map[string]any{
			"type": "object",
			"properties": map[string]any{
				"id":   map[string]any{"type": "integer"},
				"name": map[string]any{"type": "string"},
			},
		}
		if _, ok := detectListShape(w, schema); ok {
			t.Fatalf("expected a plain (non-list) object to decline list detection")
		}
	})
}

// --- coerceID: the detail-card id type-coercion helper in isolation ------

func TestCoerceID(t *testing.T) {
	tests := []struct {
		name   string
		raw    string
		schema map[string]any
		want   any
	}{
		{"integer parses", "42", map[string]any{"type": "integer"}, int64(42)},
		{"number parses", "3.5", map[string]any{"type": "number"}, 3.5},
		{"boolean parses", "true", map[string]any{"type": "boolean"}, true},
		{"string passthrough", "abc-123", map[string]any{"type": "string"}, "abc-123"},
		{"nil schema passes the raw string through", "abc-123", nil, "abc-123"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := coerceID(tc.raw, tc.schema)
			if got != tc.want {
				t.Fatalf("coerceID(%q, %v) = %#v, want %#v", tc.raw, tc.schema, got, tc.want)
			}
		})
	}

	t.Run("unparseable integer falls back deterministically, never the raw string", func(t *testing.T) {
		schema := map[string]any{"type": "integer"}
		got1 := coerceID("not-a-number", schema)
		got2 := coerceID("not-a-number", schema)
		if got1 != got2 {
			t.Fatalf("coerceID fallback is not deterministic: %v vs %v", got1, got2)
		}
		if _, ok := got1.(int64); !ok {
			t.Fatalf("expected an int64 fallback for an integer-typed id schema, got %T (%v)", got1, got1)
		}
	})
}

// TestCoerceIDValue exercises the exported, resolver-free helper directly —
// [TestCoerceID] above already proves coerceID is a thin wrapper over this
// (same table, same results), so this test's own job is the fourth branch
// (a schema-less/absent idType) and the format-carrying integer fallback
// that a signature without format would silently drop.
func TestCoerceIDValue(t *testing.T) {
	tests := []struct {
		name        string
		raw, idType string
		format      string
		want        any
	}{
		{"integer parses", "42", "integer", "", int64(42)},
		{"number parses", "3.5", "number", "", 3.5},
		{"boolean parses", "true", "boolean", "", true},
		{"string passthrough", "abc-123", "string", "", "abc-123"},
		{"empty idType passes raw through, same as no schema", "abc-123", "", "", "abc-123"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CoerceIDValue(tc.raw, tc.idType, tc.format)
			if got != tc.want {
				t.Fatalf("CoerceIDValue(%q, %q, %q) = %#v, want %#v", tc.raw, tc.idType, tc.format, got, tc.want)
			}
		})
	}

	t.Run("format reaches the integer fallback", func(t *testing.T) {
		// int64/uint64/uint keep a wider range than the int32 default
		// (idInteger's own branch) — the only way to observe format was
		// actually threaded through is to compare the two ranges against
		// the SAME unparseable raw value.
		const raw = "not-a-number"
		narrow := CoerceIDValue(raw, "integer", "")
		wide := CoerceIDValue(raw, "integer", "int64")
		nv, ok := narrow.(int64)
		if !ok {
			t.Fatalf("expected int64 fallback, got %T", narrow)
		}
		wv, ok := wide.(int64)
		if !ok {
			t.Fatalf("expected int64 fallback, got %T", wide)
		}
		if nv >= (1 << 31) {
			t.Fatalf("default-format fallback %d exceeds the int32-ish 31-bit range idInteger promises for \"\"", nv)
		}
		// The int64 format's own range is (1<<40); demonstrating it is NOT
		// clamped to the narrower default range is enough to prove format
		// changed the computation without duplicating idInteger's math here.
		if wv < (1<<31) && wv == nv {
			t.Fatalf("int64-format fallback %d is identical to the default-format one %d; format was not threaded through", wv, nv)
		}
	})
}

// TestCountValue is the switch CLAUDE.md/D3 states verbatim: "string" ->
// strconv.Itoa, "number" -> float64, everything else INCLUDING "" ->
// int64. The "" case matters most: it is what a resource with an
// unresolved or untyped count property stores, and it must NOT be treated
// like "string" (SchemaType's own untyped-leaf fallback) or the wire type
// would flip between the generator's wrapper and a confirmed resource's.
func TestCountValue(t *testing.T) {
	tests := []struct {
		countType string
		want      any
	}{
		{"string", "7"},
		{"number", float64(7)},
		{"integer", int64(7)},
		{"", int64(7)},
		{"boolean", int64(7)},
	}
	for _, tc := range tests {
		if got := CountValue(7, tc.countType); got != tc.want {
			t.Errorf("CountValue(7, %q) = %#v, want %#v", tc.countType, got, tc.want)
		}
	}
}

// TestIsTotalFieldName pins the four accepted spellings and proves the
// comparison is lower-case-literal-only, per its own parameter name
// (lname): a mixed-case name that a caller forgot to lower-case first must
// answer false rather than silently matching anyway, because a silent match
// here would hide the exact bug the parameter's name warns against.
func TestIsTotalFieldName(t *testing.T) {
	accepted := []string{"total", "count", "total_count", "totalcount"}
	for _, name := range accepted {
		if !IsTotalFieldName(name) {
			t.Errorf("IsTotalFieldName(%q) = false, want true", name)
		}
	}

	rejected := []string{"", "items", "totalCount", "TOTAL", "grandTotal", "counter"}
	for _, name := range rejected {
		if IsTotalFieldName(name) {
			t.Errorf("IsTotalFieldName(%q) = true, want false (not lower-cased, or not one of the four spellings)", name)
		}
	}
}

// --- identifyIDParam: which path parameter is "the id" -------------------

func TestIdentifyIDParam(t *testing.T) {
	t.Run("single param is unambiguous", func(t *testing.T) {
		req := Request{PathParams: map[string]string{"bulletinId": "42"}}
		name, val, ok := identifyIDParam(req)
		if !ok || name != "bulletinId" || val != "42" {
			t.Fatalf("got name=%q val=%q ok=%v, want bulletinId/42/true", name, val, ok)
		}
	})

	t.Run("zero params declines", func(t *testing.T) {
		if _, _, ok := identifyIDParam(Request{}); ok {
			t.Fatalf("expected decline with zero path params")
		}
	})

	t.Run("multiple params: the single id-shaped name wins", func(t *testing.T) {
		req := Request{PathParams: map[string]string{"org": "5", "bulletinId": "42"}}
		name, val, ok := identifyIDParam(req)
		if !ok || name != "bulletinId" || val != "42" {
			t.Fatalf("got name=%q val=%q ok=%v, want bulletinId/42/true", name, val, ok)
		}
	})

	t.Run("multiple id-shaped names without IDParam: the tie-break is at least deterministic", func(t *testing.T) {
		req := Request{PathParams: map[string]string{"orgId": "5", "bulletinId": "42"}}
		name1, val1, ok1 := identifyIDParam(req)
		name2, val2, ok2 := identifyIDParam(req)
		if !ok1 || !ok2 || name1 != name2 || val1 != val2 {
			t.Fatalf("expected a stable pick across calls: (%q,%q,%v) vs (%q,%q,%v)", name1, val1, ok1, name2, val2, ok2)
		}
	})

	// The finding this test guards: GET
	// /api/v1/tenants/{tenantId}/cohorts/{cohortId} has TWO
	// path parameters that both look id-shaped by name (both end in
	// "Id"). Without an explicit IDParam, sort.Strings(["cohortId",
	// "tenantId"]) picks "tenantId" last — the WRONG
	// parameter, an outer/shared one, not this route's own resource id.
	// Request.IDParam, set by the caller from the router's real ordered
	// pattern, must override that heuristic outright.
	t.Run("IDParam overrides the ambiguous multi-id-shaped-name heuristic", func(t *testing.T) {
		req := Request{
			PathParams: map[string]string{"tenantId": "5", "cohortId": "42"},
			IDParam:    "cohortId",
		}
		name, val, ok := identifyIDParam(req)
		if !ok || name != "cohortId" || val != "42" {
			t.Fatalf("got name=%q val=%q ok=%v, want cohortId/42/true", name, val, ok)
		}
	})

	t.Run("IDParam naming a parameter absent from PathParams falls back to the heuristic", func(t *testing.T) {
		req := Request{
			PathParams: map[string]string{"bulletinId": "42"},
			IDParam:    "doesNotExist",
		}
		name, val, ok := identifyIDParam(req)
		if !ok || name != "bulletinId" || val != "42" {
			t.Fatalf("got name=%q val=%q ok=%v, want bulletinId/42/true", name, val, ok)
		}
	})
}
