package accesscontrol

import (
	"context"
	"testing"
)

func TestPolicyAllowsDirectAndRoleInheritedRouterPermissions(t *testing.T) {
	policy := PolicyDTO{
		RoleAssignments: []RoleAssignmentDTO{{PrincipalKind: "user", PrincipalID: "operator", RoleID: "ops-viewer"}},
		Grants: []PermissionGrantDTO{
			{SubjectKind: "role", SubjectID: "ops-viewer", CapabilityID: "get.api.workforce.members.operations.read"},
			{SubjectKind: "user", SubjectID: "operator", Scope: "settings.manage"},
		},
	}

	if !PolicyAllows(policy, AuthorizeRequest{PrincipalKind: "user", PrincipalID: "operator", CapabilityID: "get.api.workforce.members.operations.read", Scope: "operations.read"}) {
		t.Fatalf("role inherited capability grant should allow operator")
	}
	if !PolicyAllows(policy, AuthorizeRequest{PrincipalKind: "user", PrincipalID: "operator", CapabilityID: "put.api.settings.settings.manage", Scope: "settings.manage"}) {
		t.Fatalf("direct scope grant should allow operator")
	}
	if PolicyAllows(policy, AuthorizeRequest{PrincipalKind: "user", PrincipalID: "other", CapabilityID: "get.api.workforce.members.operations.read", Scope: "operations.read"}) {
		t.Fatalf("unassigned user should not inherit role grant")
	}
}

func TestEffectiveGrantsExposeSourcesForPermissionPage(t *testing.T) {
	policy := PolicyDTO{
		RoleAssignments: []RoleAssignmentDTO{{PrincipalKind: "user", PrincipalID: "operator", RoleID: "ops-viewer"}},
		Grants: []PermissionGrantDTO{
			{SubjectKind: "role", SubjectID: "ops-viewer", Scope: "operations.read"},
			{SubjectKind: "user", SubjectID: "operator", CapabilityID: "put.api.settings.settings.manage"},
		},
	}
	capabilities := []CapabilityDTO{
		{CapabilityID: "get.api.workforce.members.operations.read", Scope: "operations.read"},
		{CapabilityID: "put.api.settings.settings.manage", Scope: "settings.manage"},
	}

	effective := EffectiveGrants(policy, capabilities)

	assertEffectiveGrant(t, effective, "operator", "get.api.workforce.members.operations.read", "role", "ops-viewer")
	assertEffectiveGrant(t, effective, "operator", "put.api.settings.settings.manage", "direct", "")
}

func TestMemoryStoreNormalizesAndPersistsPolicy(t *testing.T) {
	store := NewMemoryStore(PolicyDTO{})
	service := NewService(store)
	saved, err := service.SavePolicy(context.Background(), PolicyDTO{
		Roles:           []RoleDTO{{RoleID: " ops-viewer ", LabelZh: " 运营查看员 "}},
		RoleAssignments: []RoleAssignmentDTO{{PrincipalKind: "human", PrincipalID: " operator ", RoleID: " ops-viewer "}},
		Grants:          []PermissionGrantDTO{{SubjectKind: "role", SubjectID: " ops-viewer ", Scope: " operations.read "}},
	})
	if err != nil {
		t.Fatalf("SavePolicy: %v", err)
	}
	if saved.RoleAssignments[0].PrincipalKind != "user" || saved.RoleAssignments[0].PrincipalID != "operator" || saved.Roles[0].RoleID != "ops-viewer" || saved.Grants[0].Scope != "operations.read" {
		t.Fatalf("policy was not normalized: %+v", saved)
	}
	loaded, err := service.GetPolicy(context.Background())
	if err != nil {
		t.Fatalf("GetPolicy: %v", err)
	}
	if len(loaded.Grants) != 1 || loaded.Grants[0].Scope != "operations.read" {
		t.Fatalf("loaded policy = %+v", loaded)
	}
}

