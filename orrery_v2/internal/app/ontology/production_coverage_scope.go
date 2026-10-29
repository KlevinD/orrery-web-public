package ontology

import (
	"strings"
	"time"
)

// externalMCPProductionCoverageEvents lists business integration requirements
// outside the user-approved Orrery Web/API source release. Match exact events:
// unknown events and Orrery's existing publishers remain subject to the gate.
// Keep the full manifest and strict validator for separate MCP acceptance.
func externalMCPProductionCoverageEvents() map[string]bool {
	return map[string]bool{
		"people.agent.updated":                     true,
		"people.person.updated":                    true,
		"people.department.created":                true,
		"people.position.created":                  true,
		"people.position.assigned":                 true,
		"people.assignment.v2.changed":             true,
		"people.job_profile.v2.changed":            true,
		"people.org_unit.v2.changed":               true,
		"people.position.v2.changed":               true,
		"people.reporting_relationship.v2.changed": true,
		"people.responsibility.v2.changed":         true,
		"people.responsibility_binding.v2.changed": true,
		"people.subject.v2.changed":                true,
		"people.team.created":                      true,
		"people.membership.linked":                 true,
		"planning.requirement.created":             true,
		"planning.decision.recorded":               true,
		"planning.design_artifact.created":         true,
		"legal.entity.created":                     true,
		"legal.contract.created":                   true,
		"legal.policy.created":                     true,
		"legal.approval_case.opened":               true,
		"development.repository.registered":        true,
		"development.issue.created":                true,
		"development.pull_request.opened":          true,
		"development.release.created":              true,
		"value.objective.created":                  true,
		"value.metric.recorded":                    true,
		"value.initiative.created":                 true,
		"value.experiment.created":                 true,
		"discussion.message.created":               true,
		"permission.binding.updated":               true,
	}
}

func webAPIProductionCoverageManifest(manifest ProductionCoverageManifest) ProductionCoverageManifest {
	external := externalMCPProductionCoverageEvents()
	result := ProductionCoverageManifest{Records: make([]ProductionCoverageRecord, 0, len(manifest.Records))}
	for _, record := range manifest.Records {
		if !external[strings.TrimSpace(record.EventType)] {
			result.Records = append(result.Records, record)
		}
	}
	return result
}

// ValidateWebAPIProductionCoverageGate enforces the same evidence, expiry and
// tenant-coverage rules as the full gate for the Web/API release scope.
func ValidateWebAPIProductionCoverageGate(rules []MappingRule, manifest ProductionCoverageManifest, publishers []ProductionEventPublisherEvidence, asOf time.Time) ProductionCoverageGateReport {
	external := externalMCPProductionCoverageEvents()
	scopedRules := make([]MappingRule, 0, len(rules))
	for _, rule := range rules {
		if !external[strings.TrimSpace(rule.EventType)] {
			scopedRules = append(scopedRules, rule)
		}
	}
	scopedPublishers := make([]ProductionEventPublisherEvidence, 0, len(publishers))
	for _, publisher := range publishers {
		if !external[strings.TrimSpace(publisher.EventType)] {
			scopedPublishers = append(scopedPublishers, publisher)
		}
	}
	required := webAPIProductionCoverageManifest(ProductionCoverageManifest{Records: required0618CoverageRecords()})
	return validateProductionCoverageGate(scopedRules, webAPIProductionCoverageManifest(manifest), scopedPublishers, asOf, required.Records)
}

// BuildWebAPIProductionCoverageAuditReport reports the same selected scope as
// the Web/API gate. The full audit still retains external integration gaps.
func BuildWebAPIProductionCoverageAuditReport(manifest ProductionCoverageManifest, asOf time.Time) ProductionCoverageAuditReport {
	report := BuildProductionCoverageAuditReport(webAPIProductionCoverageManifest(manifest), asOf)
	report.RequiredModuleCount = 0
	external := externalMCPProductionCoverageEvents()
	for _, module := range required0618ProductionModules() {
		for _, eventType := range module.EventTypes {
			if !external[eventType] {
				report.RequiredModuleCount++
				break
			}
		}
	}
	return report
}
