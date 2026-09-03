package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testspec"
	"github.com/yashok111/mocker/internal/workspaces"
)

// newTestServerWithCheckpointDebounce is [newTestServer] with ONE field
// changed: CheckpointDebounce, non-zero. It exists only for
// TestHandler_resetData_writesNoCheckpoint (clause 8, decisions.md
// mocker-p3b-resources D10): every OTHER fixture in this package pins the
// debounce to zero (testConfig's own doc comment says why — the P2d
// auto-checkpoint wrapper is not installed at all at zero), and that
// fixture would make clause 8's own assertion pass VACUOUSLY — the wrapper
// would never run, so a mutation that mislabelled reset-data could never
// be caught. This duplicates newTestServer's own construction rather than
// widening it, exactly as autocheckpoint_test.go's loopbackTestServer
// (package admin, not this one) makes the identical choice for the
// identical reason.
func newTestServerWithCheckpointDebounce(t *testing.T, window int) *testServer {
	t.Helper()
	cfg := testConfig(t)
	cfg.CheckpointDebounce = window
	db, err := store.Open(t.Context(), cfg.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	provider := auth.NewSharedPassword(cfg)
	sessions := auth.NewManager(db, cfg, provider)
	ws := workspaces.NewRepo(db)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := admin.New(cfg, sessions, ws, db, log)
	return &testServer{handler: srv.Handler(), db: db, cfg: cfg}
}

// workspaceSlug fetches a workspace's CURRENT slug over the wire — reset-data
// tests need it fresh for confirmSlug, and re-reading it through the API
// (rather than ts.db directly) exercises the same path a real operator's
// browser would.
func (ts *testServer) workspaceSlug(t *testing.T, cookie *http.Cookie, wsID int64) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d", wsID), nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET workspace %d: status = %d, want 200; body = %s", wsID, rec.Code, rec.Body.String())
	}
	var body struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode workspace response: %v", err)
	}
	return body.Slug
}

// errorCode decodes {"error":{"code": "..."}} — every refusal in this file
// answers that shape.
func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	return body.Error.Code
}

// importDerivationSpec imports [testspec.DerivationDoc] — the shared
// derivation fixture whose /widgets and /bareitems families are the two
// positive controls — and returns its spec id, reusing the caller's session.
func (ts *testServer) importDerivationSpec(t *testing.T, cookie *http.Cookie, csrfToken string) int64 {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/specs",
		map[string]string{"name": "Derivation", "source": "upload", "document": string(testspec.DerivationDoc())},
		cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import derivation spec: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode import spec response: %v", err)
	}
	return body.ID
}

// emptyPathsDoc declares no paths at all, so [deriveSuggestions] over it
// derives nothing — the identical fixture shape
// internal/specs/rederive_test.go's own rederiveEmptyDoc gives P25, kept
// as a second, independent copy here rather than an import: that constant
// is unexported in a different package's _test.go and this package cannot
// reach it.
const emptyPathsDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "Empty API", "version": "1.0.0" },
  "paths": {}
}`

// importSpec imports document under name and returns its spec id — the
// general form [testServer.importDerivationSpec]/[testServer.importNestedSpec]
// both specialize for their own fixed documents.
func (ts *testServer) importSpecWithSession(t *testing.T, cookie *http.Cookie, csrfToken, name, document string) int64 {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/specs",
		map[string]string{"name": name, "source": "upload", "document": document},
		cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import spec %q: status = %d, want 201; body = %s", name, rec.Code, rec.Body.String())
	}
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode import spec response: %v", err)
	}
	return body.ID
}

// importNestedSpec is [testServer.importDerivationSpec]'s sibling over
// [testspec.NestedDerivationDoc] (P3e D4.3): the one fixture with a real
// parent family ("/orgs") and two children, needed by every test below
// that exercises D5.1's parent-confirmed check or D7.1's child-confirmed
// refusal.
func (ts *testServer) importNestedSpec(t *testing.T, cookie *http.Cookie, csrfToken string) int64 {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/specs",
		map[string]string{"name": "Nested Derivation", "source": "upload", "document": string(testspec.NestedDerivationDoc())},
		cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("import nested derivation spec: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode import spec response: %v", err)
	}
	return body.ID
}

// resourceFamilyWire mirrors admin's unexported resourceFamilyView on the
// wire, decoded straight from JSON so a test can assert every field's
// presence (including explicit null) rather than just its non-null value.
type resourceFamilyWire struct {
	RouteFamily string  `json:"routeFamily"`
	Name        string  `json:"name"`
	Decision    *string `json:"decision"`
	ResourceID  *int64  `json:"resourceId"`
	IDField     *string `json:"idField"`
	WriteForm   *string `json:"writeForm"`
	EntityCount *int64  `json:"entityCount"`
	// ByBaseScope mirrors ResourceFamilyView.ByBaseScope (D13, P3h): nil on
	// every row this file's own pre-P3h tests build (an unconfirmed row, or
	// a confirmed one under a workspace whose basePath carries no
	// parameter — the implicit singleton D3.3 still reports exactly one
	// element, so nil here means "not confirmed", the same rule EntityCount
	// already holds).
	ByBaseScope []resourceBaseScopeCountWire `json:"byBaseScope"`
}

// resourceBaseScopeCountWire mirrors admin's unexported
// resourceBaseScopeCountView (D13, P3h): one declared base value and the
// row count stored under it.
type resourceBaseScopeCountWire struct {
	BaseScope   string `json:"baseScope"`
	EntityCount int64  `json:"entityCount"`
}

// TestHandler_resourceSuggestions is D13 clause 8's own wire-level
// counterpart: GET /api/specs/{id}/resource-suggestions over
// [testspec.DerivationDoc] answers exactly the two positive-control
// families the fixture's own doc comment names, sentinel excluded, and a
// second call performs no re-derivation (R8) — asserted here by the RESULT
// staying identical, the row-count half of clause 9 (the call-count half is
// internal/specs' own TestRepo_EnsureSuggestions_BackfillRunsOnce).
func TestHandler_resourceSuggestions(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)

	target := fmt.Sprintf("http://mocker.local/api/specs/%d/resource-suggestions", specID)
	get := func() []resourceFamilyWire {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, target, nil)
		req.AddCookie(cookie)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET resource-suggestions: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Suggestions []struct {
				RouteFamily string  `json:"routeFamily"`
				Name        string  `json:"name"`
				IDField     string  `json:"idField"`
				Confidence  float64 `json:"confidence"`
			} `json:"suggestions"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode suggestions response: %v", err)
		}
		out := make([]resourceFamilyWire, len(body.Suggestions))
		for i, s := range body.Suggestions {
			out[i] = resourceFamilyWire{RouteFamily: s.RouteFamily, Name: s.Name}
		}
		return out
	}

	first := get()
	families := make(map[string]bool, len(first))
	for _, f := range first {
		families[f.RouteFamily] = true
	}
	if !families[testspec.FamilyWidgets] || !families[testspec.FamilyBareItems] {
		t.Fatalf("suggested families = %v, want both %q and %q present", families, testspec.FamilyWidgets, testspec.FamilyBareItems)
	}
	if len(first) != 2 {
		t.Errorf("suggested family count = %d, want exactly 2 (the fixture's two positive controls, every negative excluded)", len(first))
	}

	second := get()
	if len(second) != len(first) {
		t.Fatalf("second call returned %d suggestions, want %d (the backfill runs once, every later call reads back the same set)", len(second), len(first))
	}
}

