package main

import (
	"context"
	"strconv"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform/graphprotocol"
)

func (s *productionAgentExecutionOntologySink) projectRuntimeContextSnapshot(ctx context.Context, event runtimedaemon.RuntimeLifecycleEvent, tenantID, runID string, revision int64, occurredAt time.Time, actor graphprotocol.ActorRef, source graphprotocol.AccessRef) error {
	if err := s.apply(ctx, graphprotocol.DomainEvent{
		ID: "agent-runtime-context:" + runID + ":" + strconv.FormatInt(revision, 10), TenantID: tenantID,
		Type: "agent.runtime.context.attached", StreamID: event.ContextSnapshotID, Version: revision, OccurredAt: occurredAt,
		Payload: map[string]any{
			"id": event.ContextSnapshotID, "agent_run_id": runID, "summary": event.ContextSummary,
			"input_refs": event.InputRefs, "memory_refs": event.MemoryRefs, "knowledge_refs": event.KnowledgeRefs,
			"captured_at": projectionTime(occurredAt),
		},
		PublishedBy: actor, SourceAccessRef: source,
	}); err != nil {
		return err
	}
	return s.projectRuntimeResponsibilityContext(ctx, event, tenantID, runID, revision, occurredAt, actor, source)
}

func (s *productionAgentExecutionOntologySink) projectRuntimeResponsibilityContext(
	ctx context.Context,
	event runtimedaemon.RuntimeLifecycleEvent,
	tenantID, runID string,
	revision int64,
	occurredAt time.Time,
	actor graphprotocol.ActorRef,
	source graphprotocol.AccessRef,
) error {
	responsibility := agentcore.CloneResponsibilityContext(event.ResponsibilityContext)
	if responsibility == nil || strings.TrimSpace(responsibility.ContextID) == "" {
		return nil
	}
	capturedAt := responsibility.CapturedAt.UTC()
	if capturedAt.IsZero() {
		capturedAt = occurredAt.UTC()
	}
	effectiveAt := responsibility.EffectiveAt.UTC()
	if effectiveAt.IsZero() {
		effectiveAt = capturedAt
	}
	payload := map[string]any{
		"id": responsibility.ContextID, "context_snapshot_id": event.ContextSnapshotID,
		"responsibility_id": responsibility.ResponsibilityID, "responsibility_version": responsibility.ResponsibilityVersion,
		"source": responsibility.Source, "actor_member_id": responsibility.ActorMemberID,
		"agent_profile_id": responsibility.AgentProfileID, "mission_id": responsibility.MissionID, "task_id": responsibility.TaskID,
		"organization_id": responsibility.OrganizationID, "organization_name": responsibility.OrganizationName,
		"position_id": responsibility.PositionID, "position_name": responsibility.PositionName,
		"summary": responsibility.Summary, "goals": responsibility.Goals, "boundaries": responsibility.Boundaries,
		"evidence_requirements": responsibility.EvidenceRequirements, "source_refs": responsibility.SourceRefs,
		"captured_at": projectionTime(capturedAt), "effective_at": projectionTime(effectiveAt),
	}
	if !responsibility.ExpiresAt.IsZero() {
		payload["expires_at"] = projectionTime(responsibility.ExpiresAt)
	}
	return s.apply(ctx, graphprotocol.DomainEvent{
		ID:       "agent-runtime-responsibility-context:" + runID + ":" + strconv.FormatInt(revision, 10),
		TenantID: tenantID, Type: "agent.runtime.context.responsibility.attached",
		StreamID: responsibility.ContextID, Version: revision, OccurredAt: occurredAt,
		Payload: payload, PublishedBy: actor, SourceAccessRef: source,
		Temporal: graphprotocol.TemporalContext{
			Mode: string(ontology.TemporalModeEvent), BusinessValidFrom: effectiveAt, SourceRecordedAt: capturedAt,
		},
	})
}
