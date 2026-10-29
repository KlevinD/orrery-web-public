package main

import (
	"context"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/hil"
)

func TestProductionApprovalRoutingBindsSharedTemplateToRequesterOrganization(t *testing.T) {
	profile := func(id, tenant string, kind agentcore.AgentKind) agentcore.AgentProfile {
		return agentcore.AgentProfile{ID: agentcore.AgentProfileID(id), Enabled: true, AgentKind: kind,
			OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: agentcore.OrganizationID(tenant)}}}
	}
	requester := profile("worker", "tenant-a", agentcore.AgentKindAI)
	requester.OrgIdentity.ManagerAgentRef = agentcore.AgentProfileRef{ID: "manager"}
	directory := &recordingProductionHILProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"worker": requester, "manager": profile("manager", "tenant-a", agentcore.AgentKindHuman),
		"foreign-human": profile("foreign-human", "tenant-b", agentcore.AgentKindHuman),
	}}
	resolver := productionHILAssigneeResolver{profiles: directory}
	request := hil.WorkflowAssigneeRequest{TenantID: productionHILTenantID,
		Requester: hil.ActorRef{Kind: hil.ActorAgent, ID: "worker"},
		Selector:  hil.WorkflowActorSelector{Kind: hil.WorkflowSelectorRequesterManager}}
	actors, err := resolver.ResolveWorkflowAssignees(context.Background(), request)
	if err != nil || len(actors) != 1 || actors[0].ID != "manager" {
		t.Fatalf("same-organization manager routing: actors=%+v err=%v", actors, err)
	}
	for _, selector := range []hil.WorkflowActorSelector{
		{Kind: hil.WorkflowSelectorHuman, ID: "foreign-human"},
		{Kind: hil.WorkflowSelectorResourceOwner},
		{Kind: hil.WorkflowSelectorOntologyRecommended},
	} {
		request.Selector = selector
		request.Resource = hil.ObjectRef{Kind: "human", ID: "foreign-human"}
		request.RecommendedActors = []hil.ActorRef{{Kind: hil.ActorHuman, ID: "foreign-human"}}
		if _, err := resolver.ResolveWorkflowAssignees(context.Background(), request); err == nil || !strings.Contains(err.Error(), "approval_assignee_tenant_mismatch") {
			t.Fatalf("shared template accepted foreign assignee for %s: %v", selector.Kind, err)
		}
	}
	directory.page = agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID: "manager", AgentKind: agentcore.AgentKindHuman, Enabled: true, OrgIdentity: directory.profiles["manager"].OrgIdentity,
	}}}
	request.Selector = hil.WorkflowActorSelector{Kind: hil.WorkflowSelectorPeopleRole, ID: productionApprovalReviewerRoleID}
	if _, err := resolver.ResolveWorkflowAssignees(context.Background(), request); err != nil || directory.query.Organization != "tenant-a" {
		t.Fatalf("shared role lookup must use requester organization: query=%+v err=%v", directory.query, err)
	}
}

