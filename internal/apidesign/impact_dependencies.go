package apidesign

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/resourcemap"
	"github.com/yashok111/mocker/internal/schemamodel"
	"github.com/yashok111/mocker/internal/statediagram"
)

type impactSource struct {
	pointer, direction string
	sites              []ImpactReferenceSite
}
type impactOperation struct {
	id, key, keyPointer, pointer, sourcePointer, method, path string
	value                                                     map[string]any
	sources                                                   []impactSource
}
type impactNode struct {
	entity                    ImpactEntity
	pointer, operationPointer string
}
type impactContractMethod struct {
	operation *impactOperation
	entity    ImpactEntity
}

type impactSnapshot struct {
	root           map[string]any
	side           string
	refs           []schemamodel.DependencyReference
	operations     []*impactOperation
	contracts      []impactContractMethod
	nodes          []impactNode
	keyCounts      map[string]int
	addresses      map[string]*impactOperation
	uncertainPaths map[string]bool
}

func (c *impactCollector) snapshot(root map[string]any, side string) (*impactSnapshot, error) {
	s := &impactSnapshot{root: root, side: side, operations: []*impactOperation{}, contracts: []impactContractMethod{}, nodes: []impactNode{}, keyCounts: map[string]int{}, addresses: map[string]*impactOperation{}, uncertainPaths: map[string]bool{}}
	index, err := schemamodel.DependencyReferencesWithinBudget(c.ctx, root, impactMaxReferences, max(0, impactMaxVisits-c.visits))
	if err != nil {
		return nil, err
	}
	s.refs = index.References
	c.visits += index.Visits
	if c.visits > impactMaxVisits {
		c.truncate("traversal")
	}
	if index.Truncated {
		c.truncate(index.TruncatedReason)
	}
	for _, d := range index.Diagnostics {
		c.diagnostic(ImpactDiagnostic{Code: d.Code, Severity: "warning", Side: side, Pointer: d.Pointer, Message: d.Message}, true)
	}
	for _, group := range slices.Sorted(maps.Keys(impactObject(root["components"]))) {
		for _, name := range slices.Sorted(maps.Keys(impactObject(impactObject(root["components"])[group]))) {
			if !c.visit() {
				break
			}
			kind := "contract_node"
			if group == "schemas" {
				kind = "schema"
			}
			pointer := "/components/" + escape(group) + "/" + escape(name)
			s.nodes = append(s.nodes, impactNode{entity: impactEntity(kind, pointer, name, side, ImpactLocator{Pointer: pointer}), pointer: pointer})
		}
	}
	for _, path := range slices.Sorted(maps.Keys(impactObject(root["webhooks"]))) {
		if !c.visit() {
			break
		}
		pointer := "/webhooks/" + escape(path)
		s.nodes = append(s.nodes, impactNode{entity: impactEntity("contract_node", pointer, "Webhook "+path, side, ImpactLocator{Pointer: pointer}), pointer: pointer})
	}
	paths := impactObject(root["paths"])
	for _, path := range slices.Sorted(maps.Keys(paths)) {
		if !c.visit() {
			break
		}
		if strings.HasPrefix(path, "x-") {
			continue
		}
		at := "/paths/" + escape(path)
		chain, diagnostics := schemamodel.PathItems(root, paths[path], at)
		for _, d := range diagnostics {
			if d.Code != "path_item_method_conflict" {
				s.uncertainPaths[path] = true
			}
			c.diagnostic(ImpactDiagnostic{Code: d.Code, Severity: "warning", Side: side, Pointer: d.Pointer, Message: d.Message}, d.Code != "path_item_method_conflict")
		}
		for _, method := range schemamodel.PathItemOperations(chain) {
			if !c.visit() {
				break
			}
			value := impactObject(method.Value)
			if value == nil {
				c.diagnostic(ImpactDiagnostic{Code: "invalid_operation", Severity: "warning", Side: side, Pointer: method.Pointer, Message: "Операция должна быть объектом"}, true)
				continue
			}
			op := &impactOperation{pointer: at + "/" + method.Method, sourcePointer: method.Pointer, method: strings.ToUpper(method.Method), path: path, value: value, sources: []impactSource{}}
			op.key = impactText(value[OperationKey])
			keyPointer := op.pointer + "/" + OperationKey
			if op.pointer != method.Pointer {
				op.key = impactText(impactObject(impactObject(paths[path])[PathOperationKeys])[method.Method])
				keyPointer = at + "/" + PathOperationKeys + "/" + method.Method
			}
			op.keyPointer = keyPointer
			if op.key != "" {
				s.keyCounts[op.key]++
			}
			op.sources = append(op.sources, impactSource{pointer: keyPointer, direction: "unknown"})
			for _, key := range slices.Sorted(maps.Keys(value)) {
				if key == "parameters" || key == "security" || key == "servers" {
					continue
				}
				direction := "unknown"
				if key == "requestBody" {
					direction = "request"
				}
				if key == "responses" {
					direction = "response"
				}
				op.sources = append(op.sources, impactSource{pointer: method.Pointer + "/" + escape(key), direction: direction, sites: impactInheritanceSites(chain, method.Pointer)})
			}
			// Empty operations still have an address. This exact root source must not
			// widen child changes into overridden inherited fields.
			op.sources = append(op.sources, impactSource{pointer: method.Pointer, direction: "root", sites: impactInheritanceSites(chain, method.Pointer)})
			c.operationSources(s, op, chain)
			s.operations = append(s.operations, op)
			s.addresses[op.method+" "+op.path] = op
		}
	}
	for _, op := range s.operations {
		if op.key != "" && s.keyCounts[op.key] > 1 {
			c.diagnostic(ImpactDiagnostic{Code: "duplicate_operation_key", Severity: "warning", Side: side, Pointer: op.pointer, Message: "Ключ операции повторяется; сопоставление по ключу отключено"}, true)
		}
	}
	c.contractPathSources(s, index.PathItems)
	c.resources(s)
	c.states(s)
	return s, nil
}
func impactEntity(kind, key, label, side string, locator ImpactLocator) ImpactEntity {
	e := ImpactEntity{ID: impactID(kind, key), Kind: kind, Label: label}
	if side == "before" {
		e.Before = &locator
	} else {
		e.After = &locator
	}
	return e
}
func (c *impactCollector) matchOperations(a, b *impactSnapshot) {
	for _, s := range []*impactSnapshot{a, b} {
		for _, op := range s.operations {
			if !c.visit() {
				return
			}
			identity := "address:" + op.method + " " + op.path
			other := a
			if s == a {
				other = b
			}
			counterpart := impactAddress(other, op.method, op.path)
			fallback := op.key == "" || (counterpart != nil && counterpart.key == "")
			if !fallback && a.keyCounts[op.key] <= 1 && b.keyCounts[op.key] <= 1 {
				identity = "key:" + op.key
			}
			op.id = impactID("operation", identity)
		}
	}
}

