package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/reliability"
	"orrery_v2/internal/platform/graphprotocol"
)

const (
	productionReliabilityFailureObservedEventType       = "reliability.failure.observed"
	productionReliabilityFailureStatusChangedEventType  = "reliability.failure.status_changed"
	productionReliabilityDiagnosisCaseUpsertedEventType = "reliability.diagnosis_case.upserted"
	productionReliabilityRepairRuleUpsertedEventType    = "reliability.repair_rule.upserted"
	productionReliabilityRepairDryRunCheckedEventType   = "reliability.repair_dry_run.checked"
	productionReliabilityRepairVerificationEventType    = "reliability.repair_verification.checked"
)

func (s *productionAgentExecutionOntologySink) RecordRepairVerificationEvidence(ctx context.Context, run actioncontract.ActionRun, record repairVerificationEvidenceRecord) error {
	if s == nil || s.runner == nil || strings.TrimSpace(record.EvidenceID) == "" || strings.TrimSpace(record.IncidentID) == "" ||
		strings.TrimSpace(record.RuleID) == "" || strings.TrimSpace(record.OriginalRunID) == "" || strings.TrimSpace(record.RetryRunID) == "" ||
		strings.TrimSpace(record.Status) == "" || record.ObservedAt.IsZero() {
		return fmt.Errorf("complete repair verification evidence material is required")
	}
	revision, observedAt := timeProjectionRevision(record.ObservedAt)
	actor := graphprotocol.ActorRef{Kind: string(run.Actor.Kind), ID: run.Actor.ID}
	if strings.TrimSpace(actor.Kind) == "" || strings.TrimSpace(actor.ID) == "" {
		actor = graphprotocol.ActorRef{Kind: "service", ID: "reliability-control-plane"}
	}
	return s.apply(ctx, graphprotocol.DomainEvent{
		ID:       "repair-verification-checked:" + string(run.ID) + ":" + record.EvidenceID,
		TenantID: run.TenantID, Type: productionReliabilityRepairVerificationEventType,
		StreamID: record.EvidenceID, Version: revision, OccurredAt: observedAt,
		Payload: map[string]any{
			"id": record.EvidenceID, "action_run_id": string(run.ID), "failure_incident_id": record.IncidentID, "repair_rule_id": record.RuleID,
			"evidence_kind": "repair_verification", "summary": "Retried agent run terminal health verification", "status": record.Status,
			"source_ref": record.RetryRunID, "content_ref": ontologyAgentRunID(record.RetryRunID), "original_run_id": record.OriginalRunID,
			"retry_run_id": record.RetryRunID, "retry_agent_run_id": ontologyAgentRunID(record.RetryRunID),
			"queue_status": record.QueueStatus, "failure_code": record.FailureCode,
		},
		PublishedBy: actor, SourceAccessRef: trustedOntologySourceRef("action_run", string(run.ID), run.TenantID),
	})
}

func (s *productionAgentExecutionOntologySink) RecordRepairDryRunEvidence(ctx context.Context, run actioncontract.ActionRun, record repairDryRunEvidenceRecord) error {
	if s == nil || s.runner == nil || strings.TrimSpace(record.EvidenceID) == "" || strings.TrimSpace(record.ProviderCallID) == "" ||
		strings.TrimSpace(record.ResultID) == "" || strings.TrimSpace(record.IncidentID) == "" || strings.TrimSpace(record.RuleID) == "" ||
		record.Strategy == "" || record.ObservedAt.IsZero() {
		return fmt.Errorf("complete repair dry-run evidence material is required")
	}
	revision, observedAt := timeProjectionRevision(record.ObservedAt)
	actor := graphprotocol.ActorRef{Kind: string(run.Actor.Kind), ID: run.Actor.ID}
	if strings.TrimSpace(actor.Kind) == "" || strings.TrimSpace(actor.ID) == "" {
		actor = graphprotocol.ActorRef{Kind: "service", ID: "reliability-control-plane"}
	}
	return s.apply(ctx, graphprotocol.DomainEvent{
		ID:       "repair-dry-run-checked:" + string(run.ID) + ":" + record.EvidenceID,
		TenantID: run.TenantID, Type: productionReliabilityRepairDryRunCheckedEventType,
		StreamID: record.EvidenceID, Version: revision, OccurredAt: observedAt,
		Payload:     repairDryRunEvidencePayload(run, record),
		PublishedBy: actor, SourceAccessRef: trustedOntologySourceRef("action_run", string(run.ID), run.TenantID),
	})
}