func TestProductionApprovalRoutingRejectsInvalidRequesterWithoutGlobalFallback(t *testing.T) {
	requester := agentcore.AgentProfile{ID: "repair-agent", AgentKind: agentcore.AgentKindAI, Enabled: true,
		OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "global"}, ManagerAgentRef: agentcore.AgentProfileRef{ID: "H000001"}}}
	directory := &recordingProductionHILProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"repair-agent": requester,
		"H000001": {ID: "H000001", Enabled: true, AgentKind: agentcore.AgentKindHuman,
			OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "minvia"}}},
	}}
	resolver := productionHILAssigneeResolver{profiles: directory}
	request := hil.WorkflowAssigneeRequest{TenantID: "global", Requester: hil.ActorRef{Kind: hil.ActorAgent, ID: "repair-agent"},
		Selector: hil.WorkflowActorSelector{Kind: hil.WorkflowSelectorRequesterManager}}
	for _, tenant := range []string{"global", productionHILTenantID} {
		request.TenantID = tenant
		if _, err := resolver.ResolveWorkflowAssignees(context.Background(), request); err == nil || !strings.Contains(err.Error(), "approval_assignee_tenant_mismatch") {
			t.Fatalf("foreign manager accepted in %s: %v", tenant, err)
		}
	}
	request.TenantID = "minvia"
	if _, err := resolver.ResolveWorkflowAssignees(context.Background(), request); err == nil || !strings.Contains(err.Error(), "approval_requester_tenant_mismatch") {
		t.Fatalf("foreign requester accepted: %v", err)
	}
	request.TenantID = "global"
	requester.Enabled = false
	directory.profiles["repair-agent"] = requester
	if _, err := resolver.ResolveWorkflowAssignees(context.Background(), request); err == nil || !strings.Contains(err.Error(), "approval_requester_unavailable") {
		t.Fatalf("disabled requester accepted: %v", err)
	}
	request.Requester.ID = "missing"
	if _, err := resolver.ResolveWorkflowAssignees(context.Background(), request); err == nil || !strings.Contains(err.Error(), "approval_requester_unavailable") {
		t.Fatalf("missing requester accepted: %v", err)
	}
	if productionHILRoutingTenantAllowsProfile(productionHILTenantID, directory.profiles["H000001"].OrgIdentity) ||
		productionHILRoutingTenantAllowsProfile("global", agentcore.AgentOrgIdentity{}) {
		t.Fatal("system namespace or an unowned profile became an organization wildcard")
	}
}

func TestProductionApprovalServiceIdentityRetainsOnlyItsBoundTenantOrLegacyContract(t *testing.T) {
	directory := &recordingProductionHILProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		productionMarketingApproverID: {ID: agentcore.AgentProfileID(productionMarketingApproverID), Enabled: true, AgentKind: agentcore.AgentKindHuman,
			OrgIdentity: agentcore.AgentOrgIdentity{OrganizationRef: agentcore.OrganizationRef{ID: "minvia"}}},
	}}
	resolver := productionHILAssigneeResolver{profiles: directory}
	request := hil.WorkflowAssigneeRequest{TenantID: productionHILTenantID,
		Requester:    hil.ActorRef{Kind: hil.ActorService, ID: "marketing"},
		ActionTypeID: hil.MarketingWechatDraftApprovalActionType, TemplateID: productionMarketingWorkflowTemplateID,
		Resource: hil.ObjectRef{Kind: hil.MarketingWechatGenerationResourceKind, ID: "frozen-generation"},
		Selector: hil.WorkflowActorSelector{Kind: hil.WorkflowSelectorHuman, ID: productionMarketingApproverID}}
	actors, err := resolver.ResolveWorkflowAssignees(context.Background(), request)
	if err != nil || len(actors) != 1 || actors[0].ID != productionMarketingApproverID {
		t.Fatalf("existing fixed-owner service contract was lost: actors=%+v err=%v", actors, err)
	}
	for _, mutation := range []string{"agent", "other-service", "other-action", "other-template", "other-resource", "role", "global"} {
		t.Run(mutation, func(t *testing.T) {
			other := request
			switch mutation {
			case "agent":
				other.Requester.Kind = hil.ActorAgent
			case "other-service":
				other.Requester.ID = "repair-service"
			case "other-action":
				other.ActionTypeID = "business_approval"
			case "other-template":
				other.TemplateID = productionBusinessWorkflowTemplateID
			case "other-resource":
				other.Resource.Kind = "scheduled_repair_case"
			case "role":
				other.Selector = hil.WorkflowActorSelector{Kind: hil.WorkflowSelectorPeopleRole, ID: productionApprovalReviewerRoleID}
			case "global":
				other.TenantID = "global"
			}
			if _, err := resolver.ResolveWorkflowAssignees(context.Background(), other); err == nil {
				t.Fatalf("service contract broadened through %s", mutation)
			}
		})
	}
	request.TenantID, request.Requester.ID = "minvia", "responsibility-autoplan"
	request.TemplateID = "tenant-owned-template"
	if _, err := resolver.ResolveWorkflowAssignees(context.Background(), request); err != nil {
		t.Fatalf("tenant-bound service was required to impersonate a personnel profile: %v", err)
	}
}