// TestHandler_resourceSuggestions_unknownSpec is D10's own 404: a spec id
// that parses but names nothing must not derive from thin air.
func TestHandler_resourceSuggestions_unknownSpec(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, _ := ts.login(t, "Alex")

	req := httptest.NewRequest(http.MethodGet, "http://mocker.local/api/specs/999999/resource-suggestions", nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

// TestHandler_workspaceResources_confirmAndDecline is D13's own end-to-end
// clause 3 read through the admin surface's decision route rather than the
// mock plane's four verbs (mockplane's own tests own that half): confirming
// FamilyWidgets materializes a resources row the list route then reports,
// with entityCount equal to the workspace's default listSize (5); declining
// it without the required confirmSlug is refused and changes nothing;
// declining it WITH the slug removes it, and the family reappears on the
// list as undecided-again is NOT what happens — D4 writes state='declined'
// (D13 clause 12: "it stays declined across a reload"), so a re-list must
// report "declined", never null.
func TestHandler_workspaceResources_confirmAndDecline(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Res Demo", specID)

	listTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/resources", wsID)
	list := func() []resourceFamilyWire {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, listTarget, nil)
		req.AddCookie(cookie)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET resources: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
		}
		var body struct {
			Families []resourceFamilyWire `json:"families"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode resources response: %v", err)
		}
		return body.Families
	}
	findFamily := func(families []resourceFamilyWire, routeFamily string) (resourceFamilyWire, bool) {
		for _, f := range families {
			if f.RouteFamily == routeFamily {
				return f, true
			}
		}
		return resourceFamilyWire{}, false
	}

	before, ok := findFamily(list(), testspec.FamilyWidgets)
	if !ok {
		t.Fatalf("family %q is missing from the initial list", testspec.FamilyWidgets)
	}
	if before.Decision != nil {
		t.Errorf("decision before any call = %v, want nil (never decided)", *before.Decision)
	}
	if before.ResourceID != nil || before.IDField != nil || before.WriteForm != nil || before.EntityCount != nil {
		t.Errorf("confirmed-only fields on an undecided row = %+v, want all four nil", before)
	}

	decideTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID)
	confirmReq := jsonRequest(t, http.MethodPost, decideTarget,
		map[string]string{"routeFamily": testspec.FamilyWidgets, "state": "confirmed"}, cookie, csrfToken)
	confirmRec := ts.do(confirmReq)
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("confirm: status = %d, want 200; body = %s", confirmRec.Code, confirmRec.Body.String())
	}
	var confirmBody struct {
		Family resourceFamilyWire `json:"family"`
	}
	if err := json.Unmarshal(confirmRec.Body.Bytes(), &confirmBody); err != nil {
		t.Fatalf("decode confirm response: %v", err)
	}
	if confirmBody.Family.Decision == nil || *confirmBody.Family.Decision != "confirmed" {
		t.Errorf("confirm response decision = %v, want \"confirmed\"", confirmBody.Family.Decision)
	}
	if confirmBody.Family.EntityCount == nil || *confirmBody.Family.EntityCount != 5 {
		t.Errorf("confirm response entityCount = %v, want 5 (default listSize)", confirmBody.Family.EntityCount)
	}
	if confirmBody.Family.ResourceID == nil {
		t.Error("confirm response resourceId is nil, want the new resources row's id")
	}

	afterConfirm, ok := findFamily(list(), testspec.FamilyWidgets)
	if !ok {
		t.Fatalf("family %q is missing from the list after confirm", testspec.FamilyWidgets)
	}
	if afterConfirm.Decision == nil || *afterConfirm.Decision != "confirmed" {
		t.Errorf("list decision after confirm = %v, want \"confirmed\"", afterConfirm.Decision)
	}
	if afterConfirm.EntityCount == nil || *afterConfirm.EntityCount != 5 {
		t.Errorf("list entityCount after confirm = %v, want 5", afterConfirm.EntityCount)
	}

	// R10: declining a CONFIRMED resource with no confirmSlug is refused,
	// and the family must still read "confirmed" afterward.
	declineNoSlug := jsonRequest(t, http.MethodPost, decideTarget,
		map[string]string{"routeFamily": testspec.FamilyWidgets, "state": "declined"}, cookie, csrfToken)
	declineNoSlugRec := ts.do(declineNoSlug)
	if declineNoSlugRec.Code != http.StatusConflict {
		t.Fatalf("decline without confirmSlug: status = %d, want 409; body = %s", declineNoSlugRec.Code, declineNoSlugRec.Body.String())
	}
	var errBody struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(declineNoSlugRec.Body.Bytes(), &errBody); err != nil {
		t.Fatalf("decode error response: %v", err)
	}
	if errBody.Error.Code != "confirm_slug_required" {
		t.Errorf("error code = %q, want %q", errBody.Error.Code, "confirm_slug_required")
	}
	stillConfirmed, ok := findFamily(list(), testspec.FamilyWidgets)
	if !ok || stillConfirmed.Decision == nil || *stillConfirmed.Decision != "confirmed" {
		t.Fatalf("family after a refused decline = %+v, want still confirmed", stillConfirmed)
	}

	// Now with the workspace's own slug: the decline must succeed and the
	// resource must vanish from confirmed state, replaced by "declined".
	wsGet := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d", wsID), nil)
	wsGet.AddCookie(cookie)
	wsRec := ts.do(wsGet)
	var wsBody struct {
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(wsRec.Body.Bytes(), &wsBody); err != nil {
		t.Fatalf("decode workspace response: %v", err)
	}

	declineReq := jsonRequest(t, http.MethodPost, decideTarget,
		map[string]string{"routeFamily": testspec.FamilyWidgets, "state": "declined", "confirmSlug": wsBody.Slug}, cookie, csrfToken)
	declineRec := ts.do(declineReq)
	if declineRec.Code != http.StatusOK {
		t.Fatalf("decline with confirmSlug: status = %d, want 200; body = %s", declineRec.Code, declineRec.Body.String())
	}
	var declineBody struct {
		Family resourceFamilyWire `json:"family"`
	}
	if err := json.Unmarshal(declineRec.Body.Bytes(), &declineBody); err != nil {
		t.Fatalf("decode decline response: %v", err)
	}
	if declineBody.Family.Decision == nil || *declineBody.Family.Decision != "declined" {
		t.Errorf("decline response decision = %v, want \"declined\"", declineBody.Family.Decision)
	}
	if declineBody.Family.ResourceID != nil || declineBody.Family.IDField != nil ||
		declineBody.Family.WriteForm != nil || declineBody.Family.EntityCount != nil {
		t.Errorf("decline response confirmed-only fields = %+v, want all four nil", declineBody.Family)
	}

	afterDecline, ok := findFamily(list(), testspec.FamilyWidgets)
	if !ok {
		t.Fatalf("family %q is missing from the list after decline", testspec.FamilyWidgets)
	}
	if afterDecline.Decision == nil || *afterDecline.Decision != "declined" {
		t.Errorf("list decision after decline = %v, want \"declined\" (D13 clause 12: stays declined, not reverting to null)", afterDecline.Decision)
	}
}

// TestHandler_workspaceResources_ordersAncestorBeforeDescendants pins D12.2
// (mocker-p3g-decisions §D12.2, P22's server-side half): GET
// .../resources must place an ancestor family before its descendants,
// because that is the order a confirm chain has to be walked in and
// [buildFamiliesView]'s own doc comment already claims the sort delivers
// it. The claim rests on a fact about strings, not about resources: a
// family string is a literal PREFIX of every descendant's, so
// ORDER BY route_family ASC (listSuggestions, internal/specs/repo.go)
// already places a parent ahead of its children — this test's own
// obligation, per D12.2, is to verify that survives the SECOND sort site,
// buildFamiliesView's own sort.Slice, once a CONFIRMED ORPHAN (a family the
// bound spec no longer suggests, D8's own shape) is merged in beside a
// freshly suggested one, not just to re-confirm what listSuggestions
// already guarantees on its own.
//
// The fixture rebinds the workspace away from the nested spec entirely,
// which turns its three confirmed families ("/orgs",
// "/orgs/{}/departments", "/orgs/{}/users") into orphans in one move — D8's
// own re-bind shape — and then confirms one fresh suggestion from the NEW
// spec ("/bareitems"), leaving the derivation doc's other suggestion
// ("/widgets") undecided, so the final list mixes an orphan chain, a
// confirmed suggestion and an undecided one. "/bareitems" sorts BEFORE the
// orphan chain and "/widgets" sorts AFTER it — both unrelated to nesting —
// so what this placement is actually pinning is the WITHIN-chain order
// (orgs before its two children), which a sort by `name` instead of
// `routeFamily` would scramble: "departments" < "orgs" < "users"
// alphabetically.
func TestHandler_workspaceResources_ordersAncestorBeforeDescendants(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	nestedSpecID := ts.importNestedSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Order Demo", nestedSpecID)
	decideTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID)

	confirm := func(family string) {
		t.Helper()
		req := jsonRequest(t, http.MethodPost, decideTarget,
			map[string]string{"routeFamily": family, "state": "confirmed"}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("confirm %q: status = %d, want 200; body = %s", family, rec.Code, rec.Body.String())
		}
	}
	// Parent before child — D5.1's own confirm-order requirement, not this
	// test's subject, but every confirm route enforces it regardless.
	confirm(testspec.FamilyOrgs)
	confirm(testspec.FamilyOrgDepartments)
	confirm(testspec.FamilyOrgUsers)

	// Read the workspace's current editVersion, then rebind it to a
	// DIFFERENT spec — the derivation doc, which declares neither "/orgs"
	// nor its children — orphaning all three confirmed families in one
	// PATCH (D8's own shape for how an orphan is born).
	getReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d", wsID), nil)
	getReq.AddCookie(cookie)
	getRec := ts.do(getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("GET workspace: status = %d, want 200; body = %s", getRec.Code, getRec.Body.String())
	}
	var wsBody struct {
		EditVersion int64 `json:"editVersion"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &wsBody); err != nil {
		t.Fatalf("decode workspace response: %v", err)
	}

	derivationSpecID := ts.importDerivationSpec(t, cookie, csrfToken)
	patchReq := jsonRequest(t, http.MethodPatch, fmt.Sprintf("http://mocker.local/api/workspaces/%d", wsID),
		map[string]any{"specId": derivationSpecID, "editVersion": wsBody.EditVersion}, cookie, csrfToken)
	patchRec := ts.do(patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("rebind workspace: status = %d, want 200; body = %s", patchRec.Code, patchRec.Body.String())
	}

	// One fresh suggestion from the new spec, confirmed beside the three
	// now-orphaned families.
	confirm(testspec.FamilyBareItems)

	listReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d/resources", wsID), nil)
	listReq.AddCookie(cookie)
	listRec := ts.do(listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list resources: status = %d, want 200; body = %s", listRec.Code, listRec.Body.String())
	}
	var listBody struct {
		Families []resourceFamilyWire `json:"families"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list response: %v", err)
	}

	got := make([]string, 0, len(listBody.Families))
	for _, f := range listBody.Families {
		got = append(got, f.RouteFamily)
	}
	want := []string{
		testspec.FamilyBareItems,
		testspec.FamilyOrgs,
		testspec.FamilyOrgDepartments,
		testspec.FamilyOrgUsers,
		testspec.FamilyWidgets, // the derivation spec's OTHER suggestion, undecided — still listed, still sorted last.
	}
	if !slices.Equal(got, want) {
		t.Fatalf("family order = %v, want %v (an ancestor before its descendants, surviving buildFamiliesView's orphan merge)", got, want)
	}
}

// TestHandler_decideResource_unknownFamily is D4's 404 unknown_family: a
// routeFamily that names neither a suggestion nor an existing resources row.
func TestHandler_decideResource_unknownFamily(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsID, _ := ts.createWorkspace(t, "Alex", "Demo")

	req := jsonRequest(t, http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", int64(wsID)),
		map[string]string{"routeFamily": "/nonexistent", "state": "confirmed"}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

// --- P3b, decisions.md mocker-p3b-resources D10: clauses 6, 7, 8, 19a -----

// TestHandler_resetData_badMode is clause 6: an absent mode, an empty mode
// and an unknown mode each answer 400 naming both legal values.
// [resources.ResetMode] is a typed string, so absent and empty are the
// IDENTICAL value the instant one is constructed — this handler is the
// only place that can tell them apart, decoding into a *string first.
func TestHandler_resetData_badMode(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsID, _ := ts.createWorkspace(t, "Alex", "Demo")
	target := fmt.Sprintf("http://mocker.local/api/workspaces/%d/reset-data", int64(wsID))

	for _, tc := range []struct {
		name string
		body map[string]string
	}{
		{"absent", map[string]string{"confirmSlug": "x"}},
		{"empty", map[string]string{"mode": "", "confirmSlug": "x"}},
		{"unknown", map[string]string{"mode": "wipe", "confirmSlug": "x"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := jsonRequest(t, http.MethodPost, target, tc.body, cookie, csrfToken)
			rec := ts.do(req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body = %s", rec.Code, rec.Body.String())
			}
			body := rec.Body.String()
			if !strings.Contains(body, "reseed") || !strings.Contains(body, "clear") {
				t.Errorf("body = %s, want both legal values named", body)
			}
		})
	}
}

// TestHandler_resetData_confirmSlug is clause 7: over a workspace WITH
// entity rows, a missing confirmSlug answers 409 confirm_slug_required and
// a wrong one 409 confirm_slug_mismatch — the HANDLER's own constants, not
// [resources.Repo]'s sentinels directly — and both leave every row
// standing; a slug altered by a rename between "the caller learned it" and
// the call is caught by the AUTHORITATIVE in-transaction comparison, not
// the fence (fenceResetTx never reads slug at all, D3 R9) — proven here by
// the error code: stale_config would mean the fence caught it,
// confirm_slug_mismatch means the slug check did.
func TestHandler_resetData_confirmSlug(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Res Demo", specID)
	slug := ts.workspaceSlug(t, cookie, wsID)

	decideTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID)
	confirmReq := jsonRequest(t, http.MethodPost, decideTarget,
		map[string]string{"routeFamily": testspec.FamilyWidgets, "state": "confirmed"}, cookie, csrfToken)
	if rec := ts.do(confirmReq); rec.Code != http.StatusOK {
		t.Fatalf("confirm: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	entityCountNow := func() int64 {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d/resources", wsID), nil)
		req.AddCookie(cookie)
		rec := ts.do(req)
		var body struct {
			Families []resourceFamilyWire `json:"families"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode resources response: %v", err)
		}
		for _, f := range body.Families {
			if f.RouteFamily == testspec.FamilyWidgets && f.EntityCount != nil {
				return *f.EntityCount
			}
		}
		t.Fatalf("widgets family missing an entityCount after confirm")
		return -1
	}
	before := entityCountNow()
	if before == 0 {
		t.Fatalf("test precondition broken: want a non-zero entity count after confirm")
	}

	resetTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/reset-data", wsID)

	missingRec := ts.do(jsonRequest(t, http.MethodPost, resetTarget, map[string]string{"mode": "clear"}, cookie, csrfToken))
	if missingRec.Code != http.StatusConflict {
		t.Fatalf("missing confirmSlug: status = %d, want 409; body = %s", missingRec.Code, missingRec.Body.String())
	}
	if code := errorCode(t, missingRec); code != "confirm_slug_required" {
		t.Errorf("missing confirmSlug: error.code = %q, want %q", code, "confirm_slug_required")
	}
	if got := entityCountNow(); got != before {
		t.Errorf("entity count after refused reset (missing slug) = %d, want unchanged %d", got, before)
	}

	wrongRec := ts.do(jsonRequest(t, http.MethodPost, resetTarget,
		map[string]string{"mode": "clear", "confirmSlug": "not-the-slug"}, cookie, csrfToken))
	if wrongRec.Code != http.StatusConflict {
		t.Fatalf("wrong confirmSlug: status = %d, want 409; body = %s", wrongRec.Code, wrongRec.Body.String())
	}
	if code := errorCode(t, wrongRec); code != "confirm_slug_mismatch" {
		t.Errorf("wrong confirmSlug: error.code = %q, want %q", code, "confirm_slug_mismatch")
	}
	if got := entityCountNow(); got != before {
		t.Errorf("entity count after refused reset (wrong slug) = %d, want unchanged %d", got, before)
	}

	if _, err := ts.db.W.ExecContext(t.Context(), "UPDATE workspaces SET slug = 'renamed-away' WHERE id = ?", wsID); err != nil {
		t.Fatalf("rename workspace: %v", err)
	}
	renameRec := ts.do(jsonRequest(t, http.MethodPost, resetTarget,
		map[string]string{"mode": "clear", "confirmSlug": slug}, cookie, csrfToken))
	if renameRec.Code != http.StatusConflict {
		t.Fatalf("stale slug after rename: status = %d, want 409; body = %s", renameRec.Code, renameRec.Body.String())
	}
	if code := errorCode(t, renameRec); code != "confirm_slug_mismatch" {
		t.Errorf("stale slug after rename: error.code = %q, want %q (not stale_config — the fence never reads slug)", code, "confirm_slug_mismatch")
	}
	if got := entityCountNow(); got != before {
		t.Errorf("entity count after refused reset (renamed workspace) = %d, want unchanged %d", got, before)
	}
}

