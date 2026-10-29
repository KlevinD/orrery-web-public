package task

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestResultPolicyCheckerAcceptsValidResultForAssignee(t *testing.T) {
	checker := NewResultPolicyChecker(ResultPolicyCheckerOptions{})
	contract := validTaskContract()
	result := validAgentResultForContract()

	errs := checker.Check(ResultPolicyInput{
		Contract: contract,
		Result:   result,
		Agent:    agentcore.AgentInstanceRef{ID: agentcore.AgentInstanceID("agent-worker-001")},
	})

	if len(errs) != 0 {
		t.Fatalf("Check returned errors: %#v", errs)
	}
}

func TestResultPolicyCheckerRejectsWrongAssigneeAndReworkLimit(t *testing.T) {
	checker := NewResultPolicyChecker(ResultPolicyCheckerOptions{MaxReworkAttempts: 1})
	contract := validTaskContract()
	result := validAgentResultForContract()

	errs := checker.Check(ResultPolicyInput{
		Contract:       contract,
		Result:         result,
		Agent:          agentcore.AgentInstanceRef{ID: agentcore.AgentInstanceID("agent-other")},
		ReworkAttempts: 1,
	})

	assertValidationError(t, errs, CodeInvalidRef, "assignee")
	assertValidationError(t, errs, CodeConflict, "rework_attempts")
}

func TestResultPolicyCheckerResolvesMissionAssignmentAssignee(t *testing.T) {
	checker := NewResultPolicyChecker(ResultPolicyCheckerOptions{
		AssigneeResolver: StaticMissionAssignmentResolver{
			MissionAssignmentID("assignment-001"): {
				ProfileRef:  agentcore.AgentProfileRef{ID: agentcore.AgentProfileID("agent-worker-001")},
				InstanceRef: agentcore.AgentInstanceRef{ID: agentcore.AgentInstanceID("agent-worker-001")},
			},
		},
	})
	contract := validTaskContract()
	contract.Assignee = TaskAssigneeRef{MissionAssignmentRef: &MissionAssignmentRef{ID: MissionAssignmentID("assignment-001")}}
	result := validAgentResultForContract()

	errs := checker.Check(ResultPolicyInput{
		Contract: contract,
		Result:   result,
		Agent:    agentcore.AgentInstanceRef{ID: agentcore.AgentInstanceID("agent-worker-001")},
	})

	if len(errs) != 0 {
		t.Fatalf("Check returned errors: %#v", errs)
	}
}
