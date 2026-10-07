package responserules

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/schemamodel"
)

var ErrRuleNotFound = errors.New("response rule not found")

type validator struct {
	ctx       context.Context
	rule      Rule
	prefix    string
	result    Validation
	nodeOrder map[string]int
	edgeOrder map[string]int
}

func (v *validator) add(code, severity, message, pointer, nodeID, edgeID string) {
	if severity == "error" {
		v.result.Valid = false
	}
	if len(v.result.Diagnostics) >= MaxDiagnostics {
		v.result.DiagnosticsTruncated = true
		v.result.Valid = false
		return
	}
	v.result.Diagnostics = append(v.result.Diagnostics, Diagnostic{Code: code, Severity: severity, Message: message, Pointer: v.prefix + pointer, RuleID: v.rule.ID, NodeID: nodeID, EdgeID: edgeID})
}
func (v *validator) node(i int, code, message, field string) {
	v.add(code, "error", message, fmt.Sprintf("/nodes/%d%s", i, field), v.rule.Nodes[i].ID, "")
}
func (v *validator) edge(i int, code, message, field string) {
	v.add(code, "error", message, fmt.Sprintf("/edges/%d%s", i, field), "", v.rule.Edges[i].ID)
}

// Validate checks the entire envelope structurally, then the selected rule semantically.
func Validate(ctx context.Context, env Envelope, ruleID string, root map[string]any) (Validation, error) {
	if err := ctx.Err(); err != nil {
		return Validation{}, err
	}
	selected, err := admitEnvelope(ctx, env, ruleID)
	if err != nil {
		return Validation{}, err
	}
	r := env.Rules[selected]
	v := validator{ctx: ctx, rule: r, prefix: fmt.Sprintf("/%s/rules/%d", Extension, selected), result: Validation{Valid: true, Diagnostics: []Diagnostic{}}, nodeOrder: map[string]int{}, edgeOrder: map[string]int{}}
	for i, n := range r.Nodes {
		v.nodeOrder[n.ID] = i
	}
	for i, e := range r.Edges {
		v.edgeOrder[e.ID] = i
	}
	if strings.TrimSpace(r.Name) == "" {
		v.add("invalid_name", "error", "Укажите имя правила.", "/name", "", "")
	}
	operation := v.binding(root)
	if r.Binding != nil {
		for i, other := range env.Rules {
			if err := ctx.Err(); err != nil {
				return Validation{}, err
			}
			if i != selected && other.Binding != nil && *other.Binding == *r.Binding {
				v.add("duplicate_binding", "error", "Несколько правил привязаны к одной операции.", "/binding", "", "")
				break
			}
		}
	}
	if err := v.nodes(operation); err != nil {
		return Validation{}, err
	}
	if err := v.graph(); err != nil {
		return Validation{}, err
	}
	v.entityReferences()
	v.entityTerminals()
	// Array order is authoritative; check code breaks ties within one object.
	rank := func(d Diagnostic) int {
		if d.NodeID != "" {
			return v.nodeOrder[d.NodeID] + 1
		}
		if d.EdgeID != "" {
			return len(r.Nodes) + 1 + v.edgeOrder[d.EdgeID]
		}
		return 0
	}
	slices.SortStableFunc(v.result.Diagnostics, func(a, b Diagnostic) int {
		if rank(a) != rank(b) {
			return rank(a) - rank(b)
		}
		if c := strings.Compare(a.Code, b.Code); c != 0 {
			return c
		}
		return strings.Compare(a.Pointer, b.Pointer)
	})
	if err := ctx.Err(); err != nil {
		return Validation{}, err
	}
	return v.result, nil
}
func (v *validator) binding(root map[string]any) map[string]any {
	b := v.rule.Binding
	bad := func() {
		v.add("invalid_binding", "error", "Выберите существующую операцию API.", "/binding", "", "")
	}
	if b == nil {
		bad()
		return nil
	}
	switch b.Method {
	case http.MethodGet, http.MethodPut, http.MethodPost, http.MethodDelete, http.MethodOptions, http.MethodHead, http.MethodPatch, http.MethodTrace:
	default:
		bad()
		return nil
	}
	paths, _ := root["paths"].(map[string]any)
	item, ok := paths[b.Path].(map[string]any)
	if !ok || !strings.HasPrefix(b.Path, "/") {
		bad()
		return nil
	}
	pointer := "/paths/" + strings.ReplaceAll(strings.ReplaceAll(b.Path, "~", "~0"), "/", "~1")
	nodes, _ := schemamodel.PathItems(root, item, pointer)
	for _, occurrence := range schemamodel.PathItemOperations(nodes) {
		if occurrence.Method == strings.ToLower(b.Method) {
			if operation, ok := occurrence.Value.(map[string]any); ok {
				return operation
			}
			break
		}
	}
	bad()
	return nil
}
func (v *validator) nodes(operation map[string]any) error {
	for i, n := range v.rule.Nodes {
		if err := v.ctx.Err(); err != nil {
			return err
		}
		if strings.TrimSpace(n.Name) == "" {
			v.node(i, "invalid_name", "Укажите имя узла.", "/name")
		}
		switch n.Type {
		case "condition":
			if n.Condition != nil {
				if err := overrides.ValidateConditions([]overrides.Condition{*n.Condition}); err != nil {
					v.node(i, "invalid_condition", "Укажите источник, имя, оператор и значение условия.", "/condition")
				}
			}
		case "delay":
			if *n.DelayMs < 0 || *n.DelayMs > MaxDelayMs {
				v.node(i, "delay_limit", "Задержка должна быть от 0 до 30000 мс.", "/delayMs")
			}
		case "response":
			if err := v.response(i, n, operation); err != nil {
				return err
			}
		}
	}
	return nil
}
func ports(kind string) []string {
	switch kind {
	case "start", "delay":
		return []string{"next"}
	case "condition":
		return []string{"true", "false"}
	default:
		return nil
	}
}
func (v *validator) graph() error {
	r := v.rule
	starts := []int{}
	out := make([][]int, len(r.Nodes))
	indegree := make([]int, len(r.Nodes))
	portCounts := make([]map[string]int, len(r.Nodes))
	for i, n := range r.Nodes {
		if n.Type == "start" {
			starts = append(starts, i)
		}
		portCounts[i] = map[string]int{}
	}
	switch len(starts) {
	case 0:
		v.add("missing_start", "error", "Добавьте начальный узел.", "/nodes", "", "")
	case 1:
	default:
		v.add("multiple_starts", "error", "Допустим только один начальный узел.", "/nodes", "", "")
	}
	for i, e := range r.Edges {
		if err := v.ctx.Err(); err != nil {
			return err
		}
		from, fromOK := v.nodeOrder[e.From]
		to, toOK := v.nodeOrder[e.To]
		if !fromOK || !toOK {
			v.edge(i, "dangling_edge", "Ребро ссылается на отсутствующий узел.", "")
		}
		if fromOK {
			portCounts[from][e.Port]++
			if !slices.Contains(nodePorts(r.Nodes[from]), e.Port) {
				v.edge(i, "invalid_port", "Порт не разрешён для этого типа узла.", "/port")
			}
			if portCounts[from][e.Port] > 1 {
				v.edge(i, "duplicate_exit", "У порта уже есть выходящее ребро.", "/port")
			}
		}
		if toOK && r.Nodes[to].Type == "start" {
			v.edge(i, "invalid_port", "В начальный узел нельзя направлять рёбра.", "/to")
		}
		if fromOK && toOK {
			out[from] = append(out[from], to)
			indegree[to]++
		}
	}
	for i, n := range r.Nodes {
		for _, port := range nodePorts(n) {
			if portCounts[i][port] == 0 {
				v.node(i, "missing_exit", "Соедините выход «"+port+"».", "")
			}
		}
	}
	if err := v.cycles(out); err != nil {
		return err
	}
	if len(starts) == 1 {
		if err := v.reachability(out, starts[0]); err != nil {
			return err
		}
		return v.pathDelay(out, indegree, starts[0])

	}
	return nil
}