func TestEnsureBootstrapAdminPolicySeedsAndMergesPolicy(t *testing.T) {
	service := NewService(NewMemoryStore(PolicyDTO{}))
	seeded, err := service.EnsureBootstrapAdminPolicy(context.Background(), RoleDTO{
		RoleID:  "platform-admin",
		LabelZh: "平台管理员",
		LabelEn: "Platform Admin",
	}, []RoleAssignmentDTO{
		{PrincipalKind: "human", PrincipalID: " operator "},
		{PrincipalKind: "user", PrincipalID: "local-user"},
	}, []string{"operations.read", "operations.manage"})
	if err != nil {
		t.Fatalf("EnsureBootstrapAdminPolicy: %v", err)
	}
	if len(seeded.RoleAssignments) != 2 || len(seeded.Grants) != 2 {
		t.Fatalf("seeded policy = %+v", seeded)
	}
	if !PolicyAllows(seeded, AuthorizeRequest{PrincipalKind: "user", PrincipalID: "operator", Scope: "operations.manage"}) {
		t.Fatalf("bootstrap policy should grant operator operations.manage: %+v", seeded)
	}

	service = NewService(NewMemoryStore(PolicyDTO{
		Roles:  []RoleDTO{{RoleID: "custom-admin", LabelEn: "Custom Admin"}},
		Grants: []PermissionGrantDTO{{SubjectKind: "role", SubjectID: "custom-admin", Scope: "mission.read"}},
	}))
	kept, err := service.EnsureBootstrapAdminPolicy(context.Background(), RoleDTO{RoleID: "platform-admin"}, []RoleAssignmentDTO{{PrincipalKind: "user", PrincipalID: "operator"}}, []string{"operations.manage"})
	if err != nil {
		t.Fatalf("EnsureBootstrapAdminPolicy existing: %v", err)
	}
	if !hasRole(kept, "custom-admin") || !hasRole(kept, "platform-admin") {
		t.Fatalf("bootstrap should keep existing role and add bootstrap role: %+v", kept)
	}
	if !PolicyAllows(kept, AuthorizeRequest{PrincipalKind: "user", PrincipalID: "operator", Scope: "operations.manage"}) {
		t.Fatalf("bootstrap should add requested scope to default role: %+v", kept)
	}
	if !hasScopeGrant(kept, "role", "custom-admin", "mission.read") {
		t.Fatalf("bootstrap should not overwrite existing custom grant: %+v", kept)
	}
}

func TestBootstrapRootPolicySeedsRootUserWithAllScopes(t *testing.T) {
	service := NewService(NewMemoryStore(PolicyDTO{}))
	seeded, err := service.EnsureBootstrapAdminPolicy(context.Background(), RootRole(), RootRoleAssignments(), []string{"operations.read", "operations.manage", "settings.manage"})
	if err != nil {
		t.Fatalf("EnsureBootstrapAdminPolicy: %v", err)
	}

	if len(seeded.RoleAssignments) < 1 || seeded.RoleAssignments[0].PrincipalID != "superadmin" {
		t.Fatalf("superadmin should be assigned the bootstrap role: %+v", seeded.RoleAssignments)
	}
	if len(seeded.Roles) != 1 || seeded.Roles[0].RoleID != "root" {
		t.Fatalf("root role should be the bootstrap role: %+v", seeded.Roles)
	}
	for _, scope := range []string{"operations.read", "operations.manage", "settings.manage"} {
		if !PolicyAllows(seeded, AuthorizeRequest{PrincipalKind: "user", PrincipalID: "superadmin", Scope: scope}) {
			t.Fatalf("superadmin should be allowed scope %s: %+v", scope, seeded)
		}
	}
}

func TestBootstrapRootPolicyMergesRootIntoExistingPolicy(t *testing.T) {
	service := NewService(NewMemoryStore(PolicyDTO{
		Roles:           []RoleDTO{{RoleID: "platform-admin", LabelEn: "Platform Admin"}},
		RoleAssignments: []RoleAssignmentDTO{{PrincipalKind: "user", PrincipalID: "operator", RoleID: "platform-admin"}},
		Grants:          []PermissionGrantDTO{{SubjectKind: "role", SubjectID: "platform-admin", Scope: "operations.read"}},
	}))

	merged, err := service.EnsureBootstrapAdminPolicy(context.Background(), RootRole(), RootRoleAssignments(), []string{"operations.read", "operations.manage"})
	if err != nil {
		t.Fatalf("EnsureBootstrapAdminPolicy: %v", err)
	}
	if !PolicyAllows(merged, AuthorizeRequest{PrincipalKind: "user", PrincipalID: "superadmin", Scope: "operations.manage"}) {
		t.Fatalf("superadmin should be merged with all bootstrap scopes: %+v", merged)
	}
	if !PolicyAllows(merged, AuthorizeRequest{PrincipalKind: "user", PrincipalID: "operator", Scope: "operations.read"}) {
		t.Fatalf("existing operator grant should remain: %+v", merged)
	}
	if !hasRole(merged, "platform-admin") || !hasRole(merged, "root") {
		t.Fatalf("existing role and root role should both remain: %+v", merged.Roles)
	}
}

func assertEffectiveGrant(t *testing.T, grants []EffectiveGrantDTO, principalID, capabilityID, source, sourceID string) {
	t.Helper()
	for _, grant := range grants {
		if grant.PrincipalID == principalID && grant.CapabilityID == capabilityID && grant.Source == source && grant.SourceID == sourceID {
			return
		}
	}
	t.Fatalf("missing effective grant principal=%s capability=%s source=%s sourceID=%s in %+v", principalID, capabilityID, source, sourceID, grants)
}

func hasRole(policy PolicyDTO, roleID string) bool {
	for _, role := range policy.Roles {
		if role.RoleID == roleID {
			return true
		}
	}
	return false
}

func hasScopeGrant(policy PolicyDTO, subjectKind, subjectID, scope string) bool {
	for _, grant := range policy.Grants {
		if grant.SubjectKind == subjectKind && grant.SubjectID == subjectID && grant.Scope == scope {
			return true
		}
	}
	return false
}