func repairDryRunEvidencePayload(run actioncontract.ActionRun, record repairDryRunEvidenceRecord) map[string]any {
	payload := map[string]any{
		"id": record.EvidenceID, "action_run_id": string(run.ID), "failure_incident_id": record.IncidentID, "repair_rule_id": record.RuleID,
		"evidence_kind": "repair_dry_run", "summary": "Governed repair dry-run checks passed", "status": "passed",
		"source_ref": record.ProviderCallID, "content_ref": record.ResultID, "checks": append([]string(nil), record.Checks...), "strategy": string(record.Strategy),
	}
	if record.Target != nil {
		payload["target_kind"] = string(record.Target.Kind)
		payload["target_observation_id"] = record.Target.ObservationID
		payload["target_mission_id"] = record.Target.MissionID
		payload["target_run_id"] = record.Target.RunID
	}
	return payload
}

func (s *productionAgentExecutionOntologySink) observeFailedActionRun(ctx context.Context, run actioncontract.ActionRun) error {
	if run.Status != actioncontract.RunStatusFailed || s.reliability == nil {
		return nil
	}
	occurredAt := run.CreatedAt
	if run.CompletedAt != nil {
		occurredAt = *run.CompletedAt
	}
	evidenceID := ""
	if len(run.EvidenceRefs) > 0 {
		evidenceID = run.EvidenceRefs[len(run.EvidenceRefs)-1].ID
	}
	_, err := s.reliability.ObserveFailure(ctx, reliability.FailureObservation{
		ID:       "action-run:" + string(run.ID) + ":" + firstNonBlank(run.ErrorCode, "unknown_error"),
		TenantID: run.TenantID, Source: "action_contract", Component: string(run.ActionTypeID),
		ErrorCode: run.ErrorCode, Message: run.ErrorMessage, ActionRunID: string(run.ID), EvidenceID: evidenceID,
		CausationID: string(run.ID), Actor: reliability.ActorRef{Kind: string(run.Actor.Kind), ID: run.Actor.ID}, OccurredAt: occurredAt,
	})
	return err
}

func (s *productionAgentExecutionOntologySink) projectReliabilityActionRun(ctx context.Context, run actioncontract.ActionRun) error {
	return errors.Join(s.observeFailedActionRun(ctx, run), s.recordRepairActionOutcome(ctx, run))
}

func (s *productionAgentExecutionOntologySink) recordRepairActionOutcome(ctx context.Context, run actioncontract.ActionRun) error {
	if run.ActionTypeID != actioncontract.ActionTypeID("reliability.repair.execute") || s.reliability == nil ||
		run.ExecutionMode == actioncontract.ExecutionModeDryRun ||
		(run.Status != actioncontract.RunStatusCompleted && run.Status != actioncontract.RunStatusFailed) {
		return nil
	}
	if run.Status == actioncontract.RunStatusFailed && !strings.HasPrefix(strings.TrimSpace(run.ErrorCode), "repair_retry_terminal_") {
		// Pre-dispatch and governance failures did not test the rule against the
		// target, so they must not poison its learning counters.
		return nil
	}
	incidentID, ruleID, err := repairActionTargets(run.ObjectRefs)
	if err != nil {
		return err
	}
	evidenceID := repairActionTerminalEvidenceID(run.EvidenceRefs)
	if evidenceID == "" {
		return fmt.Errorf("repair action run %s has no terminal evidence_item", run.ID)
	}
	occurredAt := run.CreatedAt
	if run.CompletedAt != nil {
		occurredAt = *run.CompletedAt
	}
	_, err = s.reliability.RecordRepairOutcome(ctx, reliability.RepairOutcome{
		ID: "repair-outcome:action-run:" + string(run.ID), TenantID: run.TenantID,
		RuleID: ruleID, IncidentID: incidentID, Success: run.Status == actioncontract.RunStatusCompleted,
		EvidenceID: evidenceID, Actor: reliability.ActorRef{Kind: string(run.Actor.Kind), ID: run.Actor.ID}, OccurredAt: occurredAt,
	})
	return err
}

