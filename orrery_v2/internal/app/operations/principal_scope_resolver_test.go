package operations

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
)

func TestRolePermissionScopeResolverAddsEffectiveRolePermissions(t *testing.T) {
	now := time.Date(2026, 7, 11, 10, 0, 0, 0, time.UTC)
	store := &scopeResolverRoleStore{
		roles: []RoleDefinitionDTO{{
			RoleID:         "custom.resource.admin",
			Status:         "active",
			PermissionRefs: []string{"resources.read", "resources.manage"},
		}},
		assignments: []RoleAssignmentDTO{
			{
				Actor:     ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
				RoleID:    "custom.resource.admin",
				StartsAt:  now.Add(-time.Hour),
				ExpiresAt: now.Add(time.Hour),
			},
			{
				Actor:     ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
				RoleID:    "security.admin",
				ExpiresAt: now.Add(-time.Minute),
			},
		},
	}
	resolver := NewRolePermissionScopeResolver(store).WithMode(RolePermissionScopeModeHybrid)
	resolver.now = func() time.Time { return now }
	principal := identity.Principal{
		SubjectProvider: "local",
		Subject:         "operator",
		UserID:          "operator",
		Username:        "operator",
		AgentProfileID:  "human-operator",
		ViewerKind:      identity.ViewerKindUser,
	}

	scopes, err := resolver.ResolvePermissionScopes(context.Background(), principal, []string{"discussion.read", "resources.read"})
	if err != nil {
		t.Fatalf("ResolvePermissionScopes: %v", err)
	}
	for _, want := range []string{"discussion.read", "resource.read", "resource.manage"} {
		if !scopeListContains(scopes, want) {
			t.Fatalf("scopes = %+v, missing %q", scopes, want)
		}
	}
	if scopeListContains(scopes, "permissions.manage") || scopeListContains(scopes, "resources.read") {
		t.Fatalf("scopes = %+v, expired or non-canonical permission leaked", scopes)
	}
}

func TestRolePermissionScopeResolverFallsBackToAccountScopesWithoutAssignment(t *testing.T) {
	resolver := NewRolePermissionScopeResolver(&scopeResolverRoleStore{}).WithMode(RolePermissionScopeModeHybrid)
	scopes, err := resolver.ResolvePermissionScopes(context.Background(), identity.Principal{
		SubjectProvider: "local",
		Subject:         "operator",
		Username:        "operator",
		ViewerKind:      identity.ViewerKindUser,
	}, []string{"resources.read"})
	if err != nil {
		t.Fatalf("ResolvePermissionScopes: %v", err)
	}
	if len(scopes) != 1 || scopes[0] != "resource.read" {
		t.Fatalf("scopes = %+v", scopes)
	}
}

func TestRolePermissionScopeResolverMatchesCanonicalHumanUserID(t *testing.T) {
	store := &scopeResolverRoleStore{
		roles:       []RoleDefinitionDTO{{RoleID: "custom.reader", Status: "active", PermissionRefs: []string{"knowledge.read"}}},
		assignments: []RoleAssignmentDTO{{Actor: ActorRefDTO{ActorType: "human", ActorID: "user-1"}, RoleID: "custom.reader"}},
	}
	resolver := NewRolePermissionScopeResolver(store).WithMode(RolePermissionScopeModeHybrid)
	scopes, err := resolver.ResolvePermissionScopes(context.Background(), identity.Principal{
		SubjectProvider: "oidc", Subject: "external-subject", UserID: "user-1", AgentProfileID: "human-profile-1", ViewerKind: identity.ViewerKindUser,
	}, nil)
	if err != nil {
		t.Fatalf("ResolvePermissionScopes: %v", err)
	}
	if !scopeListContains(scopes, "knowledge.read") {
		t.Fatalf("canonical user role scopes = %+v", scopes)
	}
}

func TestRolePermissionScopeResolverAuthoritativeModeUsesOnlyRolePermissions(t *testing.T) {
	store := &scopeResolverRoleStore{
		roles: []RoleDefinitionDTO{{
			RoleID:         "custom.resource.reader",
			Status:         "active",
			PermissionRefs: []string{"resources.read"},
		}},
		assignments: []RoleAssignmentDTO{{
			Actor:  ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
			RoleID: "custom.resource.reader",
		}},
	}
	resolver := NewRolePermissionScopeResolver(store).WithMode(RolePermissionScopeModeAuthoritative)
	principal := identity.Principal{
		SubjectProvider: "local",
		Subject:         "operator",
		Username:        "operator",
		AgentProfileID:  "human-operator",
		ViewerKind:      identity.ViewerKindUser,
	}

	scopes, err := resolver.ResolvePermissionScopes(context.Background(), principal, []string{"discussion.read", "resources.manage"})
	if err != nil {
		t.Fatalf("ResolvePermissionScopes: %v", err)
	}
	if len(scopes) != 1 || scopes[0] != "resource.read" {
		t.Fatalf("authoritative scopes = %+v", scopes)
	}
}

