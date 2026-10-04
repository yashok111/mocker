package backendmodel

const (
	EventsProfile             = "events-service-v1"
	EventsSchemaVersion       = "5"
	EventsFlowTraversalPolicy = "runtime-flow-reachability-v2"
	MaxEventFieldsPerMessage  = 500
	MaxEventHandlers          = 50
)

func hasLineageProfile(profile string) bool {
	return profile == LineageProfile || profile == EventsProfile || profile == ComposedProfile
}
func isLineageSchema(schema string) bool {
	return schema == LineageSchemaVersion || schema == EventsSchemaVersion
}

func runtimeEntrypointForSchema(schema, kind string) bool {
	return kind == "http_operation" || schema == EventsSchemaVersion && (kind == "consumer" || kind == "job")
}