func repairActionTargets(refs []actioncontract.ObjectRef) (string, string, error) {
	incidentID, ruleID := "", ""
	for _, ref := range refs {
		switch strings.TrimSpace(ref.Kind) {
		case "failure_incident":
			if incidentID != "" {
				return "", "", fmt.Errorf("repair action requires exactly one failure_incident target")
			}
			incidentID = strings.TrimSpace(ref.ID)
		case "repair_rule":
			if ruleID != "" {
				return "", "", fmt.Errorf("repair action requires exactly one repair_rule target")
			}
			ruleID = strings.TrimSpace(ref.ID)
		default:
			return "", "", fmt.Errorf("repair action target kind %q is not supported", ref.Kind)
		}
	}
	if incidentID == "" || ruleID == "" {
		return "", "", fmt.Errorf("repair action requires one failure_incident and one repair_rule target")
	}
	return incidentID, ruleID, nil
}

func repairActionTerminalEvidenceID(refs []actioncontract.Ref) string {
	for index := len(refs) - 1; index >= 0; index-- {
		if strings.TrimSpace(refs[index].Kind) == "evidence_item" && strings.TrimSpace(refs[index].ID) != "" {
			return strings.TrimSpace(refs[index].ID)
		}
	}
	return ""
}

func (s *productionAgentExecutionOntologySink) PublishRuntimeFailure(ctx context.Context, event runtimedaemon.RuntimeFailureEvent) error {
	lifecycleErr := s.PublishRuntimeLifecycle(ctx, runtimedaemon.RuntimeLifecycleEvent{
		Kind: event.Kind, TenantID: event.TenantID, MissionID: event.MissionID, TaskID: event.TaskID,
		AgentID: event.AgentID, RunID: event.RunID, AgentTaskID: event.AgentTaskID,
		ScheduledTaskID: event.ScheduledTaskID, ScheduledRunID: event.ScheduledRunID,
		RuntimeKind: event.RuntimeKind, QueueID: event.QueueID, Status: firstNonBlank(event.Code, string(event.Kind)),
		WorkerID: event.WorkerID, Message: reliability.RedactFailureSummary(event.Message), TraceRef: event.TraceRef,
	})
	if s.reliability == nil || event.Kind == runtimedaemon.RuntimeLifecycleCancelled {
		return lifecycleErr
	}
	observation := reliability.FailureObservation{
		ID:       "runtime:" + event.RunID.String() + ":" + string(event.Kind) + ":" + firstNonBlank(event.Code, "unknown_error"),
		TenantID: runtimeFailureOntologyTenantID(event), Source: "agent_runtime", Component: firstNonBlank(event.RuntimeKind, "agent_runtime"),
		ErrorCode: event.Code, Message: event.Message,
		CausationID: event.RunID.String(), Actor: reliability.ActorRef{Kind: "agent", ID: string(event.AgentID)},
	}
	if strings.TrimSpace(event.ScheduledTaskID) == "" && strings.TrimSpace(string(event.MissionID)) != "" && strings.TrimSpace(event.RunID.String()) != "" {
		authorizationTenantID := strings.TrimSpace(event.TenantID)
		if baseTenantID, _, scoped := canonicalMissionTenantSource(authorizationTenantID); scoped {
			authorizationTenantID = baseTenantID
		}
		if authorizationTenantID == "" {
			authorizationTenantID = "global"
		}
		observation.RepairTarget = &reliability.RepairTarget{
			Kind: reliability.RepairTargetMissionAgentRun, AuthorizationTenantID: authorizationTenantID,
			MissionID: string(event.MissionID), RunID: event.RunID.String(),
		}
	}
	if lifecycleErr == nil {
		observation.AgentRunID = ontologyAgentRunID(event.RunID.String())
	}
	if observation.Actor.ID == "" {
		observation.Actor = reliability.ActorRef{Kind: "worker", ID: firstNonBlank(event.WorkerID, "agent-runtime")}
	}
	if event.Kind == runtimedaemon.RuntimeLifecycleBlocked {
		observation.Category = reliability.FailureCategorySemantic
		observation.Severity = reliability.FailureSeverityWarning
		retryable := false
		observation.Retryable = &retryable
	}
	_, reliabilityErr := s.reliability.ObserveFailure(ctx, observation)
	return errors.Join(lifecycleErr, reliabilityErr)
}

