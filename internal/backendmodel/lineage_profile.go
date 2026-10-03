package backendmodel

import "slices"

const (
	LineageProfile           = "field-lineage-v1"
	LineageSchemaVersion     = "4"
	LineageTraversalPolicy   = "field-lineage-traversal-v1"
	MaxLineageSources        = 64
	MaxLineageReferences     = 100000
	MaxAPIFieldsPerOperation = 500
)

func hasRuntimeProfile(profile string) bool {
	return profile == RuntimeProfile || hasLineageProfile(profile)
}
func isRuntimeSchema(schema string) bool {
	return schema == RuntimeSchemaVersion || isLineageSchema(schema)
}
func sourceProfilesMatch(schema string, profiles []string) bool {
	expected := []string{GraphProfile, RelationalProfile, RuntimeProfile}
	if isLineageSchema(schema) {
		expected = append(expected, LineageProfile)
		if schema == EventsSchemaVersion {
			expected = append(expected, EventsProfile)
		}
	} else if schema != RuntimeSchemaVersion {
		return false
	}
	return len(profiles) == len(expected) && slices.Equal(profileSet(profiles), profileSet(expected))
}
