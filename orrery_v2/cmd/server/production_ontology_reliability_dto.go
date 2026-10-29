package main

import (
	"strings"
	"time"

	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/reliability"
	httptransport "orrery_v2/internal/transport/http"
)

func reliabilityIncidentDTO(incident reliability.FailureIncident) httptransport.OntologyReliabilityIncidentDTO {
	return httptransport.OntologyReliabilityIncidentDTO{
		ID: incident.ID, TenantID: incident.TenantID, Fingerprint: incident.Fingerprint, Source: incident.Source,
		Component: incident.Component, Category: string(incident.Category), Severity: string(incident.Severity), Status: string(incident.Status),
		ErrorCode: incident.ErrorCode, RedactedSummary: incident.RedactedSummary, Retryable: incident.Retryable,
		OccurrenceCount: incident.OccurrenceCount, Version: incident.Version, FirstSeenAt: reliabilityTime(incident.FirstSeenAt),
		LastSeenAt: reliabilityTime(incident.LastSeenAt), UpdatedAt: reliabilityTime(incident.UpdatedAt),
	}
}

func reliabilityTargetDTO(target reliability.RepairTarget) httptransport.OntologyReliabilityRepairTargetDTO {
	return httptransport.OntologyReliabilityRepairTargetDTO{
		ObservationID: target.ObservationID, Kind: string(target.Kind), AuthorizationTenantID: target.AuthorizationTenantID,
		MissionID: target.MissionID, RunID: target.RunID, OccurredAt: reliabilityTime(target.OccurredAt),
	}
}

func reliabilityDiagnosisDTO(diagnosis reliability.DiagnosisCase) httptransport.OntologyReliabilityDiagnosisDTO {
	dto := httptransport.OntologyReliabilityDiagnosisDTO{
		ID: diagnosis.ID, IncidentID: diagnosis.IncidentID, IncidentVersion: diagnosis.IncidentVersion,
		RootCauseCategory: string(diagnosis.RootCauseCategory), RedactedSummary: diagnosis.RedactedSummary,
		Confidence: diagnosis.Confidence, Status: string(diagnosis.Status), EvidenceIDs: append([]string(nil), diagnosis.EvidenceIDs...),
		ProposedBy:   httptransport.OntologyActionRefDTO{Kind: diagnosis.ProposedBy.Kind, ID: diagnosis.ProposedBy.ID},
		ReviewReason: diagnosis.ReviewReason, Version: diagnosis.Version,
		CreatedAt: reliabilityTime(diagnosis.CreatedAt), UpdatedAt: reliabilityTime(diagnosis.UpdatedAt),
	}
	if diagnosis.ReviewedBy.Kind != "" && diagnosis.ReviewedBy.ID != "" {
		dto.ReviewedBy = &httptransport.OntologyActionRefDTO{Kind: diagnosis.ReviewedBy.Kind, ID: diagnosis.ReviewedBy.ID}
	}
	return dto
}

func reliabilityRuleDTO(binding reliabilityRuleBinding) httptransport.OntologyReliabilityRepairRuleDTO {
	rule := binding.rule
	dto := httptransport.OntologyReliabilityRepairRuleDTO{
		ID: rule.ID, DiagnosisCaseID: rule.DiagnosisCaseID, Strategy: string(rule.Strategy), ActionTypeID: rule.ActionTypeID, RiskLevel: string(rule.RiskLevel), Status: string(rule.Status),
		Confidence: rule.Confidence, SupportCount: rule.SupportCount, SuccessCount: rule.SuccessCount, FailureCount: rule.FailureCount,
		Reversible: rule.Reversible, Idempotent: rule.Idempotent, AutoApply: rule.AutoApply, Version: rule.Version,
		ArgsHash: binding.argsHash, DryRunReady: binding.dryRunOK, ActivationReady: binding.activationReady,
		ActivationBlockers: append([]string(nil), binding.activationBlockers...), ActivationDecisionID: rule.ActivationDecisionID,
		ApplyReady: len(binding.blockers) == 0, ApplyBlockers: append([]string(nil), binding.blockers...), UpdatedAt: reliabilityTime(rule.UpdatedAt),
	}
	if rule.ActivatedBy.Kind != "" && rule.ActivatedBy.ID != "" {
		dto.ActivatedBy = &httptransport.OntologyActionRefDTO{Kind: rule.ActivatedBy.Kind, ID: rule.ActivatedBy.ID}
	}
	if rule.ActivatedAt != nil {
		dto.ActivatedAt = reliabilityTime(*rule.ActivatedAt)
	}
	if rule.LastVerifiedAt != nil {
		dto.LastVerifiedAt = reliabilityTime(*rule.LastVerifiedAt)
	}
	if rule.ExpiresAt != nil {
		dto.ExpiresAt = reliabilityTime(*rule.ExpiresAt)
	}
	return dto
}

