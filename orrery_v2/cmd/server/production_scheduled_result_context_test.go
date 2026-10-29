package main

import (
	"context"
	"testing"

	schedulerapp "orrery_v2/internal/app/scheduler"
	todoapp "orrery_v2/internal/app/todo"
)

// Historical delivery and current execution state remain separate in context.
func TestScheduledResultAssistantContextUsesCurrentAuthorizationAndState(t *testing.T) {
	s, repo, _ := scheduledResultFixture(t)
	run := scheduledAuthoredRun(s)
	repo.runs = []schedulerapp.ScheduledRun{run}
	key := personalAssistantScheduledResultKey(personalAssistantScheduledRunReference(s.config.recipients[0], run))
	target := productionPersonalAssistantWeixinTarget{TenantID: "tenant-a", OwnerID: "owner-a", AccountID: "bot-a"}
	scopes := []string{"scheduler.read"}
	s.resolve = func(context.Context, productionPersonalAssistantAttentionRecipient) (todoapp.ViewerRef, productionPersonalAssistantWeixinTarget, bool, error) {
		return todoapp.ViewerRef{PermissionScope: scopes}, target, true, nil
	}
	repo.runs[0].Status = schedulerapp.RunStatusRunning
	notice, visible, err := s.ContextNotice(context.Background(), key, "delivered", run.Result.AgentReport.Body, "human-a", target)
	if err != nil || !visible || notice.CurrentStatus != "running" || notice.DeliveryStatus != "delivered" {
		t.Fatalf("stale context: %+v %v %v", notice, visible, err)
	}
	scopes = nil
	if _, visible, err := s.ContextNotice(context.Background(), key, "delivered", run.Result.AgentReport.Body, "human-a", target); err != nil || visible {
		t.Fatalf("revoked scope remained visible: %v %v", visible, err)
	}
}
