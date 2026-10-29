package task

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/testkit"
)

func TestTaskQAServiceRequestsQA(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusInReview))
	svc := NewQAService(env.UnitOfWork, repo, repo, QAServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})

	if err := svc.RequestQA(ctx, TaskID("task-001"), agentcore.AgentID("review-coordinator-agent")); err != nil {
		t.Fatalf("RequestQA returned error: %v", err)
	}

	assertEventType(t, repo.events, TaskEventQARequested)
	assertEventActorProfileID(t, repo.events, TaskEventQARequested, agentcore.AgentProfileID("review-coordinator-agent"))
}

func TestTaskQAServiceCompletesQAPlanWithArtifactRef(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	record := validTaskRecord(TaskStatusAccepted)
	record.Contract.QAEnabled = true
	record.Contract.QAPlanRequired = true
	record.Contract.QAPlanCompleted = false
	repo := newMemoryRepository(record)
	svc := NewQAService(env.UnitOfWork, repo, repo, QAServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})

	plan, err := svc.SubmitQAPlan(ctx, TaskID("task-001"), QAPlanRecord{
		ID:          QAPlanID("qa-plan-001"),
		CreatedBy:   agentcore.AgentID("qa-plan-agent"),
		ArtifactRef: agentcore.ArtifactRef{ID: "artifact-qa-plan-001", Kind: agentcore.ArtifactKindLog, URI: "artifact://qa-plan-001"},
		Summary:     "BDD/TDD plan ready",
	}, QAPlanSubmitOptions{IdempotencyKey: "qa-plan-key"})
	if err != nil {
		t.Fatalf("SubmitQAPlan returned error: %v", err)
	}

	if !repo.task.Contract.QAPlanCompleted {
		t.Fatalf("QA plan should mark task contract complete: %+v", repo.task.Contract)
	}
	if repo.task.Contract.QAPlanRef != "qa-plan-001" || repo.task.Contract.QAPlanArtifactRef.ID != "artifact-qa-plan-001" {
		t.Fatalf("QA plan refs were not persisted on task contract: %+v", repo.task.Contract)
	}
	if plan.IdempotencyKey != "qa-plan-key" || plan.CompletedAt.IsZero() {
		t.Fatalf("QA plan metadata was not prepared: %+v", plan)
	}
	assertEventType(t, repo.events, TaskEventQAPlanCompleted)
}

func TestTaskQAServiceReplaysCompletedQAPlanIdempotently(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	record := validTaskRecord(TaskStatusAccepted)
	record.Contract.QAEnabled = true
	record.Contract.QAPlanRequired = true
	record.Contract.QAPlanCompleted = false
	repo := newMemoryRepository(record)
	svc := NewQAService(env.UnitOfWork, repo, repo, QAServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})
	input := QAPlanRecord{
		ID: "qa-plan-001", CreatedBy: "qa-plan-agent", Summary: "BDD/TDD plan ready",
		ArtifactRef: agentcore.ArtifactRef{ID: "artifact-qa-plan-001", Kind: agentcore.ArtifactKindLog, URI: "artifact://qa-plan-001"},
	}
	first, err := svc.SubmitQAPlan(ctx, "task-001", input, QAPlanSubmitOptions{IdempotencyKey: "qa-plan-key"})
	if err != nil {
		t.Fatalf("first SubmitQAPlan: %v", err)
	}
	version := repo.task.Version
	eventCount := len(repo.events)
	replayed, err := svc.SubmitQAPlan(ctx, "task-001", input, QAPlanSubmitOptions{IdempotencyKey: "qa-plan-key"})
	if err != nil {
		t.Fatalf("replayed SubmitQAPlan: %v", err)
	}
	if replayed.ID != first.ID || replayed.ArtifactRef.ID != first.ArtifactRef.ID || repo.task.Version != version || len(repo.events) != eventCount {
		t.Fatalf("replay mutated completed QA plan: first=%+v replayed=%+v task=%+v events=%d", first, replayed, repo.task, len(repo.events))
	}
	if _, err := svc.SubmitQAPlan(ctx, "task-001", input, QAPlanSubmitOptions{IdempotencyKey: "different-key"}); err == nil {
		t.Fatal("expected conflicting QA plan replay to be rejected")
	}
}

