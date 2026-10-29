package operations_test

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
)

func TestCommandServiceRevokesHumanSessionsForCredentialAndDisableChanges(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	revoker := &fakeWorkforceSessionRevoker{}
	service := operations.NewCommandService(store)
	service.SetWorkforceSessionRevoker(revoker)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "operator", DepartmentID: "operations", Name: "Operator"},
	})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-session-user",
		DisplayName:    "Session User",
		AgentKind:      "human",
		UserAccount: operations.WorkforceUserAccountDTO{
			Username:       "Session.User",
			AccountEnabled: true,
			Password:       "initial-secret",
		},
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Maintains session security."},
		Enabled:        true,
	}); err != nil {
		t.Fatalf("UpsertWorkforceMember create: %v", err)
	}
	if len(revoker.usernames) != 0 {
		t.Fatalf("new account should not revoke sessions: %+v", revoker.usernames)
	}

	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-session-user",
		UserAccount: operations.WorkforceUserAccountDTO{
			Password: "rotated-secret",
		},
		UpdateMask: []string{"user_account.password"},
	}); err != nil {
		t.Fatalf("UpsertWorkforceMember password change: %v", err)
	}
	assertSessionRevocations(t, revoker.usernames, "password change")

	revoker.usernames = nil
	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-session-user",
		UserAccount: operations.WorkforceUserAccountDTO{
			AccountEnabled: false,
		},
		UpdateMask: []string{"user_account.account_enabled"},
	}); err != nil {
		t.Fatalf("UpsertWorkforceMember account disable: %v", err)
	}
	assertSessionRevocations(t, revoker.usernames, "account disable")

	revoker.usernames = nil
	if err := service.SetWorkforceMemberEnabled(ctx, viewer, operations.SetWorkforceMemberEnabledCommand{
		AgentProfileID: "profile-session-user",
		Enabled:        false,
	}); err != nil {
		t.Fatalf("SetWorkforceMemberEnabled: %v", err)
	}
	assertSessionRevocations(t, revoker.usernames, "profile disable")

	revoker.usernames = nil
	if err := service.SetWorkforceMemberEnabled(ctx, viewer, operations.SetWorkforceMemberEnabledCommand{
		AgentProfileID: "profile-session-user",
		Enabled:        true,
	}); err != nil {
		t.Fatalf("SetWorkforceMemberEnabled re-enable: %v", err)
	}
	assertSessionRevocations(t, revoker.usernames, "profile re-enable")

	revoker.usernames = nil
	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-session-user",
		UserAccount: operations.WorkforceUserAccountDTO{
			AccountEnabled: true,
		},
		UpdateMask: []string{"user_account.account_enabled"},
	}); err != nil {
		t.Fatalf("UpsertWorkforceMember account re-enable: %v", err)
	}
	assertSessionRevocations(t, revoker.usernames, "account re-enable")
}

func TestCommandServiceDoesNotChangePasswordWhenSessionRevocationFails(t *testing.T) {
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
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-session-failure",
		DisplayName:    "Session Failure",
		AgentKind:      "human",
		UserAccount: operations.WorkforceUserAccountDTO{
			Username:       "session-failure",
			AccountEnabled: true,
			Password:       "initial-secret",
		},
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Maintains session security."},
		Enabled:        true,
	}); err != nil {
		t.Fatalf("UpsertWorkforceMember create: %v", err)
	}
	originalVerifier := store.profiles["profile-session-failure"].UserAccount.PasswordVerifier
	revokeErr := errors.New("session store unavailable")
	service.SetWorkforceSessionRevoker(&fakeWorkforceSessionRevoker{err: revokeErr})

	_, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-session-failure",
		UserAccount: operations.WorkforceUserAccountDTO{
			Password: "rotated-secret",
		},
		UpdateMask: []string{"user_account.password"},
	})
	if !errors.Is(err, revokeErr) {
		t.Fatalf("UpsertWorkforceMember error = %v, want revocation error", err)
	}
	if got := store.profiles["profile-session-failure"].UserAccount.PasswordVerifier; got != originalVerifier {
		t.Fatalf("password changed despite revocation failure: before=%q after=%q", originalVerifier, got)
	}
}

