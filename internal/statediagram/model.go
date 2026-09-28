// Package statediagram models authored lifecycles. Simulation never calls a
// mock or mutates runtime entity data; both UI and MCP use this evaluator.
package statediagram

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

const Extension = "x-mocker-state-diagrams"
const MaxJSON = 65536

var identifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)
var methods = map[string]bool{"get": true, "post": true, "put": true, "patch": true, "delete": true, "head": true, "options": true, "trace": true}

type Envelope struct {
	FormatVersion int       `json:"formatVersion"`
	Diagrams      []Diagram `json:"diagrams"`
}
type Diagram struct {
	ID             string       `json:"id"`
	Name           string       `json:"name"`
	InitialStateID string       `json:"initialStateId"`
	States         []State      `json:"states"`
	Transitions    []Transition `json:"transitions"`
}
type State struct {
	ID       string  `json:"id"`
	Name     string  `json:"name"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Terminal bool    `json:"terminal"`
}
type Binding struct {
	Method string `json:"method"`
	Path   string `json:"path"`
}
type Guard struct {
	Pointer    string `json:"pointer"`
	EqualsJSON string `json:"equalsJSON"`
}
type Transition struct {
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	From           string   `json:"from"`
	To             string   `json:"to"`
	Binding        *Binding `json:"binding,omitempty"`
	Guard          *Guard   `json:"guard,omitempty"`
	PatchJSON      string   `json:"patchJSON"`
	ResponseStatus int      `json:"responseStatus"`
}
type Diagnostic struct {
	Severity  string `json:"severity"`
	ElementID string `json:"elementId"`
	Message   string `json:"message"`
}

func ValidID(id string) bool { return identifier.MatchString(id) }

// Decode rejects unknown formats/fields rather than dropping data on the next
// typed edit. Absence of the extension alone means an empty collection.
func Decode(root map[string]any) (Envelope, error) {
	raw, ok := root[Extension]
	if !ok {
		return Envelope{FormatVersion: 1, Diagrams: []Diagram{}}, nil
	}
	b, err := jsonx.Marshal(raw)
	if err != nil {
		return Envelope{}, err
	}
	var env Envelope
	dec := jsonx.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err = dec.Decode(&env); err != nil {
		return env, fmt.Errorf("неверная структура диаграмм: %w", err)
	}
	if env.FormatVersion != 1 || env.Diagrams == nil || len(env.Diagrams) > 20 {
		return env, fmt.Errorf("нужен formatVersion 1 и массив до 20 диаграмм")
	}
	seen := map[string]bool{}
	for _, d := range env.Diagrams {
		if seen[d.ID] {
			return env, fmt.Errorf("повтор id диаграммы %q", d.ID)
		}
		seen[d.ID] = true
		if err = CheckStructure(d); err != nil {
			return env, err
		}
	}
	return env, nil
}

func CheckStructure(d Diagram) error {
	if !ValidID(d.ID) || strings.TrimSpace(d.Name) == "" || len(d.Name) > 240 {
		return fmt.Errorf("у диаграммы нужны id и название до 240 байт")
	}
	if d.States == nil || d.Transitions == nil || len(d.States) > 100 || len(d.Transitions) > 300 {
		return fmt.Errorf("нужны массивы до 100 состояний и 300 переходов")
	}
	if d.InitialStateID != "" && !ValidID(d.InitialStateID) {
		return fmt.Errorf("неверный id начального состояния")
	}
	seen := map[string]bool{}
	for _, state := range d.States {
		if seen[state.ID] {
			return fmt.Errorf("повтор id состояния %q", state.ID)
		}
		seen[state.ID] = true
		if err := checkState(state); err != nil {
			return err
		}
	}
	clear(seen)
	for _, transition := range d.Transitions {
		if seen[transition.ID] {
			return fmt.Errorf("повтор id перехода %q", transition.ID)
		}
		seen[transition.ID] = true
		if err := checkTransition(transition); err != nil {
			return err
		}
	}
	return nil
}
func checkState(s State) error {
	if !ValidID(s.ID) || strings.TrimSpace(s.Name) == "" || len(s.Name) > 240 {
		return fmt.Errorf("неверное состояние %q: id или название", s.ID)
	}
	for _, coordinate := range []float64{s.X, s.Y} {
		if math.IsNaN(coordinate) || math.IsInf(coordinate, 0) || math.Abs(coordinate) > 100000 {
			return fmt.Errorf("неверные координаты состояния %q", s.ID)
		}
	}
	return nil
}
func checkTransition(tr Transition) error {
	if !ValidID(tr.ID) || !ValidID(tr.From) || !ValidID(tr.To) || strings.TrimSpace(tr.Name) == "" || len(tr.Name) > 240 || tr.ResponseStatus < 100 || tr.ResponseStatus > 599 {
		return fmt.Errorf("неверный переход %q: id, название, концы или HTTP-статус", tr.ID)
	}
	if tr.Binding != nil && (!methods[tr.Binding.Method] || !strings.HasPrefix(tr.Binding.Path, "/") || len(tr.Binding.Path) > 2048) {
		return fmt.Errorf("неверная привязка перехода %q", tr.ID)
	}
	if _, err := Object(tr.PatchJSON); err != nil {
		return fmt.Errorf("изменения перехода %q: %w", tr.ID, err)
	}
	return checkGuard(tr.Guard)
}
func checkGuard(guard *Guard) error {
	if guard == nil {
		return nil
	}
	if !validPointer(guard.Pointer) {
		return fmt.Errorf("неверный JSON Pointer условия")
	}
	_, err := Value(guard.EqualsJSON)
	return err
}

func validPointer(p string) bool {
	if len(p) > 2048 || (p != "" && !strings.HasPrefix(p, "/")) {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] == '~' {
			i++
			if i >= len(p) || (p[i] != '0' && p[i] != '1') {
				return false
			}
		}
	}
	return true
}
func Value(raw string) (any, error) {
	if len(raw) > MaxJSON {
		return nil, fmt.Errorf("JSON превышает 64 КиБ")
	}
	dec := jsonx.NewDecoder(strings.NewReader(raw))
	dec.UseNumber()
	var value any
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("неверный JSON: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("ожидается одно JSON-значение")
	}
	return value, nil
}
func Object(raw string) (map[string]any, error) {
	value, err := Value(raw)
	if err != nil {
		return nil, err
	}
	object, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("ожидается JSON-объект")
	}
	return object, nil
}
