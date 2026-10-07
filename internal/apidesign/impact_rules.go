package apidesign

import (
	"maps"
	"slices"
	"strings"
)

func (c *impactCollector) classifyImpact(change *ImpactChange, before, after *impactSnapshot) {
	parts := impactTokens(change.Pointer)
	if len(parts) == 0 {
		return
	}
	if c.identityChange(change, parts, before, after) {
		return
	}
	if impactMetadata(parts) {
		change.ChangeClass = "metadata"
		change.Compatibility = "compatible"
		change.ReasonCode = "presentation_changed"
		change.Explanation = "Изменилось описание или оформление; HTTP-контракт сохранён"
		return
	}
	// HTTP address continuity is independent of editor identity continuity.
	for _, op := range before.operations {
		if !c.visit() {
			return
		}
		if impactAddress(after, op.method, op.path) != nil {
			continue
		}
		if c.operationChange(change.Pointer, op) {
			change.Compatibility = "breaking"
			change.ReasonCode = "operation_removed"
			change.Explanation = "HTTP-адрес операции больше недоступен: " + op.method + " " + op.path
			if after.uncertainPaths[op.path] {
				change.Compatibility = "review"
				change.ReasonCode = "operation_resolution_unknown"
				change.Explanation = "Наличие HTTP-адреса зависит от неразрешённого Path Item"
			}
			return
		}
	}
	for _, op := range after.operations {
		if !c.visit() {
			return
		}
		if impactAddress(before, op.method, op.path) != nil {
			continue
		}
		if impactAncestor(change.Pointer, op.pointer) {
			change.Compatibility = "compatible"
			change.ReasonCode = "operation_added"
			change.Explanation = "Добавлен HTTP-адрес операции: " + op.method + " " + op.path
			if after.uncertainPaths[op.path] {
				change.Compatibility = "review"
				change.ReasonCode = "operation_resolution_unknown"
				change.Explanation = "Добавленный адрес содержит неразрешённый Path Item"
			}
			// Overlapping templates cannot establish compatible addition.
			for _, old := range after.operations {
				if !c.visit() {
					return
				}
				if old.pointer != op.pointer && old.method == op.method && impactPathShape(old.path) == impactPathShape(op.path) {
					change.Compatibility = "review"
					change.ReasonCode = "overlapping_operation"
					change.Explanation = "Шаблон нового пути пересекается с другой операцией"
				}
			}
			return
		}
	}
	if c.parameterArrayRule(change, before, after) {
		return
	}
	if c.requiredRule(change, before, after) {
		return
	}
	leaf := parts[len(parts)-1]
	switch leaf {
	case "operationId":
		change.ReasonCode = "operation_id_changed"
		change.Explanation = "Изменилось имя операции для генераторов клиентов"
	case "type":
		change.ReasonCode = "schema_type_changed"
		change.Explanation = "Изменился тип данных; проверьте запросы и ответы потребителей"
	case "enum":
		change.ReasonCode = "schema_enum_changed"
		change.Explanation = "Изменился набор допустимых значений"
	case "required":
		change.ReasonCode = "schema_required_review"
		change.Explanation = "Изменились обязательные поля; направление или схема требуют отдельной проверки"
	default:
		if slices.Contains(parts, "responses") {
			change.ReasonCode = "response_changed"
			change.Explanation = "Изменился ответ операции; проверьте потребителей ответа"
		}
	}
}
func impactAddress(s *impactSnapshot, method, path string) *impactOperation {
	return s.addresses[method+" "+path]
}

