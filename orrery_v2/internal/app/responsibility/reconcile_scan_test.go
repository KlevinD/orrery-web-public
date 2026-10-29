package responsibility

import (
	"context"
	"fmt"
	"testing"
	"time"

	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestReconcileScanProgressSurvivesRestartAndWaitingReviews(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		for i := 0; i < 125; i++ {
			if err := putAssignmentTx(ctx, tx, AssignmentRecord{ResponsibilityAssignment: ResponsibilityAssignment{
				ID: fmt.Sprintf("review-%03d", i), TenantID: "tenant-1", State: AssignmentStateSubmitted, Priority: 75,
			}}); err != nil {
				return err
			}
			if err := putAssignmentTx(ctx, tx, AssignmentRecord{ResponsibilityAssignment: ResponsibilityAssignment{
				ID: fmt.Sprintf("escalated-%03d", i), TenantID: "tenant-1", State: AssignmentStateEscalated, Priority: 100,
			}}); err != nil {
				return err
			}
		}
		return putAssignmentTx(ctx, tx, AssignmentRecord{ResponsibilityAssignment: ResponsibilityAssignment{
			ID: "z-new-assignment", TenantID: "tenant-1", State: AssignmentStateOffered, Priority: 50,
		}})
	}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for i := 0; i < 13; i++ {
		// Recreating the repository models a process restart between batches.
		repository = NewRepository(env.DBRouter, env.UnitOfWork)
		batch, err := repository.ListAssignmentsForReconcile(ctx, 10)
		if err != nil || len(batch) != 10 {
			t.Fatalf("batch %d size=%d err=%v", i, len(batch), err)
		}
		for _, assignment := range batch {
			if assignment.State == AssignmentStateEscalated {
				t.Fatal("an escalated assignment occupied a business supervision slot")
			}
			seen[assignment.ID] = true
		}
	}
	if len(seen) != 126 || !seen["z-new-assignment"] {
		t.Fatalf("scan did not reach all assignments: seen=%d new=%v", len(seen), seen["z-new-assignment"])
	}
}

func TestRuntimeRecoveryReleasesCapacityDespiteBusinessReviewBacklog(t *testing.T) {
	for _, outcome := range []AttemptOutcome{AttemptOutcomeFailed, AttemptOutcomeCancelled} {
		t.Run(string(outcome), func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			tasks := missiontask.NewPlatformRepository()
			saveTaskRecord(t, env, tasks, testTaskRecord("stale-runtime", "mission-1", "agent-1", missiontask.TaskStatusAssigned))
			repository := NewRepository(env.DBRouter, env.UnitOfWork)
			activation := assignTestResponsibility(t, env, repository, "stale-runtime", "agent-1")
			claimed, ok, err := repository.ClaimActivation(ctx, activation.ID, "worker", "stale-claim", env.Clock.Now(), time.Minute, 1)
			if err != nil || !ok {
				t.Fatalf("initial claim ok=%v err=%v", ok, err)
			}
			attempt, err := repository.MarkDispatched(ctx, claimed.ID, claimed.ClaimToken, RunIDForActivation(claimed.ID), "pi", env.Clock.Now())
			if err != nil {
				t.Fatal(err)
			}
			if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
				for i := 0; i < 120; i++ {
					if err := putAssignmentTx(ctx, tx, AssignmentRecord{ResponsibilityAssignment: ResponsibilityAssignment{
						ID: fmt.Sprintf("waiting-review-%03d", i), TenantID: "tenant-1", State: AssignmentStateSubmitted, Priority: 75,
					}}); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			pending := assignTestResponsibility(t, env, repository, "new-runtime", "agent-1")
			if _, ok, err := repository.ClaimActivation(ctx, pending.ID, "worker", "pending-claim", env.Clock.Now(), time.Minute, 1); err != nil || ok {
				t.Fatalf("stale attempt must occupy capacity: ok=%v err=%v", ok, err)
			}
			service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock, RetryDelays: []time.Duration{time.Hour}})
			source := &staticRuntimeOutcomeSource{fact: RuntimeOutcomeFact{
				TenantID: "tenant-1", TaskID: "stale-runtime", RunID: attempt.AgentRunID, Outcome: outcome, Error: "durable queue terminal fact",
			}}
			coordinator := NewCoordinator(CoordinatorOptions{Repository: repository, Service: service, RuntimeOutcomes: source, Clock: env.Clock})
			stats, err := coordinator.reconcileRuntimeAttempts(ctx, 1)
			if err != nil || stats.RecoveredOutcomes != 1 {
				t.Fatalf("runtime recovery stats=%+v err=%v", stats, err)
			}
			stored, err := repository.FindAttemptByRun(ctx, "tenant-1", attempt.AgentRunID)
			if err != nil || stored.Outcome != outcome || stored.Error != source.fact.Error {
				t.Fatalf("terminal attempt=%+v err=%v", stored, err)
			}
			if _, ok, err := repository.ClaimActivation(ctx, pending.ID, "worker", "pending-claim", env.Clock.Now(), time.Minute, 1); err != nil || !ok {
				t.Fatalf("terminal recovery must release capacity: ok=%v err=%v", ok, err)
			}
		})
	}
}

func TestRuntimeScanMakesProgressPastUnchangedLiveAttempts(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		for i := 0; i < 5; i++ {
			id := fmt.Sprintf("live-%d", i)
			if err := putAssignmentTx(ctx, tx, AssignmentRecord{ResponsibilityAssignment: ResponsibilityAssignment{ID: id, TenantID: "tenant-1", State: AssignmentStateActive}}); err != nil {
				return err
			}
			if err := putAttemptTx(ctx, tx, AttemptRecord{ResponsibilityAttempt: ResponsibilityAttempt{ID: "attempt-" + id, TenantID: "tenant-1", ResponsibilityID: id}}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for i := 0; i < 3; i++ {
		batch, err := NewRepository(env.DBRouter, env.UnitOfWork).listRuntimeSupervisionAssignments(ctx, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, assignment := range batch {
			seen[assignment.ID] = true
		}
	}
	if len(seen) != 5 {
		t.Fatalf("live runtime scan only reached %d assignments", len(seen))
	}
}
