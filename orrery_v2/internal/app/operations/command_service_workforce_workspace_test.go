package operations_test

import (
	"context"
	"path/filepath"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
)

func TestCommandServiceManagesWorkforceWorkspaceRoot(t *testing.T) {
	ctx := context.Background()
	workspaceBase := t.TempDir()
	workspaceRoot := filepath.Join(workspaceBase, "profile-workspace")
	updatedWorkspaceRoot := filepath.Join(workspaceBase, "profile-workspace-v2")
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "operator", DepartmentID: "operations", Name: "Operator"},
	})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage},
	}

	created, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID:    "profile-workspace",
		DisplayName:       "Workspace Agent",
		AgentKind:         "ai",
		EmploymentType:    "ai_agent",
		OrganizationID:    "orrery",
		DepartmentID:      "operations",
		PositionID:        "operator",
		SupervisionPolicy: "root",
		WorkspaceRoot:     workspaceRoot,
		Persona:           operations.WorkforcePersonaDTO{ResponsibilityBoundary: "负责工作区内的任务执行"},
		Enabled:           true,
	})
	if err != nil {
		t.Fatalf("create workforce member: %v", err)
	}
	if created.WorkspaceRoot != workspaceRoot || store.profiles["profile-workspace"].WorkspaceRoot != created.WorkspaceRoot {
		t.Fatalf("workspace root was not saved and returned: profile=%q dto=%q", store.profiles["profile-workspace"].WorkspaceRoot, created.WorkspaceRoot)
	}

	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-workspace",
		DisplayName:    "Workspace Agent Updated",
		UpdateMask:     []string{"display_name"},
	}); err != nil {
		t.Fatalf("patch display name: %v", err)
	}
	if got := store.profiles["profile-workspace"].WorkspaceRoot; got != workspaceRoot {
		t.Fatalf("omitted workspace_root was changed to %q", got)
	}

	updated, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-workspace",
		WorkspaceRoot:  updatedWorkspaceRoot,
		UpdateMask:     []string{"workspace_root"},
	})
	if err != nil {
		t.Fatalf("patch workspace root: %v", err)
	}
	if updated.WorkspaceRoot != updatedWorkspaceRoot {
		t.Fatalf("patched workspace root = %q", updated.WorkspaceRoot)
	}
}
