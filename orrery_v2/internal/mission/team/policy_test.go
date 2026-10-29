package team

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

func TestAssignmentPolicyRejectsIncompleteDisabledAndNonAgentProfiles(t *testing.T) {
	policy := NewAssignmentPolicy(DefaultStaffingPolicy())
	request := validStaffingRequest()
	entry := validDirectoryEntry()
	entry.Enabled = false

	eval := policy.Evaluate(request, entry, MissionTeam{})
	if !hasPolicyCode(eval.RejectionReasons, CodeProfileDisabled) {
		t.Fatalf("expected disabled rejection, got %#v", eval)
	}

	entry = validDirectoryEntry()
	entry.OrgIdentity.DepartmentRef = agentcore.DepartmentRef{}
	eval = policy.Evaluate(request, entry, MissionTeam{})
	if !hasPolicyCode(eval.RejectionReasons, CodeOrgIdentityIncomplete) {
		t.Fatalf("expected org identity rejection, got %#v", eval)
	}

	entry = validDirectoryEntry()
	entry.AgentKind = agentcore.AgentKind("tool")
	eval = policy.Evaluate(request, entry, MissionTeam{})
	if !hasPolicyCode(eval.RejectionReasons, CodeCandidateNotAgent) {
		t.Fatalf("expected non-agent rejection, got %#v", eval)
	}
}

func TestAssignmentPolicyEvaluatesOperationalSignalsAndRequiredRefs(t *testing.T) {
	policy := NewAssignmentPolicy(DefaultStaffingPolicy())
	request := validStaffingRequest()
	entry := validDirectoryEntry()
	entry.Availability.Status = agentcore.AvailabilityBusy
	entry.CostProfile.EstimatedCostPerHour = 200
	entry.QualitySignal.Score = 0.4
	entry.RiskProfile.RiskLevel = agentcore.RiskLevelHigh
	entry.SkillRefs = []agentcore.SkillRef{{SkillID: "go"}}
	entry.ToolRefs = nil

	eval := policy.Evaluate(request, entry, MissionTeam{})

	for _, code := range []PolicyCode{CodeAvailabilityDenied, CodeBudgetDenied, CodeQualityDenied, CodeRiskDenied, CodeSkillMissing, CodeToolMissing} {
		if !hasPolicyCode(eval.RejectionReasons, code) {
			t.Fatalf("expected %s in %#v", code, eval.RejectionReasons)
		}
	}
	if eval.Score >= 70 {
		t.Fatalf("rejected candidate should not receive strong score: %#v", eval)
	}
}

func TestAssignmentPolicyAllowsBaseAgentManagedProfilesWithoutExternalRuntime(t *testing.T) {
	policy := NewAssignmentPolicy(DefaultStaffingPolicy())
	request := validStaffingRequest()
	request.DesiredRoles = []MissionRole{MissionRoleStaffing}
	request.RequiredSkills = nil
	request.RequiredTools = nil
	request.AuthorityRequirements = "mission_coordination"
	entry := validDirectoryEntry()
	entry.AgentProfileID = "staffing-agent"
	entry.AgentInstanceID = "staffing-agent"
	entry.RoleName = "Staffing"
	entry.OrgIdentity.RoleRefs = []agentcore.RoleRef{{ID: "staffing"}}
	entry.OrgIdentity.AuthorityScope = "mission_coordination"
	entry.OrgIdentity.EmploymentType = agentcore.EmploymentTypeAIAgent
	entry.RuntimeSpecSummary = agentcore.RuntimeSpecSummary{}
	entry.BaseAgentSummary = agentcore.BaseAgentSummary{InheritsBaseAgent: true, AuditRequired: true, HasSystemPrompt: true, HasLLMAPIKeyRef: true}

	eval := policy.Evaluate(request, entry, MissionTeam{})
	if hasPolicyCode(eval.RejectionReasons, CodeRuntimeNotAvailable) {
		t.Fatalf("BaseAgent-managed profile should not require external runtime: %#v", eval)
	}

	entry.BaseAgentSummary.HasLLMAPIKeyRef = false
	eval = policy.Evaluate(request, entry, MissionTeam{})
	if !hasPolicyCode(eval.RejectionReasons, CodeRuntimeNotAvailable) {
		t.Fatalf("profile without complete BaseAgent contract or runtime should still be rejected: %#v", eval)
	}
}

func TestAssignmentPolicyRejectsOrdinaryMissionRoleMismatch(t *testing.T) {
	policy := NewAssignmentPolicy(DefaultStaffingPolicy())
	request := validStaffingRequest()
	entry := validDirectoryEntry()
	entry.RoleName = "Legal Orchestrator"
	entry.OrgIdentity.RoleRefs = []agentcore.RoleRef{{ID: "legal-orchestrator"}}

	eval := policy.Evaluate(request, entry, MissionTeam{})
	if !hasPolicyCode(eval.RejectionReasons, CodeAuthorityDenied) {
		t.Fatalf("expected role mismatch rejection, got %#v", eval)
	}
}

