package scenarioexport

import (
	"fmt"
	"strings"
)

// Unicode escapes prevent author labels from becoming PlantUML commands or markup.
func plantUMLText(value string) string {
	var out strings.Builder
	for _, r := range cleanNewlines(value) {
		switch {
		case r == '\n':
			out.WriteString(`\n`)
		case r < ' ' || strings.ContainsRune(`"\@!<>#&{}[]`, r):
			fmt.Fprintf(&out, "<U+%04X>", r)
		default:
			out.WriteRune(r)
		}
	}
	if out.Len() == 0 {
		return " "
	}
	return out.String()
}
