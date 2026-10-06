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
	selected := map[string]*backendmodel.DiagramPin{}
	known := map[string]bool{}
	unsupported := map[string]bool{}
	for _, n := range g.State.Nodes {
		known[n.ID] = true
		unsupported[n.ID] = unsupportedSourceConstruct(n.Kind, n.Attributes)
	}
	for _, e := range g.State.Edges {
		known[e.ID] = true
		unsupported[e.ID] = unsupportedSourceConstruct(e.Kind, e.Attributes)
	}
	for _, id := range p.Input.SourceScope {
		if !known[id] {
			return invalid("Selected source identity is absent from exact proposal")
		}
		if _, ok := selected[id]; ok {
			return invalid("Duplicate selected source identity")
		}
		selected[id] = nil
	}
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
	if p.Input.DiagramScope != nil {
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
			selected[selector.ID] = &pin
		}
		if d := v.Document.Interactions; d != nil {
			for _, b := range d.Branches {
				unsupported[b.ID] = true
			}
			for _, step := range d.Steps {
				if step.Kind != "request" && step.Kind != "response" || len(step.BranchPath) > 0 {
					unsupported[step.ID] = true
				}
			}
		}
		// These views can carry policies/guards but do not define executable HTTP
		// semantics. An explicit manual owner document remains authored intent.
		if d := v.Document.Lifecycle; d != nil {
			for _, transition := range d.Transitions {
				unsupported[transition.ID] = true
			}
		}
	}
	excluded := map[string]bool{}
	for _, id := range p.Input.ExcludedIDs {
		if _, ok := selected[id]; !ok || excluded[id] {
			return invalid("Excluded IDs must be unique selected identities")
		}
		excluded[id] = true
	}
	translations := map[string]Translation{}
	targets := map[string]Target{}
	for _, t := range p.Input.Targets {
		targets[t.Key] = t
	}
	for _, tr := range p.Input.Translations {
		if _, ok := selected[tr.SourceID]; !ok {
			return invalid("Translation is outside selected scope")
		}
		if _, ok := translations[tr.SourceID]; ok || excluded[tr.SourceID] {
			return invalid("Duplicate or excluded translation")
		}
		target, ok := targets[tr.TargetKey]
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
		translations[tr.SourceID] = tr
	}
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
		c := Coverage{SourceID: id, DiagramPin: selected[id], Status: "unsupported", Reason: "No supported explicit translation"}
		if excluded[id] {
			c.Status = "excluded"
			c.Reason = p.Input.Reason
		} else if tr, ok := translations[id]; ok && !unsupported[id] {
			t := targets[tr.TargetKey]
			c.Status = "translated"
			c.TargetKey = t.Key
			c.TargetOwnerRef = t.Pin
			c.Selector = tr.Selector
			c.Reason = tr.Reason
		}
		if c.Status == "unsupported" {
			p.CanApply = false
			p.Diagnostics = append(p.Diagnostics, "Unsupported selected identity: "+id)
		}
		p.Coverage = append(p.Coverage, c)
	}
	return nil
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
