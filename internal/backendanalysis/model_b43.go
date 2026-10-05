package backendanalysis

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"

	"github.com/yashok111/mocker/internal/backendmodel"
)

type PackagePayload struct {
	ChangeProposal backendmodel.ProposalReadTarget            `json:"changeProposal"`
	BaseRevisionID string                                     `json:"baseRevisionId"`
	BasePins       backendmodel.EffectiveGraphPins            `json:"basePins"`
	DraftPins      backendmodel.EffectiveGraphPins            `json:"draftPins"`
	BaseSource     backendmodel.AnalysisSourcePins            `json:"baseSource"`
	DraftSource    backendmodel.AnalysisSourcePins            `json:"draftSource"`
	EvidencePins   []backendmodel.AnalysisEvidenceDocumentPin `json:"evidencePins"`
}
type ConformancePayload struct {
	PackagePayload
	ResultRevisionID string                          `json:"resultRevisionId"`
	ResultPins       backendmodel.EffectiveGraphPins `json:"resultPins"`
	ResultSource     backendmodel.AnalysisSourcePins `json:"resultSource"`
	IdentityMap      []IdentityMapEntry              `json:"identityMap"`
	TestAttachments  []CriterionAttachment           `json:"testAttachments"`
}
type EndpointReviewPayload struct {
	FromRevisionID   string                                     `json:"fromRevisionId"`
	ToRevisionID     string                                     `json:"toRevisionId"`
	BeforeEndpointID string                                     `json:"beforeEndpointId"`
	AfterEndpointID  *string                                    `json:"afterEndpointId"`
	BeforePins       backendmodel.EffectiveGraphPins            `json:"beforePins"`
	AfterPins        backendmodel.EffectiveGraphPins            `json:"afterPins"`
	BeforeSource     backendmodel.AnalysisSourcePins            `json:"beforeSource"`
	AfterSource      backendmodel.AnalysisSourcePins            `json:"afterSource"`
	EvidencePins     []backendmodel.AnalysisEvidenceDocumentPin `json:"evidencePins"`
	Intent           *PackagePayload                            `json:"intent,omitzero"`
}
type ImmutableInputV2 struct {
	DocumentVersion  string `json:"documentVersion"`
	Kind             string `json:"kind"`
	ProjectID        string `json:"projectId"`
	Payload          any    `json:"payload"`
	Limits           Limits `json:"limits"`
	RuleSetVersion   string `json:"ruleSetVersion"`
	TraversalVersion string `json:"traversalVersion"`
	ObservationMode  string `json:"observationMode"`
}
type InputContextV2 struct {
	DocumentVersion  string `json:"documentVersion"`
	Kind             string `json:"kind"`
	Payload          any    `json:"payload"`
	Limits           Limits `json:"limits"`
	RuleSetVersion   string `json:"ruleSetVersion"`
	TraversalVersion string `json:"traversalVersion"`
	ObservationMode  string `json:"observationMode"`
}

func (in *ImmutableInputV2) UnmarshalJSON(raw []byte) error {
	if _, err := closed(raw, []string{"documentVersion", "kind", "projectId", "payload", "limits", "ruleSetVersion", "traversalVersion", "observationMode"}, nil); err != nil {
		return err
	}
	type plain ImmutableInputV2
	var next plain
	var payload jsontext.Value
	wire := struct {
		*plain
		Payload *jsontext.Value `json:"payload"`
	}{&next, &payload}
	if err := decode(raw, &wire); err != nil {
		return err
	}
	if next.DocumentVersion != "backend-analysis-input/v2" || next.RuleSetVersion != "b43-rules/v1" || next.TraversalVersion != "b42-traversal/v1" || next.ObservationMode != "none" {
		return malformed("Unsupported immutable envelope")
	}
	var out any
	switch next.Kind {
	case "change_package":
		out = new(PackagePayload)
	case "conformance":
		out = new(ConformancePayload)
	case "endpoint_review":
		out = new(EndpointReviewPayload)
	default:
		return malformed("Invalid immutable kind")
	}
	if err := decodeB43Payload(payload, out); err != nil {
		return err
	}
	next.Payload = out
	*in = ImmutableInputV2(next)
	return nil
}