func (c *impactCollector) operationSources(s *impactSnapshot, op *impactOperation, chain []schemamodel.PathItemNode) {
	// An operation parameter wins over a same-name path parameter. More local
	// Path Items win over referenced Path Items, matching the editor resolver.
	parameters := map[string]bool{}
	addParameters := func(value any, pointer string) {
		list, _ := value.([]any)
		for i, item := range list {
			if !c.visit() {
				return
			}
			at := pointer + "/" + strconv.Itoa(i)
			resolved := impactResolveObject(s.root, impactObject(item))
			name, in := impactText(resolved["name"]), impactText(resolved["in"])
			identity := name + "\x00" + in
			if name == "" || in == "" {
				identity = at
			}
			if parameters[identity] {
				continue
			}
			parameters[identity] = true
			op.sources = append(op.sources, impactSource{pointer: at, direction: "request", sites: impactInheritanceSites(chain, at)})
		}
	}
	addParameters(op.value["parameters"], op.sourcePointer+"/parameters")
	for _, node := range chain {
		addParameters(node.Value["parameters"], node.Pointer+"/parameters")
	}
	for _, field := range []string{"security", "servers"} {
		pointer := "/" + field
		value, ok := op.value[field]
		if ok {
			pointer = op.sourcePointer + "/" + field
		} else {
			if field == "servers" {
				for _, node := range chain {
					if local, found := node.Value[field]; found {
						value, ok, pointer = local, true, node.Pointer+"/"+field
						break
					}
				}
			}
			if !ok {
				value, ok = s.root[field]
			}
		}
		if !ok {
			continue
		}
		direction := "unknown"
		if field == "security" {
			direction = "request"
		}
		op.sources = append(op.sources, impactSource{pointer: pointer, direction: direction, sites: impactInheritanceSites(chain, pointer)})
		if field == "security" {
			requirements, _ := value.([]any)
			for i, requirement := range requirements {
				if !c.visit() {
					return
				}
				for _, name := range slices.Sorted(maps.Keys(impactObject(requirement))) {
					if !c.visit() {
						return
					}
					target := "/components/securitySchemes/" + escape(name)
					site := pointer + "/" + strconv.Itoa(i) + "/" + escape(name)
					op.sources = append(op.sources, impactSource{pointer: target, direction: "request", sites: append([]ImpactReferenceSite{{Pointer: site, TargetPointer: target, Kind: "security"}}, impactInheritanceSites(chain, pointer)...)})
					if _, found := impactLookup(s.root, target); !found {
						c.diagnostic(ImpactDiagnostic{Code: "missing_security_scheme", Severity: "warning", Side: s.side, Pointer: site, Message: "Схема безопасности не найдена"}, true)
					}
				}
			}
		}
	}
	// A changed Path Item link affects only methods/common fields actually
	// inherited through it. Fully overridden local methods do not depend on it.
	referenceSources := map[string]bool{}
	for _, source := range slices.Clone(op.sources) {
		for i, site := range source.sites {
			if !c.visit() {
				return
			}
			if site.Kind != "ref" {
				continue
			}
			direction := source.direction
			if direction == "root" {
				direction = "unknown"
			}
			key := site.Pointer + "\x00" + direction
			if referenceSources[key] {
				continue
			}
			referenceSources[key] = true
			op.sources = append(op.sources, impactSource{pointer: site.Pointer, direction: direction, sites: slices.Clone(source.sites[i+1:])})
		}
	}

}
func impactResolveObject(root map[string]any, m map[string]any) map[string]any {
	seen := map[string]bool{}
	for range impactMaxDepth {
		ref := impactText(m["$ref"])
		if ref == "" {
			return m
		}
		if !strings.HasPrefix(ref, "#/") || seen[ref] {
			return nil
		}
		seen[ref] = true
		value, found := impactLookup(root, strings.TrimPrefix(ref, "#"))
		if !found {
			return nil
		}
		m = impactObject(value)
	}
	return nil
}
func impactLookup(root map[string]any, pointer string) (any, bool) {
	var value any = root
	for _, part := range impactTokens(pointer) {
		switch node := value.(type) {
		case map[string]any:
			var ok bool
			value, ok = node[part]
			if !ok {
				return nil, false
			}
		case []any:
			i, err := strconv.Atoi(part)
			if err != nil || i < 0 || i >= len(node) {
				return nil, false
			}
			value = node[i]
		default:
			return nil, false
		}
	}
	return value, true
}

