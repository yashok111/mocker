package designscenario

import (
	"context"
	"fmt"

	"github.com/yashok111/mocker/internal/jsonx"
)

// CoverageReports reads at most fifty terminal reports for one exact revision.
// SQLite projects only the observations needed by BuildCoverage, so retained
// request and response bodies never accumulate in Go memory.
func (r *RunRepo) CoverageReports(ctx context.Context, scenarioID, revisionID int64) ([]RunReport, error) {
	rows, err := r.db.R.QueryContext(ctx, `
		SELECT status,
			(SELECT coalesce(json_group_array(json_object('messageId',json_extract(step.value,'$.messageId'),
				'status',json_extract(step.value,'$.status'))), '[]')
			 FROM json_each(design_scenario_runs.report,'$.steps') AS step),
			(SELECT coalesce(json_group_array(json_object('fragmentId',json_extract(decision.value,'$.fragmentId'),
				'branchId',json_extract(decision.value,'$.branchId'),
				'outcome',json_extract(decision.value,'$.outcome'))), '[]')
			 FROM json_each(design_scenario_runs.report,'$.controlFlow') AS decision)
		FROM design_scenario_runs
		WHERE scenario_id=? AND revision_id=? AND status IN ('passed','failed','cancelled') AND report IS NOT NULL
		ORDER BY started_at DESC,run_id DESC LIMIT 50`, scenarioID, revisionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	reports := make([]RunReport, 0, 50)
	for rows.Next() {
		var status, steps, decisions string
		if err := rows.Scan(&status, &steps, &decisions); err != nil {
			return nil, err
		}
		var report RunReport
		report.ScenarioID, report.RevisionID, report.Status = scenarioID, revisionID, status
		if err := jsonx.Unmarshal([]byte(steps), &report.Steps); err != nil {
			return nil, fmt.Errorf("decode coverage steps: %w", err)
		}
		if err := jsonx.Unmarshal([]byte(decisions), &report.ControlFlow); err != nil {
			return nil, fmt.Errorf("decode coverage decisions: %w", err)
		}
		reports = append(reports, report)
	}
	return reports, rows.Err()
}
