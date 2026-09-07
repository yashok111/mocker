// respond_core_test.go: Plane.serveGenerated's own basic contract —
// variant choice, Accept negotiation, the envelope, 204/HEAD suppression,
// the list-row/detail-card identity DESIGN §9 promises, content-length,
// non-JSON media types, route-off/override-off, concurrency and the
// write-failure-is-logged-not-panicked path. The two response MODES' own
// serve-time gates live in respond_pinned_test.go and
// respond_generated_test.go instead. Split out of the former
// respond_test.go (package mockplane, not mockplane_test — see
// helpers_test.go's own comment for why).
package mockplane

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
)

// TestServeGenerated_VariantChoice is TestChooseVariant's end-to-end twin:
// the same DESIGN §7 step 5 cases, now proven through the real HTTP status
// serveGenerated actually writes, across a spread the task brief names by
// name: {200, 201, 204, default}.
func TestServeGenerated_VariantChoice(t *testing.T) {
	v := func(sel string, status int) gen.ResponseVariant {
		return gen.ResponseVariant{OpRowID: 1, Selector: sel, HTTPStatus: status}
	}
	route := router.Route{OpRowID: 1, Method: "GET", Path: "/items", CanonicalPath: "/items", SourceOrder: 1}

	tests := []struct {
		name       string
		variants   []gen.ResponseVariant
		wantStatus int
	}{
		{"201 beats a higher numeric 2xx and default", []gen.ResponseVariant{v("201", 201), v("204", 204), v("default", 200)}, 201},
		{"a bare 204 wins when nothing lower exists", []gen.ResponseVariant{v("204", 204), v("default", 200)}, 204},
		{"2XX wins over default when no numeric 2xx exists", []gen.ResponseVariant{v("2XX", 200), v("default", 200)}, 200},
		{"default alone", []gen.ResponseVariant{v("default", 200)}, 200},
	}

	p := respondTestPlane()
	ws := respondTestWorkspace()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := fixtureRuntime(t, blankDoc, []router.Route{route},
				map[int64][]gen.ResponseVariant{1: tt.variants}, domain.DefaultSettings())
			m := mustMatch(t, rt, "GET", "/items")

			req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, req, ws, rt, m, resources.ScopeKey(""))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body)
			}
		})
	}
}

