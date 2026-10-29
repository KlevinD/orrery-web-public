package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"orrery_v2/internal/agent/outputpolicy"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/improvement"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/app/problemhub"
	"orrery_v2/internal/app/reliability"
	schedulerapp "orrery_v2/internal/app/scheduler"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
)

type productionAgentExecutionOntologySink struct {
	runner       *ontology.MappingRuleProjectionRunner
	state        productionOntologyStateProjector
	events       productionOntologyEventLog
	repo         *schedulerapp.Repository
	profiles     productionAgentProfileReader
	reliability  *reliability.Service
	problems     *problemhub.Service
	improvements *improvement.Store
	tasks        sync.Map
}

const scheduledRunOntologyRetention = 90 * 24 * time.Hour

var productionActionTargetEventTypes = map[string]string{
	"mission":                   "action.run.targeted.mission",
	"task":                      "action.run.targeted.task",
	"person":                    "action.run.targeted.person",
	"team":                      "action.run.targeted.team",
	"department":                "action.run.targeted.department",
	"position":                  "action.run.targeted.position",
	"role_assignment":           "action.run.targeted.role_assignment",
	"agent_run":                 "action.run.targeted.agent_run",
	"responsibility_assignment": "action.run.targeted.responsibility_assignment",
	"responsibility_context":    "action.run.targeted.responsibility_context",
	"responsibility_policy":     "action.run.targeted.responsibility_policy",
	"tool":                      "action.run.targeted.tool",
	"mcp_server":                "action.run.targeted.mcp_server",
	"idea":                      "action.run.targeted.idea",
	"requirement":               "action.run.targeted.requirement",
	"decision":                  "action.run.targeted.decision",
	"design_artifact":           "action.run.targeted.design_artifact",
	"iris_snapshot":             "action.run.targeted.iris_snapshot",
	"contract":                  "action.run.targeted.contract",
	"policy":                    "action.run.targeted.policy",
	"approval_case":             "action.run.targeted.approval_case",
	"repository":                "action.run.targeted.repository",
	"issue":                     "action.run.targeted.issue",
	"pull_request":              "action.run.targeted.pull_request",
	"release":                   "action.run.targeted.release",
	"metric":                    "action.run.targeted.metric",
	"value_objective":           "action.run.targeted.value_objective",
	"initiative":                "action.run.targeted.initiative",
	"failure_incident":          "action.run.targeted.failure_incident",
	"repair_rule":               "action.run.targeted.repair_rule",
}

func newProductionAgentExecutionServices(core *db.Store, runner *ontology.MappingRuleProjectionRunner, schedulerRepository *schedulerapp.Repository, profiles productionAgentProfileReader, authorizer ontologyActionAuthorizer, ids ontologyActionIDGenerator, executions *productionOntologyExecutionAPI, now func() time.Time, metrics ontologyMetricRecorder, scopeGate *ontologyscope.ScopeAccessGate, decisions *hil.Service, commands missionAgentRetryCommand, queue repairRuntimeQueue, memoryRepository memstore.Repository) (*productionAgentExecutionOntologySink, *productionOntologyActionAPI, *productionRepairExecutionReconciler, *productionOntologyReliabilityAPI, *productionOntologyProblemAPI) {
	sink := &productionAgentExecutionOntologySink{
		runner:   runner,
		profiles: profiles,
		state: productionOntologyStateProjector{
			runner:  runner,
			store:   ontology.NewPlatformStateProjectionStore(core),
			now:     now,
			metrics: metrics,
		},
		events:       newProductionOntologyEventLog(core),
		repo:         schedulerRepository,
		improvements: improvement.NewStore(core),
	}
	reliabilityRepository := reliability.NewPlatformRepository(core)
	problemRepository := problemhub.NewPlatformRepository(core)
	problemControl := problemhub.NewService(problemRepository, problemhub.WithClock(now), problemhub.WithEventSink(sink), problemhub.WithLessonSink(problemhub.NewMemoryLessonSink(memoryRepository)))
	sink.problems = problemControl
	activationAuthorizer := productionRepairRuleActivationAuthorizer{repository: reliabilityRepository, decisions: decisions, now: now}
	reliabilityControl := reliability.NewService(reliabilityRepository, reliability.WithClock(now), reliability.WithEventSink(sink), reliability.WithRepairRuleActivationAuthorizer(activationAuthorizer))
	sink.reliability = reliabilityControl
	schedulerRepository.SetProjectionSink(sink)
	actionRepository := actioncontract.NewPlatformRepository(core)
	actionService := actioncontract.NewService(actionRepository,
		actioncontract.WithClock(now),
		actioncontract.WithAuthorizationVerifier(productionActionHILAuthorizer{decisions: decisions}),
		actioncontract.WithRunLifecycleSink(sink),
	)
	dryRunExecutor := newProductionRepairDryRunExecutor(reliabilityRepository, reliability.NewDefaultRepairProviderRegistry(), actionService, sink, now)
	applyExecutor := &productionRepairApplyExecutor{
		repository: reliabilityRepository, control: reliabilityControl, actions: actionService, commands: commands, evidence: sink, now: now,
	}
	repairExecutor := &productionRepairExecutor{dryRun: dryRunExecutor, apply: applyExecutor}
	reconciler := &productionRepairExecutionReconciler{
		repository: reliabilityRepository, control: reliabilityControl, actions: actionService, queue: queue, evidence: sink, now: now,
	}
	actionAPI := newProductionOntologyActionAPI(actionRepository, actionService, authorizer, ids, executions, repairExecutor)
	reliabilityAPI := newProductionOntologyReliabilityAPI(reliabilityRepository, reliabilityControl, executions.context, scopeGate, decisions, actionAPI, now)
	problemAPI := newProductionOntologyProblemAPI(problemControl, scopeGate)
	return sink, actionAPI, reconciler, reliabilityAPI, problemAPI
}