func TestRolePermissionScopeResolverAuthoritativePlatformAdminIncludesPersonalAssistantTools(t *testing.T) {
	store := &scopeResolverRoleStore{assignments: []RoleAssignmentDTO{{
		Actor:     ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
		RoleID:    RoleIDPlatformAdmin,
		ScopeMode: RoleScopePlatform,
	}}}
	resolver := NewRolePermissionScopeResolver(store).WithMode(RolePermissionScopeModeAuthoritative)

	scopes, err := resolver.ResolvePermissionScopes(context.Background(), identity.Principal{
		Username:       "operator",
		AgentProfileID: "human-operator",
		ViewerKind:     identity.ViewerKindUser,
	}, nil)
	if err != nil {
		t.Fatalf("ResolvePermissionScopes: %v", err)
	}
	for _, permission := range []string{"enterprise_graph.read", "skill.read", "personal_assistant.shell"} {
		if !scopeListContains(scopes, permission) {
			t.Fatalf("platform admin authoritative scopes are missing %s: %+v", permission, scopes)
		}
	}
}

func TestRolePermissionScopeResolverAuthoritativeModeRetainsBreakGlassScopes(t *testing.T) {
	resolver := NewRolePermissionScopeResolver(failingScopeResolverRoleStore{}).WithMode(RolePermissionScopeModeAuthoritative)
	scopes, err := resolver.ResolvePermissionScopes(context.Background(), identity.Principal{
		SubjectProvider: "local",
		Subject:         "superadmin",
		Username:        "superadmin",
		AgentProfileID:  "system-superadmin",
		ViewerKind:      identity.ViewerKindSystem,
	}, []string{"permissions.manage", "resources.manage"})
	if err != nil {
		t.Fatalf("ResolvePermissionScopes: %v", err)
	}
	if len(scopes) != 2 || !scopeListContains(scopes, "permissions.manage") || !scopeListContains(scopes, "resource.manage") {
		t.Fatalf("break-glass scopes = %+v", scopes)
	}
}

func TestRolePermissionScopeResolverControlsServerAccountScopesByMode(t *testing.T) {
	principal := identity.Principal{
		Username:       "operator",
		AgentProfileID: "human-operator",
		ViewerKind:     identity.ViewerKindUser,
	}
	store := &scopeResolverRoleStore{assignments: []RoleAssignmentDTO{{
		Actor:     ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
		RoleID:    RoleIDProjectLead,
		ScopeMode: RoleScopePlatform,
	}}}
	hybrid := NewRolePermissionScopeResolver(store).WithMode(RolePermissionScopeModeHybrid)
	hybridScopes, err := hybrid.ResolveAuthorizationAccountScopes(context.Background(), principal, []string{PermissionPermissionsManage})
	if err != nil {
		t.Fatalf("hybrid ResolveAuthorizationAccountScopes: %v", err)
	}
	if !scopeListContains(hybridScopes, PermissionPermissionsManage) || !scopeListContains(hybridScopes, "mission.read") || !scopeListContains(hybridScopes, "mission.write") {
		t.Fatalf("hybrid account scopes = %+v", hybridScopes)
	}

	authoritative := hybrid.WithMode(RolePermissionScopeModeAuthoritative)
	authoritativeScopes, err := authoritative.ResolveAuthorizationAccountScopes(context.Background(), principal, []string{PermissionPermissionsManage})
	if err != nil {
		t.Fatalf("authoritative ResolveAuthorizationAccountScopes: %v", err)
	}
	if !scopeListContains(authoritativeScopes, "mission.read") || !scopeListContains(authoritativeScopes, "mission.write") || scopeListContains(authoritativeScopes, PermissionPermissionsManage) {
		t.Fatalf("authoritative server principal scopes = %+v", authoritativeScopes)
	}

	breakGlassScopes, err := authoritative.ResolveAuthorizationAccountScopes(context.Background(), identity.Principal{
		Username:       "superadmin",
		AgentProfileID: "system-superadmin",
		ViewerKind:     identity.ViewerKindSystem,
	}, []string{"permissions.manage"})
	if err != nil {
		t.Fatalf("break-glass ResolveAuthorizationAccountScopes: %v", err)
	}
	if len(breakGlassScopes) != 1 || breakGlassScopes[0] != "permissions.manage" {
		t.Fatalf("break-glass account scopes = %+v", breakGlassScopes)
	}
	spoofedUsernameScopes, err := authoritative.ResolveAuthorizationAccountScopes(context.Background(), identity.Principal{
		Username:       "superadmin",
		AgentProfileID: "human-operator",
		ViewerKind:     identity.ViewerKindUser,
	}, []string{"permissions.manage"})
	if err != nil {
		t.Fatalf("spoofed username ResolveAuthorizationAccountScopes: %v", err)
	}
	if scopeListContains(spoofedUsernameScopes, PermissionPermissionsManage) || !scopeListContains(spoofedUsernameScopes, "mission.read") {
		t.Fatalf("username alias scopes = %+v", spoofedUsernameScopes)
	}
}

