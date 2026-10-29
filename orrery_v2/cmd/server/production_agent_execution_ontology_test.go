package main

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	responsibilityapp "orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/platform/graphprotocol"
)

func agentExecutionOntologyEventTypesForTest() []string {
	return []string{
		"action.run.started",
		"action.run.completed",
		"action.writeback.succeeded",
		"action.writeback.failed",
		"action.run.policy.decided",
		"action.run.evidence.attached",
		"scheduler.task.created",
		"scheduler.task.updated",
		"scheduler.task.cancelled",
		"scheduler.task.agent_targeted",
		"scheduler.run.started",
		"scheduler.run.completed",
		"scheduler.run.agent_resolved",
		"agent.version.observed",
		"agent.task.upserted",
		"agent.runtime.run.started",
		"agent.runtime.run.completed",
		"agent.runtime.context.attached",
		"agent.runtime.context.responsibility.attached",
		"agent.runtime.context.memory.attached",
		"agent.runtime.context.knowledge_document.attached",
		"agent.runtime.context.knowledge_chunk.attached",
		"agent.runtime.context.knowledge_anchor.attached",
		"agent.runtime.trace.recorded",
		"agent.runtime.evidence.produced",
		"agent.runtime.artifact.produced",
		"agent.runtime.action.requested",
		"agent.runtime.approval.required",
		"action.run.targeted.mission",
		"action.run.targeted.task",
		"action.run.targeted.person",
		"action.run.targeted.team",
		"action.run.targeted.department",
		"action.run.targeted.position",
		"action.run.targeted.role_assignment",
		"action.run.targeted.agent_run",
		"action.run.targeted.responsibility_assignment",
		"action.run.targeted.responsibility_context",
		"action.run.targeted.responsibility_policy",
		"action.run.targeted.tool",
		"action.run.targeted.mcp_server",
		"action.run.targeted.idea",
		"action.run.targeted.requirement",
		"action.run.targeted.decision",
		"action.run.targeted.design_artifact",
		"action.run.targeted.iris_snapshot",
		"action.run.targeted.contract",
		"action.run.targeted.policy",
		"action.run.targeted.approval_case",
		"action.run.targeted.repository",
		"action.run.targeted.issue",
		"action.run.targeted.pull_request",
		"action.run.targeted.release",
		"action.run.targeted.metric",
		"action.run.targeted.value_objective",
		"action.run.targeted.initiative",
		"action.run.targeted.failure_incident",
		"action.run.targeted.repair_rule",
	}
}

func TestProductionAgentExecutionOntologySkipsPersonalAssistantRunsAndSchedules(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-private-assistant-execution")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
	assistantID := "A009995"
	if err := agentcore.RegisterPersonalAssistantProfileID("tenant-a", "owner-execution", assistantID); err != nil {
		t.Fatal(err)
	}
	if err := sink.PublishRuntimeLifecycle(ctx, runtimedaemon.RuntimeLifecycleEvent{
		Kind: runtimedaemon.RuntimeLifecycleStarted, TenantID: "tenant-a", AgentID: agentcore.AgentID(assistantID), RunID: "run-private",
	}); err != nil {
		t.Fatalf("PublishRuntimeLifecycle: %v", err)
	}
	task := schedulerapp.ScheduledTask{
		ScheduledTaskID: "scheduled-private", Target: schedulerapp.Target{AgentID: assistantID},
	}
	if events := scheduledTaskOntologyEventsForTenant(task, "tenant-a"); len(events) != 0 {
		t.Fatalf("personal assistant scheduled ontology events = %+v", events)
	}
	if events := scheduledRunOntologyEventsAt(task, schedulerapp.ScheduledRun{
		ScheduledRunID: "run-private", ScheduledTaskID: task.ScheduledTaskID, TargetMode: schedulerapp.TargetModeAgentTask,
	}, time.Now().UTC()); len(events) != 0 {
		t.Fatalf("personal assistant scheduled run ontology events = %+v", events)
	}
	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "tenant-a")
	if err != nil || len(objects)+len(links)+len(facts)+len(evidence) != 0 {
		t.Fatalf("personal assistant execution ontology = objects:%+v links:%+v facts:%+v evidence:%+v err:%v", objects, links, facts, evidence, err)
	}
}