func (s *productionAgentExecutionOntologySink) ProjectActionRun(ctx context.Context, run actioncontract.ActionRun) error {
	if s == nil || s.runner == nil {
		return fmt.Errorf("agent execution ontology projection runner is required")
	}
	events, err := actionRunLifecycleOntologyEvents(run)
	if err != nil {
		return err
	}
	if err := projectActionOntologyEvents(ctx, events, productionActionOntologyProjectionRetryDelays, s.apply); err != nil {
		return err
	}
	return s.projectReliabilityActionRun(ctx, run)
}

func actionRunLifecycleOntologyEvents(run actioncontract.ActionRun) ([]graphprotocol.DomainEvent, error) {
	events, err := actionRunOntologyEvents(run)
	if err != nil || run.Status == actioncontract.RunStatusStarted {
		return events, err
	}
	for _, event := range events {
		if event.Type == "action.run.completed" {
			return []graphprotocol.DomainEvent{event}, nil
		}
	}
	return nil, fmt.Errorf("terminal action run %s has no completion event", run.ID)
}

func actionRunOntologyEvents(run actioncontract.ActionRun) ([]graphprotocol.DomainEvent, error) {
	startedRevision, startedAt := timeProjectionRevision(run.CreatedAt)
	actor := graphprotocol.ActorRef{Kind: string(run.Actor.Kind), ID: run.Actor.ID}
	source := trustedOntologySourceRef("action_run", string(run.ID), run.TenantID)
	executionMode := run.ExecutionMode
	if executionMode == "" {
		executionMode = actioncontract.ExecutionModeApply
	}
	payload := map[string]any{
		"id": string(run.ID), "action_type_id": string(run.ActionTypeID), "status": string(actioncontract.RunStatusStarted), "risk_level": string(run.RiskLevel),
		"execution_mode":     string(executionMode),
		"policy_decision_id": run.PolicyDecisionID, "approval_case_id": run.Approval.ID, "hil_token_id": run.HILTokenID,
		"actor_kind": string(run.Actor.Kind), "actor_id": run.Actor.ID,
		"effective_user_kind": string(run.EffectiveUser.Kind), "effective_user_id": run.EffectiveUser.ID,
		"runtime_kind": string(run.Runtime.Kind), "runtime_id": run.Runtime.ID, "args_hash": run.ArgsHash,
	}
	events := []graphprotocol.DomainEvent{{
		ID: "action-run-started:" + string(run.ID) + ":" + strconv.FormatInt(startedRevision, 10), TenantID: run.TenantID,
		Type: "action.run.started", StreamID: string(run.ID), Version: startedRevision, OccurredAt: startedAt, Payload: payload,
		PublishedBy: actor, SourceAccessRef: source,
	}}
	if run.Runtime.Kind == actioncontract.RuntimeAgent && strings.TrimSpace(run.Runtime.ID) != "" {
		events = append(events, graphprotocol.DomainEvent{
			ID: "agent-action-requested:" + string(run.ID), TenantID: run.TenantID, Type: "agent.runtime.action.requested",
			StreamID: string(run.ID), Version: startedRevision, OccurredAt: startedAt,
			Payload:     map[string]any{"agent_run_id": agentRunOntologyID(run.Runtime.ID), "action_run_id": string(run.ID)},
			PublishedBy: actor, SourceAccessRef: source,
		})
	}
	if strings.TrimSpace(run.PolicyDecisionID) != "" {
		events = append(events, graphprotocol.DomainEvent{
			ID: "action-policy-decided:" + string(run.ID), TenantID: run.TenantID, Type: "action.run.policy.decided",
			StreamID: run.PolicyDecisionID, Version: startedRevision, OccurredAt: startedAt,
			Payload:     map[string]any{"id": run.PolicyDecisionID, "action_run_id": string(run.ID), "decision": "allow", "evaluated_at": projectionTime(startedAt)},
			PublishedBy: actor, SourceAccessRef: source,
		})
	}
	if strings.TrimSpace(run.Approval.ID) != "" {
		approvalID := "approval-case:" + run.Approval.ID
		events = append(events, graphprotocol.DomainEvent{
			ID: "agent-approval-required:" + string(run.ID), TenantID: run.TenantID, Type: "agent.runtime.approval.required",
			StreamID: approvalID, Version: startedRevision, OccurredAt: startedAt,
			Payload:     map[string]any{"approval_case_id": approvalID, "action_run_id": string(run.ID), "title": "HIL approval " + run.Approval.ID, "status": "approved", "risk_level": string(run.RiskLevel)},
			PublishedBy: actor, SourceAccessRef: source,
		})
	}
	for _, ref := range run.ObjectRefs {
		eventType, supported := productionActionTargetEventTypes[ref.Kind]
		if !supported {
			return nil, fmt.Errorf("action run target object type %q is not supported by ontology projection", ref.Kind)
		}
		events = append(events, graphprotocol.DomainEvent{
			ID: "action-run-targeted:" + string(run.ID) + ":" + ref.Kind + ":" + ref.ID, TenantID: run.TenantID,
			Type: eventType, StreamID: string(run.ID), Version: startedRevision, OccurredAt: startedAt,
			Payload: map[string]any{"action_run_id": string(run.ID), "object_id": ref.ID}, PublishedBy: actor, SourceAccessRef: source,
		})
	}
	for _, ref := range run.EvidenceRefs {
		events = append(events, graphprotocol.DomainEvent{
			ID: "action-run-evidence:" + string(run.ID) + ":" + ref.ID, TenantID: run.TenantID, Type: "action.run.evidence.attached",
			StreamID: string(run.ID), Version: startedRevision, OccurredAt: startedAt,
			Payload: map[string]any{"action_run_id": string(run.ID), "evidence_id": ref.ID}, PublishedBy: actor, SourceAccessRef: source,
		})
	}
	if run.Status != actioncontract.RunStatusStarted {
		completedAt := run.CreatedAt
		if run.CompletedAt != nil {
			completedAt = *run.CompletedAt
		}
		completedRevision, completedAt := timeProjectionRevision(completedAt)
		events = append(events, graphprotocol.DomainEvent{
			ID: "action-run-completed:" + string(run.ID) + ":" + strconv.FormatInt(completedRevision, 10), TenantID: run.TenantID,
			Type: "action.run.completed", StreamID: string(run.ID), Version: completedRevision, OccurredAt: completedAt,
			Payload: map[string]any{
				"id": string(run.ID), "status": string(run.Status),
				"provider_call_ref_id": run.ProviderCallRef.ID, "provider_call_ref_kind": run.ProviderCallRef.Kind,
				"result_ref_id": run.ResultRef.ID, "result_ref_kind": run.ResultRef.Kind, "error_code": run.ErrorCode,
			},
			PublishedBy: actor, SourceAccessRef: source,
		})
	}
	return events, nil
}

