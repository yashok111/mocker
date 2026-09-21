package designscenario

import (
	"fmt"
	"regexp"

	"github.com/yashok111/mocker/internal/jsonx"
)

// HexColorPattern describes opaque RGB colors in the canvas wire format.
const HexColorPattern = `^#[0-9a-fA-F]{6}$`

var hexColorPattern = regexp.MustCompile(HexColorPattern)

// HexColor is an optional card fill or arrow color. Its zero value is omitted
// from JSON to use the default appearance; an explicitly supplied value must
// be a complete #RRGGBB string, including when decoding REST request bodies.
type HexColor string

func (color *HexColor) UnmarshalJSON(data []byte) error {
	var value string
	if err := jsonx.Unmarshal(data, &value); err != nil {
		return fmt.Errorf("color must be a #RRGGBB string: %w", err)
	}
	if !hexColorPattern.MatchString(value) {
		return fmt.Errorf("color must be a #RRGGBB string")
	}
	*color = HexColor(value)
	return nil
}
