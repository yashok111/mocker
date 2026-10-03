package backendmodel

import "encoding/json/jsontext"

const (
	EventsQueryPolicy         = "source-events-projection-v1"
	EventsMaxExaminedEdges    = 20000
	EventsMaxItems            = 5000
	EventsMaxAuxiliaryRecords = 20000
	EventsMaxWitnessRecords   = 256
)

// EventsQueryInput selects a pure projection of one immutable source5 revision.
type EventsQueryInput struct {
	RevisionID string `json:"revisionId"`
	View       string `json:"view"`
	SeedNodeID string `json:"seedNodeId,omitempty"`
	ServiceID  string `json:"serviceId,omitempty"`
	Limit      int    `json:"limit,omitzero"`
	Cursor     string `json:"cursor,omitempty"`
	present    map[string]bool
}

type EventsQueryLimits struct {
	MaxExaminedEdges    int    `json:"maxExaminedEdges"`
	MaxItems            int    `json:"maxItems"`
	MaxAuxiliaryRecords int    `json:"maxAuxiliaryRecords"`
	MaxWitnessRecords   int    `json:"maxWitnessRecords"`
	DefaultPageSize     int    `json:"defaultPageSize"`
	MaxPageSize         int    `json:"maxPageSize"`
	ScanPolicy          string `json:"scanPolicy"`
}

type EventsPage struct {
	ProjectID            string            `json:"projectId"`
	RevisionID           string            `json:"revisionId"`
	SemanticHash         string            `json:"semanticHash"`
	Policy               string            `json:"policy"`
	View                 string            `json:"view"`
	SeedNodeID           string            `json:"seedNodeId,omitempty"`
	ServiceID            string            `json:"serviceId,omitempty"`
	Items                []EventsItem      `json:"items"`
	NextCursor           string            `json:"nextCursor"`
	Coverage             RevisionCoverage  `json:"coverage"`
	Limits               EventsQueryLimits `json:"limits"`
	Complete             bool              `json:"complete"`
	Truncated            bool              `json:"truncated"`
	TruncationReasons    []string          `json:"truncationReasons"`
	Limitations          []string          `json:"limitations"`
	TotalEdgeCount       int               `json:"totalEdgeCount"`
	ExaminedEdgeCount    int               `json:"examinedEdgeCount"`
	ConstructedItemCount int               `json:"constructedItemCount"`
	AuxiliaryRecordCount int               `json:"auxiliaryRecordCount"`
}

// EventsItem is a named discriminated union. Exactly one payload agrees with Kind.
// A boundary preserves the available exact route/call/job identity and witnesses.
type EventsItem struct {
	Kind        string                 `json:"kind"`
	Route       *EventsRouteItem       `json:"route,omitzero"`
	Job         *EventsJobItem         `json:"job,omitzero"`
	ServiceCall *EventsServiceCallItem `json:"serviceCall,omitzero"`
	Boundary    *EventsBoundaryItem    `json:"boundary,omitzero"`
}

type EventsReferences struct {
	ProducerID      string `json:"producerId,omitempty"`
	MessageID       string `json:"messageId,omitempty"`
	ChannelID       string `json:"channelId,omitempty"`
	ConsumerID      string `json:"consumerId,omitempty"`
	EmitsEdgeID     string `json:"emitsEdgeId,omitempty"`
	DeliveryEdgeID  string `json:"deliveryEdgeId,omitempty"`
	JobID           string `json:"jobId,omitempty"`
	CallStepID      string `json:"callStepId,omitempty"`
	CallsEdgeID     string `json:"callsEdgeId,omitempty"`
	OperationID     string `json:"operationId,omitempty"`
	TargetID        string `json:"targetId,omitempty"`
	TargetServiceID string `json:"targetServiceId,omitempty"`
}

type EventsWitness struct {
	Provenance  string   `json:"provenance"`
	NodeIDs     []string `json:"nodeIds"`
	EdgeIDs     []string `json:"edgeIds"`
	EvidenceIDs []string `json:"evidenceIds"`
	Status      string   `json:"status"`
	Limitations []string `json:"limitations"`
}

// EventsScalar preserves native configured values and explicit unknown reasons.
type EventsScalar struct {
	Status string         `json:"status"`
	Value  jsontext.Value `json:"value,omitzero"`
	Reason string         `json:"reason,omitempty"`
}

type EventsTrigger struct {
	Kind       string       `json:"kind"`
	Expression EventsScalar `json:"expression,omitzero"`
	Timezone   EventsScalar `json:"timezone,omitzero"`
	Duration   EventsScalar `json:"duration,omitzero"`
	Reason     string       `json:"reason,omitempty"`
}

type EventsDispatch struct {
	HandlesEdgeID      string        `json:"handlesEdgeId,omitempty"`
	HandlerID          string        `json:"handlerId,omitempty"`
	UnresolvedTargetID string        `json:"unresolvedTargetId,omitempty"`
	FlowIDs            []string      `json:"flowIds"`
	Witness            EventsWitness `json:"witness"`
}

type EventsRelatedRoute struct {
	Kind        string        `json:"kind"`
	EdgeID      string        `json:"edgeId"`
	ConsumerID  string        `json:"consumerId"`
	ChannelID   string        `json:"channelId"`
	MessageID   string        `json:"messageId"`
	Reason      string        `json:"reason"`
	Delay       EventsScalar  `json:"delay,omitzero"`
	MaxAttempts EventsScalar  `json:"maxAttempts,omitzero"`
	Witness     EventsWitness `json:"witness"`
}

type EventsTransactionContext struct {
	Status        string `json:"status"`
	TransactionID string `json:"transactionId,omitempty"`
	Reason        string `json:"reason,omitempty"`
}

type EventsEmitContext struct {
	FlowID         string                   `json:"flowId,omitempty"`
	Transaction    EventsTransactionContext `json:"transaction"`
	ControlWitness *EventsWitness           `json:"controlWitness,omitzero"`
	Limitations    []string                 `json:"limitations"`
}

type EventsRouteItem struct {
	References  EventsReferences     `json:"references"`
	Condition   EventsScalar         `json:"condition"`
	Group       EventsScalar         `json:"group"`
	Dispatch    []EventsDispatch     `json:"dispatch"`
	Related     []EventsRelatedRoute `json:"related"`
	EmitContext *EventsEmitContext   `json:"emitContext,omitzero"`
	Witness     EventsWitness        `json:"witness"`
}

type EventsJobItem struct {
	References EventsReferences `json:"references"`
	Trigger    EventsTrigger    `json:"trigger"`
	Dispatch   []EventsDispatch `json:"dispatch"`
	Witness    EventsWitness    `json:"witness"`
}

type EventsServiceCallItem struct {
	References EventsReferences `json:"references"`
	Dispatch   []EventsDispatch `json:"dispatch"`
	Witness    EventsWitness    `json:"witness"`
}

type EventsBoundaryItem struct {
	View        string               `json:"view"`
	Reason      string               `json:"reason"`
	References  EventsReferences     `json:"references"`
	Condition   EventsScalar         `json:"condition,omitzero"`
	Group       EventsScalar         `json:"group,omitzero"`
	Trigger     *EventsTrigger       `json:"trigger,omitzero"`
	Dispatch    []EventsDispatch     `json:"dispatch"`
	Related     []EventsRelatedRoute `json:"related"`
	EmitContext *EventsEmitContext   `json:"emitContext,omitzero"`
	Witness     EventsWitness        `json:"witness"`
}
