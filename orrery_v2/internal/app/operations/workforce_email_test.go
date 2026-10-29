package operations_test

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
)

func TestCommandServicePersistsAndPartiallyUpdatesAgentMailbox(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{{DepartmentID: "ops", OrganizationID: "org-1", Name: "Operations"}}, []operations.PositionDTO{{PositionID: "operator", DepartmentID: "ops", Name: "Operator"}})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	created, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-mail", DisplayName: "Mail Agent", Email: "mail.agent@example.test", AgentKind: "ai", EmploymentType: "ai_agent",
		OrganizationID: "org-1", DepartmentID: "ops", PositionID: "operator",
		Persona: operations.WorkforcePersonaDTO{ResponsibilityBoundary: "负责发送经过授权的业务邮件。"}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember: %v", err)
	}
	if created.Email != "mail.agent@example.test" || store.profiles["profile-mail"].Email != created.Email {
		t.Fatalf("created=%+v profile=%+v", created, store.profiles["profile-mail"])
	}

	updated, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-mail", Email: "new.mail.agent@example.test", UpdateMask: []string{"email"},
	})
	if err != nil {
		t.Fatalf("partial UpsertWorkforceMember: %v", err)
	}
	if updated.Email != "new.mail.agent@example.test" || store.profiles["profile-mail"].Email != updated.Email {
		t.Fatalf("updated=%+v profile=%+v", updated, store.profiles["profile-mail"])
	}
}
