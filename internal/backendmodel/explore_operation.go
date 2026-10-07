package backendmodel

import "encoding/json/v2"

// Older collectors retained OpenAPI annotations as a JSON description string.
// Decode recognized display metadata without changing the semantic record.
func exploreOperationMetadata(n Node) (summary, description string, tags []string) {
	summary = runtimeAttributeString(n.Attributes, "summary")
	description = runtimeAttributeString(n.Attributes, "description")
	tags = runtimeAttributeStrings(n.Attributes, "tags")
	if n.Kind != "http_operation" {
		return
	}
	var metadata struct {
		Summary     string   `json:"summary"`
		Description string   `json:"description"`
		Tags        []string `json:"tags"`
	}
	if json.Unmarshal([]byte(description), &metadata) != nil || (metadata.Summary == "" && len(metadata.Tags) == 0) {
		return
	}
	if summary == "" {
		summary = metadata.Summary
	}
	if len(tags) == 0 {
		tags = metadata.Tags
	}
	description = metadata.Description
	return
}
