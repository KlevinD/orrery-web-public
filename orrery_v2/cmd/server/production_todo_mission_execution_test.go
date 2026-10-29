package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/responsibility"
	todoapp "orrery_v2/internal/app/todo"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	missionprojection "orrery_v2/internal/mission/projection"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestProductionDevelopmentTodoMaterializesDraftTasksWithoutResponsibilityActivation(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	const (
		tenantID       = "tenant-todo-development"
		todoID         = "todo-development"
		conversationID = "conversation-todo-development"
		agentID        = "lead-agent"
	)

	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	seedProductionChatProfile(t, ctx, profiles, agentID, "任开发", "Worker")
	projectionRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	taskRepo := missiontask.NewPlatformRepository()
	responsibilityRepo := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	responsibilityService := responsibility.NewService(responsibility.ServiceOptions{
		Repository: responsibilityRepo, UnitOfWork: env.UnitOfWork, Tasks: taskRepo, Clock: env.Clock,
	})
	auth := newProductionMissionAuthorizationTestFixture()
	lifecycle := missioncontrol.NewLifecycleService(env.UnitOfWork, env.DBRouter, projectionRepo, missioncontrol.LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator, TaskRepo: taskRepo, AgentDirectory: profiles,
		AuthorizationService: auth.service, AuthorizationRelations: auth.relations,
		TaskResponsibility: productionTaskResponsibilityAssignment{service: responsibilityService},
	})
	commands := missioncontrol.NewCommandServiceWithMissionPorts(nil, nil, nil, nil, lifecycle)
	auth.wireCommand(commands)
	commands.SetRuntimeCommandPorts(productionAgentRunQueue{queue: env.ExecutionQueue}, projectionRepo, missionevent.NewStoreSink(env.EventStore), env.IDGenerator, env.Clock)

	discussionRepository := discussionsession.NewRepository(env.DBRouter)
	if err := discussionRepository.SaveConversation(ctx, discussionsession.ConversationRecord{
		ConversationID: conversationID, TenantID: tenantID, PurposeKind: "todo", PurposeID: todoID,
		Title: "Todo development", Status: "completed", CreatedBy: "human-approver", CreatedAt: env.Clock.Now(), UpdatedAt: env.Clock.Now(),
	}); err != nil {
		t.Fatalf("save Todo conversation: %v", err)
	}
	if err := discussionRepository.SaveMember(ctx, discussionsession.ConversationMemberRecord{
		ConversationID: conversationID, MemberKind: "user", MemberID: "human-approver", DisplayName: "Approver", Role: "approver", CreatedAt: env.Clock.Now(),
	}); err != nil {
		t.Fatalf("save Todo conversation approver: %v", err)
	}
	discussions := discussionsession.NewService(discussionsession.ServiceOptions{
		Repository: discussionRepository, AgentDirectory: profiles, Clock: env.Clock, IDGenerator: env.IDGenerator,
	})
	lifecycle.SetCanonicalConversationMembership(discussions)
	api := &productionAPI{missionCommand: commands, missionRepo: projectionRepo, authorizationRelations: auth.relations}
	executor := productionTodoMissionExecutor{api: api, discussions: discussions}
	receipt, err := executor.CreateMissionFromTodo(ctx, todoapp.TodoMissionExecutionRequest{
		TenantID: tenantID, TodoID: todoID, TodoTitle: "Implement repository change", TodoDescription: "Change production code",
		AcceptanceCriteria: "Tests pass", DevelopmentProjectID: "development-project-1", ConversationID: conversationID,
		Plan: todoapp.TodoExecutionPlan{Steps: []todoapp.TodoExecutionPlanStep{{
			StepID: "step-1", Title: "Implement", Description: "Implement and verify", AssigneeAgentID: agentID,
			AcceptanceCriteria: []string{"Focused tests pass"},
			EvidenceRequirements: []todoapp.TodoExecutionEvidenceRequirement{{
				EvidenceRequirementID: "evidence-step-1-1", AcceptanceCriterion: "Focused tests pass", Kind: "test_log", Required: true,
			}},
		}}},
		PlanDigest: "plan-digest", ExecutionAttempt: 1, ApprovedBy: "approver-login",
		ApproverAuthorizationSubjectID: "human-approver", ApproverDisplayName: "Approver", IdempotencyKey: "todo-development-attempt-1",
	})
	if err != nil {
		t.Fatalf("CreateMissionFromTodo: %v", err)
	}
	if !receipt.RequiresWorkspace || len(receipt.Tasks) != 1 {
		t.Fatalf("development receipt = %+v", receipt)
	}

	var task missiontask.TaskRecord
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var getErr error
		task, getErr = taskRepo.GetTask(ctx, tx, missiontask.TaskID(receipt.Tasks[0].TaskID))
		return getErr
	}); err != nil {
		t.Fatalf("get materialized task: %v", err)
	}
	if task.Status != missiontask.TaskStatusDraft {
		t.Fatalf("development task status = %q, want draft until workspace provisioning", task.Status)
	}
	if len(task.Contract.EvidenceRequirements) != 1 || task.Contract.EvidenceRequirements[0].ID != "evidence-step-1-1" || task.Contract.EvidenceRequirements[0].Kind != missiontask.EvidenceRequirementKindTestLog || !task.Contract.EvidenceRequirements[0].Required {
		t.Fatalf("development task evidence contract = %+v", task.Contract.EvidenceRequirements)
	}
	if _, err := responsibilityRepo.GetAssignmentForWork(ctx, tenantID, responsibility.WorkKindMissionTask, receipt.Tasks[0].TaskID); !errors.Is(err, responsibility.ErrNotFound) {
		t.Fatalf("responsibility assignment before workspace provisioning = %v, want not found", err)
	}
	model, err := projectionRepo.GetMissionControlForTenant(ctx, tenantID, missioncore.MissionID(receipt.MissionID))
	if err != nil || len(model.TaskBoard.Tasks) != 1 || model.TaskBoard.Tasks[0].Status != "created" {
		t.Fatalf("development Mission projection = %+v err=%v", model.TaskBoard.Tasks, err)
	}
	if model.Discussion.ConversationID != conversationID || model.Discussion.DiscussionGroupID != "" {
		t.Fatalf("Todo-origin Mission discussion identity = %+v, want canonical Conversation only", model.Discussion)
	}
	if model.Mission.OriginKind != "todo" || model.Mission.OriginID != todoID || model.Mission.OriginTodoID != todoID || model.Mission.ExecutionAttempt != 1 || model.Mission.DiscussionMode != "existing_conversation" {
		t.Fatalf("Todo-origin Mission execution metadata = %+v", model.Mission)
	}
	conversation, err := discussionRepository.GetConversation(ctx, conversationID)
	if err != nil {
		t.Fatalf("get canonical conversation: %v", err)
	}
	foundAgent := false
	for _, member := range conversation.Members {
		if member.MemberKind == "agent" && member.MemberID == agentID && strings.HasPrefix(member.ManagedSource, "todo-mission:"+tenantID+"/"+todoID+"/") {
			foundAgent = true
		}
	}
	if !foundAgent {
		t.Fatalf("Todo Mission assignee was not added to canonical conversation: %+v", conversation.Members)
	}
	if err := executor.PauseMissionForTodo(ctx, todoapp.TodoMissionPauseRequest{
		TenantID: tenantID, TodoID: todoID, BindingID: "binding-1", MissionID: receipt.MissionID,
		ExecutionAttempt: 1, Reason: "Todo scope changed", IdempotencyKey: "todo-mission-pause:binding-1",
	}); err != nil {
		t.Fatalf("PauseMissionForTodo: %v", err)
	}
	model, err = projectionRepo.GetMissionControlForTenant(ctx, tenantID, missioncore.MissionID(receipt.MissionID))
	if err != nil || model.Mission.Status != missionprojection.MissionStatusPaused {
		t.Fatalf("paused production Todo Mission = %+v err=%v", model.Mission, err)
	}
}