func admitEnvelope(ctx context.Context, env Envelope, ruleID string) (int, error) {
	if env.FormatVersion != 1 || env.Rules == nil || len(env.Rules) > MaxRules {
		return -1, invalid("/"+Extension, "недопустимая структура расширения")
	}
	selected := -1
	ids := map[string]bool{}
	for i, r := range env.Rules {
		if err := ctx.Err(); err != nil {
			return -1, err
		}
		if ids[r.ID] {
			return -1, invalid(fmt.Sprintf("/%s/rules/%d/id", Extension, i), "повторяющийся ID")
		}
		ids[r.ID] = true
		if err := checkStructure(ctx, r); err != nil {
			return -1, at(fmt.Sprintf("/%s/rules/%d", Extension, i), err)
		}
		if r.ID == ruleID {
			selected = i
		}
	}
	data, err := jsonx.Marshal(env)
	if err != nil {
		return -1, err
	}
	if len(data) > MaxExtensionBytes {
		return -1, invalid("/"+Extension, "расширение превышает 1 МиБ")
	}
	if err := ctx.Err(); err != nil {
		return -1, err
	}
	if selected < 0 {
		return -1, ErrRuleNotFound
	}
	return selected, nil
}

func (v *validator) cycles(out [][]int) error {
	r := v.rule
	// Visit every component, including disconnected cycles.
	color := make([]uint8, len(r.Nodes))
	cycle := make([]bool, len(r.Nodes))
	stack := []int{}
	var visit func(int) error
	visit = func(i int) error {
		if err := v.ctx.Err(); err != nil {
			return err
		}
		color[i] = 1
		stack = append(stack, i)
		for _, j := range out[i] {
			switch color[j] {
			case 0:
				if err := visit(j); err != nil {
					return err
				}
			case 1:
				mark := false
				for _, k := range stack {
					if k == j {
						mark = true
					}
					if mark {
						cycle[k] = true
					}
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[i] = 2
		return nil
	}
	for i := range r.Nodes {
		if color[i] == 0 {
			if err := visit(i); err != nil {
				return err
			}
		}
	}
	for i, cyclic := range cycle {
		if cyclic {
			v.node(i, "cycle", "Граф содержит цикл.", "")
		}
	}
	return nil
}

func (v *validator) reachability(out [][]int, start int) error {
	r := v.rule
	reachable := make([]bool, len(r.Nodes))
	queue := []int{start}
	reachable[start] = true
	for len(queue) > 0 {
		if err := v.ctx.Err(); err != nil {
			return err
		}
		i := queue[0]
		queue = queue[1:]
		for _, j := range out[i] {
			if !reachable[j] {
				reachable[j] = true
				queue = append(queue, j)
			}
		}
	}
	for i, seen := range reachable {
		if !seen {
			v.node(i, "unreachable_node", "Узел недостижим из начала графа.", "")
		}
	}
	return nil
}

func (v *validator) pathDelay(out [][]int, indegree []int, start int) error {
	r := v.rule
	// Longest path in topological order is linear, even with merging branches.
	distance := make([]int, len(r.Nodes))
	for i := range distance {
		distance[i] = -1
	}
	distance[start] = 0
	queue := make([]int, 0, len(r.Nodes))
	for i, d := range indegree {
		if d == 0 {
			queue = append(queue, i)
		}
	}
	for len(queue) > 0 {
		if err := v.ctx.Err(); err != nil {
			return err
		}
		i := queue[0]
		queue = queue[1:]
		if distance[i] >= 0 && r.Nodes[i].Type == "delay" && *r.Nodes[i].DelayMs >= 0 && *r.Nodes[i].DelayMs <= MaxDelayMs {
			distance[i] += *r.Nodes[i].DelayMs
			if distance[i] > MaxDelayMs {
				v.node(i, "delay_limit", "Суммарная задержка пути превышает 30000 мс.", "/delayMs")
			}
		}
		for _, j := range out[i] {
			if distance[i] > distance[j] {
				distance[j] = distance[i]
			}
			indegree[j]--
			if indegree[j] == 0 {
				queue = append(queue, j)
			}
		}
	}
	return nil
}

func (v *validator) response(i int, n Node, operation map[string]any) error {
	response := n.Response
	if response.Status < 200 || response.Status > 599 {
		v.node(i, "invalid_response", "Статус должен быть от 200 до 599.", "/response/status")
	}
	if response.BodyJSON != nil || response.BodyFrom != nil {
		if response.Status == 204 || response.Status == 205 || response.Status == 304 || v.rule.Binding != nil && v.rule.Binding.Method == http.MethodHead {
			v.node(i, "body_not_allowed", "Для этого статуса или HEAD тело должно отсутствовать.", "/response/bodyJSON")
		}
		if response.BodyJSON != nil {
			if _, err := decodeBody(v.ctx, *response.BodyJSON); err != nil {
				if v.ctx.Err() != nil {
					return v.ctx.Err()
				}
				v.node(i, "invalid_response", "Тело должно содержать одно корректное JSON-значение без повторяющихся ключей и с глубиной до 64.", "/response/bodyJSON")
			}
		}
	}
	if operation != nil && response.Status >= 200 && response.Status <= 599 {
		responses, _ := operation["responses"].(map[string]any)
		_, exact := responses[strconv.Itoa(response.Status)]
		_, statusRange := responses[fmt.Sprintf("%dXX", response.Status/100)]
		_, fallback := responses["default"]
		if !exact && !statusRange && !fallback {
			v.add("undocumented_status", "warning", "Статус ответа не описан в операции API.", fmt.Sprintf("/nodes/%d/response/status", i), n.ID, "")
		}
	}
	return nil
}