func (s *productionAgentExecutionOntologySink) PublishRuntimeLifecycle(ctx context.Context, event runtimedaemon.RuntimeLifecycleEvent) error {
	if s == nil || s.runner == nil {
		return fmt.Errorf("agent execution ontology projection runner is required")
	}
	if isPersonalAssistantProfileID(event.AgentID.String()) {
		return nil
	}
	// Ontology summaries and task titles are user-facing projections. Keep the
	// raw runtime result in the durable run/trace stores, but never publish
	// private reasoning or machine manifests into graph display fields.
	event.Message = outputpolicy.PublicRuntimeText(event.Message, string(event.Kind))
	tenantID := strings.TrimSpace(event.TenantID)
	missionID := strings.TrimSpace(string(event.MissionID))
	missionTenantID := tenantID
	if strings.TrimSpace(event.ScheduledTaskID) == "" && missionID != "" {
		baseTenantID, scopedMissionID, scoped := canonicalMissionTenantSource(tenantID)
		if !scoped {
			tenantID = missionScopeTenantID(tenantID, missionID)
		} else {
			missionTenantID = baseTenantID
			if scopedMissionID != missionID {
				tenantID = missionScopeTenantID(baseTenantID, missionID)
			}
		}
	}
	if tenantID == "" {
		tenantID = "system"
	}
	runtimeRunID := strings.TrimSpace(event.RunID.String())
	if runtimeRunID == "" {
		return fmt.Errorf("agent runtime run id is required for ontology projection")
	}
	runID := ontologyAgentRunID(runtimeRunID)
	agentTaskID := strings.TrimSpace(event.AgentTaskID)
	if agentTaskID == "" {
		if strings.TrimSpace(event.ScheduledTaskID) != "" {
			agentTaskID = schedulerapp.AgentTaskIDForScheduledTask(event.ScheduledTaskID)
		} else if strings.TrimSpace(string(event.TaskID)) != "" {
			agentTaskID = "agent-task:mission:" + string(event.TaskID)
		} else {
			agentTaskID = "agent-task:run:" + runtimeRunID
		}
	}
	agentID := workforceAgentOntologyID(event.AgentID.String())
	agentVersionID := firstNonBlank(event.AgentVersionID, runtimeAgentVersionID(agentID, event.RuntimeKind))
	revision, occurredAt := runtimeEventProjectionRevision(event)
	source := trustedOntologySourceRef("agent_run", runtimeRunID, tenantID)
	agentSource := workforceAgentOntologySourceRef(tenantID, agentID)
	agentTaskSource := source
	if strings.TrimSpace(event.ScheduledTaskID) != "" {
		agentTaskSource = trustedOntologySourceRef("scheduled_task", event.ScheduledTaskID, tenantID)
	} else if missionID != "" {
		agentSource = missionOntologySourceRef(missionTenantID, missionID)
		agentTaskSource = agentSource
	}
	actor := graphprotocol.ActorRef{Kind: "agent", ID: event.AgentID.String()}

	if agentID != "" {
		if err := s.apply(ctx, graphprotocol.DomainEvent{
			ID: "runtime-agent-observed:" + agentID + ":" + strconv.FormatInt(revision, 10), TenantID: tenantID,
			Type: "people.agent.updated", StreamID: agentID, Version: revision, OccurredAt: occurredAt,
			Payload: map[string]any{
				"id": agentID, "display_name": s.agentDisplayName(ctx, event.AgentID.String()), "runtime_kind": event.RuntimeKind,
			},
			PublishedBy: actor, SourceAccessRef: agentSource,
		}); err != nil {
			return err
		}
		if err := s.apply(ctx, graphprotocol.DomainEvent{
			ID: "agent-version-observed:" + agentVersionID + ":" + strconv.FormatInt(revision, 10), TenantID: tenantID,
			Type: "agent.version.observed", StreamID: agentVersionID, Version: revision, OccurredAt: occurredAt,
			Payload: map[string]any{
				"id": agentVersionID, "agent_id": agentID, "version": firstNonBlank(event.RuntimeKind, "default"),
				"runtime_kind": event.RuntimeKind, "status": "active",
			},
			PublishedBy: actor, SourceAccessRef: agentSource,
		}); err != nil {
			return err
		}
	}
	if strings.TrimSpace(event.ScheduledTaskID) == "" {
		if err := s.apply(ctx, graphprotocol.DomainEvent{
			ID: "runtime-agent-task-upserted:" + agentTaskID + ":" + strconv.FormatInt(revision, 10), TenantID: tenantID,
			Type: "agent.task.upserted", StreamID: agentTaskID, Version: revision, OccurredAt: occurredAt,
			Payload: map[string]any{
				"id": agentTaskID, "title": firstNonBlank(event.Message, agentTaskID), "status": runtimeTaskStatus(event),
				"source_kind": runtimeSourceKind(event), "source_ref": runtimeSourceRef(event), "agent_id": agentID,
				"mission_id": string(event.MissionID), "mission_task_id": string(event.TaskID),
			},
			PublishedBy: actor, SourceAccessRef: agentTaskSource,
		}); err != nil {
			return err
		}
	}
	if event.Kind == runtimedaemon.RuntimeLifecycleStarted {
		if err := s.projectRuntimeStarted(ctx, event, tenantID, runID, agentTaskID, agentID, agentVersionID, revision, occurredAt, actor, source); err != nil {
			return err
		}
		if err := s.projectScheduledRunResolution(ctx, event, tenantID, runID, revision, occurredAt, actor); err != nil {
			return err
		}
	} else {
		if err := s.projectRuntimeCompleted(ctx, event, tenantID, runID, revision, occurredAt, actor, source); err != nil {
			return err
		}
	}
	if event.ContextSnapshotID != "" {
		if event.Kind == runtimedaemon.RuntimeLifecycleStarted {
			if err := s.projectRuntimeContext(ctx, event, tenantID, runID, revision, occurredAt, actor, source); err != nil {
				return err
			}
		} else if len(event.KnowledgeRefs) > 0 {
			if err := s.projectRuntimeContextSnapshot(ctx, event, tenantID, runID, revision, occurredAt, actor, source); err != nil {
				return err
			}
			if err := s.projectRuntimeKnowledgeRefs(ctx, event, tenantID, runID, revision, occurredAt, actor, source); err != nil {
				return err
			}
		}
	}
	if err := s.projectRuntimeTrace(ctx, event, tenantID, runID, revision, occurredAt, actor, source); err != nil {
		return err
	}
	if event.Kind == runtimedaemon.RuntimeLifecycleCompleted {
		if err := s.projectRuntimeOutputs(ctx, event, tenantID, runID, revision, occurredAt, actor, source); err != nil {
			return err
		}
	}
	return s.observeRuntimeImprovement(ctx, event, tenantID, runID, agentTaskID, source)
}

