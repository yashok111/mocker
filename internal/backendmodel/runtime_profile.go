package backendmodel

const (
	RuntimeProfile       = "runtime-flow-v1"
	RuntimeSchemaVersion = "3"
)

func hasRelationalProfile(profile string) bool {
	return profile == RelationalProfile || profile == RuntimeProfile
}

func isRelationalSchema(schema string) bool {
	return schema == RelationalSchemaVersion || schema == RuntimeSchemaVersion
}

func profileForSchema(schema string) string {
	switch schema {
	case RuntimeSchemaVersion:
		return RuntimeProfile
	case RelationalSchemaVersion:
		return RelationalProfile
	default:
		return GraphProfile
	}
}
