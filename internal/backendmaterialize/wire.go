package backendmaterialize

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
)

func closed(raw []byte, required []string, out any) error {
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if fields == nil {
		return invalid("Expected a JSON object")
	}
	for _, key := range required {
		value, ok := fields[key]
		if !ok || string(value) == "null" {
			return invalid("Required nonnull field: " + key)
		}
	}
	return json.Unmarshal(raw, out, json.RejectUnknownMembers(true))
}
func (in *PreviewInput) UnmarshalJSON(raw []byte) error {
	type plain PreviewInput
	var value plain
	if err := closed(raw, []string{"profileVersion", "target", "targetHash", "sourceScope", "targets", "translations", "partialSimulation", "excludedIds", "reason"}, &value); err != nil {
		return err
	}
	*in = PreviewInput(value)
	return nil
}
func (in *ApplyInput) UnmarshalJSON(raw []byte) error {
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	var candidate, key string
	if err := json.Unmarshal(fields["candidateHash"], &candidate); err != nil {
		return err
	}
	if err := json.Unmarshal(fields["idempotencyKey"], &key); err != nil {
		return err
	}
	delete(fields, "candidateHash")
	delete(fields, "idempotencyKey")
	rest, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	var preview PreviewInput
	if err := json.Unmarshal(rest, &preview); err != nil {
		return err
	}
	*in = ApplyInput{PreviewInput: preview, CandidateHash: candidate, IdempotencyKey: key}
	return nil
}
func (in *Target) UnmarshalJSON(raw []byte) error {
	type plain Target
	var value plain
	if err := closed(raw, []string{"key", "kind", "name", "expectedVersion", "commands"}, &value); err != nil {
		return err
	}
	*in = Target(value)
	return nil
}
func (in *Command) UnmarshalJSON(raw []byte) error {
	type plain Command
	var value plain
	if err := closed(raw, []string{"type"}, &value); err != nil {
		return err
	}
	*in = Command(value)
	return nil
}
func (in *Translation) UnmarshalJSON(raw []byte) error {
	type plain Translation
	var value plain
	if err := closed(raw, []string{"sourceId", "targetKey", "selector", "reason"}, &value); err != nil {
		return err
	}
	*in = Translation(value)
	return nil
}

func (in *DiagramViewPin) UnmarshalJSON(raw []byte) error {
	type plain DiagramViewPin
	var value plain
	if err := closed(raw, []string{"id", "version"}, &value); err != nil {
		return err
	}
	*in = DiagramViewPin(value)
	return nil
}

func (in ApplyInput) MarshalJSON() ([]byte, error) {
	type input PreviewInput
	return json.Marshal(struct {
		input
		CandidateHash  string `json:"candidateHash"`
		IdempotencyKey string `json:"idempotencyKey"`
	}{input(in.PreviewInput), in.CandidateHash, in.IdempotencyKey})
}
