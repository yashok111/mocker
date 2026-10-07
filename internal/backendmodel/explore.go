package backendmodel

import (
	"context"
	"encoding/base64"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
)

// Explore is a bounded navigation projection. Collections never become semantic
// nodes and containment is never synthesized into a dependency edge.
type ExploreInput struct {
	NodeIDs    []string          `json:"nodeIds,omitempty"`
	Target     BackendReadTarget `json:"target"`
	Mode       string            `json:"mode"`
	ScopeID    string            `json:"scopeId,omitempty"`
	Kind       string            `json:"kind,omitempty"`
	Group      string            `json:"group,omitempty"`
	Search     string            `json:"search,omitempty"`
	SelectedID string            `json:"selectedId,omitempty"`
	Limit      int               `json:"limit,omitzero"`
	Cursor     string            `json:"cursor,omitempty"`
}
type ExploreNode struct {
	Origin      string                    `json:"origin"`
	ID          string                    `json:"id"`
	Kind        string                    `json:"kind"`
	Name        string                    `json:"name"`
	ParentID    *string                   `json:"parentId"`
	Description string                    `json:"description"`
	Attributes  map[string]jsontext.Value `json:"attributes"`
	ChildCount  int                       `json:"childCount"`
}
type ExploreEdge struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	From  string `json:"from"`
	To    string `json:"to"`
	Label string `json:"label"`
}
type ExploreGroup struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Count int    `json:"count"`
	Basis string `json:"basis"`
}
type ExplorePage struct {
	BeforeTarget   *BackendReadTarget `json:"beforeTarget,omitzero"`
	BeforeNodes    []ExploreNode      `json:"beforeNodes,omitzero"`
	BeforeEdges    []ExploreEdge      `json:"beforeEdges,omitzero"`
	Changes        []ExploreChange    `json:"changes,omitzero"`
	Scope          *ExploreNode       `json:"scope,omitzero"`
	Ancestors      []ExploreNode      `json:"ancestors"`
	Target         BackendReadTarget  `json:"target"`
	TargetHash     string             `json:"targetHash"`
	SemanticHash   string             `json:"semanticHash"`
	CoverageStatus string             `json:"coverageStatus"`
	Nodes          []ExploreNode      `json:"nodes"`
	Edges          []ExploreEdge      `json:"edges"`
	Groups         []ExploreGroup     `json:"groups"`
	Counts         map[string]int     `json:"counts"`
	Total          int                `json:"total"`
	EdgeTotal      int                `json:"edgeTotal"`
	NextCursor     string             `json:"nextCursor"`
}

