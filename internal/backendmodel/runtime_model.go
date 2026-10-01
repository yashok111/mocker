package backendmodel

import "encoding/json/jsontext"

const (
	MaxRuntimePorts          = 500
	MaxRuntimeExitReferences = 500
	MaxRuntimeCallCandidates = 500
	MaxRuntimeNativeBytes    = 64 << 10
	MaxRuntimeTextBytes      = 4096
)

type runtimePort struct {
	Key        string           `json:"key"`
	Name       string           `json:"name"`
	NativeType relationalScalar `json:"nativeType"`
}

type runtimeTransactionContext struct {
	Status         string `json:"status"`
	TransactionKey string `json:"transactionKey,omitzero"`
	TransactionID  string `json:"transactionId,omitzero"`
	Reason         string `json:"reason,omitzero"`
}

// The strict validator chooses one shape before decoding this internal union.
// Local ports are addresses; they do not describe field lineage or execution.
type runtimeAttributes struct {
	AnalysisStatus     string                    `json:"analysisStatus,omitzero"`
	Gaps               []string                  `json:"gaps,omitzero"`
	EntryStepKey       string                    `json:"entryStepKey,omitzero"`
	EntryStepID        string                    `json:"entryStepId,omitzero"`
	ExitStepKeys       []string                  `json:"exitStepKeys,omitzero"`
	ExitStepIDs        []string                  `json:"exitStepIds,omitzero"`
	ExitStatus         string                    `json:"exitStatus,omitzero"`
	StepKind           string                    `json:"stepKind,omitzero"`
	Inputs             []runtimePort             `json:"inputs,omitzero"`
	Outputs            []runtimePort             `json:"outputs,omitzero"`
	TransactionContext runtimeTransactionContext `json:"transactionContext,omitzero"`
	NativeText         jsontext.Value            `json:"nativeText,omitzero"`
	NativeReason       string                    `json:"nativeReason,omitzero"`
	Expression         relationalScalar          `json:"expression,omitzero"`
	Reason             string                    `json:"reason,omitzero"`
	DispatchStatus     string                    `json:"dispatchStatus,omitzero"`
	DispatchReason     string                    `json:"dispatchReason,omitzero"`
	Dialect            string                    `json:"dialect,omitzero"`
	NativeDefinition   jsontext.Value            `json:"nativeDefinition,omitzero"`
	DefinitionReason   string                    `json:"definitionReason,omitzero"`
	Parameters         []runtimePort             `json:"parameters,omitzero"`
	Results            []runtimePort             `json:"results,omitzero"`
	ColumnScope        string                    `json:"columnScope,omitzero"`
	DatastoreKey       string                    `json:"datastoreKey,omitzero"`
	DatastoreID        string                    `json:"datastoreId,omitzero"`
	ConnectionScope    relationalScalar          `json:"connectionScope,omitzero"`
	IsolationLevel     relationalScalar          `json:"isolationLevel,omitzero"`
	BoundaryStatus     string                    `json:"boundaryStatus,omitzero"`
	Label              string                    `json:"label,omitzero"`
	Condition          relationalScalar          `json:"condition,omitzero"`
	Outcome            string                    `json:"outcome,omitzero"`
	AccessMode         string                    `json:"accessMode,omitzero"`
	FacetKey           string                    `json:"facetKey,omitzero"`
	ScopeReason        string                    `json:"scopeReason,omitzero"`
	Description        *string                   `json:"description,omitzero"`
}