func (c *impactCollector) resources(s *impactSnapshot) {
	if err := resourcemap.ValidateStored(s.root); err != nil {
		c.diagnostic(ImpactDiagnostic{Code: "resource_projection_failed", Severity: "warning", Side: s.side, Pointer: "/" + resourcemap.Extension, Message: err.Error()}, true)
		return
	}
	assigned := map[string]bool{}
	storedByID := map[string]ImpactEntity{}
	items, _ := impactObject(s.root[resourcemap.Extension])["resources"].([]any)
	for i, item := range items {
		resource := impactObject(item)
		id, name := impactText(resource["id"]), impactText(resource["name"])
		pointer := "/" + resourcemap.Extension + "/resources/" + strconv.Itoa(i)
		storedByID[id] = impactEntity("resource", id, name, s.side, ImpactLocator{Pointer: pointer, ResourceID: id})
		keys, _ := resource["operationKeys"].([]any)
		for _, key := range keys {
			for _, op := range s.operations {
				if !c.visit() {
					return
				}
				if op.key != impactText(key) || s.keyCounts[op.key] != 1 {
					continue
				}
				assigned[op.pointer] = true
				e := impactEntity("resource", id, name, s.side, ImpactLocator{Pointer: pointer, ResourceID: id})
				s.nodes = append(s.nodes, impactNode{entity: e, pointer: pointer, operationPointer: op.pointer})
			}
		}
	}
	for _, op := range s.operations {
		if assigned[op.pointer] {
			continue
		}
		name := impactResourceFamily(op.path)
		hash := sha256.Sum256([]byte(name))
		id := fmt.Sprintf("auto-%x", hash[:12])
		locator := ImpactLocator{Pointer: op.pointer, ResourceID: id}
		if op.sourcePointer != op.pointer {
			locator.SourcePointer = op.sourcePointer
		}
		e := impactEntity("resource", id, name, s.side, locator)
		pointer := op.pointer
		if stored, ok := storedByID[id]; ok {
			e = stored
			if s.side == "before" {
				pointer = e.Before.Pointer
			} else {
				pointer = e.After.Pointer
			}
		}
		s.nodes = append(s.nodes, impactNode{entity: e, pointer: pointer, operationPointer: op.pointer})
	}
}
func impactResourceFamily(path string) string {
	parts := strings.Split(path, "/")
	for len(parts) > 1 && strings.HasPrefix(parts[len(parts)-1], "{") && strings.HasSuffix(parts[len(parts)-1], "}") {
		parts = parts[:len(parts)-1]
	}
	for i, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			parts[i] = "{}"
		}
	}
	result := strings.Join(parts, "/")
	if result == "" {
		return "/"
	}
	return result
}
func (c *impactCollector) states(s *impactSnapshot) {
	envelope, err := statediagram.Decode(s.root)
	if err != nil {
		c.diagnostic(ImpactDiagnostic{Code: "state_projection_failed", Severity: "warning", Side: s.side, Pointer: "/" + statediagram.Extension, Message: err.Error()}, true)
		return
	}
	for i, diagram := range envelope.Diagrams {
		for j, tr := range diagram.Transitions {
			if !c.visit() {
				return
			}
			pointer := fmt.Sprintf("/%s/diagrams/%d/transitions/%d", statediagram.Extension, i, j)
			locator := ImpactLocator{Pointer: pointer, DiagramID: diagram.ID, TransitionID: tr.ID}
			operationPointer := ""
			if tr.Binding != nil {
				locator.Method, locator.Path = strings.ToUpper(tr.Binding.Method), tr.Binding.Path
				for _, op := range s.operations {
					if !c.visit() {
						return
					}
					if op.method == locator.Method && op.path == locator.Path {
						operationPointer = op.pointer
						break
					}
				}
			}
			e := impactEntity("state_transition", diagram.ID+"/"+tr.ID, tr.Name, s.side, locator)
			s.nodes = append(s.nodes, impactNode{entity: e, pointer: pointer, operationPointer: operationPointer})
		}
	}
}
func (c *impactCollector) checkStateBindings(s *impactSnapshot) {
	for _, node := range s.nodes {
		if !c.visit() {
			return
		}
		if node.entity.Kind != "state_transition" {
			continue
		}
		loc := node.entity.After
		if loc != nil && loc.Method != "" && node.operationPointer == "" {
			c.diagnostic(ImpactDiagnostic{Code: "state_binding_missing_operation", Severity: "warning", Side: s.side, Pointer: node.pointer + "/binding", Message: "Привязка перехода указывает на отсутствующий HTTP-адрес: " + loc.Method + " " + loc.Path}, false)
		}
	}
}

