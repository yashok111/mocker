package scenarioexport

import (
	_ "embed"
	"strings"
	"unicode"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

//go:embed postman_bindings.js
var postmanBindingsJS string

func postmanBindingRuntime(bindings []designscenario.DataBinding) string {
	var needsCase bool
	for _, binding := range bindings {
		for _, transform := range binding.Transforms {
			needsCase = needsCase || transform.Kind == "lower" || transform.Kind == "upper"
		}
	}
	caseData := "[]"
	if needsCase {
		// Unicode simple case maps match strings.ToLower/ToUpper. JavaScript's
		// full mappings expand characters such as ß and differ from the runner.
		ranges := make([][4]int32, 0, len(unicode.CaseRanges))
		for _, span := range unicode.CaseRanges {
			ranges = append(ranges, [4]int32{
				int32(span.Lo), int32(span.Hi), //nolint:gosec // G115: Unicode case ranges end at U+10FFFF, far inside int32
				span.Delta[unicode.UpperCase], span.Delta[unicode.LowerCase],
			})
		}
		encoded, _ := jsonx.Marshal(ranges) // Fixed integer arrays cannot fail JSON encoding.
		caseData = string(encoded)
	}
	return strings.Replace(postmanBindingsJS, "/*GO_CASE_RANGES*/[]", caseData, 1)
}
