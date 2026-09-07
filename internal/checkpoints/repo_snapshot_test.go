// repo_snapshot_test.go: the stored snapshot's own format and safety —
// the gzip container bounded at both ends (§G obs 20), the identity fence
// (C5) a concurrent edit must trip, and the bounded retry that answers it.
// Split out of the former repo_test.go (package checkpoints, not
// checkpoints_test — see helpers_test.go's own comment for why, and why no
// test in this package calls t.Parallel).
package checkpoints

import (
	"bytes"
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/bundle"
)

// TestSnapshot_isGzippedAndRoundTrips is obs 20's first two checks: the
// stored column is a gzip stream (not raw JSON in a BLOB), and a
// write-then-read returns the same document.
func TestSnapshot_isGzippedAndRoundTrips(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	f.createEndpoint(t, f.wsID, "POST", "/custom")

	point := f.create(t, "точка")

	blob := f.storedBlob(t, point.ID)
	if !bytes.HasPrefix(blob, gzipMagic) {
		t.Fatalf("config_snap does not start with the gzip magic bytes: %x", blob[:min(4, len(blob))])
	}

	got, err := f.repo.Get(t.Context(), f.wsID, point.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	roundTripped, err := bundle.Encode(got.Bundle)
	if err != nil {
		t.Fatalf("re-encode: %v", err)
	}
	stored, err := decompressSnapshot(blob)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(roundTripped, stored) {
		t.Fatalf("round trip changed the document:\n stored: %s\n  round: %s", stored, roundTripped)
	}
	if len(got.Bundle.Endpoints) != 1 || got.Bundle.Endpoints[0].Path != "/custom" {
		t.Fatalf("the endpoint did not survive the round trip: %+v", got.Bundle.Endpoints)
	}
}

// TestSnapshot_refusesABlobThatIsNotGzip is obs 20's third check.
func TestSnapshot_refusesABlobThatIsNotGzip(t *testing.T) {
	f := newFixture(t, 20)
	point := f.create(t, "точка")

	if _, err := f.db.W.ExecContext(t.Context(),
		"UPDATE checkpoints SET config_snap = ? WHERE id = ?", []byte(`{"mockerBundle":5}`), point.ID); err != nil {
		t.Fatalf("overwrite config_snap: %v", err)
	}
	if _, err := f.repo.Get(t.Context(), f.wsID, point.ID); !errors.Is(err, ErrCorruptSnapshot) {
		t.Fatalf("Get over raw JSON = %v, want ErrCorruptSnapshot", err)
	}
}

// TestSnapshot_ceilingRefusesOneWhitespaceByteOver is obs 20's fourth
// check, and the reason decompressSnapshot reads maxSnapshotBytes+1: a bare
// LimitReader at exactly the limit truncates silently, and a truncation
// landing on trailing whitespace still parses as valid JSON. The overflow
// here is ONE SPACE — the document is still valid JSON, and it is still
// refused.
//
// It lowers maxSnapshotBytes and restores it. C18 forbids an 8 MiB fixture:
// building one under -race in a memory-capped scope is how this box's OOM
// killer gets invoked. NO t.Parallel here or anywhere in this package.
func TestSnapshot_ceilingRefusesOneWhitespaceByteOver(t *testing.T) {
	f := newFixture(t, 20)
	point := f.create(t, "точка")
	doc, err := decompressSnapshot(f.storedBlob(t, point.ID))
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}

	restore := maxSnapshotBytes
	t.Cleanup(func() { maxSnapshotBytes = restore })
	maxSnapshotBytes = len(doc)

	// Exactly at the ceiling: accepted.
	if _, err := f.repo.Get(t.Context(), f.wsID, point.ID); err != nil {
		t.Fatalf("a document of exactly maxSnapshotBytes was refused: %v", err)
	}

	// One whitespace byte over: refused, before bundle.Decode sees it.
	over := f.insertBlob(t, rawGzip(t, append(append([]byte(nil), doc...), ' ')))
	if _, err := f.repo.Get(t.Context(), f.wsID, over); !errors.Is(err, ErrSnapshotTooLarge) {
		t.Fatalf("Get over a document one byte past the ceiling = %v, want ErrSnapshotTooLarge", err)
	}
}

