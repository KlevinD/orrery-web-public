package missioncontrol_test

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	discussion "orrery_v2/internal/mission/discussion"
	projection "orrery_v2/internal/mission/projection"
	missiontask "orrery_v2/internal/mission/task"
	missionteam "orrery_v2/internal/mission/team"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestLifecycleServiceAssignTaskTransitionsTaskAndProjection(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentProfileID: "agent-1", AgentInstanceID: "agent-1", AgentKind: agentcore.AgentKindAI, Enabled: true},
		{AgentProfileID: "agent-2", AgentInstanceID: "agent-2", AgentKind: agentcore.AgentKindAI, Enabled: true},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, AgentDirectory: agents})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}

	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}
	created, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{IdempotencyKey: "task-key", MissionID: missioncore.MissionID(mission.MissionID), AssigneeID: "agent-1", Goal: "Implement route", WorkerPersonalGoal: "Deliver working route", Scope: "backend"})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if created.TaskID == "" {
		t.Fatalf("CreateTask should generate backend task id: %+v", created)
	}

	receipt, err := service.AssignTask(ctx, viewer, missioncontrol.AssignTaskCommand{IdempotencyKey: "assign-key", MissionID: missioncore.MissionID(mission.MissionID), TaskID: missioncore.TaskID(created.TaskID), AssigneeID: "agent-2", Reason: "rebalance"})
	if err != nil {
		t.Fatalf("AssignTask returned error: %v", err)
	}
	if receipt.Status != "accepted" || receipt.TaskID != created.TaskID {
		t.Fatalf("receipt = %+v", receipt)
	}
	view, err := newAuthorizedQueryService(repo).GetMissionControlView(ctx, viewer, missioncore.MissionID(mission.MissionID))
	if err != nil {
		t.Fatalf("GetMissionControlView returned error: %v", err)
	}
	if len(view.TaskBoard.Tasks) != 1 || view.TaskBoard.Tasks[0].Status != "assigned" || view.TaskBoard.Tasks[0].Assignee != "agent-2" {
		t.Fatalf("task board = %+v", view.TaskBoard)
	}
}

func TestLifecycleServiceImportsHistoricalSchedulerAssignmentWithoutNormalActivationPath(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	tasks := missiontask.NewPlatformRepository()
	responsibilities := &capturingTaskResponsibilityAssignments{tasks: tasks}
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID: "agent-history", AgentInstanceID: "agent-history", AgentKind: agentcore.AgentKindAI, Enabled: true,
	}}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator, TaskRepo: tasks, AgentDirectory: agents, TaskResponsibility: responsibilities,
	})
	viewer := missioncontrol.ViewerRef{ViewerID: "scheduler-agent-task", ViewerKind: missioncontrol.ViewerKindSystem, TenantID: "tenant-history", AuthorizationSubjectID: "scheduler-agent-task"}
	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{
		MissionID: "mission-history", IdempotencyKey: "scheduled-history", Title: "Scheduled history", FinalGoal: "Materialize history",
		OriginKind: "scheduler", OriginID: "scheduled-history", InitialAgentProfileID: "agent-history",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey: "history-task", MissionID: missioncore.MissionID(mission.MissionID), TaskID: "task-history",
		AssigneeID: "agent-history", Goal: "Import one historical occurrence", WorkerPersonalGoal: "Import one historical occurrence",
		SourceKind: "scheduler_agent_task", SourceID: "scheduled-history", SourceStepID: "run-history",
	})
	if err != nil {
		t.Fatal(err)
	}
	command := missioncontrol.ImportHistoricalTaskAssignmentCommand{
		IdempotencyKey: "history-task:assign", MissionID: missioncore.MissionID(mission.MissionID), TaskID: missioncore.TaskID(created.TaskID),
		ScheduledTaskID: "scheduled-history", ScheduledRunID: "run-history", AssigneeID: "agent-history",
		ResponsibilityState: "fulfilled", Reason: "stored completed run", OccurredAt: env.Clock.Now(),
	}
	if _, err := service.ImportHistoricalTaskAssignment(ctx, viewer, command); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ImportHistoricalTaskAssignment(ctx, viewer, command); err != nil {
		t.Fatalf("replay historical assignment: %v", err)
	}
	if len(responsibilities.signals) != 0 || len(responsibilities.historicalSignals) != 1 {
		t.Fatalf("normal signals=%+v historical signals=%+v", responsibilities.signals, responsibilities.historicalSignals)
	}
	if responsibilities.observedStatus != missiontask.TaskStatusAssigned || responsibilities.historicalSignals[0].State != "fulfilled" ||
		responsibilities.historicalSignals[0].SourceKind != "scheduler_agent_task" || responsibilities.historicalSignals[0].SourceID != "scheduled-history" ||
		responsibilities.historicalSignals[0].SourceStepID != "run-history" {
		t.Fatalf("historical assignment observation = status:%q signal:%+v", responsibilities.observedStatus, responsibilities.historicalSignals[0])
	}
	view, err := newAuthorizedQueryService(repo).GetMissionControlView(ctx, viewer, missioncore.MissionID(mission.MissionID))
	if err != nil {
		t.Fatal(err)
	}
	if len(view.TaskBoard.Tasks) != 1 || view.TaskBoard.Tasks[0].Status != "assigned" {
		t.Fatalf("historical Task projection = %+v", view.TaskBoard.Tasks)
	}
}

