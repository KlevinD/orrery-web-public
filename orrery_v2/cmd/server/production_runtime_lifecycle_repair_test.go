package main

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	missioncore "orrery_v2/internal/mission/core"
	missionprojection "orrery_v2/internal/mission/projection"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestProductionRuntimeLifecycleRepairPublishesOnlyExactDurableSubmissionRun(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	model := missionprojection.ReadModel{
		TenantID:   "tenant-a",
		Mission:    missionprojection.MissionView{TenantID: "tenant-a", MissionID: "mission-1", Status: missionprojection.MissionStatusActive},
		Discussion: missionprojection.DiscussionView{ConversationID: "conversation-1", DiscussionGroupID: "group-1"},
		RuntimeSessions: []missionprojection.RuntimeSessionView{
			{RunID: "run-completed", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "codex", QueueID: "mission:mission-1", Status: missionprojection.RuntimeSessionRunning},
			{RunID: "run-still-active", AgentID: "agent-2", TaskID: "task-2", Status: missionprojection.RuntimeSessionRunning},
		},
	}
	publisher := &capturingRuntimeLifecycleRepairPublisher{}
	notifier := &capturingPostCommitRepairNotifier{}
	responsibility := &capturingRuntimeLifecycleRepairResponsibility{}
	repair := &productionRuntimeLifecycleRepair{
		catalog: productionRuntimeLifecycleRepairCatalogStub{models: []missionprojection.ReadModel{model}},
		tasks: productionRuntimeLifecycleRepairTaskStub{submissions: map[missiontask.TaskID]missiontask.TaskSubmission{
			"task-1": {TaskID: "task-1", RunID: "run-completed", AgentID: "agent-1", Summary: agentcore.Summary{Text: "durable result"}},
			"task-2": {TaskID: "task-2", RunID: "another-run", AgentID: "agent-2"},
		}},
		unit: env.UnitOfWork, publisher: publisher, notifier: notifier, responsibility: responsibility,
	}

	count, err := repair.Reconcile(context.Background(), 100)
	if err != nil || count != 1 || len(publisher.events) != 1 {
		t.Fatalf("Reconcile count=%d events=%+v err=%v", count, publisher.events, err)
	}
	event := publisher.events[0]
	if event.Kind != runtimedaemon.RuntimeLifecycleCompleted || event.TenantID != "tenant-a" || event.MissionID != missioncore.MissionID("mission-1") || event.TaskID != missioncore.TaskID("task-1") || event.RunID != "run-completed" || event.Message != "durable result" {
		t.Fatalf("repair event = %+v", event)
	}
	if notifier.calls != 1 || notifier.input.ContextPack.ConversationID != "conversation-1" || notifier.result.RunID != "run-completed" {
		t.Fatalf("repair notifier calls=%d input=%+v result=%+v", notifier.calls, notifier.input, notifier.result)
	}
	if responsibility.calls != 1 || responsibility.tenantID != "tenant-a" || responsibility.taskID != "task-1" || responsibility.runID != "run-completed" {
		t.Fatalf("repair responsibility = %+v", responsibility)
	}
}

func TestProductionRuntimeLifecycleRepairProjectsTerminalQueueFailure(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	model := missionprojection.ReadModel{
		TenantID: "tenant-a",
		Mission:  missionprojection.MissionView{TenantID: "tenant-a", MissionID: "mission-1", Status: missionprojection.MissionStatusActive},
		TaskBoard: missionprojection.TaskBoardView{Tasks: []missionprojection.TaskCardView{{
			TaskID: "task-1", Status: missionprojection.TaskStatusAssigned,
		}}},
		RuntimeSessions: []missionprojection.RuntimeSessionView{{
			RunID: "run-failed", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "pi",
			QueueID: "mission:mission-1:agent:agent-1", Status: missionprojection.RuntimeSessionQueued,
		}},
	}
	publisher := &capturingRuntimeLifecycleRepairPublisher{}
	responsibility := &capturingRuntimeLifecycleRepairResponsibility{failureErr: errors.New("responsibility attempt not found")}
	repair := &productionRuntimeLifecycleRepair{
		catalog: productionRuntimeLifecycleRepairCatalogStub{models: []missionprojection.ReadModel{model}},
		tasks:   productionRuntimeLifecycleRepairTaskStub{},
		unit:    env.UnitOfWork, publisher: publisher, responsibility: responsibility,
		execution: productionRuntimeLifecycleRepairExecutionStub{statuses: map[string]execution.JobStatus{
			"run-failed": {ID: "run-failed", Status: execution.StatusFailed, LastError: "database write failed", FailureReason: "max_attempts_exhausted"},
		}},
	}

	count, err := repair.Reconcile(context.Background(), 100)
	if err != nil || count != 1 || len(publisher.failures) != 1 {
		t.Fatalf("Reconcile count=%d failures=%+v err=%v", count, publisher.failures, err)
	}
	failure := publisher.failures[0]
	if failure.Kind != runtimedaemon.RuntimeLifecycleFailed || failure.RunID != "run-failed" || failure.Code != "agent_queue_failed" || failure.Message != "database write failed" {
		t.Fatalf("queue failure event = %+v", failure)
	}
	if responsibility.failureCalls != 1 {
		t.Fatalf("responsibility failure calls = %d", responsibility.failureCalls)
	}
}

