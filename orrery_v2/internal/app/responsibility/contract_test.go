package responsibility

import (
	"errors"
	"testing"
	"time"
)

func TestAssignmentRequiresOneStableAccountableAgentAndTenant(t *testing.T) {
	now := time.Now().UTC()
	assignment := ResponsibilityAssignment{
		ID:               "resp-1",
		TenantID:         "tenant-1",
		WorkKind:         WorkKindMissionTask,
		WorkID:           "task-1",
		AccountableAgent: "agent-1",
		State:            AssignmentStateOffered,
		AcceptBy:         now.Add(time.Minute),
		DueAt:            now.Add(time.Hour),
		Version:          1,
	}
	if err := assignment.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}

	assignment.AccountableAgent = ""
	if err := assignment.Validate(); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("Validate() error = %v, want ErrInvalidContract", err)
	}

	assignment.AccountableAgent = "agent-1"
	assignment.AcceptBy = time.Time{}
	if err := assignment.Validate(); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("Validate() without acceptance deadline error = %v, want ErrInvalidContract", err)
	}
}

func TestAssignmentStateMachineSeparatesRuntimeCompletionFromFulfillment(t *testing.T) {
	allowed := [][2]AssignmentState{
		{AssignmentStateOffered, AssignmentStateAccepted},
		{AssignmentStateAccepted, AssignmentStateActive},
		{AssignmentStateActive, AssignmentStateSubmitted},
		{AssignmentStateSubmitted, AssignmentStateFulfilled},
		{AssignmentStateActive, AssignmentStateWaiting},
		{AssignmentStateWaiting, AssignmentStateActive},
		{AssignmentStateSubmitted, AssignmentStateActive},
	}
	for _, transition := range allowed {
		if err := ValidateAssignmentTransition(transition[0], transition[1]); err != nil {
			t.Errorf("ValidateAssignmentTransition(%s, %s) error = %v", transition[0], transition[1], err)
		}
	}

	for _, transition := range [][2]AssignmentState{
		{AssignmentStateActive, AssignmentStateFulfilled},
		{AssignmentStateWaiting, AssignmentStateFulfilled},
		{AssignmentStateFulfilled, AssignmentStateActive},
	} {
		if err := ValidateAssignmentTransition(transition[0], transition[1]); !errors.Is(err, ErrInvalidTransition) {
			t.Errorf("ValidateAssignmentTransition(%s, %s) error = %v, want ErrInvalidTransition", transition[0], transition[1], err)
		}
	}
}

func TestActivationRequiresDedupeKeyAndOrderedLifecycle(t *testing.T) {
	activation := ResponsibilityActivation{
		ID:                "activation-1",
		TenantID:          "tenant-1",
		ResponsibilityID:  "resp-1",
		Cause:             ActivationCauseAssigned,
		DedupeKey:         ActivationDedupeKey("tenant-1", "resp-1", ActivationCauseAssigned, 1),
		AssignmentVersion: 1,
		Status:            ActivationStatusPending,
	}
	if err := activation.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	activation.DedupeKey = ""
	if err := activation.Validate(); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("Validate() error = %v, want ErrInvalidContract", err)
	}
	if err := ValidateActivationTransition(ActivationStatusPending, ActivationStatusDispatched); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("pending -> dispatched error = %v, want ErrInvalidTransition", err)
	}

	activation.DedupeKey = ActivationDedupeKey("tenant-1", "resp-1", ActivationCauseAssigned, 1)
	activation.AssignmentVersion = 2
	if err := activation.Validate(); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("Validate() with stale dedupe key error = %v, want ErrInvalidContract", err)
	}
}

func TestOpenBlockerMustBeRecoverable(t *testing.T) {
	blocker := ResponsibilityBlocker{
		ID:               "blocker-1",
		TenantID:         "tenant-1",
		ResponsibilityID: "resp-1",
		DedupeKey:        "merge-approval",
		Kind:             BlockerKindApproval,
		Needed:           "merge approval",
		RequestedFrom:    "user-1",
		Status:           BlockerStatusOpen,
	}
	if err := blocker.Validate(); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("Validate() error = %v, want ErrInvalidContract", err)
	}
	blocker.RecheckAt = time.Now().UTC().Add(time.Minute)
	if err := blocker.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestRuntimeObjectsRequireExplicitTenantBoundary(t *testing.T) {
	activation := ResponsibilityActivation{
		ID:                "activation-1",
		ResponsibilityID:  "resp-1",
		Cause:             ActivationCauseAssigned,
		DedupeKey:         ActivationDedupeKey("tenant-1", "resp-1", ActivationCauseAssigned, 1),
		AssignmentVersion: 1,
		Status:            ActivationStatusPending,
	}
	if err := activation.Validate(); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("activation Validate() error = %v, want ErrInvalidContract", err)
	}

	attempt := ResponsibilityAttempt{
		ID:               "attempt-1",
		ResponsibilityID: "resp-1",
		AgentRunID:       "run-1",
		ClaimToken:       "claim-1",
		Attempt:          1,
		StartedAt:        time.Now().UTC(),
	}
	if err := attempt.Validate(); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("attempt Validate() error = %v, want ErrInvalidContract", err)
	}
}

func TestCompletionGateRequiresDeliverableEvidenceAndApprovals(t *testing.T) {
	submission := CompletionSubmission{
		Summary:             "Implemented and verified",
		DeliverableRefs:     []string{"artifact://result-1"},
		EvidenceRefs:        []string{"test://run-1"},
		RequiredEvidence:    1,
		QARequired:          true,
		HumanReviewRequired: true,
	}
	if err := submission.ValidateForFulfillment(); !errors.Is(err, ErrCompletionBlocked) {
		t.Fatalf("ValidateForFulfillment() error = %v, want ErrCompletionBlocked", err)
	}
	submission.QAApproved = true
	submission.HumanApproved = true
	if err := submission.ValidateForFulfillment(); err != nil {
		t.Fatalf("ValidateForFulfillment() error = %v", err)
	}
}

func TestCompletionGateNeverTreatsMissingEvidenceAsComplete(t *testing.T) {
	submission := CompletionSubmission{
		Summary:         "Implemented",
		DeliverableRefs: []string{"artifact://result-1"},
	}
	if err := submission.ValidateForFulfillment(); !errors.Is(err, ErrCompletionBlocked) {
		t.Fatalf("ValidateForFulfillment() error = %v, want ErrCompletionBlocked", err)
	}
}

func TestTraceLinksRequireCorrelationAndMissionForMissionTask(t *testing.T) {
	links := TraceLinks{TenantID: "tenant-1", CorrelationID: "trace-1", MissionTaskID: "task-1"}
	if err := links.Validate(); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("Validate() error = %v, want ErrInvalidContract", err)
	}
	links.MissionID = "mission-1"
	if err := links.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
