package backendmodel

import (
	"context"
	"slices"
)

type NamespacedArtifactQueryInput struct {
	Target             BackendReadTarget `json:"target"`
	TargetHash         string            `json:"targetHash"`
	Namespace          ArtifactNamespace `json:"namespace"`
	Artifact           ArtifactKey       `json:"artifact"`
	View               string            `json:"view"`
	EmbeddedContractID string            `json:"embeddedContractId,omitempty"`
	Limit              int               `json:"limit"`
	Cursor             string            `json:"cursor,omitempty"`
}
type NamespacedArtifactProjection struct {
	TargetHash     string                  `json:"targetHash"`
	Pin            NamespacedArtifactPin   `json:"pin"`
	Status         string                  `json:"status"`
	Reason         string                  `json:"reason"`
	APIBindings    []APIArtifactBinding    `json:"apiBindings"`
	EditorBindings []EditorBinding         `json:"editorBindings"`
	Projection     *ArtifactProjectionPage `json:"projection,omitzero"`
}

func (s *ArtifactService) QueryNamespaced(ctx context.Context, pid string, in NamespacedArtifactQueryInput) (*NamespacedArtifactProjection, error) {
	if err := in.Namespace.Validate(); err != nil {
		return nil, err
	}
	if err := in.Artifact.Validate(); err != nil {
		return nil, err
	}
	if !validHash(in.TargetHash) || in.Target.ImportCandidate != nil {
		return nil, invalid("target", "Exact immutable target hash required")
	}
	graph, err := s.repo.ResolveEffectiveGraph(ctx, pid, in.Target)
	if err != nil {
		return nil, err
	}
	if graph.Pins.TargetHash != in.TargetHash {
		return nil, diagramPinMismatch()
	}
	if graph.Pins.ArtifactContextV3 == nil {
		return nil, invalid("context", "Namespaced reader requires context-v3")
	}
	var group *ArtifactNamespaceGroup
	for _, g := range graph.Pins.ArtifactContextV3.Groups {
		if g.Namespace == in.Namespace {
			group = &g
			break
		}
	}
	if group == nil {
		return nil, notFound()
	}
	at := slices.IndexFunc(group.Pins, func(p ArtifactPin) bool { return p.Kind == in.Artifact.Kind && p.ID == in.Artifact.ID })
	if at < 0 {
		return nil, notFound()
	}
	pin := NamespacedArtifactPin{Namespace: group.Namespace, Pin: group.Pins[at]}
	query := ArtifactQueryInput{RevisionID: graph.State.Revision.ID, Artifact: in.Artifact, View: in.View, EmbeddedContractID: in.EmbeddedContractID, Limit: in.Limit, Cursor: in.Cursor}
	if err := query.Validate(); err != nil {
		return nil, err
	}
	out := &NamespacedArtifactProjection{TargetHash: in.TargetHash, Pin: pin, Status: "foreign_unresolved", Reason: "Imported foreign reference requires an explicit exact local mapping", APIBindings: []APIArtifactBinding{}, EditorBindings: []EditorBinding{}}
	for _, b := range group.APIBindings {
		if b.Ref.Kind == pin.Pin.Kind && b.Ref.ArtifactID == pin.Pin.ID {
			out.APIBindings = append(out.APIBindings, b)
		}
	}
	for _, b := range group.EditorBindings {
		if b.ArtifactKind == pin.Pin.Kind && b.ArtifactID == pin.Pin.ID {
			out.EditorBindings = append(out.EditorBindings, b)
		}
	}
	if group.Namespace.Scope == "foreign" {
		return out, nil
	}
	installation, err := s.repo.InstallationID(ctx)
	if err != nil {
		return nil, err
	}
	request := NewEditorArtifactRequest(ctx, s.api, s.scenarios)
	if _, err := request.ResolveNamespacedPin(installation, pin); err != nil {
		return nil, err
	}
	local := namespacedLocalProjection(graph, group)
	request.effective = local
	out.Projection, err = request.Project(&local.State, *local.Pins.ArtifactContext, query)
	if err != nil {
		return nil, err
	}
	out.Status = "local_resolved"
	out.Reason = "Exact local namespace and immutable owner snapshot verified"
	return out, nil
}
func (in *NamespacedArtifactQueryInput) UnmarshalJSON(raw []byte) error {
	type plain NamespacedArtifactQueryInput
	var value plain
	if err := strictAPIObject(raw, []string{"target", "targetHash", "namespace", "artifact", "view", "limit"}, []string{"embeddedContractId", "cursor"}, &value); err != nil {
		return err
	}
	*in = NamespacedArtifactQueryInput(value)
	return nil
}
