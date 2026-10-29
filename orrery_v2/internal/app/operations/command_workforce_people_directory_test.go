package operations_test

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
)

func TestCommandServiceFeishuDirectoryPersistsPeopleSourceFacts(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	service.SetWorkforceSessionRevoker(&fakeWorkforceSessionRevoker{})
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"}}, []operations.PositionDTO{{PositionID: "operator", DepartmentID: "operations", Name: "Operator"}})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "system:people-feishu-directory", ViewerKind: "system", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "human-quzheng",
		DisplayName:    "屈正",
		AgentKind:      "human",
		UserAccount: operations.WorkforceUserAccountDTO{
			Username: "quzheng", AccountEnabled: true, Password: "initial-password-1",
		},
		OrganizationID: "orrery", DepartmentID: "operations", PositionID: "operator",
		Persona: operations.WorkforcePersonaDTO{Backstory: "People managed employee."}, Enabled: true,
	}); err != nil {
		t.Fatalf("create existing human: %v", err)
	}

	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "human-quzheng",
		DisplayName:    "屈正",
		Email:          "quzheng@example.com",
		PersonSource: operations.WorkforcePersonSourceFactsDTO{
			EmployeeNumber: " E-001 ", Source: " people.feishu ", SourceRecordID: " ou_123 ", SourceTenantID: " tenant-1 ",
		},
		Enabled:    true,
		UpdateMask: []string{"display_name", "real_name", "email", "person_source", "enabled"},
	}); err != nil {
		t.Fatalf("apply Feishu directory update: %v", err)
	}

	profile := store.profiles["human-quzheng"]
	if profile.ID != "human-quzheng" || profile.PersonSource.EmployeeNumber != "E-001" || profile.PersonSource.Source != "people.feishu" || profile.PersonSource.SourceRecordID != "ou_123" || profile.PersonSource.SourceTenantID != "tenant-1" {
		t.Fatalf("People source facts = %+v profile=%+v", profile.PersonSource, profile)
	}
}

func TestCommandServiceFeishuDirectoryPreservesPeopleAuthoritativeSourceFacts(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	service.SetWorkforceSessionRevoker(&fakeWorkforceSessionRevoker{})
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"}}, []operations.PositionDTO{{PositionID: "operator", DepartmentID: "operations", Name: "Operator"}})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "system:people-feishu-directory", ViewerKind: "system", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "H000001", DisplayName: "Ding Pi", AgentKind: "human",
		UserAccount:    operations.WorkforceUserAccountDTO{Username: "ding", AccountEnabled: true, Password: "initial-password-1"},
		OrganizationID: "orrery", DepartmentID: "operations", PositionID: "operator",
		PersonSource: operations.WorkforcePersonSourceFactsDTO{
			EmployeeNumber: "H000001", Source: "people", SourceRecordID: "member-dingpi", SourceTenantID: "tenant-preview",
		},
		Persona: operations.WorkforcePersonaDTO{Backstory: "People managed employee."}, Enabled: true,
	}); err != nil {
		t.Fatalf("create People-authoritative human: %v", err)
	}

	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "H000001", DisplayName: "Ding Pi", Email: "ding@example.com",
		PersonSource: operations.WorkforcePersonSourceFactsDTO{
			EmployeeNumber: "legacy-feishu-number", Source: "people.feishu", SourceRecordID: "ou_ding", SourceTenantID: "tenant-preview",
		},
		Enabled: true, UpdateMask: []string{"display_name", "real_name", "email", "person_source", "enabled"},
	}); err != nil {
		t.Fatalf("apply Feishu directory update: %v", err)
	}

	got := store.profiles["H000001"].PersonSource
	if got.EmployeeNumber != "H000001" || got.Source != "people" || got.SourceRecordID != "member-dingpi" || got.SourceTenantID != "tenant-preview" {
		t.Fatalf("People-authoritative source facts were overwritten: %+v", got)
	}
}
