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
	EndpointID string `json:"endpointId,omitempty"`
	RouteID    string `json:"routeId,omitempty"`
}
type ImportLineageValueRef struct {
	Kind        string `json:"kind"`
	NodeKey     string `json:"nodeKey"`
	FacetKey    string `json:"facetKey,omitempty"`
	Collection  string `json:"collection,omitempty"`
	PortKey     string `json:"portKey,omitempty"`
	EndpointKey string `json:"endpointKey,omitempty"`
	RouteKey    string `json:"routeKey,omitempty"`
}
type LineageTransform struct {
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Redacted    bool   `json:"redacted"`
}
type LineageTransport struct {
	EmitsEdgeID    string `json:"emitsEdgeId"`
	DeliveryEdgeID string `json:"deliveryEdgeId"`
}
type ImportLineageTransport struct {
	EmitsEdgeKey    string `json:"emitsEdgeKey"`
	DeliveryEdgeKey string `json:"deliveryEdgeKey"`
}
type LineageMappingAttributes struct {
	Transport      *LineageTransport `json:"transport,omitempty"`
	Sources        []LineageValueRef `json:"sources"`
	Destination    LineageValueRef   `json:"destination"`
	Transform      LineageTransform  `json:"transform"`
	AnalysisStatus string            `json:"analysisStatus"`
	Gaps           []string          `json:"gaps"`
	Description    string            `json:"description,omitempty"`
}
type ImportLineageMappingAttributes struct {
	Transport      *ImportLineageTransport `json:"transport,omitempty"`
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
	if err := validateRepresentationLineageRef(raw, true); err != nil {
		return err
	}
	type plain LineageValueRef
	return json.Unmarshal(raw, (*plain)(r), json.RejectUnknownMembers(true))
}
func (r *ImportLineageValueRef) UnmarshalJSON(raw []byte) error {
	if err := validateEventsLineageRef(raw, false); err != nil {
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
	if err = validateRepresentationAttributes("field_mapping", m, false, true); err != nil {
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
	if err = validateEventsLineageAttributes("field_mapping", m, false, false); err != nil {
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
	return decodeLineageMappingForSchema(attrs, LineageSchemaVersion)
}
func decodeLineageMappingForSchema(attrs map[string]jsontext.Value, schema string) (LineageMappingAttributes, error) {
	var out LineageMappingAttributes
	validator := validateLineageAttributes
	if schema == EventsSchemaVersion {
		validator = validateEventsLineageAttributes
	}
	if schema == ComposedSchemaVersion {
		validator = validateRepresentationAttributes
	}
	if err := validator("field_mapping", attrs, false, true); err != nil {
		return out, err
	}
	raw, err := json.Marshal(attrs)
	if err == nil {
		err = json.Unmarshal(raw, &out)
	}
	return out, err
}
