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
	return profile == RuntimeProfile || profile == LineageProfile
}
func isRuntimeSchema(schema string) bool {
	return schema == RuntimeSchemaVersion || schema == LineageSchemaVersion
}
func sourceProfilesMatch(schema string, profiles []string) bool {
	expected := []string{GraphProfile, RelationalProfile, RuntimeProfile}
	if schema == LineageSchemaVersion {
		expected = append(expected, LineageProfile)
	} else if schema != RuntimeSchemaVersion {
		return false
	}
	return len(profiles) == len(expected) && slices.Equal(profileSet(profiles), profileSet(expected))
}