func TestLifecycleServiceTaskCommandsReplayWithoutRegressingExecutionState(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{{AgentProfileID: "agent-1", AgentInstanceID: "agent-1", AgentKind: agentcore.AgentKindAI, Enabled: true}}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, AgentDirectory: agents})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}

	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-task-replay", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatal(err)
	}
	create := missioncontrol.CreateTaskCommand{
		IdempotencyKey: "task-replay", MissionID: missioncore.MissionID(mission.MissionID),
		AssigneeID: "agent-1", Goal: "Implement route", WorkerPersonalGoal: "Deliver working route",
	}
	first, err := service.CreateTask(ctx, viewer, create)
	if err != nil {
		t.Fatal(err)
	}
	assign := missioncontrol.AssignTaskCommand{
		IdempotencyKey: "assign-replay", MissionID: missioncore.MissionID(mission.MissionID),
		TaskID: missioncore.TaskID(first.TaskID), AssigneeID: "agent-1", Reason: "approved assignment",
	}
	firstReceipt, err := service.AssignTask(ctx, viewer, assign)
	if err != nil {
		t.Fatal(err)
	}
	secondReceipt, err := service.AssignTask(ctx, viewer, assign)
	if err != nil {
		t.Fatalf("AssignTask replay: %v", err)
	}
	if secondReceipt.TaskID != firstReceipt.TaskID || secondReceipt.Version != firstReceipt.Version {
		t.Fatalf("assignment replay changed receipt: first=%+v second=%+v", firstReceipt, secondReceipt)
	}

	replayed, err := service.CreateTask(ctx, viewer, create)
	if err != nil {
		t.Fatalf("CreateTask replay: %v", err)
	}
	if replayed.TaskID != first.TaskID || replayed.Status != string(projection.TaskStatusAssigned) {
		t.Fatalf("create replay regressed assigned task: first=%+v replayed=%+v", first, replayed)
	}
	view, err := newAuthorizedQueryService(repo).GetMissionControlView(ctx, viewer, missioncore.MissionID(mission.MissionID))
	if err != nil {
		t.Fatal(err)
	}
	if len(view.TaskBoard.Tasks) != 1 || view.TaskBoard.Tasks[0].Status != string(projection.TaskStatusAssigned) {
		t.Fatalf("task replay duplicated or regressed projection: %+v", view.TaskBoard.Tasks)
	}

	changed := create
	changed.Goal = "Use the same key for another request"
	if _, err := service.CreateTask(ctx, viewer, changed); !errors.Is(err, missioncontrol.ErrInvalidRequest) {
		t.Fatalf("CreateTask changed replay error = %v, want ErrInvalidRequest", err)
	}
}