// TestServeGenerated_NoVariants proves "the route exists, the document just
// declared nothing" answers 200 with no body rather than 500.
func TestServeGenerated_NoVariants(t *testing.T) {
	route := router.Route{OpRowID: 1, Method: "GET", Path: "/nothing", CanonicalPath: "/nothing", SourceOrder: 1}
	rt := fixtureRuntime(t, blankDoc, []router.Route{route}, map[int64][]gen.ResponseVariant{}, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/nothing")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/nothing", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

// TestServeGenerated_Degraded proves an operation the indexer could not
// parse always answers an empty 200 (DESIGN §7), regardless of whatever
// status or content the variant row otherwise claims.
func TestServeGenerated_Degraded(t *testing.T) {
	route := router.Route{OpRowID: 1, Method: "GET", Path: "/broken", CanonicalPath: "/broken", SourceOrder: 1}
	variants := map[int64][]gen.ResponseVariant{
		1: {{OpRowID: 1, Selector: "201", HTTPStatus: 201, MediaType: "application/json", Degraded: true}},
	}
	rt := fixtureRuntime(t, blankDoc, []router.Route{route}, variants, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/broken")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/broken", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (degraded always answers 200, never the variant's own 201)", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

// TestServeGenerated_406OnIncompatibleAccept and its accept twin cover
// DESIGN §9's "явный fallback и 406" at the real HTTP layer: an Accept that
// excludes the declared type gets 406 with mocker's own standard error
// body, never a 200 carrying the wrong Content-Type.
func TestServeGenerated_406OnIncompatibleAccept(t *testing.T) {
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	req.Header.Set("Accept", "text/plain")
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusNotAcceptable {
		t.Fatalf("status = %d, want 406; body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 406 body: %v; body=%s", err, rec.Body)
	}
	if body.Error.Code != "not_acceptable" {
		t.Errorf("error.code = %q, want not_acceptable", body.Error.Code)
	}
}

func TestServeGenerated_AcceptCompatibleSucceeds(t *testing.T) {
	tests := []string{"", "*/*", "application/json", "application/*", "application/json;q=0.5, text/plain;q=0.1"}
	for _, accept := range tests {
		t.Run("Accept="+accept, func(t *testing.T) {
			rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
				map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings())
			m := mustMatch(t, rt, "GET", "/items")

			p := respondTestPlane()
			req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
			if accept != "" {
				req.Header.Set("Accept", accept)
			}
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
			}
		})
	}
}

// TestServeGenerated_EnvelopeWrapsWhenSet and its siblings cover DESIGN's
// {"<envelope>": ...} wrap: applied when configured and there is a body,
// never applied when unset, and never applied over an empty body.
func TestServeGenerated_EnvelopeWrapsWhenSet(t *testing.T) {
	settings := domain.DefaultSettings()
	envelope := "data"
	settings.Envelope = &envelope
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var wrapped struct {
		Data struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrapped); err != nil {
		t.Fatalf("decode enveloped body: %v; body=%s", err, rec.Body)
	}
	if wrapped.Data.Name == "" {
		t.Errorf("wrapped.data.name is empty, want the generated field to have survived the wrap: %s", rec.Body)
	}
}

func TestServeGenerated_NoEnvelopeWhenUnset(t *testing.T) {
	settings := domain.DefaultSettings() // Envelope is nil by default
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, rec.Body)
	}
	if _, wrapped := body["data"]; wrapped {
		t.Errorf("body = %s, want the bare object, not wrapped under any envelope key", rec.Body)
	}
	if _, ok := body["id"]; !ok {
		t.Errorf("body = %s, want the generated \"id\" field directly at the top level", rec.Body)
	}
}

// TestServeGenerated_EnvelopeNotAppliedToEmptyBody proves item 6's "not
// applied to an empty body": a 204 (never a body, DESIGN §9) with an
// envelope configured must still come back with a truly empty wire body,
// never something like {"data":null}.
func TestServeGenerated_EnvelopeNotAppliedToEmptyBody(t *testing.T) {
	settings := domain.DefaultSettings()
	envelope := "data"
	settings.Envelope = &envelope
	variants := map[int64][]gen.ResponseVariant{
		1: {{OpRowID: 1, Selector: "204", HTTPStatus: 204}},
	}
	rt := fixtureRuntime(t, blankDoc, []router.Route{itemRoute()}, variants, settings)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want truly empty, not an envelope wrapper around nothing", rec.Body.String())
	}
}

func TestServeGenerated_204SuppressesBodyEvenWhenSchemaDeclared(t *testing.T) {
	variants := map[int64][]gen.ResponseVariant{
		1: {{
			OpRowID:    1,
			Selector:   "204",
			HTTPStatus: 204,
			MediaType:  "application/json",
			SchemaPtr:  "#/paths/~1items/get/responses/204/content/application~1json/schema",
			OpPointer:  "#/paths/~1items/get",
		}},
	}
	rt := fixtureRuntime(t, declaredContent204Doc, []router.Route{itemRoute()}, variants, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/items")

	// Sanity check on the premise: gen.Generator really would produce a
	// non-empty body for this variant if asked directly — proving the
	// emptiness on the wire below comes from serveGenerated's own
	// suppression, not from gen happening to return nothing.
	if b, err := rt.gen.Body(variants[1][0], gen.Request{Method: "GET", CanonicalPath: "/items", Status: 204}); err != nil || len(b) == 0 {
		t.Fatalf("premise check: gen.Body = (%s, %v), want a real non-empty body for this declared schema", b, err)
	}

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty: 204 never has a body regardless of what the schema declares", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type = %q, want unset on a 204", ct)
	}
}

// TestServeGenerated_HEADKeepsHeadersButSuppressesBody mirrors what
// plane.go's serveResolved actually does for a HEAD request — wrap w in
// headWriter before Step 5 ever runs — and proves the GET route's
// Content-Type/Content-Length still reach the recorder while zero body
// bytes do, satisfying requirement 4 without any HEAD-specific branch in
// serveGenerated itself.
func TestServeGenerated_HEADKeepsHeadersButSuppressesBody(t *testing.T) {
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings())
	// Table.Match resolves HEAD against the GET bucket itself (router's own
	// contract) — this is exactly what routes.go hands serveGenerated for a
	// real HEAD request.
	m := mustMatch(t, rt, http.MethodHead, "/items")
	if m.Route.Method != http.MethodGet {
		t.Fatalf("matched route method = %q, want GET (HEAD resolves against the GET route)", m.Route.Method)
	}

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodHead, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	hw := &headWriter{ResponseWriter: rec}
	p.serveGenerated(hw, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD body = %q, want empty", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Errorf("Content-Type = %q, want the JSON type a GET would have sent", ct)
	}
	cl := rec.Header().Get("Content-Length")
	if cl == "" || cl == "0" {
		t.Errorf("Content-Length = %q, want the non-zero length a GET would have sent", cl)
	}
}

