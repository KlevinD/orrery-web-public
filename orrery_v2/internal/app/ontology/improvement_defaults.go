package ontology

import "context"

// Static review objects keep their original required fields. Only these
// optional execution references are added to an existing managed definition.
func upgradeRuntimeReviewExecutionProperties(ctx context.Context, registry Registry, current, desired ObjectType) error {
	if current.TenantID != "" || current.ID != desired.ID || current.OwnerDomain != "improvement" || current.CanonicalKeyRule != desired.CanonicalKeyRule {
		return nil
	}
	updated := current
	updated.Properties = append([]PropertyDefinition(nil), current.Properties...)
	changed := false
	for _, wanted := range desired.Properties {
		switch wanted.PropertyID {
		case "execution_evidence_id", "runtime_policy_sha256", "implementation_sha256":
		default:
			continue
		}
		found := false
		for _, existing := range current.Properties {
			if existing.PropertyID == wanted.PropertyID {
				found = true
				if existing.ValueType != wanted.ValueType || existing.Required {
					return ErrInvalidDefinition
				}
				break
			}
		}
		if !found {
			updated.Properties = append(updated.Properties, wanted)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	versioned, ok := registry.(versionedDefaultObjectTypeRegistry)
	if !ok {
		return ErrInvalidDefinition
	}
	_, err := versioned.SaveObjectTypeWithExpectedVersion(ctx, updated, current.Version)
	return err
}

// These definitions participate in the normal default installation and
// conformance gate; RSI is not a separate untyped graph or metadata blob.
func defaultImprovementObjectTypes() []ObjectType {
	objects := []ObjectType{
		runtimeDependenciesObjectType(),
		artifactVerificationObjectType(),
		businessAssessmentObjectType(),
		objectType("improvement_case", "任务与流程改进", "improvement_case:{id}", "improvement",
			property("status", ValueTypeString, true), property("revision", ValueTypeNumber, true),
			property("trigger", ValueTypeString, true), property("summary", ValueTypeString, true),
			property("task_ref", ValueTypeString, true), property("run_ref", ValueTypeString, true),
			property("evidence_ids", ValueTypeArray, true), property("updated_at", ValueTypeDateTime, true)),
		objectType("improvement_resource_version", "改进资源版本", "improvement_resource_version:{id}", "improvement",
			property("target_kind", ValueTypeString, true), property("resource_id", ValueTypeString, true),
			property("version", ValueTypeString, true), property("content_hash", ValueTypeString, true)),
		objectType("improvement_runtime_review", "任务运行证据审查", "improvement_runtime_review:{id}", "improvement",
			property("case_id", ValueTypeString, true), property("content_hash", ValueTypeString, true), property("policy_hash", ValueTypeString, true),
			property("pipeline_status", ValueTypeString, true), property("trace_finalization", ValueTypeString, true),
			property("business_quality", ValueTypeString, true), property("causal_attribution", ValueTypeString, true),
			property("baseline_ready", ValueTypeBoolean, true), property("domain_coverage", ValueTypeArray, true),
			property("signals", ValueTypeArray, true), property("blockers", ValueTypeArray, true), property("assessed_at", ValueTypeDateTime, true),
			property("execution_evidence_id", ValueTypeString, false), property("runtime_policy_sha256", ValueTypeString, false), property("implementation_sha256", ValueTypeString, false)),
	}
	for _, kind := range []string{"change_set", "experiment", "evaluation", "release", "outcome"} {
		id := "improvement_" + kind
		objects = append(objects, objectType(id, "RSI "+kind, id+":{id}", "improvement",
			property("content_hash", ValueTypeString, true), property("case_id", ValueTypeString, true),
			property("artifact_refs", ValueTypeArray, true)))
	}
	return objects
}

func defaultImprovementLinkTypes() []LinkType {
	out := []LinkType{
		linkType("improvement_case_has_runtime_dependencies", "Dispatch dependencies", "improvement_case", "improvement_runtime_dependencies", CardinalityOneToOne, true),
		linkType("improvement_runtime_dependencies_for_review", "Original runtime review", "improvement_runtime_dependencies", "improvement_runtime_review", CardinalityManyToOne, true),
		linkType("improvement_case_has_artifact_verification", "Artifact verification", "improvement_case", "improvement_artifact_verification", CardinalityOneToMany, true),
		linkType("improvement_artifact_verification_for_business_assessment", "Verifies submitted bytes", "improvement_artifact_verification", "improvement_business_assessment", CardinalityManyToOne, true),
		linkType("improvement_case_from_failure_incident", "改进关联原故障", "improvement_case", "failure_incident", CardinalityManyToOne, true),
		linkType("improvement_case_has_business_assessment", "Business evidence", "improvement_case", "improvement_business_assessment", CardinalityOneToMany, true),
		linkType("improvement_business_assessment_for_runtime_review", "Assesses runtime", "improvement_business_assessment", "improvement_runtime_review", CardinalityManyToOne, true),
		linkType("improvement_case_observed_in_agent_run", "改进来自执行", "improvement_case", "agent_run", CardinalityManyToOne, true),
		linkType("improvement_case_observed_in_action_run", "改进来自动作", "improvement_case", "action_run", CardinalityManyToOne, true),
		linkType("improvement_case_improves_agent_task", "改进智能体任务", "improvement_case", "agent_task", CardinalityManyToOne, true),
		linkType("improvement_case_improves_task", "改进任务", "improvement_case", "task", CardinalityManyToOne, true),
		linkType("improvement_case_follows_case", "后续改进", "improvement_case", "improvement_case", CardinalityManyToOne, true),
		linkType("improvement_case_supported_by_diagnosis", "诊断支撑改进", "improvement_case", "diagnosis_case", CardinalityManyToOne, true),
		linkType("improvement_case_has_runtime_review", "任务证据审查", "improvement_case", "improvement_runtime_review", CardinalityOneToOne, true),
		linkType("improvement_change_set_has_baseline", "变更基线", "improvement_change_set", "improvement_resource_version", CardinalityOneToMany, true),
		linkType("improvement_change_set_has_candidate", "变更候选", "improvement_change_set", "improvement_resource_version", CardinalityOneToMany, true),
	}
	for _, kind := range []string{"change_set", "experiment", "evaluation", "release", "outcome"} {
		out = append(out, linkType("improvement_case_has_"+kind, "改进关联"+kind, "improvement_case", "improvement_"+kind, CardinalityOneToOne, true))
	}
	for _, pair := range [][2]string{{"experiment", "change_set"}, {"evaluation", "experiment"}, {"release", "evaluation"}, {"outcome", "release"}} {
		out = append(out, linkType("improvement_"+pair[0]+"_references_"+pair[1], "改进证据关联", "improvement_"+pair[0], "improvement_"+pair[1], CardinalityManyToOne, true))
	}
	return out
}

func improvementLink(kind, fromType, fromID, toType, toID string) map[string]any {
	return map[string]any{"type": kind, "from_type": fromType, "from_id": fromID, "to_type": toType, "to_id": toID, "optional": true}
}

func improvementRule(kind string, properties []string, links []any) MappingRule {
	object := "improvement_" + kind
	props := make([]any, 0, len(properties))
	for _, prop := range properties {
		props = append(props, map[string]any{"object_type": object, "object_id": "$.id", "property_id": prop, "value": "$." + prop})
	}
	return MappingRule{ID: "improvement." + kind + ".upserted", EventType: "improvement." + kind + ".upserted", ObjectTypes: []string{object}, Enabled: true,
		Rule: map[string]any{"objects": []any{map[string]any{"type": object, "id": "$.id", "canonical_key": object + ":{id}"}}, "properties": props, "links": links}}
}

func defaultImprovementMappingRules() []MappingRule {
	caseLinks := []any{
		improvementLink("improvement_case_observed_in_agent_run", "improvement_case", "$.id", "agent_run", "$.agent_run_id"),
		improvementLink("improvement_case_observed_in_action_run", "improvement_case", "$.id", "action_run", "$.action_run_id"),
		improvementLink("improvement_case_improves_agent_task", "improvement_case", "$.id", "agent_task", "$.agent_task_id"),
		improvementLink("improvement_case_improves_task", "improvement_case", "$.id", "task", "$.task_id"),
		improvementLink("improvement_case_follows_case", "improvement_case", "$.id", "improvement_case", "$.parent_case_id"),
		improvementLink("improvement_case_supported_by_diagnosis", "improvement_case", "$.id", "diagnosis_case", "$.diagnosis_case_id"),
	}
	out := []MappingRule{
		runtimeDependenciesMappingRule(),
		artifactVerificationMappingRule(),
		businessAssessmentMappingRule(),
		improvementRule("case", []string{"status", "revision", "trigger", "summary", "task_ref", "run_ref", "evidence_ids", "updated_at"}, caseLinks),
		improvementRule("resource_version", []string{"target_kind", "resource_id", "version", "content_hash"}, []any{
			improvementLink("improvement_change_set_has_baseline", "improvement_change_set", "$.baseline_change_set_id", "improvement_resource_version", "$.id"),
			improvementLink("improvement_change_set_has_candidate", "improvement_change_set", "$.candidate_change_set_id", "improvement_resource_version", "$.id"),
		}),
		improvementRule("runtime_review", []string{"case_id", "content_hash", "policy_hash", "pipeline_status", "trace_finalization", "business_quality", "causal_attribution", "baseline_ready", "domain_coverage", "signals", "blockers", "assessed_at"}, []any{
			improvementLink("improvement_case_has_runtime_review", "improvement_case", "$.case_id", "improvement_runtime_review", "$.id"),
		}),
	}
	managedRepair := improvementRule("case", []string{"status", "revision", "trigger", "summary", "task_ref", "run_ref", "evidence_ids", "updated_at"}, append(append([]any(nil), caseLinks...),
		improvementLink("improvement_case_from_failure_incident", "improvement_case", "$.id", "failure_incident", "$.failure_incident_id")))
	managedRepair.ID, managedRepair.EventType = "improvement.managed_repair.upserted", "improvement.managed_repair.upserted"
	out = append(out, managedRepair)
	// Preserve the V1 rule for replay of historical events. Both protocols
	// materialize the same typed review object through the conformance gate.
	v2 := improvementRule("runtime_review", []string{"case_id", "content_hash", "policy_hash", "pipeline_status", "trace_finalization", "business_quality", "causal_attribution", "baseline_ready", "domain_coverage", "signals", "blockers", "assessed_at", "execution_evidence_id", "runtime_policy_sha256", "implementation_sha256"}, []any{
		improvementLink("improvement_case_has_runtime_review", "improvement_case", "$.case_id", "improvement_runtime_review", "$.id"),
	})
	v2.ID, v2.EventType = "improvement.runtime_review_v2.upserted", "improvement.runtime_review_v2.upserted"
	out = append(out, v2)
	parents := map[string]string{"experiment": "change_set", "evaluation": "experiment", "release": "evaluation", "outcome": "release"}
	for _, kind := range []string{"change_set", "experiment", "evaluation", "release", "outcome"} {
		links := []any{improvementLink("improvement_case_has_"+kind, "improvement_case", "$.case_id", "improvement_"+kind, "$.id")}
		if parent := parents[kind]; parent != "" {
			links = append(links, improvementLink("improvement_"+kind+"_references_"+parent, "improvement_"+kind, "$.id", "improvement_"+parent, "$.parent_id"))
		}
		out = append(out, improvementRule(kind, []string{"content_hash", "case_id", "artifact_refs"}, links))
	}
	for i := range out {
		for _, raw := range out[i].Rule["links"].([]any) {
			out[i].LinkTypes = append(out[i].LinkTypes, raw.(map[string]any)["type"].(string))
		}
	}
	return out
}