func (s *productionAgentExecutionOntologySink) projectScheduledRunResolution(ctx context.Context, event runtimedaemon.RuntimeLifecycleEvent, tenantID, runID string, revision int64, occurredAt time.Time, actor graphprotocol.ActorRef) error {
	if strings.TrimSpace(event.ScheduledRunID) == "" || strings.TrimSpace(event.ScheduledTaskID) == "" {
		return nil
	}
	return s.apply(ctx, graphprotocol.DomainEvent{
		ID: "scheduler-run-agent-resolved:" + event.ScheduledRunID + ":" + strconv.FormatInt(revision, 10), TenantID: tenantID,
		Type: "scheduler.run.agent_resolved", StreamID: event.ScheduledRunID, Version: revision, OccurredAt: occurredAt,
		Payload:         map[string]any{"id": event.ScheduledRunID, "agent_run_id": runID},
		PublishedBy:     actor,
		SourceAccessRef: trustedOntologySourceRef("scheduled_task", event.ScheduledTaskID, tenantID),
	})
}

func (s *productionAgentExecutionOntologySink) projectRuntimeStarted(ctx context.Context, event runtimedaemon.RuntimeLifecycleEvent, tenantID, runID, agentTaskID, agentID, agentVersionID string, revision int64, occurredAt time.Time, actor graphprotocol.ActorRef, source graphprotocol.AccessRef) error {
	return s.apply(ctx, graphprotocol.DomainEvent{
		ID: "agent-runtime-started:" + runID + ":" + strconv.FormatInt(revision, 10), TenantID: tenantID,
		Type: "agent.runtime.run.started", StreamID: runID, Version: revision, OccurredAt: occurredAt,
		Payload: map[string]any{
			"id": runID, "runtime_run_id": event.RunID.String(), "status": firstNonBlank(event.Status, "running"), "agent_id": agentID, "agent_task_id": agentTaskID,
			"agent_version_id": agentVersionID, "source_kind": runtimeSourceKind(event), "source_ref": runtimeSourceRef(event),
			"scheduled_run_id": event.ScheduledRunID, "trace_ref": event.TraceRef, "started_at": projectionTime(occurredAt),
		},
		PublishedBy: actor, SourceAccessRef: source,
	})
}

