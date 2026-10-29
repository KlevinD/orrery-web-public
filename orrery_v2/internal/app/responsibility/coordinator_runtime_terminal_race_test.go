package responsibility

import (
	"context"
	"testing"
	"time"

	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
)

func TestCoordinatorPreservesCompletedOutcomeWhenRuntimeCancellationFindsTerminal(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()

	ctx := context.Background()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-terminal-cancel-race", "mission-1", "agent-1", missiontask.TaskStatusInProgress))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-terminal-cancel-race", "agent-1")
	claimed, ok, err := repository.ClaimActivation(ctx, activation.ID, "worker", "claim-terminal-cancel-race", env.Clock.Now(), time.Minute, 2)
	if err != nil || !ok {
		t.Fatalf("claim activation ok=%v err=%v", ok, err)
	}
	attempt, err := repository.MarkDispatched(ctx, claimed.ID, claimed.ClaimToken, RunIDForActivation(claimed.ID), "pi", env.Clock.Now())
	if err != nil {
		t.Fatalf("mark dispatched: %v", err)
	}

	env.Clock.Advance(2 * time.Minute)
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	controller := &completingTerminalRuntimeController{service: service, usageTokens: 73}
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository:     repository,
		Service:        service,
		RuntimeHealth:  staticRuntimeHealthSource{},
		RuntimeControl: controller,
		Clock:          env.Clock,
	})
	assignment, err := repository.GetAssignment(ctx, activation.ResponsibilityID)
	if err != nil {
		t.Fatalf("get assignment: %v", err)
	}
	policy := DefaultPolicy(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind)
	policy.HeartbeatTimeout = 90 * time.Second

	stats, err := coordinator.reconcileRuntimeHealth(ctx, assignment, policy)
	if err != nil {
		t.Fatalf("reconcile runtime health: %v", err)
	}
	if controller.calls != 1 {
		t.Fatalf("runtime cancellation calls = %d, want 1", controller.calls)
	}
	if stats.TimedOut != 0 || stats.BudgetBlocked != 0 {
		t.Fatalf("terminal cancellation stats = %+v, want no timeout or budget block", stats)
	}
	if controller.request.RunID != attempt.AgentRunID || controller.request.TaskID != assignment.WorkID {
		t.Fatalf("runtime cancellation request = %+v", controller.request)
	}

	stored, err := repository.FindAttemptByRun(ctx, assignment.TenantID, attempt.AgentRunID)
	if err != nil {
		t.Fatalf("find attempt: %v", err)
	}
	if stored.Outcome != AttemptOutcomeCompleted || stored.UsageTokens != controller.usageTokens || stored.CompletedAt.IsZero() {
		t.Fatalf("terminal attempt = %+v, want completed with usage %d", stored, controller.usageTokens)
	}
}

type completingTerminalRuntimeController struct {
	service     *Service
	usageTokens int64
	calls       int
	request     RuntimeCancelRequest
}

func (c *completingTerminalRuntimeController) CancelResponsibility(ctx context.Context, request RuntimeCancelRequest) error {
	c.calls++
	c.request = request
	if err := c.service.RecordRuntimeOutcome(ctx, RuntimeOutcomeInput{
		TenantID:    request.TenantID,
		TaskID:      request.TaskID,
		RunID:       request.RunID,
		Outcome:     AttemptOutcomeCompleted,
		UsageTokens: c.usageTokens,
	}); err != nil {
		return err
	}
	return ErrRuntimeTerminal
}