func (s *productionAgentExecutionOntologySink) PublishReliabilityEvent(ctx context.Context, event reliability.Event) error {
	if s == nil || s.runner == nil {
		return fmt.Errorf("agent execution ontology projection runner is required")
	}
	domainEvent, err := productionReliabilityDomainEvent(event)
	if err != nil {
		return err
	}
	return errors.Join(s.apply(ctx, domainEvent), s.mirrorReliabilityProblemEvent(ctx, event))
}

func (s *productionAgentExecutionOntologySink) ReplayReliabilityEvent(ctx context.Context, event reliability.Event) error {
	if s == nil || s.runner == nil {
		return fmt.Errorf("agent execution ontology projection runner is required")
	}
	domainEvent, err := productionReliabilityDomainEvent(event)
	if err != nil {
		return err
	}
	return errors.Join(s.replayRetainedOntologyEvent(ctx, domainEvent), s.mirrorReliabilityProblemEvent(ctx, event))
}

func productionReliabilityDomainEvent(event reliability.Event) (graphprotocol.DomainEvent, error) {
	sourceKind := "failure_incident"
	switch event.Type {
	case productionReliabilityFailureObservedEventType, productionReliabilityFailureStatusChangedEventType:
	case productionReliabilityDiagnosisCaseUpsertedEventType:
		sourceKind = "diagnosis_case"
	case productionReliabilityRepairRuleUpsertedEventType:
		sourceKind = "repair_rule"
	default:
		return graphprotocol.DomainEvent{}, fmt.Errorf("unsupported reliability ontology event type %q", event.Type)
	}
	actor := graphprotocol.ActorRef{Kind: event.Actor.Kind, ID: event.Actor.ID}
	if strings.TrimSpace(actor.Kind) == "" || strings.TrimSpace(actor.ID) == "" {
		actor = graphprotocol.ActorRef{Kind: "service", ID: "reliability-control-plane"}
	}
	return graphprotocol.DomainEvent{
		ID: event.ID, SourceEventID: event.ID, TenantID: event.TenantID, Type: event.Type,
		SchemaVersion: "v1", StreamID: event.StreamID, Version: event.Version,
		CorrelationID: event.CorrelationID, CausationID: event.CausationID, OccurredAt: event.OccurredAt,
		Payload: event.Payload, PublishedBy: actor,
		SourceAccessRef: trustedOntologySourceRef(sourceKind, event.StreamID, event.TenantID),
	}, nil
}

func runtimeFailureOntologyTenantID(event runtimedaemon.RuntimeFailureEvent) string {
	tenantID := strings.TrimSpace(event.TenantID)
	missionID := strings.TrimSpace(string(event.MissionID))
	if strings.TrimSpace(event.ScheduledTaskID) == "" && missionID != "" {
		baseTenantID, scopedMissionID, scoped := canonicalMissionTenantSource(tenantID)
		if !scoped {
			tenantID = missionScopeTenantID(tenantID, missionID)
		} else if scopedMissionID != missionID {
			tenantID = missionScopeTenantID(baseTenantID, missionID)
		}
	}
	if tenantID == "" {
		return "system"
	}
	return tenantID
}