func TestProductionRuntimeLifecycleRepairSkipsSupersededActiveSession(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	queuedAt := time.Date(2026, 8, 9, 1, 0, 0, 0, time.UTC)
	model := missionprojection.ReadModel{
		TenantID: "tenant-a",
		Mission:  missionprojection.MissionView{TenantID: "tenant-a", MissionID: "mission-1", Status: missionprojection.MissionStatusActive},
		TaskBoard: missionprojection.TaskBoardView{Tasks: []missionprojection.TaskCardView{
			{TaskID: "task-1", Status: missionprojection.TaskStatusRejected},
			{TaskID: "task-2", Status: missionprojection.TaskStatusAssigned},
		}},
		RuntimeSessions: []missionprojection.RuntimeSessionView{
			{
				RunID: "run-superseded", AgentID: "agent-1", TaskID: "task-1",
				Status: missionprojection.RuntimeSessionQueued, QueuedAt: queuedAt, UpdatedAt: queuedAt,
			},
			{
				RunID: "run-later-terminal", AgentID: "agent-1", TaskID: "task-1",
				Status: missionprojection.RuntimeSessionFailed, QueuedAt: queuedAt.Add(time.Hour), UpdatedAt: queuedAt.Add(2 * time.Hour),
			},
			{
				RunID: "run-current", AgentID: "agent-2", TaskID: "task-2",
				Status: missionprojection.RuntimeSessionQueued, QueuedAt: queuedAt, UpdatedAt: queuedAt,
			},
		},
	}
	publisher := &capturingRuntimeLifecycleRepairPublisher{}
	repair := &productionRuntimeLifecycleRepair{
		catalog:   productionRuntimeLifecycleRepairCatalogStub{models: []missionprojection.ReadModel{model}},
		tasks:     productionRuntimeLifecycleRepairTaskStub{},
		unit:      env.UnitOfWork,
		publisher: publisher,
		execution: productionRuntimeLifecycleRepairExecutionStub{statuses: map[string]execution.JobStatus{
			"run-superseded": {ID: "run-superseded", Status: execution.StatusFailed, LastError: "obsolete failure"},
			"run-current":    {ID: "run-current", Status: execution.StatusFailed, LastError: "current failure"},
		}},
	}

	count, err := repair.Reconcile(context.Background(), 100)
	if err != nil || count != 1 {
		t.Fatalf("Reconcile count=%d err=%v", count, err)
	}
	if len(publisher.failures) != 1 || publisher.failures[0].RunID != "run-current" {
		t.Fatalf("repair failures = %+v", publisher.failures)
	}
}

func TestProductionRuntimeLifecycleRepairSkipsInactiveMission(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	model := missionprojection.ReadModel{
		TenantID: "tenant-a",
		Mission: missionprojection.MissionView{
			TenantID: "tenant-a", MissionID: "mission-paused", Status: missionprojection.MissionStatusPaused,
		},
		TaskBoard: missionprojection.TaskBoardView{Tasks: []missionprojection.TaskCardView{{
			TaskID: "task-1", Status: missionprojection.TaskStatusAssigned,
		}}},
		RuntimeSessions: []missionprojection.RuntimeSessionView{{
			RunID: "run-failed", AgentID: "agent-1", TaskID: "task-1", Status: missionprojection.RuntimeSessionQueued,
		}},
	}
	publisher := &capturingRuntimeLifecycleRepairPublisher{}
	repair := &productionRuntimeLifecycleRepair{
		catalog: productionRuntimeLifecycleRepairCatalogStub{models: []missionprojection.ReadModel{model}},
		tasks:   productionRuntimeLifecycleRepairTaskStub{},
		unit:    env.UnitOfWork, publisher: publisher,
		execution: productionRuntimeLifecycleRepairExecutionStub{statuses: map[string]execution.JobStatus{
			"run-failed": {ID: "run-failed", Status: execution.StatusFailed},
		}},
	}

	count, err := repair.Reconcile(context.Background(), 100)
	if err != nil || count != 0 || len(publisher.failures) != 0 {
		t.Fatalf("Reconcile count=%d failures=%+v err=%v", count, publisher.failures, err)
	}
}