// TestHandler_resetData_writesNoCheckpoint is clause 8, VERIFIED BY
// MUTATION (D10's own table: injecting an autoCheckpointLabels entry for
// this route must turn this test red). The fixture MUST run with
// CheckpointDebounce > 0 — every other fixture in this file pins it to
// zero, at which [Server.routeMux] does not install the auto-checkpoint
// wrapper at all, and the assertion below would pass VACUOUSLY.
func TestHandler_resetData_writesNoCheckpoint(t *testing.T) {
	t.Parallel()
	const window = 300
	ts := newTestServerWithCheckpointDebounce(t, window)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Res Demo", specID)
	slug := ts.workspaceSlug(t, cookie, wsID)

	decideTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID)
	confirmReq := jsonRequest(t, http.MethodPost, decideTarget,
		map[string]string{"routeFamily": testspec.FamilyWidgets, "state": "confirmed"}, cookie, csrfToken)
	if rec := ts.do(confirmReq); rec.Code != http.StatusOK {
		t.Fatalf("confirm: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	countCheckpoints := func() int {
		t.Helper()
		var n int
		if err := ts.db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM checkpoints WHERE workspace_id = ?", wsID).Scan(&n); err != nil {
			t.Fatalf("count checkpoints: %v", err)
		}
		return n
	}
	before := countCheckpoints()

	resetTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/reset-data", wsID)
	for _, mode := range []string{"reseed", "clear"} {
		rec := ts.do(jsonRequest(t, http.MethodPost, resetTarget,
			map[string]string{"mode": mode, "confirmSlug": slug}, cookie, csrfToken))
		if rec.Code != http.StatusOK {
			t.Fatalf("reset-data(%s): status = %d, want 200; body = %s", mode, rec.Code, rec.Body.String())
		}
	}

	if got := countCheckpoints(); got != before {
		t.Errorf("checkpoints after reset-data (both modes, debounce=%ds) = %d, want unchanged %d", window, got, before)
	}
}

