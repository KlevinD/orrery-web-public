package operations

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/platform/db"
)

func TestBuiltInOrganizationRoles(t *testing.T) {
	roles := BuiltInRoleDefinitions()
	byID := map[string]RoleDefinitionDTO{}
	for _, role := range roles {
		byID[role.RoleID] = role
	}
	for _, id := range []string{"org.owner", "org.admin", "org.manager", "member", "agent.operator", "approval.reviewer", "resource.owner", "audit.viewer", "security.admin", RoleIDBasicUser, RoleIDPlatformAdmin, RoleIDProjectLead} {
		role := byID[id]
		if role.RoleID == "" || !role.BuiltIn || role.Status != "active" {
			t.Fatalf("built-in role %s = %+v", id, role)
		}
	}
	if !byID["org.owner"].ApprovalCapability {
		t.Fatalf("org.owner must be approval-capable: %+v", byID["org.owner"])
	}
	visible := UserAssignableRoleDefinitions(roles)
	if len(visible) != 2 || visible[0].RoleID != RoleIDPlatformAdmin || visible[1].RoleID != RoleIDProjectLead {
		t.Fatalf("user-assignable built-in roles = %+v", visible)
	}
	for _, permission := range []string{"settings.manage", "enterprise_graph.read", "ontology.read", "ontology.manage", "skill.read", "personal_assistant.shell"} {
		if !scopeListContains(byID[RoleIDPlatformAdmin].PermissionRefs, permission) {
			t.Fatalf("platform admin is missing %s: %+v", permission, byID[RoleIDPlatformAdmin].PermissionRefs)
		}
	}
	if scopeListContains(byID[RoleIDProjectLead].PermissionRefs, PermissionOperationsRead) {
		t.Fatalf("project lead must not inherit broad operations access: %+v", byID[RoleIDProjectLead].PermissionRefs)
	}
	if !scopeListContains(byID[RoleIDBasicUser].PermissionRefs, "mission.read") || scopeListContains(byID[RoleIDBasicUser].PermissionRefs, "mission.write") || scopeListContains(byID[RoleIDBasicUser].PermissionRefs, "agent.run") {
		t.Fatalf("basic user scopes = %+v", byID[RoleIDBasicUser].PermissionRefs)
	}
	for _, id := range []string{RoleIDBasicUser, RoleIDPlatformAdmin, RoleIDProjectLead} {
		for _, permission := range []string{"knowledge.read", "web.read", "memory.read", "enterprise_graph.read", "ontology.read", "skill.read"} {
			if !scopeListContains(byID[id].PermissionRefs, permission) {
				t.Fatalf("role %s is missing %s: %+v", id, permission, byID[id].PermissionRefs)
			}
		}
	}
}

func TestQueryServiceListsOnlyUserAssignableRoleTemplates(t *testing.T) {
	query := NewQueryService(nil, nil)
	page, err := query.ListRoleDefinitions(context.Background(), ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{PermissionRolesRead},
	})
	if err != nil {
		t.Fatalf("ListRoleDefinitions: %v", err)
	}
	if len(page.Items) != 2 || page.Items[0].RoleID != RoleIDPlatformAdmin || page.Items[1].RoleID != RoleIDProjectLead {
		t.Fatalf("role templates = %+v", page.Items)
	}
}

