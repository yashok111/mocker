package backendmodel

import (
	"context"
	"database/sql"
)

func (r *Repo) portableDiagramContext(ctx context.Context, tx *sql.Tx) context.Context {
	request := r.portableArtifactRequest(ctx, tx)
	return (&ArtifactService{repo: r, api: request.api, scenarios: request.scenario, diagramTx: tx}).DiagramContext(ctx)
}
func namespacedLocalProjection(g *EffectiveGraphSnapshot, group *ArtifactNamespaceGroup) *EffectiveGraphSnapshot {
	graph := *g
	graph.State = g.State
	graph.State.Revision = g.State.Revision
	graph.State.Revision.ArtifactPins = group.Pins
	graph.Pins = g.Pins
	graph.Pins.ArtifactPins = group.Pins
	graph.Pins.ArtifactContextV3 = nil
	c := &ArtifactContext{SourceContentHash: g.Pins.ArtifactContextV3.SourceContentHash, SourceSemanticHash: g.Pins.ArtifactContextV3.SourceSemanticHash, APIBindings: group.APIBindings, EditorBindings: group.EditorBindings}
	if !ArtifactContextUsesV1(group.Pins, *c) {
		c.DocumentVersion = EditorArtifactDocumentVersion
	}
	graph.Pins.ArtifactContext = c
	graph.State.ArtifactContext = c
	graph.State.ArtifactContextV3 = nil
	graph.State.APIArtifactContext = legacyArtifactContext(c)
	return &graph
}
func (r *Repo) resolvePortableDiagramRows(ctx context.Context, tx *sql.Tx, g *EffectiveGraphSnapshot, d *DiagramDocument) error {
	installation, err := installationID(ctx, tx)
	if err != nil {
		return err
	}
	return visitPortableDiagramRefs(d, func(ref *DiagramRef) error {
		if ref.Kind != "namespaced_artifact" {
			return nil
		}
		group, err := namespacedDiagramGroup(g, *ref)
		if err != nil {
			return err
		}
		if group.Namespace.Scope == "foreign" {
			return nil
		}
		locator := ref.NamespacedLocator.Locator
		scoped := NamespacedArtifactPin{Namespace: group.Namespace, Pin: locator.Pin}
		request := r.portableArtifactRequest(ctx, tx)
		if _, err := request.ResolveNamespacedPin(installation, scoped); err != nil {
			return err
		}
		graph := namespacedLocalProjection(g, group)
		request.effective = graph
		in := ArtifactQueryInput{RevisionID: graph.State.Revision.ID, Artifact: ArtifactKey{Kind: locator.Pin.Kind, ID: locator.Pin.ID}, View: locator.View, Limit: 100}
		if locator.Embedded != nil {
			in.EmbeddedContractID = locator.Embedded.ContractID
		}
		owner, err := requestDigest(locator.Owner)
		if err != nil {
			return err
		}
		for {
			page, err := request.Project(&graph.State, *graph.Pins.ArtifactContext, in)
			if err != nil {
				return err
			}
			for _, row := range page.Items {
				candidate, err := requestDigest(row.Locator.Owner)
				if err != nil {
					return err
				}
				if candidate == owner && row.Locator.View == locator.View && row.Locator.Pin == locator.Pin {
					ref.RowID = row.ID
					ref.NamespacedLocator.Locator = row.Locator
					return nil
				}
			}
			if page.NextCursor == "" {
				return invalid("mapping", "Exact mapped artifact selector is unavailable")
			}
			in.Cursor = page.NextCursor
		}
	})
}
func visitPortableDiagramRefs(d *DiagramDocument, visit func(*DiagramRef) error) error {
	list := func(refs []DiagramRef) error {
		for i := range refs {
			if err := visit(&refs[i]); err != nil {
				return err
			}
		}
		return nil
	}
	for i := range d.Payload.Elements {
		if err := list(d.Payload.Elements[i].Refs); err != nil {
			return err
		}
	}
	for i := range d.Payload.Links {
		if err := list(d.Payload.Links[i].Refs); err != nil {
			return err
		}
	}
	if p := d.Interactions; p != nil {
		if err := list(p.ScopeRefs); err != nil {
			return err
		}
		for i := range p.Participants {
			if err := list(p.Participants[i].Refs); err != nil {
				return err
			}
		}
		for i := range p.Steps {
			if err := list(p.Steps[i].Refs); err != nil {
				return err
			}
		}
	}
	if p := d.Lifecycle; p != nil {
		if err := visit(&p.Entity); err != nil {
			return err
		}
		if err := list(p.StateFields); err != nil {
			return err
		}
		for i := range p.States {
			if err := list(p.States[i].Refs); err != nil {
				return err
			}
		}
		for i := range p.Transitions {
			t := &p.Transitions[i]
			for _, refs := range [][]DiagramRef{t.Refs, t.Triggers, t.Writes, t.Events} {
				if err := list(refs); err != nil {
					return err
				}
			}
		}
		for i := range p.Rules {
			if err := visit(&p.Rules[i].Trigger); err != nil {
				return err
			}
		}
	}
	if p := d.BusinessMap; p != nil {
		for i := range p.Elements {
			if err := list(p.Elements[i].Refs); err != nil {
				return err
			}
		}
		for i := range p.Links {
			if err := list(p.Links[i].Refs); err != nil {
				return err
			}
		}
	}
	return nil
}
