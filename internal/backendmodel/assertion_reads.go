package backendmodel

import "context"

type BackendAssertionsPage struct {
	DocumentVersion   string                `json:"documentVersion"`
	ViewSchemaVersion string                `json:"viewSchemaVersion"`
	Target            BackendReadTarget     `json:"target"`
	Pins              EffectiveGraphPins    `json:"pins"`
	Basis             string                `json:"basis"`
	Items             []SourceAssertionItem `json:"items"`
	NextCursor        string                `json:"nextCursor"`
}

func (r *Repo) ReadAssertions(ctx context.Context, pid string, target BackendReadTarget, in SourceAssertionsQuery) (*BackendAssertionsPage, error) {
	graph, err := r.ResolveEffectiveGraph(ctx, pid, target)
	if err != nil {
		return nil, err
	}
	page, err := QuerySourceAssertions(ctx, graph.Source, SourceReadPin{ProjectID: pid, BaseRevisionID: graph.Pins.BaseRevisionID, TargetHash: graph.Pins.TargetHash, EffectiveSemanticHash: graph.Pins.EffectiveSemanticHash, SourceVectorHash: graph.Pins.SourceVectorHash}, in)
	if err != nil {
		return nil, err
	}
	basis := "source"
	if target.ChangeProposal != nil || target.Proposal != nil {
		basis = "baseline"
	}
	if target.ImportCandidate != nil {
		basis = "candidate"
	}
	return &BackendAssertionsPage{DocumentVersion: page.DocumentVersion, ViewSchemaVersion: graph.Pins.ViewSchemaVersion, Target: graph.Target, Pins: graph.Pins, Basis: basis, Items: page.Items, NextCursor: page.NextCursor}, nil
}
