package operations

import (
	"context"
	"testing"

	"orrery_v2/internal/platform/db"
)

func TestOrganizationCatalogStorePersistsOrganizationNodeMetadata(t *testing.T) {
	ctx := context.Background()
	registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: t.Name()},
	}})
	if err != nil {
		t.Fatalf("open registry: %v", err)
	}
	defer registry.Close()

	store := NewOrganizationCatalogStore(db.NewRouter(registry))
	if err := store.SaveOrganization(ctx, OrganizationDTO{
		OrganizationID:      "org-1",
		Name:                "Orrery",
		DefaultDepartmentID: "dept-ops",
		DefaultPositionID:   "pos-operator",
		Code:                "01",
		Status:              "active",
		OwnerProfileID:      "profile-owner",
		OwnerEmployeeNo:     "E001",
		OwnerName:           "Owner",
		NodeType:            organizationNodeTypeCompany,
	}); err != nil {
		t.Fatalf("save organization: %v", err)
	}
	if err := store.SaveDepartment(ctx, DepartmentDTO{
		DepartmentID:      "dept-ops",
		OrganizationID:    "org-1",
		Name:              "Operations",
		DefaultPositionID: "pos-operator",
		Code:              "0101",
		Status:            "inactive",
		OwnerName:         "Ops Owner",
		NodeType:          organizationNodeTypeDepartment,
	}); err != nil {
		t.Fatalf("save department: %v", err)
	}
	exact, found, err := store.GetOrganization(ctx, "org-1")
	if err != nil || !found || exact.OrganizationID != "org-1" || exact.Name != "Orrery" {
		t.Fatalf("get organization = %+v found=%t err=%v", exact, found, err)
	}
	if _, found, err := store.GetOrganization(ctx, "missing"); err != nil || found {
		t.Fatalf("get missing organization found=%t err=%v", found, err)
	}

	graph, err := store.ListOrganizations(ctx, OrgGraphQuery{OrganizationID: "org-1"})
	if err != nil {
		t.Fatalf("list organizations: %v", err)
	}
	if len(graph.Organizations) != 1 {
		t.Fatalf("organizations = %+v", graph.Organizations)
	}
	organization := graph.Organizations[0]
	if organization.Code != "01" || organization.DisplayCode != "01" || organization.OwnerEmployeeNo != "E001" || organization.NodeType != organizationNodeTypeCompany {
		t.Fatalf("organization metadata = %+v", organization)
	}
	if len(graph.Departments) != 1 {
		t.Fatalf("departments = %+v", graph.Departments)
	}
	department := graph.Departments[0]
	if department.Code != "0101" || department.DisplayCode != "01.01" || department.Status != "inactive" || department.OwnerName != "Ops Owner" || department.NodeType != organizationNodeTypeDepartment {
		t.Fatalf("department metadata = %+v", department)
	}
}

func TestOrganizationCatalogStoreIgnoresInvalidUnrelatedLogicalTable(t *testing.T) {
	ctx := context.Background()
	router, closeStore := newRoleTestRouter(t, ctx)
	defer closeStore()
	store := NewOrganizationCatalogStore(router)
	if err := store.SaveOrganization(ctx, OrganizationDTO{
		OrganizationID: "org-1",
		Name:           "Orrery",
		Code:           "01",
		Status:         "active",
	}); err != nil {
		t.Fatalf("SaveOrganization: %v", err)
	}
	insertInvalidUnrelatedOperationRecord(t, ctx, router)

	graph, err := store.ListOrganizations(ctx, OrgGraphQuery{})
	if err != nil {
		t.Fatalf("ListOrganizations: %v", err)
	}
	if len(graph.Organizations) != 1 || graph.Organizations[0].OrganizationID != "org-1" {
		t.Fatalf("organizations = %+v", graph.Organizations)
	}
}