// TestServeGenerated_DetailRouteMatchesListRow is the direct proof of the
// property the phase digest calls "getting it wrong silently produces two
// different people": a detail route's ListFamily, computed from the real
// route table (router.ListFamily), must make its generated object identical —
// same id, same name — to the list route's own row for that same id.
func TestServeGenerated_DetailRouteMatchesListRow(t *testing.T) {
	routes := []router.Route{
		{OpRowID: 1, Method: "GET", Path: "/widgets", CanonicalPath: "/widgets", SourceOrder: 1},
		{OpRowID: 2, Method: "GET", Path: "/widgets/{id}", CanonicalPath: "/widgets/{}", SourceOrder: 2},
	}
	listVariant := gen.ResponseVariant{
		OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1widgets/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1widgets/get",
	}
	detailVariant := gen.ResponseVariant{
		OpRowID: 2, Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1widgets~1{id}/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1widgets~1{id}/get",
	}
	variants := map[int64][]gen.ResponseVariant{1: {listVariant}, 2: {detailVariant}}

	settings := domain.DefaultSettings()
	settings.ListSize = 5
	rt := fixtureRuntime(t, widgetsFamilyDoc, routes, variants, settings)

	p := respondTestPlane()
	ws := respondTestWorkspace()

	// 1. Fetch the list, and pick one row's identity.
	listReq := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil)
	listRec := httptest.NewRecorder()
	p.serveGenerated(listRec, listReq, ws, rt, mustMatch(t, rt, "GET", "/widgets"), resources.ScopeKey(""))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRec.Code, listRec.Body)
	}
	var items []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode list body: %v; body=%s", err, listRec.Body)
	}
	if len(items) == 0 {
		t.Fatal("list returned no items, nothing to compare against")
	}
	row := items[2%len(items)]

	// 2. Fetch the detail route for that SAME id.
	detailPath := "/widgets/" + strconv.Itoa(row.ID)
	detailReq := httptest.NewRequest(http.MethodGet, "http://alex.mock.local"+detailPath, nil)
	detailRec := httptest.NewRecorder()
	p.serveGenerated(detailRec, detailReq, ws, rt, mustMatch(t, rt, "GET", detailPath), resources.ScopeKey(""))
	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200; body=%s", detailRec.Code, detailRec.Body)
	}
	var card struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &card); err != nil {
		t.Fatalf("decode detail body: %v; body=%s", err, detailRec.Body)
	}

	if card.ID != row.ID {
		t.Errorf("detail card id = %d, want %d (the requested id, DESIGN §9's identity write-back)", card.ID, row.ID)
	}
	if card.Name != row.Name {
		t.Errorf("detail card name = %q, want %q (list row and detail card for the same id must be the SAME person)", card.Name, row.Name)
	}
}