type impactPath struct {
	pointer string
	sites   []ImpactReferenceSite
}

func (c *impactCollector) propagate(change ImpactChange, s *impactSnapshot) {
	if (change.Kind == "added" && s.side == "before") || (change.Kind == "removed" && s.side == "after") {
		return
	}
	queue := []impactPath{{pointer: change.Pointer, sites: []ImpactReferenceSite{}}}
	bestDistance := map[string]int{change.Pointer: 0}
	for head := 0; head < len(queue); head++ {
		if !c.visit() {
			return
		}
		current := queue[head]
		if len(current.sites) > bestDistance[current.pointer] {
			continue
		}
		for _, node := range s.nodes {
			if !c.visit() {
				return
			}
			if node.operationPointer != "" || !impactOverlap(node.pointer, current.pointer) {
				continue
			}
			c.affect(node.entity, ImpactEvidence{ChangeID: change.ID, Side: s.side, Direction: "unknown", ReferenceSites: current.sites, Explanation: "Изменение затрагивает определение " + node.entity.Label})
		}
		for _, op := range s.operations {
			for _, source := range op.sources {
				if !c.visit() {
					return
				}
				matches := impactOverlap(source.pointer, current.pointer)
				direction := source.direction
				if direction == "root" {
					matches = impactAncestor(current.pointer, source.pointer)
					direction = "unknown"
				}
				if !matches {
					continue
				}
				sites := append(slices.Clone(current.sites), source.sites...)
				locator := ImpactLocator{Pointer: op.pointer, OperationKey: op.key, Method: op.method, Path: op.path}
				if s.keyCounts[op.key] > 1 {
					locator.OperationKey = ""
				}
				if op.sourcePointer != op.pointer {
					locator.SourcePointer = op.sourcePointer
				}
				entity := impactEntity("operation", "", op.method+" "+op.path, s.side, locator)
				entity.ID = op.id
				c.affect(entity, ImpactEvidence{ChangeID: change.ID, Side: s.side, Direction: direction, ReferenceSites: sites, Explanation: "Операция использует изменённую часть контракта"})
				for _, node := range s.nodes {
					if !c.visit() {
						return
					}
					if node.operationPointer != op.pointer {
						continue
					}
					kind := "resource_membership"
					if node.entity.Kind == "state_transition" {
						kind = "state_binding"
					}
					withUsage := append(slices.Clone(sites), ImpactReferenceSite{Pointer: node.pointer, TargetPointer: op.pointer, Kind: kind})
					c.affect(node.entity, ImpactEvidence{ChangeID: change.ID, Side: s.side, Direction: direction, ReferenceSites: withUsage, Explanation: "Использует затронутую операцию " + op.method + " " + op.path})
				}
			}
		}

		for _, contract := range s.contracts {
			op := contract.operation
			for _, source := range op.sources {
				if !c.visit() {
					return
				}
				direction := source.direction
				matches := impactOverlap(source.pointer, current.pointer)
				if direction == "root" {
					matches = impactAncestor(current.pointer, source.pointer)
					direction = "unknown"
				}
				if !matches {
					continue
				}
				sites := append(slices.Clone(current.sites), source.sites...)
				c.affect(contract.entity, ImpactEvidence{ChangeID: change.ID, Side: s.side, Direction: direction, ReferenceSites: sites, Explanation: "Контрактный узел использует изменённую часть контракта; требуется проверка"})
				// Preserve the changed method/subtree while crossing Path Item aliases.
				// Enqueuing the entire path would resurrect overridden sibling methods.
				mapped := current.pointer
				if impactAncestor(op.sourcePointer, current.pointer) {
					mapped = op.pointer + strings.TrimPrefix(current.pointer, op.sourcePointer)
				} else if len(source.sites) > 0 {
					mapped = op.pointer
				}

				if distance, seen := bestDistance[mapped]; !seen || len(sites) < distance {
					bestDistance[mapped] = len(sites)
					queue = append(queue, impactPath{pointer: mapped, sites: sites})
				}
			}
		}
		for _, ref := range s.refs {
			if !c.visit() {
				return
			}
			// Effective method/parameter sources already resolve Path Item inheritance.
			// Following the whole Path Item here would resurrect overridden siblings.
			if !ref.Supported || ref.ObjectKind == "path" || !impactOverlap(ref.TargetPointer, current.pointer) {
				continue
			}
			pointer := impactParent(ref.Pointer)

			sites := append(slices.Clone(current.sites), ImpactReferenceSite{Pointer: ref.Pointer, TargetPointer: ref.TargetPointer, Kind: ref.Kind})
			if distance, seen := bestDistance[pointer]; seen && distance <= len(sites) {
				continue
			}
			bestDistance[pointer] = len(sites)
			queue = append(queue, impactPath{pointer: pointer, sites: sites})
		}
	}
}

