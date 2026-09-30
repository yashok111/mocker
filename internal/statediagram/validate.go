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
	if d.Entity != nil {
		values := map[string]string{}
		for _, state := range d.States {
			value := effectiveValue(state)
			if previous, ok := values[value]; ok {
				add("error", state.ID, "Значение состояния уже используется состоянием "+previous)
			}
			values[value] = state.ID
		}
	}
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
		if d.Entity != nil {
			patch, _ := Object(tr.PatchJSON)
			if value, present := patch[d.Entity.StateField]; present {
				if target, ok := states[tr.To]; ok && value != effectiveValue(target) {
					add("error", tr.ID, "Изменения поля состояния должны совпадать со значением целевого состояния")
				}
			}
		}
		if tr.Binding != nil {
			if _, err := boundOperation(root, *tr.Binding); err != nil {
				add("error", tr.ID, err.Error())
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