// TestHandler_resetData_emptyRosterAnswers200 is clause 19a's status half:
// both modes over a workspace with no confirmed family and no spec bound
// answer 200 changed:false — [resources.Repo]'s own carve-out that neither
// mode reads the spec on this path is proven the moment this call succeeds
// at all against a nil spec_id.
func TestHandler_resetData_emptyRosterAnswers200(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsIDFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsIDFloat)
	slug := ts.workspaceSlug(t, cookie, wsID)
	target := fmt.Sprintf("http://mocker.local/api/workspaces/%d/reset-data", wsID)

	for _, mode := range []string{"reseed", "clear"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()
			rec := ts.do(jsonRequest(t, http.MethodPost, target,
				map[string]string{"mode": mode, "confirmSlug": slug}, cookie, csrfToken))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body = %s", rec.Code, rec.Body.String())
			}
			var body struct {
				Changed bool  `json:"changed"`
				Deleted int64 `json:"deleted"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if body.Changed {
				t.Errorf("changed = true, want false")
			}
			if body.Deleted != 0 {
				t.Errorf("deleted = %d, want 0", body.Deleted)
			}
		})
	}
}

// --- P3e, decisions.md mocker-p3e-nested D5.1/D7.1/D5.5/D12: the two new
// refusal codes and the wire's per-scope entityCount total. -------------

// TestHandler_decideResource_confirmNestedWithoutParent_refused is D5.1: a
// nested family (whose route carries exactly one outer path parameter)
// cannot be confirmed while its parent family has no confirmed resources
// row — 409 parent_not_confirmed, the handler's own new constant, mapped
// straight from [resources.ErrParentNotConfirmed].
func TestHandler_decideResource_confirmNestedWithoutParent_refused(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importNestedSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Nested Demo", specID)

	decideTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID)
	req := jsonRequest(t, http.MethodPost, decideTarget,
		map[string]string{"routeFamily": testspec.FamilyOrgUsers, "state": "confirmed"}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("confirm nested family with unconfirmed parent: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "parent_not_confirmed" {
		t.Errorf("error code = %q, want %q", code, "parent_not_confirmed")
	}
}

// TestHandler_decideResource_confirmNestedWithConfirmedParent_reportsTotal
// is D5.1's positive case plus D5.5 point 4/D12.1: once the parent
// ("/orgs") is confirmed, confirming a child populates one row set per LIVE
// parent row, and both the confirm response's entityCount and the list
// route's (now CountEntities-backed, D6.2) entityCount report the FAMILY
// TOTAL across every scope — P (the parent's own seeded row count) times L
// (the workspace's default listSize) — never one scope's slice of it.
func TestHandler_decideResource_confirmNestedWithConfirmedParent_reportsTotal(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importNestedSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Nested Demo", specID)
	decideTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID)

	confirmFamily := func(family string) resourceFamilyWire {
		t.Helper()
		req := jsonRequest(t, http.MethodPost, decideTarget,
			map[string]string{"routeFamily": family, "state": "confirmed"}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != http.StatusOK {
			t.Fatalf("confirm %q: status = %d, want 200; body = %s", family, rec.Code, rec.Body.String())
		}
		var body struct {
			Family resourceFamilyWire `json:"family"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode confirm %q response: %v", family, err)
		}
		return body.Family
	}

	parent := confirmFamily(testspec.FamilyOrgs)
	if parent.EntityCount == nil {
		t.Fatalf("parent confirm entityCount is nil")
	}
	parentCount := *parent.EntityCount // P: the default listSize, one scope.

	child := confirmFamily(testspec.FamilyOrgUsers)
	if child.EntityCount == nil {
		t.Fatalf("child confirm entityCount is nil")
	}
	want := parentCount * parentCount // P scopes, each populated to L == P (both default listSize).
	if *child.EntityCount != want {
		t.Errorf("child confirm entityCount = %d, want %d (P=%d parent rows x L=%d per scope)", *child.EntityCount, want, parentCount, parentCount)
	}

	// The list route reads CountEntities (D6.2), not the confirm struct's
	// own Seq field — asserted separately so a wrong CountEntities
	// implementation (e.g. still scope-scoped) cannot hide behind the
	// confirm response's own already-correct number.
	listReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d/resources", wsID), nil)
	listReq.AddCookie(cookie)
	listRec := ts.do(listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list resources: status = %d, want 200; body = %s", listRec.Code, listRec.Body.String())
	}
	var listBody struct {
		Families []resourceFamilyWire `json:"families"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	found := false
	for _, f := range listBody.Families {
		if f.RouteFamily != testspec.FamilyOrgUsers {
			continue
		}
		found = true
		if f.EntityCount == nil || *f.EntityCount != want {
			t.Errorf("list entityCount for %q = %v, want %d", testspec.FamilyOrgUsers, f.EntityCount, want)
		}
	}
	if !found {
		t.Fatalf("family %q missing from list after confirm", testspec.FamilyOrgUsers)
	}
}

// TestHandler_decideResource_declineParentWithConfirmedChild_refused is
// D7.1: declining a family that is the parent of a still-confirmed child
// answers 409 child_confirmed rather than cascading the delete — the
// refusal [resources.ErrChildConfirmed] maps to, and the parent's own
// resources row (and every row beneath it) must still stand afterward.
func TestHandler_decideResource_declineParentWithConfirmedChild_refused(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importNestedSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Nested Demo", specID)
	slug := ts.workspaceSlug(t, cookie, wsID)
	decideTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID)

	for _, family := range []string{testspec.FamilyOrgs, testspec.FamilyOrgUsers} {
		req := jsonRequest(t, http.MethodPost, decideTarget,
			map[string]string{"routeFamily": family, "state": "confirmed"}, cookie, csrfToken)
		if rec := ts.do(req); rec.Code != http.StatusOK {
			t.Fatalf("confirm %q: status = %d, want 200; body = %s", family, rec.Code, rec.Body.String())
		}
	}

	declineReq := jsonRequest(t, http.MethodPost, decideTarget,
		map[string]string{"routeFamily": testspec.FamilyOrgs, "state": "declined", "confirmSlug": slug}, cookie, csrfToken)
	rec := ts.do(declineReq)
	if rec.Code != http.StatusConflict {
		t.Fatalf("decline parent with confirmed child: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "child_confirmed" {
		t.Errorf("error code = %q, want %q", code, "child_confirmed")
	}

	listReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d/resources", wsID), nil)
	listReq.AddCookie(cookie)
	listRec := ts.do(listReq)
	var listBody struct {
		Families []resourceFamilyWire `json:"families"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	for _, f := range listBody.Families {
		if f.RouteFamily == testspec.FamilyOrgs && (f.Decision == nil || *f.Decision != "confirmed") {
			t.Errorf("parent decision after refused decline = %v, want still \"confirmed\"", f.Decision)
		}
	}
}

// patchBasePath PATCHes wsID's settings.basePath and settings.basePathValues,
// keeping every other settings field byte-identical to whatever the
// workspace currently holds (PATCH replaces settings WHOLESALE) — GET
// first, mutate the two fields, PATCH back with the workspace's own
// current editVersion. Fails the test on anything but 200.
func (ts *testServer) patchBasePath(t *testing.T, cookie *http.Cookie, csrfToken string, wsID int64, basePath string, basePathValues []string) {
	t.Helper()
	getReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d", wsID), nil)
	getReq.AddCookie(cookie)
	getRec := ts.do(getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get workspace before base-path patch: status = %d; body = %s", getRec.Code, getRec.Body.String())
	}
	var current struct {
		Settings    map[string]any `json:"settings"`
		EditVersion int64          `json:"editVersion"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &current); err != nil {
		t.Fatalf("decode workspace before base-path patch: %v", err)
	}
	current.Settings["basePath"] = basePath
	current.Settings["basePathValues"] = basePathValues

	patchReq := jsonRequest(t, http.MethodPatch, fmt.Sprintf("http://mocker.local/api/workspaces/%d", wsID),
		map[string]any{"settings": current.Settings, "editVersion": current.EditVersion}, cookie, csrfToken)
	patchRec := ts.do(patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch base path to %q: status = %d, want 200; body = %s", basePath, patchRec.Code, patchRec.Body.String())
	}
}

// TestHandler_workspaceResources_byBaseScopeBreakdown is D13's own wire-level
// case for ResourceFamilyView.byBaseScope (P3h): a workspace declared over
// TWO base values ("7", "8") confirms FamilyWidgets, and GET
// .../resources must report one breakdown element per declared value, in
// declared order, each holding its own listSize-sized population — the
// family-wide entityCount stays the SUM (D10's own pre-P3h rule, unmoved),
// while byBaseScope is what actually tells the two populations apart.
func TestHandler_workspaceResources_byBaseScopeBreakdown(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Base Scope Demo", specID)
	ts.patchBasePath(t, cookie, csrfToken, wsID, "/tenants/{tenantId}", []string{"7", "8"})

	decideTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID)
	confirmReq := jsonRequest(t, http.MethodPost, decideTarget,
		map[string]string{"routeFamily": testspec.FamilyWidgets, "state": "confirmed"}, cookie, csrfToken)
	confirmRec := ts.do(confirmReq)
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("confirm: status = %d, want 200; body = %s", confirmRec.Code, confirmRec.Body.String())
	}
	var confirmBody struct {
		Family resourceFamilyWire `json:"family"`
	}
	if err := json.Unmarshal(confirmRec.Body.Bytes(), &confirmBody); err != nil {
		t.Fatalf("decode confirm response: %v", err)
	}
	// D6.2: entityCount stays the family-wide TOTAL across every base
	// scope — 2 declared values x listSize 5.
	if confirmBody.Family.EntityCount == nil || *confirmBody.Family.EntityCount != 10 {
		t.Fatalf("confirm response entityCount = %v, want 10 (2 declared base values x listSize 5)", confirmBody.Family.EntityCount)
	}
	assertBreakdown := func(t *testing.T, by []resourceBaseScopeCountWire) {
		t.Helper()
		if len(by) != 2 {
			t.Fatalf("byBaseScope = %+v, want exactly 2 elements (one per declared value)", by)
		}
		if by[0].BaseScope != "7" || by[1].BaseScope != "8" {
			t.Errorf("byBaseScope order = [%q, %q], want [\"7\", \"8\"] (declared order)", by[0].BaseScope, by[1].BaseScope)
		}
		if by[0].EntityCount != 5 || by[1].EntityCount != 5 {
			t.Errorf("byBaseScope counts = [%d, %d], want [5, 5] (listSize under each declared value)", by[0].EntityCount, by[1].EntityCount)
		}
	}
	assertBreakdown(t, confirmBody.Family.ByBaseScope)

	listReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d/resources", wsID), nil)
	listReq.AddCookie(cookie)
	listRec := ts.do(listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("GET resources: status = %d, want 200; body = %s", listRec.Code, listRec.Body.String())
	}
	var listBody struct {
		Families []resourceFamilyWire `json:"families"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &listBody); err != nil {
		t.Fatalf("decode list response: %v", err)
	}
	found := false
	for _, f := range listBody.Families {
		if f.RouteFamily != testspec.FamilyWidgets {
			continue
		}
		found = true
		assertBreakdown(t, f.ByBaseScope)
	}
	if !found {
		t.Fatalf("family %q missing from the list", testspec.FamilyWidgets)
	}
}

// TestHandler_decideResource_baseScopeUndeclared_refused is P3h D6.4 at the
// WIRE, and it exists because the domain half proves nothing about this
// layer: [resources.Repo.Confirm] produces ErrBaseScopeUndeclared and
// internal/resources tests it directly, but a sentinel with no case in
// [Server.answerResourceDecisionError] does not answer its own code — it
// falls to the default and ships a 500. That is exactly what this route did
// until the case was added: the tenth code was defined nowhere, and both
// callers of this dispatcher (the admin API and MCP through CallAsMCP) got
// "failed to record resource decision" with no way to tell an undeclared
// base scope from a bug in the server.
func TestHandler_decideResource_baseScopeUndeclared_refused(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Undeclared Base Scope", specID)
	// A parameterised basePath with an EMPTY declared set: legal to store
	// (D4.3 refuses a malformed shape, never an empty list), and the
	// confirm is what has nothing to populate.
	ts.patchBasePath(t, cookie, csrfToken, wsID, "/tenants/{tenantId}", nil)

	decideTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID)
	req := jsonRequest(t, http.MethodPost, decideTarget,
		map[string]string{"routeFamily": testspec.FamilyWidgets, "state": "confirmed"}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("confirm with an undeclared base scope: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "base_scope_undeclared" {
		t.Errorf("error code = %q, want %q", code, "base_scope_undeclared")
	}
}