func TestProductionAgentExecutionOntologySinkProjectsLivePeopleResponsibilitySnapshot(t *testing.T) {
	baseURL := strings.TrimSpace(os.Getenv("ORRERY_PEOPLE_LIVE_URL"))
	token := strings.TrimSpace(os.Getenv("ORRERY_PEOPLE_LIVE_TOKEN"))
	if baseURL == "" || token == "" {
		t.Skip("set ORRERY_PEOPLE_LIVE_URL and ORRERY_PEOPLE_LIVE_TOKEN to run the People preview integration")
	}
	actorMemberID := strings.TrimSpace(os.Getenv("ORRERY_PEOPLE_LIVE_ACTOR_MEMBER_ID"))
	if actorMemberID == "" {
		actorMemberID = "member-openclaw-dafu"
	}

	request := responsibilityapp.ContextRequest{
		TenantID: "system", ActorMemberID: actorMemberID, AgentProfileID: "dafu",
		MissionID: "local-responsibility-smoke", TaskID: "local-responsibility-smoke",
		ResponsibilityID: "local-responsibility-smoke", AssignmentVersion: 1,
	}
	provider := responsibilityapp.NewHTTPContextProviderWithOptions(responsibilityapp.HTTPContextProviderOptions{
		BaseURL: baseURL,
		Token:   token,
	})
	responsibilityContext, err := provider.ResolveResponsibilityContext(context.Background(), request)
	if err != nil {
		t.Fatalf("resolve live People responsibility context: %v", err)
	}
	if responsibilityContext.ContextID == "" || responsibilityContext.Source != "people.responsibility_context" ||
		responsibilityContext.ResponsibilityID != request.ResponsibilityID || responsibilityContext.ResponsibilityVersion != 1 ||
		responsibilityContext.OrganizationID == "" || responsibilityContext.PositionID == "" ||
		responsibilityContext.CapturedAt.IsZero() || responsibilityContext.EffectiveAt.IsZero() {
		t.Fatalf("live People responsibility context is incomplete: %+v", responsibilityContext)
	}

	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-live-people-responsibility")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
	event := runtimedaemon.RuntimeLifecycleEvent{
		Kind: runtimedaemon.RuntimeLifecycleStarted, TenantID: "tenant-live-people-responsibility",
		AgentID: "dafu", RunID: "run-live-people-responsibility", Status: "running",
		ContextSnapshotID: "context-live-people-responsibility", ResponsibilityContext: &responsibilityContext,
		StartedAt: responsibilityContext.CapturedAt,
	}
	if err := sink.PublishRuntimeLifecycle(ctx, event); err != nil {
		t.Fatalf("project live People responsibility context: %v", err)
	}

	objects, links, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, event.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	object := assertContextGraphObject(t, objects, "responsibility_context_snapshot", responsibilityContext.ContextID)
	if object.Temporal.Mode != string(ontology.TemporalModeEvent) ||
		!object.Temporal.BusinessValidFrom.Equal(responsibilityContext.EffectiveAt) ||
		!object.Temporal.SourceRecordedAt.Equal(responsibilityContext.CapturedAt) {
		t.Fatalf("live responsibility temporal metadata = %+v", object.Temporal)
	}
	if !contextGraphLinksContain(links, "context_snapshot_uses_responsibility_context", event.ContextSnapshotID, responsibilityContext.ContextID) {
		t.Fatalf("live responsibility context link missing: %+v", links)
	}
	assertContextGraphFact(t, facts, "responsibility_context_snapshot", responsibilityContext.ContextID, "responsibility_version", float64(1))
}

func TestProductionAgentExecutionOntologyEventsHaveDefaultMappings(t *testing.T) {
	ctx := context.Background()
	registry := ontology.NewMemoryRegistry()
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}
	rules, err := registry.ListMappingRules(ctx, "")
	if err != nil {
		t.Fatalf("list mapping rules: %v", err)
	}
	byEventType := make(map[string]bool, len(rules))
	for _, rule := range rules {
		byEventType[rule.EventType] = rule.Enabled
	}
	for _, eventType := range agentExecutionOntologyEventTypesForTest() {
		if !byEventType[eventType] {
			t.Fatalf("production agent execution event %s has no enabled default mapping", eventType)
		}
	}
	for _, eventType := range productionActionTargetEventTypes {
		if !byEventType[eventType] {
			t.Fatalf("production action target event %s has no enabled default mapping", eventType)
		}
	}
}

