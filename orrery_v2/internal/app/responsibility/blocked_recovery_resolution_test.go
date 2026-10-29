package responsibility

import (
	"context"
	"errors"
	"testing"
	"time"

	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
)

func TestResolvedRuntimeBlockerSurvivesRecoveryAndAllowsAnotherAttempt(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-recovered", "mission-1", "agent-1", missiontask.TaskStatusInProgress))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-recovered", "agent-1")
	claimAttempt := func(a ActivationRecord, claim string) AttemptRecord {
		t.Helper()
		claimed, ok, err := repository.ClaimActivation(ctx, a.ID, "worker", claim, env.Clock.Now(), time.Minute, 1)
		if err != nil || !ok {
			t.Fatalf("claim ok=%v err=%v", ok, err)
		}
		attempt, err := repository.MarkDispatched(ctx, claimed.ID, claim, RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
		if err != nil {
			t.Fatal(err)
		}
		return attempt
	}
	first := claimAttempt(activation, "first")
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	if err := service.RecordRuntimeOutcome(ctx, RuntimeOutcomeInput{TenantID: "tenant-1", TaskID: "task-recovered", RunID: first.AgentRunID, Outcome: AttemptOutcomeBlocked, Error: errors.New("missing input")}); err != nil {
		t.Fatal(err)
	}
	blockers, err := repository.ListBlockers(ctx, "tenant-1", first.ResponsibilityID, true)
	if err != nil || len(blockers) != 1 {
		t.Fatalf("blockers=%+v err=%v", blockers, err)
	}
	resolved, next, err := repository.ResolveBlocker(ctx, "tenant-1", blockers[0].ID, "owner", "input supplied", "resolve-first", blockers[0].Version, ActivationCauseBlockerResolved, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	for repeat := 0; repeat < 2; repeat++ {
		assignment, err := repository.GetAssignment(ctx, first.ResponsibilityID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := service.RecoverAssignment(ctx, assignment); err != nil {
			t.Fatal(err)
		}
		assignment, err = repository.GetAssignment(ctx, first.ResponsibilityID)
		if err != nil || assignment.Version != next.AssignmentVersion {
			t.Fatalf("recovery invalidated retry: assignment=%+v err=%v", assignment, err)
		}
		open, err := repository.ListBlockers(ctx, "tenant-1", first.ResponsibilityID, true)
		if err != nil || len(open) != 0 {
			t.Fatalf("resolved attempt blocker reopened: %+v err=%v", open, err)
		}
	}
	second := claimAttempt(next, "second")
	// Even if an old resolution were missing, a current live attempt must not
	// replay earlier failures. Then a genuinely new failure still blocks.
	assignment, err := repository.GetAssignment(ctx, first.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.RecoverAssignment(ctx, assignment); err != nil {
		t.Fatal(err)
	}
	if err := service.RecordRuntimeOutcome(ctx, RuntimeOutcomeInput{TenantID: "tenant-1", TaskID: "task-recovered", RunID: second.AgentRunID, Outcome: AttemptOutcomeBlocked, Error: errors.New("different missing input")}); err != nil {
		t.Fatal(err)
	}
	open, err := repository.ListBlockers(ctx, "tenant-1", first.ResponsibilityID, true)
	if err != nil || len(open) != 1 || open[0].DedupeKey != "runtime-blocked-"+second.ID {
		t.Fatalf("new attempt failure must remain blocked: %+v err=%v", open, err)
	}
	all, err := repository.ListBlockers(ctx, "tenant-1", first.ResponsibilityID, false)
	if err != nil || len(all) != 2 {
		t.Fatalf("audit trail lost: %+v err=%v", all, err)
	}
	for _, b := range all {
		if b.ID == resolved.ID && (b.Status != BlockerStatusResolved || b.Version != resolved.Version) {
			t.Fatalf("previous resolution changed: %+v", b)
		}
	}
}