// TestHandler_decideResource_configuredEntityCapReachesTheAdminDoor is P3h
// P14's admin half: MOCKER_MAX_ENTITIES is a CONFIGURED cap, and D11 wires
// it at both places a *resources.Repo is built — cmd/mocker/main.go for the
// mock plane and internal/admin/server.go for this one — so that a
// workspace's ceiling does not depend on which door a request came through.
// The property is about the value REACHING the constructor, so the fixture
// has to move it off its default: at 1000 (testConfig's own value, D11's
// default) no fixture this package can build would ever hit the cap, and a
// server that ignored cfg.MaxEntities entirely would be indistinguishable.
//
// Set to 3 against a listSize-5 population, the confirm must refuse with the
// existing 409 entity_limit and write nothing.
//
// The mock-plane door (cmd/mocker/main.go) is NOT observed here — that
// package has no tests and its wiring is only exercised end to end by
// scripts/smoke.sh, which runs the compose stack at the default value.
func TestHandler_decideResource_configuredEntityCapReachesTheAdminDoor(t *testing.T) {
	t.Parallel()
	ts := newTestServerCfg(t, func(cfg *config.Config) { cfg.MaxEntities = 3 })
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Tight Cap", specID)

	decideTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID)
	req := jsonRequest(t, http.MethodPost, decideTarget,
		map[string]string{"routeFamily": testspec.FamilyWidgets, "state": "confirmed"}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("confirm under a configured cap of 3 with listSize 5: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "entity_limit" {
		t.Errorf("error code = %q, want %q", code, "entity_limit")
	}
}

// ---------------------------------------------------------------------
// P3f: POST /api/specs/{id}/rederive (decisions.md mocker-p3f-rederive).
// ---------------------------------------------------------------------

// rederiveURL is POST /api/specs/{id}/rederive's own target.
func rederiveURL(specID int64) string {
	return fmt.Sprintf("http://mocker.local/api/specs/%d/rederive", specID)
}

// rederiveResultWire mirrors admin's unexported rederiveResultView — POST
// /api/specs/{id}/rederive's whole 200 body (D4.2).
type rederiveResultWire struct {
	Changed    bool     `json:"changed"`
	Generation int      `json:"generation"`
	Added      []string `json:"added"`
	Removed    []string `json:"removed"`
}

// rederive POSTs the rederive route and decodes a 200 body into
// rederiveResultWire. Callers that expect a non-200 status use ts.do and
// errorCode directly instead, the same split every other route in this
// file already makes.
func (ts *testServer) rederive(t *testing.T, cookie *http.Cookie, csrfToken string, specID int64) rederiveResultWire {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, rederiveURL(specID), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rederive spec %d: status = %d, want 200; body = %s", specID, rec.Code, rec.Body.String())
	}
	var out rederiveResultWire
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode rederive response: %v; body = %s", err, rec.Body.String())
	}
	return out
}

// seedSuggestionRow inserts one extra resource_suggestions row directly
// (decisions.md §D9's own fixture rule: the tree holds one
// deriveSuggestions, so a generation WIDER than what the current rules
// produce — one carrying a family the deriver does not — can only be
// produced this way, never by running the deriver a second, different way).
// gen defaults to 1 in every caller here: every fixture in this file seeds
// its widened row into the generation Import itself already wrote.
func (ts *testServer) seedSuggestionRow(t *testing.T, specID int64, gen int, routeFamily string) {
	t.Helper()
	// wrapper is NEVER a NULL column for a real row ([specs.Suggestion]'s
	// own doc comment: "Wrapper is never nil for a Suggestion this package
	// returns") — [resources.Repo]'s own row scan (unlike
	// [specs.scanResourceSuggestion]'s NullString) assumes that and fails
	// to load ANY suggestion of the spec, for an unrelated family, the
	// moment one row breaks it. A bare-array wrapper (both keys null) is
	// the shape a family with no wrapper object carries.
	if _, err := ts.db.W.ExecContext(t.Context(), `
		INSERT INTO resource_suggestions (spec_id, gen, route_family, name, id_field, entity_schema, wrapper, confidence)
		VALUES (?, ?, ?, ?, 'id', '{"type":"object"}', '{"arrayKey":null,"countKey":null}', 1.0)`,
		specID, gen, routeFamily, routeFamily); err != nil {
		t.Fatalf("seed suggestion row %q at gen %d for spec %d: %v", routeFamily, gen, specID, err)
	}
}

// deleteSuggestionRow removes one resource_suggestions row directly — the
// narrowing half of seedSuggestionRow's own fixture rule, used to simulate
// an OLD generation missing a family the current rules DO derive.
func (ts *testServer) deleteSuggestionRow(t *testing.T, specID int64, gen int, routeFamily string) {
	t.Helper()
	if _, err := ts.db.W.ExecContext(t.Context(),
		"DELETE FROM resource_suggestions WHERE spec_id = ? AND gen = ? AND route_family = ?",
		specID, gen, routeFamily); err != nil {
		t.Fatalf("delete suggestion row %q at gen %d for spec %d: %v", routeFamily, gen, specID, err)
	}
}

// TestHandler_rederive_addedRemovedBothDirections is P6: added/removed are
// the diff of two generations, in BOTH directions, over three seeded
// transitions of the SAME derivation spec (widgets, bareitems).
func TestHandler_rederive_addedRemovedBothDirections(t *testing.T) {
	t.Parallel()

	t.Run("narrower: generation 1 missing a family the current rules derive", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")
		specID := ts.importDerivationSpec(t, cookie, csrfToken)
		ts.deleteSuggestionRow(t, specID, 1, testspec.FamilyBareItems)

		out := ts.rederive(t, cookie, csrfToken, specID)
		if !out.Changed {
			t.Fatalf("Changed = false, want true (bareitems is missing from generation 1)")
		}
		if !slices.Equal(out.Added, []string{testspec.FamilyBareItems}) {
			t.Errorf("Added = %v, want [%s]", out.Added, testspec.FamilyBareItems)
		}
		if len(out.Removed) != 0 {
			t.Errorf("Removed = %v, want empty", out.Removed)
		}
	})

	t.Run("wider: generation 1 carries a family the current rules do not derive", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")
		specID := ts.importDerivationSpec(t, cookie, csrfToken)
		ts.seedSuggestionRow(t, specID, 1, "/decoy-wide")

		out := ts.rederive(t, cookie, csrfToken, specID)
		if !out.Changed {
			t.Fatalf("Changed = false, want true (generation 1 carries an extra family)")
		}
		if len(out.Added) != 0 {
			t.Errorf("Added = %v, want empty", out.Added)
		}
		if !slices.Equal(out.Removed, []string{"/decoy-wide"}) {
			t.Errorf("Removed = %v, want [/decoy-wide]", out.Removed)
		}
	})

	t.Run("mixed: generation 1 both misses a real family and carries a fake one", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")
		specID := ts.importDerivationSpec(t, cookie, csrfToken)
		ts.deleteSuggestionRow(t, specID, 1, testspec.FamilyBareItems)
		ts.seedSuggestionRow(t, specID, 1, "/decoy-mixed")

		out := ts.rederive(t, cookie, csrfToken, specID)
		if !out.Changed {
			t.Fatalf("Changed = false, want true")
		}
		if !slices.Equal(out.Added, []string{testspec.FamilyBareItems}) {
			t.Errorf("Added = %v, want [%s]", out.Added, testspec.FamilyBareItems)
		}
		if !slices.Equal(out.Removed, []string{"/decoy-mixed"}) {
			t.Errorf("Removed = %v, want [/decoy-mixed]", out.Removed)
		}
	})
}

// TestHandler_rederive_ignoresWhatIsConfirmed is P7: the response does not
// depend on what any workspace confirmed. A confirmed family the new
// generation drops still appears in removed, byte-identical to the answer
// the SAME transition produces with nothing confirmed anywhere.
func TestHandler_rederive_ignoresWhatIsConfirmed(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")

	// Spec A: the decoy family is confirmed in a workspace before the drop.
	// Spec B is the byte-different twin (docWithSuffix's own trick,
	// mirroring internal/specs/rederive_test.go's: Import dedupes by
	// sha256, so importing the SAME bytes twice would hand back the SAME
	// spec_id rather than two independent specs) carrying the identical
	// transition with nothing confirmed anywhere.
	specA := ts.importDerivationSpec(t, cookie, csrfToken)
	ts.seedSuggestionRow(t, specA, 1, "/decoy-p7")
	wsA := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "P7 Confirmed", specA)
	confirmDecoyDirectly(t, ts, wsA, "/decoy-p7")

	specB := ts.importSpecWithSession(t, cookie, csrfToken, "Derivation Twin", string(docWithSuffix(t, testspec.DerivationDoc(), `"x-p7-twin":true`)))
	ts.seedSuggestionRow(t, specB, 1, "/decoy-p7")

	outA := ts.rederive(t, cookie, csrfToken, specA)
	outB := ts.rederive(t, cookie, csrfToken, specB)

	// Generation numbers are identical by construction (both specs start
	// fresh and take exactly one rederive each), so the two results compare
	// field by field, not just on Added/Removed — a confirmed decoy must
	// not change ANY of them.
	if outA.Changed != outB.Changed || outA.Generation != outB.Generation ||
		!slices.Equal(outA.Added, outB.Added) || !slices.Equal(outA.Removed, outB.Removed) {
		t.Errorf("rederive with a confirmed decoy = %+v, want byte-identical to nothing confirmed = %+v", outA, outB)
	}
}

