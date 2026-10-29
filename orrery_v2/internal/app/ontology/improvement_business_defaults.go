package ontology

func businessAssessmentProperties() []string {
	return []string{"case_id", "runtime_review_id", "content_hash", "business_status", "causal_attribution", "submission_id", "qa_status", "qa_required", "contract_sha256", "source_sha256", "deliverables", "evidence", "blockers", "assessed_at"}
}

func businessAssessmentObjectType() ObjectType {
	props := []PropertyDefinition{}
	for _, name := range businessAssessmentProperties() {
		kind := ValueTypeString
		switch name {
		case "qa_required":
			kind = ValueTypeBoolean
		case "deliverables", "evidence":
			kind = ValueTypeNumber
		case "blockers":
			kind = ValueTypeArray
		case "assessed_at":
			kind = ValueTypeDateTime
		}
		props = append(props, property(name, kind, true))
	}
	return objectType("improvement_business_assessment", "Task business evidence", "improvement_business_assessment:{id}", "improvement", props...)
}

func businessAssessmentMappingRule() MappingRule {
	return improvementRule("business_assessment", businessAssessmentProperties(), []any{
		improvementLink("improvement_case_has_business_assessment", "improvement_case", "$.case_id", "improvement_business_assessment", "$.id"),
		improvementLink("improvement_business_assessment_for_runtime_review", "improvement_business_assessment", "$.id", "improvement_runtime_review", "$.runtime_review_id"),
	})
}
