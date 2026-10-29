package ontology

func runtimeDependenciesProperties() []string {
	return []string{"case_id", "runtime_review_id", "content_hash", "model_dispatches", "tool_dispatches", "dropped", "invalid", "runner_returned", "baseline_ready", "captured_at"}
}

func runtimeDependenciesObjectType() ObjectType {
	props := []PropertyDefinition{}
	for _, name := range runtimeDependenciesProperties() {
		kind := ValueTypeString
		switch name {
		case "model_dispatches", "tool_dispatches", "dropped", "invalid":
			kind = ValueTypeNumber
		case "runner_returned", "baseline_ready":
			kind = ValueTypeBoolean
		case "captured_at":
			kind = ValueTypeDateTime
		}
		props = append(props, property(name, kind, true))
	}
	return objectType("improvement_runtime_dependencies", "Runtime dispatch dependencies", "improvement_runtime_dependencies:{id}", "improvement", props...)
}

func runtimeDependenciesMappingRule() MappingRule {
	return improvementRule("runtime_dependencies", runtimeDependenciesProperties(), []any{
		improvementLink("improvement_case_has_runtime_dependencies", "improvement_case", "$.case_id", "improvement_runtime_dependencies", "$.id"),
		improvementLink("improvement_runtime_dependencies_for_review", "improvement_runtime_dependencies", "$.id", "improvement_runtime_review", "$.runtime_review_id"),
	})
}
