package backendmodel

import (
	"context"
	"encoding/json/v2"
	"slices"
)

// NamespacedDiagramLocator keeps the entire locator behind an installation
// boundary. Its nested legacy pin is never an independent local lookup address.
type NamespacedDiagramLocator struct {
	Namespace ArtifactNamespace         `json:"namespace"`
	Locator   ArtifactProjectionLocator `json:"locator"`
}

func (v *NamespacedDiagramLocator) UnmarshalJSON(raw []byte) error {
	type plain NamespacedDiagramLocator
	var out plain
	if err := strictAPIObject(raw, []string{"namespace", "locator"}, nil, &out); err != nil {
		return err
	}
	if err := (NamespacedArtifactPin{Namespace: out.Namespace, Pin: out.Locator.Pin}).Validate(); err != nil {
		return err
	}
	// Reuse the complete owner locator validator without any owner lookup.
	b, err := json.Marshal(out.Locator)
	if err != nil {
		return err
	}
	var locator ArtifactProjectionLocator
	if err := json.Unmarshal(b, &locator); err != nil {
		return err
	}
	*v = NamespacedDiagramLocator(out)
	return nil
}
func namespacedDiagramGroup(g *EffectiveGraphSnapshot, ref DiagramRef) (*ArtifactNamespaceGroup, error) {
	if ref.NamespacedLocator == nil || g.Pins.ArtifactContextV3 == nil {
		return nil, invalid("ref", "Namespaced locator requires a v3 target")
	}
	loc := ref.NamespacedLocator
	for _, group := range g.Pins.ArtifactContextV3.Groups {
		if group.Namespace == loc.Namespace && slices.Contains(group.Pins, loc.Locator.Pin) {
			return &group, nil
		}
	}
	return nil, invalid("ref", "Artifact namespace/pin is outside the exact target")
}
func (r *diagramArtifactResolver) resolveNamespaced(ref DiagramRef) (bool, error) {
	group, err := namespacedDiagramGroup(r.graph, ref)
	if err != nil {
		return false, err
	}
	if group.Namespace.Scope == "foreign" {
		return false, nil
	}
	if r.installationID == "" {
		return false, invalid("namespace", "Local installation identity is unavailable")
	}
	loc := ref.NamespacedLocator
	scoped := NamespacedArtifactPin{Namespace: loc.Namespace, Pin: loc.Locator.Pin}
	if _, err := scoped.LocalPin(r.installationID); err != nil {
		return false, err
	}
	if r.request == nil {
		return false, editorReaderUnavailable("Namespaced")
	}
	if _, err := r.request.ResolveNamespacedPin(r.installationID, scoped); err != nil {
		return false, err
	}
	// A local group is adapted only after the namespace and immutable snapshot gate.
	graph := namespacedLocalProjection(r.graph, group)
	request := NewEditorArtifactRequest(r.request.ctx, r.request.api, r.request.scenario)
	request.effective = graph
	return resolveDiagramArtifact(graph, request, DiagramRef{Kind: "artifact", Locator: &loc.Locator, RowID: ref.RowID})
}
func (s *ArtifactService) diagramInstallationID(ctx context.Context) string {
	id, err := s.repo.InstallationID(ctx)
	if err != nil {
		return ""
	}
	return id
}
