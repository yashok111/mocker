package backendreplay

import (
	"database/sql"
	"testing"

	"github.com/yashok111/mocker/internal/testkit"
)

func TestReplayMigrationTablesAndRecovery(t *testing.T) {
	db := testkit.NewDB(t)
	for _, table := range []string{"backend_replay_profiles", "backend_replay_packages", "backend_replay_runs", "backend_replay_receipts", "backend_replay_steps", "backend_replay_evidence", "backend_replay_target_leases", "backend_replay_revocations"} {
		var n int
		if err := db.R.QueryRowContext(t.Context(), `SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil || n != 1 {
			t.Fatalf("missing %s: %v", table, err)
		}
	}
	if err := NewRepo(db).RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	_ = sql.ErrNoRows
}

func TestReplayRejectsPreviouslyKnownConfigRollback(t *testing.T) {
	r := NewRepo(testkit.NewDB(t))
	v1 := Target{TargetInfo: TargetInfo{ID: "fixture", Version: 1}, ConfigFingerprint: "v1"}
	v2 := v1
	v2.Version = 2
	v2.ConfigFingerprint = "v2"
	if err := r.registerConfigs(t.Context(), []Target{v1}); err != nil {
		t.Fatal(err)
	}
	if err := r.registerConfigs(t.Context(), []Target{v2}); err != nil {
		t.Fatal(err)
	}
	if err := r.registerConfigs(t.Context(), []Target{v1}); err == nil {
		t.Fatal("accepted prior registry version")
	}
}