func TestLifecycleServiceAddsTodoTaskAssigneesToCanonicalConversation(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentProfileID: "agent-1", AgentInstanceID: "agent-1", AgentKind: agentcore.AgentKindAI, Enabled: true},
		{AgentProfileID: "agent-2", AgentInstanceID: "agent-2", AgentKind: agentcore.AgentKindAI, Enabled: true},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, AgentDirectory: agents})
	memberships := &capturingCanonicalConversationMembership{}
	service.SetCanonicalConversationMembership(memberships)
	viewer := missioncontrol.ViewerRef{ViewerID: "todo-automation-agent", ViewerKind: missioncontrol.ViewerKindSystem, TenantID: "tenant-a"}
	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{
		IdempotencyKey: "mission-todo-chat", Title: "Todo execution", FinalGoal: "Implement",
		OriginKind: "todo", OriginID: "todo-1", OriginTodoID: "todo-1", ExecutionAttempt: 1,
		DiscussionMode: "existing_conversation", ExistingConversationID: "conversation-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey: "task-todo-chat", MissionID: missioncore.MissionID(mission.MissionID), AssigneeID: "agent-1", Goal: "Implement step",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AssignTask(ctx, viewer, missioncontrol.AssignTaskCommand{
		IdempotencyKey: "assign-todo-chat", MissionID: missioncore.MissionID(mission.MissionID), TaskID: missioncore.TaskID(created.TaskID), AssigneeID: "agent-2", Reason: "manual reassignment",
	}); err != nil {
		t.Fatal(err)
	}
	if len(memberships.todoSets) != 2 || len(memberships.sets) != 0 || len(memberships.todoRequests) != 0 || len(memberships.requests) != 0 {
		t.Fatalf("canonical membership reconciliation = regular:%+v todo:%+v", memberships.sets, memberships.todoSets)
	}
	for index, agentID := range []string{"agent-1", "agent-2"} {
		request := memberships.todoSets[index]
		if request.ActorKind != "system" || request.ActorID == "" || request.TenantID != "tenant-a" || request.ConversationID != "conversation-1" || request.MissionID != mission.MissionID || request.TodoID != "todo-1" || len(request.AgentIDs) != 1 || request.AgentIDs[0] != agentID {
			t.Fatalf("membership request[%d] = %+v", index, request)
		}
		wantSource := "todo-mission:tenant-a/todo-1/" + mission.MissionID
		if request.ManagedSource != wantSource {
			t.Fatalf("managed source = %q, want %q", request.ManagedSource, wantSource)
		}
	}
}

func TestLifecycleServiceAddsRegularMissionTaskAssigneeToCanonicalConversation(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentProfileID: "agent-1", AgentInstanceID: "agent-1", AgentKind: agentcore.AgentKindAI, Enabled: true},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, AgentDirectory: agents})
	memberships := &capturingCanonicalConversationMembership{}
	service.SetCanonicalConversationMembership(memberships)
	viewer := missioncontrol.ViewerRef{ViewerID: "mission-conversation-linker", ViewerKind: missioncontrol.ViewerKindSystem, TenantID: "tenant-a"}
	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{
		IdempotencyKey: "mission-chat", Title: "Mission execution", FinalGoal: "Implement", ExistingConversationID: "conversation-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey: "task-chat", MissionID: missioncore.MissionID(mission.MissionID), AssigneeID: "agent-1", Goal: "Implement step",
	}); err != nil {
		t.Fatal(err)
	}
	if len(memberships.sets) != 1 || len(memberships.todoSets) != 0 || len(memberships.requests) != 0 || len(memberships.todoRequests) != 0 {
		t.Fatalf("canonical membership reconciliation = regular:%+v todo:%+v", memberships.sets, memberships.todoSets)
	}
	request := memberships.sets[0]
	if request.ActorKind != "system" || request.ActorID != "mission-lifecycle" || request.TenantID != "tenant-a" || request.ConversationID != "conversation-1" || request.MissionID != mission.MissionID || request.TodoID != "" || len(request.AgentIDs) != 1 || request.AgentIDs[0] != "agent-1" {
		t.Fatalf("membership request = %+v", request)
	}
	if want := "mission-task-assignee:tenant-a/" + mission.MissionID; request.ManagedSource != want {
		t.Fatalf("managed source = %q, want %q", request.ManagedSource, want)
	}
}

type capturingCanonicalConversationMembership struct {
	requests     []missioncontrol.CanonicalConversationMembershipRequest
	todoRequests []missioncontrol.CanonicalConversationMembershipRequest
	sets         []missioncontrol.CanonicalConversationMembershipSetRequest
	todoSets     []missioncontrol.CanonicalConversationMembershipSetRequest
	err          error
}

func (p *capturingCanonicalConversationMembership) EnsureMissionAgentMember(_ context.Context, request missioncontrol.CanonicalConversationMembershipRequest) error {
	p.requests = append(p.requests, request)
	return p.err
}

func (p *capturingCanonicalConversationMembership) EnsureTodoMissionAgentMember(_ context.Context, request missioncontrol.CanonicalConversationMembershipRequest) error {
	p.todoRequests = append(p.todoRequests, request)
	return p.err
}

func (p *capturingCanonicalConversationMembership) ReconcileMissionAgentMembers(_ context.Context, request missioncontrol.CanonicalConversationMembershipSetRequest) error {
	p.sets = append(p.sets, request)
	return p.err
}

