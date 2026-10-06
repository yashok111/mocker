package backendportable

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
)

func portableObject(raw []byte, required []string, out any) error {
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fault(422, "Expected a closed portable object")
	}
	if fields == nil {
		return fault(422, "Expected a portable object")
	}
	for _, key := range required {
		value, ok := fields[key]
		if !ok || string(value) == "null" {
			return fault(422, "Required nonnull portable field: "+key)
		}
	}
	if err := json.Unmarshal(raw, out, json.RejectUnknownMembers(true)); err != nil {
		return fault(422, "Invalid portable document: "+err.Error())
	}
	return nil
}
func (in *ExportInput) UnmarshalJSON(raw []byte) error {
	type plain ExportInput
	var v plain
	if err := portableObject(raw, []string{"selection", "idempotencyKey"}, &v); err != nil {
		return err
	}
	*in = ExportInput(v)
	return nil
}
func (in *BeginInput) UnmarshalJSON(raw []byte) error {
	type plain BeginInput
	var v plain
	if err := portableObject(raw, []string{"manifest", "idempotencyKey"}, &v); err != nil {
		return err
	}
	*in = BeginInput(v)
	return nil
}
func (in *PutInput) UnmarshalJSON(raw []byte) error {
	type plain PutInput
	var v plain
	if err := portableObject(raw, []string{"expectedVersion", "index", "body", "idempotencyKey"}, &v); err != nil {
		return err
	}
	*in = PutInput(v)
	return nil
}
func (in *PreviewInput) UnmarshalJSON(raw []byte) error {
	type plain PreviewInput
	var v plain
	if err := portableObject(raw, []string{"expectedVersion", "name", "artifactMappings", "idempotencyKey"}, &v); err != nil {
		return err
	}
	*in = PreviewInput(v)
	return nil
}
func (in *CommitInput) UnmarshalJSON(raw []byte) error {
	type plain CommitInput
	var v plain
	if err := portableObject(raw, []string{"expectedVersion", "candidateHash", "idempotencyKey"}, &v); err != nil {
		return err
	}
	*in = CommitInput(v)
	return nil
}
func (in *SessionInput) UnmarshalJSON(raw []byte) error {
	type plain SessionInput
	var v plain
	if err := portableObject(raw, []string{"expectedVersion", "idempotencyKey"}, &v); err != nil {
		return err
	}
	*in = SessionInput(v)
	return nil
}

func (in *Selection) UnmarshalJSON(raw []byte) error {
	type plain Selection
	var v plain
	if err := portableObject(raw, []string{"projectId", "target", "targetHash", "diagramViews"}, &v); err != nil {
		return err
	}
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if string(fields["savedViews"]) == "null" {
		return fault(422, "savedViews must be an array")
	}
	*in = Selection(v)
	return nil
}
func (in *Manifest) UnmarshalJSON(raw []byte) error {
	type plain Manifest
	var v plain
	if err := portableObject(raw, []string{"format", "originInstallationId", "schemas", "selection", "chunks"}, &v); err != nil {
		return err
	}
	*in = Manifest(v)
	return nil
}
func (in *ChunkDescriptor) UnmarshalJSON(raw []byte) error {
	type plain ChunkDescriptor
	var v plain
	if err := portableObject(raw, []string{"index", "sha256", "bytes", "records"}, &v); err != nil {
		return err
	}
	*in = ChunkDescriptor(v)
	return nil
}
func (in *Record) UnmarshalJSON(raw []byte) error {
	type plain Record
	var v plain
	if err := portableObject(raw, []string{"kind", "identity", "contentHash", "document"}, &v); err != nil {
		return err
	}
	*in = Record(v)
	return nil
}
func (in *Identity) UnmarshalJSON(raw []byte) error {
	type plain Identity
	var v plain
	if err := portableObject(raw, []string{"installationId", "projectId", "kind", "id", "version"}, &v); err != nil {
		return err
	}
	*in = Identity(v)
	return nil
}
