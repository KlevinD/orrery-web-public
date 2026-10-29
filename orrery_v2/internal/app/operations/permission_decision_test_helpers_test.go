package operations

import (
	"context"
	"testing"

	"orrery_v2/internal/app/rebac"
)

func newPermissionDecisionService(t *testing.T, ctx context.Context) (*QueryService, *RoleStore) {
	t.Helper()
	router, closeStore := newRoleTestRouter(t, ctx)
	t.Cleanup(closeStore)
	roleStore := NewRoleStore(router)
	orgStore := NewOrganizationCatalogStore(router)
	if err := orgStore.SaveOrganization(ctx, OrganizationDTO{
		OrganizationID: "org-1",
		Name:           "Orrery",
		Code:           "01",
		Status:         organizationStatusActive,
		OwnerProfileID: "root-owner",
		NodeType:       organizationNodeTypeCompany,
	}); err != nil {
		t.Fatalf("seed org: %v", err)
	}
	if err := orgStore.SaveDepartment(ctx, DepartmentDTO{
		DepartmentID:       "dept-ai",
		OrganizationID:     "org-1",
		Name:               "AI",
		ParentDepartmentID: "",
		Code:               "0101",
		Status:             organizationStatusActive,
		OwnerProfileID:     "dept-owner",
		NodeType:           organizationNodeTypeDepartment,
	}); err != nil {
		t.Fatalf("seed department: %v", err)
	}
	service := NewQueryService(nil, nil)
	service.SetRoleStore(roleStore)
	service.SetOrganizationCatalogStore(orgStore)
	service.SetReBACStore(rebac.NewMemoryStore())
	return service, roleStore
}

func savePermissionDecisionRelation(t *testing.T, ctx context.Context, service *QueryService, tuple rebac.RelationTuple) {
	t.Helper()
	if service == nil || service.rebac == nil {
		t.Fatal("rebac store is not configured")
	}
	writer, ok := service.rebac.(interface {
		SaveRelationTuple(context.Context, rebac.RelationTuple) error
	})
	if !ok {
		t.Fatal("rebac store does not support test writes")
	}
	if err := writer.SaveRelationTuple(ctx, tuple); err != nil {
		t.Fatalf("save rebac relation: %v", err)
	}
}

func permissionDecisionViewer() ViewerRef {
	return ViewerRef{ViewerID: "admin", PermissionScope: []string{PermissionPermissionsRead}}
}

type permissionDecisionAuditStore struct {
	events []AuditEventDTO
}

func (s *permissionDecisionAuditStore) ListAuditEvents(ctx context.Context, query AuditLogQuery) (AuditEventPageDTO, error) {
	items := make([]AuditEventDTO, 0, len(s.events))
	for _, event := range s.events {
		if query.EntityType != "" && event.EntityType != query.EntityType {
			continue
		}
		items = append(items, event)
	}
	return AuditEventPageDTO{Events: items, Page: PageDTO{Total: len(items), Returned: len(items)}}, nil
}

func assertPermissionDecision(t *testing.T, decision PermissionDecisionDTO, wantDecision string, wantSource string) {
	t.Helper()
	if decision.Decision != wantDecision {
		t.Fatalf("decision = %+v, want %s", decision, wantDecision)
	}
	if len(decision.Sources) == 0 {
		t.Fatalf("decision sources are empty: %+v", decision)
	}
	for _, source := range decision.Sources {
		if source.SourceType == wantSource {
			return
		}
	}
	t.Fatalf("decision sources = %+v, want source %s", decision.Sources, wantSource)
}