func (in ImmutableInputV2) MarshalJSON() ([]byte, error) {
	type plain ImmutableInputV2
	payload, err := encodeB43Payload(in.Payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		plain
		Payload jsontext.Value `json:"payload"`
	}{plain(in), payload}, json.Deterministic(true))
}
func encodeB43Payload(payload any) (jsontext.Value, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	var fields map[string]jsontext.Value
	if err = json.Unmarshal(raw, &fields); err != nil {
		return nil, err
	}
	pins := map[string]backendmodel.EffectiveGraphPins{}
	switch p := payload.(type) {
	case *PackagePayload:
		pins["basePins"] = p.BasePins
		pins["draftPins"] = p.DraftPins
	case *ConformancePayload:
		pins["basePins"] = p.BasePins
		pins["draftPins"] = p.DraftPins
		pins["resultPins"] = p.ResultPins
	case *EndpointReviewPayload:
		pins["beforePins"] = p.BeforePins
		pins["afterPins"] = p.AfterPins
		if p.Intent != nil {
			fields["intent"], err = encodeB43Payload(p.Intent)
			if err != nil {
				return nil, err
			}
		}
	}
	for key, pin := range pins {
		encoded, e := encodePins(pin)
		if e != nil {
			return nil, e
		}
		fields[key], err = json.Marshal(encoded)
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(fields, json.Deterministic(true))
}

func decodeB43Payload(raw []byte, out any) error {
	if err := validateB43PayloadShape(raw, out); err != nil {
		return err
	}
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	pins := map[string]*backendmodel.EffectiveGraphPins{}
	switch p := out.(type) {
	case *PackagePayload:
		pins["basePins"] = &p.BasePins
		pins["draftPins"] = &p.DraftPins
	case *ConformancePayload:
		pins["basePins"] = &p.BasePins
		pins["draftPins"] = &p.DraftPins
		pins["resultPins"] = &p.ResultPins
	case *EndpointReviewPayload:
		pins["beforePins"] = &p.BeforePins
		pins["afterPins"] = &p.AfterPins
	}
	if err := validateB43PayloadSources(fields); err != nil {
		return err
	}
	saved := map[string]backendmodel.EffectiveGraphPins{}
	for key := range pins {
		var pin persistedPins
		if err := validateB43Pins(fields[key]); err != nil {
			return err
		}
		if err := decode(fields[key], &pin); err != nil {
			return err
		}
		decoded, err := decodePins(pin)
		if err != nil {
			return err
		}
		saved[key] = decoded
		delete(fields, key)
	}
	var intent jsontext.Value
	if _, ok := out.(*EndpointReviewPayload); ok {
		intent = fields["intent"]
		delete(fields, "intent")
	}
	remaining, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	if err = decode(remaining, out); err != nil {
		return err
	}
	for key, ptr := range pins {
		*ptr = saved[key]
	}
	if p, ok := out.(*EndpointReviewPayload); ok && len(intent) > 0 {
		p.Intent = new(PackagePayload)
		if err = decodeB43Payload(intent, p.Intent); err != nil {
			return err
		}
	}
	return nil
}

func (in InputContextV2) MarshalJSON() ([]byte, error) {
	type plain InputContextV2
	payload, err := encodeB43Payload(in.Payload)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		plain
		Payload jsontext.Value `json:"payload"`
	}{plain(in), payload})
}

func validateB43Pins(raw []byte) error {
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if bytes.Equal(bytes.TrimSpace(fields["artifactContext"]), []byte("null")) {
		fields["artifactContext"] = []byte(`{}`)
	}
	check, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	_, err = closed(check, []string{"targetHash", "viewSchemaVersion", "structuralSchemaVersion", "effectiveSemanticHash", "baseRevisionId", "baseSemanticHash", "sourceVectorHash", "sourceSnapshotIds", "artifactPins", "artifactContext"}, nil)
	return err
}

func validateB43PayloadShape(raw []byte, out any) error {
	required := []string{"changeProposal", "baseRevisionId", "basePins", "draftPins", "baseSource", "draftSource", "evidencePins"}
	optional := []string{}
	check := raw
	switch out.(type) {
	case *ConformancePayload:
		required = append(required, "resultRevisionId", "resultPins", "resultSource", "identityMap", "testAttachments")
	case *EndpointReviewPayload:
		required = []string{"fromRevisionId", "toRevisionId", "beforeEndpointId", "afterEndpointId", "beforePins", "afterPins", "beforeSource", "afterSource", "evidencePins"}
		optional = []string{"intent"}
		var nullable map[string]jsontext.Value
		if err := json.Unmarshal(raw, &nullable); err != nil {
			return err
		}
		if bytes.Equal(bytes.TrimSpace(nullable["afterEndpointId"]), []byte("null")) {
			nullable["afterEndpointId"] = []byte(`""`)
		}
		var err error
		check, err = json.Marshal(nullable)
		if err != nil {
			return err
		}
	}
	if _, err := closed(check, required, optional); err != nil {
		return err
	}
	return nil
}

func validateB43PayloadSources(fields map[string]jsontext.Value) error {
	for _, key := range []string{"baseSource", "draftSource", "resultSource", "beforeSource", "afterSource"} {
		if source, ok := fields[key]; ok {
			if _, err := closed(source, []string{"revisionId", "semanticHash", "contentHash", "sourceVectorHash", "sourceSnapshotIds"}, nil); err != nil {
				return err
			}
		}
	}
	var evidence []jsontext.Value
	if err := json.Unmarshal(fields["evidencePins"], &evidence); err != nil {
		return err
	}
	for _, pin := range evidence {
		if _, err := closed(pin, []string{"revisionId", "kind", "contentHash"}, nil); err != nil {
			return err
		}
	}
	return nil
}
