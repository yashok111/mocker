package scenarioexport

import (
	"cmp"
	"encoding/xml"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/designscenario"
)

type documentationDiagram struct {
	Markup string
	Width  int
	Height int
}

const diagramLineHeight = 20

type diagramText struct {
	value       string
	x, y, width int
}

type diagramFrame struct {
	depth, x, y, width, height int
	labels                     []diagramText
	separators                 []int
}

type diagramEvent struct {
	frame                  *diagramFrame
	label                  string
	end, branch, separator bool
}

type diagramMessage struct {
	message            designscenario.Message
	label, description diagramText
	from, to, arrowY   int
}

// renderDocumentationDiagram receives the same validated document as the other
// sequence exporters. Layout uses document order, never author-provided offsets,
// colors, IDs or style. The conservative text width leaves room for fallback fonts.
func renderDocumentationDiagram(doc designscenario.Document, limit int64) (documentationDiagram, error) {
	// Bound layout allocations before creating maps, events or escaped strings.
	budget := jsonBudget{remaining: limit}
	charge := func(values ...string) error {
		if err := budget.take(64); err != nil {
			return err
		}
		for _, value := range values {
			if err := budget.take(int64(len(value))); err != nil {
				return err
			}
		}
		return nil
	}
	for _, p := range doc.Participants {
		if err := charge(p.Name, p.Description); err != nil {
			return documentationDiagram{}, err
		}
	}
	for _, m := range doc.Messages {
		if err := charge(m.Label, m.Description); err != nil {
			return documentationDiagram{}, err
		}
	}
	for _, f := range doc.Fragments {
		if err := charge(f.Label); err != nil {
			return documentationDiagram{}, err
		}
		for _, b := range f.Branches {
			if err := charge(b.Label); err != nil {
				return documentationDiagram{}, err
			}
		}
	}
	intervals, issues := sequenceIntervals(doc)
	if len(issues) != 0 {
		return documentationDiagram{}, fmt.Errorf("invalid diagram fragments: %s", issues[0].Message)
	}
	frames, before, after := documentationDiagramEvents(doc, intervals)
	depth := 0
	for _, frame := range frames {
		depth = max(depth, frame.depth+1)
	}
	margin := 40 + depth*18
	width := max(640, 2*margin+len(doc.Participants)*260+120)
	centers := make(map[string]int, len(doc.Participants))
	participantHeight := 44
	for i, p := range doc.Participants {
		centers[p.ID] = margin + 110 + i*260
		h := 24 + diagramTextHeight(p.Name, 196)
		if p.Description != "" {
			h += 12 + diagramTextHeight(p.Description, 196)
		}
		participantHeight = max(participantHeight, h)
	}
	y := participantHeight + 52
	apply := func(event diagramEvent) {
		frame := event.frame
		if event.end {
			y += 12
			frame.height = y - frame.y
			y += 12
			return
		}
		if !event.branch {
			frame.x = 16 + frame.depth*18
			frame.y, frame.width = y, width-2*frame.x
		}
		if event.separator {
			frame.separators = append(frame.separators, y)
			y += 10
		}
		label := diagramText{event.label, frame.x + 12, y + 18, frame.width - 24}
		frame.labels = append(frame.labels, label)
		y += diagramTextHeight(label.value, label.width) + 18
	}
	messages := make([]diagramMessage, 0, len(doc.Messages))
	for i, m := range doc.Messages {
		for _, event := range before[i] {
			apply(event)
		}
		from, hasFrom := centers[m.FromID]
		to, hasTo := centers[m.ToID]
		if !hasFrom || !hasTo {
			return documentationDiagram{}, fmt.Errorf("invalid diagram message participant")
		}
		x, textWidth := min(from, to)+12, max(from, to)-min(from, to)-24
		if from == to {
			x, textWidth = from+18, 200
		}
		if m.Kind == "note" {
			x, textWidth = min(from, to)-100, max(from, to)-min(from, to)+200
		}
		row := diagramMessage{message: m, from: from, to: to, label: diagramText{m.Label, x, y + 20, textWidth}}
		y += diagramTextHeight(m.Label, textWidth) + 16
		row.arrowY = y
		if m.Kind != "note" {
			y += 12
		}
		if from == to && m.Kind != "note" {
			y += 28
		}
		if m.Description != "" {
			row.description = diagramText{m.Description, x, y + 20, textWidth}
			y += diagramTextHeight(m.Description, textWidth) + 24
		}
		y += 20
		messages = append(messages, row)
		for _, event := range after[i] {
			apply(event)
		}
	}
	height := y + 24
	out := diagramWriter{out: boundedBuffer{limit: limit}}
	out.write(`<defs><marker id="doc-diagram-arrow" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10 Z" fill="#334155"/></marker><marker id="doc-diagram-event" viewBox="0 0 10 10" refX="9" refY="5" markerWidth="8" markerHeight="8" orient="auto-start-reverse"><path d="M 0 0 L 10 5 L 0 10" fill="none" stroke="#334155" stroke-width="1.5"/></marker></defs>`)
	out.write(`<rect width="%d" height="%d" fill="white"/>`, width, height)
	for i, p := range doc.Participants {
		center := margin + 110 + i*260
		out.write(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#94a3b8" stroke-dasharray="5 5"/>`, center, participantHeight+20, center, height-16)
		out.write(`<rect x="%d" y="20" width="220" height="%d" rx="5" fill="#f1f5f9" stroke="#475569"/>`, center-110, participantHeight)
		out.text(diagramText{p.Name, center - 98, 42, 196})
		if p.Description != "" {
			out.text(diagramText{p.Description, center - 98, 54 + diagramTextHeight(p.Name, 196), 196})
		}
	}
	for _, frame := range frames {
		out.write(`<rect class="doc-diagram-frame" x="%d" y="%d" width="%d" height="%d" fill="none" stroke="#64748b" stroke-width="1.5"/>`, frame.x, frame.y, frame.width, frame.height)
		for _, separator := range frame.separators {
			out.write(`<line x1="%d" y1="%d" x2="%d" y2="%d" stroke="#64748b" stroke-dasharray="6 4"/>`, frame.x, separator, frame.x+frame.width, separator)
		}
		for _, label := range frame.labels {
			out.box(label, "#e2e8f0")
			out.text(label)
		}
	}
	for _, row := range messages {
		fill := "white"
		if row.message.Kind == "note" {
			fill = "#fef3c7"
		}
		out.box(row.label, fill)
		out.text(row.label)
		if row.message.Kind != "note" {
			marker, dash, kind := "doc-diagram-arrow", "", "request"
			if row.message.Kind == "response" {
				kind, dash = "response", ` stroke-dasharray="6 4"`
			}
			if row.message.Kind == "event" {
				kind, marker = "event", "doc-diagram-event"
			}
			path := fmt.Sprintf("M %d %d L %d %d", row.from, row.arrowY, row.to, row.arrowY)
			if row.from == row.to {
				path = fmt.Sprintf("M %d %d H %d V %d H %d", row.from, row.arrowY, row.from+90, row.arrowY+28, row.to)
			}
			out.write(`<path data-kind="%s" d="%s" fill="none" stroke="#334155" stroke-width="1.5"%s marker-end="url(#%s)"/>`, kind, path, dash, marker)
		}
		if row.description.value != "" {
			out.box(row.description, "#fef3c7")
			out.text(row.description)
		}
		if out.err != nil {
			return documentationDiagram{}, out.err
		}
	}
	if out.err != nil {
		return documentationDiagram{}, out.err
	}
	return documentationDiagram{Markup: out.out.String(), Width: width, Height: height}, nil
}

// Events share the legacy interval ordering and v2 explicit parent/branch tree
// used by renderSequence. A separate event reserves space for every alt condition.
func documentationDiagramEvents(doc designscenario.Document, intervals []interval) ([]*diagramFrame, map[int][]diagramEvent, map[int][]diagramEvent) {
	frames := []*diagramFrame{}
	before, after := map[int][]diagramEvent{}, map[int][]diagramEvent{}
	if doc.FormatVersion != 2 {
		stack := []interval{}
		active := []*diagramFrame{}
		next := 0
		for i := range doc.Messages {
			for next < len(intervals) && intervals[next].start == i {
				current := intervals[next]
				frame := &diagramFrame{depth: len(stack)}
				frames = append(frames, frame)
				before[i] = append(before[i], diagramEvent{frame: frame, label: current.fragment.Kind + " " + current.fragment.Label})
				stack, active = append(stack, current), append(active, frame)
				next++
			}
			for len(stack) > 0 && stack[len(stack)-1].end == i {
				after[i] = append(after[i], diagramEvent{frame: active[len(active)-1], end: true})
				stack, active = stack[:len(stack)-1], active[:len(active)-1]
			}
		}
		return frames, before, after
	}
	positions := make(map[string]int, len(doc.Messages))
	for i, m := range doc.Messages {
		positions[m.ID] = i
	}
	type scope struct{ parent, branch string }
	children := map[scope][]designscenario.Fragment{}
	for _, f := range doc.Fragments {
		key := scope{f.ParentFragmentID, f.ParentBranchID}
		children[key] = append(children[key], f)
	}
	var visit func(scope, int)
	visit = func(key scope, depth int) {
		group := children[key]
		slices.SortStableFunc(group, func(a, b designscenario.Fragment) int {
			return cmp.Compare(positions[a.FromMessageID], positions[b.FromMessageID])
		})
		for _, f := range group {
			frame := &diagramFrame{depth: depth}
			frames = append(frames, frame)
			start := positions[f.FromMessageID]
			before[start] = append(before[start], diagramEvent{frame: frame, label: f.Kind + " " + f.Label})
			if f.Kind == "alt" {
				for i, b := range f.Branches {
					label := "[" + b.Label + "]"
					if i > 0 {
						label = "else " + label
					}
					at := positions[b.FromMessageID]
					before[at] = append(before[at], diagramEvent{frame: frame, label: label, branch: true, separator: i > 0})
					visit(scope{f.ID, b.ID}, depth+1)
				}
			} else {
				visit(scope{parent: f.ID}, depth+1)
			}
			end := positions[f.ToMessageID]
			after[end] = append(after[end], diagramEvent{frame: frame, end: true})
		}
	}
	visit(scope{}, 0)
	return frames, before, after
}

// Each callback receives a slice of the original text: wrapping does not discard
// spaces or allocate a rune array proportional to an untrusted label's length.
func diagramLines(value string, width int, visit func(string)) {
	maxWidth := max(28, width)
	for {
		end, lineWidth, lastSpace := 0, 0, 0
		newline := 0
		for end < len(value) {
			r, n := utf8.DecodeRuneInString(value[end:])
			if r == '\n' || r == '\r' {
				newline = n
				if r == '\r' && end+n < len(value) && value[end+n] == '\n' {
					newline++
				}
				break
			}
			advance := 14
			if r > 0x2fff {
				advance = 28
			}
			if lineWidth+advance > maxWidth && end > 0 {
				if lastSpace > 0 {
					end = lastSpace
				}
				break
			}
			end += n
			lineWidth += advance
			if r == ' ' {
				lastSpace = end
			}
		}
		visit(value[:end])
		value = value[end+newline:]
		if value == "" {
			if newline > 0 {
				visit("")
			}
			return
		}
	}
}

func diagramTextHeight(value string, width int) int {
	lines := 0
	diagramLines(value, width, func(string) { lines++ })
	return lines * diagramLineHeight
}

type diagramWriter struct {
	out boundedBuffer
	err error
}

func (w *diagramWriter) write(format string, args ...any) {
	if w.err == nil {
		_, w.err = fmt.Fprintf(&w.out, format, args...)
	}
}

func (w *diagramWriter) box(text diagramText, fill string) {
	w.write(`<rect x="%d" y="%d" width="%d" height="%d" rx="3" fill="%s"/>`, text.x-4, text.y-16, text.width+8, diagramTextHeight(text.value, text.width)+8, fill)
}

func (w *diagramWriter) text(text diagramText) {
	y := text.y
	diagramLines(text.value, text.width, func(line string) {
		if w.err != nil {
			return
		}
		w.write(`<text x="%d" y="%d" fill="#0f172a" font-family="monospace" font-size="14" xml:space="preserve">`, text.x, y)
		if w.err == nil {
			w.err = xml.EscapeText(&w.out, []byte(strings.ToValidUTF8(line, "�")))
		}
		w.write(`</text>`)
		y += diagramLineHeight
	})
}
