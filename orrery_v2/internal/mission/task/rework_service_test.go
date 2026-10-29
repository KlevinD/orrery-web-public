package task

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/testkit"
)

func TestReworkServiceReassignsOpenReworkToOriginalAssignee(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusReworkRequested))
	repo.openReworkRequest = &ReworkRequest{
		ID:             ReworkRequestID("rework-001"),
		OriginalTaskID: TaskID("task-001"),
		TargetAssignee: validTaskContract().Assignee,
		Reason:         "missing evidence",
		Status:         ReworkStatusOpen,
	}
	svc := NewReworkService(env.UnitOfWork, repo, repo, ReworkServiceOptions{Clock: env.Clock})

	request, err := svc.ReassignOpenRework(ctx, TaskID("task-001"), agentcore.AgentID("review-coordinator-agent"))
	if err != nil {
		t.Fatalf("ReassignOpenRework returned error: %v", err)
	}

	if request.Status != ReworkStatusAssigned {
		t.Fatalf("rework status = %s, want assigned", request.Status)
	}
	if repo.task.Status != TaskStatusAssigned {
		t.Fatalf("task status = %s, want assigned", repo.task.Status)
	}
	assertEventActorProfileID(t, repo.events, TaskEventReworkAssigned, agentcore.AgentProfileID("review-coordinator-agent"))

	replayed, err := svc.ReassignOpenRework(ctx, TaskID("task-001"), agentcore.AgentID("review-coordinator-agent"))
	if err != nil || replayed.ID != request.ID || replayed.Status != ReworkStatusAssigned {
		t.Fatalf("idempotent ReassignOpenRework = %#v, %v", replayed, err)
	}
	if got := countTaskEvents(repo.events, TaskEventReworkAssigned); got != 1 {
		t.Fatalf("rework assigned event count = %d, want 1", got)
	}
}

func TestReworkServiceResolvesReworkWhenTaskIsAccepted(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusAccepted))
	repo.openReworkRequest = &ReworkRequest{
		ID:             ReworkRequestID("rework-001"),
		OriginalTaskID: TaskID("task-001"),
		TargetAssignee: validTaskContract().Assignee,
		Reason:         "missing evidence",
		Status:         ReworkStatusAssigned,
	}
	svc := NewReworkService(env.UnitOfWork, repo, repo, ReworkServiceOptions{Clock: env.Clock})

	request, err := svc.ResolveRework(ctx, TaskID("task-001"), agentcore.AgentID("agent-worker-001"))
	if err != nil {
		t.Fatalf("ResolveRework returned error: %v", err)
	}

	if request.Status != ReworkStatusResolved {
		t.Fatalf("rework status = %s, want resolved", request.Status)
	}

	replayed, err := svc.ResolveRework(ctx, TaskID("task-001"), agentcore.AgentID("agent-worker-001"))
	if err != nil || replayed.ID != "" {
		t.Fatalf("idempotent ResolveRework = %#v, %v", replayed, err)
	}
	if got := countTaskEvents(repo.events, TaskEventReworkResolved); got != 1 {
		t.Fatalf("rework resolved event count = %d, want 1", got)
	}
}

func countTaskEvents(events []TaskEvent, eventType TaskEventType) int {
	count := 0
	for _, event := range events {
		if event.Type == eventType {
			count++
		}
	}
	return count
}

func TestDeliveryServiceAcceptsMissionAssignmentAssigneeViaResolver(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	record := validTaskRecord(TaskStatusAssigned)
	record.Contract.Assignee = TaskAssigneeRef{MissionAssignmentRef: &MissionAssignmentRef{ID: MissionAssignmentID("assignment-001")}}
	repo := newMemoryRepository(record)
	resolver := StaticMissionAssignmentResolver{
		MissionAssignmentID("assignment-001"): {
			ProfileRef:  agentcore.AgentProfileRef{ID: agentcore.AgentProfileID("agent-worker-001")},
			InstanceRef: agentcore.AgentInstanceRef{ID: agentcore.AgentInstanceID("agent-worker-001:mission-001")},
		},
	}
	svc := NewDeliveryService(env.UnitOfWork, repo, repo, DeliveryServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, AssigneeResolver: resolver})

	if err := svc.AcceptTask(ctx, TaskID("task-001"), agentcore.AgentInstanceRef{ID: agentcore.AgentInstanceID("agent-worker-001:mission-001")}); err != nil {
		t.Fatalf("AcceptTask returned error: %v", err)
	}
	if repo.task.Status != TaskStatusAccepted {
		t.Fatalf("task status = %s, want accepted", repo.task.Status)
	}
	assertEventActorProfileID(t, repo.events, TaskEventTaskAccepted, agentcore.AgentProfileID("agent-worker-001"))
}