func (c *impactCollector) operationChange(pointer string, op *impactOperation) bool {
	if impactOverlap(pointer, op.pointer) || impactOverlap(pointer, op.sourcePointer) {
		return true
	}
	for _, source := range op.sources {
		if !c.visit() {
			return false
		}
		if strings.HasSuffix(source.pointer, "/$ref") && impactOverlap(pointer, source.pointer) {
			return true
		}
	}
	return false
}
func impactPathShape(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			parts[i] = "{}"
		}
	}
	return strings.Join(parts, "/")
}
func (c *impactCollector) identityChange(change *ImpactChange, parts []string, before, after *impactSnapshot) bool {
	if parts[0] != "paths" || len(parts) < 3 {
		return false
	}
	isKey := len(parts) == 4 && methods[parts[2]] && parts[3] == OperationKey
	isKeys := parts[2] == PathOperationKeys && len(parts) <= 4
	if !isKey && !isKeys {
		return false
	}
	change.ChangeClass = "metadata"
	change.Compatibility = "compatible"
	change.ReasonCode = "operation_identity_added"
	change.Explanation = "Добавлен устойчивый ключ операции; HTTP-контракт сохранён"
	for _, op := range before.operations {
		if !c.visit() {
			return true
		}
		if op.key == "" || !impactOverlap(change.Pointer, op.keyPointer) {
			continue
		}
		target := impactAddress(after, op.method, op.path)
		if target == nil || target.key != op.key {
			change.Compatibility = "review"
			change.ReasonCode = "operation_identity_changed"
			change.Explanation = "Изменилась привязка операции; проверьте назначения ресурсов и сценарные шаги"
			return true
		}
	}
	return true
}

