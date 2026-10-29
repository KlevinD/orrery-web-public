package httptransport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/operations"
)

func TestPermissionRolesRouteReturnsABoundedCursorPage(t *testing.T) {
	service := &fakeOperationsService{roles: operations.RoleDefinitionPageDTO{Items: []operations.RoleDefinitionDTO{
		{RoleID: "custom.beta", Name: "Beta", Description: "Beta", Status: "active"},
		{RoleID: "custom.alpha", Name: "Alpha", Description: "Alpha", Status: "active"},
	}}}
	handler := newTestRouter(RouterOptions{Operations: service})
	req := authedRequest(http.MethodGet, "/api/permissions/roles?limit=1", "")
	req.Header.Set("X-Permission-Scope", operations.PermissionPermissionsRead)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Items []struct {
			RoleID string `json:"role_id"`
		} `json:"items"`
		NextCursor string `json:"next_cursor"`
		Revision   int64  `json:"revision"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode roles: %v", err)
	}
	if len(body.Items) != 1 || body.Items[0].RoleID != "custom.alpha" || body.NextCursor == "" {
		t.Fatalf("roles page = %+v", body)
	}
}

func TestPermissionPackagesAndDetailsAreLoadedInSeparatePages(t *testing.T) {
	service := &fakeOperationsService{roles: operations.RoleDefinitionPageDTO{Items: []operations.RoleDefinitionDTO{{
		RoleID: "custom.security", Name: "Security", Description: "Security", Status: "active", PermissionRefs: []string{operations.PermissionPermissionsRead},
	}}}}
	handler := newTestRouter(RouterOptions{Operations: service})

	packages := authedRequest(http.MethodGet, "/api/permissions/packages?role_id=custom.security&limit=100", "")
	packages.Header.Set("X-Permission-Scope", operations.PermissionPermissionsRead)
	packagesRec := httptest.NewRecorder()
	handler.ServeHTTP(packagesRec, packages)
	if packagesRec.Code != http.StatusOK {
		t.Fatalf("packages status = %d body = %s", packagesRec.Code, packagesRec.Body.String())
	}
	var packagesBody struct {
		Items []struct {
			PackageID       string `json:"package_id"`
			GrantState      string `json:"grant_state"`
			CapabilityCount int    `json:"capability_count"`
		} `json:"items"`
	}
	if err := json.NewDecoder(packagesRec.Body).Decode(&packagesBody); err != nil {
		t.Fatalf("decode packages: %v", err)
	}
	var permissionReadID string
	for _, item := range packagesBody.Items {
		if item.PackageID == "permission.read" {
			permissionReadID = item.PackageID
			if item.GrantState != "granted" || item.CapabilityCount < 2 {
				t.Fatalf("permission read package = %+v", item)
			}
		}
	}
	if permissionReadID == "" {
		t.Fatalf("permission.read package missing from %+v", packagesBody.Items)
	}

	details := authedRequest(http.MethodGet, "/api/permissions/packages/permission.read/capabilities?role_id=custom.security&limit=1", "")
	details.Header.Set("X-Permission-Scope", operations.PermissionPermissionsRead)
	detailsRec := httptest.NewRecorder()
	handler.ServeHTTP(detailsRec, details)
	if detailsRec.Code != http.StatusOK {
		t.Fatalf("details status = %d body = %s", detailsRec.Code, detailsRec.Body.String())
	}
	var detailsBody struct {
		Items []struct {
			CapabilityID string `json:"capability_id"`
			Granted      bool   `json:"granted"`
		} `json:"items"`
		NextCursor string `json:"next_cursor"`
	}
	if err := json.NewDecoder(detailsRec.Body).Decode(&detailsBody); err != nil {
		t.Fatalf("decode details: %v", err)
	}
	if len(detailsBody.Items) != 1 || !detailsBody.Items[0].Granted || detailsBody.NextCursor == "" {
		t.Fatalf("details page = %+v", detailsBody)
	}
}

func TestPermissionBatchSaveKeepsOneAtomicRevisionBoundary(t *testing.T) {
	service := &fakeOperationsService{roles: operations.RoleDefinitionPageDTO{Items: []operations.RoleDefinitionDTO{{
		RoleID: "custom.security", Name: "Security", Category: "custom", Status: "active", AssignableActorTypes: []string{"human"},
	}}}}
	handler := newTestRouter(RouterOptions{Operations: service})
	req := authedRequest(http.MethodPut, "/api/permissions/policy/batch", `{
		"expected_revision":0,
		"package_changes":[{"role_id":"custom.security","package_id":"permission.read","effect":"grant"}]
	}`)
	req.Header.Set("X-Permission-Scope", operations.PermissionPermissionsManage)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(service.putRoleDefinitions.Roles) != 1 || strings.Join(service.putRoleDefinitions.Roles[0].PermissionRefs, ",") != operations.PermissionPermissionsRead {
		t.Fatalf("batch command = %+v", service.putRoleDefinitions)
	}
}

func TestPermissionLayeredRoutesRejectOversizedPages(t *testing.T) {
	handler := newTestRouter(RouterOptions{Operations: &fakeOperationsService{}})
	req := authedRequest(http.MethodGet, "/api/permissions/roles?limit=101", "")
	req.Header.Set("X-Permission-Scope", operations.PermissionPermissionsRead)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}
