package scenarioexport

import (
	"fmt"
	"strings"
)

// Mermaid's decimal entities keep punctuation inside label tokens.
func mermaidText(value string) string {
	var out strings.Builder
	for _, r := range cleanNewlines(value) {
		switch {
		case r == '\n':
			out.WriteString("<br/>")
		case r < ' ' || strings.ContainsRune(`"\@!<>#&;{}[]:`, r):
			fmt.Fprintf(&out, "#%d;", r)
		default:
			out.WriteRune(r)
		}
	}
	if out.Len() == 0 {
		return " "
	}
	if strings.EqualFold(strings.TrimSpace(value), "end") {
		return "#101;nd"
	}
	return out.String()
}
