package backendmodel

import (
	"cmp"
	"crypto/sha256"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"slices"
)

// APIArtifactSourceProjection includes the full persisted source context while
// deliberately leaving revision/CAS metadata and manual associations outside.
type APIArtifactSourceProjection struct {
	Domain             string           `json:"domain"`
	Nodes              []Node           `json:"nodes"`
	Edges              []Edge           `json:"edges"`
	Evidence           []Evidence       `json:"evidence"`
	Sources            []SourceSnapshot `json:"sources"`
	SourceSnapshotIDs  []string         `json:"sourceSnapshotIds"`
	Snapshots          []SourceSnapshot `json:"snapshots"`
	Inventory          []InventoryItem  `json:"inventory"`
	Coverage           Coverage         `json:"coverage"`
	ReconciliationGaps []string         `json:"reconciliationGaps"`
	StaleCounts        StaleCounts      `json:"staleCounts"`
}

func canonicalAPIJSON(v any) ([]byte, error) {
	raw, err := json.Marshal(v, json.Deterministic(true))
	if err != nil {
		return nil, err
	}
	value := jsontext.Value(raw)
	if err = value.Canonicalize(); err != nil {
		return nil, err
	}
	return value, nil
}
func hashAPIJSON(v any) (string, error) {
	raw, err := canonicalAPIJSON(v)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}
