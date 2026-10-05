package backendmodel

import (
	"encoding/json/v2"
	"strings"
)

// TranslateAnalysisSourceProperty follows the same declared reference sites as
// source composition/rebase. It never rewrites arbitrary strings or native text.
// Missing correspondence is lack of evidence, not a known unequal value.
func TranslateAnalysisSourceProperty(payload SourceAssertionPayload, selector TypedSourcePropertySelector, expected SourcePropertyValue, mapping map[string]string) (SourcePropertyValue, bool, error) {
	candidate, err := ApplySourceProperty(payload, selector, expected)
	if err != nil {
		return expected, false, err
	}
	path, err := sourcePropertyPath(candidate, selector)
	if err != nil {
		return expected, false, err
	}
	prefix := ""
	for _, part := range path {
		prefix += "/" + escapeRelationalPointer(part)
	}
	refs, err := analysisPayloadReferenceSites(candidate)
	if err != nil {
		return expected, false, err
	}
	raw, err := json.Marshal(candidate)
	if err != nil {
		return expected, false, err
	}
	for _, ref := range refs {
		selected := ref.Path == prefix || strings.HasPrefix(ref.Path, prefix+"/")
		if selector.Kind == "edge_endpoints" {
			selected = ref.Path == "/from" || ref.Path == "/to"
		}
		if !selected {
			continue
		}
		translated := mapping[ref.ID]
		if translated == "" {
			return expected, false, nil
		}
		raw, err = rebaseSetReference(raw, strings.Split(strings.TrimPrefix(ref.Path, "/"), "/"), translated)
		if err != nil {
			return expected, false, err
		}
	}
	if err = json.Unmarshal(raw, &candidate); err != nil {
		return expected, false, err
	}
	value, err := SelectSourceProperty(candidate, selector)
	return value, err == nil, err
}

func analysisPayloadReferenceSites(p SourceAssertionPayload) ([]relationalReference, error) {
	if !relationalSubject(p.Kind, p.Attributes, p.RecordType == "edge") {
		return sourcePayloadReferenceSites(p)
	}
	refs, err := relationalReferencesMode(p.Kind, p.Attributes, p.RecordType == "edge", true, false)
	if err != nil {
		return nil, err
	}
	current := []relationalReference{}
	if p.RecordType == "node" && p.ParentID != nil {
		current = append(current, relationalReference{Path: "/parentId", ID: *p.ParentID, RecordType: "node"})
	}
	if p.RecordType == "edge" {
		current = append(current, relationalReference{Path: "/from", ID: p.From, RecordType: "node"}, relationalReference{Path: "/to", ID: p.To, RecordType: "node"})
	}
	for _, ref := range refs {
		if ref.Kind != "evidence" && ref.HistoricalRevisionID == "" {
			current = append(current, ref)
		}
	}
	return current, nil
}
