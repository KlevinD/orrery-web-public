package team

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
)

func TestMissionTeamContractsPreserveAgentProfileOrgIdentity(t *testing.T) {
	profile := validTeamProfile()
	assignment := MissionAssignment{
		ID:                     MissionAssignmentID("assignment-001"),
		MissionID:              missioncore.MissionID("mission-001"),
		MemberID:               MissionTeamMemberID("member-001"),
		AgentInstanceID:        agentcore.AgentInstanceID("instance-001"),
		AgentProfileID:         profile.ID,
		Role:                   MissionRoleWorker,
		ResponsibilityBoundary: "implement checkout flow",
		AuthorityScope:         "repo:write",
		AssignedBy:             ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent},
		AssignedAt:             time.Date(2026, 6, 1, 1, 2, 3, 0, time.UTC),
		Status:                 AssignmentStatusActive,
	}

	if assignment.AgentProfileID != profile.ID {
		t.Fatalf("assignment profile id = %s, want %s", assignment.AgentProfileID, profile.ID)
	}
	if profile.OrgIdentity.DepartmentRef.ID != agentcore.DepartmentID("engineering") {
		t.Fatalf("mission assignment mutated stable org identity: %#v", profile.OrgIdentity)
	}
	if assignment.Role == "" || assignment.AuthorityScope == "" {
		t.Fatalf("assignment should carry mission-local role and authority: %#v", assignment)
	}
}

func TestMissionTeamMemberRejectsNonAgentResourceRefs(t *testing.T) {
	if err := ValidateMissionTeamMember(MissionTeamMember{AgentProfileID: "profile-001"}); err == nil {
		t.Fatalf("member without agent instance should be rejected")
	}
	contract := missiontask.TaskContract{
		ID:                 "task-001",
		MissionID:          "mission-001",
		Assignee:           missiontask.TaskAssigneeRef{ToolRef: &agentcore.ToolRef{ToolID: "tool-search"}},
		Title:              "Search",
		Goal:               "Search docs",
		WorkerPersonalGoal: "Return cited findings",
		WorkerPersonalGoalSources: []missiontask.WorkerPersonalGoalSourceRef{{
			Kind:             missiontask.WorkerPersonalGoalSourceGoal,
			ContractFieldRef: missiontask.ContractFieldRef{Field: missiontask.ContractFieldGoal},
		}},
		ExpectedDeliverables: []missiontask.ExpectedDeliverable{{ID: "deliverable-001", Title: "Findings"}},
	}
	if errs := missiontask.ValidateTaskContract(contract); len(errs) == 0 {
		t.Fatalf("task contract should reject tool refs as assignees")
	}
}

func TestStaffingRequestDecisionAndAuditContractsCaptureRejectedReasons(t *testing.T) {
	eval := CandidateEvaluation{
		CandidateProfileID:  agentcore.AgentProfileID("profile-rejected"),
		CandidateInstanceID: agentcore.AgentInstanceID("instance-rejected"),
		AvailabilityResult:  EvaluationResult{Allowed: false, Code: CodeAvailabilityDenied, Message: "busy"},
		RiskResult:          EvaluationResult{Allowed: false, Code: CodeRiskDenied, Message: "high risk"},
		Score:               12,
		RejectionReasons:    []PolicyCode{CodeAvailabilityDenied, CodeRiskDenied},
	}
	decision := StaffingDecision{
		ID:                      StaffingDecisionID("decision-001"),
		RequestID:               StaffingRequestID("request-001"),
		SelectedAgentProfileID:  agentcore.AgentProfileID("profile-selected"),
		SelectedAgentInstanceID: agentcore.AgentInstanceID("instance-selected"),
		RejectedCandidates:      []CandidateEvaluation{eval},
		Rationale:               "selected lower risk worker with matching skills",
		DecidedBy:               ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent},
		DecidedAt:               time.Date(2026, 6, 1, 1, 2, 3, 0, time.UTC),
		AuditRef:                StaffingAuditID("audit-001"),
	}
	audit := StaffingAudit{
		ID:                  decision.AuditRef,
		MissionID:           missioncore.MissionID("mission-001"),
		RequestID:           decision.RequestID,
		DecisionID:          decision.ID,
		SelectedRationale:   decision.Rationale,
		RejectedEvaluations: decision.RejectedCandidates,
		CreatedAt:           decision.DecidedAt,
	}

	if len(audit.RejectedEvaluations) != 1 || audit.RejectedEvaluations[0].RejectionReasons[0] != CodeAvailabilityDenied {
		t.Fatalf("audit lost rejection reasons: %#v", audit)
	}
	if decision.SelectedAgentProfileID == "" || decision.AuditRef == "" {
		t.Fatalf("decision should carry selected refs and audit ref: %#v", decision)
	}
}

func validTeamProfile() agentcore.AgentProfile {
	return agentcore.AgentProfile{
		ID:          agentcore.AgentProfileID("profile-001"),
		RealName:    "Ada Lovelace",
		DisplayName: "Ada",
		RoleName:    "Worker",
		AgentKind:   agentcore.AgentKindAI,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef:   agentcore.OrganizationRef{ID: "orrery"},
			DepartmentRef:     agentcore.DepartmentRef{ID: "engineering"},
			PositionRef:       agentcore.PositionRef{ID: "backend-worker"},
			RoleRefs:          []agentcore.RoleRef{{ID: "worker"}},
			AuthorityScope:    "repo:write",
			EmploymentType:    agentcore.EmploymentTypeAIAgent,
			SupervisionPolicy: agentcore.SupervisionPolicyRoot,
		},
		Enabled: true,
	}
}
