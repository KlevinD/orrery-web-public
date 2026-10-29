package task

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/platform/testkit"
)

func TestTaskDeliveryServiceImportsHistoricalOutcomes(t *testing.T) {
	tests := []struct {
		name              string
		outcome           HistoricalTaskOutcome
		evidenceValidated bool
		failureReason     string
		wantStatus        TaskStatus
		wantEvent         TaskEventType
		unwantedEvent     TaskEventType
		wantSubmission    bool
	}{
		{name: "completed with revalidated evidence", outcome: HistoricalTaskOutcomeCompleted, evidenceValidated: true, wantStatus: TaskStatusApproved, wantEvent: TaskEventQASkipped, wantSubmission: true},
		{name: "completed without sufficient evidence", outcome: HistoricalTaskOutcomeCompleted, wantStatus: TaskStatusSubmitted, wantEvent: TaskEventAgentResultSubmitted, wantSubmission: true},
		{name: "failed", outcome: HistoricalTaskOutcomeFailed, failureReason: "stored runtime failure", wantStatus: TaskStatusBlocked, wantEvent: TaskEventTaskBlocked},
		{name: "skipped", outcome: HistoricalTaskOutcomeSkipped, failureReason: "stored scheduler skip", wantStatus: TaskStatusCancelled, wantEvent: TaskEventTaskCancelled, unwantedEvent: TaskEventTaskAccepted},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			repo := newMemoryRepository(validTaskRecord(TaskStatusAssigned))
			service := NewDeliveryService(env.UnitOfWork, repo, repo, DeliveryServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})
			result := validAgentResultForContract()
			result.RunID = ""
			result.ID = ""
			if !test.evidenceValidated {
				result.Deliverables = nil
				result.Evidence = nil
			}

			imported, err := service.ImportHistoricalOutcome(context.Background(), HistoricalTaskOutcomeInput{
				TaskID: "task-001", MissionID: missioncore.MissionID("mission-001"), AgentProfileID: "agent-worker-001",
				Outcome: test.outcome, Result: result, EvidenceValidated: test.evidenceValidated,
				FailureReason: test.failureReason, IdempotencyKey: "scheduler-history:run-001", OccurredAt: env.Clock.Now(),
			})
			if err != nil {
				t.Fatal(err)
			}
			if imported.Status != test.wantStatus || repo.task.Status != test.wantStatus {
				t.Fatalf("historical Task status = result:%q stored:%q want:%q", imported.Status, repo.task.Status, test.wantStatus)
			}
			if test.failureReason != "" && repo.task.LastTransitionReason != test.failureReason {
				t.Fatalf("historical failure reason = %q, want %q", repo.task.LastTransitionReason, test.failureReason)
			}
			if test.wantSubmission != (len(repo.submissions) == 1) {
				t.Fatalf("historical submissions = %+v, wantSubmission=%v", repo.submissions, test.wantSubmission)
			}
			assertEventType(t, repo.events, test.wantEvent)
			for _, event := range repo.events {
				if event.Type == test.unwantedEvent {
					t.Fatalf("historical %s import emitted unsupported event %q", test.outcome, event.Type)
				}
				if event.ActorProfileID != agentcore.AgentProfileID("agent-worker-001") || !event.OccurredAt.Equal(env.Clock.Now()) {
					t.Fatalf("historical event identity/time = %+v", event)
				}
			}
		})
	}
}

func TestTaskDeliveryServiceHistoricalImportIsIdempotent(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusAssigned))
	service := NewDeliveryService(env.UnitOfWork, repo, repo, DeliveryServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})
	input := HistoricalTaskOutcomeInput{
		TaskID: "task-001", MissionID: "mission-001", AgentProfileID: "agent-worker-001",
		Outcome: HistoricalTaskOutcomeCompleted, Result: validAgentResultForContract(), EvidenceValidated: true,
		IdempotencyKey: "scheduler-history:run-001", OccurredAt: env.Clock.Now(),
	}
	first, err := service.ImportHistoricalOutcome(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	firstEventCount := len(repo.events)
	second, err := service.ImportHistoricalOutcome(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Status != second.Status || len(repo.submissions) != 1 || len(repo.events) != firstEventCount {
		t.Fatalf("idempotent historical import = first:%+v second:%+v submissions:%d events:%d/%d", first, second, len(repo.submissions), firstEventCount, len(repo.events))
	}
}

func TestTaskDeliveryServiceHistoricalImportRejectsDifferentOutcome(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusAssigned))
	service := NewDeliveryService(env.UnitOfWork, repo, repo, DeliveryServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})
	base := HistoricalTaskOutcomeInput{
		TaskID: "task-001", MissionID: "mission-001", AgentProfileID: "agent-worker-001",
		Outcome: HistoricalTaskOutcomeSkipped, FailureReason: "stored skip", IdempotencyKey: "scheduler-history:run-001", OccurredAt: env.Clock.Now(),
	}
	if _, err := service.ImportHistoricalOutcome(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	base.Outcome = HistoricalTaskOutcomeFailed
	base.FailureReason = "different failure"
	if _, err := service.ImportHistoricalOutcome(context.Background(), base); err == nil {
		t.Fatal("different historical outcome overwrote terminal Task")
	}
	if repo.task.Status != TaskStatusCancelled {
		t.Fatalf("conflicting historical import changed Task to %q", repo.task.Status)
	}
}

func TestTaskDeliveryServiceHistoricalImportRejectsResultIdentityMismatch(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusAssigned))
	service := NewDeliveryService(env.UnitOfWork, repo, repo, DeliveryServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})
	result := validAgentResultForContract()
	result.MissionID = "other-mission"

	_, err := service.ImportHistoricalOutcome(context.Background(), HistoricalTaskOutcomeInput{
		TaskID: "task-001", MissionID: "mission-001", AgentProfileID: "agent-worker-001",
		Outcome: HistoricalTaskOutcomeCompleted, Result: result, EvidenceValidated: true,
		IdempotencyKey: "scheduler-history:run-001", OccurredAt: env.Clock.Now(),
	})
	if err == nil {
		t.Fatal("historical import accepted a result from a different Mission")
	}
	if repo.task.Status != TaskStatusAssigned || len(repo.submissions) != 0 || len(repo.events) != 0 {
		t.Fatalf("identity conflict mutated historical Task: status=%q submissions=%d events=%d", repo.task.Status, len(repo.submissions), len(repo.events))
	}
}