func (p *capturingCanonicalConversationMembership) ReconcileTodoMissionAgentMembers(_ context.Context, request missioncontrol.CanonicalConversationMembershipSetRequest) error {
	p.todoSets = append(p.todoSets, request)
	return p.err
}

func TestLifecycleServiceRecordsResponsibilityInsideTaskAssignmentTransaction(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	projectionRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	taskRepo := missiontask.NewPlatformRepository()
	responsibilities := &capturingTaskResponsibilityAssignments{tasks: taskRepo}
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentProfileID: "agent-1", AgentInstanceID: "agent-1", AgentKind: agentcore.AgentKindAI, Enabled: true},
		{AgentProfileID: "agent-2", AgentInstanceID: "agent-2", AgentKind: agentcore.AgentKindAI, Enabled: true},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, projectionRepo, missioncontrol.LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator, TaskRepo: taskRepo, AgentDirectory: agents, TaskResponsibility: responsibilities,
	})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}
	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-responsibility", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}
	created, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{IdempotencyKey: "task-responsibility", MissionID: missioncore.MissionID(mission.MissionID), AssigneeID: "agent-1", Goal: "Implement route", WorkerPersonalGoal: "Deliver working route"})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if _, err := service.AssignTask(ctx, viewer, missioncontrol.AssignTaskCommand{IdempotencyKey: "assign-responsibility", MissionID: missioncore.MissionID(mission.MissionID), TaskID: missioncore.TaskID(created.TaskID), AssigneeID: "agent-2", Reason: "activate responsibility"}); err != nil {
		t.Fatalf("AssignTask returned error: %v", err)
	}
	if len(responsibilities.signals) != 1 || responsibilities.signals[0].TaskID != created.TaskID || responsibilities.signals[0].AgentProfileID != "agent-2" {
		t.Fatalf("responsibility signals = %+v", responsibilities.signals)
	}
	if responsibilities.observedStatus != missiontask.TaskStatusAssigned {
		t.Fatalf("responsibility port observed task status %q, want assigned in the same transaction", responsibilities.observedStatus)
	}
}

func TestLifecycleServiceRollsBackTaskAssignmentWhenResponsibilityWriteFails(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	projectionRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	taskRepo := missiontask.NewPlatformRepository()
	writeErr := errors.New("responsibility store unavailable")
	responsibilities := &capturingTaskResponsibilityAssignments{tasks: taskRepo, err: writeErr}
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentProfileID: "agent-1", AgentInstanceID: "agent-1", AgentKind: agentcore.AgentKindAI, Enabled: true},
		{AgentProfileID: "agent-2", AgentInstanceID: "agent-2", AgentKind: agentcore.AgentKindAI, Enabled: true},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, projectionRepo, missioncontrol.LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator, TaskRepo: taskRepo, AgentDirectory: agents, TaskResponsibility: responsibilities,
	})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}
	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-rollback", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}
	created, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{IdempotencyKey: "task-rollback", MissionID: missioncore.MissionID(mission.MissionID), AssigneeID: "agent-1", Goal: "Implement route", WorkerPersonalGoal: "Deliver working route"})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	_, err = service.AssignTask(ctx, viewer, missioncontrol.AssignTaskCommand{IdempotencyKey: "assign-rollback", MissionID: missioncore.MissionID(mission.MissionID), TaskID: missioncore.TaskID(created.TaskID), AssigneeID: "agent-2", Reason: "activate responsibility"})
	if !errors.Is(err, writeErr) {
		t.Fatalf("AssignTask error = %v, want responsibility write error", err)
	}
	var record missiontask.TaskRecord
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var getErr error
		record, getErr = taskRepo.GetTask(ctx, tx, missiontask.TaskID(created.TaskID))
		return getErr
	}); err != nil {
		t.Fatalf("get task after rollback: %v", err)
	}
	if record.Status != missiontask.TaskStatusDraft || record.Contract.Assignee.AgentProfileRef == nil || record.Contract.Assignee.AgentProfileRef.ID != "agent-1" {
		t.Fatalf("task assignment was not rolled back: %+v", record)
	}
	view, err := newAuthorizedQueryService(projectionRepo).GetMissionControlView(ctx, viewer, missioncore.MissionID(mission.MissionID))
	if err != nil {
		t.Fatalf("GetMissionControlView returned error: %v", err)
	}
	if len(view.TaskBoard.Tasks) != 1 || view.TaskBoard.Tasks[0].Status != "created" || view.TaskBoard.Tasks[0].Assignee != "agent-1" {
		t.Fatalf("projection changed despite rollback: %+v", view.TaskBoard.Tasks)
	}
}