// Resolve object vocabulary, consuming named-map keys as names. A property
// named "description" or "example" therefore never becomes metadata.
func impactContext(parts []string) string {
	kind := "root"
	for i := 0; i < len(parts); i++ {
		token := parts[i]
		switch kind {
		case "root":
			switch token {
			case "info":
				kind = "info"
			case "components":
				kind = "components"
			case "paths", "webhooks":
				if i+1 >= len(parts) {
					return "named_map"
				}
				i++
				kind = "path"
			default:
				return "opaque"
			}
		case "components":
			if i+1 >= len(parts) {
				return "named_map"
			}
			i++
			switch token {
			case "schemas":
				kind = "schema"
			case "parameters", "headers":
				kind = "parameter"
			case "requestBodies":
				kind = "request"
			case "responses":
				kind = "response"
			case "pathItems":
				kind = "path"
			case "callbacks":
				kind = "callback"
			case "securitySchemes":
				kind = "security"
			case "examples":
				kind = "example"
			default:
				return "opaque"
			}
		case "path":
			if methods[token] {
				kind = "operation"
			} else if token == "parameters" {
				if i+1 >= len(parts) {
					return "array"
				}
				i++
				kind = "parameter"
			} else {
				return "opaque"
			}
		case "operation":
			switch token {
			case "parameters":
				if i+1 >= len(parts) {
					return "array"
				}
				i++
				kind = "parameter"
			case "requestBody":
				kind = "request"
			case "responses":
				if i+1 >= len(parts) {
					return "named_map"
				}
				i++
				kind = "response"
			case "callbacks":
				if i+1 >= len(parts) {
					return "named_map"
				}
				i++
				kind = "callback"
			default:
				return "opaque"
			}
		case "request", "response", "parameter":
			switch token {
			case "schema":
				kind = "schema"
			case "content":
				if i+1 >= len(parts) {
					return "named_map"
				}
				i++
				kind = "media"
			case "headers":
				if i+1 >= len(parts) {
					return "named_map"
				}
				i++
				kind = "parameter"
			case "examples":
				if i+1 >= len(parts) {
					return "named_map"
				}
				i++
				kind = "example"
			default:
				return "opaque"
			}
		case "media":
			switch token {
			case "schema":
				kind = "schema"
			case "examples":
				if i+1 >= len(parts) {
					return "named_map"
				}
				i++
				kind = "example"
			default:
				return "opaque"
			}
		case "schema":
			switch token {
			case "properties", "patternProperties", "$defs", "definitions", "dependentSchemas", "dependencies":
				if i+1 >= len(parts) {
					return "named_map"
				}
				i++
			case "allOf", "oneOf", "anyOf", "prefixItems":
				if i+1 >= len(parts) {
					return "array"
				}
				i++
			case "items", "additionalProperties", "unevaluatedProperties", "additionalItems", "contains", "not", "if", "then", "else", "propertyNames":
			default:
				return "opaque"
			}
		case "callback":
			kind = "path"
		default:
			return "opaque"
		}
	}
	return kind
}
func impactMetadata(parts []string) bool {
	for i := 1; i < len(parts); i++ {
		if impactMetadataAt(parts[:i]) {
			return true
		}
	}
	return impactMetadataAt(parts)
}
func impactMetadataAt(parts []string) bool {
	if len(parts) == 0 {
		return false
	}
	if parts[0] == "x-mocker-schema-layout" {
		return len(parts) == 4 && parts[1] == "positions" && (parts[3] == "x" || parts[3] == "y")
	}
	if parts[0] == "x-mocker-resource-map" {
		if len(parts) == 4 && parts[1] == "resources" {
			return slices.Contains([]string{"x", "y", "name", "description", "service"}, parts[3])
		}
		if len(parts) == 4 && parts[1] == "relations" && parts[3] == "label" {
			return true
		}
	}
	if parts[0] == "x-mocker-state-diagrams" && len(parts) >= 4 {
		if len(parts) == 4 && parts[1] == "diagrams" && parts[3] == "name" {
			return true
		}
		if len(parts) == 6 && parts[1] == "diagrams" && parts[3] == "states" {
			return slices.Contains([]string{"x", "y", "name"}, parts[5])
		}
		if len(parts) == 6 && parts[3] == "transitions" && parts[5] == "name" {
			return true
		}
	}
	leaf := parts[len(parts)-1]
	kind := impactContext(parts[:len(parts)-1])
	if kind == "info" {
		return slices.Contains([]string{"title", "version", "description", "termsOfService", "contact", "license"}, leaf)
	}
	if kind == "opaque" || kind == "named_map" || kind == "array" {
		return false
	}
	if leaf == "description" || leaf == "summary" {
		return true
	}
	if leaf == "example" || leaf == "examples" {
		return slices.Contains([]string{"schema", "parameter", "media"}, kind)
	}
	if leaf == "title" {
		return kind == "schema"
	}
	return kind == "example" && leaf == "value"
}
func (c *impactCollector) requiredRule(change *ImpactChange, before, after *impactSnapshot) bool {
	parts := impactTokens(change.Pointer)
	if len(parts) < 3 || parts[0] != "paths" {
		return false
	}
	location, tail := inputLocation(parts)
	if location == "" {
		return false
	}
	// Compare full parameter objects by identity, including inherited defaults.
	for _, op := range after.operations {
		if !c.visit() {
			return false
		}
		old := impactAddress(before, op.method, op.path)
		if old == nil {
			continue
		}
		for _, source := range op.sources {
			if !c.visit() {
				return false
			}
			if source.direction != "request" || !impactOverlap(change.Pointer, source.pointer) {
				continue
			}
			value, _ := impactLookup(after.root, source.pointer)
			parameter := impactObject(value)
			if parameter == nil || parameter["$ref"] != nil {
				continue
			}
			name, in := impactText(parameter["name"]), impactText(parameter["in"])
			if name == "" || in == "" {
				continue
			}
			prior := map[string]any{}
			for _, oldSource := range old.sources {
				if !c.visit() {
					return false
				}
				oldValue, _ := impactLookup(before.root, oldSource.pointer)
				candidate := impactResolveObject(before.root, impactObject(oldValue))
				if oldSource.direction == "request" && impactObject(oldValue)["$ref"] != nil && candidate == nil {
					return false
				}
				if impactText(candidate["name"]) == name && impactText(candidate["in"]) == in {
					prior = candidate
					break
				}
			}
			if parameter["required"] == true && prior["required"] != true {
				change.Compatibility = "breaking"
				change.ReasonCode = "input_required"
				change.Explanation = "Входной параметр стал обязательным: " + name
				return true
			}
		}
	}
	if len(tail) == 0 || (len(tail) == 1 && tail[0] == "required") {
		objectPointer := change.Pointer
		if len(tail) == 1 {
			objectPointer = impactParent(objectPointer)
		}
		a, existed := impactLookup(before.root, objectPointer)
		b, _ := impactLookup(after.root, objectPointer)
		am, bm := impactResolveObject(before.root, impactObject(a)), impactResolveObject(after.root, impactObject(b))
		if existed && am == nil {
			return false
		}
		if bm != nil && bm["$ref"] == nil && bm["required"] == true && am["required"] != true {
			change.Compatibility = "breaking"
			change.ReasonCode = "input_required"
			change.Explanation = "Входные данные стали обязательными"
			return true
		}
	}
	return c.schemaRequiredRule(change, before.root, after.root)
}

