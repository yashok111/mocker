package designscenario

import (
	"fmt"

	"github.com/yashok111/mocker/internal/jsonx"
)

// OffsetX adds horizontal space before a participant. An omitted value is zero.
// The named type rejects JSON null, which a plain int would silently decode as zero.
type OffsetX int

func (offset *OffsetX) UnmarshalJSON(data []byte) error {
	if isJSONNull(data) {
		return fmt.Errorf("offsetX must be an integer from 0 to 2000")
	}
	var value int
	if err := jsonx.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("offsetX must be an integer from 0 to 2000: %w", err)
	}
	*offset = OffsetX(value)
	return nil
}