func TestRoleStorePersistsDefinitionsAndAssignments(t *testing.T) {
	ctx := context.Background()
	router, closeStore := newRoleTestRouter(t, ctx)
	defer closeStore()

	store := NewRoleStore(router)
	if err := store.SaveRoleDefinition(ctx, RoleDefinitionDTO{
		RoleID:               "org.custom",
		Name:                 "Custom",
		Category:             "organization",
		Status:               "active",
		AssignableActorTypes: []string{"human"},
		BuiltIn:              false,
		PermissionRefs:       []string{"org.read"},
	}); err != nil {
		t.Fatalf("SaveRoleDefinition: %v", err)
	}
	if err := store.SaveRoleAssignment(ctx, RoleAssignmentDTO{
		AssignmentID:       "assign-1",
		Actor:              ActorRefDTO{ActorType: "human", ActorID: "profile-1"},
		RoleID:             "org.custom",
		OrganizationNodeID: "dept-1",
		OrganizationCode:   "01.01",
		ScopeMode:          "self_and_descendants",
		Source:             "direct",
		AssignedBy:         "admin",
	}); err != nil {
		t.Fatalf("SaveRoleAssignment: %v", err)
	}

	roles, err := store.ListRoleDefinitions(ctx)
	if err != nil {
		t.Fatalf("ListRoleDefinitions: %v", err)
	}
	if len(roles) != 1 || roles[0].RoleID != "org.custom" || roles[0].PermissionRefs[0] != "org.read" {
		t.Fatalf("roles = %+v", roles)
	}
	assignments, err := store.ListRoleAssignments(ctx, RoleAssignmentQuery{OrganizationCode: "01.01"})
	if err != nil {
		t.Fatalf("ListRoleAssignments: %v", err)
	}
	if len(assignments) != 1 || assignments[0].Actor.ActorID != "profile-1" || assignments[0].ScopeMode != "self_and_descendants" {
		t.Fatalf("assignments = %+v", assignments)
	}
}

func TestQueryServiceListsEffectiveRoleAssignments(t *testing.T) {
	ctx := context.Background()
	router, closeStore := newRoleTestRouter(t, ctx)
	defer closeStore()

	roleStore := NewRoleStore(router)
	if err := roleStore.SaveRoleAssignment(ctx, RoleAssignmentDTO{
		AssignmentID:       "active-ancestor",
		Actor:              ActorRefDTO{ActorType: "human", ActorID: "profile-1"},
		RoleID:             "org.manager",
		OrganizationNodeID: "org-1",
		OrganizationCode:   "01",
		ScopeMode:          "self_and_descendants",
		Source:             "direct",
	}); err != nil {
		t.Fatalf("save active assignment: %v", err)
	}
	if err := roleStore.SaveRoleAssignment(ctx, RoleAssignmentDTO{
		AssignmentID:       "expired-direct",
		Actor:              ActorRefDTO{ActorType: "human", ActorID: "profile-2"},
		RoleID:             "org.admin",
		OrganizationNodeID: "dept-1",
		OrganizationCode:   "01.01",
		ScopeMode:          "self",
		Source:             "direct",
		ExpiresAt:          time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("save expired assignment: %v", err)
	}

	query := NewQueryService(nil, nil)
	query.SetRoleStore(roleStore)
	assignments, err := query.ListRoleAssignments(ctx, ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionRolesRead}}, RoleAssignmentQuery{
		OrganizationCode: "01.01",
		EffectiveOnly:    true,
	})
	if err != nil {
		t.Fatalf("ListRoleAssignments: %v", err)
	}
	if len(assignments.Items) != 1 {
		t.Fatalf("assignments = %+v", assignments.Items)
	}
	if assignments.Items[0].AssignmentID != "active-ancestor" || assignments.Items[0].Source != "inherited" {
		t.Fatalf("effective assignment = %+v", assignments.Items[0])
	}
}

