package backendmodel

import (
	"encoding/json/v2"
)

const MaxRepresentationSelectorSegments = 32

type RepresentationAttributes struct {
	QualifiedName  string   `json:"qualifiedName"`
	AnalysisStatus string   `json:"analysisStatus"`
	Gaps           []string `json:"gaps"`
	Description    string   `json:"description,omitempty"`
}

// RepresentationKnown preserves an explicit false value and distinguishes it
// from an absent value in the unknown branch.
type RepresentationKnown[T string | bool] struct {
	Status string `json:"status"`
	Value  *T     `json:"value,omitzero"`
	Reason string `json:"reason,omitempty"`
}

type RepresentationPropertySegment struct {
	Property string `json:"property"`
}

type RepresentationFieldAttributes struct {
	Selector       []RepresentationPropertySegment `json:"selector"`
	NativeType     RepresentationKnown[string]     `json:"nativeType"`
	Nullable       RepresentationKnown[bool]       `json:"nullable"`
	Cardinality    RepresentationKnown[string]     `json:"cardinality"`
	AnalysisStatus string                          `json:"analysisStatus"`
	Gaps           []string                        `json:"gaps"`
	Description    string                          `json:"description,omitempty"`
}

type ImportRepresentationValueRef struct {
	Kind    string          `json:"kind"`
	NodeRef ImportRecordRef `json:"nodeRef"`
}

func (a *RepresentationAttributes) UnmarshalJSON(raw []byte) error {
	m, err := relationalObject(raw)
	if err != nil {
		return err
	}
	if err := validateRepresentationAttributes("domain_entity", m, false, true); err != nil {
		return err
	}
	type plain RepresentationAttributes
	return json.Unmarshal(raw, (*plain)(a), json.RejectUnknownMembers(true))
}

func (a *RepresentationFieldAttributes) UnmarshalJSON(raw []byte) error {
	m, err := relationalObject(raw)
	if err != nil {
		return err
	}
	if err := validateRepresentationAttributes("representation_field", m, false, true); err != nil {
		return err
	}
	type plain RepresentationFieldAttributes
	return json.Unmarshal(raw, (*plain)(a), json.RejectUnknownMembers(true))
}

func (r *ImportRepresentationValueRef) UnmarshalJSON(raw []byte) error {
	m, err := relationalObject(raw)
	if err != nil {
		return err
	}
	if err := relationalFields(m, []string{"kind", "nodeRef"}, nil); err != nil {
		return err
	}
	if runtimeString(m["kind"]) != "representation_field" {
		return semantic("kind", "Expected representation field reference")
	}
	type plain ImportRepresentationValueRef
	return json.Unmarshal(raw, (*plain)(r), json.RejectUnknownMembers(true))
}
