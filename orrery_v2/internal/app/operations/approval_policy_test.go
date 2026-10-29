package operations

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestApproverResolverUsesCurrentOrgOwner(t *testing.T) {
	graph := approvalPolicyTestGraph()
	req := ApprovalRequirementDTO{
		RequirementID:    "req-1",
		PolicyID:         "risk:high",
		ApprovalType:     "hil",
		OrganizationCode: "01.01",
		ApproverType:     "org_owner",
		Reason:           "high risk tool call",
	}

	approver, reason, err := ResolveApprover(req, graph, nil, "high")
	if err != nil {
		t.Fatalf("ResolveApprover: %v", err)
	}
	if approver != (ActorRefDTO{ActorType: "human", ActorID: "dept-owner"}) {
		t.Fatalf("approver = %+v, want dept owner", approver)
	}
	if reason == "" {
		t.Fatalf("reason should explain owner resolution")
	}
}

func TestApproverResolverFallsBackToAncestorOwner(t *testing.T) {
	graph := approvalPolicyTestGraph()
	graph.Departments[0].OwnerProfileID = ""
	graph.Departments[0].OwnerEmployeeNo = ""
	graph.Departments[0].OwnerName = ""

	approver, reason, err := ResolveApprover(ApprovalRequirementDTO{
		RequirementID:    "req-1",
		PolicyID:         "risk:high",
		ApprovalType:     "hil",
		OrganizationCode: "01.01",
		ApproverType:     "org_owner",
		Reason:           "high risk tool call",
	}, graph, nil, "high")
	if err != nil {
		t.Fatalf("ResolveApprover: %v", err)
	}
	if approver.ActorID != "root-owner" {
		t.Fatalf("approver = %+v, want ancestor owner", approver)
	}
	if reason == "" {
		t.Fatalf("reason should explain ancestor fallback")
	}
}

func TestApproverResolverUsesOrgRole(t *testing.T) {
	graph := approvalPolicyTestGraph()

	approver, reason, err := ResolveApprover(ApprovalRequirementDTO{
		RequirementID:    "req-1",
		PolicyID:         "approval:reviewer",
		ApprovalType:     "approval",
		OrganizationCode: "01.01",
		ApproverType:     "org_role",
		ApproverRef:      "approval.reviewer",
		Reason:           "business approval",
	}, graph, []RoleAssignmentDTO{{
		AssignmentID:       "reviewer-1",
		Actor:              ActorRefDTO{ActorType: "human", ActorID: "reviewer-1"},
		RoleID:             "approval.reviewer",
		OrganizationNodeID: "dept-1",
		OrganizationCode:   "01.01",
		ScopeMode:          RoleScopeSelf,
		Source:             RoleSourceDirect,
	}}, "medium")
	if err != nil {
		t.Fatalf("ResolveApprover: %v", err)
	}
	if approver.ActorID != "reviewer-1" {
		t.Fatalf("approver = %+v, want role assignee", approver)
	}
	if reason == "" {
		t.Fatalf("reason should explain role resolution")
	}
}

func TestApproverResolverUsesAdminFallbackForLowRisk(t *testing.T) {
	graph := approvalPolicyTestGraph()
	graph.Organizations[0].OwnerProfileID = ""
	graph.Departments[0].OwnerProfileID = ""

	approver, reason, err := ResolveApprover(ApprovalRequirementDTO{
		RequirementID:    "req-1",
		PolicyID:         "risk:medium",
		ApprovalType:     "approval",
		OrganizationCode: "01.01",
		ApproverType:     "org_owner",
		Reason:           "medium risk request",
	}, graph, nil, "medium")
	if err != nil {
		t.Fatalf("ResolveApprover: %v", err)
	}
	if approver != (ActorRefDTO{ActorType: "org_role", ActorID: "org.admin"}) {
		t.Fatalf("approver = %+v, want org admin fallback", approver)
	}
	if reason == "" {
		t.Fatalf("reason should explain admin fallback")
	}
}

func TestApproverResolverDeniesHighRiskWithoutApprover(t *testing.T) {
	graph := approvalPolicyTestGraph()
	graph.Organizations[0].OwnerProfileID = ""
	graph.Departments[0].OwnerProfileID = ""

	_, _, err := ResolveApprover(ApprovalRequirementDTO{
		RequirementID:    "req-1",
		PolicyID:         "risk:high",
		ApprovalType:     "hil",
		OrganizationCode: "01.01",
		ApproverType:     "org_owner",
		Reason:           "high risk request",
	}, graph, nil, "high")
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("err = %v, want ErrPermissionDenied", err)
	}
}

