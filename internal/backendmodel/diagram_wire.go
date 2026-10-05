package backendmodel

import "encoding/json/v2"

func (v *DiagramPin) UnmarshalJSON(b []byte) error {
	type plain DiagramPin
	if err := strictAPIObject(b, []string{"id", "version", "contentHash"}, nil, (*plain)(v)); err != nil {
		return err
	}
	return v.Validate()
}
func (v DiagramPin) Validate() error {
	if !ValidID(v.ID) || v.Version <= 0 || !validHash(v.ContentHash) {
		return invalid("pin", "Exact UUID, positive int64 version and hash required")
	}
	return nil
}
func (v *DiagramEvidenceRef) UnmarshalJSON(b []byte) error {
	type plain DiagramEvidenceRef
	if err := strictAPIObject(b, []string{"revisionId", "evidenceId", "subjectId"}, nil, (*plain)(v)); err != nil {
		return err
	}
	if !ValidID(v.RevisionID) || !ValidID(v.EvidenceID) || !ValidID(v.SubjectID) {
		return invalid("evidence", "Exact evidence identities required")
	}
	return nil
}
func (v *DiagramOrigin) UnmarshalJSON(b []byte) error {
	type plain DiagramOrigin
	var discriminator struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(b, &discriminator); err != nil {
		return err
	}
	var fields []string
	switch discriminator.Kind {
	case "source_assertion":
		fields = []string{"kind", "evidence"}
	case "authored":
		fields = []string{"kind", "reason"}
	default:
		return invalid("origin", "Unknown origin kind")
	}
	*v = DiagramOrigin{}
	if err := strictAPIObject(b, fields, nil, (*plain)(v)); err != nil {
		return err
	}
	return validateDiagramOrigin(*v)
}
func (v *DiagramRef) UnmarshalJSON(b []byte) error {
	type plain DiagramRef
	var discriminator struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(b, &discriminator); err != nil {
		return err
	}
	var fields []string
	switch discriminator.Kind {
	case "record":
		fields = []string{"kind", "recordType", "id"}
	case "artifact":
		fields = []string{"kind", "locator", "rowId"}
	default:
		return invalid("ref", "Unknown reference kind")
	}
	*v = DiagramRef{}
	if err := strictAPIObject(b, fields, nil, (*plain)(v)); err != nil {
		return err
	}
	return validateDiagramRef(*v)
}
func (v *ArchitectureElement) UnmarshalJSON(b []byte) error {
	type plain ArchitectureElement
	*v = ArchitectureElement{}
	return strictAPIObject(b, []string{"id", "label", "origin", "refs", "role", "responsibility", "technology"}, []string{"parentId"}, (*plain)(v))
}
func (v *ArchitectureLink) UnmarshalJSON(b []byte) error {
	type plain ArchitectureLink
	*v = ArchitectureLink{}
	return strictAPIObject(b, []string{"id", "label", "origin", "refs", "from", "to", "relation"}, nil, (*plain)(v))
}
func (v *ArchitecturePayload) UnmarshalJSON(b []byte) error {
	type plain ArchitecturePayload
	*v = ArchitecturePayload{}
	return strictAPIObject(b, []string{"elements", "links", "primarySystemId"}, nil, (*plain)(v))
}
func (v *DiagramDocument) UnmarshalJSON(b []byte) error {
	// Admission is checked before decoding the kind-specific payload.
	var head struct {
		Format string `json:"format"`
		Kind   string `json:"kind"`
	}
	if err := json.Unmarshal(b, &head); err != nil {
		return err
	}
	if head.Format != DiagramDocumentVersion || (head.Kind != "architecture" && head.Kind != "interactions" && head.Kind != "lifecycle" && head.Kind != "business_map") {
		return diagramUnsupported()
	}
	if head.Kind == "business_map" {
		var wire struct {
			Format  string             `json:"format"`
			Kind    string             `json:"kind"`
			Target  BackendReadTarget  `json:"target"`
			Payload BusinessMapPayload `json:"payload"`
		}
		if err := strictAPIObject(b, []string{"format", "kind", "target", "payload"}, nil, &wire); err != nil {
			return err
		}
		*v = DiagramDocument{Format: wire.Format, Kind: wire.Kind, Target: wire.Target, BusinessMap: &wire.Payload}
		return v.Validate()
	}
	if head.Kind == "lifecycle" {
		var wire struct {
			Format  string            `json:"format"`
			Kind    string            `json:"kind"`
			Target  BackendReadTarget `json:"target"`
			Payload LifecyclePayload  `json:"payload"`
		}
		if err := strictAPIObject(b, []string{"format", "kind", "target", "payload"}, nil, &wire); err != nil {
			return err
		}
		*v = DiagramDocument{Format: wire.Format, Kind: wire.Kind, Target: wire.Target, Lifecycle: &wire.Payload}
		return v.Validate()
	}
	if head.Kind == "interactions" {
		var wire struct {
			Format  string             `json:"format"`
			Kind    string             `json:"kind"`
			Target  BackendReadTarget  `json:"target"`
			Payload InteractionPayload `json:"payload"`
		}
		if err := strictAPIObject(b, []string{"format", "kind", "target", "payload"}, nil, &wire); err != nil {
			return err
		}
		*v = DiagramDocument{Format: wire.Format, Kind: wire.Kind, Target: wire.Target, Interactions: &wire.Payload}
		return v.Validate()
	}
	type plain DiagramDocument
	*v = DiagramDocument{}
	if err := strictAPIObject(b, []string{"format", "kind", "target", "payload"}, nil, (*plain)(v)); err != nil {
		return err
	}
	return v.Validate()
}
func (v *DiagramCreateInput) UnmarshalJSON(b []byte) error {
	type plain DiagramCreateInput
	*v = DiagramCreateInput{}
	return strictAPIObject(b, []string{"document", "idempotencyKey"}, nil, (*plain)(v))
}
func (v *DiagramSaveInput) UnmarshalJSON(b []byte) error {
	type plain DiagramSaveInput
	*v = DiagramSaveInput{}
	return strictAPIObject(b, []string{"expectedVersion", "document", "idempotencyKey"}, nil, (*plain)(v))
}
func (v *DiagramForkInput) UnmarshalJSON(b []byte) error {
	type plain DiagramForkInput
	*v = DiagramForkInput{}
	return strictAPIObject(b, []string{"source", "target", "reason", "idempotencyKey"}, []string{"architecture"}, (*plain)(v))
}
