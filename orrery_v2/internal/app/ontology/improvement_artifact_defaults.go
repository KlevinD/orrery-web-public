package ontology

func artifactVerificationProperties() []string {
	return []string{"case_id", "runtime_review_id", "business_assessment_id", "submission_id", "manifest_id", "content_hash", "verification_status", "items", "attempts", "verified_at"}
}

func artifactVerificationObjectType() ObjectType {
	props := []PropertyDefinition{}
	for _, name := range artifactVerificationProperties() {
		kind := ValueTypeString
		switch name {
		case "items":
			kind = ValueTypeArray
		case "attempts":
			kind = ValueTypeNumber
		case "verified_at":
			kind = ValueTypeDateTime
		}
		props = append(props, property(name, kind, true))
	}
	return objectType("improvement_artifact_verification", "Task artifact verification", "improvement_artifact_verification:{id}", "improvement", props...)
}

func artifactVerificationMappingRule() MappingRule {
	return improvementRule("artifact_verification", artifactVerificationProperties(), []any{
		improvementLink("improvement_case_has_artifact_verification", "improvement_case", "$.case_id", "improvement_artifact_verification", "$.id"),
		improvementLink("improvement_artifact_verification_for_business_assessment", "improvement_artifact_verification", "$.id", "improvement_business_assessment", "$.business_assessment_id"),
	})
}
