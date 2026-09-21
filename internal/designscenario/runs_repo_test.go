package designscenario

import (
	"errors"
	"fmt"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestRunReportsArePersistedAndScopedToScenario(t *testing.T) {
	scenarios := newTestRepo(t)
	scenario, err := scenarios.Create(t.Context(), CreateInput{Document: validDocument("Runs"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	// The migration must supply real storage before any runner can dispatch.
	report := fmt.Sprintf(`{"id":"first","scenarioId":%d,"revisionId":%d,"version":1,"status":"running"}`, scenario.Scenario.ID, scenario.Draft.ID)
	_, err = scenarios.db.W.ExecContext(t.Context(), `INSERT INTO design_scenario_runs(scenario_id,run_id,revision_id,input_hash,version,name,source,status,started_at,report) VALUES (?,?,?,'hash',1,'','ui','running',1,?)`, scenario.Scenario.ID, "first", scenario.Draft.ID, report)
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := scenarios.db.R.QueryRowContext(t.Context(), `SELECT report FROM design_scenario_runs WHERE scenario_id=? AND run_id=?`, scenario.Scenario.ID, "first").Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var result RunReport
	if err := jsonx.Unmarshal([]byte(stored), &result); err != nil {
		t.Fatal(err)
	}
	if result.ID != "first" || result.RevisionID != scenario.Draft.ID {
		t.Fatalf("wrong stored report: %s", stored)
	}
}

func TestRunRepoIdempotencyProgressRetentionAndRecovery(t *testing.T) {
	scenarios := newTestRepo(t)
	scenario, err := scenarios.Create(t.Context(), CreateInput{Document: validDocument("Runs"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	repo := NewRunRepo(scenarios.db)
	report := RunReport{RunSummary: RunSummary{ID: "first", ScenarioID: scenario.Scenario.ID, RevisionID: scenario.Draft.ID, Version: 1, Source: "ui", Status: "running", StartedAt: 1}, Document: scenario.Draft.Document, InputVariables: ExecutionValues{}, Variables: ExecutionValues{}, Steps: []StepResult{}}
	_, created, err := repo.Create(t.Context(), report, "hash")
	if err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	_, created, err = repo.Create(t.Context(), report, "hash")
	if err != nil || created {
		t.Fatalf("duplicate created=%v err=%v", created, err)
	}
	if _, _, err = repo.Create(t.Context(), report, "different"); !errors.Is(err, ErrRunConflict) {
		t.Fatalf("changed input: %v", err)
	}
	if _, _, err = repo.Lookup(t.Context(), scenario.Scenario.ID+1, "first"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unscoped read: %v", err)
	}
	report.Variables["progress"] = "visible"
	if err = repo.SaveProgress(t.Context(), report); err != nil {
		t.Fatal(err)
	}
	read, _, err := repo.Lookup(t.Context(), scenario.Scenario.ID, "first")
	if err != nil || read.Variables["progress"] != "visible" {
		t.Fatalf("progress=%+v err=%v", read, err)
	}
	if err = repo.RecoverInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	read, _, err = repo.Lookup(t.Context(), scenario.Scenario.ID, "first")
	if err != nil || read.Status != "cancelled" {
		t.Fatalf("recovered=%+v err=%v", read, err)
	}
	for i := 2; i <= 52; i++ {
		report.ID = fmt.Sprint(i)
		report.StartedAt = int64(i)
		report.Status = "running"
		if _, _, err = repo.Create(t.Context(), report, report.ID); err != nil {
			t.Fatal(err)
		}
		report.Status = "passed"
		if err = repo.Finish(t.Context(), report); err != nil {
			t.Fatal(err)
		}
	}
	if _, hash, err := repo.Lookup(t.Context(), scenario.Scenario.ID, "first"); !errors.Is(err, ErrRunGone) || hash != "hash" {
		t.Fatalf("pruned report hash=%q err=%v", hash, err)
	}
	report.ID = "first"
	if _, _, err = repo.Create(t.Context(), report, "hash"); !errors.Is(err, ErrRunGone) {
		t.Fatalf("pruned ID replay: %v", err)
	}
	list, err := repo.List(t.Context(), scenario.Scenario.ID)
	if err != nil || len(list) != 50 || list[0].ID != "52" {
		t.Fatalf("list=%+v err=%v", list, err)
	}
}
