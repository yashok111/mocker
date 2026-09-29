package designscenario

import (
	"context"
	"database/sql"

	"github.com/yashok111/mocker/internal/jsonx"
)

// ResourceMapUsage describes a binding in a scenario's current saved draft.
// ContractRevisionID is the pinned API revision; it may differ from the API's current draft.
type ResourceMapUsage struct {
	ScenarioID         int64  `json:"scenarioId"`
	ScenarioName       string `json:"scenarioName"`
	RevisionID         int64  `json:"revisionId"`
	MessageID          string `json:"messageId"`
	OperationKey       string `json:"operationKey"`
	ContractRevisionID int64  `json:"contractRevisionId"`
	Mode               string `json:"mode"`
}

const (
	resourceMapScenarioScanLimit = 500
	resourceMapUsageLimit        = 1000
)

// ResourceMapUsages scans bounded current scenario drafts without reading revision
// histories or run reports. A true truncation flag means callers need to inspect
// additional scenarios manually; no absence claim is safe past the scan cap.
func (r *Repo) ResourceMapUsages(ctx context.Context, designID int64) ([]ResourceMapUsage, bool, error) {
	usages := []ResourceMapUsage{}
	truncated := false
	err := r.db.Read(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT s.id,s.name,s.draft_revision_id,v.document
			FROM design_scenarios s JOIN design_scenario_revisions v
				ON v.scenario_id=s.id AND v.id=s.draft_revision_id
			ORDER BY s.id LIMIT ?`, resourceMapScenarioScanLimit+1)
		if err != nil {
			return err
		}
		defer func() { _ = rows.Close() }()
		count := 0
		for rows.Next() {
			count++
			if count > resourceMapScenarioScanLimit {
				truncated = true
				break
			}
			var scenarioID, revisionID int64
			var scenarioName, raw string
			if err := rows.Scan(&scenarioID, &scenarioName, &revisionID, &raw); err != nil {
				return err
			}
			var document Document
			if err := jsonx.Unmarshal([]byte(raw), &document); err != nil {
				return err
			}
			contracts := make(map[string]ContractSource)
			modes := make(map[string]string)
			for _, contract := range document.Contracts {
				if contract.Source == nil || contract.Source.DesignID != designID {
					continue
				}
				contracts[contract.ID] = *contract.Source
				mode := contract.Mode
				if mode == "" {
					mode = "copy"
				}
				modes[contract.ID] = mode
			}
			for _, message := range document.Messages {
				if message.Operation == nil {
					continue
				}
				binding := message.Operation
				source, ok := contracts[binding.ContractID]
				if !ok {
					continue
				}
				if len(usages) >= resourceMapUsageLimit {
					truncated = true
					break
				}
				usages = append(usages, ResourceMapUsage{ScenarioID: scenarioID, ScenarioName: scenarioName,
					RevisionID: revisionID, MessageID: message.ID, OperationKey: binding.OperationKey,
					ContractRevisionID: source.RevisionID, Mode: modes[binding.ContractID]})
			}
		}
		return rows.Err()
	})
	return usages, truncated, err
}
