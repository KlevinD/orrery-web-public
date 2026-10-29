package responsibility

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/testkit"
)

func TestRetryOfferedAssignmentRenewsExplicitAcceptDeadline(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "retry-offered", "agent-1")
	assignment, err := repository.GetAssignment(ctx, activation.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	now := assignment.AcceptBy.Add(time.Minute)
	blocker, err := repository.OpenBlocker(ctx, OpenBlockerInput{
		TenantID: assignment.TenantID, ResponsibilityID: assignment.ID,
		Kind: BlockerKindCapacity, DedupeKey: "accept-timeout", Needed: "accept deadline passed",
		RequestedFrom: "agent:agent-1", RecheckAt: now.Add(time.Minute),
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	command := AssignmentControl{
		TenantID: assignment.TenantID, AssignmentID: assignment.ID,
		ActorID: "manager-1", Reason: "dispatch context repaired", IdempotencyKey: "retry-offered-context-fix",
		ExpectedVersion: assignment.Version, AcceptBy: now.Add(10 * time.Minute),
	}
	retried, next, err := repository.RetryAssignment(ctx, command, now)
	if err != nil || !retried.AcceptBy.Equal(command.AcceptBy) || next.Status != ActivationStatusPending {
		t.Fatalf("retry did not renew the offered assignment: assignment=%+v activation=%+v err=%v", retried, next, err)
	}
	open, err := repository.ListBlockers(ctx, assignment.TenantID, assignment.ID, true)
	if err != nil || len(open) != 1 {
		t.Fatalf("retry must retain the existing blocker until explicitly resolved: blockers=%+v err=%v", open, err)
	}
	if _, _, err := repository.ResolveBlocker(ctx, assignment.TenantID, blocker.ID, "manager-1", "dispatch context repaired", "resolve-retry-deadline", blocker.Version, ActivationCauseBlockerResolved, now); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := repository.ClaimActivation(ctx, next.ID, "worker", "retry-deadline-claim", now, time.Minute, 1); err != nil || !ok {
		t.Fatalf("renewed activation could not be claimed: ok=%v err=%v", ok, err)
	}
	replay, repeated, err := repository.RetryAssignment(ctx, command, now.Add(time.Minute))
	if err != nil || replay.Version != retried.Version || repeated.ID != "" || !replay.AcceptBy.Equal(command.AcceptBy) {
		t.Fatalf("retry replay changed the deadline or created work: assignment=%+v activation=%+v err=%v", replay, repeated, err)
	}
}

func TestRetryOfferedAssignmentRejectsExpiredAcceptDeadline(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "retry-expired", "agent-1")
	assignment, err := repository.GetAssignment(ctx, activation.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = repository.RetryAssignment(ctx, AssignmentControl{
		TenantID: assignment.TenantID, AssignmentID: assignment.ID, ActorID: "manager-1",
		IdempotencyKey: "retry-expired-deadline", ExpectedVersion: assignment.Version, AcceptBy: env.Clock.Now(),
	}, env.Clock.Now())
	if !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("expired acceptance deadline: got %v", err)
	}
	stored, err := repository.GetAssignment(ctx, assignment.ID)
	if err != nil || stored.Version != assignment.Version || !stored.AcceptBy.Equal(assignment.AcceptBy) {
		t.Fatalf("invalid retry changed the assignment: %+v err=%v", stored, err)
	}
}
