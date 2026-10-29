package task

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

func TestTaskStatusTransitionMatrixAllowsDocumentedFlow(t *testing.T) {
	allowed := [][2]TaskStatus{
		{TaskStatusDraft, TaskStatusAssigned},
		{TaskStatusAssigned, TaskStatusAccepted},
		{TaskStatusAssigned, TaskStatusCancelled},
		{TaskStatusAccepted, TaskStatusInProgress},
		{TaskStatusAccepted, TaskStatusBlocked},
		{TaskStatusInProgress, TaskStatusSubmitted},
		{TaskStatusSubmitted, TaskStatusInReview},
		{TaskStatusSubmitted, TaskStatusApproved},
		{TaskStatusInReview, TaskStatusApproved},
		{TaskStatusInReview, TaskStatusRejected},
		{TaskStatusRejected, TaskStatusReworkRequested},
		{TaskStatusReworkRequested, TaskStatusAssigned},
	}

	for _, tc := range allowed {
		if !CanTransition(tc[0], tc[1]) {
			t.Fatalf("expected transition %s -> %s to be allowed", tc[0], tc[1])
		}
	}
}

func TestTaskStatusTransitionMatrixRejectsTerminalAndSkippedTransitions(t *testing.T) {
	rejected := [][2]TaskStatus{
		{TaskStatusDraft, TaskStatusApproved},
		{TaskStatusAssigned, TaskStatusSubmitted},
		{TaskStatusApproved, TaskStatusSubmitted},
		{TaskStatusCancelled, TaskStatusAccepted},
	}

	for _, tc := range rejected {
		if CanTransition(tc[0], tc[1]) {
			t.Fatalf("expected transition %s -> %s to be rejected", tc[0], tc[1])
		}
	}
}