func TestLifecycleServiceMarksQAEnabledWorkerTasksForPlanAndCheck(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	projectionRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	taskRepo := missiontask.NewPlatformRepository()
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentInstanceID: "profile-worker", AgentProfileID: "profile-worker", AgentKind: agentcore.AgentKindAI, QAEnabled: true, Enabled: true},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, projectionRepo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, TaskRepo: taskRepo, AgentDirectory: agents})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}
	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}

	created, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{IdempotencyKey: "task-key", MissionID: missioncore.MissionID(mission.MissionID), AssigneeID: "profile-worker", Goal: "Implement route", WorkerPersonalGoal: "Deliver working route"})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}

	var record missiontask.TaskRecord
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var getErr error
		record, getErr = taskRepo.GetTask(ctx, tx, missiontask.TaskID(created.TaskID))
		return getErr
	}); err != nil {
		t.Fatalf("get task: %v", err)
	}
	if !record.Contract.QAEnabled || !record.Contract.QAPlanRequired || !record.Contract.QACheckRequired {
		t.Fatalf("QA-enabled worker task was not marked for QA plan/check: %+v", record.Contract)
	}
}

func TestLifecycleServiceMarksNonQAWorkerTasksAsPlanCompleted(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	projectionRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	taskRepo := missiontask.NewPlatformRepository()
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentInstanceID: "profile-worker", AgentProfileID: "profile-worker", AgentKind: agentcore.AgentKindAI, QAEnabled: false, Enabled: true},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, projectionRepo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, TaskRepo: taskRepo, AgentDirectory: agents})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}
	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}

	created, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{IdempotencyKey: "task-key", MissionID: missioncore.MissionID(mission.MissionID), AssigneeID: "profile-worker", Goal: "Implement route", WorkerPersonalGoal: "Deliver working route"})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}

	var record missiontask.TaskRecord
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var getErr error
		record, getErr = taskRepo.GetTask(ctx, tx, missiontask.TaskID(created.TaskID))
		return getErr
	}); err != nil {
		t.Fatalf("get task: %v", err)
	}
	if record.Contract.QAEnabled || record.Contract.QAPlanRequired || record.Contract.QACheckRequired || !record.Contract.QAPlanCompleted {
		t.Fatalf("non-QA worker task should bypass QA plan and be plan-complete: %+v", record.Contract)
	}
}

func TestLifecycleServiceForcesQAForTodoPlanTaskAssignedToExistingMissionAgent(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	projectionRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	taskRepo := missiontask.NewPlatformRepository()
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, projectionRepo, missioncontrol.LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator, TaskRepo: taskRepo,
		AgentDirectory: &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{}},
	})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}
	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-todo-qa", Title: "Todo execution", FinalGoal: "Ship Todo"})
	if err != nil {
		t.Fatal(err)
	}
	model, err := projectionRepo.GetMissionControl(ctx, missioncore.MissionID(mission.MissionID))
	if err != nil {
		t.Fatal(err)
	}
	model.Team.Members = append(model.Team.Members, projection.TeamMemberView{
		AgentID: "lead-agent", AgentProfileID: "lead-agent", AgentInstanceID: "lead-agent", AgentKind: agentcore.AgentKindAI, Status: projection.TeamMemberJoined,
	})
	model.Checkpoint.Version++
	if err := projectionRepo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	created, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey: "task-todo-qa", MissionID: missioncore.MissionID(mission.MissionID), AssigneeID: "lead-agent",
		Goal: "Implement Todo plan", WorkerPersonalGoal: "Deliver approved step", SourceKind: "todo_plan_step", SourceID: "step-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	var record missiontask.TaskRecord
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var getErr error
		record, getErr = taskRepo.GetTask(ctx, tx, missiontask.TaskID(created.TaskID))
		return getErr
	}); err != nil {
		t.Fatal(err)
	}
	if !record.Contract.QAEnabled || !record.Contract.QAPlanRequired || !record.Contract.QACheckRequired || record.Contract.QAPlanCompleted {
		t.Fatalf("Todo plan task bypassed required QA: %+v", record.Contract)
	}
}

