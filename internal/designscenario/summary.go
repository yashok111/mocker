package designscenario

import (
	"fmt"
	"maps"
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
	changes := []string{}
	count := 0
	addColorChange := func(beforeColor, afterColor HexColor, target string) {
		if strings.EqualFold(string(beforeColor), string(afterColor)) {
			return
		}
		count++
		if len(changes) == 2 {
			return
		}
		if afterColor == "" {
			changes = append(changes, "Сброшен цвет "+target)
		} else {
			changes = append(changes, "Цвет "+target+": "+strings.ToUpper(string(afterColor)))
		}
	}
	participants := make(map[string]Participant, len(before.Document.Participants))
	for _, participant := range before.Document.Participants {
		participants[participant.ID] = participant
	}
	for _, participant := range document.Participants {
		if previous, exists := participants[participant.ID]; exists {
			addColorChange(previous.Color, participant.Color, "объекта "+summaryEntityName(participant.Name, participant.ID))
		}
	}
	messages := make(map[string]Message, len(before.Document.Messages))
	for _, message := range before.Document.Messages {
		messages[message.ID] = message
	}
	for _, message := range document.Messages {
		if previous, exists := messages[message.ID]; exists {
			name := summaryEntityName(message.Label, message.ID)
			addColorChange(previous.Color, message.Color, "карточки сообщения "+name)
			addColorChange(previous.ArrowColor, message.ArrowColor, "стрелки сообщения "+name)
		}
	}
	if count == 0 {
		return "Изменён сценарий"
	}
	summary := strings.Join(changes, "; ")
	if count > len(changes) {
		remaining := count - len(changes)
		word := "изменений"
		if remaining%100 < 11 || remaining%100 > 14 {
			switch remaining % 10 {
			case 1:
				word = "изменение"
			case 2, 3, 4:
				word = "изменения"
			}
		}
		summary += fmt.Sprintf(" + ещё %d %s цвета", remaining, word)
	}
	if !sameDocumentExceptColors(before.Document, document) || !maps.Equal(before.FormDrafts, formDrafts) {
		summary += " + другие изменения"
	}
	return summary
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
