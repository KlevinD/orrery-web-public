package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/responsibility"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestProductionResponsibilityDispatchPreservesBoundTaskContext(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()

	const tenantID = "tenant-responsibility"
	const missionID = "mission-responsibility"
	const taskID = "task-responsibility"
	const agentID = "responsible-agent"
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	seedProductionChatProfile(t, ctx, profiles, agentID, "任执行", "Worker")
	profile, err := profiles.GetProfile(ctx, agentID)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	profile.RuntimeSpec = agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindOpenClaw}
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save runtime profile: %v", err)
	}

	tasks := missiontask.NewPlatformRepository()
	record := productionResponsibilityTask(taskID, missionID, agentID, missiontask.TaskStatusAssigned)

	record.Contract.SourceKind = "scheduler_agent_task"
	record.Contract.SourceID = "schedule-1"
	record.Contract.SourceStepID = "run-1"
	record.Contract.ExecutionPermissionScopes = []string{"knowledge.write"}
	record.Contract.ExecutionAllowedToolIDs = []string{"mcp-code-knowledge.code_knowledge_begin"}
	record.Contract.ExecutionEnvelope = &agentcore.ExecutionEnvelope{
		TenantID: tenantID, RunID: "run-1", MissionID: missionID, TaskID: taskID,
		AgentID: agentID, AgentProfileID: agentID, PrincipalID: "scheduler-owner", PrincipalKind: agentcore.ExecutionPrincipalSystem,
		ScheduledTaskID: "schedule-1", ScheduledRunID: "run-1",
		PermissionScopes: append([]string(nil), record.Contract.ExecutionPermissionScopes...),
		AllowedToolIDs:   append([]string(nil), record.Contract.ExecutionAllowedToolIDs...),
	}
	if err := record.Contract.ExecutionEnvelope.SealAt(env.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	saveProductionResponsibilityTask(t, env, tasks, record)
	missionRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	at := env.Clock.Now().UTC()
	model := projection.ReadModel{
		Mission: projection.MissionView{MissionID: missionID, Title: "Responsibility", FinalGoal: "Dispatch automatically", Status: projection.MissionStatusActive, CreatedAt: at},
		Team: projection.TeamView{Members: []projection.TeamMemberView{{
			AgentID: agentID, AgentProfileID: agentID, AgentInstanceID: agentcore.AgentInstanceID(agentID + ":" + missionID), AgentKind: "ai", DisplayName: "任执行", MissionRole: "worker", Status: projection.TeamMemberJoined, JoinedAt: at,
		}}},
		TaskBoard: projection.TaskBoardView{Tasks: []projection.TaskCardView{{
			SourceKind: record.Contract.SourceKind, SourceID: record.Contract.SourceID, SourceStepID: record.Contract.SourceStepID,
			ExecutionEnvelope:         agentcore.CloneExecutionEnvelope(record.Contract.ExecutionEnvelope),
			ExecutionPermissionScopes: append([]string(nil), record.Contract.ExecutionPermissionScopes...),
			ExecutionAllowedToolIDs:   append([]string(nil), record.Contract.ExecutionAllowedToolIDs...), TaskID: taskID, Assignee: agentID, Goal: record.Contract.Goal, WorkerPersonalGoalSummary: record.Contract.WorkerPersonalGoal, Status: projection.TaskStatusAssigned, UpdatedAt: at}}},
		Checkpoint: projection.Checkpoint{MissionID: missionID, Version: 1, UpdatedAt: at}, UpdatedAt: at,
	}
	if err := missionRepo.SaveForTenant(ctx, tenantID, model); err != nil {
		t.Fatalf("save mission projection: %v", err)
	}

	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	service := responsibility.NewService(responsibility.ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	assignmentPort := productionTaskResponsibilityAssignment{service: service}
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return assignmentPort.RecordTaskAssignment(ctx, tx, missioncontrol.TaskResponsibilityAssignmentSignal{
			TenantID: tenantID, MissionID: missionID, TaskID: taskID, AgentProfileID: agentID, Priority: 75,
			SourceKind: "scheduler_agent_task", SourceID: "schedule-1", SourceStepID: "run-1",
		})
	}); err != nil {
		t.Fatalf("record task assignment: %v", err)
	}
	ready, err := repository.ListReadyActivations(ctx, env.Clock.Now(), 10)
	if err != nil || len(ready) != 1 {
		t.Fatalf("durable activations = %+v err=%v", ready, err)
	}
	assignment, err := repository.GetAssignmentForWork(ctx, tenantID, responsibility.WorkKindMissionTask, taskID)
	if err != nil || assignment.SourceKind != "scheduler_agent_task" || assignment.SourceID != "schedule-1" || assignment.SourceStepID != "run-1" {
		t.Fatalf("assignment source = %+v err=%v", assignment, err)
	}

	authorizer := authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}})
	commands := missioncontrol.NewCommandService(nil)
	commands.SetAuthorizationService(authorizer)
	commands.SetRuntimeCommandPorts(productionAgentRunQueue{queue: env.ExecutionQueue}, missionRepo, missionevent.NewStoreSink(env.EventStore), env.IDGenerator, env.Clock)
	commands.SetRuntimeEventReader(missionevent.NewStoreReader(env.EventStore))
	coordinator := responsibility.NewCoordinator(responsibility.CoordinatorOptions{
		Repository: repository, Service: service, Profiles: profiles,
		Dispatcher: productionResponsibilityRuntimeDispatcher{commands: commands}, Clock: env.Clock, IDGenerator: env.IDGenerator,
		WorkerID: "production-responsibility-test", ClaimLease: time.Minute, MaxParallel: 1,
	})
	stats, err := coordinator.Reconcile(ctx, 10)
	if err != nil {
		t.Fatalf("reconcile responsibility: %v", err)
	}
	if stats.Dispatched != 1 {
		t.Fatalf("reconcile stats = %+v", stats)
	}
	runID := responsibility.RunIDForActivation(ready[0].ID)
	job, ok, err := env.ExecutionQueue.Get(ctx, runID)
	if err != nil || !ok {
		t.Fatalf("queued responsibility run found=%v err=%v", ok, err)
	}
	run, err := runtimedaemon.DecodeAgentRunPayload(job.Payload)
	if err != nil {
		t.Fatalf("decode queued Agent run: %v", err)
	}
	if run.RunID.String() != runID || run.RuntimeID != string(agentcore.RuntimeKindOpenClaw) || run.AgentProfileID != agentID || run.TaskID != taskID || run.Request.TenantID != tenantID || run.Attempt != 1 {
		t.Fatalf("queued Agent run = %+v", run)
	}

	if run.Request.ExecutionEnvelope == nil || run.Request.ExecutionEnvelope.PrincipalID != "scheduler-owner" {
		t.Fatalf("scheduled principal was not retained: %+v", run.Request.ExecutionEnvelope)
	}
	if got := run.Request.ContextPack.ResponsibilityContext.AllowedTools; len(got) != 1 || got[0] != "mcp-code-knowledge.code_knowledge_begin" {
		t.Fatalf("dispatched context must retain the bounded scheduled tools: %v", got)
	}
	attempts, err := repository.ListAttempts(ctx, responsibility.AssignmentID(tenantID, responsibility.WorkKindMissionTask, taskID))
	if err != nil || len(attempts) != 1 || attempts[0].RuntimeKind != string(agentcore.RuntimeKindOpenClaw) || attempts[0].AgentRunID != runID {
		t.Fatalf("responsibility attempts = %+v err=%v", attempts, err)
	}
}