// TestServeGenerated_DetailRouteMatchesListRow_TwoIDShapedPathParams is the
// regression test for the BLOCKER finding: a detail route with TWO path
// parameters that both look id-shaped by name ("tenantId" and
// "cohortId") must still resolve its OWN id ("cohortId") rather than the
// outer, shared "tenantId" a lexicographic tie-break over an
// unordered PathParams map would have picked instead. Without the fix, the
// family seedList is keyed by the wrong parameter and the detail route
// answers a completely different object than the list row for the
// requested id.
func TestServeGenerated_DetailRouteMatchesListRow_TwoIDShapedPathParams(t *testing.T) {
	routes := []router.Route{
		{OpRowID: 1, Method: "GET", Path: "/tenants/{tenantId}/cohorts", CanonicalPath: "/tenants/{}/cohorts", SourceOrder: 1},
		{OpRowID: 2, Method: "GET", Path: "/tenants/{tenantId}/cohorts/{cohortId}", CanonicalPath: "/tenants/{}/cohorts/{}", SourceOrder: 2},
	}
	listVariant := gen.ResponseVariant{
		OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1tenants~1{tenantId}~1cohorts/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1tenants~1{tenantId}~1cohorts/get",
	}
	detailVariant := gen.ResponseVariant{
		OpRowID: 2, Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1tenants~1{tenantId}~1cohorts~1{cohortId}/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1tenants~1{tenantId}~1cohorts~1{cohortId}/get",
	}
	variants := map[int64][]gen.ResponseVariant{1: {listVariant}, 2: {detailVariant}}

	settings := domain.DefaultSettings()
	settings.ListSize = 5
	rt := fixtureRuntime(t, cohortsFamilyDoc, routes, variants, settings)

	p := respondTestPlane()
	ws := respondTestWorkspace()

	// 1. Fetch the list under tenant 5, and pick one row's identity.
	listReq := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/tenants/5/cohorts", nil)
	listRec := httptest.NewRecorder()
	p.serveGenerated(listRec, listReq, ws, rt, mustMatch(t, rt, "GET", "/tenants/5/cohorts"), resources.ScopeKey(""))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRec.Code, listRec.Body)
	}
	var items []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode list body: %v; body=%s", err, listRec.Body)
	}
	if len(items) == 0 {
		t.Fatal("list returned no items, nothing to compare against")
	}
	row := items[2%len(items)]

	// 2. Fetch the detail route for that SAME cohort id, under the SAME
	// tenant. A wrong id-param pick (tenantId instead of
	// cohortId) would key the family seedList differently and answer a
	// different object entirely — not merely a mismatched id.
	detailPath := "/tenants/5/cohorts/" + strconv.Itoa(row.ID)
	detailReq := httptest.NewRequest(http.MethodGet, "http://alex.mock.local"+detailPath, nil)
	detailRec := httptest.NewRecorder()
	p.serveGenerated(detailRec, detailReq, ws, rt, mustMatch(t, rt, "GET", detailPath), resources.ScopeKey(""))
	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200; body=%s", detailRec.Code, detailRec.Body)
	}
	var card struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &card); err != nil {
		t.Fatalf("decode detail body: %v; body=%s", err, detailRec.Body)
	}

	if card.ID != row.ID {
		t.Errorf("detail card id = %d, want %d (the requested cohortId, not tenantId)", card.ID, row.ID)
	}
	if card.Name != row.Name {
		t.Errorf("detail card name = %q, want %q (list row and detail card for the same cohort must be the SAME person)", card.Name, row.Name)
	}
}

// TestServeGenerated_GenerationErrorNeverBecomesA500 proves item 9: a
// schema gen itself cannot satisfy answers the declared status with the
// declared Content-Type and an empty body — never a 500, and the error is
// only logged, never surfaced to the client.
func TestServeGenerated_GenerationErrorNeverBecomesA500(t *testing.T) {
	route := router.Route{OpRowID: 1, Method: "GET", Path: "/bad", CanonicalPath: "/bad", SourceOrder: 1}
	variants := map[int64][]gen.ResponseVariant{
		1: {{
			OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "application/json",
			SchemaPtr: "#/paths/~1bad/get/responses/200/content/application~1json/schema",
			OpPointer: "#/paths/~1bad/get",
		}},
	}
	rt := fixtureRuntime(t, unsatisfiableDoc, []router.Route{route}, variants, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/bad")

	// Sanity check on the premise: this schema really does fail generation.
	if _, err := rt.gen.Body(variants[1][0], gen.Request{Method: "GET", CanonicalPath: "/bad", Status: 200}); err == nil {
		t.Fatal("premise check: gen.Body succeeded, want an error for an unsatisfiable schema")
	}

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/bad", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the declared status, never a 500)", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty (the most honest answer to an ungenerable schema)", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want the declared type even though generation failed", ct)
	}
}

