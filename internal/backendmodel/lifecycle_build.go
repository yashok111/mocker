package backendmodel

import (
	"context"
	"encoding/json/v2"
	"slices"
)

type LifecycleArtifactSelection struct {
	Locator ArtifactProjectionLocator `json:"locator"`
	RowID   string                    `json:"rowId"`
}
type DiagramLifecycleBuildInput struct {
	Target                BackendReadTarget          `json:"target"`
	StateDiagram          LifecycleArtifactSelection `json:"stateDiagram"`
	Entity                DiagramRef                 `json:"entity"`
	StateFields           []DiagramRef               `json:"stateFields"`
	CompoundMappingReason string                     `json:"compoundMappingReason,omitempty"`
}
type DiagramLifecycleCandidate struct {
	Document   DiagramDocument `json:"document"`
	TargetHash string          `json:"targetHash"`
	Gaps       []DiagramGap    `json:"gaps"`
}

func (v *LifecycleArtifactSelection) UnmarshalJSON(b []byte) error {
	type plain LifecycleArtifactSelection
	*v = LifecycleArtifactSelection{}
	return strictAPIObject(b, []string{"locator", "rowId"}, nil, (*plain)(v))
}
func (v *DiagramLifecycleBuildInput) UnmarshalJSON(b []byte) error {
	type plain DiagramLifecycleBuildInput
	*v = DiagramLifecycleBuildInput{}
	if err := strictAPIObject(b, []string{"target", "stateDiagram", "entity", "stateFields"}, []string{"compoundMappingReason"}, (*plain)(v)); err != nil {
		return err
	}
	return v.Validate()
}
func (in DiagramLifecycleBuildInput) Validate() error {
	if err := validateDiagramTarget(in.Target); err != nil {
		return err
	}
	if err := validateDiagramRef(DiagramRef{Kind: "artifact", Locator: &in.StateDiagram.Locator, RowID: in.StateDiagram.RowID}); err != nil {
		return err
	}
	if in.StateDiagram.Locator.View != "states" || in.StateDiagram.Locator.Owner.DiagramID == "" || in.StateDiagram.Locator.Owner.StateID != "" || in.StateDiagram.Locator.Owner.TransitionID != "" {
		return lifecycleSemanticRefError("stateDiagram")
	}
	d := DiagramDocument{Format: DiagramDocumentVersion, Kind: "lifecycle", Target: in.Target, Lifecycle: &LifecyclePayload{Entity: in.Entity, StateFields: in.StateFields, CompoundMappingReason: in.CompoundMappingReason, States: []LifecycleState{}, Transitions: []LifecycleTransition{}, Rules: []LifecycleRule{}, Coverage: "partial", CoverageOrigin: DiagramOrigin{Kind: "authored", Reason: "pinned state diagram"}}}
	return d.Validate()
}
func (r *Repo) BuildLifecycle(ctx context.Context, pid string, in DiagramLifecycleBuildInput) (*DiagramLifecycleCandidate, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	g, err := r.ResolveEffectiveGraph(ctx, pid, in.Target)
	if err != nil {
		return nil, err
	}
	if len(g.State.Nodes)+len(g.State.Edges)+len(g.State.Evidence) > 250000 {
		return nil, &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Lifecycle resolution exceeds visited-object budget"}
	}
	resolver := newDiagramArtifactResolver(ctx, g)
	if resolver.request == nil || g.Pins.ArtifactContext == nil || !slices.Contains(g.Pins.ArtifactPins, in.StateDiagram.Locator.Pin) {
		return nil, invalid("stateDiagram", "Exact owned artifact context required")
	}
	loc := in.StateDiagram.Locator
	q := ArtifactQueryInput{RevisionID: g.State.Revision.ID, Artifact: ArtifactKey{Kind: loc.Pin.Kind, ID: loc.Pin.ID}, View: "states", Limit: 100}
	if loc.Embedded != nil {
		q.EmbeddedContractID = loc.Embedded.ContractID
	}
	rows, err := readLifecycleStateRows(resolver, g, q)
	if err != nil {
		return nil, err
	}
	d, err := buildLifecycleRows(ctx, in, rows)
	if err != nil {
		return nil, err
	}
	bindLifecycleTriggers(g, d, rows)
	d, err = normalizeDiagram(d)
	if err != nil {
		return nil, err
	}
	gaps, err := resolveDiagramEvidence(ctx, g, d, nil)
	if err != nil {
		return nil, err
	}
	return &DiagramLifecycleCandidate{Document: d, TargetHash: g.Pins.TargetHash, Gaps: gaps}, nil
}

// readLifecycleStateRows pages through the pinned state view; an incomplete
// last page refuses, since a lifecycle built from it would miss states.
func readLifecycleStateRows(resolver *diagramArtifactResolver, g *EffectiveGraphSnapshot, q ArtifactQueryInput) ([]ArtifactProjectionItem, error) {
	rows := []ArtifactProjectionItem{}
	for {
		page, err := resolver.request.Project(&g.State, *g.Pins.ArtifactContext, q)
		if err != nil {
			return nil, err
		}
		if len(rows)+len(page.Items) > 250000 {
			return nil, lifecycleSemanticRefError("projection budget")
		}
		rows = append(rows, page.Items...)
		if page.NextCursor == "" {
			if !page.Complete {
				return nil, lifecycleSemanticRefError("incomplete pinned states")
			}
			return rows, nil
		}
		q.Cursor = page.NextCursor
	}
}

