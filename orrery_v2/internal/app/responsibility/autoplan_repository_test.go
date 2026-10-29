package responsibility

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/platform/testkit"
)

func TestAutoplanRepositoryClaimIsIdempotentAndListsByStatus(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	now := env.Clock.Now()
	candidate, err := NewAutoplanRecord(testAutoplanRequest(), now)
	if err != nil {
		t.Fatalf("NewAutoplanRecord: %v", err)
	}
	first, claimed, err := repository.ClaimAutoplan(context.Background(), candidate, 100, now)
	if err != nil || !claimed {
		t.Fatalf("first ClaimAutoplan record=%+v claimed=%v err=%v", first, claimed, err)
	}
	retry := candidate
	retry.ScheduledRunID = "run-retry"
	second, claimed, err := repository.ClaimAutoplan(context.Background(), retry, 100, now.Add(time.Minute))
	if err != nil || claimed || second.ID != first.ID || second.Version != first.Version {
		t.Fatalf("replayed ClaimAutoplan record=%+v claimed=%v err=%v", second, claimed, err)
	}
	stored, err := repository.GetAutoplan(context.Background(), first.ID)
	if err != nil || stored.ID != first.ID || stored.Status != AutoplanStatusPlanning {
		t.Fatalf("GetAutoplan record=%+v err=%v", stored, err)
	}
	planning, err := repository.ListAutoplans(context.Background(), "tenant-1", []AutoplanStatus{AutoplanStatusPlanning}, 10)
	if err != nil || len(planning) != 1 || planning[0].ID != first.ID {
		t.Fatalf("planning list=%+v err=%v", planning, err)
	}
	otherRequest := testAutoplanRequest()
	otherRequest.TenantID = "tenant-2"
	otherRequest.PortfolioBindingID = "binding-2"
	otherRequest.PortfolioVersionID = "binding-2:v1"
	otherRequest.TriggerRef = "binding-2:v1"
	other, err := NewAutoplanRecord(otherRequest, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("NewAutoplanRecord other tenant: %v", err)
	}
	if _, claimed, err := repository.ClaimAutoplan(context.Background(), other, 100, now.Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("other tenant ClaimAutoplan claimed=%v err=%v", claimed, err)
	}
	planning, err = repository.ListAutoplans(context.Background(), "tenant-1", []AutoplanStatus{AutoplanStatusPlanning}, 10)
	if err != nil || len(planning) != 1 || planning[0].TenantID != "tenant-1" {
		t.Fatalf("tenant-isolated planning list=%+v err=%v", planning, err)
	}
	global, err := repository.ListAutoplansForReconcile(context.Background(), []AutoplanStatus{AutoplanStatusPlanning}, 500)
	if err != nil || len(global) != 2 || global[0].TenantID != "tenant-2" || global[1].TenantID != "tenant-1" {
		t.Fatalf("global planning list=%+v err=%v", global, err)
	}
	if _, err := repository.ListAutoplans(context.Background(), "tenant-1", []AutoplanStatus{"unknown"}, 10); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("invalid status error=%v, want invalid contract", err)
	}
}

