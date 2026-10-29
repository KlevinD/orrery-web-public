package operations

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/rebac"
)

func TestPermissionDecisionAllowsPlatformRoleWithoutOrganizationCode(t *testing.T) {
	ctx := context.Background()
	service, store := newPermissionDecisionService(t, ctx)
	if err := store.SaveRoleDefinition(ctx, RoleDefinitionDTO{
		RoleID:               "marketing.mcp.operator",
		Name:                 "Marketing MCP Operator",
		Category:             "platform",
		Status:               organizationStatusActive,
		AssignableActorTypes: []string{"agent"},
		PermissionRefs:       []string{"mcp.tool.call"},
	}); err != nil {
		t.Fatalf("save role definition: %v", err)
	}
	if err := store.SaveRoleAssignment(ctx, RoleAssignmentDTO{
		AssignmentID: "assign-marketing-mcp",
		Actor:        ActorRefDTO{ActorType: "agent", ActorID: "human-ding"},
		RoleID:       "marketing.mcp.operator",
		ScopeMode:    RoleScopePlatform,
		Source:       RoleSourceDirect,
	}); err != nil {
		t.Fatalf("save role assignment: %v", err)
	}
	savePermissionDecisionRelation(t, ctx, service, rebac.RelationTuple{
		ID:        "rel-marketing-mcp-editor",
		TenantID:  "default",
		Subject:   rebac.Ref{Kind: "human", ID: "ding"},
		Relation:  "editor",
		Object:    rebac.Ref{Kind: "tool", ID: "mcp.marketing.marketing_wechat_publish_intent_prepare"},
		CreatedAt: time.Now().UTC(),
	})

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:      ActorRefDTO{ActorType: "agent", ActorID: "human-ding"},
		OnBehalfOf:   ActorRefDTO{ActorType: "human", ActorID: "ding"},
		ActionTypeID: "mcp.tool.call",
		ResourceType: "tool",
		ResourceID:   "mcp.marketing.marketing_wechat_publish_intent_prepare",
		ArgsHash:     "sha256:prepare-r3",
		Metadata:     map[string]string{"resource_binding": "allow"},
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, PermissionDecisionAllow, permissionDecisionSourceRole)
	assertPermissionDecision(t, decision, PermissionDecisionAllow, permissionDecisionSourceReBAC)
}

func TestPermissionDecisionDoesNotApplyOrganizationRoleWithoutOrganizationCode(t *testing.T) {
	ctx := context.Background()
	service, store := newPermissionDecisionService(t, ctx)
	if err := store.SaveRoleDefinition(ctx, RoleDefinitionDTO{
		RoleID:               "marketing.org.operator",
		Name:                 "Marketing Organization Operator",
		Category:             "resource",
		Status:               organizationStatusActive,
		AssignableActorTypes: []string{"agent"},
		PermissionRefs:       []string{"mcp.tool.call"},
	}); err != nil {
		t.Fatalf("save role definition: %v", err)
	}
	if err := store.SaveRoleAssignment(ctx, RoleAssignmentDTO{
		AssignmentID:       "assign-marketing-org",
		Actor:              ActorRefDTO{ActorType: "agent", ActorID: "human-ding"},
		RoleID:             "marketing.org.operator",
		OrganizationNodeID: "org-1",
		OrganizationCode:   "01",
		ScopeMode:          RoleScopeSelf,
		Source:             RoleSourceDirect,
	}); err != nil {
		t.Fatalf("save role assignment: %v", err)
	}

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:      ActorRefDTO{ActorType: "agent", ActorID: "human-ding"},
		OnBehalfOf:   ActorRefDTO{ActorType: "human", ActorID: "ding"},
		ActionTypeID: "mcp.tool.call",
		ResourceType: "tool",
		ResourceID:   "mcp.marketing.marketing_wechat_publish_intent_prepare",
		ArgsHash:     "sha256:prepare-r3",
		Metadata:     map[string]string{"resource_binding": "allow"},
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}

	assertPermissionDecision(t, decision, PermissionDecisionDeny, permissionDecisionSourceMissing)
}