func TestRolePermissionScopeResolverHybridServerAuthorizationIncludesPlatformRoles(t *testing.T) {
	store := &scopeResolverRoleStore{assignments: []RoleAssignmentDTO{
		{Actor: ActorRefDTO{ActorType: "human", ActorID: "H000002"}, RoleID: RoleIDPlatformAdmin, ScopeMode: RoleScopePlatform},
		{Actor: ActorRefDTO{ActorType: "human", ActorID: "H000002"}, RoleID: "org.owner", ScopeMode: RoleScopeSelfAndDescendants, OrganizationNodeID: "minvia", OrganizationCode: "01"},
	}}
	resolver := NewRolePermissionScopeResolver(store).WithMode(RolePermissionScopeModeHybrid)

	scopes, err := resolver.ResolveAuthorizationAccountScopes(context.Background(), identity.Principal{
		SubjectProvider: identity.ProviderKratos,
		Subject:         "kratos-quzheng",
		UserID:          "quzheng",
		Username:        "quzheng",
		AgentProfileID:  "H000002",
		ViewerKind:      identity.ViewerKindUser,
	}, nil)
	if err != nil {
		t.Fatalf("ResolveAuthorizationAccountScopes: %v", err)
	}
	for _, permission := range []string{PermissionPermissionsRead, PermissionPermissionsManage, PermissionRolesManage, "settings.manage"} {
		if !scopeListContains(scopes, permission) {
			t.Fatalf("hybrid platform admin scopes are missing %s: %+v", permission, scopes)
		}
	}
}

func TestRolePermissionScopeResolverHybridServerAuthorizationDoesNotPromoteOrganizationRoles(t *testing.T) {
	store := &scopeResolverRoleStore{assignments: []RoleAssignmentDTO{{
		Actor:              ActorRefDTO{ActorType: "human", ActorID: "H000002"},
		RoleID:             "org.owner",
		ScopeMode:          RoleScopeSelfAndDescendants,
		OrganizationNodeID: "minvia",
		OrganizationCode:   "01",
	}}}
	resolver := NewRolePermissionScopeResolver(store).WithMode(RolePermissionScopeModeHybrid)

	scopes, err := resolver.ResolveAuthorizationAccountScopes(context.Background(), identity.Principal{
		UserID: "quzheng", Username: "quzheng", AgentProfileID: "H000002", ViewerKind: identity.ViewerKindUser,
	}, nil)
	if err != nil {
		t.Fatalf("ResolveAuthorizationAccountScopes: %v", err)
	}
	for _, permission := range []string{PermissionPermissionsRead, PermissionPermissionsManage, PermissionOrgManage} {
		if scopeListContains(scopes, permission) {
			t.Fatalf("organization role leaked global permission %s: %+v", permission, scopes)
		}
	}
}

func TestRolePermissionScopeResolverHybridServerAuthorizationRetainsLegacyScopesWithoutPlatformRole(t *testing.T) {
	resolver := NewRolePermissionScopeResolver(&scopeResolverRoleStore{}).WithMode(RolePermissionScopeModeHybrid)

	scopes, err := resolver.ResolveAuthorizationAccountScopes(context.Background(), identity.Principal{
		UserID: "legacy-user", Username: "legacy-user", AgentProfileID: "H000098", ViewerKind: identity.ViewerKindUser,
	}, []string{"resources.read"})
	if err != nil {
		t.Fatalf("ResolveAuthorizationAccountScopes: %v", err)
	}
	if len(scopes) != 1 || scopes[0] != "resource.read" {
		t.Fatalf("legacy account scopes = %+v", scopes)
	}
}

