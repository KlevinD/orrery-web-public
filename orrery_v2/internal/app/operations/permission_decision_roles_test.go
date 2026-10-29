package operations

import (
	"context"
	"testing"
)

func TestPermissionDecisionAllowsPlatformRoleWithoutOrganization(t *testing.T) {
	ctx := context.Background()
	service, store := newPermissionDecisionService(t, ctx)
	if err := store.SaveRoleAssignment(ctx, RoleAssignmentDTO{
		AssignmentID: "assign-platform-reviewer",
		Actor:        ActorRefDTO{ActorType: "human", ActorID: "human-ding"},
		RoleID:       "approval.reviewer",
		ScopeMode:    RoleScopePlatform,
		Source:       RoleSourceDirect,
	}); err != nil {
		t.Fatalf("save assignment: %v", err)
	}

	decision, err := service.DecidePermission(ctx, permissionDecisionViewer(), PermissionDecisionRequestDTO{
		Subject:      ActorRefDTO{ActorType: "human", ActorID: "human-ding"},
		Action:       "approvals.decide",
		ResourceType: "platform",
		ResourceID:   "global",
	})
	if err != nil {
		t.Fatalf("DecidePermission: %v", err)
	}
	assertPermissionDecision(t, decision, PermissionDecisionAllow, permissionDecisionSourceRole)
}