func sortedAPIStrings(s []string) []string { out := slices.Clone(s); slices.Sort(out); return out }
func BuildAPIArtifactSourceProjection(state RevisionState, coverage RevisionCoverage) (APIArtifactSourceProjection, error) {
	// Copy every nested record before sorting so callers retain their original bytes.
	input := APIArtifactSourceProjection{Domain: "backend-api-pins-source-content-v1", Nodes: state.Nodes, Edges: state.Edges, Evidence: state.Evidence, Sources: state.Sources, SourceSnapshotIDs: state.Revision.SourceSnapshotIDs, Snapshots: coverage.Snapshots, Inventory: coverage.Inventory, Coverage: coverage.Coverage, ReconciliationGaps: coverage.ReconciliationGaps, StaleCounts: coverage.StaleCounts}
	raw, err := json.Marshal(input)
	if err != nil {
		return input, err
	}
	var out APIArtifactSourceProjection
	if err = json.Unmarshal(raw, &out); err != nil {
		return out, err
	}
	slices.SortFunc(out.Nodes, func(a, b Node) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(out.Edges, func(a, b Edge) int { return cmp.Compare(a.ID, b.ID) })
	slices.SortFunc(out.Evidence, func(a, b Evidence) int { return cmp.Compare(a.ID, b.ID) })
	for i := range out.Nodes {
		out.Nodes[i].EvidenceIDs = sortedAPIStrings(out.Nodes[i].EvidenceIDs)
		if out.Nodes[i].Freshness != nil {
			out.Nodes[i].Freshness.Reasons = sortedAPIStrings(out.Nodes[i].Freshness.Reasons)
		}
	}
	for i := range out.Edges {
		out.Edges[i].EvidenceIDs = sortedAPIStrings(out.Edges[i].EvidenceIDs)
		if out.Edges[i].Freshness != nil {
			out.Edges[i].Freshness.Reasons = sortedAPIStrings(out.Edges[i].Freshness.Reasons)
		}
	}
	for i := range out.Evidence {
		if out.Evidence[i].Freshness != nil {
			out.Evidence[i].Freshness.Reasons = sortedAPIStrings(out.Evidence[i].Freshness.Reasons)
		}
	}
	out.SourceSnapshotIDs = sortedAPIStrings(out.SourceSnapshotIDs)
	slices.SortFunc(out.Snapshots, func(a, b SourceSnapshot) int { return cmp.Compare(a.ID, b.ID) })
	for i := range out.Snapshots {
		p := &out.Snapshots[i]
		p.Provider.Profiles = sortedAPIStrings(p.Provider.Profiles)
		p.Provider.Limitations = sortedAPIStrings(p.Provider.Limitations)
		slices.SortFunc(p.Files, func(a, b ManifestFile) int { return cmp.Compare(a.Path, b.Path) })
	}
	slices.SortFunc(out.Sources, func(a, b SourceSnapshot) int { return cmp.Compare(a.ID, b.ID) })
	for i := range out.Sources {
		p := &out.Sources[i]
		p.Provider.Profiles = sortedAPIStrings(p.Provider.Profiles)
		p.Provider.Limitations = sortedAPIStrings(p.Provider.Limitations)
		slices.SortFunc(p.Files, func(a, b ManifestFile) int { return cmp.Compare(a.Path, b.Path) })
	}
	for i := range out.Inventory {
		out.Inventory[i].Gaps = sortedAPIStrings(out.Inventory[i].Gaps)
	}
	slices.SortFunc(out.Inventory, func(a, b InventoryItem) int {
		aj, _ := canonicalAPIJSON(a)
		bj, _ := canonicalAPIJSON(b)
		return slices.Compare(aj, bj)
	})
	out.Coverage.Gaps = sortedAPIStrings(out.Coverage.Gaps)
	out.ReconciliationGaps = sortedAPIStrings(out.ReconciliationGaps)
	return out, nil
}
func APIArtifactSourceContentHash(state RevisionState, coverage RevisionCoverage) (string, error) {
	projection, err := BuildAPIArtifactSourceProjection(state, coverage)
	if err != nil {
		return "", err
	}
	return hashAPIJSON(projection)
}

type apiSemanticRef struct {
	Kind            string              `json:"kind"`
	ArtifactID      string              `json:"artifactId"`
	RevisionID      string              `json:"revisionId"`
	ContentHash     string              `json:"contentHash"`
	Selector        APIArtifactSelector `json:"selector"`
	ObjectHash      string              `json:"objectHash"`
	ResolvedPointer string              `json:"resolvedPointer"`
}
type apiSemanticBinding struct {
	SourceNodeID string         `json:"sourceNodeId"`
	SourceKind   string         `json:"sourceKind"`
	Ref          apiSemanticRef `json:"ref"`
}

func canonicalAPIPins(pins []ArtifactPin) []ArtifactPin {
	out := slices.Clone(pins)
	slices.SortFunc(out, func(a, b ArtifactPin) int {
		return cmp.Or(cmp.Compare(a.Kind, b.Kind), cmp.Compare(a.ID, b.ID), cmp.Compare(a.RevisionID, b.RevisionID), cmp.Compare(a.ContentHash, b.ContentHash))
	})
	return out
}

// APIArtifactSemanticHash restores the supplied original import anchor on clear;
// callers retain that anchor across pin-only revisions, without recursive hashing.
func APIArtifactSemanticHash(sourceContentHash, sourceSemanticHash string, pins []ArtifactPin, bindings []APIArtifactBinding) (string, error) {
	if !validHash(sourceContentHash) || !validHash(sourceSemanticHash) {
		return "", invalid("sourceHash", "Expected lowercase SHA-256 anchors")
	}
	if err := ValidateAPIArtifactVector(pins, bindings); err != nil {
		return "", err
	}
	if len(pins) == 0 && len(bindings) == 0 {
		return sourceSemanticHash, nil
	}
	semantic := make([]apiSemanticBinding, 0, len(bindings))
	for _, b := range bindings {
		r := b.Ref
		semantic = append(semantic, apiSemanticBinding{b.SourceNodeID, b.SourceKind, apiSemanticRef{r.Kind, r.ArtifactID, r.RevisionID, r.ContentHash, r.Selector, r.ObjectHash, r.ResolvedPointer}})
	}
	slices.SortFunc(semantic, func(a, b apiSemanticBinding) int { return cmp.Compare(a.SourceNodeID, b.SourceNodeID) })
	return hashAPIJSON(struct {
		Domain            string               `json:"domain"`
		SourceContentHash string               `json:"sourceContentHash"`
		Pins              []ArtifactPin        `json:"pins"`
		Bindings          []apiSemanticBinding `json:"bindings"`
	}{"backend-api-pins-semantic-v1", sourceContentHash, canonicalAPIPins(pins), semantic})
}

// APIArtifactCandidateHash binds the exact commands and full candidate independently
// of the semantic hash. CandidateHash itself is cleared to avoid recursion.
func APIArtifactCandidateHash(input PreviewAPIPinsInput, candidate APIPinsPreview) (string, error) {
	if err := input.Validate(); err != nil {
		return "", err
	}
	candidate.CandidateHash = ""
	candidate.Pins = canonicalAPIPins(candidate.Pins)
	candidate.Bindings = slices.Clone(candidate.Bindings)
	slices.SortFunc(candidate.Bindings, func(a, b APIArtifactBinding) int { return cmp.Compare(a.SourceNodeID, b.SourceNodeID) })
	candidate.SourceSnapshotIDs = sortedAPIStrings(candidate.SourceSnapshotIDs)
	return hashAPIJSON(struct {
		Domain    string              `json:"domain"`
		Input     PreviewAPIPinsInput `json:"input"`
		Candidate APIPinsPreview      `json:"candidate"`
	}{"backend-api-pins-candidate-v1", input, candidate})
}