func reliabilityActivationReviewDTO(decision hil.DecisionRequest, binding reliabilityRuleBinding, incidentID string, current bool) httptransport.OntologyReliabilityActivationReviewDTO {
	ruleVersion := reliabilityPayloadInt64(decision.PayloadPreview, "rule_version")
	if ruleVersion == 0 {
		ruleVersion = binding.rule.Version
	}
	diagnosisID := reliabilityPayloadString(decision.PayloadPreview, "diagnosis_case_id")
	if diagnosisID == "" {
		diagnosisID = binding.rule.DiagnosisCaseID
	}
	return httptransport.OntologyReliabilityActivationReviewDTO{
		ID: decision.ID, RuleID: binding.rule.ID, RuleVersion: ruleVersion, IncidentID: incidentID,
		DiagnosisCaseID: diagnosisID, PreflightEvidenceID: reliabilityPayloadString(decision.PayloadPreview, "preflight_evidence_id"),
		Status: string(decision.Status), RouteMode: string(decision.Route.Mode),
		Requester: httptransport.OntologyActionRefDTO{Kind: string(decision.Requester.Kind), ID: decision.Requester.ID},
		ArgsHash:  decision.ArgsHash, Purpose: decision.Purpose, Current: current,
		Activated: binding.rule.Status == reliability.RepairRuleStatusActive && binding.rule.ActivationDecisionID == decision.ID,
		CreatedAt: reliabilityTime(decision.CreatedAt), ExpiresAt: reliabilityTime(decision.ExpiresAt),
	}
}

func reliabilityPayloadString(payload map[string]any, key string) string {
	value, _ := payload[key].(string)
	return strings.TrimSpace(value)
}

func reliabilityPayloadInt64(payload map[string]any, key string) int64 {
	switch value := payload[key].(type) {
	case int:
		return int64(value)
	case int64:
		return value
	case float64:
		return int64(value)
	default:
		return 0
	}
}

func reliabilityPreflightDTO(preflight reliability.RepairPreflight, current bool) httptransport.OntologyReliabilityPreflightDTO {
	return httptransport.OntologyReliabilityPreflightDTO{
		EvidenceID: preflight.EvidenceID, ActionRunID: preflight.ActionRunID, IncidentID: preflight.IncidentID,
		IncidentVersion: preflight.IncidentVersion, RuleID: preflight.RuleID, RuleVersion: preflight.RuleVersion,
		Strategy: string(preflight.Strategy), ArgsHash: preflight.ArgsHash, TargetObservationID: preflight.TargetObservationID,
		TargetKind: string(preflight.TargetKind), TargetRunID: preflight.TargetRunID, Passed: preflight.Passed, Current: current,
		CheckedAt: reliabilityTime(preflight.CheckedAt), ExpiresAt: reliabilityTime(preflight.ExpiresAt),
	}
}

func reliabilityExecutionDTO(execution reliability.RepairExecution) httptransport.OntologyReliabilityExecutionDTO {
	dto := httptransport.OntologyReliabilityExecutionDTO{
		ActionRunID: execution.ActionRunID, IncidentID: execution.IncidentID, RuleID: execution.RuleID,
		PreflightEvidenceID: execution.PreflightEvidenceID, Strategy: string(execution.Strategy), OriginalRunID: execution.Target.RunID,
		RetryRunID: execution.RetryRunID, Status: string(execution.Status), FailureCode: execution.FailureCode, Version: execution.Version,
		StartedAt: reliabilityTime(execution.StartedAt), DeadlineAt: reliabilityTime(execution.DeadlineAt), UpdatedAt: reliabilityTime(execution.UpdatedAt),
	}
	if execution.CompletedAt != nil {
		dto.CompletedAt = reliabilityTime(*execution.CompletedAt)
	}
	return dto
}

func reliabilityAuthorizationDTO(decision hil.DecisionRequest, ruleID string, current bool) httptransport.OntologyReliabilityAuthorizationDTO {
	mode, _ := reliabilityExecutionModeFromAction(decision.Action)
	return httptransport.OntologyReliabilityAuthorizationDTO{
		ID: decision.ID, ExecutionMode: string(mode), RuleID: ruleID, Status: string(decision.Status), RouteMode: string(decision.Route.Mode),
		Requester: httptransport.OntologyActionRefDTO{Kind: string(decision.Requester.Kind), ID: decision.Requester.ID},
		ArgsHash:  decision.ArgsHash, Purpose: decision.Purpose, Current: current, CreatedAt: reliabilityTime(decision.CreatedAt), ExpiresAt: reliabilityTime(decision.ExpiresAt),
	}
}

func reliabilityTargetRunID(target *reliability.RepairTarget) string {
	if target == nil {
		return ""
	}
	return target.RunID
}

func reliabilityTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
