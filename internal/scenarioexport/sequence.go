package scenarioexport

import (
	"cmp"
	"fmt"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/designscenario"
)

type interval struct {
	start, end, index int
	fragment          designscenario.Fragment
}

func sequenceIntervals(doc designscenario.Document) ([]interval, []Diagnostic) {
	positions := make(map[string]int, len(doc.Messages))
	for i, m := range doc.Messages {
		positions[m.ID] = i
	}
	intervals := make([]interval, 0, len(doc.Fragments))
	issues := []Diagnostic{}
	for i, f := range doc.Fragments {
		start, hasStart := positions[f.FromMessageID]
		end, hasEnd := positions[f.ToMessageID]
		if !hasStart || !hasEnd || start > end || (f.Kind != "opt" && f.Kind != "loop") {
			issues = append(issues, diagnostic("fragment_invalid", "error", "Исправьте границы или тип блока", "fragment", f.ID, fmt.Sprintf("/fragments/%d", i)))
			continue
		}
		intervals = append(intervals, interval{start: start, end: end, index: i, fragment: f})
	}
	slices.SortFunc(intervals, func(a, b interval) int {
		return cmp.Or(cmp.Compare(a.start, b.start), cmp.Compare(b.end, a.end), cmp.Compare(a.index, b.index))
	})
	stack := []interval{}
	for _, current := range intervals {
		for len(stack) > 0 && stack[len(stack)-1].end < current.start {
			stack = stack[:len(stack)-1]
		}
		if len(stack) > 0 && current.end > stack[len(stack)-1].end {
			for _, bad := range []interval{stack[len(stack)-1], current} {
				issues = append(issues, diagnostic("fragment_overlap", "error", "Пересекающиеся блоки нужно разделить или вложить один в другой", "fragment", bad.fragment.ID, fmt.Sprintf("/fragments/%d", bad.index)))
			}
		}
		stack = append(stack, current)
	}
	return intervals, issues
}

func renderSequence(doc designscenario.Document, format Format, limit int64) ([]byte, error) {
	out := boundedBuffer{limit: limit}
	var writeErr error
	line := func(value string) {
		if writeErr == nil {
			_, writeErr = out.Write([]byte(value + "\n"))
		}
	}
	label := plantUMLText
	if format == Mermaid {
		label = mermaidText
		line("sequenceDiagram")
	} else {
		line("@startuml")
		if strings.TrimSpace(doc.Title) != "" {
			line("title " + label(doc.Title))
		}
	}
	aliases := make(map[string]string, len(doc.Participants))
	for i, p := range doc.Participants {
		if writeErr != nil {
			return nil, writeErr
		}
		alias := fmt.Sprintf("p%d", i)
		aliases[p.ID] = alias
		if format == Mermaid {
			line("participant " + alias + " as " + label(p.Name))
		} else {
			line("participant \"" + label(p.Name) + "\" as " + alias)
		}
		if p.Description != "" {
			line("note over " + alias + ": " + label(p.Description))
		}
	}
	intervals, _ := sequenceIntervals(doc)
	stack := []interval{}
	next := 0
	for i, m := range doc.Messages {
		if writeErr != nil {
			return nil, writeErr
		}
		for next < len(intervals) && intervals[next].start == i {
			current := intervals[next]
			line(current.fragment.Kind + " " + label(current.fragment.Label))
			stack = append(stack, current)
			next++
		}
		from, to := aliases[m.FromID], aliases[m.ToID]
		if m.Kind == "note" {
			pair := from
			if from != to {
				pair += "," + to
			}
			line("note over " + pair + ": " + label(m.Label))
		} else {
			arrow := " -> "
			if format == Mermaid {
				arrow = " ->> "
			}
			if m.Kind == "response" {
				arrow = " --> "
				if format == Mermaid {
					arrow = " -->> "
				}
			}
			if m.Kind == "event" {
				arrow = " ->> "
				if format == Mermaid {
					arrow = " -) "
				}
			}
			line(from + arrow + to + ": " + label(m.Label))
		}
		if m.Description != "" {
			line("note over " + from + ": " + label(m.Description))
		}
		for len(stack) > 0 && stack[len(stack)-1].end == i {
			line("end")
			stack = stack[:len(stack)-1]
		}
	}
	if format == PlantUML {
		line("@enduml")
	}
	if writeErr != nil {
		return nil, writeErr
	}
	return out.Bytes(), nil
}

func cleanNewlines(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}