// docWithSuffix returns doc with an irrelevant trailing field spliced in
// right before its closing brace, changing its sha256 so [specs.Repo.Import]
// mints a genuinely new spec_id without changing what the document declares
// — the identical trick internal/specs/rederive_test.go's own unexported
// helper uses, copied here rather than imported (unexported, different
// package).
func docWithSuffix(t *testing.T, doc []byte, field string) []byte {
	t.Helper()
	return append(slices.Clone(doc[:len(doc)-1]), []byte(","+field+"}")...)
}

// confirmDecoyDirectly inserts resources/resource_decisions/entities rows
// for routeFamily in wsID by direct SQL, bypassing the HTTP confirm route
// entirely: the family is manufactured (seedSuggestionRow's own doc
// comment) and carries no real matching routes in the bound spec's
// document, so [resources.Repo.Confirm]'s own route lookup would refuse it
// — exactly what a real operator who confirmed a family a LATER, stricter
// derivation rule then dropped could never have hit, but is state this
// slice's own properties (P7, P8) need to observe regardless of how it
// arose.
func confirmDecoyDirectly(t *testing.T, ts *testServer, wsID int64, routeFamily string) int64 {
	t.Helper()
	now := time.Now().Unix()
	res, err := ts.db.W.ExecContext(t.Context(), `
		INSERT INTO resources (workspace_id, route_family, name, id_field, entity_schema, write_form)
		VALUES (?, ?, ?, 'id', '{"type":"object"}', 'bare')`, wsID, routeFamily, routeFamily)
	if err != nil {
		t.Fatalf("insert decoy resource %q for workspace %d: %v", routeFamily, wsID, err)
	}
	resourceID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("decoy resource insert id: %v", err)
	}
	if _, err := ts.db.W.ExecContext(t.Context(),
		"INSERT INTO resource_decisions (workspace_id, route_family, state) VALUES (?, ?, 'confirmed')",
		wsID, routeFamily); err != nil {
		t.Fatalf("insert decoy resource_decisions %q for workspace %d: %v", routeFamily, wsID, err)
	}
	if _, err := ts.db.W.ExecContext(t.Context(), `
		INSERT INTO entities (resource_id, entity_key, data, created_at, updated_at)
		VALUES (?, '1', '{"id":1}', ?, ?)`, resourceID, now, now); err != nil {
		t.Fatalf("insert decoy entity for resource %d: %v", resourceID, err)
	}
	return resourceID
}

// dumpAllTablesExcept reads EVERY table of the schema except the ones
// named, each ordered by rowid (every table in this schema is an ordinary
// rowid table — none declares WITHOUT ROWID), and returns one comparable
// string per row. The table population itself is read from the database at
// run time (decisions.md §D9's own requirement for this property: "a list
// a human maintains has already been short by one twice in this gate"),
// never a Go slice of table names copied out of a migration.
func dumpAllTablesExcept(t *testing.T, ts *testServer, exclude ...string) map[string][]string {
	t.Helper()
	excl := make(map[string]bool, len(exclude))
	for _, name := range exclude {
		excl[name] = true
	}

	rows, err := ts.db.R.QueryContext(t.Context(),
		"SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table names: %v", err)
	}
	_ = rows.Close()

	out := make(map[string][]string, len(tables))
	for _, table := range tables {
		if excl[table] {
			continue
		}
		out[table] = dumpTableRows(t, ts, table)
	}
	return out
}

// dumpTableRows reads every row of table, ordered by rowid, as one
// comparable string per row — a generic scan (database/sql's own
// column-count-agnostic `any` pointers), since this file's tables have
// nothing in common but being SQLite rowid tables.
func dumpTableRows(t *testing.T, ts *testServer, table string) []string {
	t.Helper()
	//nolint:gosec // table comes from sqlite_master itself (dumpAllTablesExcept), never request input.
	rows, err := ts.db.R.QueryContext(t.Context(), fmt.Sprintf("SELECT * FROM %s ORDER BY rowid", table))
	if err != nil {
		t.Fatalf("dump table %q: %v", table, err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatalf("columns of %q: %v", table, err)
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatalf("scan row of %q: %v", table, err)
		}
		out = append(out, fmt.Sprintf("%v", vals))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate rows of %q: %v", table, err)
	}
	return out
}

// assertTablesUnchanged compares two dumpAllTablesExcept results table by
// table, failing on any table set mismatch or any row difference within a
// table both dumps share.
func assertTablesUnchanged(t *testing.T, before, after map[string][]string) {
	t.Helper()
	beforeTables := make([]string, 0, len(before))
	for table := range before {
		beforeTables = append(beforeTables, table)
	}
	afterTables := make([]string, 0, len(after))
	for table := range after {
		afterTables = append(afterTables, table)
	}
	sort.Strings(beforeTables)
	sort.Strings(afterTables)
	if !slices.Equal(beforeTables, afterTables) {
		t.Fatalf("table set changed: before = %v, after = %v", beforeTables, afterTables)
	}
	for _, table := range beforeTables {
		if !slices.Equal(before[table], after[table]) {
			t.Errorf("table %q rows changed:\n before = %v\n after  = %v", table, before[table], after[table])
		}
	}
}

// TestHandler_rederive_leavesEveryWorkspaceRowUntouched is P8: two
// workspaces bound to the spec, each carrying a confirmed family, a
// scenario, an operation override, a custom endpoint, a traffic record and
// a checkpoint — every table of the schema except resource_suggestions
// dumped, a rederive run that DROPS one of the two confirmed families,
// dumped again, and the two dumps asserted byte-identical. This also
// subsumes D7.4's promise that no scenario or checkpoint snapshot changes
// (nothing else in this file observes that directly) and P17's revision
// claim for these two workspaces specifically (P17 below is the dedicated,
// named property).
func TestHandler_rederive_leavesEveryWorkspaceRowUntouched(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	ts.seedSuggestionRow(t, specID, 1, "/decoy-p8")

	ws1 := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "P8 One", specID)
	ws2 := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "P8 Two", specID)

	// ws1's own confirmed family is the manufactured one the rederive is
	// about to drop; ws2's is a real, always-derivable one that survives.
	confirmDecoyDirectly(t, ts, ws1, "/decoy-p8")
	decideTarget := func(wsID int64) string {
		return fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID)
	}
	confirmReq := jsonRequest(t, http.MethodPost, decideTarget(ws2),
		map[string]string{"routeFamily": testspec.FamilyWidgets, "state": "confirmed"}, cookie, csrfToken)
	if rec := ts.do(confirmReq); rec.Code != http.StatusOK {
		t.Fatalf("confirm %q in workspace %d: status = %d, want 200; body = %s", testspec.FamilyWidgets, ws2, rec.Code, rec.Body.String())
	}

	// Every OTHER table the fixture is supposed to populate, for both
	// workspaces: a scenario, an operation override, a custom endpoint, a
	// traffic record (direct SQL — this package's own fixtures never wire
	// a live traffic.Recorder, and a raw insert exercises exactly the row
	// this property cares about without needing one) and a checkpoint.
	opKey := firstOperationOpKey(t, ts, cookie, ws1)
	populateOtherWorkspaceTables := func(wsID int64) {
		t.Helper()
		scenarioTestCreate(t, ts, cookie, csrfToken, wsID, "p8-scenario", http.StatusCreated)

		putReq := jsonRequest(t, http.MethodPut,
			fmt.Sprintf("http://mocker.local/api/workspaces/%d/operations/%s", wsID, opKey),
			map[string]any{"overrideOn": true, "routeOff": false, "responses": map[string]any{}, "editVersion": 0},
			cookie, csrfToken)
		if rec := ts.do(putReq); rec.Code != http.StatusOK {
			t.Fatalf("put override for workspace %d: status = %d, want 200; body = %s", wsID, rec.Code, rec.Body.String())
		}

		epReq := jsonRequest(t, http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/endpoints", wsID),
			map[string]any{"method": "GET", "path": "/p8-custom"}, cookie, csrfToken)
		if rec := ts.do(epReq); rec.Code != http.StatusCreated {
			t.Fatalf("create custom endpoint for workspace %d: status = %d, want 201; body = %s", wsID, rec.Code, rec.Body.String())
		}

		if _, err := ts.db.W.ExecContext(t.Context(), `
			INSERT INTO traffic (workspace_id, ts, method, path, status, duration_ms)
			VALUES (?, ?, 'GET', '/p8-traffic', 200, 1.5)`, wsID, time.Now().Unix()); err != nil {
			t.Fatalf("insert traffic row for workspace %d: %v", wsID, err)
		}

		checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "p8-checkpoint", http.StatusCreated)
	}
	populateOtherWorkspaceTables(ws1)
	populateOtherWorkspaceTables(ws2)

	before := dumpAllTablesExcept(t, ts, "resource_suggestions")

	out := ts.rederive(t, cookie, csrfToken, specID)
	if !slices.Contains(out.Removed, "/decoy-p8") {
		t.Fatalf("Removed = %v, want it to name /decoy-p8 (this test's own precondition)", out.Removed)
	}

	after := dumpAllTablesExcept(t, ts, "resource_suggestions")
	assertTablesUnchanged(t, before, after)
}

