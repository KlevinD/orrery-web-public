package operations

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/platform/db"
)

func TestRoleStorePermissionPolicyRevisionAndCASBatch(t *testing.T) {
	ctx := context.Background()
	store, closeStore := newRoleStoreTestStore(t, ctx, "permission-policy-cas")
	defer closeStore()

	revision, err := store.PermissionPolicyRevision(ctx)
	if err != nil {
		t.Fatalf("PermissionPolicyRevision: %v", err)
	}
	if revision != 0 {
		t.Fatalf("initial revision = %d, want 0", revision)
	}

	role := RoleDefinitionDTO{
		RoleID:               "custom.security-reviewer",
		Name:                 "Security Reviewer",
		Category:             "custom",
		Status:               "active",
		AssignableActorTypes: []string{"human", "agent"},
		PermissionRefs:       []string{"permissions.read"},
	}
	savedRevision, err := store.ApplyPermissionRoleDefinitions(ctx, 0, []RoleDefinitionDTO{role})
	if err != nil {
		t.Fatalf("ApplyPermissionRoleDefinitions: %v", err)
	}
	if savedRevision != 1 {
		t.Fatalf("saved revision = %d, want 1", savedRevision)
	}

	role.PermissionRefs = []string{"permissions.manage"}
	if _, err := store.ApplyPermissionRoleDefinitions(ctx, 0, []RoleDefinitionDTO{role}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale batch error = %v, want ErrVersionConflict", err)
	}

	stored, found, err := store.GetRoleDefinition(ctx, role.RoleID)
	if err != nil {
		t.Fatalf("GetRoleDefinition: %v", err)
	}
	if !found || len(stored.PermissionRefs) != 1 || stored.PermissionRefs[0] != "permissions.read" {
		t.Fatalf("stale batch mutated role = %+v found=%v", stored, found)
	}
}

func TestRoleStoreListsRoleDefinitionsByStableCursorWithoutSnapshot(t *testing.T) {
	ctx := context.Background()
	store, closeStore := newRoleStoreTestStore(t, ctx, "permission-role-page")
	defer closeStore()

	for _, roleID := range []string{"custom.alpha", "custom.beta", "custom.gamma"} {
		if err := store.SaveRoleDefinition(ctx, RoleDefinitionDTO{
			RoleID: roleID, Name: roleID, Category: "custom", Status: "active",
			AssignableActorTypes: []string{"human"},
		}); err != nil {
			t.Fatalf("SaveRoleDefinition(%s): %v", roleID, err)
		}
	}

	page, err := store.ListRoleDefinitionsPage(ctx, "custom.alpha", 1)
	if err != nil {
		t.Fatalf("ListRoleDefinitionsPage: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].RoleID != "custom.beta" || !page.HasMore {
		t.Fatalf("page = %+v", page)
	}
}

func TestQueryServicePaginatesEffectivePermissionScopesWithinOneAssignment(t *testing.T) {
	ctx := context.Background()
	store, closeStore := newRoleStoreTestStore(t, ctx, "permission-effective-page")
	defer closeStore()
	if err := store.SaveRoleDefinition(ctx, RoleDefinitionDTO{
		RoleID: "custom.security", Name: "Security", Category: "custom", Status: "active",
		AssignableActorTypes: []string{"human"}, PermissionRefs: []string{"permissions.manage", "permissions.read"},
	}); err != nil {
		t.Fatalf("SaveRoleDefinition: %v", err)
	}
	if err := store.SaveRoleAssignment(ctx, RoleAssignmentDTO{
		AssignmentID: "assignment-1", Actor: ActorRefDTO{ActorType: "human", ActorID: "operator"},
		RoleID: "custom.security", ScopeMode: RoleScopePlatform,
	}); err != nil {
		t.Fatalf("SaveRoleAssignment: %v", err)
	}
	query := NewQueryService(nil, nil)
	query.SetRoleStore(store)
	viewer := ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionPermissionsRead}}

	first, err := query.ListPermissionEffectiveGrants(ctx, viewer, PermissionEffectiveGrantQuery{Limit: 1})
	if err != nil {
		t.Fatalf("first ListPermissionEffectiveGrants: %v", err)
	}
	if len(first.Items) != 1 || first.Items[0].Scope != "permissions.manage" || !first.HasMore || first.NextScopeOffset != 1 {
		t.Fatalf("first page = %+v", first)
	}
	second, err := query.ListPermissionEffectiveGrants(ctx, viewer, PermissionEffectiveGrantQuery{
		AssignmentID: first.NextAssignmentID, ScopeOffset: first.NextScopeOffset, Limit: 1,
	})
	if err != nil {
		t.Fatalf("second ListPermissionEffectiveGrants: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].Scope != "permissions.read" || second.HasMore {
		t.Fatalf("second page = %+v", second)
	}
}

func newRoleStoreTestStore(t *testing.T, ctx context.Context, name string) (*RoleStore, func()) {
	t.Helper()
	registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: name},
	}})
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	return NewRoleStore(db.NewRouter(registry)), func() { _ = registry.Close() }
}