// bindLifecycleTriggers attaches each transition's operation triggers.
// Explicit editor bindings are the only source-operation mappings we carry.
func bindLifecycleTriggers(g *EffectiveGraphSnapshot, d DiagramDocument, rows []ArtifactProjectionItem) {
	nodes := map[string]Node{}
	for _, n := range g.State.Nodes {
		nodes[n.ID] = n
	}
	for i := range d.Lifecycle.Transitions {
		tr := &d.Lifecycle.Transitions[i]
		rowID := tr.Refs[0].RowID
		for _, row := range rows {
			if row.ID != rowID {
				continue
			}
			for _, id := range row.SourceNodeIDs {
				if slices.Contains([]string{"http_operation", "handler", "symbol", "consumer", "job"}, nodes[id].Kind) {
					tr.Triggers = append(tr.Triggers, DiagramRef{Kind: "record", RecordType: "node", ID: id})
				}
			}
		}
	}
}

// selectLifecycleDiagramRow finds the exact pinned state-diagram row the
// build was asked for, matching row ID and full locator.
func selectLifecycleDiagramRow(rows []ArtifactProjectionItem, in DiagramLifecycleBuildInput) *ArtifactProjectionItem {
	wanted, _ := requestDigest(in.StateDiagram.Locator)
	for i := range rows {
		row := &rows[i]
		hash, _ := requestDigest(row.Locator)
		if row.ID == in.StateDiagram.RowID && hash == wanted && row.Kind == "state_diagram" && row.Data.StateDiagram != nil {
			return row
		}
	}
	return nil
}

func buildLifecycleRows(ctx context.Context, in DiagramLifecycleBuildInput, rows []ArtifactProjectionItem) (DiagramDocument, error) {
	origin := DiagramOrigin{Kind: "authored", Reason: "pinned state diagram"}
	p := &LifecyclePayload{Entity: in.Entity, StateFields: slices.Clone(in.StateFields), CompoundMappingReason: in.CompoundMappingReason, States: []LifecycleState{}, Transitions: []LifecycleTransition{}, Rules: []LifecycleRule{}, Coverage: "partial", CoverageOrigin: origin}
	d := DiagramDocument{Format: DiagramDocumentVersion, Kind: "lifecycle", Target: in.Target, Lifecycle: p}
	loc := in.StateDiagram.Locator
	selected := selectLifecycleDiagramRow(rows, in)
	if selected == nil {
		return d, invalid("stateDiagram", "Exact pinned diagram row not found")
	}
	embedded := ""
	if loc.Embedded != nil {
		embedded = loc.Embedded.ContractID
	}
	identity := func(kind, id string) string {
		return diagramIdentity("lifecycle-artifact-v1", loc.Pin.Kind, loc.Pin.ID, embedded, loc.Owner.DiagramID, kind, id)
	}
	for _, row := range rows {
		if err := ctx.Err(); err != nil {
			return d, err
		}
		if row.Locator.Pin != loc.Pin || row.Locator.Owner.DiagramID != loc.Owner.DiagramID {
			continue
		}
		a, _ := requestDigest(row.Locator.Embedded)
		b, _ := requestDigest(loc.Embedded)
		if a != b {
			continue
		}
		ref := DiagramRef{Kind: "artifact", Locator: new(row.Locator), RowID: row.ID}
		if row.Kind == "state" && row.Data.State != nil {
			s := row.Data.State
			state := LifecycleState{ID: identity("state", s.ID), Label: s.Name, Origin: origin, Refs: []DiagramRef{ref}, Initial: s.ID == selected.Data.StateDiagram.InitialStateID, Terminal: s.Terminal}
			if s.Value != nil {
				raw, err := json.Marshal(*s.Value)
				if err != nil {
					return d, err
				}
				state.Value = &LifecycleValue{JSON: string(raw)}
			}
			p.States = append(p.States, state)
		}
		if row.Kind == "state_transition" && row.Data.StateTransition != nil {
			tr := row.Data.StateTransition
			guard := LifecycleGuard{Kind: "none"}
			if tr.Guard != nil {
				raw, err := json.Marshal(tr.Guard)
				if err != nil {
					return d, err
				}
				guard = LifecycleGuard{Kind: "opaque", Text: string(raw)}
			}
			p.Transitions = append(p.Transitions, LifecycleTransition{ID: identity("transition", tr.ID), Label: tr.Name, Origin: origin, Refs: []DiagramRef{ref}, From: identity("state", tr.From), To: identity("state", tr.To), Triggers: []DiagramRef{}, Writes: []DiagramRef{}, Events: []DiagramRef{}, Guard: guard})
		}
	}
	return normalizeDiagram(d)
}