func TestAutoplanRepositoryCompareAndSaveUsesVersionCAS(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	now := env.Clock.Now()
	claimed := claimTestAutoplan(t, repository, now)
	proposal := testAutoplanProposal()
	digest, err := AutoplanProposalDigest(proposal)
	if err != nil {
		t.Fatalf("AutoplanProposalDigest: %v", err)
	}
	proposed := claimed
	proposed.Status = AutoplanStatusProposed
	proposed.Proposal = proposal
	proposed.ProposalDigest = digest
	updated, current, err := repository.CompareAndSaveAutoplan(context.Background(), proposed, claimed.Version, now.Add(time.Minute))
	if err != nil || !updated || current.Status != AutoplanStatusProposed || current.Version != claimed.Version+1 {
		t.Fatalf("propose updated=%v current=%+v err=%v", updated, current, err)
	}
	stale := proposed
	stale.Status = AutoplanStatusPendingApproval
	stale.ApprovalID = "approval-stale"
	updated, latest, err := repository.CompareAndSaveAutoplan(context.Background(), stale, claimed.Version, now.Add(2*time.Minute))
	if err != nil || updated || latest.Version != current.Version || latest.Status != AutoplanStatusProposed {
		t.Fatalf("stale CAS updated=%v latest=%+v err=%v", updated, latest, err)
	}
	pending := current
	pending.Status = AutoplanStatusPendingApproval
	pending.ApprovalID = "approval-1"
	pending.ApprovalAttempt = 1
	pending.ApprovalRequestedAt = now.Add(3 * time.Minute)
	updated, current, err = repository.CompareAndSaveAutoplan(context.Background(), pending, current.Version, now.Add(3*time.Minute))
	if err != nil || !updated || current.Status != AutoplanStatusPendingApproval || current.Version != claimed.Version+2 {
		t.Fatalf("pending approval updated=%v current=%+v err=%v", updated, current, err)
	}
	changedDigest := current
	changedDigest.ProposalDigest = "sha256:other"
	if _, _, err := repository.CompareAndSaveAutoplan(context.Background(), changedDigest, current.Version, now.Add(4*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed proposal digest error=%v, want conflict", err)
	}
}

func TestAutoplanRepositoryLinksHILDecisionIdempotently(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	now := env.Clock.Now()
	pending := pendingTestAutoplan(t, repository, now)
	linked, err := repository.LinkAutoplanHILDecision(context.Background(), pending.ID, "hil-1", pending.Version, now.Add(time.Minute))
	if err != nil || linked.HILDecisionID != "hil-1" || linked.Version != pending.Version+1 {
		t.Fatalf("LinkAutoplanHILDecision linked=%+v err=%v", linked, err)
	}
	// Delivery can be retried with the original version after the first link.
	replayed, err := repository.LinkAutoplanHILDecision(context.Background(), pending.ID, "hil-1", pending.Version, now.Add(2*time.Minute))
	if err != nil || replayed.Version != linked.Version || replayed.HILDecisionID != linked.HILDecisionID {
		t.Fatalf("replayed HIL link=%+v err=%v", replayed, err)
	}
	if _, err := repository.LinkAutoplanHILDecision(context.Background(), pending.ID, "hil-other", linked.Version, now.Add(3*time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("different HIL link error=%v, want conflict", err)
	}
}

func TestAutoplanRepositoryClaimsCapacityAtomicallyPerTenantAndAgent(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	now := env.Clock.Now()
	first := claimTestAutoplan(t, repository, now)
	if _, claimed, err := repository.ClaimAutoplan(context.Background(), first, 1, now.Add(time.Minute)); err != nil || claimed {
		t.Fatalf("replay existing plan claimed=%v err=%v", claimed, err)
	}
	secondRequest := testAutoplanRequest()
	secondRequest.TriggerRef = "binding-1:v2"
	second, err := NewAutoplanRecord(secondRequest, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("NewAutoplanRecord second: %v", err)
	}
	if _, claimed, err := repository.ClaimAutoplan(context.Background(), second, 1, now.Add(time.Minute)); claimed || !errors.Is(err, ErrAutoplanCapacity) {
		t.Fatalf("capacity claim claimed=%v err=%v", claimed, err)
	}
}

func TestAutoplanRepositoryRetryRespectsOpenCapacity(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	now := env.Clock.Now()
	failed := claimTestAutoplan(t, repository, now)
	failed.Status, failed.FailureReason = AutoplanStatusFailed, "temporary planner outage"
	updated, failed, err := repository.CompareAndSaveAutoplan(context.Background(), failed, failed.Version, now.Add(time.Minute))
	if err != nil || !updated {
		t.Fatalf("persist failed plan updated=%v plan=%+v err=%v", updated, failed, err)
	}
	openRequest := testAutoplanRequest()
	openRequest.TriggerRef = "binding-1:other-responsibility"
	open, err := NewAutoplanRecord(openRequest, now.Add(2*time.Minute))
	if err != nil {
		t.Fatalf("NewAutoplanRecord open: %v", err)
	}
	if _, claimed, err := repository.ClaimAutoplan(context.Background(), open, 1, now.Add(2*time.Minute)); err != nil || !claimed {
		t.Fatalf("claim open plan claimed=%v err=%v", claimed, err)
	}
	if _, reopened, err := repository.RetryAutoplanPlanning(context.Background(), failed, 1, now.Add(3*time.Minute)); reopened || !errors.Is(err, ErrAutoplanCapacity) {
		t.Fatalf("retry with exhausted capacity reopened=%v err=%v", reopened, err)
	}
	stored, err := repository.GetAutoplan(context.Background(), failed.ID)
	if err != nil || stored.Status != AutoplanStatusFailed {
		t.Fatalf("failed plan after capacity rejection=%+v err=%v", stored, err)
	}
}

func TestAutoplanRepositoryPlanningLeaseExcludesConcurrentOwnerAndExpires(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	now := env.Clock.Now()
	plan := claimTestAutoplan(t, repository, now)

	first, acquired, err := repository.AcquireAutoplanPlanningLease(context.Background(), plan, "planner-one", time.Minute, now)
	if err != nil || !acquired || first.PlanningLeaseOwner != "planner-one" || first.PlanningAttempt != 1 {
		t.Fatalf("first planning lease plan=%+v acquired=%v err=%v", first, acquired, err)
	}
	busy, acquired, err := repository.AcquireAutoplanPlanningLease(context.Background(), first, "planner-two", time.Minute, now.Add(10*time.Second))
	if err != nil || acquired || busy.Version != first.Version || busy.PlanningLeaseOwner != "planner-one" {
		t.Fatalf("held planning lease plan=%+v acquired=%v err=%v", busy, acquired, err)
	}
	second, acquired, err := repository.AcquireAutoplanPlanningLease(context.Background(), first, "planner-two", time.Minute, now.Add(time.Minute+time.Second))
	if err != nil || !acquired || second.PlanningLeaseOwner != "planner-two" || second.Version != first.Version+1 || second.PlanningAttempt != 2 {
		t.Fatalf("expired planning lease plan=%+v acquired=%v err=%v", second, acquired, err)
	}
	proposal := testAutoplanProposal()
	digest, err := AutoplanProposalDigest(proposal)
	if err != nil {
		t.Fatalf("proposal digest: %v", err)
	}
	stale := first
	stale.Status, stale.Proposal, stale.ProposalDigest = AutoplanStatusProposed, proposal, digest
	stale.PlanningLeaseOwner, stale.PlanningLeaseExpiresAt = "", time.Time{}
	updated, current, err := repository.CompareAndSaveAutoplan(context.Background(), stale, first.Version, now.Add(time.Minute+2*time.Second))
	if err != nil || updated || current.Version != second.Version || current.PlanningLeaseOwner != "planner-two" {
		t.Fatalf("stale planning owner CAS updated=%v current=%+v err=%v", updated, current, err)
	}
}

func TestAutoplanRepositoryFindsLatestCycleForExactSnapshot(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	now := env.Clock.Now()
	request := testAutoplanRequest()
	first, err := NewAutoplanRecord(request, now)
	if err != nil {
		t.Fatalf("NewAutoplanRecord first: %v", err)
	}
	if _, claimed, err := repository.ClaimAutoplan(context.Background(), first, 10, now); err != nil || !claimed {
		t.Fatalf("claim first cycle claimed=%v err=%v", claimed, err)
	}
	secondRequest := request
	secondRequest.PlanningCycle = 2
	second, err := NewAutoplanRecord(secondRequest, now.Add(time.Minute))
	if err != nil {
		t.Fatalf("NewAutoplanRecord second: %v", err)
	}
	if _, claimed, err := repository.ClaimAutoplan(context.Background(), second, 10, now.Add(time.Minute)); err != nil || !claimed {
		t.Fatalf("claim second cycle claimed=%v err=%v", claimed, err)
	}
	latest, found, err := repository.GetLatestAutoplanForSnapshot(context.Background(), request)
	if err != nil || !found || latest.ID != second.ID || latest.PlanningCycle != 2 {
		t.Fatalf("latest snapshot plan=%+v found=%v err=%v", latest, found, err)
	}
}

func claimTestAutoplan(t *testing.T, repository *Repository, now time.Time) AutoplanRecord {
	t.Helper()
	candidate, err := NewAutoplanRecord(testAutoplanRequest(), now)
	if err != nil {
		t.Fatalf("NewAutoplanRecord: %v", err)
	}
	claimed, ok, err := repository.ClaimAutoplan(context.Background(), candidate, 100, now)
	if err != nil || !ok {
		t.Fatalf("ClaimAutoplan record=%+v claimed=%v err=%v", claimed, ok, err)
	}
	return claimed
}

func pendingTestAutoplan(t *testing.T, repository *Repository, now time.Time) AutoplanRecord {
	t.Helper()
	claimed := claimTestAutoplan(t, repository, now)
	proposal := testAutoplanProposal()
	digest, err := AutoplanProposalDigest(proposal)
	if err != nil {
		t.Fatalf("AutoplanProposalDigest: %v", err)
	}
	proposed := claimed
	proposed.Status, proposed.Proposal, proposed.ProposalDigest = AutoplanStatusProposed, proposal, digest
	updated, current, err := repository.CompareAndSaveAutoplan(context.Background(), proposed, claimed.Version, now.Add(time.Minute))
	if err != nil || !updated {
		t.Fatalf("save proposed updated=%v current=%+v err=%v", updated, current, err)
	}
	pending := current
	pending.Status, pending.ApprovalID = AutoplanStatusPendingApproval, "approval-1"
	pending.ApprovalAttempt = 1
	pending.ApprovalRequestedAt = now.Add(2 * time.Minute)
	updated, current, err = repository.CompareAndSaveAutoplan(context.Background(), pending, current.Version, now.Add(2*time.Minute))
	if err != nil || !updated {
		t.Fatalf("save pending updated=%v current=%+v err=%v", updated, current, err)
	}
	return current
}