func (s *productionAgentExecutionOntologySink) projectRuntimeCompleted(ctx context.Context, event runtimedaemon.RuntimeLifecycleEvent, tenantID, runID string, revision int64, occurredAt time.Time, actor graphprotocol.ActorRef, source graphprotocol.AccessRef) error {
	return s.apply(ctx, graphprotocol.DomainEvent{
		ID: "agent-runtime-completed:" + runID + ":" + strconv.FormatInt(revision, 10) + ":" + string(event.Kind), TenantID: tenantID,
		Type: "agent.runtime.run.completed", StreamID: runID, Version: revision, OccurredAt: occurredAt,
		Payload: map[string]any{
			"id": runID, "status": firstNonBlank(event.Status, string(event.Kind)), "summary": event.Message,
			"outcome": string(event.Kind), "completed_at": projectionTime(occurredAt), "duration_ms": event.DurationMs,
		},
		PublishedBy: actor, SourceAccessRef: source,
	})
}

func (s *productionAgentExecutionOntologySink) projectRuntimeContext(ctx context.Context, event runtimedaemon.RuntimeLifecycleEvent, tenantID, runID string, revision int64, occurredAt time.Time, actor graphprotocol.ActorRef, source graphprotocol.AccessRef) error {
	if err := s.projectRuntimeContextSnapshot(ctx, event, tenantID, runID, revision, occurredAt, actor, source); err != nil {
		return err
	}
	seenMemory := make(map[string]bool, len(event.MemoryRefs))
	for _, ref := range event.MemoryRefs {
		memoryID := strings.TrimSpace(ref.ID)
		if memoryID == "" || seenMemory[memoryID] {
			continue
		}
		seenMemory[memoryID] = true
		if err := s.apply(ctx, graphprotocol.DomainEvent{
			ID: "agent-runtime-context-memory:" + runID + ":" + memoryID + ":" + strconv.FormatInt(revision, 10), TenantID: tenantID,
			Type: "agent.runtime.context.memory.attached", StreamID: event.ContextSnapshotID, Version: revision, OccurredAt: occurredAt,
			Payload: map[string]any{
				"context_snapshot_id": event.ContextSnapshotID, "memory_digest_id": memoryID,
				"memory_kind": ref.MemoryKind, "summary_ref": ref.SummaryRef, "sensitivity": ref.Sensitivity,
				"captured_at": projectionTime(occurredAt),
			},
			PublishedBy: actor, SourceAccessRef: source,
		}); err != nil {
			return err
		}
	}
	return s.projectRuntimeKnowledgeRefs(ctx, event, tenantID, runID, revision, occurredAt, actor, source)
}

func (s *productionAgentExecutionOntologySink) projectRuntimeKnowledgeRefs(ctx context.Context, event runtimedaemon.RuntimeLifecycleEvent, tenantID, runID string, revision int64, occurredAt time.Time, actor graphprotocol.ActorRef, source graphprotocol.AccessRef) error {
	seenKnowledge := make(map[string]bool, len(event.KnowledgeRefs))
	for _, ref := range event.KnowledgeRefs {
		kind := strings.TrimSpace(ref.Kind)
		knowledgeID := strings.TrimSpace(ref.ID)
		key := kind + ":" + knowledgeID
		eventType := runtimeKnowledgeEventType(kind)
		if knowledgeID == "" || seenKnowledge[key] || eventType == "" {
			continue
		}
		seenKnowledge[key] = true
		if err := s.apply(ctx, graphprotocol.DomainEvent{
			ID: "agent-runtime-context-knowledge:" + runID + ":" + key + ":" + strconv.FormatInt(revision, 10), TenantID: tenantID,
			Type: eventType, StreamID: event.ContextSnapshotID, Version: revision, OccurredAt: occurredAt,
			Payload:     map[string]any{"context_snapshot_id": event.ContextSnapshotID, "knowledge_ref_id": knowledgeID},
			PublishedBy: actor, SourceAccessRef: source,
		}); err != nil {
			return err
		}
	}
	return nil
}