// firstOperationOpKey reads wsID's first listed operation's opKey (already
// percent-encoded, CLAUDE.md's own note on MergedOperationView.opKey) — used
// wherever a fixture needs SOME real opKey to PUT an override against and
// does not care which operation it names.
func firstOperationOpKey(t *testing.T, ts *testServer, cookie *http.Cookie, wsID int64) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d/operations", wsID), nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list operations for workspace %d: status = %d, want 200; body = %s", wsID, rec.Code, rec.Body.String())
	}
	var ops []struct {
		OpKey string `json:"opKey"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ops); err != nil {
		t.Fatalf("decode operations list: %v", err)
	}
	if len(ops) == 0 {
		t.Fatalf("workspace %d has no operations to pick an opKey from", wsID)
	}
	return ops[0].OpKey
}

// TestHandler_rederive_bumpsNoRevision is P17: a rederive bumps no
// workspaces.revision, the shape
// TestCheckpoints_createAnswersSummaryAndDoesNotBumpRevision already uses
// for the analogous checkpoint-create handler.
func TestHandler_rederive_bumpsNoRevision(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	ts.seedSuggestionRow(t, specID, 1, "/decoy-p17")
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "P17 Demo", specID)

	before := workspaceRevision(t, ts, cookie, wsID)
	out := ts.rederive(t, cookie, csrfToken, specID)
	if !out.Changed {
		t.Fatalf("Changed = false, want true (this test's own precondition)")
	}
	after := workspaceRevision(t, ts, cookie, wsID)
	if after != before {
		t.Errorf("revision after rederive = %d, want unchanged from %d", after, before)
	}
}

// TestHandler_rederive_unknownSpec is P12's own 404 clause: a spec id that
// parses but names nothing, the shape
// TestHandler_resourceSuggestions_unknownSpec already pins for the sibling
// GET.
func TestHandler_rederive_unknownSpec(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")

	req := jsonRequest(t, http.MethodPost, rederiveURL(999999), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("rederive unknown spec: status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

// TestHandler_rederive_noSentinelOnTheWire is P16: a generation that gains
// or loses the sentinel row puts no empty family name on the wire, in
// either transition.
func TestHandler_rederive_noSentinelOnTheWire(t *testing.T) {
	t.Parallel()

	t.Run("generation 1 is sentinel-only, current rules derive real families", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")
		specID := ts.importDerivationSpec(t, cookie, csrfToken)
		ts.deleteSuggestionRow(t, specID, 1, testspec.FamilyWidgets)
		ts.deleteSuggestionRow(t, specID, 1, testspec.FamilyBareItems)
		ts.seedSuggestionRow(t, specID, 1, "")

		out := ts.rederive(t, cookie, csrfToken, specID)
		wantAdded := []string{testspec.FamilyBareItems, testspec.FamilyWidgets}
		gotAdded := slices.Clone(out.Added)
		sort.Strings(gotAdded)
		if !slices.Equal(gotAdded, wantAdded) {
			t.Errorf("Added = %v, want %v", out.Added, wantAdded)
		}
		if len(out.Removed) != 0 {
			t.Errorf("Removed = %v, want empty", out.Removed)
		}
		if slices.Contains(out.Added, "") || slices.Contains(out.Removed, "") {
			t.Errorf("empty string on the wire: Added = %v, Removed = %v", out.Added, out.Removed)
		}
	})

	t.Run("generation 1 holds real families, current rules derive none", func(t *testing.T) {
		t.Parallel()
		ts := newTestServer(t)
		cookie, csrfToken := ts.login(t, "Alex")
		// A document declaring NO paths at all — [deriveSuggestions] over
		// it derives nothing, the same emptiness
		// internal/specs/rederive_test.go's own rederiveEmptyDoc fixture
		// gives P25 — imported here fresh (not that unexported constant:
		// a different package's own test-only fixture) so this
		// subtest's "current rules derive none" is a REAL derivation
		// outcome, not a DB row deleted out from under a document that
		// would just be re-derived on the next call.
		specID := ts.importSpecWithSession(t, cookie, csrfToken, "Empty", emptyPathsDoc)
		ts.seedSuggestionRow(t, specID, 1, testspec.FamilyWidgets)
		ts.seedSuggestionRow(t, specID, 1, testspec.FamilyBareItems)

		out := ts.rederive(t, cookie, csrfToken, specID)
		wantRemoved := []string{testspec.FamilyBareItems, testspec.FamilyWidgets}
		gotRemoved := slices.Clone(out.Removed)
		sort.Strings(gotRemoved)
		if !slices.Equal(gotRemoved, wantRemoved) {
			t.Errorf("Removed = %v, want %v", out.Removed, wantRemoved)
		}
		if len(out.Added) != 0 {
			t.Errorf("Added = %v, want empty", out.Added)
		}
		if slices.Contains(out.Added, "") || slices.Contains(out.Removed, "") {
			t.Errorf("empty string on the wire: Added = %v, Removed = %v", out.Added, out.Removed)
		}
	})
}

// TestHandler_rederiveResponse_carriesNoGeneration is P19: no generation
// reaches the SUGGESTION wire — GET /api/specs/{id}/resource-suggestions'
// own row shape stays exactly routeFamily/name/idField/confidence, decoded
// with DisallowUnknownFields so an extra key fails to decode rather than
// being silently ignored.
func TestHandler_rederiveResponse_carriesNoGeneration(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	// A rederive first, so the wire is exercised against a spec that has
	// actually been rederived at least once — a generation field leaking
	// in is most likely right after the verb that introduces the concept,
	// not on a spec that never called it.
	ts.rederive(t, cookie, csrfToken, specID)

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/specs/%d/resource-suggestions", specID), nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list resource suggestions: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Suggestions []map[string]json.RawMessage `json:"suggestions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode suggestions response: %v", err)
	}
	if len(body.Suggestions) == 0 {
		t.Fatalf("no suggestions returned to check key sets against")
	}
	wantKeys := []string{"confidence", "idField", "name", "routeFamily"}
	for _, sugg := range body.Suggestions {
		gotKeys := make([]string, 0, len(sugg))
		for k := range sugg {
			gotKeys = append(gotKeys, k)
		}
		sort.Strings(gotKeys)
		if !slices.Equal(gotKeys, wantKeys) {
			t.Errorf("suggestion key set = %v, want exactly %v", gotKeys, wantKeys)
		}
	}
}

// --- D4: GET /api/workspaces/{id}/resources/{family}/entities ------------

// resourceEntityWire mirrors admin's unexported resourceEntityView.
type resourceEntityWire struct {
	ID           int64           `json:"id"`
	EntityKey    string          `json:"entityKey"`
	ScopeKey     string          `json:"scopeKey"`
	BaseScopeKey string          `json:"baseScopeKey"`
	Data         json.RawMessage `json:"data"`
	CreatedAt    time.Time       `json:"createdAt"`
	UpdatedAt    time.Time       `json:"updatedAt"`
}

// resourceEntitiesWire mirrors admin's unexported resourceEntitiesView.
type resourceEntitiesWire struct {
	Rows   []resourceEntityWire `json:"rows"`
	LastID int64                `json:"lastId"`
}

// listResourceEntities calls GET .../resources/{family}/entities, escaping
// family the same way the wire contract requires a client to (D4's own
// Addressing note) — url.PathEscape, never a raw join, so a family carrying
// its own leading "/" (every family does) or an internal "/{}" (a nested
// one) reaches the server as one path segment. query is appended verbatim
// (already-encoded key=value&... pairs, or "" for none).
func (ts *testServer) listResourceEntities(t *testing.T, cookie *http.Cookie, wsID int64, family, query string) (*httptest.ResponseRecorder, resourceEntitiesWire) {
	t.Helper()
	target := fmt.Sprintf("http://mocker.local/api/workspaces/%d/resources/%s/entities", wsID, url.PathEscape(family))
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	var body resourceEntitiesWire
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode entities response: %v", err)
		}
	}
	return rec, body
}

// patchListSize PATCHes wsID's settings.listSize, keeping every other
// settings field byte-identical — the same GET-mutate-PATCH-back shape
// patchBasePath already uses, for the same reason (PATCH replaces settings
// WHOLESALE).
func (ts *testServer) patchListSize(t *testing.T, cookie *http.Cookie, csrfToken string, wsID int64, listSize int) {
	t.Helper()
	getReq := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d", wsID), nil)
	getReq.AddCookie(cookie)
	getRec := ts.do(getReq)
	if getRec.Code != http.StatusOK {
		t.Fatalf("get workspace before listSize patch: status = %d; body = %s", getRec.Code, getRec.Body.String())
	}
	var current struct {
		Settings    map[string]any `json:"settings"`
		EditVersion int64          `json:"editVersion"`
	}
	if err := json.Unmarshal(getRec.Body.Bytes(), &current); err != nil {
		t.Fatalf("decode workspace before listSize patch: %v", err)
	}
	current.Settings["listSize"] = listSize

	patchReq := jsonRequest(t, http.MethodPatch, fmt.Sprintf("http://mocker.local/api/workspaces/%d", wsID),
		map[string]any{"settings": current.Settings, "editVersion": current.EditVersion}, cookie, csrfToken)
	patchRec := ts.do(patchReq)
	if patchRec.Code != http.StatusOK {
		t.Fatalf("patch listSize to %d: status = %d, want 200; body = %s", listSize, patchRec.Code, patchRec.Body.String())
	}
}

// confirmFamilyForEntities confirms family over wsID and fails the test on
// anything but 200 — the shared setup step every test below needs before it
// can read any entity row back.
func (ts *testServer) confirmFamilyForEntities(t *testing.T, cookie *http.Cookie, csrfToken string, wsID int64, family string) {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID),
		map[string]string{"routeFamily": family, "state": "confirmed"}, cookie, csrfToken)
	if rec := ts.do(req); rec.Code != http.StatusOK {
		t.Fatalf("confirm %q: status = %d, want 200; body = %s", family, rec.Code, rec.Body.String())
	}
}

