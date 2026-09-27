package designscenario

import (
	"bytes"
	"fmt"

	"github.com/yashok111/mocker/internal/jsonx"
)

func (f *Fragment) UnmarshalJSON(data []byte) error {
	type wire Fragment
	var out wire
	decoder := jsonx.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return err
	}
	var fields map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, field := range []string{"parentFragmentId", "parentBranchId"} {
		if raw, exists := fields[field]; exists {
			var value string
			if err := jsonx.Unmarshal(raw, &value); err != nil || value == "" {
				return fmt.Errorf("fragment %s must be a non-empty string when present", field)
			}
		}
	}
	if raw, exists := fields["branches"]; exists && isJSONNull(raw) {
		return fmt.Errorf("fragment branches must be an array")
	}
	*f = Fragment(out)
	return nil
}

func (b *FragmentBranch) UnmarshalJSON(data []byte) error {
	type wire FragmentBranch
	var out wire
	var fields map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"id", "label", "fromMessageId", "toMessageId"} {
		raw, exists := fields[key]
		if !exists || isJSONNull(raw) {
			return fmt.Errorf("fragment branch %s is required", key)
		}
	}
	decoder := jsonx.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&out); err != nil {
		return err
	}
	*b = FragmentBranch(out)
	return nil
}