func TestCommandServiceFailsClosedWhenSessionRevokerIsMissing(t *testing.T) {
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
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-missing-revoker",
		DisplayName:    "Missing Revoker",
		AgentKind:      "human",
		UserAccount: operations.WorkforceUserAccountDTO{
			Username:       "missing.revoker",
			AccountEnabled: true,
			Password:       "initial-secret",
		},
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Maintains session security."},
		Enabled:        true,
	}); err != nil {
		t.Fatalf("UpsertWorkforceMember create: %v", err)
	}
	original := store.profiles["profile-missing-revoker"]

	_, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-missing-revoker",
		UserAccount: operations.WorkforceUserAccountDTO{
			Password: "rotated-secret",
		},
		UpdateMask: []string{"user_account.password"},
	})
	if !errors.Is(err, operations.ErrServiceDependency) {
		t.Fatalf("UpsertWorkforceMember error = %v, want ErrServiceDependency", err)
	}
	if got := store.profiles["profile-missing-revoker"]; got.UserAccount.PasswordVerifier != original.UserAccount.PasswordVerifier {
		t.Fatalf("password changed without session revoker: before=%q after=%q", original.UserAccount.PasswordVerifier, got.UserAccount.PasswordVerifier)
	}

	err = service.SetWorkforceMemberEnabled(ctx, viewer, operations.SetWorkforceMemberEnabledCommand{
		AgentProfileID: "profile-missing-revoker",
		Enabled:        false,
	})
	if !errors.Is(err, operations.ErrServiceDependency) {
		t.Fatalf("SetWorkforceMemberEnabled error = %v, want ErrServiceDependency", err)
	}
	if got := store.profiles["profile-missing-revoker"].Enabled; got != original.Enabled {
		t.Fatalf("profile enabled state changed without session revoker: got=%t want=%t", got, original.Enabled)
	}

	if err := service.SetWorkforceMemberEnabled(ctx, viewer, operations.SetWorkforceMemberEnabledCommand{
		AgentProfileID: "profile-missing-revoker",
		Enabled:        true,
	}); err != nil {
		t.Fatalf("SetWorkforceMemberEnabled no-op: %v", err)
	}
}

func TestCommandServiceDisablesHumanLoginWhenPostChangeRevocationFails(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	service.SetWorkforceSessionRevoker(&fakeWorkforceSessionRevoker{})
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "operator", DepartmentID: "operations", Name: "Operator"},
	})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-post-revoke-failure",
		DisplayName:    "Post Revoke Failure",
		AgentKind:      "human",
		UserAccount: operations.WorkforceUserAccountDTO{
			Username:       "post.revoke.failure",
			AccountEnabled: true,
			Password:       "initial-secret",
		},
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Maintains session security."},
		Enabled:        true,
	}); err != nil {
		t.Fatalf("UpsertWorkforceMember create: %v", err)
	}
	revokeErr := errors.New("session store unavailable")
	service.SetWorkforceSessionRevoker(&fakeWorkforceSessionRevoker{failAt: 2, err: revokeErr})

	_, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-post-revoke-failure",
		UserAccount: operations.WorkforceUserAccountDTO{
			Password: "rotated-secret",
		},
		UpdateMask: []string{"user_account.password"},
	})
	if !errors.Is(err, revokeErr) {
		t.Fatalf("UpsertWorkforceMember error = %v, want post-change revocation error", err)
	}
	profile := store.profiles["profile-post-revoke-failure"]
	if profile.Enabled || profile.UserAccount.AccountEnabled {
		t.Fatalf("human login remained enabled after post-change revocation failure: %+v", profile)
	}
}