// TestHandler_listResourceEntities_unknownFamily is D4's own error taxonomy:
// a family never suggested at all, and a family that WAS confirmed and then
// declined, both answer the identical 404 unknown_family — one result for
// every cause, never distinguished by a second query.
func TestHandler_listResourceEntities_unknownFamily(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Entities Demo", specID)

	rec, _ := ts.listResourceEntities(t, cookie, wsID, "/never-suggested", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("never-suggested family: status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
	if code := errorCode(t, rec); code != "unknown_family" {
		t.Errorf("never-suggested family: error code = %q, want %q", code, "unknown_family")
	}

	ts.confirmFamilyForEntities(t, cookie, csrfToken, wsID, testspec.FamilyWidgets)
	slug := ts.workspaceSlug(t, cookie, wsID)
	declineReq := jsonRequest(t, http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID),
		map[string]string{"routeFamily": testspec.FamilyWidgets, "state": "declined", "confirmSlug": slug}, cookie, csrfToken)
	if rec := ts.do(declineReq); rec.Code != http.StatusOK {
		t.Fatalf("decline %q: status = %d, want 200; body = %s", testspec.FamilyWidgets, rec.Code, rec.Body.String())
	}

	rec2, _ := ts.listResourceEntities(t, cookie, wsID, testspec.FamilyWidgets, "")
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("declined family: status = %d, want 404; body = %s", rec2.Code, rec2.Body.String())
	}
	if code := errorCode(t, rec2); code != "unknown_family" {
		t.Errorf("declined family: error code = %q, want %q (same taxonomy as never-suggested)", code, "unknown_family")
	}
}

// TestHandler_listResourceEntities_nestedFamily_resolvesByFamilyAndScopes is
// D4's own fixture: a confirmed NESTED family ("/orgs/{}/users") at
// listSize=15 — at least two live parent rows and more than nine rows in one
// scope, the shape the decision document itself requires because
// confirmDeepChain's own six call sites all leave it vacuous at the default
// listSize of 5. It asserts three of D4's Fails-if clauses at once: the
// family resolves and answers 200 with at least one row (never resolved
// through resources.id — [Server.confirmedResourceByFamily] addresses it by
// route_family alone); a scopeKey filter never leaks a row of the fixture's
// OTHER scope; and requesting past the ninth row of a >9-row scope neither
// omits nor reorders a row — the clause that exists to catch the TEXT
// (entity_key) collation defect D4's own Shape describes, which a dozen-row
// fixture cannot exercise at all.
func TestHandler_listResourceEntities_nestedFamily_resolvesByFamilyAndScopes(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importNestedSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Nested Entities Demo", specID)
	ts.patchListSize(t, cookie, csrfToken, wsID, 15) // P (orgs) = 15, L (users per org) = 15.

	ts.confirmFamilyForEntities(t, cookie, csrfToken, wsID, testspec.FamilyOrgs)
	ts.confirmFamilyForEntities(t, cookie, csrfToken, wsID, testspec.FamilyOrgUsers)

	rec, body := ts.listResourceEntities(t, cookie, wsID, testspec.FamilyOrgUsers, "limit=500")
	if rec.Code != http.StatusOK {
		t.Fatalf("list entities for confirmed nested family: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(body.Rows) == 0 {
		t.Fatalf("confirmed nested family answered 200 with zero rows, want at least one")
	}

	byScope := make(map[string][]resourceEntityWire)
	for _, row := range body.Rows {
		byScope[row.ScopeKey] = append(byScope[row.ScopeKey], row)
	}
	if len(byScope) < 2 {
		t.Fatalf("distinct scopes among returned rows = %d, want at least 2 (fixture requires >=2 live parent rows)", len(byScope))
	}

	var scopeX, scopeY string
	var rowsX []resourceEntityWire
	for scope, rows := range byScope {
		if len(rows) > 9 {
			scopeX, rowsX = scope, rows
		} else if scopeY == "" || scope != scopeX {
			scopeY = scope
		}
	}
	if rowsX == nil {
		t.Fatalf("no scope among %v held more than 9 rows, want at least one (fixture requires listSize=15 per scope)", byScope)
	}
	if scopeY == "" || scopeY == scopeX {
		for scope := range byScope {
			if scope != scopeX {
				scopeY = scope
				break
			}
		}
	}

	// scopeKey filter: every row answered must belong to scopeX, never to
	// scopeY (D4's own Fails-if: "a row belonging to the fixture's OTHER
	// scope").
	filterRec, filterBody := ts.listResourceEntities(t, cookie, wsID, testspec.FamilyOrgUsers,
		"scopeKey="+url.QueryEscape(scopeX)+"&limit=500")
	if filterRec.Code != http.StatusOK {
		t.Fatalf("list entities with scopeKey filter: status = %d, want 200; body = %s", filterRec.Code, filterRec.Body.String())
	}
	if len(filterBody.Rows) != len(rowsX) {
		t.Errorf("scopeKey=%q returned %d rows, want %d (every row of that scope, exactly)", scopeX, len(filterBody.Rows), len(rowsX))
	}
	for _, row := range filterBody.Rows {
		if row.ScopeKey != scopeX {
			t.Errorf("scopeKey=%q returned a row from scope %q (%s belongs to scope %q's fixture)", scopeX, row.ScopeKey, scopeY, scopeY)
		}
	}

	// Cursor: rowsX is already ordered by id ASC (Repo.ListFiltered's own
	// ORDER BY). Cursor past the 9th row must return exactly rowsX[9:], in
	// order — never omitted, never reordered, regardless of what
	// entity_key's own decimal string sorts as.
	sort.Slice(rowsX, func(i, j int) bool { return rowsX[i].ID < rowsX[j].ID })
	pivot := rowsX[8] // the 9th row.
	afterRec, afterBody := ts.listResourceEntities(t, cookie, wsID, testspec.FamilyOrgUsers,
		"scopeKey="+url.QueryEscape(scopeX)+"&after="+strconv.FormatInt(pivot.ID, 10)+"&limit=500")
	if afterRec.Code != http.StatusOK {
		t.Fatalf("list entities after cursor: status = %d, want 200; body = %s", afterRec.Code, afterRec.Body.String())
	}
	want := rowsX[9:]
	if len(afterBody.Rows) != len(want) {
		t.Fatalf("after=%d returned %d rows, want %d (rowsX[9:], exactly)", pivot.ID, len(afterBody.Rows), len(want))
	}
	for i, row := range afterBody.Rows {
		if row.ID != want[i].ID || row.EntityKey != want[i].EntityKey {
			t.Errorf("after=%d row %d = {id:%d entityKey:%q}, want {id:%d entityKey:%q} (order or membership disturbed past the 9th row)",
				pivot.ID, i, row.ID, row.EntityKey, want[i].ID, want[i].EntityKey)
		}
	}
	if afterBody.LastID != want[len(want)-1].ID {
		t.Errorf("lastId = %d, want %d (the highest id in the page)", afterBody.LastID, want[len(want)-1].ID)
	}
}

// resourceEntitiesPopulateRaw inserts n extra entity rows directly against
// resourceID's default (empty) scope, bypassing Confirm entirely — D4's own
// text says why: the two clamp clauses need a population above 500, which a
// real confirm's own listSize ceiling (MOCKER_MAX_ENTITIES) cannot reach
// through this fixture's spec, so "the test may build the rows directly
// rather than through a confirm". entity_key values start at startKey and
// count up as unpadded decimal strings, distinct from whatever a prior
// confirm already minted, so the UNIQUE(resource_id, scope_key, entity_key)
// constraint holds.
func (ts *testServer) resourceEntitiesPopulateRaw(t *testing.T, resourceID int64, startKey, n int) {
	t.Helper()
	for i := range n {
		key := strconv.Itoa(startKey + i)
		if _, err := ts.db.W.ExecContext(t.Context(),
			"INSERT INTO entities (resource_id, scope_key, entity_key, data, created_at, updated_at) VALUES (?, '', ?, '{}', 0, 0)",
			resourceID, key,
		); err != nil {
			t.Fatalf("insert raw entity row %q: %v", key, err)
		}
	}
	t.Cleanup(func() {
		if _, err := ts.db.W.ExecContext(context.Background(),
			"DELETE FROM entities WHERE resource_id = ? AND CAST(entity_key AS INTEGER) >= ?", resourceID, startKey,
		); err != nil {
			t.Fatalf("clean up raw entity rows: %v", err)
		}
	})
}

// TestHandler_listResourceEntities_defaultLimitClamps100 is D4's first clamp
// clause: against a population above 100 rows in one scope, a request with
// no "limit" returns at most 100 — never all of them.
func TestHandler_listResourceEntities_defaultLimitClamps100(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Clamp Demo", specID)
	ts.confirmFamilyForEntities(t, cookie, csrfToken, wsID, testspec.FamilyWidgets)
	resourceID := checkpointTestResourceID(t, ts, wsID, testspec.FamilyWidgets)
	ts.resourceEntitiesPopulateRaw(t, resourceID, 100000, 150) // + the confirm's own default-listSize seed, well above 100.

	rec, body := ts.listResourceEntities(t, cookie, wsID, testspec.FamilyWidgets, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("list entities with no limit: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(body.Rows) != 100 {
		t.Errorf("rows with no limit against a >100-row scope = %d, want exactly 100 (the default clamp)", len(body.Rows))
	}
}

// TestHandler_listResourceEntities_maxLimitClamps500 is D4's second clamp
// clause: against a population above 500 rows in one scope, limit=100000
// returns at most 500 — never a 400, and never more than the ceiling.
func TestHandler_listResourceEntities_maxLimitClamps500(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Clamp Demo", specID)
	ts.confirmFamilyForEntities(t, cookie, csrfToken, wsID, testspec.FamilyWidgets)
	resourceID := checkpointTestResourceID(t, ts, wsID, testspec.FamilyWidgets)
	ts.resourceEntitiesPopulateRaw(t, resourceID, 200000, 600) // well above 500.

	rec, body := ts.listResourceEntities(t, cookie, wsID, testspec.FamilyWidgets, "limit=100000")
	if rec.Code != http.StatusOK {
		t.Fatalf("list entities with limit=100000: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	if len(body.Rows) != 500 {
		t.Errorf("rows with limit=100000 against a >500-row scope = %d, want exactly 500 (the max clamp)", len(body.Rows))
	}
}
