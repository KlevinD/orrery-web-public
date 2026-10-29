package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	todoapp "orrery_v2/internal/app/todo"
	missioncore "orrery_v2/internal/mission/core"
	missionprojection "orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/db"
	platformrealtime "orrery_v2/internal/platform/realtime"
	"orrery_v2/internal/platform/testkit"
)

func TestTodoProgressReconcilerRepairsProjectionCommittedBeforeOwnerLink(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	const tenantID = "tenant-progress-recovery"
	const todoID = "todo-progress-recovery"
	const missionID = "mission-progress-recovery"
	now := env.Clock.Now().UTC()
	todos := todoapp.NewRepository(env.DBRouter)
	item := todoapp.TodoItem{
		TenantID: tenantID, TodoID: todoID, Title: "Recover durable progress", Status: todoapp.TodoStatusPlanned,
		WorkKind: todoapp.TodoWorkKindDevelopment, AutomationMode: todoapp.TodoAutomationModeManaged,
		DevelopmentProjectID: "development-project-recovery", CreatedBy: "owner-recovery", ReviewerID: "owner-recovery", CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := todos.CreateTodo(ctx, item); err != nil {
		t.Fatalf("CreateTodo: %v", err)
	}
	loadedItem, err := todos.GetTodoForTenant(ctx, tenantID, todoID)
	if err != nil {
		t.Fatalf("GetTodoForTenant: %v", err)
	}
	item = loadedItem
	state := todoapp.TodoAutomationState{
		TenantID: tenantID, ProposalID: "proposal-recovery", TodoID: todoID, TodoVersion: item.Version,
		Status: todoapp.TodoAutomationStatusExecuting, ConversationID: "conversation-recovery",
		PlanDigest: "digest-recovery", ApprovedPlanDigest: "digest-recovery", DecisionBy: "owner-recovery",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := todos.SaveTodoAutomation(ctx, state); err != nil {
		t.Fatalf("SaveTodoAutomation: %v", err)
	}
	binding, acquired, err := todos.BeginTodoMissionExecution(ctx, item, state, "owner-recovery", "claim-recovery", now)
	if err != nil || !acquired {
		t.Fatalf("BeginTodoMissionExecution binding=%+v acquired=%v err=%v", binding, acquired, err)
	}
	base := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	model := missionprojection.ReadModel{
		TenantID: tenantID,
		Mission: missionprojection.MissionView{
			TenantID: tenantID, MissionID: missioncore.MissionID(missionID), Title: "Recovered Mission",
			Status: missionprojection.MissionStatusActive, OriginKind: "todo", OriginTodoID: todoID,
			OriginDevelopmentProjectID: item.DevelopmentProjectID,
		},
		UpdatedAt:  now.Add(time.Minute),
		Checkpoint: missionprojection.Checkpoint{MissionID: missioncore.MissionID(missionID), Version: 5, UpdatedAt: now.Add(time.Minute)},
	}
	if err := base.SaveForTenant(ctx, tenantID, model); err != nil {
		t.Fatalf("save Mission before owner link: %v", err)
	}
	if _, err := todos.GetTodoExecutionProgressProjection(ctx, tenantID, todoID, binding.ExecutionAttempt); !errors.Is(err, todoapp.ErrNotFound) {
		t.Fatalf("progress before owner link error = %v, want ErrNotFound", err)
	}
	binding.MissionID = missionID
	binding.Status = todoapp.TodoMissionBindingStatusMaterialized
	binding.UpdatedAt = now.Add(2 * time.Minute)
	if err := todos.SaveTodoMissionBinding(ctx, binding); err != nil {
		t.Fatalf("SaveTodoMissionBinding: %v", err)
	}
	reconciler := newProductionTodoProgressProjectionRepository(base, base, todos, env.Clock)
	audit := &todoProgressAuditRecorder{}
	reconciler.SetAuditStore(audit)
	jobs, err := env.Databases.Store(db.RoleJobs)
	if err != nil {
		t.Fatal(err)
	}
	realtimeStore, err := platformrealtime.NewStore(ctx, jobs, platformrealtime.StoreOptions{Now: env.Clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	reconciler.SetRealtimeStore(realtimeStore)
	reconciler.realtime = &failOnceProjectExecutionRealtimeAppender{delegate: realtimeStore}
	applied, err := reconciler.ReconcileTodoExecutionProgress(ctx, 10)
	if err == nil || applied != 0 {
		t.Fatalf("reconcile with outbox failure applied=%d err=%v", applied, err)
	}
	if len(audit.events) != 1 || audit.events[0].Action != "todo.execution_progress.projected" || audit.events[0].AuditID == "" {
		t.Fatalf("Todo progress audit events = %+v", audit.events)
	}
	progress, err := todos.GetTodoExecutionProgressProjection(ctx, tenantID, todoID, binding.ExecutionAttempt)
	if err != nil || progress.MissionID != missionID || progress.MissionProjectionVersion != 5 {
		t.Fatalf("progress was not committed before outbox failure: progress=%+v err=%v", progress, err)
	}
	reconciler.SetRealtimeStore(realtimeStore)
	applied, err = reconciler.ReconcileTodoExecutionProgress(ctx, 10)
	if err != nil || applied != 0 {
		t.Fatalf("reconcile did not repair unchanged projection outbox applied=%d err=%v", applied, err)
	}
	eventID := projectExecutionRealtimeEventID(tenantID, item.DevelopmentProjectID, todoID, missionID, 5)
	event, found, err := realtimeStore.Get(ctx, eventID)
	if err != nil || !found {
		t.Fatalf("project execution realtime event found=%v err=%v", found, err)
	}
	wantChannel, err := platformrealtime.ProjectChannel(tenantID, item.DevelopmentProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if event.Envelope.Channel != wantChannel || event.Envelope.Type != platformrealtime.EventProjectExecutionUpdated || event.Envelope.AggregateID != item.DevelopmentProjectID {
		t.Fatalf("project execution realtime envelope = %+v", event.Envelope)
	}
	var payload map[string]any
	if err := json.Unmarshal(event.Envelope.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	wantPayload := map[string]any{"todo_id": todoID, "mission_id": missionID, "projection_version": float64(5)}
	if len(payload) != len(wantPayload) || payload["todo_id"] != wantPayload["todo_id"] || payload["mission_id"] != wantPayload["mission_id"] || payload["projection_version"] != wantPayload["projection_version"] {
		t.Fatalf("project execution realtime payload = %#v", payload)
	}
	applied, err = reconciler.ReconcileTodoExecutionProgress(ctx, 10)
	if err != nil || applied != 0 {
		t.Fatalf("idempotent reconcile applied=%d err=%v", applied, err)
	}
	if len(audit.events) != 3 || audit.events[0].AuditID != audit.events[1].AuditID || audit.events[1].AuditID != audit.events[2].AuditID {
		t.Fatalf("Todo progress audit replay identity = %+v", audit.events)
	}
}

func TestTodoProgressProjectionRetriesDeterministicAuditAfterAuditFailure(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	const (
		tenantID  = "tenant-progress-audit"
		todoID    = "todo-progress-audit"
		missionID = "mission-progress-audit"
	)
	todos := todoapp.NewRepository(env.DBRouter)
	now := env.Clock.Now().UTC()
	item := todoapp.TodoItem{TenantID: tenantID, TodoID: todoID, Title: "Audit progress", Status: todoapp.TodoStatusPlanned, WorkKind: todoapp.TodoWorkKindGeneral, AutomationMode: todoapp.TodoAutomationModeManaged, CreatedBy: "owner", CreatedAt: now, UpdatedAt: now, Version: 1}
	if err := todos.CreateTodo(ctx, item); err != nil {
		t.Fatal(err)
	}
	item, err := todos.GetTodoForTenant(ctx, tenantID, todoID)
	if err != nil {
		t.Fatal(err)
	}
	state := todoapp.TodoAutomationState{TenantID: tenantID, ProposalID: "proposal-audit", TodoID: todoID, TodoVersion: item.Version, Status: todoapp.TodoAutomationStatusExecuting, ConversationID: "conversation-audit", PlanDigest: "digest-audit", ApprovedPlanDigest: "digest-audit", DecisionBy: "owner", CreatedAt: now, UpdatedAt: now}
	if err := todos.SaveTodoAutomation(ctx, state); err != nil {
		t.Fatal(err)
	}
	binding, acquired, err := todos.BeginTodoMissionExecution(ctx, item, state, "owner", "claim-audit", now)
	if err != nil || !acquired {
		t.Fatalf("begin binding=%+v acquired=%v err=%v", binding, acquired, err)
	}
	binding.MissionID = missionID
	binding.Status = todoapp.TodoMissionBindingStatusMaterialized
	binding.UpdatedAt = now.Add(time.Minute)
	if err := todos.SaveTodoMissionBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	base := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	model := missionprojection.ReadModel{TenantID: tenantID, Mission: missionprojection.MissionView{TenantID: tenantID, MissionID: missioncore.MissionID(missionID), OriginKind: "todo", OriginTodoID: todoID, Status: missionprojection.MissionStatusActive}, UpdatedAt: now.Add(2 * time.Minute), Checkpoint: missionprojection.Checkpoint{MissionID: missioncore.MissionID(missionID), Version: 2, UpdatedAt: now.Add(2 * time.Minute)}}
	recorder := &todoProgressAuditRecorder{err: errors.New("audit unavailable")}
	repository := newProductionTodoProgressProjectionRepository(base, base, todos, env.Clock)
	repository.SetAuditStore(recorder)
	if err := repository.SaveForTenant(ctx, tenantID, model); err == nil {
		t.Fatal("expected audit failure")
	}
	recorder.err = nil
	if err := repository.SaveForTenant(ctx, tenantID, model); err != nil {
		t.Fatalf("retry projection audit: %v", err)
	}
	if len(recorder.events) != 2 || recorder.events[0].AuditID == "" || recorder.events[0].AuditID != recorder.events[1].AuditID || recorder.events[1].Action != "todo.execution_progress.projected" {
		t.Fatalf("retried progress audits = %+v", recorder.events)
	}
}

func TestTodoProgressProjectionAuditsGeneralTodoExecutionCompletion(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	const (
		tenantID  = "tenant-progress-completed"
		todoID    = "todo-progress-completed"
		missionID = "mission-progress-completed"
	)
	now := env.Clock.Now().UTC()
	todos := todoapp.NewRepository(env.DBRouter)
	item := todoapp.TodoItem{
		TenantID: tenantID, TodoID: todoID, Title: "Complete general Todo", Status: todoapp.TodoStatusInProgress,
		WorkKind: todoapp.TodoWorkKindGeneral, AutomationMode: todoapp.TodoAutomationModeManaged,
		CreatedBy: "owner", CreatedAt: now, UpdatedAt: now, Version: 1,
	}
	if err := todos.CreateTodo(ctx, item); err != nil {
		t.Fatal(err)
	}
	item, err := todos.GetTodoForTenant(ctx, tenantID, todoID)
	if err != nil {
		t.Fatal(err)
	}
	state := todoapp.TodoAutomationState{
		TenantID: tenantID, ProposalID: "proposal-completed", TodoID: todoID, TodoVersion: item.Version,
		Status: todoapp.TodoAutomationStatusExecuting, ConversationID: "conversation-completed",
		PlanDigest: "digest-completed", ApprovedPlanDigest: "digest-completed", DecisionBy: "owner", CreatedAt: now, UpdatedAt: now,
	}
	if err := todos.SaveTodoAutomation(ctx, state); err != nil {
		t.Fatal(err)
	}
	binding, acquired, err := todos.BeginTodoMissionExecution(ctx, item, state, "owner", "claim-completed", now)
	if err != nil || !acquired {
		t.Fatalf("begin binding=%+v acquired=%v err=%v", binding, acquired, err)
	}
	binding.MissionID = missionID
	for _, status := range []string{
		todoapp.TodoMissionBindingStatusMaterialized,
		todoapp.TodoMissionBindingStatusDispatching,
		todoapp.TodoMissionBindingStatusExecuting,
	} {
		binding.Status = status
		binding.UpdatedAt = binding.UpdatedAt.Add(time.Second)
		if err := todos.SaveTodoMissionBinding(ctx, binding); err != nil {
			t.Fatalf("advance binding to %s: %v", status, err)
		}
		binding, err = todos.GetTodoMissionBinding(ctx, tenantID, todoID)
		if err != nil {
			t.Fatal(err)
		}
	}
	base := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	audit := &todoProgressAuditRecorder{}
	repository := newProductionTodoProgressProjectionRepository(base, base, todos, env.Clock)
	repository.SetAuditStore(audit)
	model := missionprojection.ReadModel{
		TenantID:  tenantID,
		Mission:   missionprojection.MissionView{TenantID: tenantID, MissionID: missionID, OriginKind: "todo", OriginTodoID: todoID, Status: missionprojection.MissionStatusCompleted},
		TaskBoard: missionprojection.TaskBoardView{Tasks: []missionprojection.TaskCardView{{TaskID: "task-1", Status: missionprojection.TaskStatusApproved, QAStatus: missionprojection.QAStatusApproved}}},
		UpdatedAt: now.Add(time.Minute), Checkpoint: missionprojection.Checkpoint{MissionID: missionID, Version: 4, UpdatedAt: now.Add(time.Minute)},
	}
	if err := repository.SaveForTenant(ctx, tenantID, model); err != nil {
		t.Fatal(err)
	}
	if len(audit.events) != 2 || audit.events[0].Action != "todo.execution_progress.projected" || audit.events[1].Action != "todo.execution.completed" || audit.events[1].TenantID != tenantID || audit.events[1].AuditID == "" {
		t.Fatalf("completion audit events = %+v", audit.events)
	}
	if err := repository.SaveForTenant(ctx, tenantID, model); err != nil {
		t.Fatal(err)
	}
	if len(audit.events) != 4 || audit.events[1].AuditID != audit.events[3].AuditID {
		t.Fatalf("completion audit replay identity = %+v", audit.events)
	}
}

func TestProjectExecutionRealtimeEventIdentityIncludesTenantAndChannelIdentity(t *testing.T) {
	firstChannel, err := platformrealtime.ProjectChannel("tenant-a", "shared-project")
	if err != nil {
		t.Fatal(err)
	}
	secondChannel, err := platformrealtime.ProjectChannel("tenant-b", "shared-project")
	if err != nil {
		t.Fatal(err)
	}
	firstID := projectExecutionRealtimeEventID("tenant-a", "shared-project", "shared-todo", "shared-mission", 3)
	secondID := projectExecutionRealtimeEventID("tenant-b", "shared-project", "shared-todo", "shared-mission", 3)
	if firstChannel == secondChannel || firstID == secondID {
		t.Fatalf("same project identity collided across tenants: channels=%q/%q ids=%q/%q", firstChannel, secondChannel, firstID, secondID)
	}
}

func TestTodoProgressReconcilerAppendsOneTerminalRunNoticeToCanonicalConversation(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	const (
		tenantID       = "tenant-runtime-notice"
		conversationID = "conversation-runtime-notice"
		missionID      = "mission-runtime-notice"
		taskID         = "task-runtime-notice"
		agentID        = "agent-runtime-notice"
	)
	repository := discussionsession.NewRepository(env.DBRouter)
	conversation := discussionsession.ConversationRecord{
		ConversationID: conversationID, TenantID: tenantID, PurposeKind: "todo", PurposeID: "todo-runtime-notice",
		Title: "Runtime notice", CreatedBy: "owner-runtime-notice", CreatedAt: env.Clock.Now(), UpdatedAt: env.Clock.Now(),
	}
	if err := repository.SaveConversation(ctx, conversation); err != nil {
		t.Fatalf("SaveConversation: %v", err)
	}
	if err := repository.SaveMember(ctx, discussionsession.ConversationMemberRecord{
		ConversationID: conversationID, MemberID: agentID, MemberKind: "agent", DisplayName: "Worker", CreatedAt: env.Clock.Now(),
	}); err != nil {
		t.Fatalf("SaveMember: %v", err)
	}
	if err := repository.SaveMissionLink(ctx, discussionsession.LinkedMissionRecord{
		ConversationID: conversationID, MissionID: missionID, Title: "Runtime Mission", CreatedAt: env.Clock.Now(),
	}); err != nil {
		t.Fatalf("SaveMissionLink: %v", err)
	}
	service := discussionsession.NewService(discussionsession.ServiceOptions{Repository: repository, Clock: env.Clock, IDGenerator: env.IDGenerator})
	reconciler := newProductionTodoProgressProjectionRepository(nil, nil, nil, env.Clock)
	reconciler.SetDiscussionService(service)
	model := missionprojection.ReadModel{
		TenantID:   tenantID,
		Mission:    missionprojection.MissionView{TenantID: tenantID, MissionID: missionID, OriginKind: "todo", OriginTodoID: "todo-runtime-notice"},
		Discussion: missionprojection.DiscussionView{ConversationID: conversationID},
		RuntimeSessions: []missionprojection.RuntimeSessionView{{
			RunID: "run-runtime-notice", AgentID: agentcore.AgentID(agentID), TaskID: missioncore.TaskID(taskID),
			Status: missionprojection.RuntimeSessionCompleted, LastMessage: "<think>private execution details</think>\n```orrery-public-summary\nFocused and full tests passed.\n```\n```orrery-agent-result\n{\"run_id\":\"secret\"}\n```",
		}},
	}
	if err := reconciler.reconcileTodoConversationRuntimeNotices(ctx, model); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	if err := reconciler.reconcileTodoConversationRuntimeNotices(ctx, model); err != nil {
		t.Fatalf("idempotent reconcile: %v", err)
	}
	view, err := repository.GetConversation(ctx, conversationID)
	if err != nil {
		t.Fatalf("GetConversation: %v", err)
	}
	if len(view.Messages) != 1 || view.Messages[0].MissionID != missionID || view.Messages[0].MissionTaskID != taskID || !strings.Contains(view.Messages[0].Content, "Agent 已完成任务") || !strings.Contains(view.Messages[0].Content, "Focused and full tests passed") || strings.Contains(view.Messages[0].Content, taskID) || strings.Contains(view.Messages[0].Content, agentID) || strings.Contains(view.Messages[0].Content, "orrery-agent-result") || strings.Contains(view.Messages[0].Content, "private execution details") {
		t.Fatalf("terminal runtime notices = %+v", view.Messages)
	}
}

type failOnceProjectExecutionRealtimeAppender struct {
	delegate projectExecutionRealtimeAppender
	failed   bool
}

type todoProgressAuditRecorder struct {
	events []operations.AuditEventDTO
	err    error
}

func (r *todoProgressAuditRecorder) RecordAuditEvent(_ context.Context, event operations.AuditEventDTO) error {
	r.events = append(r.events, event)
	return r.err
}

func (a *failOnceProjectExecutionRealtimeAppender) Append(ctx context.Context, event platformrealtime.AppendEvent) (platformrealtime.Envelope, error) {
	if !a.failed {
		a.failed = true
		return platformrealtime.Envelope{}, errors.New("injected realtime outbox failure")
	}
	return a.delegate.Append(ctx, event)
}