func TestProductionTodoMissionPlanStepsCreatesRootTaskFallback(t *testing.T) {
	steps := productionTodoMissionPlanSteps(todoapp.TodoMissionExecutionRequest{
		TodoTitle: "Prepare release notes", AcceptanceCriteria: "Release notes are approved",
	})
	if len(steps) != 1 || steps[0].StepID != "root" || steps[0].Title != "完成 Todo：Prepare release notes" || steps[0].AssigneeAgentID != qaPlanSystemViewerID {
		t.Fatalf("root Mission task fallback = %+v", steps)
	}
	if len(steps[0].EvidenceRequirements) != 1 || steps[0].EvidenceRequirements[0].AcceptanceCriterion != "Release notes are approved" {
		t.Fatalf("root Mission task evidence = %+v", steps[0].EvidenceRequirements)
	}
}

func TestTodoMissionTaskIDIsTenantScoped(t *testing.T) {
	first := todoMissionTaskID("tenant-a", "todo-shared", 1, "step-shared")
	second := todoMissionTaskID("tenant-b", "todo-shared", 1, "step-shared")
	if first == second {
		t.Fatalf("cross-tenant Todo Mission task IDs collided: %q", first)
	}
	if replay := todoMissionTaskID("tenant-a", "todo-shared", 1, "step-shared"); replay != first {
		t.Fatalf("Todo Mission task ID is not stable: first=%q replay=%q", first, replay)
	}
}