func (c *impactCollector) hydrateLocators(s *impactSnapshot) {
	for _, node := range s.nodes {
		if !c.visit() {
			return
		}
		if index, ok := c.entities[node.entity.ID]; ok {
			if node.entity.Before != nil {
				c.result.Affected[index].Before = node.entity.Before
			}
			if node.entity.After != nil {
				c.result.Affected[index].After = node.entity.After
			}
		}
	}
	for _, op := range s.operations {
		if !c.visit() {
			return
		}
		if index, ok := c.entities[op.id]; ok {
			loc := &ImpactLocator{Pointer: op.pointer, OperationKey: op.key, Method: op.method, Path: op.path}
			if s.keyCounts[op.key] > 1 {
				loc.OperationKey = ""
			}
			if op.sourcePointer != op.pointer {
				loc.SourcePointer = op.sourcePointer
			}
			if s.side == "before" {
				c.result.Affected[index].Before = loc
			} else {
				c.result.Affected[index].After = loc
			}
		}
	}
}

// Reference sites are ordered from the authored dependency outwards to the
// concrete consuming path, matching the direction shown in the report graph.
func impactInheritanceSites(chain []schemamodel.PathItemNode, source string) []ImpactReferenceSite {
	sites := []ImpactReferenceSite{}
	for i, node := range chain {
		if !impactAncestor(node.Pointer, source) {
			continue
		}
		for j := i; j > 0; j-- {
			sites = append(sites, ImpactReferenceSite{Pointer: chain[j-1].Pointer + "/$ref", TargetPointer: chain[j].Pointer, Kind: "ref"})
		}
		break
	}
	return sites
}

