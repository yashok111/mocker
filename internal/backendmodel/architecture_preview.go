package backendmodel

import "context"

type ArchitecturePreviewInput struct {
	GapScope  *ArchitectureGapScope `json:"gapScope,omitzero"`
	Document  DiagramDocument       `json:"document"`
	Level     string                `json:"level"`
	RootID    string                `json:"rootId"`
	Section   string                `json:"section"`
	SubjectID string                `json:"subjectId,omitempty"`
	Origin    string                `json:"origin"`
	Search    string                `json:"search"`
	Limit     int                   `json:"limit"`
	Cursor    string                `json:"cursor,omitempty"`
}
type ArchitecturePreviewPage struct {
	DocumentHash string                         `json:"documentHash"`
	TargetHash   string                         `json:"targetHash"`
	Projection   *ArchitectureProjectionContext `json:"projection"`
	Total        int                            `json:"total"`
	Items        []DiagramRow                   `json:"items"`
	GapSummary   *DiagramGapSummary             `json:"gapSummary"`
	NextCursor   string                         `json:"nextCursor"`
	Truncated    bool                           `json:"truncated"`
}

func (in *ArchitecturePreviewInput) UnmarshalJSON(b []byte) error {
	type plain ArchitecturePreviewInput
	*in = ArchitecturePreviewInput{}
	return strictAPIObject(b, []string{"document", "level", "rootId", "section", "origin", "search", "limit"}, []string{"subjectId", "cursor", "gapScope"}, (*plain)(in))
}

func (r *Repo) PreviewArchitecture(ctx context.Context, pid string, in ArchitecturePreviewInput) (*ArchitecturePreviewPage, error) {
	document, err := normalizeDiagram(in.Document)
	if err != nil {
		return nil, err
	}
	if document.Kind != "architecture" {
		return nil, diagramUnsupported()
	}
	if _, err := r.Get(ctx, pid); err != nil {
		return nil, err
	}
	if err := r.validateAnalysisLeaseTarget(ctx, pid, document.Target); err != nil {
		return nil, err
	}
	hash, err := requestDigest(document)
	if err != nil {
		return nil, err
	}
	// This internal identity scopes cursors only. The public response exposes a
	// documentHash, never a synthetic saved-diagram pin or publication receipt.
	v := &DiagramVersion{ProjectID: pid, Document: document, Pin: DiagramPin{ID: diagramIdentity("architecture-preview-v1", pid, hash), Version: 1, ContentHash: hash}}
	q := DiagramQueryInput{Pin: v.Pin, Level: in.Level, RootID: in.RootID, Section: in.Section, SubjectID: in.SubjectID, Origin: in.Origin, Search: in.Search, Limit: in.Limit, Cursor: in.Cursor, ResponseMode: "compact-v1"}
	q.GapScope = in.GapScope
	if err := q.Validate(); err != nil {
		return nil, err
	}
	key, err := requestDigest([]any{"architecture-preview-v1", pid, hash, in.Level, in.RootID, in.GapScope})
	if err != nil {
		return nil, err
	}
	p, err := r.architectureReads.load(ctx, key, func() (*architectureProjection, error) {
		graph, err := r.readArchitectureGraph(ctx, pid, document.Target)
		if err != nil {
			return nil, err
		}
		v.TargetHash = graph.Pins.TargetHash
		v.Gaps, err = resolveDiagramEvidence(ctx, graph, document, nil)
		if err != nil {
			return nil, err
		}
		navigationGaps, err := resolveArchitectureNavigation(ctx, r.db.R, pid, graph, document, nil)
		if err != nil {
			return nil, err
		}
		v.Gaps = append(v.Gaps, navigationGaps...)
		return projectArchitecture(ctx, v, graph, q)
	})
	if err != nil {
		return nil, err
	}
	v.TargetHash = p.targetHash
	page, err := architecturePage(ctx, v, p, q)
	if err != nil {
		return nil, err
	}
	return &ArchitecturePreviewPage{DocumentHash: hash, TargetHash: page.TargetHash, Projection: page.Projection, Total: page.Total, Items: page.Items, GapSummary: page.GapSummary, NextCursor: page.NextCursor, Truncated: page.Truncated}, nil
}
