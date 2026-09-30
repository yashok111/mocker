package statediagram

import (
	"bytes"
	"fmt"
	"slices"

	"github.com/yashok111/mocker/internal/jsonx"
)

type Command struct {
	Entity         *EntityBinding `json:"entity,omitempty"`
	ClearEntity    bool           `json:"clearEntity,omitzero"`
	Kind           string         `json:"kind"`
	ID             string         `json:"id,omitempty"`
	State          *State         `json:"state,omitempty"`
	Transition     *Transition    `json:"transition,omitempty"`
	Name           *string        `json:"name,omitempty"`
	InitialStateID *string        `json:"initialStateId,omitempty"`
}

func ApplyCommands(d Diagram, commands []Command) (Diagram, error) {
	if len(commands) == 0 || len(commands) > 100 {
		return d, fmt.Errorf("нужно от 1 до 100 команд")
	}
	d.States = slices.Clone(d.States)
	d.Transitions = slices.Clone(d.Transitions)
	for _, c := range commands {
		if c.Kind != "settings" && (c.Entity != nil || c.ClearEntity) {
			return d, fmt.Errorf("привязку сущности меняет только команда settings")
		}
		switch c.Kind {
		case "upsert_state":
			if c.State == nil {
				return d, fmt.Errorf("нужно state")
			}
			i := slices.IndexFunc(d.States, func(s State) bool { return s.ID == c.State.ID })
			if i < 0 {
				d.States = append(d.States, *c.State)
			} else {
				d.States[i] = *c.State
			}
		case "remove_state":
			if !ValidID(c.ID) {
				return d, fmt.Errorf("нужен id состояния")
			}
			d.States = slices.DeleteFunc(d.States, func(s State) bool { return s.ID == c.ID })
			d.Transitions = slices.DeleteFunc(d.Transitions, func(tr Transition) bool { return tr.From == c.ID || tr.To == c.ID })
			if d.InitialStateID == c.ID {
				d.InitialStateID = ""
			}
		case "upsert_transition":
			if c.Transition == nil {
				return d, fmt.Errorf("нужно transition")
			}
			i := slices.IndexFunc(d.Transitions, func(tr Transition) bool { return tr.ID == c.Transition.ID })
			if i < 0 {
				d.Transitions = append(d.Transitions, *c.Transition)
			} else {
				d.Transitions[i] = *c.Transition
			}
		case "remove_transition":
			if !ValidID(c.ID) {
				return d, fmt.Errorf("нужен id перехода")
			}
			d.Transitions = slices.DeleteFunc(d.Transitions, func(tr Transition) bool { return tr.ID == c.ID })
		case "settings":
			if c.Entity != nil && c.ClearEntity {
				return d, fmt.Errorf("entity и clearEntity нельзя задавать вместе")
			}
			if c.ClearEntity {
				d.Entity = nil
			}
			if c.Entity != nil {
				d.Entity = new(*c.Entity)
			}
			if c.Name != nil {
				d.Name = *c.Name
			}
			if c.InitialStateID != nil {
				d.InitialStateID = *c.InitialStateID
			}
		default:
			return d, fmt.Errorf("неизвестная команда %q", c.Kind)
		}
	}
	return d, CheckStructure(d)
}

// UnmarshalJSON applies strict decoding even when a caller uses Unmarshal.
func (c *Command) UnmarshalJSON(data []byte) error {
	type plain Command
	var decoded plain
	if err := decodeStrict(data, &decoded); err != nil {
		return err
	}
	var fields map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(data, &fields); err != nil {
		return err
	}
	for _, key := range []string{"entity", "clearEntity"} {
		if raw, ok := fields[key]; ok {
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return fmt.Errorf("%s не может быть null", key)
			}
			if decoded.Kind != "settings" {
				return fmt.Errorf("привязку сущности меняет только команда settings")
			}
		}
	}
	if decoded.Entity != nil && decoded.ClearEntity {
		return fmt.Errorf("entity и clearEntity нельзя задавать вместе")
	}
	*c = Command(decoded)
	return nil
}
