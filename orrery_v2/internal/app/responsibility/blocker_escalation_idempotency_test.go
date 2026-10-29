package responsibility

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/platform/testkit"
)

func TestEscalateBlockerIsNoOpAfterEscalationChainIsExhausted(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-terminal-blocker", "agent-1")
	assignment, err := repository.GetAssignment(ctx, activation.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := repository.OpenBlocker(ctx, OpenBlockerInput{
		TenantID: assignment.TenantID, ResponsibilityID: assignment.ID,
		Kind: BlockerKindInput, Needed: "owner input", RequestedFrom: "user:owner",
		RecheckAt: env.Clock.Now(), DedupeKey: "terminal-input",
	}, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultPolicy(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind)
	policy.EscalationChain = nil

	env.Clock.Advance(time.Minute)
	first, escalated, err := repository.EscalateBlocker(ctx, blocker.ID, policy, env.Clock.Now())
	if err != nil || !escalated {
		t.Fatalf("first escalation = %+v escalated=%t err=%v", first, escalated, err)
	}
	firstAssignment, err := repository.GetAssignment(ctx, assignment.ID)
	if err != nil {
		t.Fatal(err)
	}
	firstAudits, err := repository.ListAudits(ctx, assignment.TenantID, assignment.ID, 100)
	if err != nil {
		t.Fatal(err)
	}

	env.Clock.Advance(time.Minute)
	second, escalated, err := repository.EscalateBlocker(ctx, blocker.ID, policy, env.Clock.Now())
	if err != nil || escalated {
		t.Fatalf("second escalation = %+v escalated=%t err=%v", second, escalated, err)
	}
	secondAssignment, err := repository.GetAssignment(ctx, assignment.ID)
	if err != nil {
		t.Fatal(err)
	}
	secondAudits, err := repository.ListAudits(ctx, assignment.TenantID, assignment.ID, 100)
	if err != nil {
		t.Fatal(err)
	}
	if second.Version != first.Version || !second.UpdatedAt.Equal(first.UpdatedAt) ||
		secondAssignment.Version != firstAssignment.Version || !secondAssignment.UpdatedAt.Equal(firstAssignment.UpdatedAt) ||
		len(secondAudits) != len(firstAudits) {
		t.Fatalf("terminal escalation mutated blocker=%+v assignment=%+v audits=%d", second, secondAssignment, len(secondAudits))
	}
}
