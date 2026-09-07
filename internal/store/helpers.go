package store

import "strings"

// IsUniqueViolation reports whether err is a UNIQUE constraint failure.
// modernc.org/sqlite reports these as a plain error whose message contains
// "UNIQUE constraint failed" — matched by substring so a caller does not
// need to import the driver just to compare an error code.
//
// Until 2026-09-07 this was a private, byte-identical copy in
// internal/customep, internal/resources, internal/scenarios and
// internal/workspaces, each justified the same way [BumpRevisionTx]'s own
// copies were (see revision.go): store is the shared dependency none of the
// four import from each other, so it is the legal home once all four
// already import it anyway.
func IsUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// BoolToInt is the trivial bool -> 0/1 conversion every INSERT/UPDATE in
// this tree needs for a column with no native boolean type in SQLite.
// Copied, before 2026-09-07, as an identical private function in
// internal/overrides and internal/customep — consolidated here for
// [IsUniqueViolation]'s reason.
func BoolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// RowScanner is satisfied by both *sql.Row and *sql.Rows, so a package's
// scan function is written once and called from both a single-row Get and a
// multi-row List/ForWorkspace. Before 2026-09-07 this was declared as an
// identical named type in internal/traffic, internal/customep,
// internal/workspaces, internal/specs and internal/overrides, and inlined
// anonymously (the same method set, spelled out at the call site instead of
// named) in internal/assets and twice in internal/resources — consolidated
// here for [IsUniqueViolation]'s reason.
type RowScanner interface {
	Scan(dest ...any) error
}
