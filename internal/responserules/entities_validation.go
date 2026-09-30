package responserules

import "fmt"

type nodeValueRef struct {
	Ref     ValueRef
	Pointer string
}

func nodeValueRefs(n Node) []nodeValueRef {
	refs := []nodeValueRef{}
	if n.ResultCondition != nil {
		refs = append(refs, resultConditionRefs(*n.ResultCondition, "/resultCondition", 1)...)
	}
	if n.Response != nil && n.Response.BodyFrom != nil {
		refs = append(refs, nodeValueRef{*n.Response.BodyFrom, "/response/bodyFrom"})
	}
	if n.Entity != nil {
		if n.Entity.Key != nil {
			refs = append(refs, nodeValueRef{*n.Entity.Key, "/entity/key"})
		}
		if n.Entity.Data != nil {
			refs = append(refs, nodeValueRef{*n.Entity.Data, "/entity/data"})
		}
		if n.Entity.Scope != nil {
			for i, r := range *n.Entity.Scope {
				refs = append(refs, nodeValueRef{r, fmt.Sprintf("/entity/scope/%d", i)})
			}
		}
	}
	return refs
}

func resultConditionRefs(c ResultCondition, pointer string, level int) []nodeValueRef {
	if level > MaxResultConditionDepth {
		return nil
	}
	if c.All != nil || c.Any != nil {
		name, children := "all", c.All
		if c.Any != nil {
			name, children = "any", c.Any
		}
		refs := []nodeValueRef{}
		for i, child := range children {
			refs = append(refs, resultConditionRefs(child, fmt.Sprintf("%s/%s/%d", pointer, name, i), level+1)...)
		}
		return refs
	}
	refs := []nodeValueRef{{c.Source, pointer + "/source"}}
	if c.ValueFrom != nil {
		refs = append(refs, nodeValueRef{*c.ValueFrom, pointer + "/valueFrom"})
	}
	return refs
}
func nodePorts(n Node) []string {
	if n.Type == "entity_read" && n.Entity.Operation == "get" || n.Type == "entity_update" {
		return []string{"found", "missing"}
	}
	if n.Entity != nil {
		return []string{"next"}
	}
	return ports(n.Type)
}

// A bounded reachability check with one node/edge removed proves dominance
// without enumerating the exponentially many paths of a branching DAG.
func reachableWithout(r Rule, start, target, blocked, blockedPort string) bool {
	queue := []string{start}
	seen := map[string]bool{}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] || id == blocked && blockedPort == "" {
			continue
		}
		seen[id] = true
		if id == target {
			return true
		}
		for _, e := range r.Edges {
			if e.From == id && !(id == blocked && e.Port == blockedPort) {
				queue = append(queue, e.To)
			}
		}
	}
	return false
}
func (v *validator) entityReferences() {
	start := ""
	for _, n := range v.rule.Nodes {
		if n.Type == "start" {
			start = n.ID
			break
		}
	}
	if start == "" {
		return
	}
	for i, n := range v.rule.Nodes {
		for _, ref := range nodeValueRefs(n) {
			if ref.Ref.Source != "result" {
				continue
			}
			producer, ok := v.nodeOrder[ref.Ref.NodeID]
			if !ok || v.rule.Nodes[producer].Entity == nil || ref.Ref.NodeID == n.ID || reachableWithout(v.rule, start, n.ID, ref.Ref.NodeID, "") {
				v.node(i, "invalid_result_reference", "Результат должен быть создан на каждом пути до этого узла.", ref.Pointer)
				continue
			}
			p := v.rule.Nodes[producer]
			if p.Type == "entity_update" || p.Type == "entity_read" && p.Entity.Operation == "get" {
				if reachableWithout(v.rule, start, n.ID, p.ID, "found") {
					v.node(i, "missing_result_reference", "Используйте результат только после выхода «Найдено».", ref.Pointer)
				}
			}
		}
	}
}
func responseNoBody(status int) bool { return status == 204 || status == 205 || status == 304 }
func entityAdmission(rule Rule) (*EntityAdmission, string) {
	nodes := map[string]Node{}
	for _, n := range rule.Nodes {
		nodes[n.ID] = n
	}
	var admission *EntityAdmission
	for _, n := range rule.Nodes {
		if n.Type != "entity_create" && n.Type != "entity_update" {
			continue
		}
		queue := []string{n.ID}
		seen := map[string]bool{}
		for len(queue) > 0 {
			id := queue[0]
			queue = queue[1:]
			if seen[id] {
				continue
			}
			seen[id] = true
			current, ok := nodes[id]
			if !ok {
				continue
			}
			if current.Type == "fallback" {
				return nil, n.ID
			}
			if current.Response != nil {
				candidate := EntityAdmission{MediaType: current.Response.MediaType, NoBody: responseNoBody(current.Response.Status)}
				if admission != nil && *admission != candidate {
					return nil, n.ID
				}
				admission = &candidate
				continue
			}
			for _, e := range rule.Edges {
				if e.From == id {
					queue = append(queue, e.To)
				}
			}
		}
	}
	return admission, ""
}
func (v *validator) entityTerminals() {
	_, bad := entityAdmission(v.rule)
	if bad != "" {
		v.node(v.nodeOrder[bad], "unsafe_entity_terminal", "После записи все пути должны завершаться ответом с одним типом содержимого и правилом тела.", "/entity")
	}
}