func TestLifecycleServiceIgnoresQAEnabledForQAAgents(t *testing.T) {
	for _, agentID := range []string{"qa-plan-agent", "qa-check-agent"} {
		t.Run(agentID, func(t *testing.T) {
			ctx := context.Background()
			env := testkit.New(t)
			defer env.Close()
			projectionRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
			taskRepo := missiontask.NewPlatformRepository()
			agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
				{AgentInstanceID: agentcore.AgentInstanceID(agentID), AgentProfileID: agentcore.AgentProfileID(agentID), AgentKind: agentcore.AgentKindAI, QAEnabled: true, Enabled: true},
			}}
			service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, projectionRepo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, TaskRepo: taskRepo, AgentDirectory: agents})
			viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}
			mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v2"})
			if err != nil {
				t.Fatalf("CreateMission returned error: %v", err)
			}
			created, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{IdempotencyKey: "task-key", MissionID: missioncore.MissionID(mission.MissionID), AssigneeID: agentID, Goal: "Plan QA", WorkerPersonalGoal: "Deliver QA plan"})
			if err != nil {
				t.Fatalf("CreateTask returned error: %v", err)
			}

			var record missiontask.TaskRecord
			if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
				var getErr error
				record, getErr = taskRepo.GetTask(ctx, tx, missiontask.TaskID(created.TaskID))
				return getErr
			}); err != nil {
				t.Fatalf("get task: %v", err)
			}
			if record.Contract.QAEnabled || record.Contract.QAPlanRequired || record.Contract.QACheckRequired || !record.Contract.QAPlanCompleted {
				t.Fatalf("QA agent task should ignore qa_enabled to avoid recursion: %+v", record.Contract)
			}
		})
	}
}

func TestLifecycleServiceAddsTaskAssigneesToDiscussionGroup(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	discussions := &fakeDiscussionInitializer{}
	authzSink := &missionAuthzSink{}
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentProfileID: "agent-1", AgentInstanceID: "agent-1", AgentKind: agentcore.AgentKindAI, Enabled: true},
		{AgentProfileID: "agent-2", AgentInstanceID: "agent-2", AgentKind: agentcore.AgentKindAI, Enabled: true},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, AgentDirectory: agents, Discussion: discussions, AuthorizationRelations: authorization.NewRelationProjector(authzSink)})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser, TenantID: "tenant-assignment"}

	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}
	created, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{IdempotencyKey: "task-key", MissionID: missioncore.MissionID(mission.MissionID), AssigneeID: "agent-1", Goal: "Implement route", WorkerPersonalGoal: "Deliver working route", Scope: "backend"})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if _, err := service.AssignTask(ctx, viewer, missioncontrol.AssignTaskCommand{IdempotencyKey: "assign-key", MissionID: missioncore.MissionID(mission.MissionID), TaskID: missioncore.TaskID(created.TaskID), AssigneeID: "agent-2", Reason: "rebalance"}); err != nil {
		t.Fatalf("AssignTask returned error: %v", err)
	}

	memberByAgent := map[string]discussion.DiscussionMember{}
	for _, member := range discussions.members {
		if member.AgentProfileID != "" {
			memberByAgent[member.AgentProfileID.String()] = member
		}
	}
	for _, agentID := range []string{"agent-1", "agent-2"} {
		member, ok := memberByAgent[agentID]
		if !ok || member.DiscussionGroupID != "discussion-1" || member.MemberKind != discussion.DiscussionMemberAgent || !member.CanPost || !member.CanMention {
			t.Fatalf("discussion member for %s = %+v ok=%v all=%+v", agentID, member, ok, discussions.members)
		}
	}
	if !missionAuthzTupleExists(authzSink.written, "agent", "agent-1", "member", "discussion", "discussion-1") || !missionAuthzTupleExists(authzSink.written, "agent", "agent-2", "member", "discussion", "discussion-1") {
		t.Fatalf("discussion authz member tuples missing: %+v", authzSink.written)
	}
	for _, tuple := range authzSink.written {
		if tuple.Object.Type == "discussion" && tuple.Relation == "member" && (tuple.TenantID != "tenant-assignment" || tuple.Object.TenantID != "tenant-assignment") {
			t.Fatalf("discussion member tuple escaped mission tenant: %+v", tuple)
		}
	}
}

func missionAuthzTupleExists(tuples []authorization.RelationTuple, subjectKind string, subjectID string, relation string, objectType string, objectID string) bool {
	for _, tuple := range tuples {
		if tuple.Subject.Kind == subjectKind && tuple.Subject.ID == subjectID && tuple.Relation == relation && tuple.Object.Type == objectType && tuple.Object.ID == objectID {
			return true
		}
	}
	return false
}

