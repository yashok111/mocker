// repo_auto_test.go: Auto, P2d's debounce trigger (SIG-AUTO) — the
// window that suppresses a second automatic checkpoint, its exact boundary,
// and that it prunes/validates exactly like a manual Create. Split out of
// the former repo_test.go (package checkpoints, not checkpoints_test — see
// helpers_test.go's own comment for why, and why no test in this package
// calls t.Parallel).
package checkpoints

import (
	"errors"
	"testing"
)

// TestAuto_writesAKindAutoRowCarryingItsLabel is the plain case: an empty
// history has nothing to debounce against, so the outer probe never
// suppresses the very first call.
func TestAuto_writesAKindAutoRowCarryingItsLabel(t *testing.T) {
	f := newFixture(t, 20)

	s := f.auto(t, "перед массовой правкой", 60)
	if s == nil {
		t.Fatal("Auto on an empty history returned (nil, nil), want a write")
	}
	if s.Kind != KindAuto {
		t.Fatalf("kind = %q, want %q", s.Kind, KindAuto)
	}
	if s.Label != "перед массовой правкой" {
		t.Fatalf("label = %q, want the label Auto was given", s.Label)
	}
	if s.CreatedBy == nil || *s.CreatedBy != f.userID {
		t.Fatalf("createdBy = %v, want %d (C15)", s.CreatedBy, f.userID)
	}

	list := f.list(t)
	if len(list) != 1 || list[0].ID != s.ID || list[0].Kind != KindAuto {
		t.Fatalf("stored history = %+v, want exactly the one auto row Auto returned", list)
	}
}

// TestAuto_suppressesASecondCallInsideTheWindow is SIG-AUTO's central
// promise: a second call before the window elapses returns (nil, nil) — an
// ordinary return, not an error — and writes NOTHING, so the stored row
// still carries the FIRST call's label.
func TestAuto_suppressesASecondCallInsideTheWindow(t *testing.T) {
	f := newFixture(t, 20)
	const window = 3600 // far longer than this test can possibly take

	first := f.auto(t, "первая", window)
	if first == nil {
		t.Fatal("first Auto returned (nil, nil), want a write")
	}

	second, err := f.repo.Auto(t.Context(), f.wsID, "вторая", f.userID, window)
	if err != nil {
		t.Fatalf("suppressed Auto returned an error, want a plain (nil, nil): %v", err)
	}
	if second != nil {
		t.Fatalf("second Auto inside the window = %+v, want (nil, nil)", second)
	}

	list := f.list(t)
	if len(list) != 1 {
		t.Fatalf("history after a suppressed Auto = %d rows, want exactly 1 (nothing written)", len(list))
	}
	if list[0].Label != "первая" {
		t.Fatalf("stored label = %q, want %q — the suppressed call must not have written its own label", list[0].Label, "первая")
	}
}

// TestAuto_boundaryAtExactlyTheWindowWrites is the >= in SIG-AUTO's
// "now - max >= window": a sleep of EXACTLY the window must re-arm. Rather
// than a real sleep, the first row's created_at is pushed back by exactly
// `window` seconds through direct SQL — the same technique
// [fixture.ageAutoCheckpoint] uses elsewhere in this file — so the second
// call's elapsed time is deterministically at (or, in the rare case a wall
// clock second ticks between the two statements, past) the boundary. An
// implementation using `>` instead of `>=` fails this the common case: it
// suppresses at elapsed == window instead of re-arming.
func TestAuto_boundaryAtExactlyTheWindowWrites(t *testing.T) {
	f := newFixture(t, 20)
	const window = 5

	first := f.auto(t, "первая", window)
	if first == nil {
		t.Fatal("first Auto returned (nil, nil), want a write")
	}
	f.ageAutoCheckpoint(t, first.ID, window)

	second, err := f.repo.Auto(t.Context(), f.wsID, "вторая", f.userID, window)
	if err != nil {
		t.Fatalf("Auto exactly at the window: %v", err)
	}
	if second == nil {
		t.Fatal("Auto exactly at the window returned (nil, nil), want a write: the comparison must be >=, not >")
	}
}

// TestAuto_prunesLikeCreate is C7's retention rule reached through Auto
// instead of Create: [pruneRetentionTx]'s filter is `kind <> 'manual'`
// rather than an enumeration of the machine-made kinds, so auto rows join
// the pruned population for free (checkpoints.go's Kind doc comment) — this
// is the test that would fail if [Repo.Auto] wrote through
// [insertCheckpointTx] alone, skipping the prune SIG-AUTO forbids skipping.
func TestAuto_prunesLikeCreate(t *testing.T) {
	const retention = 2
	const window = 1
	f := newFixture(t, retention)

	const writes = 5
	for i := 0; i < writes; i++ {
		s := f.auto(t, "дебаунс", window)
		if s == nil {
			t.Fatalf("call %d suppressed unexpectedly", i)
		}
		// Age the row well past the window so the NEXT call is not
		// suppressed by the very row this call just wrote — this test is
		// about retention, not the window, and a suppressed call would
		// never reach pruneRetentionTx at all.
		f.ageAutoCheckpoint(t, s.ID, window+1)
	}

	machine := f.machineMade(t)
	if len(machine) != retention {
		t.Fatalf("auto checkpoints after %d writes = %d, want the retention of %d", writes, len(machine), retention)
	}
}

// TestAuto_rejectsEmptyLabelLikeCreate is SIG-AUTO's "reuses Create's whole
// sequence": validateLabel runs first, through the SAME [ErrInvalidLabel]
// path [TestCreate_rejectsEmptyAndOverlongLabel] exercises, and a rejected
// label writes nothing at all — not even the outer window probe's read
// matters, because there is no row to write.
func TestAuto_rejectsEmptyLabelLikeCreate(t *testing.T) {
	f := newFixture(t, 20)

	for _, label := range []string{"", "   ", "\t\n"} {
		if _, err := f.repo.Auto(t.Context(), f.wsID, label, f.userID, 60); !errors.Is(err, ErrInvalidLabel) {
			t.Fatalf("Auto(%q) error = %v, want ErrInvalidLabel", label, err)
		}
	}
	if got := len(f.list(t)); got != 0 {
		t.Fatalf("a rejected label still wrote %d rows", got)
	}
}