func TestTaskQAServiceApprovesWithAcceptanceReport(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusInReview))
	repo.seedSubmission(validSubmission())
	repo.seedEvidence(validEvidenceSubmission())
	svc := NewQAService(env.UnitOfWork, repo, repo, QAServiceOptions{
		Clock:       env.Clock,
		IDGenerator: env.IDGenerator,
		Contract: QAContract{
			AcceptanceCriteria:   validTaskContract().AcceptanceCriteria,
			EvidenceRequirements: validTaskContract().EvidenceRequirements,
		},
		Policy: QAPolicy{
			EvidenceStrictness:      EvidenceStrictnessRequired,
			RequireAcceptanceReport: true,
			RequireReasonOnReject:   true,
		},
	})

	result := QAResultRecord{
		TaskID:       TaskID("task-001"),
		SubmissionID: "submission-001",
		CheckedBy:    agentcore.AgentID("qa-agent"),
		Status:       QAStatusApproved,
		CheckedCriteria: []CheckedCriterion{{
			CriterionID: AcceptanceCriterionID("acceptance-001"),
			Status:      CheckStatusPassed,
		}},
		EvidenceResults: []EvidenceCheckResult{{
			RequirementID: EvidenceRequirementID("evidence-required-001"),
			Status:        CheckStatusPassed,
		}},
	}

	qaResult, err := svc.SubmitQAResult(ctx, TaskID("task-001"), result, QASubmitOptions{IdempotencyKey: "qa-001"})
	if err != nil {
		t.Fatalf("SubmitQAResult returned error: %v", err)
	}

	if repo.task.Status != TaskStatusApproved {
		t.Fatalf("status = %s, want approved", repo.task.Status)
	}
	if qaResult.AcceptanceReportRef == "" {
		t.Fatalf("expected acceptance report ref: %#v", qaResult)
	}
	if len(repo.acceptanceReports) != 1 || repo.acceptanceReports[0].Summary != "QA approved submission submission-001: 1/1 acceptance criteria passed and 1/1 evidence requirements passed." {
		t.Fatalf("acceptance report = %+v", repo.acceptanceReports)
	}
	assertEventType(t, repo.events, TaskEventQAApproved)
	assertEventType(t, repo.events, TaskEventAcceptanceReportCreated)
}

func TestTaskQAServiceRejectsApprovedResultWhenRequiredEvidenceMissing(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusInReview))
	repo.seedSubmission(validSubmission())
	svc := NewQAService(env.UnitOfWork, repo, repo, QAServiceOptions{
		Clock:       env.Clock,
		IDGenerator: env.IDGenerator,
		Contract: QAContract{
			AcceptanceCriteria:   validTaskContract().AcceptanceCriteria,
			EvidenceRequirements: validTaskContract().EvidenceRequirements,
		},
		Policy: QAPolicy{
			EvidenceStrictness:      EvidenceStrictnessRequired,
			RequireAcceptanceReport: true,
			RequireReasonOnReject:   true,
		},
	})

	qaResult, err := svc.SubmitQAResult(ctx, TaskID("task-001"), QAResultRecord{
		TaskID:       TaskID("task-001"),
		SubmissionID: "submission-001",
		CheckedBy:    agentcore.AgentID("qa-agent"),
		Status:       QAStatusApproved,
	}, QASubmitOptions{IdempotencyKey: "qa-missing-evidence"})
	if err != nil {
		t.Fatalf("SubmitQAResult returned error: %v", err)
	}

	if repo.task.Status != TaskStatusReworkRequested {
		t.Fatalf("status = %s, want rework_requested", repo.task.Status)
	}
	if qaResult.Status != QAStatusRejected || len(qaResult.MissingEvidence) == 0 {
		t.Fatalf("QA result was not policy-rejected: %#v", qaResult)
	}
}

func TestTaskQAServiceRejectsWithReworkRequest(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusInReview))
	repo.seedSubmission(validSubmission())
	svc := NewQAService(env.UnitOfWork, repo, repo, QAServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})

	result := QAResultRecord{
		TaskID:       TaskID("task-001"),
		SubmissionID: "submission-001",
		CheckedBy:    agentcore.AgentID("qa-agent"),
		Status:       QAStatusRejected,
		CheckedCriteria: []CheckedCriterion{{
			CriterionID: AcceptanceCriterionID("acceptance-001"),
			Status:      CheckStatusFailed,
			Reason:      "tests missing",
		}},
		MissingEvidence:  []EvidenceRequirementID{EvidenceRequirementID("evidence-required-001")},
		RejectionReasons: []string{"tests missing"},
	}

	qaResult, err := svc.SubmitQAResult(ctx, TaskID("task-001"), result, QASubmitOptions{IdempotencyKey: "qa-001"})
	if err != nil {
		t.Fatalf("SubmitQAResult returned error: %v", err)
	}

	if repo.task.Status != TaskStatusReworkRequested {
		t.Fatalf("status = %s, want rework_requested", repo.task.Status)
	}
	if qaResult.ReworkRequestRef == "" || repo.openReworkRequest == nil {
		t.Fatalf("expected rework request: result=%#v request=%#v", qaResult, repo.openReworkRequest)
	}
	assertEventType(t, repo.events, TaskEventQARejected)
	assertEventType(t, repo.events, TaskEventReworkRequested)
}

