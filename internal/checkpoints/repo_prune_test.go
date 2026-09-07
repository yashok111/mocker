// repo_prune_test.go: retention (C7) — pruning machine-made checkpoints
// down to N while sparing named ones and a rollback's own target. Split out
// of the former repo_test.go (package checkpoints, not checkpoints_test —
// see helpers_test.go's own comment for why, and why no test in this
// package calls t.Parallel).
package checkpoints

import (
	"testing"
)

// TestRetention_prunesMachineMadeAndSparesTheNamedOnes is C7's first case
// and DESIGN §12:773's «именованные не удаляются».
func TestRetention_prunesMachineMadeAndSparesTheNamedOnes(t *testing.T) {
	f := newFixture(t, 3)
	manual := f.create(t, "названная точка")

	for i := range 5 {
		f.pinStatus(t, 400+i)
		f.reset(t)
	}

	machine := f.machineMade(t)
	if len(machine) != 3 {
		t.Fatalf("machine-made checkpoints = %d, want the retention of 3", len(machine))
	}
	// The survivors must be the NEWEST three. machineMade returns them
	// oldest first, so every id is greater than the one before it, and the
	// FIRST of the five inserts (the smallest machine-made id there ever
	// was) must not be among them.
	for i := 1; i < len(machine); i++ {
		if machine[i] <= machine[i-1] {
			t.Fatalf("survivors are not in id order: %v", machine)
		}
	}
	if machine[0] <= manual.ID {
		t.Fatalf("the oldest machine-made checkpoint survived: %v", machine)
	}
	var found bool
	for _, s := range f.list(t) {
		if s.ID == manual.ID {
			found = true
		}
	}
	if !found {
		t.Fatalf("the manual checkpoint %d was pruned", manual.ID)
	}
}

// TestRetention_zeroPrunesNothing is §G obs 19, and it exists because
// smoke.sh runs at 3 and cannot see this. Zero means PRUNE NOTHING — a
// deliberate departure from traffic.NewRecorder's
// `if rec.retention <= 0 { rec.retention = DefaultRetention }`
// (recorder.go:98-100). An implementation that copies that default
// substitution keeps only the newest 1000 (or, read as "prune everything",
// none at all) and fails here.
func TestRetention_zeroPrunesNothing(t *testing.T) {
	f := newFixture(t, 0)

	const destructiveResets = 6
	for i := range destructiveResets {
		f.pinStatus(t, 400+i)
		f.reset(t)
	}

	machine := f.machineMade(t)
	if len(machine) != destructiveResets {
		t.Fatalf("machine-made checkpoints at retention 0 = %d, want all %d", len(machine), destructiveResets)
	}
}

// TestRetention_rollbackNeverPrunesItsOwnTarget is C7's third case: a
// MACHINE-MADE target is spared even when it falls outside the newest N,
// leaving N+1 rows for the duration of that one rollback — the alternative
// deletes the row the user just returned to, in the transaction that
// applies it. The overflow then corrects itself at the next machine-made
// insert whose target is not that row, and a MANUAL target gets no
// exemption at all.
func TestRetention_rollbackNeverPrunesItsOwnTarget(t *testing.T) {
	const retention = 2
	f := newFixture(t, retention)

	f.pinStatus(t, 401)
	manual := f.create(t, "M")

	f.pinStatus(t, 402)
	f.rollback(t, manual.ID) // writes O, holding 402
	f.pinStatus(t, 403)
	f.rollback(t, manual.ID) // writes P, holding 403

	machine := f.machineMade(t)
	if len(machine) != retention {
		t.Fatalf("machine-made population = %v, want exactly %d before the interesting rollback", machine, retention)
	}
	oldest := machine[0]

	// Roll back to the OLDEST machine-made row: it is the target, so it
	// survives its own prune and the population is N+1.
	f.rollback(t, oldest)

	after := f.machineMade(t)
	if len(after) != retention+1 {
		t.Fatalf("machine-made population = %v, want %d (newest %d plus the spared target)", after, retention+1, retention)
	}
	if after[0] != oldest {
		t.Fatalf("the rollback pruned the checkpoint it was restoring FROM: %v does not start with %d", after, oldest)
	}
	if s := f.pinnedStatus(t); s == nil || *s != 402 {
		t.Fatalf("state after rolling back to the oldest machine-made row = %v, want 402", s)
	}

	// A MANUAL target grants no exemption, so the overflow corrects itself.
	f.pinStatus(t, 405)
	f.rollback(t, manual.ID)
	corrected := f.machineMade(t)
	if len(corrected) != retention {
		t.Fatalf("machine-made population = %v, want back down to %d", corrected, retention)
	}
	for _, id := range corrected {
		if id == oldest {
			t.Fatalf("the spared target %d survived a rollback whose target was manual: %v", oldest, corrected)
		}
	}
}