// TestSnapshot_ceilingRefusesAnOversizedCapture is obs 20's fifth check and
// C18's write half — the one that stops the ceiling from being a trap.
// Every existing cap in this tree is write-side and none bounds a
// workspace's TOTAL, so a read-only ceiling could make a checkpoint this
// very build wrote permanently unreadable. The refusal happens BEFORE any
// write: no checkpoint row, no revision move.
func TestSnapshot_ceilingRefusesAnOversizedCapture(t *testing.T) {
	f := newFixture(t, 20)
	f.pinStatus(t, 418)
	point := f.create(t, "точка")
	doc, err := decompressSnapshot(f.storedBlob(t, point.ID))
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}

	restore := maxSnapshotBytes
	t.Cleanup(func() { maxSnapshotBytes = restore })
	maxSnapshotBytes = len(doc) - 1

	beforeRevision := f.revision(t)
	beforeCheckpoints := len(f.list(t))

	if _, err := f.repo.Create(t.Context(), f.wsID, "слишком большой", f.userID); !errors.Is(err, ErrSnapshotTooLarge) {
		t.Fatalf("Create over the ceiling = %v, want ErrSnapshotTooLarge", err)
	}
	// Rollback and Reset capture a pre-destructive checkpoint first, so both
	// are refused too — C18's stated consequence, not a surprise.
	if _, err := f.repo.Rollback(t.Context(), f.wsID, point.ID, f.userID, false, ""); !errors.Is(err, ErrSnapshotTooLarge) {
		t.Fatalf("Rollback over the ceiling = %v, want ErrSnapshotTooLarge", err)
	}
	if _, err := f.repo.Reset(t.Context(), f.wsID, f.userID); !errors.Is(err, ErrSnapshotTooLarge) {
		t.Fatalf("Reset over the ceiling = %v, want ErrSnapshotTooLarge", err)
	}

	if got := len(f.list(t)); got != beforeCheckpoints {
		t.Fatalf("a refused capture still wrote a checkpoint: %d -> %d", beforeCheckpoints, got)
	}
	if got := f.revision(t); got != beforeRevision {
		t.Fatalf("a refused capture still moved the revision: %d -> %d", beforeRevision, got)
	}
}

// TestFenceTx_comparesAllThreeIdentityColumns is C5 step 4. revision ALONE
// cannot prove workspace identity — workspaces.id has no AUTOINCREMENT, so
// a deleted workspace's id can be reused with revision back at 1, reachable
// precisely because a manual checkpoint does not bump (C12) — and
// created_at is second-resolution, which is why slug is compared beside it.
func TestFenceTx_comparesAllThreeIdentityColumns(t *testing.T) {
	f := newFixture(t, 20)
	before, err := f.repo.readWorkspaceCore(t.Context(), f.wsID)
	if err != nil {
		t.Fatalf("read core: %v", err)
	}

	cases := map[string]workspaceCore{
		"revision":  {revision: before.revision + 1, createdAt: before.createdAt, slug: before.slug},
		"createdAt": {revision: before.revision, createdAt: before.createdAt + 1, slug: before.slug},
		"slug":      {revision: before.revision, createdAt: before.createdAt, slug: before.slug + "-other"},
	}
	for name, moved := range cases {
		t.Run(name, func(t *testing.T) {
			tx, err := f.db.W.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatalf("begin: %v", err)
			}
			defer func() { _ = tx.Rollback() }()
			if _, err := fenceTx(t.Context(), tx, f.wsID, moved); !errors.Is(err, errFenceMoved) {
				t.Fatalf("fenceTx with a moved %s = %v, want errFenceMoved", name, err)
			}
		})
	}

	tx, err := f.db.W.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := fenceTx(t.Context(), tx, f.wsID, before); err != nil {
		t.Fatalf("fenceTx over an unchanged workspace: %v", err)
	}
}

// TestRetrying_isBoundedAndAnswersConcurrentEdit is C5 step 7: three
// attempts, then a 409's error. Driven at the loop rather than through a
// racing goroutine, which is a flaky test that proves less — the same
// judgement scenarios/repo.go makes about its own fence seam.
func TestRetrying_isBoundedAndAnswersConcurrentEdit(t *testing.T) {
	attempts := 0
	err := retrying(func() error {
		attempts++
		return errFenceMoved
	})
	if !errors.Is(err, ErrConcurrentEdit) {
		t.Fatalf("exhausted retry = %v, want ErrConcurrentEdit", err)
	}
	if attempts != maxAttempts {
		t.Fatalf("attempts = %d, want %d", attempts, maxAttempts)
	}

	attempts = 0
	if err := retrying(func() error {
		attempts++
		if attempts < maxAttempts {
			return errFenceMoved
		}
		return nil
	}); err != nil {
		t.Fatalf("a retry that eventually succeeds returned %v", err)
	}

	sentinel := errors.New("not a fence")
	attempts = 0
	if err := retrying(func() error {
		attempts++
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatalf("a non-fence error was swallowed: %v", err)
	}
	if attempts != 1 {
		t.Fatalf("a non-fence error was retried %d times", attempts)
	}
}