// TestServeGenerated_ContentLengthMatchesBodyWithinMaxResponse covers item
// 10 end to end: Content-Type set exactly once (no duplicate/garbled
// header), Content-Length consistent with the actual bytes written, and the
// body never exceeding cfg.MaxResponse — asserted here, not re-enforced
// (the generator already bounds it, per gen.Options.MaxBytes).
func TestServeGenerated_ContentLengthMatchesBodyWithinMaxResponse(t *testing.T) {
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if ctValues := rec.Header().Values("Content-Type"); len(ctValues) != 1 {
		t.Errorf("Content-Type set %d times, want exactly once: %v", len(ctValues), ctValues)
	}
	wantLen := strconv.Itoa(rec.Body.Len())
	if got := rec.Header().Get("Content-Length"); got != wantLen {
		t.Errorf("Content-Length = %q, want %q (the actual byte count written)", got, wantLen)
	}
	const cfgMaxResponse = 4 << 20
	if rec.Body.Len() > cfgMaxResponse {
		t.Errorf("body = %d bytes, want at most cfg.MaxResponse (%d)", rec.Body.Len(), cfgMaxResponse)
	}
}

// TestServeGenerated_NonJSONMediaTypeGetsItsOwnContentType is item 8: a
// non-JSON media type gets a placeholder body of the RIGHT type — never
// JSON with the wrong Content-Type — and item 6's "not applied to
// non-JSON": an envelope configured on the workspace must not corrupt it.
func TestServeGenerated_NonJSONMediaTypeGetsItsOwnContentType(t *testing.T) {
	settings := domain.DefaultSettings()
	envelope := "data"
	settings.Envelope = &envelope // must NOT apply to this non-JSON body

	route := router.Route{OpRowID: 1, Method: "GET", Path: "/export", CanonicalPath: "/export", SourceOrder: 1}
	variant := gen.ResponseVariant{
		OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "text/csv",
		SchemaPtr: "#/paths/~1export/get/responses/200/content/text~1csv/schema",
		OpPointer: "#/paths/~1export/get",
	}
	rt := fixtureRuntime(t, csvDoc, []router.Route{route}, map[int64][]gen.ResponseVariant{1: {variant}}, settings)
	m := mustMatch(t, rt, "GET", "/export")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/export", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("Content-Type = %q, want text/csv (the declared type, never application/json)", ct)
	}
	if json.Valid(rec.Body.Bytes()) {
		// A raw generated string can coincidentally look like valid JSON
		// (e.g. a bare quoted word), but it must never have been silently
		// re-encoded as a JSON string (which would double-quote it) or
		// wrapped in an envelope object — either would also happen to
		// produce "valid JSON" bytes here, so check the un-enveloped shape
		// directly instead of relying on json.Valid alone.
		var probe map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &probe); err == nil {
			t.Errorf("body = %s, looks like a JSON object — envelope must not apply to a non-JSON media type", rec.Body)
		}
	}
}

// TestServeGenerated_RouteOff_Answers404LikeUnmatchedRoute is the direct
// proof of DESIGN §8: a switched-off operation must be indistinguishable
// from one the route table never had at all — compared byte-for-byte
// against what [Plane.serveNoRoute] itself answers for a genuinely
// unmatched path, not merely "some 404".
func TestServeGenerated_RouteOff_Answers404LikeUnmatchedRoute(t *testing.T) {
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true, RouteOff: true,
			Responses: map[string]overrides.Variant{},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	ws := respondTestWorkspace()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, ws, rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route_off answers the same 404 an unmatched route gets); body=%s", rec.Code, rec.Body)
	}

	want := httptest.NewRecorder()
	p.serveNoRoute(want, req, ws, NormalizeSegments(req.URL.EscapedPath()))
	if rec.Body.String() != want.Body.String() {
		t.Errorf("route_off body = %s, want the exact unmatched-route 404 body %s", rec.Body, want.Body)
	}
	if rec.Header().Get("Content-Type") != want.Header().Get("Content-Type") {
		t.Errorf("route_off Content-Type = %q, want %q", rec.Header().Get("Content-Type"), want.Header().Get("Content-Type"))
	}
}