func TestRolePermissionScopeResolverKeepsOnlyPlatformRoleScopesForServerAuthorization(t *testing.T) {
	store := &scopeResolverRoleStore{
		roles: []RoleDefinitionDTO{{
			RoleID:         "custom.department.resource",
			Category:       "resource",
			Status:         "active",
			PermissionRefs: []string{"resource.manage"},
		}},
		assignments: []RoleAssignmentDTO{
			{
				Actor:     ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
				RoleID:    "pm",
				ScopeMode: RoleScopePlatform,
			},
			{
				Actor:     ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
				RoleID:    "custom.department.resource",
				ScopeMode: RoleScopeSelf,
			},
		},
	}
	resolver := NewRolePermissionScopeResolver(store).WithMode(RolePermissionScopeModeAuthoritative)
	principal := identity.Principal{
		Username:       "operator",
		AgentProfileID: "human-operator",
		ViewerKind:     identity.ViewerKindUser,
	}

	scopes, err := resolver.ResolveAuthorizationAccountScopes(context.Background(), principal, []string{"permissions.manage"})
	if err != nil {
		t.Fatalf("ResolveAuthorizationAccountScopes: %v", err)
	}
	if !scopeListContains(scopes, "mission.read") || !scopeListContains(scopes, "mission.write") {
		t.Fatalf("platform PM scopes missing from server authorization: %+v", scopes)
	}
	if scopeListContains(scopes, "resource.manage") || scopeListContains(scopes, "permissions.manage") {
		t.Fatalf("organization or legacy account scopes leaked into server authorization: %+v", scopes)
	}
}

func TestRolePermissionScopeResolverDoesNotTreatExternalSubjectAsRoleAlias(t *testing.T) {
	store := &scopeResolverRoleStore{
		roles: []RoleDefinitionDTO{{
			RoleID:         "custom.admin",
			Status:         "active",
			PermissionRefs: []string{"permissions.manage"},
		}},
		assignments: []RoleAssignmentDTO{{
			Actor:  ActorRefDTO{ActorType: "human", ActorID: "external-admin-subject"},
			RoleID: "custom.admin",
		}},
	}
	resolver := NewRolePermissionScopeResolver(store).WithMode(RolePermissionScopeModeAuthoritative)
	scopes, err := resolver.ResolvePermissionScopes(context.Background(), identity.Principal{
		SubjectProvider: "oidc",
		Subject:         "external-admin-subject",
		UserID:          "operator",
		Username:        "operator",
		AgentProfileID:  "human-operator",
		ViewerKind:      identity.ViewerKindUser,
	}, nil)
	if err != nil {
		t.Fatalf("ResolvePermissionScopes: %v", err)
	}
	if len(scopes) != 0 {
		t.Fatalf("external subject should not inherit role assignment: %+v", scopes)
	}
}

