package main

import (
	"context"
	"errors"
	"testing"
	"time"

	schedulerapp "orrery_v2/internal/app/scheduler"
)

type scheduledApprovalAssignmentRepositoryFake struct {
	task      schedulerapp.ScheduledTask
	saved     schedulerapp.ScheduledTask
	saveCalls int
}

func (f *scheduledApprovalAssignmentRepositoryFake) GetTask(context.Context, string) (schedulerapp.ScheduledTask, error) {
	return f.task, nil
}

func (f *scheduledApprovalAssignmentRepositoryFake) SaveTask(_ context.Context, task schedulerapp.ScheduledTask) error {
	f.saved = task
	f.saveCalls++
	return nil
}

type scheduledApprovalAssignmentLifecycleFake struct {
	task      schedulerapp.ScheduledTask
	missionID string
	err       error
}

func (f *scheduledApprovalAssignmentLifecycleFake) EnsureScheduledMission(_ context.Context, task schedulerapp.ScheduledTask) (string, error) {
	f.task = task
	return f.missionID, f.err
}

func TestScheduledApprovalAssignerAssignsCanonicalHumanWithoutEnablingTask(t *testing.T) {
	now := time.Date(2026, 8, 8, 9, 30, 0, 0, time.UTC)
	repo := &scheduledApprovalAssignmentRepositoryFake{task: schedulerapp.ScheduledTask{
		ScheduledTaskID: "schedule-wechat",
		Status:          schedulerapp.TaskStatusDisabled,
		Enabled:         false,
		Target: schedulerapp.Target{
			Mode:     schedulerapp.TargetModeAgentTask,
			AgentID:  "jordan-belfort",
			TaskGoal: "Create the WeChat draft",
		},
	}}
	lifecycle := &scheduledApprovalAssignmentLifecycleFake{missionID: "mission-wechat"}
	assigner := scheduledApprovalAssigner{repository: repo, lifecycle: lifecycle, now: func() time.Time { return now }}

	report, err := assigner.Assign(context.Background(), "schedule-wechat", "ding")
	if err != nil {
		t.Fatal(err)
	}
	if report.ApprovalAssigneeID != "human-ding" || report.MissionID != "mission-wechat" {
		t.Fatalf("report = %+v", report)
	}
	if lifecycle.task.Target.ApprovalAssigneeID != "human-ding" {
		t.Fatalf("synced target = %+v", lifecycle.task.Target)
	}
	if repo.saveCalls != 1 || repo.saved.Target.ApprovalAssigneeID != "human-ding" {
		t.Fatalf("saved task = %+v calls=%d", repo.saved, repo.saveCalls)
	}
	if repo.saved.MissionID != "mission-wechat" {
		t.Fatalf("saved mission_id = %q, want mission-wechat", repo.saved.MissionID)
	}
	if repo.saved.Enabled || repo.saved.Status != schedulerapp.TaskStatusDisabled {
		t.Fatalf("assignment must not enable task: %+v", repo.saved)
	}
	if !repo.saved.UpdatedAt.Equal(now) {
		t.Fatalf("updated_at = %s, want %s", repo.saved.UpdatedAt, now)
	}
}

func TestScheduledApprovalAssignerRejectsNonAgentTaskBeforeSaving(t *testing.T) {
	repo := &scheduledApprovalAssignmentRepositoryFake{task: schedulerapp.ScheduledTask{
		ScheduledTaskID: "schedule-maintenance",
		Target:          schedulerapp.Target{Mode: schedulerapp.TargetModeMemoryLifecycleSweep},
	}}
	lifecycle := &scheduledApprovalAssignmentLifecycleFake{}
	assigner := scheduledApprovalAssigner{repository: repo, lifecycle: lifecycle}

	_, err := assigner.Assign(context.Background(), "schedule-maintenance", "ding")
	if !errors.Is(err, schedulerapp.ErrInvalidTarget) {
		t.Fatalf("error = %v, want invalid target", err)
	}
	if repo.saveCalls != 0 || lifecycle.task.ScheduledTaskID != "" {
		t.Fatalf("invalid task was mutated: saved=%d synced=%+v", repo.saveCalls, lifecycle.task)
	}
}
