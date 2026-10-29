package operations_test

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
)

func TestCommandServiceRejectsInvalidWorkforceProfileBeforeSave(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "operator", DepartmentID: "operations", Name: "Operator"},
	})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	_, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-ops",
		RealName:       "Ops Agent",
		AgentKind:      "ai",
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		RuntimeKind:    "codex",
		Enabled:        true,
	})
	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("UpsertWorkforceMember error = %v, want ErrInvalidRequest", err)
	}
	if len(store.profiles) != 0 || len(store.availability) != 0 {
		t.Fatalf("invalid profile should not be saved: profiles=%+v availability=%+v", store.profiles, store.availability)
	}
}

func TestCommandServiceRejectsWorkforceMemberOutsideOrganizationTree(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	if err := orgs.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: "org-1", Name: "Orrery", DefaultDepartmentID: "dept-ops", DefaultPositionID: "pos-operator"}); err != nil {
		t.Fatalf("seed organization: %v", err)
	}
	if err := orgs.SaveDepartment(ctx, operations.DepartmentDTO{DepartmentID: "dept-ops", OrganizationID: "org-1", Name: "Operations"}); err != nil {
		t.Fatalf("seed ops department: %v", err)
	}
	if err := orgs.SaveDepartment(ctx, operations.DepartmentDTO{DepartmentID: "dept-ai", OrganizationID: "org-1", Name: "AI"}); err != nil {
		t.Fatalf("seed ai department: %v", err)
	}
	if err := orgs.SavePosition(ctx, operations.PositionDTO{PositionID: "pos-operator", DepartmentID: "dept-ops", Name: "Operator"}); err != nil {
		t.Fatalf("seed operator position: %v", err)
	}
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	created, err := command.CreateWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		RealName:       "Ops Agent",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "org-1",
		DepartmentID:   "dept-ops",
		PositionID:     "pos-operator",
		RuntimeKind:    "codex",
		Enabled:        true,
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Handles operations work."},
	})
	if err != nil {
		t.Fatalf("valid CreateWorkforceMember: %v", err)
	}
	if created.DepartmentID != "dept-ops" || len(store.profiles) != 1 {
		t.Fatalf("valid member not saved against org tree: member=%+v profiles=%+v", created, store.profiles)
	}

	_, err = command.CreateWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		RealName:       "Research Agent",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "org-1",
		DepartmentID:   "dept-ai",
		PositionID:     "pos-operator",
		RuntimeKind:    "codex",
		Enabled:        true,
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Handles research work."},
	})
	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("CreateWorkforceMember error = %v, want ErrInvalidRequest", err)
	}
	if len(store.profiles) != 1 {
		t.Fatalf("member outside org tree should not be saved: %+v", store.profiles)
	}
}

func TestCommandServiceRequiresOrganizationCatalogForWorkforceMemberWrites(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	command := operations.NewCommandService(store)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	_, err := command.CreateWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		RealName:       "Ops Agent",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "org-1",
		DepartmentID:   "dept-ops",
		PositionID:     "pos-operator",
		RuntimeKind:    "codex",
		Enabled:        true,
	})
	if !errors.Is(err, operations.ErrServiceDependency) {
		t.Fatalf("CreateWorkforceMember error = %v, want ErrServiceDependency", err)
	}
	if len(store.profiles) != 0 {
		t.Fatalf("member should not be saved without organization catalog: %+v", store.profiles)
	}
}

func TestCommandServiceRequiresOrganizationCatalogForOrganizationWrites(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	command := operations.NewCommandService(store)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage}}

	_, err := command.CreateOrganization(ctx, viewer, operations.CreateOrganizationCommand{
		Name:                  "Platform",
		DefaultDepartmentName: "Operations",
		DefaultPositionName:   "Operator",
	})
	if !errors.Is(err, operations.ErrServiceDependency) {
		t.Fatalf("CreateOrganization error = %v, want ErrServiceDependency", err)
	}

	_, err = command.CreateOrganizationUnit(ctx, viewer, operations.CreateOrganizationUnitCommand{
		OrganizationID:      "org-1",
		Name:                "Research",
		DefaultPositionName: "Researcher",
	})
	if !errors.Is(err, operations.ErrServiceDependency) {
		t.Fatalf("CreateOrganizationUnit error = %v, want ErrServiceDependency", err)
	}
}

func TestCommandServiceRejectsOrganizationUnitOutsideCatalog(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{
		{DepartmentID: "dept-ops", OrganizationID: "org-1", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "pos-operator", DepartmentID: "dept-ops", Name: "Operator"},
	})
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionOrgManage}}

	_, err := command.CreateOrganizationUnit(ctx, viewer, operations.CreateOrganizationUnitCommand{
		OrganizationID:      "org-missing",
		Name:                "Research",
		DefaultPositionName: "Researcher",
	})
	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("CreateOrganizationUnit missing organization error = %v, want ErrInvalidRequest", err)
	}

	_, err = command.CreateOrganizationUnit(ctx, viewer, operations.CreateOrganizationUnitCommand{
		OrganizationID:      "org-1",
		Name:                "Research",
		ParentDepartmentID:  "dept-missing",
		DefaultPositionName: "Researcher",
	})
	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("CreateOrganizationUnit missing parent error = %v, want ErrInvalidRequest", err)
	}
}

func TestCommandServiceOrganizationWritesRequireManagePermissionBeforeDependencies(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	command := operations.NewCommandService(store)
	viewer := operations.ViewerRef{ViewerID: "reader", PermissionScope: []string{operations.PermissionOrgRead}}

	_, err := command.CreateOrganization(ctx, viewer, operations.CreateOrganizationCommand{
		Name:                  "Platform",
		DefaultDepartmentName: "Operations",
		DefaultPositionName:   "Operator",
	})
	if !errors.Is(err, operations.ErrPermissionDenied) {
		t.Fatalf("CreateOrganization error = %v, want ErrPermissionDenied", err)
	}

	_, err = command.CreateOrganizationUnit(ctx, viewer, operations.CreateOrganizationUnitCommand{
		OrganizationID:      "org-1",
		Name:                "Research",
		DefaultPositionName: "Researcher",
	})
	if !errors.Is(err, operations.ErrPermissionDenied) {
		t.Fatalf("CreateOrganizationUnit error = %v, want ErrPermissionDenied", err)
	}
}

func TestCommandServiceWorkforceWritesRequireManagePermission(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceRead}}

	_, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-ops",
		RealName:       "Ops Agent",
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		Enabled:        true,
	})
	if !errors.Is(err, operations.ErrPermissionDenied) {
		t.Fatalf("UpsertWorkforceMember error = %v, want ErrPermissionDenied", err)
	}
	if len(store.profiles) != 0 {
		t.Fatalf("profile should not be saved without manage permission: %+v", store.profiles)
	}
}
