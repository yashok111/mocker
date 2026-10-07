package backendmaterialize

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func (s *Service) coverageTx(ctx context.Context, tx *sql.Tx, pid string, g *backendmodel.EffectiveGraphSnapshot, p *Preview) error {
	c := coverage{p: p, selected: map[string]*backendmodel.DiagramPin{}, unsupported: map[string]bool{}, excluded: map[string]bool{}, translations: map[string]Translation{}, targets: map[string]Target{}}
	if err := c.selectSources(g); err != nil {
		return err
	}
	if err := s.selectDiagram(ctx, tx, pid, &c); err != nil {
		return err
	}
	for _, id := range p.Input.ExcludedIDs {
		if _, ok := c.selected[id]; !ok || c.excluded[id] {
			return invalid("Excluded IDs must be unique selected identities")
		}
		c.excluded[id] = true
	}
	for _, t := range p.Input.Targets {
		c.targets[t.Key] = t
	}
	for _, tr := range p.Input.Translations {
		if err := c.addTranslation(tr); err != nil {
			return err
		}
	}
	c.report()
	return nil
}

// coverage decides, per selected source identity, whether the plan
// translates it, excludes it, or leaves it unsupported.
type coverage struct {
	p            *Preview
	selected     map[string]*backendmodel.DiagramPin
	unsupported  map[string]bool
	excluded     map[string]bool
	translations map[string]Translation
	targets      map[string]Target
}

// selectSources admits the caller's explicit source scope, each once and
// each present in the exact proposal graph.
func (c *coverage) selectSources(g *backendmodel.EffectiveGraphSnapshot) error {
	known := map[string]bool{}
	for _, n := range g.State.Nodes {
		known[n.ID] = true
		c.unsupported[n.ID] = unsupportedSourceConstruct(n.Kind, n.Attributes)
	}
	for _, e := range g.State.Edges {
		known[e.ID] = true
		c.unsupported[e.ID] = unsupportedSourceConstruct(e.Kind, e.Attributes)
	}
	for _, id := range c.p.Input.SourceScope {
		if !known[id] {
			return invalid("Selected source identity is absent from exact proposal")
		}
		if _, ok := c.selected[id]; ok {
			return invalid("Duplicate selected source identity")
		}
		c.selected[id] = nil
	}
	return nil
}

// selectDiagram adds the diagram scope's selectors to the selection; a saved
// view must show exactly the scoped diagram.
func (s *Service) selectDiagram(ctx context.Context, tx *sql.Tx, pid string, c *coverage) error {
	p := c.p
	if p.Input.DiagramView != nil {
		if p.Input.DiagramScope == nil {
			return invalid("Saved diagram view requires an explicit DiagramScope")
		}
		pin := p.Input.DiagramView
		view, err := s.models.GetDiagramViewTx(ctx, tx, pid, pin.ID, pin.Version)
		if err != nil {
			return err
		}
		if view.State.Diagram != p.Input.DiagramScope.Pin {
			return conflict("Saved view and selected diagram pin differ")
		}
		p.DiagramView = view
	}
	if p.Input.DiagramScope == nil {
		return nil
	}
	scope, err := s.models.ResolveDiagramScopeTx(ctx, tx, pid, *p.Input.DiagramScope)
	if err != nil {
		return err
	}
	if scope.TargetHash != p.Input.TargetHash || scope.Truncated {
		return invalid("Diagram scope differs from target or is truncated")
	}
	p.DiagramScope = scope
	v, err := s.models.GetDiagramTx(ctx, tx, pid, scope.Pin)
	if err != nil {
		return err
	}
	for _, selector := range scope.Selectors {
		pin := scope.Pin
		c.selected[selector.ID] = &pin
	}
	c.markUnsupportedDiagram(v)
	return nil
}

