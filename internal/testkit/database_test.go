package testkit_test

import (
	"path/filepath"
	"strconv"
	"testing"

	"github.com/yashok111/mocker/internal/testkit"
)

func TestNewDBIsolationAndReopen(t *testing.T) {
	for index := range 8 {
		t.Run(strconv.Itoa(index), func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "nested", "fixture.db")
			first := testkit.NewDBAt(t, path)
			if _, err := first.W.ExecContext(t.Context(), `CREATE TABLE fixture_marker(value TEXT); INSERT INTO fixture_marker VALUES('persisted')`); err != nil {
				t.Fatal(err)
			}
			second := testkit.NewDB(t)
			var tables int
			if err := second.R.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_master WHERE name='fixture_marker'`).Scan(&tables); err != nil || tables != 0 {
				t.Fatalf("fixture state leaked: count=%d err=%v", tables, err)
			}
			if err := first.Close(); err != nil {
				t.Fatal(err)
			}
			reopened := testkit.NewDBAt(t, path)
			var marker string
			if err := reopened.R.QueryRowContext(t.Context(), `SELECT value FROM fixture_marker`).Scan(&marker); err != nil || marker != "persisted" {
				t.Fatalf("reopen lost state: marker=%q err=%v", marker, err)
			}
			var foreignKeys int
			if err := reopened.W.QueryRowContext(t.Context(), "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil || foreignKeys != 1 {
				t.Fatalf("foreign keys disabled: %d %v", foreignKeys, err)
			}
		})
	}
}

func BenchmarkNewDB(b *testing.B) {
	warm := testkit.NewDB(b)
	if err := warm.Close(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	for b.Loop() {
		db := testkit.NewDB(b)
		if err := db.Close(); err != nil {
			b.Fatal(err)
		}
	}
}