func (r *Repo) QueryExplore(ctx context.Context, pid string, in ExploreInput) (*ExplorePage, error) {
	if in.NodeIDs != nil {
		if len(in.NodeIDs) < 1 || len(in.NodeIDs) > 100 || in.Mode != "objects" {
			return nil, invalid("nodeIds", "Use 1–100 exact node IDs in objects mode")
		}
		for _, id := range in.NodeIDs {
			if !ValidID(id) {
				return nil, invalid("nodeIds", "Expected exact node IDs")
			}
		}
	}
	if err := in.Target.Validate(); err != nil {
		return nil, err
	}
	if in.Limit < 0 || in.Limit > 100 || len(in.Search) > 256 || len(in.Group) > 512 || len(in.Kind) > 80 || len(in.Cursor) > 4096 {
		return nil, invalid("query", "Invalid exploration bounds")
	}
	if in.Mode == "comparison" && in.Target.ChangeProposal == nil {
		return nil, invalid("target", "Comparison requires an exact proposal")
	}
	if in.Target.RevisionID != "" {
		revision, err := r.Revision(ctx, pid, in.Target.RevisionID)
		if err != nil {
			return nil, err
		}
		return r.exploreSource(ctx, pid, revision, in)
	}
	g, err := r.ResolveEffectiveGraph(ctx, pid, in.Target)
	if err != nil {
		return nil, err
	}
	if in.Mode == "comparison" {
		return projectExploreComparison(g, in)
	}
	return projectExplore(g, in)
}
func projectExplore(g *EffectiveGraphSnapshot, in ExploreInput) (*ExplorePage, error) {
	index := newExploreIndex(g)
	if err := index.validate(in); err != nil {
		return nil, err
	}
	scope := in
	scope.Cursor = ""
	scope.Limit = 0
	digest, err := requestDigest(struct {
		Hash  string
		Query ExploreInput
	}{g.Pins.TargetHash, scope})
	if err != nil {
		return nil, err
	}
	size := in.Limit
	if size == 0 {
		size = 40
	}
	limit, after, err := decodeExplorePage(size, in.Cursor, g.State.Revision.ProjectID, digest)
	if err != nil {
		return nil, err
	}
	out := index.page(in)
	matches := index.match(in, out.Counts)
	out.Total = len(matches)
	switch in.Mode {
	case "collections":
		fillExploreGroups(out, matches, limit, after, g.State.Revision.ProjectID, digest)
	case "neighborhood":
		if limit < 2 {
			return nil, invalid("limit", "Neighborhood needs room for its anchor and a neighbor")
		}
		index.neighborhood(out, matches, in.ScopeID, limit, after, digest)
	default:
		index.nodesPage(out, matches, limit, after, digest)
	}
	return out, nil
}
func compactExploreNode(n Node, childCount int) ExploreNode {
	attrs := map[string]jsontext.Value{}
	// Navigation has no need for SQL, contract bodies, file manifests or snippets.
	for _, key := range []string{"method", "path", "summary", "technology", "engine", "type", "dataType", "nullable", "stepKind", "analysisStatus", "exitStatus", "protocol", "schedule", "tags", "entryStepId", "operationId"} {
		if value, ok := n.Attributes[key]; ok && len(value) <= 2048 {
			attrs[key] = value
		}
	}

	var facets map[string]map[string]jsontext.Value
	facetRaw := n.Attributes["facets"]
	if len(facetRaw) == 0 {
		var relational map[string]jsontext.Value
		if json.Unmarshal(n.Attributes["relational"], &relational) == nil {
			facetRaw = relational["facets"]
		}
	}
	if len(facetRaw) > 0 && json.Unmarshal(facetRaw, &facets) == nil && len(facets) > 0 {
		keys := make([]string, 0, len(facets))
		for key := range facets {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		attrs["facetKeys"], _ = json.Marshal(keys)
		if len(keys) == 1 {
			attrs["facetKey"], _ = json.Marshal(keys[0])
			for _, key := range []string{"nativeType", "nullable", "typeFamily", "qualifiedName", "dialect", "analysisStatus"} {
				if value, ok := facets[keys[0]][key]; ok && len(value) < 2048 {
					attrs[key] = value
				}
			}
		}
	}
	summary, description, tags := exploreOperationMetadata(n)
	if summary != "" {
		attrs["summary"], _ = json.Marshal(summary)
	}
	if len(tags) > 0 {
		attrs["tags"], _ = json.Marshal(tags)
	}
	if len(description) > 600 {
		description = string([]rune(description)[:min(240, len([]rune(description)))])
	}
	name := summary
	if name == "" {
		name = n.Name
	}
	return ExploreNode{Origin: "source", ID: n.ID, Kind: n.Kind, Name: name, ParentID: n.ParentID, Description: description, Attributes: attrs, ChildCount: childCount}
}

func decodeExplorePage(limit int, cursor, pid, scope string) (int, string, error) {
	if limit < 1 || limit > 100 {
		return 0, "", invalid("limit", "Exploration limit must be between 1 and 100")
	}
	if cursor == "" {
		return limit, "", nil
	}
	if len(cursor) > 4096 {
		return 0, "", invalid("cursor", "Invalid exploration cursor")
	}
	b, err := base64.RawURLEncoding.Strict().DecodeString(cursor)
	if err != nil {
		return 0, "", invalid("cursor", "Invalid exploration cursor")
	}
	var c graphCursor
	if json.Unmarshal(b, &c, json.RejectUnknownMembers(true)) != nil || c.Resource != "explore" || c.ProjectID != pid || c.Scope != scope || c.After == "" || len(c.After) > 700 {
		return 0, "", invalid("cursor", "Cursor does not match exploration scope")
	}
	return limit, c.After, nil
}
