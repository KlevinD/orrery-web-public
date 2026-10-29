package operations_test

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
)

func TestCommandServicePeopleSSOAccountUsesKratosCredentialForOrdinaryHuman(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	service.SetPeopleSSOEnabled(true)
	service.SetWorkforceSessionRevoker(&fakeWorkforceSessionRevoker{})
	service.SetWorkforceAccountProjector(&fakeWorkforceAccountProjector{result: operations.WorkforceAccountProjectionResult{
		Status: "applied", Revision: 1, KratosIdentityID: "kratos-quzheng", InitialPassword: "initial-password-123",
	}})
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"}}, []operations.PositionDTO{{PositionID: "operator", DepartmentID: "operations", Name: "Operator"}})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	created, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "human-quzheng", DisplayName: "屈正", AgentKind: "human",
		UserAccount:    operations.WorkforceUserAccountDTO{Username: "quzheng", AccountEnabled: true},
		OrganizationID: "orrery", DepartmentID: "operations", PositionID: "operator",
		Persona: operations.WorkforcePersonaDTO{Backstory: "People managed employee."}, Enabled: true,
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember: %v", err)
	}
	profile := store.profiles["human-quzheng"]
	if profile.UserAccount.PasswordSalt != "" || profile.UserAccount.PasswordVerifier != "" {
		t.Fatalf("ordinary People profile retained password material: %+v", profile.UserAccount)
	}
	if created.AccountProjection.Status != "applied" || created.AccountProjection.Revision != 1 || created.AccountProjection.InitialPassword != "initial-password-123" {
		t.Fatalf("account projection = %+v", created.AccountProjection)
	}

	_, err = service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "human-quzheng", UserAccount: operations.WorkforceUserAccountDTO{Password: "must-not-be-stored"},
		UpdateMask: []string{"user_account.password"},
	})
	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("ordinary password update error = %v, want ErrInvalidRequest", err)
	}
}

func TestCommandServiceRejectsReservedNonWorkforcePrincipal(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	for _, profileID := range []string{"system-superadmin", "human-superadmin", "human-local-admin", "human-local-user"} {
		_, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{AgentProfileID: profileID})
		if !errors.Is(err, operations.ErrInvalidRequest) {
			t.Fatalf("reserved profile %s error = %v, want ErrInvalidRequest", profileID, err)
		}
	}
	if len(store.profiles) != 0 {
		t.Fatalf("reserved profile request wrote workforce data: %+v", store.profiles)
	}
}

type fakeWorkforceAccountProjectionStateReader struct {
	state operations.WorkforceAccountProjectionDTO
}

func (r fakeWorkforceAccountProjectionStateReader) AccountProjectionState(context.Context, string) (operations.WorkforceAccountProjectionDTO, bool, error) {
	return r.state, true, nil
}

func TestQueryServicePeopleSSOAccountShowsProjectionState(t *testing.T) {
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-quzheng": {
			ID: "human-quzheng", RealName: "屈正", DisplayName: "屈正", AgentKind: agentcore.AgentKindHuman,
			OrgIdentity: agentcore.AgentOrgIdentity{EmploymentType: agentcore.EmploymentTypeHuman},
			UserAccount: agentcore.UserAccountSpec{Username: "quzheng", AccountEnabled: true}, Enabled: true,
		},
	}}
	service := operations.NewQueryService(store, nil)
	service.SetWorkforceAccountProjectionStateReader(fakeWorkforceAccountProjectionStateReader{state: operations.WorkforceAccountProjectionDTO{
		Status: "applied", Revision: 7, KratosIdentityID: "kratos-quzheng",
	}})

	page, err := service.ListWorkforceMembers(context.Background(), operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceRead}}, operations.WorkforceQuery{IncludeDetails: true})
	if err != nil {
		t.Fatalf("ListWorkforceMembers: %v", err)
	}
	if len(page.Members) != 1 || page.Members[0].AccountProjection.Status != "applied" || page.Members[0].AccountProjection.Revision != 7 || page.Members[0].AccountProjection.InitialPassword != "" {
		t.Fatalf("members = %+v", page.Members)
	}
}
