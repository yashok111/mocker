package designscenario

import (
	"bytes"
	"fmt"

	"github.com/yashok111/mocker/internal/jsonx"
)

// A present null eventModel is not an absent eventModel. Preserve that
// distinction at the wire boundary, including for legacy document versions.
func (d *Document) UnmarshalJSON(data []byte) error {
	type wire Document
	var fields map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("design scenario document must be an object")
	}
	if raw, present := fields["eventModel"]; present && isJSONNull(raw) {
		return fmt.Errorf("eventModel must be an object")
	}
	if raw, present := fields["eventModel"]; present {
		var model EventModel
		decoder := jsonx.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&model); err != nil {
			return fmt.Errorf("decode eventModel: %w", err)
		}
	}
	var out wire
	decoder := jsonx.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return err
	}
	if out.FormatVersion < 3 {
		if _, present := fields["eventModel"]; present {
			return fmt.Errorf("eventModel requires formatVersion 3")
		}
		var messages []map[string]jsonx.RawMessage
		if raw, present := fields["messages"]; present && !isJSONNull(raw) {
			if err := jsonx.Unmarshal(raw, &messages); err != nil {
				return err
			}
			for i, message := range messages {
				if _, present := message["eventBindings"]; present {
					return fmt.Errorf("messages[%d].eventBindings require formatVersion 3", i)
				}
			}
		}
	}
	*d = Document(out)
	return nil
}

func (m *Message) UnmarshalJSON(data []byte) error {
	type wire Message
	var fields map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("design scenario message must be an object")
	}
	if raw, present := fields["eventBindings"]; present && isJSONNull(raw) {
		return fmt.Errorf("eventBindings must be an array")
	}
	var out wire
	decoder := jsonx.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return err
	}
	*m = Message(out)
	return nil
}

func (k *EventOperationKafka) UnmarshalJSON(data []byte) error {
	type wire EventOperationKafka
	var fields map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("Kafka operation binding must be an object")
	}
	for _, name := range []string{"groupId", "clientId"} {
		if raw, present := fields[name]; present && (isJSONNull(raw) || bytes.Equal(bytes.TrimSpace(raw), []byte(`""`))) {
			return fmt.Errorf("%s must be a non-empty string when present", name)
		}
	}
	var out wire
	decoder := jsonx.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return err
	}
	*k = EventOperationKafka(out)
	return nil
}

func (k *EventChannelKafka) UnmarshalJSON(data []byte) error {
	type wire EventChannelKafka
	var fields map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(data, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("Kafka channel binding must be an object")
	}
	for _, name := range []string{"partitions", "replicas"} {
		if raw, present := fields[name]; present && isJSONNull(raw) {
			return fmt.Errorf("%s must be an integer when present", name)
		}
	}
	var out wire
	decoder := jsonx.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return err
	}
	*k = EventChannelKafka(out)
	return nil
}
