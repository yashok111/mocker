package apidesign

// ImpactInput compares a saved base with one explicit target.
type ImpactInput struct {
	FromRevisionID int64   `json:"fromRevisionId"`
	ToRevisionID   *int64  `json:"toRevisionId,omitempty"`
	Document       *string `json:"document,omitempty"`
}

type ImpactAnalysis struct {
	Changes     []ImpactChange     `json:"changes"`
	Affected    []ImpactEntity     `json:"affected"`
	Evidence    []ImpactEvidence   `json:"evidence"`
	Diagnostics []ImpactDiagnostic `json:"diagnostics"`
	Coverage    ImpactCoverage     `json:"coverage"`
	Complete    bool               `json:"complete"`
}

type ImpactReport struct {
	ImpactAnalysis
	DesignID       int64  `json:"designId"`
	Version        int64  `json:"version"`
	FromRevisionID int64  `json:"fromRevisionId"`
	ToRevisionID   *int64 `json:"toRevisionId,omitempty"`
	FromHash       string `json:"fromHash"`
	ProposedHash   string `json:"proposedHash"`
}

type ImpactChange struct {
	ID              string  `json:"id"`
	Pointer         string  `json:"pointer"`
	Kind            string  `json:"kind"`
	ChangeClass     string  `json:"changeClass"`
	Compatibility   string  `json:"compatibility"`
	ReasonCode      string  `json:"reasonCode"`
	Explanation     string  `json:"explanation"`
	BeforeJSON      *string `json:"beforeJSON,omitempty"`
	AfterJSON       *string `json:"afterJSON,omitempty"`
	BeforeTruncated bool    `json:"beforeTruncated"`
	AfterTruncated  bool    `json:"afterTruncated"`
}

type ImpactEntity struct {
	ID     string         `json:"id"`
	Kind   string         `json:"kind"`
	Label  string         `json:"label"`
	Before *ImpactLocator `json:"before,omitempty"`
	After  *ImpactLocator `json:"after,omitempty"`
}

type ImpactLocator struct {
	Pointer          string `json:"pointer"`
	SourcePointer    string `json:"sourcePointer,omitempty"`
	OperationKey     string `json:"operationKey,omitempty"`
	Method           string `json:"method,omitempty"`
	Path             string `json:"path,omitempty"`
	ResourceID       string `json:"resourceId,omitempty"`
	ScenarioID       int64  `json:"scenarioId,omitempty"`
	ScenarioName     string `json:"scenarioName,omitempty"`
	ScenarioRevision int64  `json:"scenarioRevision,omitempty"`
	ContractID       string `json:"contractId,omitempty"`
	PinnedRevisionID int64  `json:"pinnedRevisionId,omitempty"`
	Mode             string `json:"mode,omitempty"`
	MessageID        string `json:"messageId,omitempty"`
	DiagramID        string `json:"diagramId,omitempty"`
	TransitionID     string `json:"transitionId,omitempty"`
}

type ImpactReferenceSite struct {
	Pointer       string `json:"pointer"`
	TargetPointer string `json:"targetPointer"`
	Kind          string `json:"kind"`
}

type ImpactEvidence struct {
	ID             string                `json:"id"`
	ChangeID       string                `json:"changeId"`
	EntityID       string                `json:"entityId"`
	Side           string                `json:"side"`
	Direction      string                `json:"direction"`
	ReferenceSites []ImpactReferenceSite `json:"referenceSites"`
	Explanation    string                `json:"explanation"`
}

type ImpactDiagnostic struct {
	Code     string `json:"code"`
	Severity string `json:"severity"`
	Side     string `json:"side"`
	Pointer  string `json:"pointer"`
	Message  string `json:"message"`
	EntityID string `json:"entityId,omitempty"`
}

type ImpactCoverage struct {
	ScenariosScanned       int      `json:"scenariosScanned"`
	ScenarioUsagesReturned int      `json:"scenarioUsagesReturned"`
	ChangesReturned        int      `json:"changesReturned"`
	EntitiesReturned       int      `json:"entitiesReturned"`
	EvidenceReturned       int      `json:"evidenceReturned"`
	TruncatedReasons       []string `json:"truncatedReasons"`
}