func TestCommandServiceFailsClosedWithoutRestoringRolesWhenPostChangeRevocationFails(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	roles := newFakeRoleStore()
	service := operations.NewCommandService(store)
	service.SetRoleStore(roles)
	service.SetWorkforceSessionRevoker(&fakeWorkforceSessionRevoker{})
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{{
		DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations",
	}}, []operations.PositionDTO{{
		PositionID: "operator", DepartmentID: "operations", Name: "Operator",
	}})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "human-recovery-admin",
		DisplayName:    "Recovery Administrator",
		AgentKind:      "human",
		UserAccount: operations.WorkforceUserAccountDTO{
			Username:       "recovery-admin",
			AccountEnabled: true,
			Password:       "initial-secret-1",
		},
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		RoleIDs:        []string{"member"},
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Maintains administrative access."},
		Enabled:        true,
	}); err != nil {
		t.Fatalf("create recovery profile: %v", err)
	}
	previous := store.profiles["human-recovery-admin"]
	revokeErr := errors.New("session store unavailable")
	service.SetWorkforceSessionRevoker(&fakeWorkforceSessionRevoker{failAt: 2, err: revokeErr})

	_, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "human-recovery-admin",
		UserAccount: operations.WorkforceUserAccountDTO{
			Password: "replacement-secret-1",
		},
		RoleIDs:    []string{"org.admin"},
		UpdateMask: []string{"user_account.password", "role_ids"},
	})
	if !errors.Is(err, revokeErr) {
		t.Fatalf("recovery account update error = %v, want revocation error", err)
	}
	current := store.profiles["human-recovery-admin"]
	if current.UserAccount.PasswordVerifier == previous.UserAccount.PasswordVerifier || current.Enabled || current.UserAccount.AccountEnabled {
		t.Fatalf("updated account was not failed closed: before=%+v after=%+v", previous, current)
	}
	if assignment := roles.assignments["workforce:human-recovery-admin:org.admin"]; assignment.RoleID != "org.admin" {
		t.Fatalf("replacement role assignment was not retained: %+v", assignment)
	}
	if _, exists := roles.assignments["workforce:human-recovery-admin:member"]; exists {
		t.Fatal("stale member role assignment remains")
	}
}

func TestCommandServiceRevokesHumanSessionsWhenReleasedToTalentMarket(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	revoker := &fakeWorkforceSessionRevoker{}
	service := operations.NewCommandService(store)
	service.SetWorkforceSessionRevoker(revoker)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "operator", DepartmentID: "operations", Name: "Operator"},
	})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-released-user",
		DisplayName:    "Released User",
		AgentKind:      "human",
		UserAccount: operations.WorkforceUserAccountDTO{
			Username:       "released.user",
			AccountEnabled: true,
			Password:       "initial-secret",
		},
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Supports operations."},
		Enabled:        true,
	}); err != nil {
		t.Fatalf("UpsertWorkforceMember create: %v", err)
	}
	revoker.usernames = nil

	if err := service.ReleaseWorkforceMember(ctx, viewer, operations.ReleaseWorkforceMemberCommand{
		AgentProfileID: "profile-released-user",
		Reason:         "contract ended",
	}); err != nil {
		t.Fatalf("ReleaseWorkforceMember: %v", err)
	}
	if got := revoker.usernames; len(got) != 2 || got[0] != "released.user" || got[1] != "released.user" {
		t.Fatalf("release revocations = %+v", got)
	}
	profile := store.profiles["profile-released-user"]
	if !profile.Placement.IsMarket(profile.OrgIdentity) {
		t.Fatalf("released profile should be in talent market: %+v", profile.Placement)
	}

	revoker.usernames = nil
	if _, err := service.HireTalentFromMarket(ctx, viewer, operations.HireTalentFromMarketCommand{
		AgentProfileID: "profile-released-user",
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
	}); err != nil {
		t.Fatalf("HireTalentFromMarket: %v", err)
	}
	if got := revoker.usernames; len(got) != 2 || got[0] != "released.user" || got[1] != "released.user" {
		t.Fatalf("rehire revocations = %+v", got)
	}
}

func assertSessionRevocations(t *testing.T, got []string, action string) {
	t.Helper()
	if len(got) != 2 || got[0] != "session.user" || got[1] != "session.user" {
		t.Fatalf("%s revocations = %+v", action, got)
	}
}

type fakeWorkforceSessionRevoker struct {
	usernames []string
	failAt    int
	err       error
}

func (f *fakeWorkforceSessionRevoker) RevokeSessionsForUsername(_ context.Context, username string) error {
	f.usernames = append(f.usernames, username)
	if f.failAt > 0 && len(f.usernames) != f.failAt {
		return nil
	}
	return f.err
}