func TestLifecycleServiceRejectsDirectoryTaskAssigneeWithoutProfileIdentity(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	taskRepo := missiontask.NewPlatformRepository()
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentInstanceID: "agent-1", AgentKind: agentcore.AgentKindAI, Enabled: true},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, TaskRepo: taskRepo, AgentDirectory: agents})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}

	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}
	_, err = service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{IdempotencyKey: "task-key", MissionID: missioncore.MissionID(mission.MissionID), AssigneeID: "agent-1", Goal: "Implement route", WorkerPersonalGoal: "Deliver working route"})
	if !errors.Is(err, missioncontrol.ErrInvalidRequest) {
		t.Fatalf("CreateTask error = %v, want ErrInvalidRequest", err)
	}
}

func TestLifecycleServiceRejectsDirectoryTaskAssigneeRuntimeInstanceBinding(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	taskRepo := missiontask.NewPlatformRepository()
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentProfileID: "agent-1", AgentInstanceID: "agent-1:runtime", AgentKind: agentcore.AgentKindAI, Enabled: true},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, TaskRepo: taskRepo, AgentDirectory: agents})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}

	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}
	_, err = service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{IdempotencyKey: "task-key", MissionID: missioncore.MissionID(mission.MissionID), AssigneeID: "agent-1", Goal: "Implement route", WorkerPersonalGoal: "Deliver working route"})
	if !errors.Is(err, missioncontrol.ErrInvalidRequest) {
		t.Fatalf("CreateTask error = %v, want ErrInvalidRequest", err)
	}
}

func TestLifecycleServiceResolvesStableTaskAssigneeToEnabledInstance(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	taskRepo := missiontask.NewPlatformRepository()
	discussions := &fakeDiscussionInitializer{}
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentProfileID: "profile-worker", AgentInstanceID: "profile-worker", AgentKind: agentcore.AgentKindAI, Enabled: true},
		{AgentProfileID: "profile-reviewer", AgentInstanceID: "profile-reviewer", AgentKind: agentcore.AgentKindAI, QAEnabled: true, Enabled: true},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{
		Clock:          env.Clock,
		IDGenerator:    env.IDGenerator,
		TaskRepo:       taskRepo,
		AgentDirectory: agents,
		Discussion:     discussions,
	})

	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}

	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}
	created, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{IdempotencyKey: "task-key", MissionID: missioncore.MissionID(mission.MissionID), AssigneeID: "profile-worker", Goal: "Implement route", WorkerPersonalGoal: "Deliver working route"})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if created.Assignee != "profile-worker" {
		t.Fatalf("task should expose stable assignee profile id, got %+v", created)
	}
	if _, err := service.AssignTask(ctx, viewer, missioncontrol.AssignTaskCommand{IdempotencyKey: "assign-key", MissionID: missioncore.MissionID(mission.MissionID), TaskID: missioncore.TaskID(created.TaskID), AssigneeID: "profile-reviewer", Reason: "needs QA gate"}); err != nil {
		t.Fatalf("AssignTask returned error: %v", err)
	}

	var record missiontask.TaskRecord
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var getErr error
		record, getErr = taskRepo.GetTask(ctx, tx, missiontask.TaskID(created.TaskID))
		return getErr
	}); err != nil {
		t.Fatalf("get task: %v", err)
	}
	if record.Contract.Assignee.AgentProfileRef == nil || record.Contract.Assignee.AgentProfileRef.ID != "profile-reviewer" {
		t.Fatalf("task contract should use resolved assignee profile: %+v", record.Contract.Assignee)
	}
	view, err := newAuthorizedQueryService(repo).GetMissionControlView(ctx, viewer, missioncore.MissionID(mission.MissionID))
	if err != nil {
		t.Fatalf("GetMissionControlView returned error: %v", err)
	}
	if len(view.TaskBoard.Tasks) != 1 || view.TaskBoard.Tasks[0].Assignee != "profile-reviewer" {
		t.Fatalf("task board should expose stable reassigned profile id: %+v", view.TaskBoard)
	}
	teamMembers := map[string]missioncontrol.TeamMemberDTO{}
	for _, member := range view.Team.Members {
		teamMembers[member.AgentID] = member
	}
	for _, want := range []struct {
		profileID  string
		instanceID string
	}{
		{profileID: "profile-worker", instanceID: "profile-worker"},
		{profileID: "profile-reviewer", instanceID: "profile-reviewer"},
	} {
		member, ok := teamMembers[want.profileID]
		if !ok || member.AgentProfileID != want.profileID || member.AgentInstanceID != want.instanceID || member.Status != "joined" {
			t.Fatalf("team member for %s = %+v ok=%v all=%+v", want.profileID, member, ok, view.Team.Members)
		}
	}
	memberByInstance := map[string]discussion.DiscussionMember{}
	for _, member := range discussions.members {
		if member.AgentInstanceID != "" {
			memberByInstance[member.AgentInstanceID.String()] = member
		}
	}
	for _, want := range []struct {
		profileID  agentcore.AgentProfileID
		instanceID agentcore.AgentInstanceID
	}{
		{profileID: "profile-worker", instanceID: "profile-worker"},
		{profileID: "profile-reviewer", instanceID: "profile-reviewer"},
	} {
		member := memberByInstance[want.instanceID.String()]
		if member.AgentProfileID != want.profileID || member.MemberKind != discussion.DiscussionMemberAgent || !member.CanPost || !member.CanMention {
			t.Fatalf("discussion member for %s = %+v all=%+v", want.instanceID, member, discussions.members)
		}
	}
}

