package designscenario

// BindingTargetTypes resolves each binding's target type from the document's
// pinned contracts, using the same schema rules as scenario execution. Keys are
// message IDs followed by binding IDs; unresolved types are "unknown".
func BindingTargetTypes(document Document) map[string]map[string]string {
	schemas := bindingSchemas(document)
	out := map[string]map[string]string{}
	for i, message := range document.Messages {
		if message.Execution == nil || len(message.Execution.Bindings) == 0 {
			continue
		}
		types := map[string]string{}
		for _, binding := range message.Execution.Bindings {
			types[binding.ID] = schemas[i].targetType(binding.Target)
		}
		out[message.ID] = types
	}
	return out
}