// markUnsupportedDiagram marks the diagram constructs this profile cannot
// translate: branches and non-request/response or branched steps, and
// lifecycle transitions.
func (c *coverage) markUnsupportedDiagram(v *backendmodel.DiagramVersion) {
	if d := v.Document.Interactions; d != nil {
		for _, b := range d.Branches {
			c.unsupported[b.ID] = true
		}
		for _, step := range d.Steps {
			if step.Kind != "request" && step.Kind != "response" || len(step.BranchPath) > 0 {
				c.unsupported[step.ID] = true
			}
		}
	}
	// These views can carry policies/guards but do not define executable HTTP
	// semantics. An explicit manual owner document remains authored intent.
	if d := v.Document.Lifecycle; d != nil {
		for _, transition := range d.Transitions {
			c.unsupported[transition.ID] = true
		}
	}
}

// addTranslation admits one explicit translation of a selected, unexcluded
// identity into an object its target's output actually contains.
func (c *coverage) addTranslation(tr Translation) error {
	if _, ok := c.selected[tr.SourceID]; !ok {
		return invalid("Translation is outside selected scope")
	}
	if _, ok := c.translations[tr.SourceID]; ok || c.excluded[tr.SourceID] {
		return invalid("Duplicate or excluded translation")
	}
	target, ok := c.targets[tr.TargetKey]
	if !ok || strings.TrimSpace(tr.Reason) == "" {
		return invalid("Translation requires an explicit target and reason")
	}
	var raw []byte
	if target.Kind == "api_design" {
		raw = []byte(target.Commands[0].APIDocument)
	} else {
		var err error
		raw, err = json.Marshal(target.Commands[0].Scenario)
		if err != nil {
			return err
		}
	}
	if !hasPointer(raw, tr.Selector) {
		return invalid("Translation selector does not identify an output object")
	}
	c.translations[tr.SourceID] = tr
	return nil
}

// report appends one coverage row per selected identity; any unsupported
// row makes the plan inapplicable.
func (c *coverage) report() {
	p := c.p
	// Preserve explicit caller scope order; diagram selection is already canonical.
	ids := append([]string{}, p.Input.SourceScope...)
	if p.DiagramScope != nil {
		for _, selector := range p.DiagramScope.Selectors {
			if !contains(ids, selector.ID) {
				ids = append(ids, selector.ID)
			}
		}
	}
	for _, id := range ids {
		row := Coverage{SourceID: id, DiagramPin: c.selected[id], Status: "unsupported", Reason: "No supported explicit translation"}
		if c.excluded[id] {
			row.Status = "excluded"
			row.Reason = p.Input.Reason
		} else if tr, ok := c.translations[id]; ok && !c.unsupported[id] {
			t := c.targets[tr.TargetKey]
			row.Status = "translated"
			row.TargetKey = t.Key
			row.TargetOwnerRef = t.Pin
			row.Selector = tr.Selector
			row.Reason = tr.Reason
		}
		if row.Status == "unsupported" {
			p.CanApply = false
			p.Diagnostics = append(p.Diagnostics, "Unsupported selected identity: "+id)
		}
		p.Coverage = append(p.Coverage, row)
	}
}
func contains(values []string, value string) bool {
	for _, v := range values {
		if v == value {
			return true
		}
	}
	return false
}
func hasPointer(raw []byte, pointer string) bool {
	if pointer != "" && !strings.HasPrefix(pointer, "/") {
		return false
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	if pointer == "" {
		return true
	}
	for _, segment := range strings.Split(pointer[1:], "/") {
		key := strings.ReplaceAll(strings.ReplaceAll(segment, "~1", "/"), "~0", "~")
		switch x := v.(type) {
		case map[string]any:
			var ok bool
			v, ok = x[key]
			if !ok {
				return false
			}
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(x) || strconv.Itoa(i) != key {
				return false
			}
			v = x[i]
		default:
			return false
		}
	}
	return true
}

func unsupportedSourceConstruct(kind string, attributes map[string]jsontext.Value) bool {
	if slices.Contains([]string{"query", "producer", "consumer", "job", "event_handler", "event_route", "message", "channel", "subscription", "delivery"}, kind) {
		return true
	}
	var step string
	if raw, ok := attributes["stepKind"]; ok {
		if json.Unmarshal(raw, &step) != nil {
			return true
		}
		return slices.Contains([]string{"loop", "parallel", "join", "opaque", "query", "transform"}, step)
	}
	return false
}
