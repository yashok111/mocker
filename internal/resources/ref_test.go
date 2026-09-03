// ref_test.go holds D11 property 7's CONFIRM and RESEED half — the "read
// nothing" side lives here, in internal/resources; the "Preview reads
// nothing" half lives in internal/mockplane/ref_test.go, where Preview
// itself does.
//
// Why there is nothing to stub: [populateEntities] (populate.go) builds its
// own [gen.Request] literal, with no Recipes field and no Ref field set —
// this package never reads op_overrides at all, so a "ref" recipe bound
// anywhere in a spec's schema is structurally unreachable from Confirm or
// from a reseed. [gen.Request.Ref] stays nil, and gen's own recipeValue
// short-circuits on a nil *recipes.Set before Recipe.Value — and therefore
// Ref — is ever consulted (values.go:35-38#lookupRecipe). Nothing in this
// package can even construct a *recipes.Set to hand it one.
//
// What IS observable, and worth a real regression test, is the other half
// of "read nothing": a family's generated population must be byte-for-byte
// IDENTICAL whether or not some OTHER confirmed family already holds rows a
// live "ref" resolver (had one ever run here) could have read from. Same
// seed, same settings, same schema — determinism (D4's own "Seed is
// load-bearing" argument) says the two runs below must produce the exact
// same bytes; if some future change wired op_overrides into this package's
// generation path, this test would go red the moment it let population of
// one family depend on another's stored rows.
package resources

import (
	"testing"

	"github.com/yashok111/mocker/internal/domain"
)

// confirmedEntityBytes imports fixtureDoc into a FRESH db, confirms family
// alone on a fresh workspace over it, and returns the raw bytes of every
// entity it stored, in id order (List's own "ORDER BY id ASC").
func confirmedEntityBytes(t *testing.T, family string, settings domain.Settings) [][]byte {
	t.Helper()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, settings)
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, family)
	if err != nil {
		t.Fatalf("Confirm %q: %v", family, err)
	}
	entities, err := repo.List(t.Context(), res.ID, "", "")
	if err != nil {
		t.Fatalf("List after Confirm %q: %v", family, err)
	}
	out := make([][]byte, len(entities))
	for i, e := range entities {
		out[i] = []byte(e.Data)
	}
	return out
}

func equalByteSlices(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if string(a[i]) != string(b[i]) {
			return false
		}
	}
	return true
}

// TestConfirm_ReadsNothingFromAnotherAlreadyConfirmedFamily is D11 property
// 7's Confirm half: /widgets confirmed FIRST (so /users' own confirm runs
// with another family's real rows already sitting in storage) must produce
// byte-identical /users entities to confirming /users alone, on a fresh
// workspace where /widgets was never touched at all. Any dependency on
// another family's stored rows — the shape a live "ref" resolver would
// introduce, had Confirm ever wired one — would show up here as a
// divergence.
func TestConfirm_ReadsNothingFromAnotherAlreadyConfirmedFamily(t *testing.T) {
	t.Parallel()
	settings := domain.Settings{Seed: 3, ListSize: 4}

	// Alone: nothing else in the workspace's storage at all.
	alone := confirmedEntityBytes(t, familyUsers, settings)

	// Alongside: /widgets confirmed and populated FIRST, on its own fresh
	// workspace+db, then /users confirmed second in that SAME workspace —
	// so if Confirm ever read another family's rows, /widgets' rows would
	// be sitting right there to read.
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, settings)
	repo := newTestRepo(t, db, 4<<20, 64<<10)
	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); err != nil {
		t.Fatalf("Confirm %q (the other, already-stored family): %v", familyWidgets, err)
	}
	resUsers, err := repo.Confirm(t.Context(), wsID, familyUsers)
	if err != nil {
		t.Fatalf("Confirm %q alongside an already-confirmed %q: %v", familyUsers, familyWidgets, err)
	}
	usersEntities, err := repo.List(t.Context(), resUsers.ID, "", "")
	if err != nil {
		t.Fatalf("List %q: %v", familyUsers, err)
	}
	alongside := make([][]byte, len(usersEntities))
	for i, e := range usersEntities {
		alongside[i] = []byte(e.Data)
	}

	if !equalByteSlices(alone, alongside) {
		t.Fatalf("/users entities confirmed ALONE = %s\n/users entities confirmed ALONGSIDE an already-populated /widgets = %s\nwant byte-identical — Confirm must read nothing from any other family's storage", alone, alongside)
	}
}

// TestResetData_ReseedReadsNothingFromAnotherConfirmedFamily is D11
// property 7's reseed half: [ResetModeReseed] repopulates every confirmed
// family from the workspace's current configuration
// (populate.go/reset.go's own populateEntities call, identical to
// Confirm's) — a reseed of /users with /widgets already confirmed and
// populated alongside it must produce the SAME bytes a reseed of a
// workspace confirming /users ALONE would.
func TestResetData_ReseedReadsNothingFromAnotherConfirmedFamily(t *testing.T) {
	t.Parallel()
	settings := domain.Settings{Seed: 9, ListSize: 3}

	// Alone: a workspace with only /users ever confirmed, reseeded once.
	aloneDB := newTestDB(t, t.TempDir()+"/mocker.db")
	aloneSpecID := importFixtureSpec(t, aloneDB)
	aloneWsID := insertWorkspace(t, aloneDB, "alpha", &aloneSpecID, settings)
	aloneRepo := newTestRepo(t, aloneDB, 4<<20, 64<<10)
	aloneRes, err := aloneRepo.Confirm(t.Context(), aloneWsID, familyUsers)
	if err != nil {
		t.Fatalf("Confirm %q (alone fixture): %v", familyUsers, err)
	}
	if _, err := aloneRepo.ResetData(t.Context(), aloneWsID, ResetModeReseed, "alpha"); err != nil {
		t.Fatalf("ResetData reseed (alone fixture): %v", err)
	}
	aloneEntities, err := aloneRepo.List(t.Context(), aloneRes.ID, "", "")
	if err != nil {
		t.Fatalf("List after reseed (alone fixture): %v", err)
	}
	alone := make([][]byte, len(aloneEntities))
	for i, e := range aloneEntities {
		alone[i] = []byte(e.Data)
	}

	// Alongside: /widgets confirmed and populated too, in the SAME
	// workspace, before the SAME reseed call — reseed repopulates every
	// confirmed family in one pass (reset.go), so if it ever read across
	// families, /widgets' freshly (re)generated rows would be sitting
	// right there while /users' own population runs in the same
	// transaction.
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, settings)
	repo := newTestRepo(t, db, 4<<20, 64<<10)
	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); err != nil {
		t.Fatalf("Confirm %q (alongside fixture): %v", familyWidgets, err)
	}
	resUsers, err := repo.Confirm(t.Context(), wsID, familyUsers)
	if err != nil {
		t.Fatalf("Confirm %q (alongside fixture): %v", familyUsers, err)
	}
	if _, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha"); err != nil {
		t.Fatalf("ResetData reseed (alongside fixture): %v", err)
	}
	usersEntities, err := repo.List(t.Context(), resUsers.ID, "", "")
	if err != nil {
		t.Fatalf("List after reseed (alongside fixture): %v", err)
	}
	alongside := make([][]byte, len(usersEntities))
	for i, e := range usersEntities {
		alongside[i] = []byte(e.Data)
	}

	if !equalByteSlices(alone, alongside) {
		t.Fatalf("/users entities reseeded ALONE = %s\n/users entities reseeded ALONGSIDE a confirmed /widgets = %s\nwant byte-identical — a reseed must read nothing from any other family's storage", alone, alongside)
	}
}
