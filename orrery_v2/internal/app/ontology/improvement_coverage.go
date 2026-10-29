package ontology

func improvementProductionCoverageRecords() []ProductionCoverageRecord {
	out := []ProductionCoverageRecord{}
	for _, kind := range []string{"case", "managed_repair", "change_set", "resource_version", "experiment", "evaluation", "release", "outcome", "runtime_review", "runtime_review_v2", "business_assessment", "artifact_verification", "runtime_dependencies"} {
		testPath := "internal/app/ontology/improvement_defaults_test.go"
		objectKind := kind
		fact := "content_hash"
		publisher := "internal/app/improvement/projection.go"
		evidence := "evidence:improvement-projection"
		required := payload("id", "content_hash", "case_id", "artifact_refs")
		if kind == "case" {
			required = payload("id", "status", "revision", "trigger", "summary", "task_ref", "run_ref", "evidence_ids", "updated_at")
			fact = "status"
		}
		if kind == "managed_repair" {
			objectKind, fact = "case", "status"
			required = payload("id", "status", "revision", "trigger", "summary", "task_ref", "run_ref", "evidence_ids", "updated_at", "failure_incident_id", "agent_run_id")
			testPath = "cmd/server/production_repair_improvement_test.go"
		}
		if kind == "resource_version" {
			required = payload("id", "target_kind", "resource_id", "version", "content_hash")
		}
		if kind == "business_assessment" {
			publisher = "internal/app/improvement/task_business_projection.go"
			required = payload(append([]string{"id"}, businessAssessmentProperties()...)...)
			fact = "business_status"
		}
		if kind == "artifact_verification" {
			publisher = "internal/app/improvement/task_artifact_projection.go"
			required = payload(append([]string{"id"}, artifactVerificationProperties()...)...)
			fact = "verification_status"
		}
		if kind == "runtime_dependencies" {
			publisher = "internal/app/improvement/runtime_dependencies_projection.go"
			testPath = "internal/app/ontology/improvement_dependencies_test.go"
			required = payload(append([]string{"id"}, runtimeDependenciesProperties()...)...)
			fact = "baseline_ready"
		}
		if kind == "runtime_review" || kind == "runtime_review_v2" {
			objectKind = "runtime_review"
			required = payload("id", "case_id", "content_hash", "policy_hash", "pipeline_status", "trace_finalization", "business_quality", "causal_attribution", "baseline_ready", "domain_coverage", "signals", "blockers", "assessed_at")
			evidence = "evidence:runtime-review-projection"
			if kind == "runtime_review_v2" {
				required = append(required, payload("execution_evidence_id", "runtime_policy_sha256", "implementation_sha256")...)
			}
		}
		out = append(out, partialCoverage("improvement", "task-level improvement "+kind, "improvement."+kind+".upserted", required,
			publishers(publisher),
			tests(testPath, failureTest(testPath, "incomplete improvement properties fail conformance"), replayTest(testPath, "outbox replay does not duplicate graph entities"), tenantTest(testPath, "improvement graph nodes retain originating tenant and access reference")),
			assertions("object:improvement_"+objectKind, "fact:improvement_"+objectKind+"."+fact, evidence),
			gaps("runtime observations, formal task acceptance and bounded scoped artifact verification are wired; unsupported artifact sources, independent business experiments, domain apply adapters and publication coordinator remain incomplete; private assistant execution remains outside this graph ingress")))
	}
	return out
}