// Webhooks, callback expressions and reusable Path Items have effective methods
// like paths, but remain contract nodes without runtime/scenario identities.
func (c *impactCollector) contractPathSources(s *impactSnapshot, pointers []string) {
	for _, pointer := range pointers {
		if !c.visit() {
			return
		}
		parts := impactTokens(pointer)
		if len(parts) == 2 && parts[0] == "paths" {
			continue
		}
		value, _ := impactLookup(s.root, pointer)
		chain, diagnostics := schemamodel.PathItems(s.root, value, pointer)
		for _, d := range diagnostics {
			c.diagnostic(ImpactDiagnostic{Code: d.Code, Severity: "warning", Side: s.side, Pointer: d.Pointer, Message: d.Message}, d.Code != "path_item_method_conflict")
		}
		label := parts[len(parts)-1]
		if len(parts) > 0 && parts[0] == "webhooks" {
			label = "Webhook " + label
		}
		entity := impactEntity("contract_node", pointer, label, s.side, ImpactLocator{Pointer: pointer})
		present := false
		for _, node := range s.nodes {
			if !c.visit() {
				return
			}
			if node.entity.ID == entity.ID {
				present = true
				break
			}
		}
		if !present {
			s.nodes = append(s.nodes, impactNode{entity: entity, pointer: pointer})
		}
		for _, method := range schemamodel.PathItemOperations(chain) {
			if !c.visit() {
				return
			}
			object := impactObject(method.Value)
			if object == nil {
				continue
			}
			op := &impactOperation{pointer: pointer + "/" + method.Method, sourcePointer: method.Pointer, value: object, sources: []impactSource{}}
			for _, key := range slices.Sorted(maps.Keys(object)) {
				if !c.visit() {
					return
				}
				if key == "parameters" || key == "security" || key == "servers" {
					continue
				}
				direction := "unknown"
				if key == "requestBody" {
					direction = "request"
				}
				if key == "responses" {
					direction = "response"
				}
				op.sources = append(op.sources, impactSource{pointer: method.Pointer + "/" + escape(key), direction: direction, sites: impactInheritanceSites(chain, method.Pointer)})
			}
			op.sources = append(op.sources, impactSource{pointer: method.Pointer, direction: "root", sites: impactInheritanceSites(chain, method.Pointer)})
			c.operationSources(s, op, chain)
			s.contracts = append(s.contracts, impactContractMethod{operation: op, entity: entity})
		}
	}
}