func TestRolePermissionScopeResolverDiagnosesLegacyScopeDependencies(t *testing.T) {
	store := &scopeResolverRoleStore{
		roles: []RoleDefinitionDTO{{
			RoleID:         "custom.resource.reader",
			Status:         "active",
			PermissionRefs: []string{"resource.read"},
		}},
		assignments: []RoleAssignmentDTO{{
			Actor:  ActorRefDTO{ActorType: "human", ActorID: "human-operator"},
			RoleID: "custom.resource.reader",
		}},
	}
	profiles := &scopeTransitionProfileReader{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-operator": {
			ID:        "human-operator",
			AgentKind: agentcore.AgentKindHuman,
			Enabled:   true,
			UserAccount: agentcore.UserAccountSpec{
				Username:        "operator",
				AccountEnabled:  true,
				PermissionScope: []string{"resource.read", "discussion.read"},
			},
		},
		"human-reviewer": {
			ID:        "human-reviewer",
			AgentKind: agentcore.AgentKindHuman,
			Enabled:   true,
			UserAccount: agentcore.UserAccountSpec{
				Username:        "reviewer",
				AccountEnabled:  true,
				PermissionScope: []string{"discussion.read"},
			},
		},
		"human-disabled": {
			ID:        "human-disabled",
			AgentKind: agentcore.AgentKindHuman,
			Enabled:   false,
			UserAccount: agentcore.UserAccountSpec{
				Username:        "disabled",
				AccountEnabled:  true,
				PermissionScope: []string{"operations.manage"},
			},
		},
		"human-market": {
			ID:        "human-market",
			AgentKind: agentcore.AgentKindHuman,
			Enabled:   true,
			Placement: agentcore.TalentPlacement{Status: agentcore.TalentPlacementMarket},
			UserAccount: agentcore.UserAccountSpec{
				Username:        "market-user",
				AccountEnabled:  true,
				PermissionScope: []string{"operations.manage"},
			},
		},
	}}

	hybrid, err := NewRolePermissionScopeResolver(store).
		WithMode(RolePermissionScopeModeHybrid).
		DiagnoseTransition(context.Background(), profiles)
	if err != nil {
		t.Fatalf("DiagnoseTransition hybrid: %v", err)
	}
	if !hybrid.Ready || hybrid.TransitionReady || hybrid.Principals != 2 || hybrid.BreakGlassPrincipals != 0 || hybrid.LegacyDependentPrincipals != 2 || hybrid.AccountScopes != 3 || hybrid.EffectiveRoleScopes != 1 || hybrid.DroppedScopes != 3 || hybrid.BreakGlassRetainedScopes != 0 || hybrid.ObjectScopeConflicts != 1 {
		t.Fatalf("hybrid diagnostics = %+v", hybrid)
	}

	authoritative, err := NewRolePermissionScopeResolver(store).
		WithMode(RolePermissionScopeModeAuthoritative).
		DiagnoseTransition(context.Background(), profiles)
	if err != nil {
		t.Fatalf("DiagnoseTransition authoritative: %v", err)
	}
	if authoritative.Ready || authoritative.TransitionReady || authoritative.Mode != RolePermissionScopeModeAuthoritative {
		t.Fatalf("authoritative diagnostics = %+v", authoritative)
	}
}

type scopeResolverRoleStore struct {
	roles       []RoleDefinitionDTO
	assignments []RoleAssignmentDTO
}

type failingScopeResolverRoleStore struct{}

func (failingScopeResolverRoleStore) ListRoleDefinitions(context.Context) ([]RoleDefinitionDTO, error) {
	return nil, errors.New("role store unavailable")
}

func (failingScopeResolverRoleStore) SaveRoleDefinition(context.Context, RoleDefinitionDTO) error {
	return nil
}

func (failingScopeResolverRoleStore) ListRoleAssignments(context.Context, RoleAssignmentQuery) ([]RoleAssignmentDTO, error) {
	return nil, errors.New("role store unavailable")
}

func (failingScopeResolverRoleStore) SaveRoleAssignment(context.Context, RoleAssignmentDTO) error {
	return nil
}

func (failingScopeResolverRoleStore) DeleteRoleAssignment(context.Context, string) error {
	return nil
}

type scopeTransitionProfileReader struct {
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
}

func (r *scopeTransitionProfileReader) QueryDirectory(_ context.Context, _ agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	entries := make([]agentcore.AgentDirectoryEntry, 0, len(r.profiles))
	for id, profile := range r.profiles {
		entries = append(entries, agentcore.AgentDirectoryEntry{AgentProfileID: id, AgentKind: profile.AgentKind, Enabled: profile.Enabled})
	}
	return agentcore.AgentDirectoryPage{Entries: entries, Total: len(entries)}, nil
}

func (r *scopeTransitionProfileReader) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	return r.profiles[id], nil
}

func (s *scopeResolverRoleStore) ListRoleDefinitions(context.Context) ([]RoleDefinitionDTO, error) {
	return append([]RoleDefinitionDTO(nil), s.roles...), nil
}

func (s *scopeResolverRoleStore) SaveRoleDefinition(context.Context, RoleDefinitionDTO) error {
	return nil
}

func (s *scopeResolverRoleStore) ListRoleAssignments(context.Context, RoleAssignmentQuery) ([]RoleAssignmentDTO, error) {
	return append([]RoleAssignmentDTO(nil), s.assignments...), nil
}

func (s *scopeResolverRoleStore) SaveRoleAssignment(context.Context, RoleAssignmentDTO) error {
	return nil
}

func (s *scopeResolverRoleStore) DeleteRoleAssignment(context.Context, string) error {
	return nil
}

func scopeListContains(scopes []string, want string) bool {
	for _, scope := range scopes {
		if scope == want {
			return true
		}
	}
	return false
}