func TestTaskLifecycleTransitionGuardsVersionAndTerminalState(t *testing.T) {
	record := TaskRecord{
		Contract: validTaskContract(),
		Status:   TaskStatusAssigned,
		Version:  7,
	}

	updated, err := record.Transition(TaskStatusAccepted, TransitionOptions{
		ExpectedVersion: 7,
		Reason:          "worker accepted",
		Now:             time.Date(2026, 5, 31, 1, 2, 3, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Transition returned error: %v", err)
	}
	if updated.Status != TaskStatusAccepted || updated.Version != 8 {
		t.Fatalf("unexpected updated record: %#v", updated)
	}
	if updated.StatusChangedAt.IsZero() || updated.LastTransitionReason != "worker accepted" {
		t.Fatalf("transition metadata not recorded: %#v", updated)
	}

	if _, err := updated.Transition(TaskStatusSubmitted, TransitionOptions{ExpectedVersion: 7}); err == nil {
		t.Fatal("expected stale version transition to fail")
	}

	terminal := updated
	terminal.Status = TaskStatusApproved
	if _, err := terminal.Transition(TaskStatusSubmitted, TransitionOptions{ExpectedVersion: terminal.Version}); err == nil {
		t.Fatal("expected terminal transition to fail")
	}
}

func TestTaskRecordReassignReturnsExecutableStatesToAssigned(t *testing.T) {
	for _, status := range []TaskStatus{TaskStatusDraft, TaskStatusAssigned, TaskStatusAccepted, TaskStatusInProgress, TaskStatusBlocked, TaskStatusRejected, TaskStatusReworkRequested} {
		t.Run(string(status), func(t *testing.T) {
			record := TaskRecord{Contract: validTaskContract(), Status: status, Version: 3}
			now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
			updated, err := record.Reassign(TaskAssigneeRef{AgentProfileRef: &agentcore.AgentProfileRef{ID: "agent-new"}}, TransitionOptions{ExpectedVersion: 3, Reason: "capacity recovery", Now: now})
			if err != nil {
				t.Fatal(err)
			}
			if updated.Status != TaskStatusAssigned || updated.Contract.Assignee.AgentProfileRef == nil || updated.Contract.Assignee.AgentProfileRef.ID != "agent-new" || updated.Version != 4 || updated.LastTransitionReason != "capacity recovery" || !updated.StatusChangedAt.Equal(now) {
				t.Fatalf("reassigned record = %+v", updated)
			}
			if record.Contract.Assignee.AgentProfileRef != nil && record.Contract.Assignee.AgentProfileRef.ID == "agent-new" {
				t.Fatal("reassignment mutated the original record")
			}
		})
	}
}

func TestTaskRecordReassignRejectsStaleInvalidAndReviewOrTerminalStates(t *testing.T) {
	record := TaskRecord{Contract: validTaskContract(), Status: TaskStatusInProgress, Version: 3}
	assignee := TaskAssigneeRef{AgentProfileRef: &agentcore.AgentProfileRef{ID: "agent-new"}}
	if _, err := record.Reassign(assignee, TransitionOptions{ExpectedVersion: 2}); err == nil {
		t.Fatal("stale reassignment succeeded")
	}
	if _, err := record.Reassign(TaskAssigneeRef{}, TransitionOptions{ExpectedVersion: 3}); err == nil {
		t.Fatal("invalid assignee reassignment succeeded")
	}
	for _, status := range []TaskStatus{TaskStatusInReview, TaskStatusSubmitted, TaskStatusApproved, TaskStatusCancelled} {
		record.Status = status
		if _, err := record.Reassign(assignee, TransitionOptions{ExpectedVersion: 3}); err == nil {
			t.Fatalf("reassignment from %s succeeded", status)
		}
	}
}

func TestNewTaskAssignmentRequiresAgentSubject(t *testing.T) {
	contract := validTaskContract()
	assignment, err := NewTaskAssignment(contract, TaskAssignmentOptions{
		AssignedBy: agentcore.AgentID("task-assigner-agent"),
		AssignedAt: time.Date(2026, 5, 31, 1, 2, 3, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NewTaskAssignment returned error: %v", err)
	}
	if assignment.TaskID != contract.ID || assignment.MissionID != contract.MissionID {
		t.Fatalf("assignment lost task identity: %#v", assignment)
	}
	if assignment.AssigneeAgentID != agentcore.AgentInstanceID("agent-worker-001") {
		t.Fatalf("assignment did not resolve agent assignee: %#v", assignment)
	}
	if assignment.Status != TaskStatusAssigned {
		t.Fatalf("assignment status = %s, want assigned", assignment.Status)
	}

	contract.Assignee = TaskAssigneeRef{ToolRef: &agentcore.ToolRef{ToolID: agentcore.ToolID("search")}}
	if _, err := NewTaskAssignment(contract, TaskAssignmentOptions{}); err == nil {
		t.Fatal("expected tool assignee to be rejected")
	}
}

func TestSubmissionIdempotencyKeyUsesRunWhenExplicitKeyMissing(t *testing.T) {
	key := SubmissionIdempotencyKey(agentcore.AgentRunID("run-001"), "")
	if key != "run:run-001" {
		t.Fatalf("key = %q, want run:run-001", key)
	}
	if got := SubmissionIdempotencyKey(agentcore.AgentRunID("run-001"), "manual-key"); got != "manual-key" {
		t.Fatalf("key = %q, want manual-key", got)
	}
}

func TestReworkRequestTracksOriginalTaskAndFailedChecks(t *testing.T) {
	request := NewReworkRequest(ReworkRequestOptions{
		ID:              ReworkRequestID("rework-001"),
		OriginalTaskID:  TaskID("task-001"),
		TargetAssignee:  TaskAssigneeRef{AgentProfileRef: &agentcore.AgentProfileRef{ID: agentcore.AgentProfileID("agent-worker-001")}},
		Reason:          "tests missing",
		FailedCriteria:  []AcceptanceCriterionID{AcceptanceCriterionID("acceptance-001")},
		MissingEvidence: []EvidenceRequirementID{EvidenceRequirementID("evidence-required-001")},
		RequestedBy:     agentcore.AgentID("qa-agent"),
		CreatedAt:       time.Date(2026, 5, 31, 1, 2, 3, 0, time.UTC),
	})

	if request.OriginalTaskID != missioncore.TaskID("task-001") || request.Status != ReworkStatusOpen {
		t.Fatalf("unexpected rework request: %#v", request)
	}
	if len(request.FailedCriteria) != 1 || len(request.MissingEvidence) != 1 {
		t.Fatalf("rework request lost QA details: %#v", request)
	}
}
