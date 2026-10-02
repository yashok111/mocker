package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
)

// LineageValueRef identifies a value in one pinned source revision. PortKey is
// an opaque local address; equality includes every field, including the facet.
type LineageValueRef struct {
	Kind       string `json:"kind"`
	NodeID     string `json:"nodeId"`
	FacetKey   string `json:"facetKey,omitempty"`
	Collection string `json:"collection,omitempty"`
	PortKey    string `json:"portKey,omitempty"`
}
type ImportLineageValueRef struct {
	Kind       string `json:"kind"`
	NodeKey    string `json:"nodeKey"`
	FacetKey   string `json:"facetKey,omitempty"`
	Collection string `json:"collection,omitempty"`
	PortKey    string `json:"portKey,omitempty"`
}
type LineageTransform struct {
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Redacted    bool   `json:"redacted"`
}
type LineageMappingAttributes struct {
	Sources        []LineageValueRef `json:"sources"`
	Destination    LineageValueRef   `json:"destination"`
	Transform      LineageTransform  `json:"transform"`
	AnalysisStatus string            `json:"analysisStatus"`
	Gaps           []string          `json:"gaps"`
	Description    string            `json:"description,omitempty"`
}
type ImportLineageMappingAttributes struct {
	Sources        []ImportLineageValueRef `json:"sources"`
	Destination    ImportLineageValueRef   `json:"destination"`
	Transform      LineageTransform        `json:"transform"`
	AnalysisStatus string                  `json:"analysisStatus"`
	Gaps           []string                `json:"gaps"`
	Description    string                  `json:"description,omitempty"`
}
type APIFieldPathSegment struct {
	Property string `json:"property,omitempty"`
	Items    bool   `json:"items,omitzero"`
}
type APIFieldSelector struct {
	Kind string                `json:"kind"`
	Name string                `json:"name,omitempty"`
	Path []APIFieldPathSegment `json:"path,omitzero"`
}
type APIFieldNativeType struct {
	Status string `json:"status"`
	Value  string `json:"value,omitempty"`
	Reason string `json:"reason,omitempty"`
}
type APIFieldAttributes struct {
	Direction      string             `json:"direction"`
	Location       string             `json:"location"`
	Selector       APIFieldSelector   `json:"selector"`
	ResponseStatus string             `json:"responseStatus,omitempty"`
	MediaType      string             `json:"mediaType,omitempty"`
	NativeType     APIFieldNativeType `json:"nativeType"`
	AnalysisStatus string             `json:"analysisStatus"`
	Gaps           []string           `json:"gaps"`
	Description    string             `json:"description,omitempty"`
}

func (r *LineageValueRef) UnmarshalJSON(raw []byte) error {
	if err := validateLineageRef(raw, true); err != nil {
		return err
	}
	type plain LineageValueRef
	return json.Unmarshal(raw, (*plain)(r), json.RejectUnknownMembers(true))
}
func (r *ImportLineageValueRef) UnmarshalJSON(raw []byte) error {
	if err := validateLineageRef(raw, false); err != nil {
		return err
	}
	type plain ImportLineageValueRef
	return json.Unmarshal(raw, (*plain)(r), json.RejectUnknownMembers(true))
}
func (a *LineageMappingAttributes) UnmarshalJSON(raw []byte) error {
	m, err := relationalObject(raw)
	if err != nil {
		return err
	}
	if err = validateLineageAttributes("field_mapping", m, false, true); err != nil {
		return err
	}
	type plain LineageMappingAttributes
	return json.Unmarshal(raw, (*plain)(a), json.RejectUnknownMembers(true))
}
func (a *ImportLineageMappingAttributes) UnmarshalJSON(raw []byte) error {
	m, err := relationalObject(raw)
	if err != nil {
		return err
	}
	if err = validateLineageAttributes("field_mapping", m, false, false); err != nil {
		return err
	}
	type plain ImportLineageMappingAttributes
	return json.Unmarshal(raw, (*plain)(a), json.RejectUnknownMembers(true))
}
func (a *APIFieldAttributes) UnmarshalJSON(raw []byte) error {
	m, err := relationalObject(raw)
	if err != nil {
		return err
	}
	if err = validateLineageAttributes("api_field", m, false, true); err != nil {
		return err
	}
	type plain APIFieldAttributes
	return json.Unmarshal(raw, (*plain)(a), json.RejectUnknownMembers(true))
}
func decodeLineageMapping(attrs map[string]jsontext.Value) (LineageMappingAttributes, error) {
	var out LineageMappingAttributes
	raw, err := json.Marshal(attrs)
	if err == nil {
		err = json.Unmarshal(raw, &out)
	}
	return out, err
}
