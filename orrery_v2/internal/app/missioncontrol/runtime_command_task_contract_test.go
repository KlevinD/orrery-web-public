package missioncontrol_test

import (
	"context"
	"strings"
	"testing"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/projection"
)

func TestCommandServiceStartAgentRunHydratesCanonicalTaskContract(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 30, 5, 30, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.TaskBoard.Tasks[0].Goal = ""
	model.TaskBoard.Tasks[0].WorkerPersonalGoalSummary = ""
	model.TaskBoard.Tasks[0].ExpectedDeliverables = nil
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	runStore := &capturingRunStore{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, &capturingMissionEventSink{}, &sequenceContextIDGenerator{values: []string{"run-contract", "event-contract"}}, fixedClock{at: at})
	service.SetRuntimeTaskProjectionReader(&capturingRuntimeTaskProjectionReader{task: projection.TaskCardView{
		TaskID: "task-1", Goal: "Validate the playable game", WorkerPersonalGoalSummary: "Verify a complete game loop",
		ExpectedDeliverables: []projection.ExpectedDeliverableView{{ID: "expected-1", Title: "Runtime verification"}},
	}})

	_, err := service.StartAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.StartAgentRunCommand{
		IdempotencyKey: "start-contract", MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RuntimeKind: "codex",
	})
	if err != nil {
		t.Fatalf("StartAgentRun: %v", err)
	}
	if len(runStore.runs) != 1 {
		t.Fatalf("runs = %+v", runStore.runs)
	}
	run := runStore.runs[0]
	if run.Task.Goal != "Validate the playable game" || run.Task.WorkerPersonalGoal != "Verify a complete game loop" || len(run.Task.ExpectedDeliverables) != 1 || run.Task.ExpectedDeliverables[0].ID != "expected-1" {
		t.Fatalf("hydrated task = %+v", run.Task)
	}
	if run.Request.ContextPack.TaskContractSummary != "Validate the playable game" || len(run.Request.InputMessages) == 0 || !strings.Contains(run.Request.InputMessages[len(run.Request.InputMessages)-1].Content, "expected-1") {
		t.Fatalf("hydrated runtime request = %+v", run.Request)
	}
	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil || updated.TaskBoard.Tasks[0].Goal != "Validate the playable game" || len(updated.TaskBoard.Tasks[0].ExpectedDeliverables) != 1 {
		t.Fatalf("persisted task contract = %+v err=%v", updated.TaskBoard.Tasks, err)
	}
}

func TestCommandServiceStartAgentRunRejectsMismatchedCanonicalTask(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 30, 5, 31, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	if err := repo.Save(ctx, appRuntimeReadModel(at)); err != nil {
		t.Fatal(err)
	}
	runStore := &capturingRunStore{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, &capturingMissionEventSink{}, &sequenceContextIDGenerator{}, fixedClock{at: at})
	service.SetRuntimeTaskProjectionReader(&capturingRuntimeTaskProjectionReader{task: projection.TaskCardView{TaskID: "task-other", Goal: "Wrong task"}})

	_, err := service.StartAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.StartAgentRunCommand{
		IdempotencyKey: "start-mismatch", MissionID: "mission-1", TaskID: "task-1", AgentID: "agent-1", RuntimeKind: "codex",
	})
	if err != missioncontrol.ErrInvalidRequest || len(runStore.runs) != 0 {
		t.Fatalf("mismatched canonical task error=%v runs=%+v", err, runStore.runs)
	}
}