func TestProductionAgentExecutionOntologyMappingsRejectMissingRequiredPayload(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-agent-execution-invalid-payload")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	for _, eventType := range agentExecutionOntologyEventTypesForTest() {
		t.Run(eventType, func(t *testing.T) {
			_, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
				ID: "invalid:" + eventType, TenantID: "tenant-invalid", Type: eventType, StreamID: "invalid", Version: 1,
				OccurredAt: time.Date(2026, 7, 29, 9, 0, 0, 0, time.UTC), Payload: map[string]any{},
				PublishedBy:     graphprotocol.ActorRef{Kind: "system", ID: "test"},
				SourceAccessRef: trustedOntologySourceRef("agent_run", "invalid", "tenant-invalid"),
			})
			if err == nil {
				t.Fatalf("mapping %s accepted an empty payload", eventType)
			}
		})
	}
}

func TestProductionAgentExecutionOntologySinkProjectsWritebackReceipts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    actioncontract.WritebackStatus
		eventType string
	}{
		{name: "succeeded", status: actioncontract.WritebackStatusSucceeded, eventType: "action.writeback.succeeded"},
		{name: "failed", status: actioncontract.WritebackStatusFailed, eventType: "action.writeback.failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			core := newOntologyEventLogTestCore(t, "production-action-writeback-"+tc.name)
			runner, err := newProductionOntologyProjectionRunner(ctx, core)
			if err != nil {
				t.Fatal(err)
			}
			sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
			now := time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
			run := actioncontract.ActionRun{
				ID: actioncontract.ActionRunID("action-run-writeback-" + tc.name), TenantID: "tenant-writeback", ActionTypeID: "mcp.tool.call",
				Actor:   actioncontract.ActionSubject{Kind: actioncontract.SubjectAgent, ID: "agent-1"},
				Runtime: actioncontract.RuntimeRef{Kind: actioncontract.RuntimeMCPGateway, ID: "mcp-gateway"},
				Status:  actioncontract.RunStatusStarted, ArgsHash: "sha256:args", CreatedAt: now,
			}
			if err := sink.ProjectActionRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			receipt := actioncontract.WritebackReceipt{
				ID: actioncontract.WritebackReceiptID("receipt-" + tc.name), TenantID: run.TenantID, ActionRunID: run.ID,
				TargetSystem: "mcp.github", Status: tc.status, CreatedAt: now.Add(time.Second),
			}
			if err := sink.ProjectWritebackReceipt(ctx, run, receipt); err != nil {
				t.Fatal(err)
			}
			_, _, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, run.TenantID)
			if err != nil {
				t.Fatal(err)
			}
			assertContextGraphFact(t, facts, "action_run", string(run.ID), "writeback_receipt_id", string(receipt.ID))
			assertContextGraphFact(t, facts, "action_run", string(run.ID), "writeback_status", string(tc.status))
			assertContextGraphFact(t, facts, "action_run", string(run.ID), "writeback_target_system", "mcp.github")
			events, err := sink.events.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, event := range events {
				if event.Type == tc.eventType {
					found = true
				}
			}
			if !found {
				t.Fatalf("event %s not found in %+v", tc.eventType, events)
			}
		})
	}
}

func TestProductionAgentExecutionOntologySinkProjectsScheduledTaskAndRun(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-agent-execution-ontology")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
	now := time.Date(2026, 7, 29, 10, 0, 0, 0, time.UTC)
	task := schedulerapp.ScheduledTask{
		TenantID:        "tenant-agent-ontology",
		ScheduledTaskID: "schedule-1",
		AgentTaskID:     "scheduled-task:schedule-1",
		Title:           "Collect weekly evidence",
		Target:          schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "agent-1", TaskGoal: "Collect evidence"},
		Schedule:        schedulerapp.Schedule{Type: schedulerapp.ScheduleTypeCron, Cron: "0 9 * * 1", Timezone: "UTC"},
		Status:          schedulerapp.TaskStatusEnabled,
		Enabled:         true,
		CreatedBy:       "operator-1",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := sink.ProjectScheduledTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	run := schedulerapp.ScheduledRun{
		ScheduledRunID:  "run-1",
		ScheduledTaskID: task.ScheduledTaskID,
		Status:          schedulerapp.RunStatusRunning,
		TargetMode:      schedulerapp.TargetModeAgentTask,
		CommandID:       "run-1",
		AgentID:         "agent-1",
		StartedAt:       now.Add(time.Minute),
		CreatedAt:       now.Add(time.Minute),
	}
	if err := sink.ProjectScheduledRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	objects, links, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, task.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObject(t, objects, "agent_task", task.AgentTaskID)
	assertContextGraphObject(t, objects, "scheduled_task", task.ScheduledTaskID)
	assertContextGraphObject(t, objects, "scheduled_run", run.ScheduledRunID)
	assertContextGraphFact(t, facts, "scheduled_run", run.ScheduledRunID, "agent_run_ref_status", "unresolved")
	if !contextGraphLinksContain(links, "scheduled_task_schedules_agent_task", task.ScheduledTaskID, task.AgentTaskID) {
		t.Fatalf("scheduled task to agent task link missing: %+v", links)
	}
	if !contextGraphLinksContain(links, "scheduled_task_targets_agent", task.ScheduledTaskID, "agent:agent-1") {
		t.Fatalf("scheduled task to target agent link missing: %+v", links)
	}
	if !contextGraphLinksContain(links, "scheduled_task_has_run", task.ScheduledTaskID, run.ScheduledRunID) {
		t.Fatalf("scheduled task to run link missing: %+v", links)
	}
}