func (c *impactCollector) schemaRequiredRule(change *ImpactChange, beforeRoot, afterRoot map[string]any) bool {
	parts := impactTokens(change.Pointer)
	if len(parts) < 3 || parts[0] != "paths" {
		return false
	}
	location, tail := inputLocation(parts)
	if location == "" {
		return false
	}
	if parts[len(parts)-1] != "required" {
		return false
	}
	schemaTail := tail
	if len(schemaTail) > 0 && schemaTail[0] == "schema" {
		schemaTail = schemaTail[1:]
	} else if len(schemaTail) > 2 && schemaTail[0] == "content" && schemaTail[2] == "schema" {
		schemaTail = schemaTail[3:]
	} else {
		return false
	}
	if len(schemaTail) == 0 || !directInputSchema(schemaTail[:len(schemaTail)-1]) {
		return false
	}
	parent := impactParent(change.Pointer)
	a, _ := impactLookup(beforeRoot, parent)
	b, _ := impactLookup(afterRoot, parent)
	am, bm := impactObject(a), impactObject(b)
	if impactAmbiguousSchema(am) || impactAmbiguousSchema(bm) {
		return false
	}
	// A reference/composition on any containing input schema also makes a local
	// required keyword insufficient to prove request compatibility.
	at := parent
	for at != "" {
		if !c.visit() {
			return false
		}
		v, _ := impactLookup(afterRoot, at)
		oldValue, _ := impactLookup(beforeRoot, at)
		if impactContext(impactTokens(at)) == "schema" && (impactAmbiguousSchema(impactObject(oldValue)) || impactObject(oldValue)["readOnly"] == true) {
			return false
		}
		if impactContext(impactTokens(at)) == "schema" && (impactAmbiguousSchema(impactObject(v)) || impactObject(v)["readOnly"] == true) {
			return false
		}
		at = impactParent(at)
	}
	oldSet, newSet := impactStringSet(am["required"]), impactStringSet(bm["required"])
	if oldSet == nil || newSet == nil {
		return false
	}
	added := false
	changedNames := maps.Clone(oldSet)
	maps.Copy(changedNames, newSet)
	for name := range changedNames {
		if !c.visit() {
			return false
		}
		if oldSet[name] == newSet[name] {
			continue
		}
		ap := impactObject(impactObject(am["properties"])[name])
		bp := impactObject(impactObject(bm["properties"])[name])
		if (ap == nil && bp == nil) || ap["readOnly"] == true || bp["readOnly"] == true || impactAmbiguousSchema(ap) || impactAmbiguousSchema(bp) {
			return false
		}
		if !oldSet[name] && newSet[name] {
			added = true
		}
	}
	change.Compatibility = "compatible"
	change.ReasonCode = "input_required_relaxed"
	change.Explanation = "Новые обязательные входные поля не добавлены"
	if added {
		change.Compatibility = "breaking"
		change.ReasonCode = "input_required"
		change.Explanation = "Добавлены обязательные входные поля"
	}
	return true
}
func impactAmbiguousSchema(m map[string]any) bool {
	for _, key := range []string{"$ref", "$dynamicRef", "allOf", "oneOf", "anyOf", "not", "if", "then", "else", "dependentSchemas", "dependentRequired"} {
		if _, ok := m[key]; ok {
			return true
		}
	}
	return false
}
func impactStringSet(v any) map[string]bool {
	out := map[string]bool{}
	if v == nil {
		return out
	}
	array, ok := v.([]any)
	if !ok {
		return nil
	}
	for _, value := range array {
		name, ok := value.(string)
		if !ok {
			return nil
		}
		out[name] = true
	}
	return out
}