func TestCommandServiceSyncsOrganizationOwnerRoleAssignment(t *testing.T) {
	ctx := context.Background()
	router, closeStore := newRoleTestRouter(t, ctx)
	defer closeStore()

	orgStore := NewOrganizationCatalogStore(router)
	roleStore := NewRoleStore(router)
	command := NewCommandService(&roleAuthorizationProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"profile-owner": {
			ID:           "profile-owner",
			DisplayName:  "Owner",
			PersonSource: agentcore.PersonSourceFacts{EmployeeNumber: "E001"},
			AgentKind:    agentcore.AgentKindHuman,
			Enabled:      true,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "org-1"},
			},
		},
	}})
	command.SetOrganizationCatalogStore(orgStore)
	command.SetRoleStore(roleStore)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionOrgManage}}

	if err := orgStore.SaveOrganization(ctx, OrganizationDTO{
		OrganizationID: "org-1",
		Name:           "Orrery",
		Code:           "01",
		Status:         "active",
		NodeType:       organizationNodeTypeCompany,
	}); err != nil {
		t.Fatalf("seed organization: %v", err)
	}

	updated, err := command.UpdateOrganizationOwner(ctx, viewer, UpdateOrganizationOwnerCommand{
		OrganizationNodeID: "org-1",
		OwnerProfileID:     "profile-owner",
		OwnerEmployeeNo:    "E001",
		OwnerName:          "Owner",
	})
	if err != nil {
		t.Fatalf("UpdateOrganizationOwner: %v", err)
	}
	if updated.OwnerProfileID != "profile-owner" || updated.OwnerEmployeeNo != "E001" {
		t.Fatalf("updated owner = %+v", updated)
	}

	assignments, err := roleStore.ListRoleAssignments(ctx, RoleAssignmentQuery{OrganizationCode: "01"})
	if err != nil {
		t.Fatalf("ListRoleAssignments: %v", err)
	}
	if len(assignments) != 1 {
		t.Fatalf("assignments = %+v", assignments)
	}
	assignment := assignments[0]
	if assignment.RoleID != "org.owner" || assignment.Actor.ActorID != "profile-owner" || assignment.Source != "system" || assignment.ScopeMode != "self_and_descendants" {
		t.Fatalf("owner assignment = %+v", assignment)
	}
}

func TestCommandServiceRejectsInactiveOrganizationOwnerProfiles(t *testing.T) {
	tests := []struct {
		name    string
		profile agentcore.AgentProfile
	}{
		{
			name:    "disabled profile",
			profile: agentcore.AgentProfile{ID: "profile-owner", AgentKind: agentcore.AgentKindHuman, Enabled: false},
		},
		{
			name: "talent market profile",
			profile: agentcore.AgentProfile{
				ID:        "profile-owner",
				AgentKind: agentcore.AgentKindHuman,
				Enabled:   true,
				Placement: agentcore.TalentPlacement{Status: agentcore.TalentPlacementMarket},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			router, closeStore := newRoleTestRouter(t, ctx)
			defer closeStore()

			orgStore := NewOrganizationCatalogStore(router)
			roleStore := NewRoleStore(router)
			command := NewCommandService(&roleAuthorizationProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
				"profile-owner": tt.profile,
			}})
			command.SetOrganizationCatalogStore(orgStore)
			command.SetRoleStore(roleStore)
			if err := orgStore.SaveOrganization(ctx, OrganizationDTO{
				OrganizationID: "org-1",
				Name:           "Orrery",
				Code:           "01",
				Status:         "active",
				NodeType:       organizationNodeTypeCompany,
			}); err != nil {
				t.Fatalf("seed organization: %v", err)
			}

			_, err := command.UpdateOrganizationOwner(ctx, ViewerRef{
				ViewerID:        "admin",
				PermissionScope: []string{PermissionOrgManage},
			}, UpdateOrganizationOwnerCommand{
				OrganizationNodeID: "org-1",
				OwnerProfileID:     "profile-owner",
			})
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("UpdateOrganizationOwner error = %v, want ErrInvalidRequest", err)
			}
			assignments, listErr := roleStore.ListRoleAssignments(ctx, RoleAssignmentQuery{RoleID: "org.owner"})
			if listErr != nil {
				t.Fatalf("ListRoleAssignments: %v", listErr)
			}
			if len(assignments) != 0 {
				t.Fatalf("inactive owner assignment was saved: %+v", assignments)
			}
		})
	}
}

