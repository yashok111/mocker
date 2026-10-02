package backendmodel

const (
	RuntimeProfile       = "runtime-flow-v1"
	RuntimeSchemaVersion = "3"
)

func hasRelationalProfile(profile string) bool {
	return profile == RelationalProfile || hasRuntimeProfile(profile)
}

func isRelationalSchema(schema string) bool {
	return schema == RelationalSchemaVersion || isRuntimeSchema(schema)
}

func profileForSchema(schema string) string {
	switch schema {
	case LineageSchemaVersion:
		return LineageProfile
	case RuntimeSchemaVersion:
		return RuntimeProfile
	case RelationalSchemaVersion:
		return RelationalProfile
	default:
		return GraphProfile
	}
}
