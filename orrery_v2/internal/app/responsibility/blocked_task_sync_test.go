package responsibility

import (
	"context"
	"testing"
	"time"

	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
)

func TestBlockedTaskRuntimeCompletionSynchronizesResponsibility(t *testing.T) {
	for _, recoverOnly := range []bool{false, true} {
		name := "runtime callback"
		if recoverOnly {
			name = "recovery after completed attempt"
		}
		t.Run(name, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			tasks := missiontask.NewPlatformRepository()
			task := testTaskRecord("task-blocked", "mission-1", "agent-1", missiontask.TaskStatusBlocked)
			task.LastTransitionReason = "permission denied while reading the required ontology"
			saveTaskRecord(t, env, tasks, task)
			repository := NewRepository(env.DBRouter, env.UnitOfWork)
			activation := assignTestResponsibility(t, env, repository, "task-blocked", "agent-1")
			claimed, ok, err := repository.ClaimActivation(ctx, activation.ID, "worker", "claim", env.Clock.Now(), time.Minute, 1)
			if err != nil || !ok {
				t.Fatalf("claim ok=%v err=%v", ok, err)
			}
			attempt, err := repository.MarkDispatched(ctx, claimed.ID, "claim", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
			if err != nil {
				t.Fatal(err)
			}
			service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
			if recoverOnly {
				if _, err := repository.CompleteAttempt(ctx, attempt.ID, AttemptOutcomeCompleted, nil, env.Clock.Now()); err != nil {
					t.Fatal(err)
				}
			}
			for repeat := 0; repeat < 2; repeat++ {
				if recoverOnly {
					assignment, err := repository.GetAssignment(ctx, attempt.ResponsibilityID)
					if err != nil {
						t.Fatal(err)
					}
					if _, err := service.RecoverAssignment(ctx, assignment); err != nil {
						t.Fatal(err)
					}
				} else if err := service.RecordRuntimeOutcome(ctx, RuntimeOutcomeInput{
					TenantID: "tenant-1", TaskID: "task-blocked", RunID: attempt.AgentRunID, Outcome: AttemptOutcomeCompleted,
				}); err != nil {
					t.Fatal(err)
				}
			}
			assignment, err := repository.GetAssignment(ctx, attempt.ResponsibilityID)
			if err != nil || assignment.State != AssignmentStateWaiting {
				t.Fatalf("blocked business result left responsibility state=%s err=%v", assignment.State, err)
			}
			blockers, err := repository.ListBlockers(ctx, "tenant-1", assignment.ID, true)
			if err != nil || len(blockers) != 1 {
				t.Fatalf("blockers=%+v err=%v", blockers, err)
			}
			if blockers[0].Needed != task.LastTransitionReason || blockers[0].Kind != BlockerKindPermission || blockers[0].RecheckAt.IsZero() {
				t.Fatalf("business blocker lost its reason or recovery information: %+v", blockers[0])
			}
			persisted, err := repository.FindAttemptByRun(ctx, "tenant-1", attempt.AgentRunID)
			if err != nil || persisted.Outcome != AttemptOutcomeCompleted {
				t.Fatalf("runtime completion was rewritten: %+v err=%v", persisted, err)
			}
			storedTask, err := service.Task(ctx, "task-blocked")
			if err != nil || storedTask.Status != missiontask.TaskStatusBlocked {
				t.Fatalf("business failure was rewritten: status=%s err=%v", storedTask.Status, err)
			}
		})
	}
}

func TestBlockedTaskSyncDoesNotReopenTerminatedResponsibility(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-terminal", "mission-1", "agent-1", missiontask.TaskStatusBlocked))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-terminal", "agent-1")
	assignment, err := repository.GetAssignment(ctx, activation.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.TerminateAssignment(ctx, AssignmentControl{
		TenantID: "tenant-1", AssignmentID: assignment.ID, ExpectedVersion: assignment.Version,
		ActorID: "owner", Reason: "old run closed", IdempotencyKey: "terminate-old-run",
	}, env.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	if err := service.SynchronizeTask(ctx, "tenant-1", "task-terminal"); err != nil {
		t.Fatal(err)
	}
	blockers, err := repository.ListBlockers(ctx, "tenant-1", assignment.ID, true)
	if err != nil || len(blockers) != 0 {
		t.Fatalf("terminal responsibility received new blockers: %+v err=%v", blockers, err)
	}
}