func TestAssignmentPolicyRejectsDuplicateActiveAssignmentsUnlessIdempotent(t *testing.T) {
	policy := NewAssignmentPolicy(DefaultStaffingPolicy())
	request := validStaffingRequest()
	entry := validDirectoryEntry()
	team := MissionTeam{
		ID:        "team-001",
		MissionID: request.MissionID,
		Members: []MissionTeamMember{{
			ID:             "member-001",
			MissionID:      request.MissionID,
			AgentProfileID: entry.AgentProfileID,
			AssignmentRefs: []AssignmentRef{{ID: "assignment-001", Role: MissionRoleWorker, Status: AssignmentStatusActive}},
		}},
	}

	eval := policy.Evaluate(request, entry, team)
	if !hasPolicyCode(eval.RejectionReasons, CodeDuplicateAssignment) {
		t.Fatalf("expected duplicate assignment rejection, got %#v", eval)
	}

	request.IdempotencyKey = "assignment-001"
	eval = policy.Evaluate(request, entry, team)
	if hasPolicyCode(eval.RejectionReasons, CodeDuplicateAssignment) {
		t.Fatalf("same idempotency key should not be duplicate conflict: %#v", eval)
	}
}

func validStaffingRequest() StaffingRequest {
	return StaffingRequest{
		ID:                       "request-001",
		MissionID:                missioncore.MissionID("mission-001"),
		RequestedBy:              ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent},
		CapabilityGap:            "backend implementation",
		DesiredRoles:             []MissionRole{MissionRoleWorker},
		RequiredSkills:           []agentcore.SkillRef{{SkillID: "go"}, {SkillID: "testing"}},
		RequiredTools:            []agentcore.ToolRef{{ToolID: "git"}},
		AuthorityRequirements:    "repo:write",
		AvailabilityWindow:       AvailabilityWindow{From: time.Now().Add(-time.Minute), Until: time.Now().Add(time.Hour)},
		BudgetConstraints:        BudgetConstraints{MaxCostPerHour: 50},
		QualityConstraints:       QualityConstraints{MinScore: 0.8, MinSampleSize: 5},
		RiskConstraints:          RiskConstraints{AllowedLevels: []agentcore.RiskLevel{agentcore.RiskLevelLow, agentcore.RiskLevelMedium}},
		AllowUnknownAvailability: false,
		AllowAI:                  true,
		Status:                   StaffingRequestStatusEvaluating,
		CreatedAt:                time.Now().UTC(),
	}
}

func validDirectoryEntry() agentcore.AgentDirectoryEntry {
	now := time.Now().UTC()
	return agentcore.AgentDirectoryEntry{
		AgentProfileID:  "profile-001",
		AgentInstanceID: "instance-001",
		RealName:        "Ada Lovelace",
		DisplayName:     "Ada",
		RoleName:        "Worker",
		AgentKind:       agentcore.AgentKindAI,
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef:   agentcore.OrganizationRef{ID: "orrery"},
			DepartmentRef:     agentcore.DepartmentRef{ID: "engineering"},
			PositionRef:       agentcore.PositionRef{ID: "backend"},
			RoleRefs:          []agentcore.RoleRef{{ID: "worker"}},
			AuthorityScope:    "repo:write",
			EmploymentType:    agentcore.EmploymentTypeAIAgent,
			SupervisionPolicy: agentcore.SupervisionPolicyRoot,
		},
		SkillRefs:          []agentcore.SkillRef{{SkillID: "go"}, {SkillID: "testing"}},
		ToolRefs:           []agentcore.ToolRef{{ToolID: "git"}},
		RuntimeSpecSummary: agentcore.RuntimeSpecSummary{Kind: agentcore.RuntimeKindCodex},
		MemoryProfileRef:   agentcore.MemoryProfileRef{MemoryProfileID: "memory-001", MemoryKind: agentcore.MemoryKindPersonal},
		Availability: agentcore.AgentAvailability{
			AgentProfileID: "profile-001", AgentInstanceID: "instance-001",
			Status: agentcore.AvailabilityAvailable, AvailableFrom: now.Add(-time.Hour), AvailableUntil: now.Add(time.Hour), Capacity: 4, CurrentLoad: 1,
		},
		CostProfile:   agentcore.AgentCostProfile{AgentProfileID: "profile-001", EstimatedCostPerHour: 10},
		QualitySignal: agentcore.AgentQualitySignal{AgentProfileID: "profile-001", Score: 0.95, SampleSize: 10},
		RiskProfile:   agentcore.AgentRiskProfile{AgentProfileID: "profile-001", RiskLevel: agentcore.RiskLevelLow},
		Enabled:       true,
		UpdatedAt:     now,
	}
}

func hasPolicyCode(codes []PolicyCode, want PolicyCode) bool {
	for _, code := range codes {
		if code == want {
			return true
		}
	}
	return false
}
