package main

import (
	"slices"
	"testing"
)

func TestReplayStartupRecoveryBeforeReadiness(t *testing.T) {
	a := analysisApp(t)
	if err := a.buildPlanes(t.Context()); err != nil {
		t.Fatal(err)
	}
	if a.replayService == nil || a.replayService.Running() {
		t.Fatal("replay must recover without starting workers during construction")
	}
	a.adminSrv.SetBackendReplay(nil)
	if !slices.Contains(a.adminSrv.Ready(), "SetBackendReplay") {
		t.Fatal("missing replay owner not detected before readiness")
	}
}
func TestReplayStartupRecoveryFailureRefusesConstruction(t *testing.T) {
	a := analysisApp(t)
	// This test owns a temporary DB. A damaged replay store must stop readiness.
	if _, err := a.db.W.ExecContext(t.Context(), `DROP TABLE backend_replay_runs`); err != nil {
		t.Fatal(err)
	}
	if err := a.buildPlanes(t.Context()); err == nil {
		t.Fatal("production ignored replay recovery failure")
	}
}
