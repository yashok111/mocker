package designscenario

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

// revisionDescription describes appearance edits without replacing an explicit
// UI or MCP summary. Other edits remain visible through a generic suffix.
func revisionDescription(before *Revision, document Document, formDrafts map[string]string, explicit string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	if before == nil {
		return "Создан сценарий"
	}
	colors := colorChanges{}
	participants := make(map[string]Participant, len(before.Document.Participants))
	for _, participant := range before.Document.Participants {
		participants[participant.ID] = participant
	}
	for _, participant := range document.Participants {
		if previous, exists := participants[participant.ID]; exists {
			colors.add(previous.Color, participant.Color, "объекта "+summaryEntityName(participant.Name, participant.ID))
		}
	}
	messages := make(map[string]Message, len(before.Document.Messages))
	for _, message := range before.Document.Messages {
		messages[message.ID] = message
	}
	for _, message := range document.Messages {
		if previous, exists := messages[message.ID]; exists {
			name := summaryEntityName(message.Label, message.ID)
			colors.add(previous.Color, message.Color, "карточки сообщения "+name)
			colors.add(previous.ArrowColor, message.ArrowColor, "стрелки сообщения "+name)
		}
	}
	if colors.count == 0 {
		return colorlessDescription(before.Document, document, messages)
	}
	summary := strings.Join(colors.changes, "; ")
	if colors.count > len(colors.changes) {
		remaining := colors.count - len(colors.changes)
		summary += fmt.Sprintf(" + ещё %d %s цвета", remaining, russianChangeWord(remaining))
	}
	if !sameDocumentExceptColors(before.Document, document) || !maps.Equal(before.FormDrafts, formDrafts) {
		summary += " + другие изменения"
	}
	return summary
}

// colorChanges counts every color edit but spells out only the first two.
type colorChanges struct {
	changes []string
	count   int
}

func (c *colorChanges) add(beforeColor, afterColor HexColor, target string) {
	if strings.EqualFold(string(beforeColor), string(afterColor)) {
		return
	}
	c.count++
	if len(c.changes) == 2 {
		return
	}
	if afterColor == "" {
		c.changes = append(c.changes, "Сброшен цвет "+target)
	} else {
		c.changes = append(c.changes, "Цвет "+target+": "+strings.ToUpper(string(afterColor)))
	}
}

// colorlessDescription names the event edits a color-free revision may carry.
func colorlessDescription(before, document Document, previousMessages map[string]Message) string {
	if !reflect.DeepEqual(before.EventModel, document.EventModel) {
		return "Изменена событийная модель"
	}
	for _, message := range document.Messages {
		if previous, exists := previousMessages[message.ID]; exists && !reflect.DeepEqual(previous.EventBindings, message.EventBindings) {
			return "Изменены привязки событий"
		}
	}
	return "Изменён сценарий"
}

// russianChangeWord declines «изменение» for a count (1, 2–4, 5+ and 11–14).
func russianChangeWord(n int) string {
	if n%100 >= 11 && n%100 <= 14 {
		return "изменений"
	}
	switch n % 10 {
	case 1:
		return "изменение"
	case 2, 3, 4:
		return "изменения"
	}
	return "изменений"
}

func sameDocumentExceptColors(before, after Document) bool {
	beforeJSON, err := jsonx.Marshal(withoutColors(before))
	if err != nil {
		return false
	}
	afterJSON, err := jsonx.Marshal(withoutColors(after))
	if err != nil {
		return false
	}
	// Contract snapshots are RawMessage: browsers may reorder their object keys
	// without changing their content. equalJSON also preserves exact numbers.
	equal, err := equalJSON(beforeJSON, afterJSON)
	return err == nil && equal
}

func summaryEntityName(name, id string) string {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		name = strings.Join(strings.Fields(id), " ")
	}
	runes := []rune(name)
	if len(runes) > 40 {
		name = string(runes[:39]) + "…"
	}
	return "«" + name + "»"
}

func withoutColors(document Document) Document {
	document.Participants = slices.Clone(document.Participants)
	for index := range document.Participants {
		document.Participants[index].Color = ""
	}
	document.Messages = slices.Clone(document.Messages)
	for index := range document.Messages {
		document.Messages[index].Color = ""
		document.Messages[index].ArrowColor = ""
	}
	return document
}
