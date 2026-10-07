package backendmodel

import (
	"database/sql"
	"strings"
	"testing"
)

// Review 2026-10-06, F83/F172: the staging budget summed the rows of every
// session the project ever had, committed and aborted included, so a project
// that reconciled often enough crossed 512 MiB for good and every import verb,
// Abort included, answered 413. Closed sessions must not count, and the
// transient budgets that use stagingBytes as their baseline must start from
// the open sessions only.
func TestImportStagingCountsOnlyOpenSessions(t *testing.T) {
	r, p, old, _ := committedBase(t)
	// Inflate the committed session's staged record so that its bytes alone
	// would exceed the headroom left below.
	err := r.db.Write(t.Context(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), `UPDATE backend_import_records SET document=json_set(document,'$.pad',?) WHERE session_id=?`, strings.Repeat("x", 1<<20), old.ID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	err = r.db.Read(t.Context(), func(tx *sql.Tx) error {
		n, err := stagingBytes(t.Context(), tx, p.ID)
		if err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("closed sessions still count against staging: %d bytes", n)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	scope := "backend:" + p.ID
	held, ok := r.db.ReserveTransient(scope, MaxProjectStagingBytes-256<<10, MaxProjectStagingBytes)
	if !ok {
		t.Fatal("reserve project budget")
	}
	defer held.Release()
	s := beginRepeat(t, r, p, old)
	// Fill the budget to the byte: Abort only shrinks staging and is the
	// documented way out, so it must still succeed.
	rest, ok := r.db.ReserveTransient(scope, MaxProjectStagingBytes-r.db.TransientBytes(scope), MaxProjectStagingBytes)
	if !ok {
		t.Fatal("reserve the rest of the budget")
	}
	defer rest.Release()
	aborted, err := r.AbortImport(t.Context(), p.ID, s.ID, AbortImportInput{ExpectedImportVersion: s.Version, IdempotencyKey: "abort"})
	if err != nil {
		t.Fatalf("abort refused at the staging cap: %v", err)
	}
	if aborted.State != "aborted" {
		t.Fatalf("abort state = %q", aborted.State)
	}
}