// TestServeGenerated_OverrideOnFalse_IsInert proves the gate every other
// P1c branch depends on: a row that EXISTS but has OverrideOn=false must
// leave route_off, active_status, mode "pinned", delay_ms AND when[] (P1c2)
// all unconsulted — the operation serves exactly as if there were no row at
// all, not "the row minus whichever one field a caller happened to check".
func TestServeGenerated_OverrideOnFalse_IsInert(t *testing.T) {
	pinnedStatus := 201
	rowDelay := 300 // large enough to notice if wrongly applied, small enough to keep the test fast
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: false, // <- the row is switched off wholesale
			RouteOff:     true,
			ActiveStatus: &pinnedStatus,
			DelayMs:      &rowDelay,
			ListSize:     &overrides.ListSize{Min: 1, Max: 1},
			Responses: map[string]overrides.Variant{
				"200": {
					Mode: "pinned",
					Body: json.RawMessage(`{"id":999,"name":"should-not-appear"}`),
				},
				// A when[] that WOULD match the request built below, if
				// SelectWhen were ever consulted for a switched-off row —
				// it must not be: overrideActive gates that call exactly
				// like every other branch here (respond.go's own comment).
				"409": {When: []overrides.Condition{{In: "header", Name: "X-Debug", Op: "exists"}}},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	req.Header.Set("X-Debug", "1") // matches the "409" variant's own when[], were it ever consulted
	rec := httptest.NewRecorder()

	start := time.Now()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("elapsed = %v, want near-instant: OverrideOn=false must leave delay_ms unapplied too", elapsed)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — RouteOff/when[]/ActiveStatus must all be ignored when OverrideOn is false; body=%s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body)
	}
	if body["name"] == "should-not-appear" {
		t.Errorf("body = %s, want ordinary generation — a switched-off row must not leak its pinned body", rec.Body)
	}
}

// TestServeGenerated_ConcurrentRequestsWithRecipesBound is the -race
// regression the task brief asks for by name: many goroutines serving the
// SAME runtime, with a recipe bound, must never race on rt.recipeSets or
// anything serveGenerated derives from it.
func TestServeGenerated_ConcurrentRequestsWithRecipesBound(t *testing.T) {
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode: "generated",
					Recipes: map[string]recipes.Recipe{
						"name": {Kind: recipes.KindConst, Data: json.RawMessage(`"race-const"`)},
					},
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	ws := respondTestWorkspace()

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, req, ws, rt, m, resources.ScopeKey(""))
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", rec.Code)
				return
			}
			var body struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Errorf("decode: %v; body=%s", err, rec.Body)
				return
			}
			if body.Name != "race-const" {
				t.Errorf("name = %q, want %q", body.Name, "race-const")
			}
		}()
	}
	wg.Wait()
}

// TestServeGenerated_WriteFailureIsLoggedNotPanicked proves the final
// w.Write(asm.Body) error path (respond.go:238-240) does not panic, leaves
// the status/headers it already wrote intact, and — the discriminating half
// a bare status/body assertion cannot provide, since Write's error is
// otherwise unobservable once the response is already committed — actually
// emits the "write generated response" Debug record naming the failure. A
// second run over the SAME plane with a succeeding Write proves the record
// is specific to the failure, not emitted unconditionally: reverting the
// guard to a bare w.Write(asm.Body) turns the first subtest red without
// touching status, headers, or the no-panic guarantee.
func TestServeGenerated_WriteFailureIsLoggedNotPanicked(t *testing.T) {
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/items")
	ws := respondTestWorkspace()

	t.Run("write fails: logs the failure", func(t *testing.T) {
		handler := &recordingHandler{}
		p := New(runtimeTestConfig(4<<20, 32), nil, nil, slog.New(handler))
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
		w := &failingResponseWriter{ResponseRecorder: httptest.NewRecorder()}

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("serveGenerated panicked on a failing Write: %v", r)
				}
			}()
			p.serveGenerated(w, req, ws, rt, m, resources.ScopeKey(""))
		}()

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 — already written before the failing Write call", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json — headers are set before the failing Write", ct)
		}

		rec := handler.debugRecord("write generated response")
		if rec == nil {
			t.Fatal(`no Debug "write generated response" record — the write failure went unlogged`)
		}
		attrs := map[string]string{}
		rec.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.String()
			return true
		})
		if attrs["workspace"] != ws.Slug {
			t.Errorf(`log attr "workspace" = %q, want %q`, attrs["workspace"], ws.Slug)
		}
		if attrs["err"] == "" {
			t.Error(`log attr "err" is empty, want the Write failure's error text`)
		}
	})

	t.Run("write succeeds: logs nothing", func(t *testing.T) {
		handler := &recordingHandler{}
		p := New(runtimeTestConfig(4<<20, 32), nil, nil, slog.New(handler))
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
		w := httptest.NewRecorder()

		p.serveGenerated(w, req, ws, rt, m, resources.ScopeKey(""))

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
		if rec := handler.debugRecord("write generated response"); rec != nil {
			t.Errorf(`unexpected Debug "write generated response" record on a successful write: %+v`, rec)
		}
	})
}
