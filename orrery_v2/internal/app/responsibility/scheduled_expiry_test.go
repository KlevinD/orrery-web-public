package responsibility

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestScheduledIdleExpiryRejectsConcurrentDispatchAndPreservesSuccess(t *testing.T) {
	for _, outcome := range []AttemptOutcome{"", AttemptOutcomeFailed, AttemptOutcomeCompleted} {
		t.Run("outcome-"+string(outcome), func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			repository := NewRepository(env.DBRouter, env.UnitOfWork)
			activation := assignScheduledExpiryTestResponsibility(t, env, repository, "idle-scheduled")
			claimed, ok, err := repository.ClaimActivation(ctx, activation.ID, "worker", "expiry-race", env.Clock.Now(), time.Minute, 1)
			if err != nil || !ok {
				t.Fatalf("claim ok=%v err=%v", ok, err)
			}
			attempt, err := repository.MarkDispatched(ctx, claimed.ID, claimed.ClaimToken, RunIDForActivation(claimed.ID), "pi", env.Clock.Now())
			if err != nil {
				t.Fatal(err)
			}
			if outcome != "" {
				if _, err := repository.CompleteAttempt(ctx, attempt.ID, outcome, nil, env.Clock.Now()); err != nil {
					t.Fatal(err)
				}
			}
			assignment, err := repository.GetAssignment(ctx, activation.ResponsibilityID)
			if err != nil {
				t.Fatal(err)
			}
			_, err = repository.ExpireIdleScheduledAssignment(ctx, AssignmentControl{
				TenantID: assignment.TenantID, AssignmentID: assignment.ID, ActorID: "system:scheduler-reconciler",
				IdempotencyKey: "idle-expiry", ExpectedVersion: assignment.Version, Reason: "occurrence expired",
			}, env.Clock.Now())
			stored, readErr := repository.GetAssignment(ctx, assignment.ID)
			if readErr != nil {
				t.Fatal(readErr)
			}
			if outcome == AttemptOutcomeFailed {
				if err != nil || stored.State != AssignmentStateTerminated {
					t.Fatalf("failed idle attempt should expire: assignment=%+v err=%v", stored, err)
				}
				if _, ok, err := repository.ClaimActivation(ctx, activation.ID, "worker", "late-claim", env.Clock.Now(), time.Minute, 1); err != nil || ok {
					t.Fatalf("expired activation must not dispatch: ok=%v err=%v", ok, err)
				}
			} else if !errors.Is(err, ErrConflict) || stored.State == AssignmentStateTerminated {
				t.Fatalf("live or completed attempt must be preserved: assignment=%+v err=%v", stored, err)
			}
		})
	}
}

func TestScheduledIdleExpiryClosesUnacceptedAssignmentAndAudits(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignScheduledExpiryTestResponsibility(t, env, repository, "unaccepted-occurrence")
	assignment, err := repository.GetAssignment(ctx, activation.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := repository.ExpireIdleScheduledAssignment(ctx, AssignmentControl{
		TenantID: assignment.TenantID, AssignmentID: assignment.ID, ActorID: "system:scheduler-reconciler",
		IdempotencyKey: "unaccepted-idle-expiry", ExpectedVersion: assignment.Version, Reason: "occurrence expired",
	}, env.Clock.Now())
	if err != nil || expired.State != AssignmentStateTerminated || expired.TerminationNote != "occurrence expired" {
		t.Fatalf("expired assignment=%+v err=%v", expired, err)
	}
	if got := getActivationForTest(t, env, activation.ID); got.Status != ActivationStatusSuperseded {
		t.Fatalf("expired activation=%+v", got)
	}
	audits, err := repository.ListAudits(ctx, assignment.TenantID, assignment.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, audit := range audits {
		if audit.Action == AuditResponsibilityTerminated && audit.ActorID == "system:scheduler-reconciler" && audit.Details == "occurrence expired" {
			found = true
		}
	}
	if !found {
		t.Fatal("idle expiry did not preserve a termination audit")
	}
}

func assignScheduledExpiryTestResponsibility(t *testing.T, env *testkit.Env, repository *Repository, taskID string) ActivationRecord {
	t.Helper()
	var activation ActivationRecord
	if err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
		_, created, err := repository.AssignMissionTaskTx(ctx, tx, MissionTaskAssignmentInput{
			TenantID: "tenant-1", MissionID: "mission-1", TaskID: taskID, AgentProfileID: "agent-1",
			SourceKind: "scheduler_agent_task", SourceID: "schedule-1", SourceStepID: "run-1",
			Priority: 50, AcceptBy: env.Clock.Now().Add(time.Minute), ActivationCause: ActivationCauseAssigned,
		}, env.Clock.Now())
		activation = created
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return activation
}
