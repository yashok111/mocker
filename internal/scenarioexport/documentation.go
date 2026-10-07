package scenarioexport

import (
	"fmt"
	"strings"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

func (s *Service) documentationDiagnosticsWithCache(rev designscenario.Revision, cache *eventValidationCache) ([]Diagnostic, error) {
	if err := cache.ctx.Err(); err != nil {
		return nil, err
	}
	result := diagramDiagnostics(rev.Document)
	for _, contract := range rev.Document.Contracts {
		if err := cache.ctx.Err(); err != nil {
			return nil, err
		}
		if err := s.CheckResponse(result); err != nil {
			return nil, err
		}
		ds, err := s.contractDiagnostics(rev, contract)
		if err != nil {
			return nil, err
		}
		for _, d := range ds {
			if d.Code == "descriptive_messages_omitted" || d.Code == "api_forms_pending" {
				continue
			}
			if d.Severity == "error" {
				d.Severity = "warning"
			}
			result = append(result, d)
		}
	}
	result, err := s.appendEventDocumentationDiagnostics(result, rev, cache)
	if err != nil {
		return nil, err
	}
	if documentationFormsPending(rev.FormDrafts) {
		result = append(result, Diagnostic{Code: "api_forms_pending", Severity: "warning", Message: "Есть несохранённые поля API. Документ содержит сохранённые контракты; завершите редактирование API для их обновления."})
	}
	return result, s.CheckResponse(result)
}

// appendEventDocumentationDiagnostics demotes event-contract errors to
// warnings: documentation is still produced for an unexportable contract.
func (s *Service) appendEventDocumentationDiagnostics(result []Diagnostic, rev designscenario.Revision, cache *eventValidationCache) ([]Diagnostic, error) {
	if rev.Document.EventModel == nil {
		return result, nil
	}
	for _, contract := range rev.Document.EventModel.Contracts {
		ds, err := s.eventContractDiagnostics(cache, contract)
		if err != nil {
			return nil, err
		}
		for _, d := range ds {
			if d.Severity == "error" {
				d.Severity = "warning"
			}
			result = append(result, d)
		}
	}
	return result, nil
}

// documentationFormsPending is true unless the only draft is an empty "all" envelope.
func documentationFormsPending(drafts map[string]string) bool {
	pending := false
	for key, raw := range drafts {
		var forms map[string]jsonx.RawMessage
		if key != "all" || jsonx.Unmarshal([]byte(raw), &forms) != nil || forms == nil || len(forms) != 0 {
			pending = true
		}
	}
	return pending
}

// Every output path uses the bounded writer, including escaping and raw code blocks.
// Keep the error sticky so no later section can resume after exhausting the budget.
type documentationWriter struct {
	buffer boundedBuffer
	html   bool
	err    error
}

func (w *documentationWriter) raw(s string) {
	for w.err == nil && s != "" {
		n := min(len(s), 4096)
		_, w.err = w.buffer.Write([]byte(s[:n]))
		s = s[n:]
	}
}
func (w *documentationWriter) text(s string) {
	start := 0
	for i, r := range s {
		if w.err != nil {
			return
		}
		replacement := ""
		switch r {
		case '&':
			replacement = "&amp;"
		case '<':
			replacement = "&lt;"
		case '>':
			replacement = "&gt;"
		case '"':
			replacement = "&quot;"
		case '\'':
			replacement = "&#39;"
		default:
			if !w.html {
				if r == '\n' || r == '\r' {
					replacement = " "
				} else if r < 128 && strings.ContainsRune("\\`*_{}[]()#+-.!|~:=", r) {
					replacement = "\\" + string(r)
				}
			}
		}
		if replacement != "" {
			w.raw(s[start:i])
			w.raw(replacement)
			start = i + len(string(r))
		}
	}
	w.raw(s[start:])
}
func (w *documentationWriter) heading(level int, parts ...string) {
	if w.html {
		w.raw(fmt.Sprintf("<h%d>", level))
		for _, part := range parts {
			w.text(part)
		}
		w.raw(fmt.Sprintf("</h%d>\n", level))
	} else {
		w.raw(strings.Repeat("#", level) + " ")
		for _, part := range parts {
			w.text(part)
		}
		w.raw("\n\n")
	}
}
func (w *documentationWriter) paragraph(parts ...string) {
	if w.html {
		w.raw("<p>")
	}
	for _, part := range parts {
		w.text(part)
	}
	if w.html {
		w.raw("</p>")
	}
	w.raw("\n\n")
}
func (w *documentationWriter) code(language, source string) {
	if int64(len(source)) > w.buffer.limit-int64(w.buffer.Len()) {
		w.err = ErrTooLarge
		return
	}
	if w.html {
		w.raw("<pre><code>")
		w.text(source)
		w.raw("</code></pre>\n")
		return
	}
	maxRun, run := 0, 0
	for _, r := range source {
		if r == '`' {
			run++
			maxRun = max(maxRun, run)
		} else {
			run = 0
		}
	}
	fence := strings.Repeat("`", max(3, maxRun+1))
	w.raw(fence + language + "\n")
	w.raw(source)
	w.raw("\n" + fence + "\n\n")
}

const documentationCSS = `:root{color-scheme:light}*{box-sizing:border-box}body{margin:0 auto;padding:24px;max-width:1200px;font:15px/1.5 system-ui,sans-serif;color:#172033;background:#fff}h1,h2,h3,p,li{overflow-wrap:anywhere}h1{font-size:28px}h2{margin-top:32px;border-bottom:1px solid #cbd5e1}h3{margin-bottom:8px}p{white-space:pre-wrap}pre{white-space:pre-wrap;overflow-wrap:anywhere;font:12px/1.5 ui-monospace,monospace;background:#f1f5f9;padding:12px}.diagram-screen{overflow:auto}.diagram-screen svg{display:block;max-width:none}.diagram-print{display:none}.diagram-defs{position:absolute;width:0;height:0;overflow:hidden}@page{size:A4 landscape;margin:10mm}@media print{body{padding:0;max-width:none;font-size:10pt}.diagram-screen,.screen-hint{display:none}.diagram-print{display:block}.diagram-sheet{break-before:page;break-after:page;page-break-inside:avoid;width:277mm;height:185mm}.diagram-sheet p{height:8mm;margin:0;font-size:9pt}.diagram-sheet svg{display:block;width:277mm;height:175mm}h1,h2,h3{break-after:avoid}pre{background:none;padding:0}#participants{break-before:page}}`

// At 96 CSS pixels per inch, these tiles fit on an A4 landscape page with
// room for the page caption. Neighbouring tiles overlap by 24px on both axes.
const documentationTileWidth = 1040
const documentationTileHeight = 650
const documentationTileOverlap = 24
const documentationMaxTiles = 200

type documentationTile struct{ X, Y, Width, Height int }

func documentationTiles(width, height int) ([]documentationTile, error) {
	if width <= 0 || height <= 0 {
		return nil, ErrInvalidRequest
	}
	count := func(size, span int) int {
		if size <= span {
			return 1
		}
		return 1 + (size-span-1)/(span-documentationTileOverlap) + 1
	}
	cols, rows := count(width, documentationTileWidth), count(height, documentationTileHeight)
	if cols > documentationMaxTiles || rows > documentationMaxTiles || cols > documentationMaxTiles/rows {
		return nil, fmt.Errorf("%w: %w", ErrTooLarge, ErrTooManyPages)
	}
	tiles := make([]documentationTile, 0, cols*rows)
	for row := range rows {
		for col := range cols {
			x, y := col*(documentationTileWidth-documentationTileOverlap), row*(documentationTileHeight-documentationTileOverlap)
			tiles = append(tiles, documentationTile{X: x, Y: y, Width: documentationTileWidth, Height: documentationTileHeight})
		}
	}
	return tiles, nil
}

// renderDocumentation writes the sections in a fixed order. Each section
// returns the sticky writer error at the points the original single function
// stopped, so a document that exhausts its budget ends with no bytes.
func (s *Service) renderDocumentation(rev designscenario.Revision, format Format, diagnostics []Diagnostic) ([]byte, error) {
	w := documentationWriter{buffer: boundedBuffer{limit: s.maxBytes}, html: format == HTML}
	w.documentHeader(rev, diagnostics)
	if w.err != nil {
		return nil, w.err
	}
	if err := w.diagramSection(rev.Document, s.maxBytes); err != nil {
		return nil, err
	}
	names, err := w.participantsSection(rev.Document)
	if err != nil {
		return nil, err
	}
	if err := w.stepsSection(rev.Document, names); err != nil {
		return nil, err
	}
	if err := w.fragmentsSection(rev.Document); err != nil {
		return nil, err
	}
	if err := w.contractsSection(rev.Document); err != nil {
		return nil, err
	}
	if rev.Document.EventModel != nil {
		if err := w.eventModelSection(rev.Document.EventModel); err != nil {
			return nil, err
		}
	}
	if w.html {
		w.raw("</section></body></html>")
	}
	return w.buffer.Bytes(), w.err
}

func (w *documentationWriter) documentHeader(rev designscenario.Revision, diagnostics []Diagnostic) {
	if w.html {
		w.raw(`<!doctype html><html lang="ru"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; form-action 'none'"><title>`)
		w.text(rev.Document.Title)
		w.raw("</title><style>" + documentationCSS + "</style></head><body>")
	}
	w.heading(1, rev.Document.Title)
	w.paragraph(fmt.Sprintf("Сценарий %d · Ревизия %d · Версия %d", rev.ScenarioID, rev.ID, rev.Version))
	w.paragraph("Хеш исходника: ", rev.Hash)
	if len(diagnostics) > 0 {
		w.heading(2, "Замечания")
		for _, d := range diagnostics {
			w.paragraph(d.Code, " (", d.Severity, "): ", d.Message)
		}
	}
}

// diagramSection renders tiled SVG for HTML (printable on A4 sheets) and a
// Mermaid block for Markdown, each bounded by the budget still unspent.
func (w *documentationWriter) diagramSection(doc designscenario.Document, maxBytes int64) error {
	if !w.html {
		w.heading(2, "Диаграмма")
		diagram, err := renderSequence(doc, Mermaid, maxBytes-int64(w.buffer.Len()))
		if err != nil {
			return err
		}
		w.code("mermaid", string(diagram))
		return nil
	}
	diagram, err := renderDocumentationDiagram(doc, maxBytes-int64(w.buffer.Len()))
	if err != nil {
		return err
	}
	tiles, err := documentationTiles(diagram.Width, diagram.Height)
	if err != nil {
		return err
	}
	w.raw(`<section id="diagram">`)
	w.heading(2, "Диаграмма")
	w.raw(`<p class="screen-hint">Для PDF выберите печать и «Сохранить как PDF». Схема печатается на листах A4 в альбомной ориентации с перекрытием.</p>`)
	w.raw(`<svg xmlns="http://www.w3.org/2000/svg" class="diagram-defs" width="0" height="0" aria-hidden="true"><defs><g id="doc-sequence-content">`)
	w.raw(diagram.Markup)
	w.raw(`</g></defs></svg>`)
	w.raw(fmt.Sprintf(`<div class="diagram-screen"><svg xmlns="http://www.w3.org/2000/svg" role="img" aria-label="Диаграмма сценария" width="%d" height="%d" viewBox="0 0 %d %d"><use href="#doc-sequence-content"/></svg></div><div class="diagram-print">`, diagram.Width, diagram.Height, diagram.Width, diagram.Height))
	for i, tile := range tiles {
		w.raw(`<div class="diagram-sheet">`)
		w.paragraph(fmt.Sprintf("Диаграмма · Лист %d из %d · x=%d, y=%d", i+1, len(tiles), tile.X, tile.Y))
		w.raw(fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="%d %d %d %d"><use href="#doc-sequence-content"/></svg></div>`, tile.X, tile.Y, tile.Width, tile.Height))
	}
	w.raw("</div></section>")
	return nil
}

// participantsSection also returns the ID→name map the steps section uses.
func (w *documentationWriter) participantsSection(doc designscenario.Document) (map[string]string, error) {
	if w.html {
		w.raw(`<section id="participants">`)
	}
	w.heading(2, "Участники")
	names := map[string]string{}
	for _, p := range doc.Participants {
		if w.err != nil {
			return nil, w.err
		}
		names[p.ID] = p.Name
		w.heading(3, p.Name)
		w.paragraph("ID: ", p.ID, " · Тип: ", p.Kind)
		w.paragraph(p.Description)
	}
	return names, nil
}

func (w *documentationWriter) stepsSection(doc designscenario.Document, names map[string]string) error {
	if w.html {
		w.raw(`</section><section id="steps">`)
	}
	w.heading(2, "Шаги")
	for i, m := range doc.Messages {
		if w.err != nil {
			return w.err
		}
		w.heading(3, fmt.Sprintf("%d. ", i+1), m.Label)
		w.paragraph(names[m.FromID], " → ", names[m.ToID], " · Тип: ", m.Kind, " · ID: ", m.ID)
		if m.ReplyToID != "" {
			w.paragraph("Ответ на: ", m.ReplyToID)
		}
		w.paragraph(m.Description)
		if m.Operation != nil {
			w.stepOperation(doc.Contracts, *m.Operation)
		}
		for _, binding := range m.EventBindings {
			w.paragraph("Kafka контракт: ", binding.ContractID, " · Операция: ", binding.OperationID)
		}
		if m.Execution != nil && len(m.Execution.Bindings) > 0 {
			w.stepBindings(m.Execution.Bindings)
		}
	}
	return nil
}

// stepOperation names the bound operation and, when the first contract with
// that ID still holds it, its method, path, summary and description.
func (w *documentationWriter) stepOperation(contracts []designscenario.Contract, binding designscenario.OperationBinding) {
	w.paragraph("Контракт: ", binding.ContractID, " · Операция: ", binding.OperationKey)
	for _, c := range contracts {
		if c.ID != binding.ContractID {
			continue
		}
		op, ok := findSavedOperation(c.Document, binding.OperationKey)
		if !ok {
			continue
		}
		w.paragraph(op.Method, " ", op.Path)
		for _, key := range []string{"summary", "description"} {
			if value, ok := op.Operation[key].(string); ok {
				w.paragraph(value)
			}
		}
		break
	}
}

func (w *documentationWriter) stepBindings(bindings []designscenario.DataBinding) {
	w.heading(4, "Передача данных")
	for _, binding := range bindings {
		target := binding.Target.Name
		if binding.Target.Kind == "body" {
			target = binding.Target.Pointer
		}
		w.paragraph("Источник: ", binding.SourceMessageID, " · JSON Pointer: ", binding.SourcePointer,
			" → ", binding.Target.Kind, " ", target)
		if len(binding.Transforms) > 0 {
			kinds := make([]string, len(binding.Transforms))
			for i, transform := range binding.Transforms {
				kinds[i] = transform.Kind
			}
			w.paragraph("Преобразования: ", strings.Join(kinds, " → "))
		}
	}
}

func (w *documentationWriter) fragmentsSection(doc designscenario.Document) error {
	if len(doc.Fragments) == 0 {
		return nil
	}
	w.heading(2, "Блоки и ветки")
	for _, f := range doc.Fragments {
		if w.err != nil {
			return w.err
		}
		w.heading(3, f.Kind, " ", f.Label)
		w.paragraph("ID: ", f.ID, " · Шаги: ", f.FromMessageID, " → ", f.ToMessageID)
		if f.ParentFragmentID != "" {
			w.paragraph("Родительский блок: ", f.ParentFragmentID, " · Ветка: ", f.ParentBranchID)
		}
		for _, b := range f.Branches {
			w.paragraph("Ветка ", b.ID, ": ", b.Label, " · Шаги: ", b.FromMessageID, " → ", b.ToMessageID)
		}
	}
	return nil
}

func (w *documentationWriter) contractsSection(doc designscenario.Document) error {
	if w.html {
		w.raw(`</section><section id="contracts">`)
	}
	w.heading(2, "Приложение: HTTP API контракты")
	if len(doc.Contracts) == 0 {
		w.paragraph("HTTP API контракты не добавлены.")
	}
	for _, c := range doc.Contracts {
		if w.err != nil {
			return w.err
		}
		w.heading(3, c.Name)
		w.paragraph("ID: ", c.ID)
		if c.Source != nil {
			w.paragraph(fmt.Sprintf("API %d · Ревизия %d · Версия %d", c.Source.DesignID, c.Source.RevisionID, c.Source.Version))
		}
		w.code("json", string(c.Document))
	}
	return nil
}

// documentationEventIndex is the event model keyed by ID for the appendix.
type documentationEventIndex struct {
	channels map[string]designscenario.EventChannel
	messages map[string]designscenario.EventMessage
	schemas  map[string]designscenario.EventSchema
}

func (w *documentationWriter) eventModelSection(model *designscenario.EventModel) error {
	w.heading(2, "Kafka · Событийные контракты")
	if len(model.Contracts) == 0 {
		w.paragraph("Событийные контракты не добавлены.")
	}
	index := documentationEventIndex{
		channels: map[string]designscenario.EventChannel{},
		messages: map[string]designscenario.EventMessage{},
		schemas:  map[string]designscenario.EventSchema{},
	}
	for _, item := range model.Channels {
		index.channels[item.ID] = item
	}
	for _, item := range model.Messages {
		index.messages[item.ID] = item
	}
	for _, item := range model.Schemas {
		index.schemas[item.ID] = item
	}
	for _, contract := range model.Contracts {
		if w.err != nil {
			return w.err
		}
		w.heading(3, contract.Name)
		w.paragraph("ID: ", contract.ID, " · Приложение: ", contract.ParticipantID, " · Версия: ", contract.Version)
		w.paragraph(contract.Description)
		if len(contract.Operations) == 0 {
			w.paragraph("Нет операций Kafka: контракт пока не готов к экспорту.")
		}
		for _, op := range contract.Operations {
			w.eventOperation(index, op)
		}
	}
	return nil
}

func (w *documentationWriter) eventOperation(index documentationEventIndex, op designscenario.EventOperation) {
	ch, ok := index.channels[op.ChannelID]
	if !ok {
		w.paragraph("Операция ", op.ID, ": канал не найден")
		return
	}
	m, ok := index.messages[op.MessageID]
	if !ok {
		w.paragraph("Операция ", op.ID, ": тип события не найден")
		return
	}
	w.paragraph(op.Action, " · ", op.Name, " · topic: ", ch.Address, " · событие: ", m.Name)
	w.paragraph(op.Description)
	w.paragraph(m.Description)
	for _, id := range []string{m.PayloadSchemaID, m.HeadersSchemaID, m.KeySchemaID} {
		if id == "" {
			continue
		}
		if schema, found := index.schemas[id]; found {
			w.paragraph("Схема: ", schema.Name, " (", id, ")")
			w.code("json", schema.SchemaJSON)
		} else {
			w.paragraph("Схема ", id, " не найдена")
		}
	}
	for _, example := range m.Examples {
		w.paragraph("Пример: ", example.Name)
		w.code("json", example.PayloadJSON)
		if example.HeadersJSON != "" {
			w.paragraph("Headers:")
			w.code("json", example.HeadersJSON)
		}
	}
}