func runtimeKnowledgeEventType(kind string) string {
	switch kind {
	case "knowledge_document":
		return "agent.runtime.context.knowledge_document.attached"
	case "knowledge_chunk":
		return "agent.runtime.context.knowledge_chunk.attached"
	case "knowledge_anchor":
		return "agent.runtime.context.knowledge_anchor.attached"
	default:
		return ""
	}
}

func (s *productionAgentExecutionOntologySink) projectRuntimeTrace(ctx context.Context, event runtimedaemon.RuntimeLifecycleEvent, tenantID, runID string, revision int64, occurredAt time.Time, actor graphprotocol.ActorRef, source graphprotocol.AccessRef) error {
	traceID := firstNonBlank(event.TraceID, "trace:"+runID)
	return s.apply(ctx, graphprotocol.DomainEvent{
		ID: "agent-runtime-trace:" + runID + ":" + strconv.FormatInt(revision, 10) + ":" + string(event.Kind), TenantID: tenantID,
		Type: "agent.runtime.trace.recorded", StreamID: traceID, Version: revision, OccurredAt: occurredAt,
		Payload: map[string]any{
			"id": traceID, "agent_run_id": runID, "trace_id": traceID, "trace_ref": event.TraceRef,
			"summary": event.Message, "started_at": projectionTime(event.StartedAt), "completed_at": projectionTime(event.CompletedAt),
		},
		PublishedBy: actor, SourceAccessRef: source,
	})
}