func TestProductionScheduledTaskUsesWorkforceProfileDisplayName(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-agent-execution-profile-name")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
	sink.profiles = &organizationOntologyProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"gangbeng": {ID: "gangbeng", RealName: "贴饼", DisplayName: "贴饼"},
	}}
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	task := schedulerapp.ScheduledTask{
		TenantID:        "minvia",
		ScheduledTaskID: "schedule-profile-name-1",
		AgentTaskID:     "scheduled-task:schedule-profile-name-1",
		Title:           "Verify profile name",
		Target:          schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "gangbeng"},
		Status:          schedulerapp.TaskStatusEnabled,
		Enabled:         true,
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := sink.ProjectScheduledTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	_, _, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, task.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphFact(t, facts, "agent", "agent:gangbeng", "display_name", "贴饼")
}

func TestProductionScheduledTaskProjectsDynamicResponsibilityScopeWithoutAgent(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-agent-execution-dynamic-scope")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
	now := time.Date(2026, 8, 25, 10, 0, 0, 0, time.UTC)
	task := schedulerapp.ScheduledTask{
		TenantID:        "minvia",
		ScheduledTaskID: "responsibility-evaluation-batch",
		AgentTaskID:     "scheduled-task:responsibility-evaluation-batch",
		Title:           "Evaluate responsibility portfolios",
		Target: schedulerapp.Target{
			Mode:            schedulerapp.TargetModeResponsibilityEvaluation,
			SourceScopeKind: "people_portfolios",
			SourceScopeID:   "tenant-preview",
		},
		Status: schedulerapp.TaskStatusEnabled, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := sink.ProjectScheduledTask(ctx, task); err != nil {
		t.Fatal(err)
	}

	_, links, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, task.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range []struct {
		typeID string
		id     string
	}{{"agent_task", task.AgentTaskID}, {"scheduled_task", task.ScheduledTaskID}} {
		assertContextGraphFact(t, facts, object.typeID, object.id, "target_mode", string(task.Target.Mode))
		assertContextGraphFact(t, facts, object.typeID, object.id, "source_scope_kind", task.Target.SourceScopeKind)
		assertContextGraphFact(t, facts, object.typeID, object.id, "source_scope_id", task.Target.SourceScopeID)
	}
	for _, link := range links {
		if link.LinkType == "agent_assigned_to_task" || link.LinkType == "scheduled_task_targets_agent" {
			t.Fatalf("dynamic-scope task unexpectedly projected an agent assignment: %+v", link)
		}
	}
}

func TestProductionAgentExecutionOntologySinkMirrorsSystemScheduledWorkIntoDefaultOrganization(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-agent-execution-organization-mirror")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
	now := time.Date(2026, 8, 4, 14, 0, 0, 0, time.UTC)
	task := schedulerapp.ScheduledTask{
		TenantID:        "system",
		ScheduledTaskID: "system-schedule-1",
		AgentTaskID:     "scheduled-task:system-schedule-1",
		Title:           "Collect organization evidence",
		Target:          schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "agent-1", TaskGoal: "Collect evidence"},
		Schedule:        schedulerapp.Schedule{Type: schedulerapp.ScheduleTypeCron, Cron: "0 9 * * 1", Timezone: "UTC"},
		Status:          schedulerapp.TaskStatusEnabled,
		Enabled:         true,
		CreatedBy:       "scheduler",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	run := schedulerapp.ScheduledRun{
		ScheduledRunID:  "system-run-1",
		ScheduledTaskID: task.ScheduledTaskID,
		Status:          schedulerapp.RunStatusCompleted,
		TargetMode:      schedulerapp.TargetModeAgentTask,
		CommandID:       "system-run-1",
		AgentID:         "agent-1",
		StartedAt:       now.Add(time.Minute),
		CompletedAt:     now.Add(2 * time.Minute),
		CreatedAt:       now.Add(time.Minute),
	}

	if err := sink.ProjectScheduledTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := sink.ProjectScheduledRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	for _, tenantID := range []string{"system", defaultSystemSupportOrganizationID} {
		objects, links, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, tenantID)
		if err != nil {
			t.Fatal(err)
		}
		assertContextGraphObject(t, objects, "scheduled_task", task.ScheduledTaskID)
		assertContextGraphObject(t, objects, "scheduled_run", run.ScheduledRunID)
		if !contextGraphLinksContain(links, "scheduled_task_has_run", task.ScheduledTaskID, run.ScheduledRunID) {
			t.Fatalf("tenant %s is missing scheduled task to run link: %+v", tenantID, links)
		}
		if !contextGraphLinksContain(links, "scheduled_task_targets_agent", task.ScheduledTaskID, "agent:agent-1") {
			t.Fatalf("tenant %s is missing scheduled task to target agent link: %+v", tenantID, links)
		}
	}

	organizationObjects, _, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, defaultSystemSupportOrganizationID)
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range organizationObjects {
		if object.ID != task.ScheduledTaskID && object.ID != run.ScheduledRunID {
			continue
		}
		if object.OwnerAccessRef != (contextgraph.AccessRef{Kind: "organization", ID: defaultSystemSupportOrganizationID, TenantID: defaultSystemSupportOrganizationID}) {
			t.Fatalf("organization object %s access ref = %+v", object.ID, object.OwnerAccessRef)
		}
	}

	events, err := sink.events.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	wantScopedEvents := map[string]bool{
		"scheduler-run-projected:system-run-1:1785852120000000000:completed:scope:" + defaultBusinessOrganizationID: false,
	}
	for _, event := range events {
		if _, ok := wantScopedEvents[event.ID]; ok {
			wantScopedEvents[event.ID] = true
		}
	}
	for eventID, found := range wantScopedEvents {
		if !found {
			t.Errorf("organization-scoped projection event %s was not recorded", eventID)
		}
	}

	firstObjects, firstLinks, firstFacts, firstEvidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, defaultSystemSupportOrganizationID)
	if err != nil {
		t.Fatal(err)
	}
	if err := sink.ProjectScheduledTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := sink.ProjectScheduledRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	secondObjects, secondLinks, secondFacts, secondEvidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, defaultSystemSupportOrganizationID)
	if err != nil {
		t.Fatal(err)
	}
	if len(secondObjects) != len(firstObjects) || len(secondLinks) != len(firstLinks) || len(secondFacts) != len(firstFacts) || len(secondEvidence) != len(firstEvidence) {
		t.Fatalf(
			"repeat projection changed organization graph counts: first=%d/%d/%d/%d second=%d/%d/%d/%d",
			len(firstObjects), len(firstLinks), len(firstFacts), len(firstEvidence),
			len(secondObjects), len(secondLinks), len(secondFacts), len(secondEvidence),
		)
	}
	replayedEvents, err := sink.events.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayedEvents) != len(events) {
		t.Fatalf("repeat projection appended events: first=%d second=%d", len(events), len(replayedEvents))
	}
}

