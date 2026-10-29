package main

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/testkit"
)

type schedulerIdentityBeforeTaskPort struct {
	schedulerMissionCommandPort
	repo  *schedulerapp.Repository
	runID string
	t     *testing.T
	calls int
}

func (p *schedulerIdentityBeforeTaskPort) CreateTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error) {
	p.calls++
	run, err := p.repo.GetRun(ctx, p.runID)
	if err != nil || run.MissionID != string(cmd.MissionID) || run.TaskID != string(cmd.TaskID) || run.ResponsibilityID == "" {
		p.t.Fatalf("Task can activate before scheduler identity is durable: run=%+v error=%v", run, err)
	}
	return p.schedulerMissionCommandPort.CreateTask(ctx, viewer, cmd)
}

func TestBindScheduledAgentTaskIdentityRejectsExistingDifferentMission(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := schedulerapp.NewRepository(env.DBRouter)
	run := schedulerapp.ScheduledRun{ScheduledRunID: "run-bound", ScheduledTaskID: "task-bound", TenantID: "tenant-a", MissionID: "existing-mission", TargetMode: schedulerapp.TargetModeAgentTask, Status: schedulerapp.RunStatusRunning}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	p := productionSchedulerAgentTaskPort{runs: repo}
	err := p.bindScheduledAgentTaskIdentity(ctx, schedulerapp.AgentTaskRequest{RunID: run.ScheduledRunID, ScheduledTaskID: run.ScheduledTaskID, TenantID: run.TenantID, AgentID: "agent-a"}, "different-mission")
	if !errors.Is(err, responsibility.ErrConflict) {
		t.Fatalf("different Mission accepted: %v", err)
	}
	stored, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil || stored.MissionID != run.MissionID || stored.TaskID != "" {
		t.Fatalf("conflicting identity was overwritten: %+v error=%v", stored, err)
	}
}
