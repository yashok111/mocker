package scenarioexport

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
)

func TestDocumentationDiagramMessagesAndSafeText(t *testing.T) {
	t.Parallel()
	hostile := `<script>alert("x")</script> & <image href="https://bad/"/>`
	doc := designscenario.Document{
		FormatVersion: 1,
		Participants:  []designscenario.Participant{{ID: "a", Name: "Клиент " + hostile}, {ID: "b", Name: "Сервис", Description: "Описание участника"}},
		Messages: []designscenario.Message{
			{ID: "1", FromID: "a", ToID: "b", Kind: "request", Label: "Запрос " + hostile, Description: "Описание запроса"},
			{ID: "2", FromID: "b", ToID: "a", Kind: "response", Label: "Ответ"},
			{ID: "3", FromID: "a", ToID: "b", Kind: "event", Label: "Событие"},
			{ID: "4", FromID: "b", ToID: "b", Kind: "request", Label: "Самовызов"},
			{ID: "5", FromID: "a", ToID: "b", Kind: "note", Label: "Заметка"},
		},
	}
	got, err := renderDocumentationDiagram(doc, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	text, elements := readDocumentationSVG(t, got)
	for _, want := range []string{"Клиент " + hostile, "Сервис", "Описание участника", "Запрос " + hostile, "Описание запроса", "Ответ", "Событие", "Самовызов", "Заметка"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing text %q in %q", want, text)
		}
	}
	var paths []map[string]string
	for _, el := range elements {
		if el.name == "path" && el.attrs["data-kind"] != "" {
			paths = append(paths, el.attrs)
		}
	}
	if len(paths) != 4 {
		t.Fatalf("got %d message arrows, want 4", len(paths))
	}
	if paths[0]["marker-end"] != "url(#doc-diagram-arrow)" || paths[1]["stroke-dasharray"] == "" || paths[2]["marker-end"] != "url(#doc-diagram-event)" {
		t.Fatalf("incorrect arrow semantics: %#v", paths)
	}
	for i, forward := range []bool{true, false, true} {
		var x1, y1, x2, y2 int
		if _, err := fmt.Sscanf(paths[i]["d"], "M %d %d L %d %d", &x1, &y1, &x2, &y2); err != nil {
			t.Fatal(err)
		}
		if (x1 < x2) != forward || y1 != y2 {
			t.Errorf("incorrect direction of message %d: %s", i, paths[i]["d"])
		}
	}
	if !strings.Contains(paths[3]["d"], " V ") {
		t.Fatalf("self message lacks return: %s", paths[3]["d"])
	}
}

func TestDocumentationDiagramNestedFrames(t *testing.T) {
	t.Parallel()
	for _, version := range []int{1, 2, 3} {
		t.Run(strconv.Itoa(version), func(t *testing.T) {
			doc := branchRevision(t).Document
			doc.FormatVersion = version
			if version == 1 {
				doc.Fragments = []designscenario.Fragment{
					{ID: "inner", Kind: "opt", Label: "INNER", FromMessageID: "a", ToMessageID: "b"},
					{ID: "outer", Kind: "loop", Label: "OUTER", FromMessageID: "a", ToMessageID: "c"},
				}
			}
			got, err := renderDocumentationDiagram(doc, 1_000_000)
			if err != nil {
				t.Fatal(err)
			}
			text, elements := readDocumentationSVG(t, got)
			want := []string{"loop", "opt", "OUTER", "INNER"}
			if version >= 2 {
				want = []string{"alt", "Decision", "ok", "loop", "retry", "else"}
			}
			for _, label := range want {
				if !strings.Contains(text, label) {
					t.Errorf("missing %q in %q", label, text)
				}
			}
			var frames []map[string]string
			for _, el := range elements {
				if el.attrs["class"] == "doc-diagram-frame" {
					frames = append(frames, el.attrs)
				}
			}
			if len(frames) != 2 {
				t.Fatalf("frames = %d, want 2", len(frames))
			}
			outer, inner := frames[0], frames[1]
			if number(t, outer, "x") >= number(t, inner, "x") || number(t, outer, "y") >= number(t, inner, "y") || number(t, outer, "y")+number(t, outer, "height") <= number(t, inner, "y")+number(t, inner, "height") {
				t.Fatalf("frames not nested: %#v", frames)
			}
		})
	}
}

