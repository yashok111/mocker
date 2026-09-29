package designscenario

import (
	"context"
	"database/sql"
)

// EventMap reads the selected saved revision and scenario version in one snapshot.
// A zero revisionID selects the current saved draft.
func (r *Repo) EventMap(ctx context.Context, scenarioID, revisionID int64) (EventMapReport, error) {
	var scenario Scenario
	var revision Revision
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		var err error
		scenario, err = getScenario(ctx, tx, scenarioID)
		if err != nil {
			return err
		}
		if revisionID == 0 {
			revisionID = scenario.DraftRevisionID
		}
		revision, err = getRevision(ctx, tx, scenarioID, revisionID)
		return err
	})
	if err != nil {
		return EventMapReport{}, err
	}
	analysis, err := AnalyzeEventMap(ctx, revision.Document)
	if err != nil {
		return EventMapReport{}, err
	}
	return EventMapReport{EventMapAnalysis: analysis, ScenarioID: scenarioID, Version: scenario.Version, RevisionID: revision.ID, Proposed: false}, nil
}