func TestCommandServiceRetryAgentRunHydratesCanonicalTaskContract(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 30, 7, 30, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.TaskBoard.Tasks[0].Status = projection.TaskStatusBlocked
	model.TaskBoard.Tasks[0].Goal = ""
	model.TaskBoard.Tasks[0].WorkerPersonalGoalSummary = ""
	model.TaskBoard.Tasks[0].ExpectedDeliverables = nil
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-failed", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "codex",
		Status: projection.RuntimeSessionFailed, CompletedAt: at.Add(-time.Minute), UpdatedAt: at.Add(-time.Minute),
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	runStore := &capturingRunStore{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, &capturingMissionEventSink{}, &sequenceContextIDGenerator{values: []string{"run-retry-contract", "event-retry-contract"}}, fixedClock{at: at})
	service.SetRuntimeTaskProjectionReader(&capturingRuntimeTaskProjectionReader{task: projection.TaskCardView{
		TaskID: "task-1", Status: projection.TaskStatusStarted, Goal: "Validate the playable game", WorkerPersonalGoalSummary: "Verify a complete game loop",
		ExpectedDeliverables: []projection.ExpectedDeliverableView{{ID: "expected-1", Title: "Runtime verification"}},
	}})

	receipt, err := service.RetryAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.RetryAgentRunCommand{
		IdempotencyKey: "retry-contract", MissionID: "mission-1", RunID: "run-failed",
	})
	if err != nil {
		t.Fatalf("RetryAgentRun: %v", err)
	}
	if receipt.CommandID != "run-retry-contract" || len(runStore.runs) != 1 {
		t.Fatalf("receipt=%+v runs=%+v", receipt, runStore.runs)
	}
	run := runStore.runs[0]
	if run.Task.Goal != "Validate the playable game" || run.Task.WorkerPersonalGoal != "Verify a complete game loop" || len(run.Task.ExpectedDeliverables) != 1 || run.Task.ExpectedDeliverables[0].ID != "expected-1" {
		t.Fatalf("hydrated retry task = %+v", run.Task)
	}
	if run.Request.ContextPack.TaskContractSummary != "Validate the playable game" || len(run.Request.InputMessages) == 0 || !strings.Contains(run.Request.InputMessages[len(run.Request.InputMessages)-1].Content, "expected-1") {
		t.Fatalf("hydrated retry request = %+v", run.Request)
	}
	updated, err := repo.GetMissionControl(ctx, "mission-1")
	if err != nil || updated.TaskBoard.Tasks[0].Goal != "Validate the playable game" || len(updated.TaskBoard.Tasks[0].ExpectedDeliverables) != 1 {
		t.Fatalf("persisted retry task contract = %+v err=%v", updated.TaskBoard.Tasks, err)
	}
}

func TestCommandServiceRetryAgentRunRejectsCanonicalApprovedTask(t *testing.T) {
	ctx := context.Background()
	at := time.Date(2026, 8, 30, 7, 31, 0, 0, time.UTC)
	repo := missioncontrol.NewMemoryProjectionRepository()
	model := appRuntimeReadModel(at)
	model.TaskBoard.Tasks[0].Status = projection.TaskStatusBlocked
	model.RuntimeSessions = []projection.RuntimeSessionView{{
		RunID: "run-stale", AgentID: "agent-1", TaskID: "task-1", RuntimeKind: "codex",
		Status: projection.RuntimeSessionFailed, CompletedAt: at.Add(-time.Minute), UpdatedAt: at.Add(-time.Minute),
	}}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatal(err)
	}
	runStore := &capturingRunStore{}
	service := newAuthorizedCommandService(nil)
	service.SetRuntimeCommandPorts(runStore, repo, &capturingMissionEventSink{}, &sequenceContextIDGenerator{}, fixedClock{at: at})
	service.SetRuntimeTaskProjectionReader(&capturingRuntimeTaskProjectionReader{task: projection.TaskCardView{TaskID: "task-1", Status: projection.TaskStatusApproved, Goal: "Already complete"}})

	_, err := service.RetryAgentRun(ctx, missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.RetryAgentRunCommand{
		IdempotencyKey: "retry-approved-contract", MissionID: "mission-1", RunID: "run-stale",
	})
	if err != missioncontrol.ErrInvalidRequest || len(runStore.runs) != 0 {
		t.Fatalf("canonical approved retry error=%v runs=%+v", err, runStore.runs)
	}
}

type capturingRuntimeTaskProjectionReader struct {
	task projection.TaskCardView
	err  error
}

func (r *capturingRuntimeTaskProjectionReader) ResolveRuntimeTask(_ context.Context, _ string, _ missioncore.MissionID, _ missioncore.TaskID) (projection.TaskCardView, error) {
	return r.task, r.err
}