func TestRoleStoreRejectsBuiltInRoleInBatch(t *testing.T) {
	ctx := context.Background()
	router, closeStore := newRoleTestRouter(t, ctx)
	defer closeStore()
	store := NewRoleStore(router)

	err := store.SaveRoleDefinitions(ctx, []RoleDefinitionDTO{
		{RoleID: "custom.reader", Name: "Custom reader"},
		{RoleID: "security.admin", Name: "Backdoor", PermissionRefs: []string{"permissions.manage"}},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("SaveRoleDefinitions error = %v, want ErrInvalidRequest", err)
	}
	roles, listErr := store.ListRoleDefinitions(ctx)
	if listErr != nil {
		t.Fatalf("ListRoleDefinitions: %v", listErr)
	}
	if len(roles) != 0 {
		t.Fatalf("rejected batch wrote partial roles: %+v", roles)
	}
}

func TestRoleStoreIgnoresInvalidUnrelatedLogicalTable(t *testing.T) {
	ctx := context.Background()
	router, closeStore := newRoleTestRouter(t, ctx)
	defer closeStore()
	store := NewRoleStore(router)
	if err := store.SaveRoleDefinition(ctx, RoleDefinitionDTO{RoleID: "org.custom", Name: "Custom", Status: "active"}); err != nil {
		t.Fatalf("SaveRoleDefinition: %v", err)
	}
	insertInvalidUnrelatedOperationRecord(t, ctx, router)

	roles, err := store.ListRoleDefinitions(ctx)
	if err != nil {
		t.Fatalf("ListRoleDefinitions: %v", err)
	}
	if len(roles) != 1 || roles[0].RoleID != "org.custom" {
		t.Fatalf("roles = %+v", roles)
	}
}

func TestRoleStoreListsAssignmentsAcrossRecordPages(t *testing.T) {
	ctx := context.Background()
	router, closeStore := newRoleTestRouter(t, ctx)
	defer closeStore()
	store := NewRoleStore(router)
	for i := 0; i <= db.MaxRecordPageLimit; i++ {
		id := fmt.Sprintf("assignment-%04d", i)
		if err := store.SaveRoleAssignment(ctx, RoleAssignmentDTO{
			AssignmentID: id,
			Actor:        ActorRefDTO{ActorType: "human", ActorID: "profile-1"},
			RoleID:       "member",
			ScopeMode:    RoleScopeSelf,
		}); err != nil {
			t.Fatalf("SaveRoleAssignment(%s): %v", id, err)
		}
	}

	assignments, err := store.ListRoleAssignments(ctx, RoleAssignmentQuery{})
	if err != nil {
		t.Fatalf("ListRoleAssignments: %v", err)
	}
	if len(assignments) != db.MaxRecordPageLimit+1 {
		t.Fatalf("assignment count = %d, want %d", len(assignments), db.MaxRecordPageLimit+1)
	}
	if last := assignments[len(assignments)-1].AssignmentID; last != "assignment-1000" {
		t.Fatalf("last assignment = %q, want assignment-1000", last)
	}
}

func insertInvalidUnrelatedOperationRecord(t *testing.T, ctx context.Context, router *db.Router) {
	t.Helper()
	store, table, err := router.StoreForTable("core.aaa_unrelated")
	if err != nil {
		t.Fatalf("StoreForTable: %v", err)
	}
	tx, err := store.BeginTx(ctx)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO platform_records (table_name, record_key, payload_json) VALUES (?, ?, ?)`, table, "broken", "{"); err != nil {
		_ = tx.Rollback()
		t.Fatalf("insert invalid unrelated record: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit invalid unrelated record: %v", err)
	}
}

func newRoleTestRouter(t *testing.T, ctx context.Context) (*db.Router, func()) {
	t.Helper()
	registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: t.Name()},
	}})
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	return db.NewRouter(registry), func() { _ = registry.Close() }
}