func TestLifecycleServicePreservesExistingTeamRoleWhenTaskAssigned(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	taskRepo := missiontask.NewPlatformRepository()
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{
		Clock:       env.Clock,
		IDGenerator: env.IDGenerator,
		TaskRepo:    taskRepo,
	})

	missionID := missioncore.MissionID("mission-1")
	now := env.Clock.Now().UTC()
	model := projection.ReadModel{
		Mission: projection.MissionView{MissionID: missionID, Title: "Launch", FinalGoal: "Ship", Status: projection.MissionStatusActive, CreatedAt: now},
		Team: projection.TeamView{Members: []projection.TeamMemberView{{
			AgentID:         "lead-agent",
			AgentProfileID:  "lead-agent",
			AgentInstanceID: "lead-agent",
			AgentKind:       agentcore.AgentKindAI,
			Status:          projection.TeamMemberJoined,
			MissionRole:     string(missionteam.MissionRoleLead),
			JoinedAt:        now,
		}}},
		Checkpoint: projection.Checkpoint{MissionID: missionID, Version: 1, UpdatedAt: now},
		UpdatedAt:  now,
	}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("seed projection: %v", err)
	}

	if _, err := service.CreateTask(ctx, missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.CreateTaskCommand{
		IdempotencyKey:     "task-key",
		MissionID:          missionID,
		AssigneeID:         "lead-agent",
		Goal:               "Coordinate delivery",
		WorkerPersonalGoal: "Coordinate delivery",
	}); err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}

	view, err := newAuthorizedQueryService(repo).GetMissionControlView(ctx, missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}, missionID)
	if err != nil {
		t.Fatalf("GetMissionControlView returned error: %v", err)
	}
	if len(view.Team.Members) != 1 || view.Team.Members[0].MissionRole != string(missionteam.MissionRoleLead) {
		t.Fatalf("team member role changed after task assignment: %+v", view.Team.Members)
	}
}

type capturingTaskResponsibilityAssignments struct {
	tasks             missiontask.Repository
	err               error
	signals           []missioncontrol.TaskResponsibilityAssignmentSignal
	historicalSignals []missioncontrol.HistoricalTaskResponsibilityAssignmentSignal
	observedStatus    missiontask.TaskStatus
}

func (c *capturingTaskResponsibilityAssignments) RecordTaskAssignment(ctx context.Context, tx uow.Tx, signal missioncontrol.TaskResponsibilityAssignmentSignal) error {
	c.signals = append(c.signals, signal)
	if tx == nil {
		return errors.New("assignment transaction is nil")
	}
	if c.tasks != nil {
		record, err := c.tasks.GetTask(ctx, tx, missiontask.TaskID(signal.TaskID))
		if err != nil {
			return err
		}
		c.observedStatus = record.Status
	}
	return c.err
}

func (c *capturingTaskResponsibilityAssignments) RecordHistoricalTaskAssignment(ctx context.Context, tx uow.Tx, signal missioncontrol.HistoricalTaskResponsibilityAssignmentSignal) error {
	c.historicalSignals = append(c.historicalSignals, signal)
	if tx == nil {
		return errors.New("historical assignment transaction is nil")
	}
	if c.tasks != nil {
		record, err := c.tasks.GetTask(ctx, tx, missiontask.TaskID(signal.TaskID))
		if err != nil {
			return err
		}
		c.observedStatus = record.Status
	}
	return c.err
}
