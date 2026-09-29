package designscenario

import "context"

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
	read, err := r.readDesignUsageDrafts(ctx, func(draft designUsageDraft) (bool, error) {
		contracts := make(map[string]ContractSource)
		modes := make(map[string]string)
		for _, contract := range draft.Document.Contracts {
			if err := ctx.Err(); err != nil {
				return false, err
			}
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
		for _, message := range draft.Document.Messages {
			if err := ctx.Err(); err != nil {
				return false, err
			}
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
				return true, nil
			}
			usages = append(usages, ResourceMapUsage{
				ScenarioID: draft.ScenarioID, ScenarioName: draft.ScenarioName,
				RevisionID: draft.RevisionID, MessageID: message.ID, OperationKey: binding.OperationKey,
				ContractRevisionID: source.RevisionID, Mode: modes[binding.ContractID],
			})
		}
		return false, nil
	})
	return usages, truncated || len(read.TruncatedReasons) > 0, err
}