func TestProductionRuntimeLifecycleRepairSkipsInactiveTask(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	model := missionprojection.ReadModel{
		TenantID: "tenant-a",
		Mission: missionprojection.MissionView{
			TenantID: "tenant-a", MissionID: "mission-1", Status: missionprojection.MissionStatusActive,
		},
		TaskBoard: missionprojection.TaskBoardView{Tasks: []missionprojection.TaskCardView{{
			TaskID: "task-blocked", Status: missionprojection.TaskStatusBlocked,
		}}},
		RuntimeSessions: []missionprojection.RuntimeSessionView{{
			RunID: "run-cancelled", AgentID: "agent-1", TaskID: "task-blocked", Status: missionprojection.RuntimeSessionQueued,
		}},
	}
	publisher := &capturingRuntimeLifecycleRepairPublisher{}
	repair := &productionRuntimeLifecycleRepair{
		catalog: productionRuntimeLifecycleRepairCatalogStub{models: []missionprojection.ReadModel{model}},
		tasks:   productionRuntimeLifecycleRepairTaskStub{},
		unit:    env.UnitOfWork, publisher: publisher,
		execution: productionRuntimeLifecycleRepairExecutionStub{statuses: map[string]execution.JobStatus{
			"run-cancelled": {ID: "run-cancelled", Status: execution.StatusCancelled},
		}},
	}

	count, err := repair.Reconcile(context.Background(), 100)
	if err != nil || count != 0 || len(publisher.failures) != 0 {
		t.Fatalf("Reconcile count=%d failures=%+v err=%v", count, publisher.failures, err)
	}
}

type productionRuntimeLifecycleRepairCatalogStub struct {
	models []missionprojection.ReadModel
}

func (s productionRuntimeLifecycleRepairCatalogStub) ListMissionCatalog(context.Context, string, int) ([]missionprojection.ReadModel, bool, error) {
	return append([]missionprojection.ReadModel(nil), s.models...), false, nil
}

type productionRuntimeLifecycleRepairTaskStub struct {
	submissions map[missiontask.TaskID]missiontask.TaskSubmission
}

func (s productionRuntimeLifecycleRepairTaskStub) LatestSubmission(_ context.Context, _ uow.Tx, taskID missiontask.TaskID) (missiontask.TaskSubmission, bool, error) {
	submission, ok := s.submissions[taskID]
	return submission, ok, nil
}

type capturingRuntimeLifecycleRepairPublisher struct {
	events   []runtimedaemon.RuntimeLifecycleEvent
	failures []runtimedaemon.RuntimeFailureEvent
}

type capturingRuntimeLifecycleRepairResponsibility struct {
	calls, failureCalls     int
	tenantID, taskID, runID string
	failureErr              error
}

func (r *capturingRuntimeLifecycleRepairResponsibility) Completed(_ context.Context, tenantID, taskID, runID string) error {
	r.calls++
	r.tenantID, r.taskID, r.runID = tenantID, taskID, runID
	return nil
}

func (r *capturingRuntimeLifecycleRepairResponsibility) Failed(context.Context, string, string, string, runtimedaemon.RuntimeLifecycleKind, error) error {
	r.failureCalls++
	return r.failureErr
}

func (p *capturingRuntimeLifecycleRepairPublisher) PublishRuntimeLifecycle(_ context.Context, event runtimedaemon.RuntimeLifecycleEvent) error {
	p.events = append(p.events, event)
	return nil
}

func (p *capturingRuntimeLifecycleRepairPublisher) PublishRuntimeFailure(_ context.Context, event runtimedaemon.RuntimeFailureEvent) error {
	p.failures = append(p.failures, event)
	return nil
}

type productionRuntimeLifecycleRepairExecutionStub struct {
	statuses map[string]execution.JobStatus
}

func (s productionRuntimeLifecycleRepairExecutionStub) GetStatus(_ context.Context, runID string) (execution.JobStatus, bool, error) {
	status, ok := s.statuses[runID]
	return status, ok, nil
}
