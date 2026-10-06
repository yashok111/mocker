package backendobservations

import (
	bm "github.com/yashok111/mocker/internal/backendmodel"
)

type CorrelationSettings struct {
	InferSourceLocator bool `json:"inferSourceLocator"`
	InferFingerprint   bool `json:"inferFingerprint"`
}
type Override struct {
	RecordID string        `json:"recordId"`
	Ref      bm.DiagramRef `json:"ref"`
	Reason   string        `json:"reason"`
}
type CorrelateInput struct {
	Observation                VersionReceipt        `json:"observation"`
	RevisionID                 string                `json:"revisionId"`
	SourceHash                 string                `json:"sourceHash"`
	TargetGraphHash            string                `json:"targetGraphHash"`
	ServiceID                  string                `json:"serviceId"`
	Policy                     string                `json:"policy"`
	Settings                   CorrelationSettings   `json:"settings"`
	Overrides                  []Override            `json:"overrides"`
	ExpectedCorrelationVersion int64                 `json:"expectedCorrelationVersion"`
	IdempotencyKey             string                `json:"idempotencyKey"`
	DiagramScope               *bm.DiagramScopeInput `json:"diagramScope,omitzero"`
	ScopeHash                  string                `json:"scopeHash,omitempty"`
}
type CorrelationRow struct {
	RecordID   string          `json:"recordId"`
	Outcome    string          `json:"outcome"`
	Candidates []bm.DiagramRef `json:"candidates"`
	Selected   *bm.DiagramRef  `json:"selected,omitzero"`
	Method     string          `json:"method"`
	Reasons    []string        `json:"reasons"`
}
type DiagramCorrelationRow struct {
	RecordID  string                    `json:"recordId"`
	Selectors []bm.DiagramScopeSelector `json:"selectors"`
	Basis     string                    `json:"basis"`
	Gaps      []string                  `json:"gaps"`
}
type CorrelationSnapshot struct {
	Total            int                     `json:"total"`
	NextCursor       string                  `json:"nextCursor"`
	Version          int64                   `json:"version"`
	ContentHash      string                  `json:"contentHash"`
	Input            CorrelateInput          `json:"input"`
	SourceCompatible bool                    `json:"sourceCompatible"`
	Rows             []CorrelationRow        `json:"rows"`
	DiagramRows      []DiagramCorrelationRow `json:"diagramRows"`
	DiagramScope     *bm.DiagramScope        `json:"diagramScope,omitzero"`
	Gaps             []string                `json:"gaps"`
}

func (v *CorrelateInput) UnmarshalJSON(b []byte) error {
	type wire CorrelateInput
	return decode(b, (*wire)(v))
}
