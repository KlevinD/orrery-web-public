package responsibility

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestPersistRuntimeOutcomeUsageReplayIsMonotonic(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()

	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	attempt := createRuntimeOutcomeUsageAttempt(t, ctx, env, repository)
	completedAt := env.Clock.Now().Add(time.Minute)

	completed, applied, err := repository.PersistRuntimeOutcome(ctx, RuntimeOutcomeRecord{
		TenantID:    attempt.TenantID,
		TaskID:      "task-runtime-outcome-usage",
		RunID:       attempt.AgentRunID,
		Outcome:     AttemptOutcomeCompleted,
		UsageTokens: 120,
	}, completedAt)
	if err != nil || !applied {
		t.Fatalf("persist completed outcome applied=%v err=%v", applied, err)
	}
	if completed.Outcome != AttemptOutcomeCompleted || completed.UsageTokens != 120 {
		t.Fatalf("completed attempt = %+v", completed)
	}
	if !completed.CompletedAt.Equal(completedAt) || !completed.HeartbeatAt.Equal(completedAt) {
		t.Fatalf("completed timestamps completed_at=%v heartbeat_at=%v, want %v", completed.CompletedAt, completed.HeartbeatAt, completedAt)
	}

	replayedAt := completedAt.Add(5 * time.Minute)
	replayed, applied, err := repository.PersistRuntimeOutcome(ctx, RuntimeOutcomeRecord{
		TenantID:    attempt.TenantID,
		TaskID:      "task-runtime-outcome-usage",
		RunID:       attempt.AgentRunID,
		Outcome:     AttemptOutcomeCompleted,
		UsageTokens: 275,
	}, replayedAt)
	if err != nil || !applied {
		t.Fatalf("replay completed outcome applied=%v err=%v", applied, err)
	}
	if replayed.UsageTokens != 275 {
		t.Fatalf("replayed usage = %d, want 275", replayed.UsageTokens)
	}
	if !replayed.CompletedAt.Equal(completed.CompletedAt) || !replayed.HeartbeatAt.Equal(completed.HeartbeatAt) {
		t.Fatalf("replay rewrote terminal timestamps: first=%+v replayed=%+v", completed, replayed)
	}

	if _, applied, err := repository.PersistRuntimeOutcome(ctx, RuntimeOutcomeRecord{
		TenantID:    attempt.TenantID,
		TaskID:      "task-runtime-outcome-usage",
		RunID:       attempt.AgentRunID,
		Outcome:     AttemptOutcomeCompleted,
		UsageTokens: -1,
	}, replayedAt.Add(time.Minute)); !errors.Is(err, ErrInvalidContract) || applied {
		t.Fatalf("negative usage applied=%v err=%v, want ErrInvalidContract", applied, err)
	}

	if _, applied, err := repository.PersistRuntimeOutcome(ctx, RuntimeOutcomeRecord{
		TenantID:    attempt.TenantID,
		TaskID:      "task-runtime-outcome-usage",
		RunID:       attempt.AgentRunID,
		Outcome:     AttemptOutcomeFailed,
		UsageTokens: 500,
	}, replayedAt.Add(2*time.Minute)); !errors.Is(err, ErrConflict) || applied {
		t.Fatalf("conflicting outcome applied=%v err=%v, want ErrConflict", applied, err)
	}

	stored, err := repository.FindAttemptByRun(ctx, attempt.TenantID, attempt.AgentRunID)
	if err != nil {
		t.Fatalf("find stored attempt: %v", err)
	}
	if stored.Outcome != AttemptOutcomeCompleted || stored.UsageTokens != 275 ||
		!stored.CompletedAt.Equal(completed.CompletedAt) || !stored.HeartbeatAt.Equal(completed.HeartbeatAt) {
		t.Fatalf("invalid or conflicting replay changed attempt: %+v", stored)
	}
}

func createRuntimeOutcomeUsageAttempt(t *testing.T, ctx context.Context, env *testkit.Env, repository *Repository) AttemptRecord {
	t.Helper()

	var activation ActivationRecord
	err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		_, saved, err := repository.AssignMissionTaskTx(ctx, tx, MissionTaskAssignmentInput{
			TenantID: "tenant-1", MissionID: "mission-1", TaskID: "task-runtime-outcome-usage", AgentProfileID: "agent-1",
			Priority: 50, AcceptBy: env.Clock.Now().Add(time.Minute), ActivationCause: ActivationCauseAssigned,
		}, env.Clock.Now())
		activation = saved
		return err
	})
	if err != nil {
		t.Fatalf("assign responsibility: %v", err)
	}

	claimed, ok, err := repository.ClaimActivation(ctx, activation.ID, "worker", "claim-runtime-outcome-usage", env.Clock.Now(), time.Minute, 1)
	if err != nil || !ok {
		t.Fatalf("claim activation ok=%v err=%v", ok, err)
	}
	attempt, err := repository.MarkDispatched(ctx, claimed.ID, claimed.ClaimToken, RunIDForActivation(claimed.ID), "pi", env.Clock.Now())
	if err != nil {
		t.Fatalf("mark dispatched: %v", err)
	}
	return attempt
}