// Parameter arrays are paired by (name,in). Their report excerpt stays atomic,
// while classification retains each paired object's structural context.
func (c *impactCollector) parameterArrayRule(change *ImpactChange, before, after *impactSnapshot) bool {
	parts := impactTokens(change.Pointer)
	if len(parts) == 0 || parts[len(parts)-1] != "parameters" {
		return false
	}
	kind := impactContext(parts[:len(parts)-1])
	if kind != "operation" && kind != "path" {
		return false
	}
	if c.requiredRule(change, before, after) {
		return true
	}
	oldValue, _ := impactLookup(before.root, change.Pointer)
	newValue, _ := impactLookup(after.root, change.Pointer)
	oldParameters, oldOK := c.parameterPairs(before.root, oldValue)
	newParameters, newOK := c.parameterPairs(after.root, newValue)
	if !oldOK || !newOK || len(oldParameters) != len(newParameters) {
		return false
	}
	allMetadata, allCompatible := true, true
	const pointer = "/paths/~1__impact_parameter/get/parameters/0"
	for _, identity := range slices.Sorted(maps.Keys(oldParameters)) {
		if !c.visit() {
			return false
		}
		oldParameter := oldParameters[identity]
		newParameter, exists := newParameters[identity]
		if !exists {
			return false
		}
		detail := newImpactCollector(c.ctx)
		detail.visits = c.visits
		detail.diff(pointer, oldParameter, newParameter, len(parts)+1)
		c.visits = detail.visits
		for _, reason := range detail.result.Coverage.TruncatedReasons {
			c.truncate(reason)
		}
		if !detail.result.Complete {
			return false
		}
		oldRoot := impactParameterDocument(before.root, oldParameter)
		newRoot := impactParameterDocument(after.root, newParameter)
		for _, delta := range detail.result.Changes {
			if !c.visit() {
				return false
			}
			if impactMetadata(impactTokens(delta.Pointer)) {
				continue
			}
			allMetadata = false
			if c.schemaRequiredRule(&delta, oldRoot, newRoot) {
				if delta.Compatibility == "breaking" {
					if !c.parameterHasEffectivePair(change.Pointer, identity, before, after) {
						return false
					}
					change.Compatibility = delta.Compatibility
					change.ReasonCode = delta.ReasonCode
					change.Explanation = delta.Explanation
					return true
				}
			} else {
				allCompatible = false
			}
		}
	}
	if allMetadata {
		change.ChangeClass = "metadata"
		change.Compatibility = "compatible"
		change.ReasonCode = "parameter_presentation_changed"
		change.Explanation = "Изменились описания или порядок параметров; HTTP-контракт сохранён"
		return true
	}
	if allCompatible {
		change.Compatibility = "compatible"
		change.ReasonCode = "input_required_relaxed"
		change.Explanation = "Новые обязательные входные поля параметров не добавлены"
		return true
	}
	return false
}
func (c *impactCollector) parameterPairs(root map[string]any, value any) (map[string]map[string]any, bool) {
	out := map[string]map[string]any{}
	if value == nil {
		return out, true
	}
	parameters, ok := value.([]any)
	if !ok {
		return out, false
	}
	for _, value := range parameters {
		if !c.visit() {
			return out, false
		}
		raw := impactObject(value)
		resolved := impactResolveObject(root, raw)
		name, in := impactText(resolved["name"]), impactText(resolved["in"])
		if name == "" || in == "" {
			return out, false
		}
		identity := name + "\x00" + in
		if _, duplicate := out[identity]; duplicate {
			return out, false
		}
		out[identity] = raw
	}
	return out, true
}
func impactParameterDocument(root, parameter map[string]any) map[string]any {
	next := maps.Clone(root)
	next["paths"] = map[string]any{"/__impact_parameter": map[string]any{"get": map[string]any{"parameters": []any{parameter}}}}
	return next
}

// A tightening in an authored parameter is an HTTP break only when a concrete
// operation used that parameter on both sides. An override may have already
// required the field, or may still hide the changed parameter entirely.
func (c *impactCollector) parameterHasEffectivePair(pointer, identity string, before, after *impactSnapshot) bool {
	for _, op := range after.operations {
		if !c.visit() {
			return false
		}
		old := impactAddress(before, op.method, op.path)
		if old == nil {
			continue
		}
		if c.operationUsesParameter(after, op, pointer, identity) && c.operationUsesParameter(before, old, pointer, identity) {
			return true
		}
	}
	return false
}
func (c *impactCollector) operationUsesParameter(snapshot *impactSnapshot, op *impactOperation, pointer, identity string) bool {
	for _, source := range op.sources {
		if !c.visit() {
			return false
		}
		if source.direction != "request" || !impactAncestor(pointer, source.pointer) {
			continue
		}
		value, _ := impactLookup(snapshot.root, source.pointer)
		parameter := impactResolveObject(snapshot.root, impactObject(value))
		if impactText(parameter["name"])+"\x00"+impactText(parameter["in"]) == identity {
			return true
		}
	}
	return false
}