func TestTaskQAServiceRejectsStaleSubmissionAndIdempotentReject(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusInReview))
	repo.seedSubmission(validSubmission())
	repo.seedSubmission(TaskSubmission{ID: TaskSubmissionID("submission-002"), TaskID: TaskID("task-001"), SubmittedAt: env.Clock.Now().Add(time.Hour)})
	svc := NewQAService(env.UnitOfWork, repo, repo, QAServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})

	stale := QAResultRecord{TaskID: TaskID("task-001"), SubmissionID: "submission-001", CheckedBy: agentcore.AgentID("qa-agent"), Status: QAStatusApproved}
	if _, err := svc.SubmitQAResult(ctx, TaskID("task-001"), stale, QASubmitOptions{IdempotencyKey: "qa-stale"}); err == nil {
		t.Fatal("expected stale submission QA to fail")
	}

	reject := QAResultRecord{
		TaskID:           TaskID("task-001"),
		SubmissionID:     "submission-002",
		CheckedBy:        agentcore.AgentID("qa-agent"),
		Status:           QAStatusRejected,
		RejectionReasons: []string{"missing evidence"},
	}
	first, err := svc.SubmitQAResult(ctx, TaskID("task-001"), reject, QASubmitOptions{IdempotencyKey: "qa-dup"})
	if err != nil {
		t.Fatalf("first reject returned error: %v", err)
	}
	second, err := svc.SubmitQAResult(ctx, TaskID("task-001"), reject, QASubmitOptions{IdempotencyKey: "qa-dup"})
	if err != nil {
		t.Fatalf("second reject returned error: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("idempotent QA returned different result: %s vs %s", first.ID, second.ID)
	}
}

func TestTaskQAServiceRejectsTerminalTasks(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusApproved))
	repo.seedSubmission(validSubmission())
	svc := NewQAService(env.UnitOfWork, repo, repo, QAServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})

	_, err := svc.SubmitQAResult(ctx, TaskID("task-001"), QAResultRecord{
		TaskID:       TaskID("task-001"),
		SubmissionID: "submission-001",
		CheckedBy:    agentcore.AgentID("qa-agent"),
		Status:       QAStatusApproved,
	}, QASubmitOptions{IdempotencyKey: "qa-terminal"})

	if err == nil {
		t.Fatal("expected terminal task QA to fail")
	}
}

func TestTaskQAServiceRequiresConfiguredReviewer(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := newMemoryRepository(validTaskRecord(TaskStatusInReview))
	repo.seedSubmission(validSubmission())
	repo.seedEvidence(validEvidenceSubmission())
	svc := NewQAService(env.UnitOfWork, repo, repo, QAServiceOptions{
		Clock:       env.Clock,
		IDGenerator: env.IDGenerator,
		Contract: QAContract{
			AcceptanceCriteria:         validTaskContract().AcceptanceCriteria,
			EvidenceRequirements:       validTaskContract().EvidenceRequirements,
			RequiredReviewerProfileRef: agentcore.AgentProfileRef{ID: agentcore.AgentProfileID("qa-agent-required")},
		},
		Policy: QAPolicy{EvidenceStrictness: EvidenceStrictnessRequired},
	})

	_, err := svc.SubmitQAResult(ctx, TaskID("task-001"), QAResultRecord{
		TaskID:       TaskID("task-001"),
		SubmissionID: "submission-001",
		CheckedBy:    agentcore.AgentID("qa-agent-other"),
		Status:       QAStatusApproved,
	}, QASubmitOptions{IdempotencyKey: "qa-wrong-reviewer"})

	if err == nil {
		t.Fatal("expected non-required reviewer to be rejected")
	}
}

func validSubmission() TaskSubmission {
	return TaskSubmission{
		ID:             TaskSubmissionID("submission-001"),
		TaskID:         TaskID("task-001"),
		AgentID:        agentcore.AgentID("agent-worker-001"),
		AgentResultRef: agentcore.AgentResultID("result-001"),
		Summary:        agentcore.Summary{Text: "done"},
		IdempotencyKey: "submission-001",
	}
}

func validEvidenceSubmission() EvidenceSubmission {
	return EvidenceSubmission{
		ID:           EvidenceSubmissionID("evidence-submission-001"),
		SubmissionID: TaskSubmissionID("submission-001"),
		TaskID:       TaskID("task-001"),
		AgentID:      agentcore.AgentID("agent-worker-001"),
		Evidence:     validAgentResultForContract().Evidence[0],
		SubmittedAt:  time.Date(2026, 5, 31, 1, 2, 3, 0, time.UTC),
	}
}
