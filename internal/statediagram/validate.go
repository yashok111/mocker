package statediagram

func Validate(d Diagram, root map[string]any) []Diagnostic {
	out := []Diagnostic{}
	add := func(severity, id, message string) {
		out = append(out, Diagnostic{Severity: severity, ElementID: id, Message: message})
	}
	if err := CheckStructure(d); err != nil {
		add("error", d.ID, err.Error())
		return out
	}
	states := map[string]State{}
	for _, s := range d.States {
		states[s.ID] = s
	}
	if _, ok := states[d.InitialStateID]; !ok {
		add("error", d.ID, "Выберите существующее начальное состояние")
	}
	paths, _ := root["paths"].(map[string]any)
	for _, tr := range d.Transitions {
		source, ok := states[tr.From]
		if !ok {
			add("error", tr.ID, "Исходное состояние не найдено")
		}
		if _, ok := states[tr.To]; !ok {
			add("error", tr.ID, "Целевое состояние не найдено")
		}
		if source.Terminal {
			add("error", tr.ID, "У конечного состояния не может быть исходящих переходов")
		}
		if tr.Binding != nil {
			item, _ := paths[tr.Binding.Path].(map[string]any)
			if operation, ok := item[tr.Binding.Method].(map[string]any); !ok || operation == nil {
				add("error", tr.ID, "Связанная операция API не найдена: "+tr.Binding.Method+" "+tr.Binding.Path)
			}
		} else {
			add("warning", tr.ID, "Переход не привязан к операции API")
		}
	}
	reachable := map[string]bool{d.InitialStateID: true}
	for changed := true; changed; {
		changed = false
		for _, tr := range d.Transitions {
			if reachable[tr.From] && !reachable[tr.To] {
				reachable[tr.To] = true
				changed = true
			}
		}
	}
	for _, s := range d.States {
		if !reachable[s.ID] {
			add("warning", s.ID, "Состояние недостижимо из начального")
		}
	}
	return out
}
func HasErrors(diagnostics []Diagnostic) bool {
	for _, d := range diagnostics {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}
