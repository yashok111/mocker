package responserules

import (
	"fmt"
	"slices"

	"github.com/yashok111/mocker/internal/jsonx"
)

// ApplyCommands applies an atomic batch to a private copy, retaining incomplete graphs.
func ApplyCommands(rule Rule, commands []Command) (Rule, error) {
	if len(commands) > MaxCommands {
		return Rule{}, invalid("/commands", "слишком много команд")
	}
	if err := CheckStructure(rule); err != nil {
		return Rule{}, err
	}
	data, err := jsonx.Marshal(rule)
	if err != nil {
		return Rule{}, err
	}
	var result Rule
	if err := jsonx.Unmarshal(data, &result); err != nil {
		return Rule{}, err
	}
	for i, command := range commands {
		// Round-tripping checks typed callers too and owns all inserted payloads.
		raw, err := jsonx.Marshal(command)
		if err != nil {
			return Rule{}, at(fmt.Sprintf("/commands/%d", i), err)
		}
		var c Command
		if err := jsonx.Unmarshal(raw, &c); err != nil {
			return Rule{}, at(fmt.Sprintf("/commands/%d", i), err)
		}
		if err := apply(&result, c); err != nil {
			return Rule{}, at(fmt.Sprintf("/commands/%d", i), err)
		}
		if err := CheckStructure(result); err != nil {
			return Rule{}, at(fmt.Sprintf("/commands/%d", i), err)
		}
	}
	return result, nil
}
func nodeIndex(r *Rule, id string) int {
	return slices.IndexFunc(r.Nodes, func(n Node) bool { return n.ID == id })
}
func edgeIndex(r *Rule, id string) int {
	return slices.IndexFunc(r.Edges, func(e Edge) bool { return e.ID == id })
}
func apply(r *Rule, c Command) error {
	switch c.Type {
	case "set_rule":
		r.Name = *c.Name
		r.Binding = c.Binding
	case "add_node":
		if nodeIndex(r, c.Node.ID) >= 0 {
			return invalid("/node/id", "узел уже существует")
		}
		r.Nodes = append(r.Nodes, *c.Node)
	case "update_node":
		i := nodeIndex(r, c.Node.ID)
		if i < 0 {
			return invalid("/node/id", "узел не найден")
		}
		if r.Nodes[i].Type != c.Node.Type {
			return invalid("/node/type", "тип узла нельзя изменить")
		}
		r.Nodes[i] = *c.Node
	case "remove_node":
		i := nodeIndex(r, c.NodeID)
		if i < 0 {
			return invalid("/nodeId", "узел не найден")
		}
		r.Nodes = slices.Delete(r.Nodes, i, i+1)
		r.Edges = slices.DeleteFunc(r.Edges, func(e Edge) bool { return e.From == c.NodeID || e.To == c.NodeID })
	case "add_edge":
		if edgeIndex(r, c.Edge.ID) >= 0 {
			return invalid("/edge/id", "ребро уже существует")
		}
		r.Edges = append(r.Edges, *c.Edge)
	case "update_edge":
		i := edgeIndex(r, c.Edge.ID)
		if i < 0 {
			return invalid("/edge/id", "ребро не найдено")
		}
		r.Edges[i] = *c.Edge
	case "remove_edge":
		i := edgeIndex(r, c.EdgeID)
		if i < 0 {
			return invalid("/edgeId", "ребро не найдено")
		}
		r.Edges = slices.Delete(r.Edges, i, i+1)
	case "move_nodes":
		seen := map[string]bool{}
		for i, p := range c.Positions {
			prefix := fmt.Sprintf("/positions/%d", i)
			if seen[p.NodeID] {
				return invalid(prefix+"/nodeId", "повторяющийся ID")
			}
			seen[p.NodeID] = true
			j := nodeIndex(r, p.NodeID)
			if j < 0 {
				return invalid(prefix+"/nodeId", "узел не найден")
			}
			if !coordinate(p.X) || !coordinate(p.Y) {
				return invalid(prefix, "недопустимые координаты")
			}
			r.Nodes[j].X = p.X
			r.Nodes[j].Y = p.Y
		}
	}
	return nil
}