func TestApproveOnceCreatesOneCallEffect(t *testing.T) {
	req := approvalPolicyDecisionRequest()

	decision := ApprovalDecisionEffect("approve_once", req)

	if decision.Decision != PermissionDecisionAllow {
		t.Fatalf("decision = %+v, want allow", decision)
	}
	if decision.ExpiresAt.IsZero() {
		t.Fatalf("approve_once should carry an expiration boundary")
	}
	if got := decisionSourceByType(decision.Sources, permissionDecisionSourceApproval); got.SourceType == "" || got.Effect != PermissionDecisionAllow {
		t.Fatalf("approval source = %+v", decision.Sources)
	}
	if got := decisionSourceByType(decision.Sources, permissionDecisionSourceGrant); got.SourceType != "" {
		t.Fatalf("approve_once must not create a broad temporary grant source: %+v", decision.Sources)
	}
}

func TestApproveOnceEffectIDBindsActionFingerprint(t *testing.T) {
	req := approvalPolicyDecisionRequest()
	req.Action = ""
	req.ActionTypeID = "mcp.tool.call"
	req.OnBehalfOf = ActorRefDTO{ActorType: "human", ActorID: "human-ops"}
	req.RuntimeRef = PermissionRuntimeRefDTO{RuntimeKind: "mcp_gateway", RuntimeID: "gateway-1"}
	req.ObjectRefs = []PermissionObjectRefDTO{{ResourceType: "tool", ResourceID: "tool.deploy"}}
	req.RequestedFields = []string{"payload", "result"}
	req.ArgsHash = "sha256:deploy-prod"

	decision := ApprovalDecisionEffect("approve_once", req)
	source := decisionSourceByType(decision.Sources, permissionDecisionSourceApproval)

	if source.SourceType == "" {
		t.Fatalf("approval source = %+v", decision.Sources)
	}
	for _, want := range []string{"mcp.tool.call", "human-ops", "sha256_deploy-prod", "tool_tool.deploy"} {
		if !strings.Contains(source.SourceID, want) {
			t.Fatalf("approval source id = %q, want fingerprint segment %q", source.SourceID, want)
		}
	}
}

func TestApproveSessionCreatesScopedTemporaryGrant(t *testing.T) {
	req := approvalPolicyDecisionRequest()

	decision := ApprovalDecisionEffect("approve_session", req)

	if decision.Decision != PermissionDecisionAllow {
		t.Fatalf("decision = %+v, want allow", decision)
	}
	if decision.ExpiresAt.IsZero() || !decision.ExpiresAt.After(time.Now().UTC()) {
		t.Fatalf("approve_session should expire in the future: %+v", decision)
	}
	source := decisionSourceByType(decision.Sources, permissionDecisionSourceGrant)
	if source.SourceType == "" || source.ResourceID != "tool.deploy" || source.OrganizationCode != "0101" {
		t.Fatalf("temporary grant source = %+v, all sources %+v", source, decision.Sources)
	}
	if source.Reason == "" {
		t.Fatalf("temporary grant source should explain scope")
	}
}

func approvalPolicyTestGraph() OrgGraphDTO {
	return OrgGraphDTO{
		Organizations: []OrganizationDTO{{
			OrganizationID: "org-1",
			Name:           "Orrery",
			Code:           "01",
			Status:         organizationStatusActive,
			OwnerProfileID: "root-owner",
			OwnerName:      "Root Owner",
			NodeType:       organizationNodeTypeCompany,
		}},
		Departments: []DepartmentDTO{{
			DepartmentID:       "dept-1",
			OrganizationID:     "org-1",
			Name:               "Platform",
			Code:               "0101",
			Status:             organizationStatusActive,
			OwnerProfileID:     "dept-owner",
			OwnerName:          "Dept Owner",
			NodeType:           organizationNodeTypeDepartment,
			ParentDepartmentID: "",
		}},
	}
}

func approvalPolicyDecisionRequest() PermissionDecisionRequestDTO {
	return PermissionDecisionRequestDTO{
		Subject:          ActorRefDTO{ActorType: "agent", ActorID: "agent-ops"},
		OnBehalfOf:       ActorRefDTO{ActorType: "human", ActorID: "human-ops"},
		Action:           "tool.call",
		ResourceType:     "tool",
		ResourceID:       "tool.deploy",
		OrganizationCode: "01.01",
		MissionID:        "mission-1",
		TaskID:           "task-1",
		RiskLevel:        "high",
		Metadata:         map[string]string{"scope": "deploy:prod", "session_id": "session-1"},
	}
}

func decisionSourceByType(sources []PermissionDecisionSourceDTO, sourceType string) PermissionDecisionSourceDTO {
	for _, source := range sources {
		if source.SourceType == sourceType {
			return source
		}
	}
	return PermissionDecisionSourceDTO{}
}
