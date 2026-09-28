package designscenario

import (
	"fmt"
	"strings"
	"testing"
)

func TestCoverageReportsSelectsRevisionAndProjectsSmallObservations(t *testing.T) {
	scenarios := newTestRepo(t)
	first, err := scenarios.Create(t.Context(), CreateInput{Document: validDocument("Coverage"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := scenarios.Save(t.Context(), first.Scenario.ID, SaveInput{ExpectedVersion: first.Scenario.Version, Document: validDocument("Coverage 2"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	largeBody := strings.Repeat("x", 1<<20)
	for i := range 55 {
		revisionID := first.Draft.ID
		if i == 54 {
			revisionID = second.Draft.ID
		}
		raw := fmt.Sprintf(`{"scenarioId":%d,"revisionId":%d,"status":"passed","steps":[{"messageId":"m","status":"passed","response":{"body":"%s"}}],"controlFlow":[{"fragmentId":"f","branchId":"b","outcome":"taken"}]}`, first.Scenario.ID, revisionID, largeBody)
		_, err := scenarios.db.W.ExecContext(t.Context(), `INSERT INTO design_scenario_runs(scenario_id,run_id,revision_id,input_hash,version,name,source,status,started_at,report) VALUES(?,?,?,'hash',1,'','ui','passed',?,?)`, first.Scenario.ID, fmt.Sprint(i), revisionID, i, raw)
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := NewRunRepo(scenarios.db).CoverageReports(t.Context(), first.Scenario.ID, first.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 50 {
		t.Fatalf("reports=%d, want 50", len(rows))
	}
	for _, row := range rows {
		if row.RevisionID != first.Draft.ID || len(row.Steps) != 1 || row.Steps[0].Response != nil || len(row.ControlFlow) != 1 {
			t.Fatalf("unscoped or unprojected report: %+v", row)
		}
	}
}