func (s *productionAgentExecutionOntologySink) projectRuntimeOutputs(ctx context.Context, event runtimedaemon.RuntimeLifecycleEvent, tenantID, runID string, revision int64, occurredAt time.Time, actor graphprotocol.ActorRef, source graphprotocol.AccessRef) error {
	for _, artifact := range event.Artifacts {
		if strings.TrimSpace(artifact.ID) == "" {
			continue
		}
		if err := s.apply(ctx, graphprotocol.DomainEvent{
			ID: "agent-runtime-artifact:" + runID + ":" + artifact.ID, TenantID: tenantID,
			Type: "agent.runtime.artifact.produced", StreamID: artifact.ID, Version: revision, OccurredAt: occurredAt,
			Payload: map[string]any{
				"id": artifact.ID, "agent_run_id": runID, "title": artifact.ID, "artifact_type": string(artifact.Kind),
				"uri": artifact.URI, "status": "produced",
			},
			PublishedBy: actor, SourceAccessRef: source,
		}); err != nil {
			return err
		}
	}
	for _, evidence := range event.Evidence {
		if strings.TrimSpace(evidence.ID) == "" {
			continue
		}
		if err := s.apply(ctx, graphprotocol.DomainEvent{
			ID: "agent-runtime-evidence:" + runID + ":" + evidence.ID, TenantID: tenantID,
			Type: "agent.runtime.evidence.produced", StreamID: evidence.ID, Version: revision, OccurredAt: occurredAt,
			Payload: map[string]any{
				"id": evidence.ID, "agent_run_id": runID, "evidence_kind": evidence.Kind, "summary": evidence.Summary,
				"source_ref": evidence.SourceRef, "content_ref": evidence.ContentRef, "checksum": evidence.Checksum,
				"status": firstNonBlank(evidence.Status, "produced"),
			},
			PublishedBy: actor, SourceAccessRef: source,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (s *productionAgentExecutionOntologySink) ProjectScheduledTask(ctx context.Context, task schedulerapp.ScheduledTask) error {
	if s == nil || s.runner == nil {
		return fmt.Errorf("agent execution ontology projection runner is required")
	}
	_, err := s.projectScheduledTaskState(ctx, task)
	return err
}

func scheduledTaskOntologyEventsForTenantWithAgentDisplayName(task schedulerapp.ScheduledTask, tenantID, agentDisplayName string) []graphprotocol.DomainEvent {
	if isPersonalAssistantProfileID(task.Target.AgentID) {
		return nil
	}
	revision, occurredAt := schedulerTaskProjectionRevision(task)
	actor := graphprotocol.ActorRef{Kind: string(task.CreatedByKind), ID: task.CreatedBy}
	if strings.TrimSpace(actor.Kind) == "" {
		actor.Kind = "system"
	}
	if strings.TrimSpace(actor.ID) == "" {
		actor.ID = "scheduler"
	}
	source := scheduledTaskProjectionAccessRef(task, tenantID)
	ontologyAgentID := workforceAgentOntologyID(task.Target.AgentID)
	events := make([]graphprotocol.DomainEvent, 0, 3)
	if ontologyAgentID != "" {
		events = append(events, graphprotocol.DomainEvent{
			ID: scheduledProjectionEventID("scheduler-agent-observed:"+ontologyAgentID+":"+strconv.FormatInt(revision, 10), task.TenantID, tenantID), TenantID: tenantID,
			Type: "people.agent.updated", StreamID: ontologyAgentID, Version: revision, OccurredAt: occurredAt,
			Payload:     map[string]any{"id": ontologyAgentID, "display_name": firstNonBlank(agentDisplayName, task.Target.AgentID), "runtime_kind": ""},
			PublishedBy: actor, SourceAccessRef: workforceAgentOntologySourceRef(tenantID, ontologyAgentID),
		})
	}
	events = append(events, graphprotocol.DomainEvent{
		ID: scheduledProjectionEventID("agent-task-upserted:"+task.AgentTaskID+":"+strconv.FormatInt(revision, 10), task.TenantID, tenantID), TenantID: tenantID,
		Type: "agent.task.upserted", StreamID: task.AgentTaskID, Version: revision, OccurredAt: occurredAt,
		Payload: map[string]any{
			"id": task.AgentTaskID, "title": task.Title, "description": task.Description, "status": string(task.Status),
			"source_kind": "scheduled_task", "source_ref": task.ScheduledTaskID, "input_ref": string(task.Target.Mode),
			"agent_id": ontologyAgentID, "created_by": task.CreatedBy, "target_mode": string(task.Target.Mode),
			"source_scope_kind": task.Target.SourceScopeKind, "source_scope_id": task.Target.SourceScopeID,
			"target_scope_kind": task.Target.TargetScopeKind, "target_scope_id": task.Target.TargetScopeID,
		},
		PublishedBy: actor, SourceAccessRef: source,
	}, graphprotocol.DomainEvent{
		ID: scheduledProjectionEventID("scheduler-task-projected:"+task.ScheduledTaskID+":"+strconv.FormatInt(revision, 10), task.TenantID, tenantID), TenantID: tenantID,
		Type: schedulerTaskOntologyEventType(task), StreamID: task.ScheduledTaskID, Version: revision, OccurredAt: occurredAt,
		Payload: map[string]any{
			"id": task.ScheduledTaskID, "agent_task_id": task.AgentTaskID, "title": task.Title, "description": task.Description,
			"cron": task.Schedule.Cron, "agent_id": task.Target.AgentID, "status": string(task.Status), "enabled": task.Enabled,
			"target_mode": string(task.Target.Mode), "source_scope_kind": task.Target.SourceScopeKind, "source_scope_id": task.Target.SourceScopeID,
			"target_scope_kind": task.Target.TargetScopeKind, "target_scope_id": task.Target.TargetScopeID,
		},
		PublishedBy: actor, SourceAccessRef: source,
	})
	if ontologyAgentID != "" {
		events = append(events, graphprotocol.DomainEvent{
			ID: scheduledProjectionEventID("scheduler-task-agent-targeted:"+task.ScheduledTaskID+":"+strconv.FormatInt(revision, 10), task.TenantID, tenantID), TenantID: tenantID,
			Type: "scheduler.task.agent_targeted", StreamID: task.ScheduledTaskID, Version: revision, OccurredAt: occurredAt,
			Payload:         map[string]any{"scheduled_task_id": task.ScheduledTaskID, "agent_id": ontologyAgentID},
			PublishedBy:     actor,
			SourceAccessRef: source,
		})
	}
	return events
}

func (s *productionAgentExecutionOntologySink) ProjectScheduledRun(ctx context.Context, run schedulerapp.ScheduledRun) error {
	if s == nil || s.runner == nil {
		return fmt.Errorf("agent execution ontology projection runner is required")
	}
	task, err := s.scheduledTask(ctx, run.ScheduledTaskID)
	if err != nil {
		return err
	}
	for _, event := range scheduledRunOntologyEvents(task, run) {
		if err := s.apply(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func scheduledRunOntologyEvents(task schedulerapp.ScheduledTask, run schedulerapp.ScheduledRun) []graphprotocol.DomainEvent {
	return scheduledRunOntologyEventsAt(task, run, time.Now().UTC())
}

func scheduledRunOntologyEventsAt(task schedulerapp.ScheduledTask, run schedulerapp.ScheduledRun, now time.Time) []graphprotocol.DomainEvent {
	if isPersonalAssistantProfileID(task.Target.AgentID) {
		return nil
	}
	if !shouldProjectScheduledRun(task, run, now) {
		return nil
	}
	var events []graphprotocol.DomainEvent
	for _, tenantID := range scheduledTaskProjectionTenantIDs(task) {
		events = append(events, scheduledRunOntologyEventsForTenant(task, run, tenantID)...)
	}
	return events
}

func shouldProjectScheduledRun(task schedulerapp.ScheduledTask, run schedulerapp.ScheduledRun, now time.Time) bool {
	if strings.TrimSpace(task.ScheduledTaskID) == defaultResponsibilityReconcileScheduledTaskID {
		return false
	}
	anchor := run.CompletedAt
	if anchor.IsZero() {
		anchor = run.StartedAt
	}
	if anchor.IsZero() {
		anchor = run.CreatedAt
	}
	if anchor.IsZero() {
		return true
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return !anchor.UTC().Before(now.UTC().Add(-scheduledRunOntologyRetention))
}

func scheduledRunOntologyEventsForTenant(task schedulerapp.ScheduledTask, run schedulerapp.ScheduledRun, tenantID string) []graphprotocol.DomainEvent {
	revision, occurredAt := schedulerRunProjectionRevision(run)
	payload := map[string]any{
		"id": run.ScheduledRunID, "scheduled_task_id": run.ScheduledTaskID, "status": string(run.Status),
		"scheduled_for": projectionTime(task.NextRunAt), "started_at": projectionTime(run.StartedAt), "completed_at": projectionTime(run.CompletedAt),
		"error_summary": run.FailureReason,
	}
	if run.TargetMode == schedulerapp.TargetModeAgentTask {
		payload["agent_run_id"] = ontologyAgentRunID(firstNonBlank(run.CommandID, run.ScheduledRunID))
		if run.Status == schedulerapp.RunStatusQueued || run.Status == schedulerapp.RunStatusRunning {
			payload["agent_run_ref_status"] = "unresolved"
		}
	}
	return []graphprotocol.DomainEvent{{
		ID: scheduledProjectionEventID(
			"scheduler-run-projected:"+run.ScheduledRunID+":"+strconv.FormatInt(revision, 10)+":"+string(run.Status),
			task.TenantID,
			tenantID,
		),
		TenantID: tenantID, Type: schedulerRunOntologyEventType(run), StreamID: run.ScheduledRunID, Version: revision,
		OccurredAt: occurredAt, Payload: payload,
		PublishedBy:     graphprotocol.ActorRef{Kind: "system", ID: "scheduler"},
		SourceAccessRef: scheduledTaskProjectionAccessRef(task, tenantID),
	}}
}

func scheduledTaskProjectionTenantIDs(task schedulerapp.ScheduledTask) []string {
	tenantID := strings.TrimSpace(task.TenantID)
	if tenantID != "system" {
		return []string{tenantID}
	}
	return []string{tenantID, defaultSystemSupportOrganizationID}
}

func scheduledTaskProjectionAccessRef(task schedulerapp.ScheduledTask, tenantID string) graphprotocol.AccessRef {
	sourceTenantID := strings.TrimSpace(task.TenantID)
	if tenantID != sourceTenantID {
		return organizationOntologySourceRef(tenantID)
	}
	return trustedOntologySourceRef("scheduled_task", task.ScheduledTaskID, sourceTenantID)
}

func scheduledProjectionEventID(base, sourceTenantID, targetTenantID string) string {
	if strings.TrimSpace(sourceTenantID) == strings.TrimSpace(targetTenantID) {
		return base
	}
	return base + ":scope:" + strings.TrimSpace(targetTenantID)
}

func (s *productionAgentExecutionOntologySink) scheduledTask(ctx context.Context, scheduledTaskID string) (schedulerapp.ScheduledTask, error) {
	if value, ok := s.tasks.Load(scheduledTaskID); ok {
		return value.(schedulerapp.ScheduledTask), nil
	}
	if s.repo == nil {
		return schedulerapp.ScheduledTask{}, fmt.Errorf("scheduled task %s is unavailable for ontology projection", scheduledTaskID)
	}
	task, err := s.repo.GetTask(ctx, scheduledTaskID)
	if err != nil {
		return schedulerapp.ScheduledTask{}, err
	}
	s.tasks.Store(scheduledTaskID, task)
	return task, nil
}

func (s *productionAgentExecutionOntologySink) apply(ctx context.Context, event graphprotocol.DomainEvent) error {
	if isProductionOntologyStateEvent(event.Type) {
		_, err := s.state.Apply(ctx, event)
		return err
	}
	if s.events.store != nil {
		if err := s.events.Record(ctx, event); err != nil {
			return err
		}
	}
	_, err := s.runner.ApplyEvent(ctx, event)
	if err != nil {
		return fmt.Errorf("project ontology event %s: %w", event.Type, err)
	}
	return nil
}

func schedulerTaskOntologyEventType(task schedulerapp.ScheduledTask) string {
	if task.Status == schedulerapp.TaskStatusArchived {
		return "scheduler.task.cancelled"
	}
	if !task.CreatedAt.IsZero() && task.UpdatedAt.Equal(task.CreatedAt) {
		return "scheduler.task.created"
	}
	return "scheduler.task.updated"
}

func schedulerRunOntologyEventType(run schedulerapp.ScheduledRun) string {
	switch run.Status {
	case schedulerapp.RunStatusCompleted, schedulerapp.RunStatusFailed, schedulerapp.RunStatusSkipped:
		return "scheduler.run.completed"
	default:
		return "scheduler.run.started"
	}
}

func schedulerTaskProjectionRevision(task schedulerapp.ScheduledTask) (int64, time.Time) {
	occurredAt := task.UpdatedAt
	if occurredAt.IsZero() {
		occurredAt = task.CreatedAt
	}
	return timeProjectionRevision(occurredAt)
}

func schedulerRunProjectionRevision(run schedulerapp.ScheduledRun) (int64, time.Time) {
	occurredAt := run.CompletedAt
	if occurredAt.IsZero() {
		occurredAt = run.StartedAt
	}
	if occurredAt.IsZero() {
		occurredAt = run.CreatedAt
	}
	return timeProjectionRevision(occurredAt)
}
