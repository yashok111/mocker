package admin_test

import (
	"encoding/json"
	"net/http"
	"testing"
)

// listWorkspaces GETs the caller's own workspaces and decodes just the two
// fields these tests need (slug, specId) — the full workspaceView shape is
// already exercised by workspace_handlers_test.go elsewhere in this package.
func (ts *testServer) listWorkspaces(t *testing.T, cookie *http.Cookie) []struct {
	Slug   string `json:"slug"`
	SpecID *int64 `json:"specId"`
} {
	t.Helper()
	req := jsonRequest(t, http.MethodGet, "http://mocker.local/api/workspaces", nil, cookie, "")
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list workspaces: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var out []struct {
		Slug   string `json:"slug"`
		SpecID *int64 `json:"specId"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode workspace list: %v", err)
	}
	return out
}

// TestHandler_login_defaultSpec_unset is the "must be unaffected" half of
// the DEBT this feature closes: MOCKER_DEFAULT_SPEC unset (the zero value
// [testConfig] already leaves it at) must behave exactly as it does today —
// no workspace materializes for a brand-new user just because they logged
// in.
func TestHandler_login_defaultSpec_unset(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	if ts.cfg.DefaultSpecID != 0 {
		t.Fatalf("test precondition: DefaultSpecID = %d, want 0 (unset)", ts.cfg.DefaultSpecID)
	}

	cookie, _ := ts.login(t, "Alex No Default Spec")
	got := ts.listWorkspaces(t, cookie)
	if len(got) != 0 {
		t.Fatalf("brand-new user has %d workspaces with MOCKER_DEFAULT_SPEC unset, want 0", len(got))
	}
}

// TestHandler_login_defaultSpec_autoCreatesOnce covers DESIGN §14 screen
// 2's "Первый вход": a brand-new user's first login, with MOCKER_DEFAULT_SPEC
// set, gets exactly one workspace against that spec — visible through the
// SAME GET /api/workspaces call the panel already makes after login, with no
// extra route needed to "show" the picked slug — and a second login by the
// same user (now no longer zero-workspace) does not create a second one.
func TestHandler_login_defaultSpec_autoCreatesOnce(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)

	// Import a spec first (as an unrelated user — importing a spec never
	// touches workspaces), THEN point DefaultSpecID at its freshly assigned
	// id: an operator cannot know that id before import either, which is
	// exactly why [config.Config.DefaultSpecID]'s doc comment picks id over
	// name or a document path. ts.cfg is the SAME *config.Config pointer
	// admin.Server holds (admin.New never copies it), so mutating a field on
	// it here is visible to every subsequent request through this handler.
	_, _, specID := ts.importSpec(t, "Spec Importer", "Widgets API", minimalSpecDoc)
	ts.cfg.DefaultSpecID = specID

	cookie, _ := ts.login(t, "Alex First Login")
	got := ts.listWorkspaces(t, cookie)
	if len(got) != 1 {
		t.Fatalf("brand-new user has %d workspaces after first login, want 1", len(got))
	}
	if got[0].Slug == "" {
		t.Error("auto-created workspace has an empty slug — DESIGN §14 requires the picked slug be shown")
	}
	if got[0].SpecID == nil || *got[0].SpecID != specID {
		t.Errorf("auto-created workspace SpecID = %v, want %d", got[0].SpecID, specID)
	}

	// Second login, same user: no longer zero-workspace, so nothing new.
	cookie2, _ := ts.login(t, "Alex First Login")
	got2 := ts.listWorkspaces(t, cookie2)
	if len(got2) != 1 {
		t.Fatalf("second login left %d workspaces, want still 1 (no duplicate auto-create)", len(got2))
	}
	if got2[0].Slug != got[0].Slug {
		t.Errorf("second login's workspace slug %q != first login's %q", got2[0].Slug, got[0].Slug)
	}
}

// TestHandler_login_defaultSpec_leavesExistingUserAlone is the "a user with
// one or more workspaces must be unaffected" constraint: a user who already
// owns a workspace before MOCKER_DEFAULT_SPEC is ever consulted must not
// gain a second one on their next login.
func TestHandler_login_defaultSpec_leavesExistingUserAlone(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	_, _, specID := ts.importSpec(t, "Spec Importer 2", "Widgets API", minimalSpecDoc)

	// Log in and create a workspace by hand BEFORE MOCKER_DEFAULT_SPEC is
	// set — mirroring an existing deployment that turns the knob on after
	// people already have workspaces.
	cookie, csrfToken := ts.login(t, "Bob Existing Workspace")
	req := jsonRequest(t, http.MethodPost, "http://mocker.local/api/workspaces",
		map[string]string{"name": "Bob's Own"}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create workspace: status = %d, want 201; body = %s", rec.Code, rec.Body.String())
	}

	ts.cfg.DefaultSpecID = specID

	cookie2, _ := ts.login(t, "Bob Existing Workspace")
	got := ts.listWorkspaces(t, cookie2)
	if len(got) != 1 {
		t.Fatalf("existing-workspace user has %d workspaces after MOCKER_DEFAULT_SPEC was set, want 1 (unaffected)", len(got))
	}
}
