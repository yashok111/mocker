// repo_read_test.go: ForWorkspace (mockplane.ResourceSource) and R37's
// vanished-resource distinction — a missing resource row and a declined one
// both answer ErrResourceGone, never a bare not-found. Split out of the
// former repo_test.go (package resources, not resources_test — see
// helpers_test.go's own comment for why).
package resources

import (
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/testkit"
)

// TestForWorkspace_ReturnsConfirmedResources proves the method the review
// finding "resources.Repo never implements a ForWorkspace-style bulk read"
// says the package needs actually exists and answers what buildRuntime
// (internal/mockplane/runtime.go) needs: every CONFIRMED resource of ONE
// workspace, none of another's, keyed (by the caller, not this method) on
// RouteFamily.
func TestForWorkspace_ReturnsConfirmedResources(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsA := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
	wsB := insertWorkspace(t, db, "beta", &specID, domain.Settings{Seed: 1, ListSize: 3})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	widgets, err := repo.Confirm(t.Context(), wsA, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm widgets for wsA: %v", err)
	}
	users, err := repo.Confirm(t.Context(), wsA, familyUsers)
	if err != nil {
		t.Fatalf("Confirm users for wsA: %v", err)
	}
	// A confirm on wsB must never show up in wsA's list.
	if _, err := repo.Confirm(t.Context(), wsB, familyWidgets); err != nil {
		t.Fatalf("Confirm widgets for wsB: %v", err)
	}

	got, err := repo.ForWorkspace(t.Context(), wsA)
	if err != nil {
		t.Fatalf("ForWorkspace: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ForWorkspace(wsA) returned %d resources, want 2 (widgets, users)", len(got))
	}
	byFamily := make(map[string]*Resource, len(got))
	for _, r := range got {
		byFamily[r.RouteFamily] = r
	}
	w, ok := byFamily[familyWidgets]
	if !ok {
		t.Fatalf("ForWorkspace(wsA) missing %q", familyWidgets)
	}
	if w.ID != widgets.ID || w.IDField != "id" || w.SeedCount != 3 || w.WorkspaceID != wsA {
		t.Fatalf("widgets row = %+v, want ID=%d IDField=id SeedCount=3 WorkspaceID=%d", w, widgets.ID, wsA)
	}
	if w.WriteForm == nil || *w.WriteForm != "bare" {
		t.Fatalf("widgets.WriteForm = %v, want \"bare\" (the fixture's POST body matches the item schema)", w.WriteForm)
	}
	u, ok := byFamily[familyUsers]
	if !ok {
		t.Fatalf("ForWorkspace(wsA) missing %q", familyUsers)
	}
	if u.ID != users.ID || u.IDField != "userId" {
		t.Fatalf("users row = %+v, want ID=%d IDField=userId", u, users.ID)
	}
	if u.WriteForm != nil {
		t.Fatalf("users.WriteForm = %v, want nil (the fixture declares no POST on /users)", *u.WriteForm)
	}

	// A workspace with no confirmed resource at all gets an empty slice,
	// never an error.
	wsEmpty := insertWorkspace(t, db, "gamma", &specID, domain.Settings{})
	got, err = repo.ForWorkspace(t.Context(), wsEmpty)
	if err != nil || len(got) != 0 {
		t.Fatalf("ForWorkspace(wsEmpty) = %v, %v, want empty, nil", got, err)
	}
}

// TestListGetDelete_ErrResourceGoneWhenResourceRowMissing is D13 clause 34
// exercised directly against the real store, not a hand-written fake: the
// review finding this defends against is that List/Get/Delete queried only
// the entities table, so a resource declined out from under a parked
// request (R37) answered an ordinary empty/not-found instead of
// ErrResourceGone — silently breaking [resourceBranch]'s (internal/
// mockplane/resource.go) fall-through-to-the-generator contract for three
// of the four verbs. resourceID here never named a row at all (never
// inserted), the simplest stand-in for "the row is gone" that needs no
// Confirm-then-Decline dance to set up.
func TestListGetDelete_ErrResourceGoneWhenResourceRowMissing(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	repo := newTestRepo(t, db, 4<<20, 64<<10)
	const goneID = 999999

	if _, err := repo.List(t.Context(), goneID, "", ""); !errors.Is(err, ErrResourceGone) {
		t.Fatalf("List on a missing resource = %v, want ErrResourceGone", err)
	}
	if _, ok, err := repo.Get(t.Context(), goneID, "", "", "1"); !errors.Is(err, ErrResourceGone) || ok {
		t.Fatalf("Get on a missing resource = ok=%v err=%v, want ok=false err=ErrResourceGone", ok, err)
	}
	if deleted, err := repo.Delete(t.Context(), goneID, "", "", "1"); !errors.Is(err, ErrResourceGone) || deleted {
		t.Fatalf("Delete on a missing resource = deleted=%v err=%v, want deleted=false err=ErrResourceGone", deleted, err)
	}
}

// TestListGetDelete_ErrResourceGoneAfterDecline is the same property through
// the real lifecycle: Confirm, populate an entity via Create, Decline (which
// deletes the resources row and cascades its entities), then prove List/Get/
// Delete on the now-gone resourceID answer ErrResourceGone — never a bare
// empty list or a bare not-found, which [resourceBranch] cannot tell apart
// from "the family is confirmed but this row/entity legitimately doesn't
// exist".
func TestListGetDelete_ErrResourceGoneAfterDecline(t *testing.T) {
	t.Parallel()
	db := testkit.NewDBAt(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	entities, err := repo.List(t.Context(), res.ID, "", "")
	if err != nil || len(entities) == 0 {
		t.Fatalf("List before decline = %v, %v, want at least one entity", entities, err)
	}
	key := entities[0].EntityKey

	if err := repo.Decline(t.Context(), wsID, familyWidgets, "alpha"); err != nil {
		t.Fatalf("Decline: %v", err)
	}

	if _, err := repo.List(t.Context(), res.ID, "", ""); !errors.Is(err, ErrResourceGone) {
		t.Fatalf("List after decline = %v, want ErrResourceGone", err)
	}
	if _, ok, err := repo.Get(t.Context(), res.ID, "", "", key); !errors.Is(err, ErrResourceGone) || ok {
		t.Fatalf("Get after decline = ok=%v err=%v, want ok=false err=ErrResourceGone", ok, err)
	}
	if deleted, err := repo.Delete(t.Context(), res.ID, "", "", key); !errors.Is(err, ErrResourceGone) || deleted {
		t.Fatalf("Delete after decline = deleted=%v err=%v, want deleted=false err=ErrResourceGone", deleted, err)
	}
}