func TestDocumentationDiagramWrapsWithoutDroppingText(t *testing.T) {
	t.Parallel()
	label := strings.Repeat("ОченьДлиннаяСтрока界😀 ", 90)
	doc := designscenario.Document{FormatVersion: 1, Participants: []designscenario.Participant{{ID: "p", Name: label}}, Messages: []designscenario.Message{{ID: "m", FromID: "p", ToID: "p", Kind: "note", Label: label}}}
	got, err := renderDocumentationDiagram(doc, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	text, elements := readDocumentationSVG(t, got)
	if strings.Count(text, label) != 2 {
		t.Fatal("wrapped text lost characters")
	}
	lines := 0
	for _, el := range elements {
		if el.name == "text" {
			lines++
			if number(t, el.attrs, "x") < 0 || number(t, el.attrs, "y") >= got.Height {
				t.Fatalf("text outside bounds: %#v", el)
			}
		}
	}
	if lines < 30 || got.Height < 1000 {
		t.Fatalf("long text not wrapped: %d lines, height %d", lines, got.Height)
	}
}

func TestDocumentationDiagramLimit(t *testing.T) {
	t.Parallel()
	doc := branchRevision(t).Document
	full, err := renderDocumentationDiagram(doc, 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int64{0, 1, int64(len(full.Markup) - 1)} {
		if _, err := renderDocumentationDiagram(doc, limit); !errors.Is(err, ErrTooLarge) {
			t.Errorf("limit %d: %v", limit, err)
		}
	}
	if _, err := renderDocumentationDiagram(doc, int64(len(full.Markup))); err != nil {
		t.Fatal(err)
	}
	doc.Messages[0].Label = strings.Repeat("<", 1_000_000)
	if _, err := renderDocumentationDiagram(doc, 1024); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("huge text: %v", err)
	}
}

type documentationSVGElement struct {
	name  string
	attrs map[string]string
}

func readDocumentationSVG(t *testing.T, got documentationDiagram) (string, []documentationSVGElement) {
	t.Helper()
	if got.Width <= 0 || got.Height <= 0 {
		t.Fatalf("invalid bounds: %dx%d", got.Width, got.Height)
	}
	decoder := xml.NewDecoder(strings.NewReader("<svg>" + got.Markup + "</svg>"))
	var text strings.Builder
	var elements []documentationSVGElement
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		switch value := token.(type) {
		case xml.StartElement:
			if value.Name.Local == "script" || value.Name.Local == "foreignObject" || value.Name.Local == "image" || value.Name.Local == "style" {
				t.Fatalf("unsafe element %s", value.Name.Local)
			}
			el := documentationSVGElement{name: value.Name.Local, attrs: map[string]string{}}
			for _, attr := range value.Attr {
				if strings.HasPrefix(attr.Name.Local, "on") || attr.Name.Local == "style" || attr.Name.Local == "href" {
					t.Fatalf("unsafe attribute %s", attr.Name.Local)
				}
				if attr.Name.Local == "id" && !strings.HasPrefix(attr.Value, "doc-diagram-") {
					t.Fatalf("unscoped ID %q", attr.Value)
				}
				el.attrs[attr.Name.Local] = attr.Value
			}
			elements = append(elements, el)
		case xml.CharData:
			text.Write(value)
		}
	}
	return text.String(), elements
}

func number(t *testing.T, attrs map[string]string, key string) int {
	t.Helper()
	value, err := strconv.Atoi(attrs[key])
	if err != nil {
		t.Fatalf("%s=%q: %v", key, attrs[key], err)
	}
	return value
}