func TestProductionAgentExecutionOntologySinkProjectsRuntimeLineageWithoutRawCoT(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-agent-runtime-ontology")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
	now := time.Date(2026, 7, 29, 11, 0, 0, 0, time.UTC)
	task := schedulerapp.ScheduledTask{
		TenantID: "tenant-runtime-ontology", ScheduledTaskID: "schedule-2", AgentTaskID: "scheduled-task:schedule-2",
		Title: "Runtime lineage", Target: schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "agent-2"},
		Status: schedulerapp.TaskStatusEnabled, Enabled: true, CreatedAt: now, UpdatedAt: now,
	}
	if err := sink.ProjectScheduledTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	if err := sink.ProjectScheduledRun(ctx, schedulerapp.ScheduledRun{
		ScheduledRunID: "run-2", ScheduledTaskID: task.ScheduledTaskID, Status: schedulerapp.RunStatusRunning,
		TargetMode: schedulerapp.TargetModeAgentTask, CommandID: "run-2", AgentID: "agent-2", StartedAt: now, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	started := runtimedaemon.RuntimeLifecycleEvent{
		Kind: runtimedaemon.RuntimeLifecycleStarted, TenantID: task.TenantID, AgentID: "agent-2", RunID: "run-2",
		AgentTaskID: task.AgentTaskID, ScheduledTaskID: task.ScheduledTaskID, ScheduledRunID: "run-2",
		Status: "running", Message: "run started", ContextSnapshotID: "context-run-2", TraceID: "trace-run-2",
		MemoryRefs: []runtimedaemon.RuntimeMemoryRef{{
			ID: "memory-digest-2", MemoryKind: "personal", SummaryRef: "memory://digest-2", Sensitivity: "internal",
		}},
		ResponsibilityContext: &agentcore.ResponsibilityContext{
			ContextID: "people-context-2", ResponsibilityID: "responsibility-2", ResponsibilityVersion: 7,
			Source: "people.responsibility_context", ActorMemberID: "member-2", AgentProfileID: "agent-2",
			MissionID: "mission-2", TaskID: "task-2", OrganizationID: "org-2", PositionID: "position-2",
			Summary: "Own runtime delivery", SourceRefs: []string{"people:position:position-2:revision:4"},
			CapturedAt: now.Add(-time.Minute), EffectiveAt: now.Add(-2 * time.Minute),
		},
	}
	if err := sink.apply(ctx, graphprotocol.DomainEvent{
		ID: "knowledge-document:knowledge-2", TenantID: task.TenantID, Type: "knowledge.document.metadata.updated", StreamID: "knowledge-2", Version: 1, OccurredAt: now,
		Payload: map[string]any{
			"id": "knowledge-2", "title": "Runtime knowledge", "source_uri": "knowledge://2", "provider": "test",
			"provider_document_id": "knowledge-2", "knowledge_base_id": "knowledge-base-2", "content_type": "text/plain",
			"size_bytes": 10, "checksum": "sha256:knowledge-2", "status": "indexed",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "system", ID: "test"}, SourceAccessRef: trustedOntologySourceRef("knowledge_document", "knowledge-2", task.TenantID),
	}); err != nil {
		t.Fatal(err)
	}
	if err := sink.PublishRuntimeLifecycle(ctx, started); err != nil {
		t.Fatal(err)
	}
	completed := started
	completed.Kind = runtimedaemon.RuntimeLifecycleCompleted
	completed.Status = "completed"
	completed.Message = "<think>private execution plan</think>\n```orrery-public-summary\nEvidence collected and verified.\n```\n```orrery-agent-result\n{\"job_id\":\"job-secret\"}\n```"
	completed.TraceRef = "trace://run-2"
	completed.KnowledgeRefs = []runtimedaemon.RuntimeKnowledgeRef{{Kind: "knowledge_document", ID: "knowledge-2"}}
	completed.Artifacts = []agentcore.ArtifactRef{{ID: "artifact-2", Kind: agentcore.ArtifactKindReport, URI: "artifact://report-2"}}
	completed.Evidence = []runtimedaemon.RuntimeEvidence{{ID: "evidence-2", Kind: "test_log", Summary: "checks passed", ContentRef: "artifact://test-2"}}
	if err := sink.PublishRuntimeLifecycle(ctx, completed); err != nil {
		t.Fatal(err)
	}
	if err := sink.ProjectScheduledRun(ctx, schedulerapp.ScheduledRun{
		ScheduledRunID: "run-2", ScheduledTaskID: task.ScheduledTaskID, Status: schedulerapp.RunStatusCompleted,
		TargetMode: schedulerapp.TargetModeAgentTask, CommandID: "run-2", AgentID: "agent-2",
		StartedAt: now, CompletedAt: now.Add(time.Minute), CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	if err := sink.apply(ctx, graphprotocol.DomainEvent{
		ID: "mission-created:mission-2", TenantID: task.TenantID, Type: "mission.created", StreamID: "mission-2", Version: 1, OccurredAt: now,
		Payload:     map[string]any{"id": "mission-2", "title": "Runtime mission", "status": "active"},
		PublishedBy: graphprotocol.ActorRef{Kind: "system", ID: "test"}, SourceAccessRef: trustedOntologySourceRef("mission", "mission-2", task.TenantID),
	}); err != nil {
		t.Fatal(err)
	}
	if err := sink.ProjectActionRun(ctx, actioncontract.ActionRun{
		ID: "action-run-2", TenantID: task.TenantID, ActionTypeID: "mcp.tool.call", Status: actioncontract.RunStatusStarted,
		Actor:   actioncontract.ActionSubject{Kind: actioncontract.SubjectAgent, ID: "agent-2"},
		Runtime: actioncontract.RuntimeRef{Kind: actioncontract.RuntimeAgent, ID: "run-2"}, RiskLevel: actioncontract.RiskHigh,
		PolicyDecisionID: "policy-decision-2", Approval: actioncontract.Ref{Kind: "approval", ID: "approval-2"}, HILTokenID: "hil-2",
		ObjectRefs:   []actioncontract.ObjectRef{{Kind: "mission", ID: "mission-2"}},
		EvidenceRefs: []actioncontract.Ref{{Kind: "evidence", ID: "evidence-2"}}, ArgsHash: "sha256:args", CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	objects, links, facts, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, task.TenantID)
	if err != nil {
		t.Fatal(err)
	}
	for objectType, id := range map[string]string{
		"agent_run": "agent-run:run-2", "context_snapshot": "context-run-2", "execution_trace": "trace-run-2",
		"responsibility_context_snapshot": "people-context-2",
		"memory_digest":                   "memory-digest-2", "knowledge_document": "knowledge-2",
		"artifact": "artifact-2", "evidence_item": "evidence-2", "action_run": "action-run-2",
		"policy_decision": "policy-decision-2", "approval_case": "approval-case:approval-2",
	} {
		assertContextGraphObject(t, objects, objectType, id)
	}
	for linkType, endpoints := range map[string][2]string{
		"scheduled_run_triggers_agent_run":             {"run-2", "agent-run:run-2"},
		"agent_run_executes_agent_task":                {"agent-run:run-2", task.AgentTaskID},
		"agent_run_uses_context":                       {"agent-run:run-2", "context-run-2"},
		"context_snapshot_uses_responsibility_context": {"context-run-2", "people-context-2"},
		"context_snapshot_uses_memory_digest":          {"context-run-2", "memory-digest-2"},
		"context_snapshot_uses_knowledge":              {"context-run-2", "knowledge-2"},
		"agent_run_has_trace":                          {"agent-run:run-2", "trace-run-2"},
		"agent_run_produces_artifact":                  {"agent-run:run-2", "artifact-2"},
		"agent_run_produces_evidence":                  {"agent-run:run-2", "evidence-2"},
		"agent_run_requests_action_run":                {"agent-run:run-2", "action-run-2"},
		"action_run_authorized_by_policy":              {"action-run-2", "policy-decision-2"},
		"action_run_requires_approval_case":            {"action-run-2", "approval-case:approval-2"},
		"action_run_targets_mission":                   {"action-run-2", "mission-2"},
		"evidence_item_supports_action_run":            {"evidence-2", "action-run-2"},
	} {
		if !contextGraphLinksContain(links, linkType, endpoints[0], endpoints[1]) {
			t.Fatalf("runtime lineage link %s missing: %+v", linkType, links)
		}
	}
	assertContextGraphFact(t, facts, "scheduled_run", "run-2", "agent_run_ref_status", "resolved")
	assertContextGraphFact(t, facts, "action_run", "action-run-2", "actor_kind", "agent")
	assertContextGraphFact(t, facts, "responsibility_context_snapshot", "people-context-2", "responsibility_version", float64(7))
	responsibilityObject := assertContextGraphObject(t, objects, "responsibility_context_snapshot", "people-context-2")
	if responsibilityObject.Temporal.Mode != string(ontology.TemporalModeEvent) ||
		!responsibilityObject.Temporal.BusinessValidFrom.Equal(now.Add(-2*time.Minute)) ||
		!responsibilityObject.Temporal.SourceRecordedAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("responsibility context temporal metadata = %+v", responsibilityObject.Temporal)
	}
	for _, fact := range facts {
		if fact.Predicate == "raw_cot" || fact.Predicate == "chain_of_thought" {
			t.Fatalf("raw reasoning leaked into ontology fact: %+v", fact)
		}
	}
	events, err := sink.events.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Type == "agent.runtime.run.completed" || event.Type == "agent.runtime.trace.recorded" {
			payload := event.Payload
			summary, _ := payload["summary"].(string)
			if strings.Contains(summary, "private execution plan") || strings.Contains(summary, "job-secret") || strings.Contains(summary, "orrery-agent-result") {
				t.Fatalf("private runtime summary leaked into ontology event: %+v", event)
			}
			if event.Type == "agent.runtime.run.completed" && summary != "Evidence collected and verified." {
				t.Fatalf("runtime completion summary = %q", summary)
			}
		}
		if _, err := runner.ApplyEvent(ctx, event); err != nil {
			t.Fatalf("replay retained event %s (%s): %v", event.ID, event.Type, err)
		}
	}
}

func TestProductionAgentExecutionOntologySinkProjectsRuntimeAgentBeforeVersionLinks(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-agent-runtime-agent-order")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
	now := time.Date(2026, 7, 29, 11, 30, 0, 0, time.UTC)

	err = sink.PublishRuntimeLifecycle(ctx, runtimedaemon.RuntimeLifecycleEvent{
		Kind: runtimedaemon.RuntimeLifecycleStarted, TenantID: "tenant-runtime-agent-order",
		AgentID: "agent-order", RunID: "run-order", RuntimeKind: "pi", Status: "running", StartedAt: now,
	})
	if err != nil {
		t.Fatalf("PublishRuntimeLifecycle: %v", err)
	}

	objects, links, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "tenant-runtime-agent-order")
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObject(t, objects, "agent", "agent:agent-order")
	if !contextGraphLinksContain(links, "agent_has_version", "agent:agent-order", "agent:agent-order:pi") {
		t.Fatalf("agent version link missing: %+v", links)
	}
}

func TestProductionAgentExecutionOntologySinkUsesMissionScopeForMissionRuntime(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-agent-runtime-mission-scope")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
	now := time.Date(2026, 7, 29, 11, 45, 0, 0, time.UTC)
	tenantID := "tenant-runtime-mission-scope"
	missionID := "mission-runtime-scope"
	taskID := "task-runtime-scope"
	graphTenantID := missionScopeTenantID(tenantID, missionID)
	actor := graphprotocol.ActorRef{Kind: "system", ID: "test"}
	if err := sink.apply(ctx, graphprotocol.DomainEvent{
		ID: "mission-created:" + missionID, TenantID: graphTenantID, Type: "mission.created", StreamID: missionID, Version: 1, OccurredAt: now,
		Payload:     map[string]any{"id": missionID, "title": "Runtime mission", "status": "active"},
		PublishedBy: actor, SourceAccessRef: missionOntologySourceRef(tenantID, missionID),
	}); err != nil {
		t.Fatal(err)
	}
	if err := sink.apply(ctx, graphprotocol.DomainEvent{
		ID: "mission-task-created:" + taskID, TenantID: graphTenantID, Type: "mission.task.created", StreamID: taskID, Version: 1, OccurredAt: now,
		Payload:     map[string]any{"mission_id": missionID, "task_id": taskID, "title": "Runtime task", "status": "ready", "event_id": "mission-task-created:" + taskID},
		PublishedBy: actor, SourceAccessRef: missionOntologySourceRef(tenantID, missionID),
	}); err != nil {
		t.Fatal(err)
	}

	err = sink.PublishRuntimeLifecycle(ctx, runtimedaemon.RuntimeLifecycleEvent{
		Kind: runtimedaemon.RuntimeLifecycleStarted, TenantID: tenantID, MissionID: missioncore.MissionID(missionID),
		TaskID: missioncore.TaskID(taskID), AgentID: "agent-mission-scope", RunID: "run-mission-scope",
		RuntimeKind: "pi", Status: "running", StartedAt: now,
	})
	if err != nil {
		t.Fatalf("PublishRuntimeLifecycle: %v", err)
	}

	objects, _, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, graphTenantID)
	if err != nil {
		t.Fatal(err)
	}
	assertContextGraphObject(t, objects, "agent_task", "agent-task:mission:"+taskID)
}

func TestProductionAgentExecutionOntologyIsolatesTenants(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "production-agent-execution-tenants")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	sink := newProductionAgentExecutionOntologyTestSink(core, runner, time.Now)
	now := time.Date(2026, 7, 29, 12, 0, 0, 0, time.UTC)
	for _, task := range []schedulerapp.ScheduledTask{
		{TenantID: "tenant-a", ScheduledTaskID: "schedule-a", AgentTaskID: "scheduled-task:schedule-a", Title: "Tenant A", Status: schedulerapp.TaskStatusEnabled, Enabled: true, CreatedAt: now, UpdatedAt: now},
		{TenantID: "tenant-b", ScheduledTaskID: "schedule-b", AgentTaskID: "scheduled-task:schedule-b", Title: "Tenant B", Status: schedulerapp.TaskStatusEnabled, Enabled: true, CreatedAt: now, UpdatedAt: now},
	} {
		if err := sink.ProjectScheduledTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	objects, _, _, _, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	for _, object := range objects {
		if object.ID == "scheduled-task:schedule-b" || object.ID == "schedule-b" {
			t.Fatalf("tenant-a snapshot contains tenant-b object %+v", object)
		}
	}
}
