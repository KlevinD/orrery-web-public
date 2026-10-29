package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/accesscontrol"
	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
)

func TestPermissionCapabilitiesExposeLocalizedReadWriteCatalog(t *testing.T) {
	handler := newTestRouter(RouterOptions{})
	req := authedRequest(http.MethodGet, "/api/permissions/capabilities", "")
	req.Header.Set("X-Permission-Scope", operations.PermissionPermissionsRead)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body PermissionCapabilitiesDTO
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode capabilities: %v", err)
	}
	capability := findCapability(t, body.Capabilities, http.MethodGet, "/api/permissions/matrix", operations.PermissionPermissionsRead)
	if capability.LabelZh != "权限查看" || capability.LabelEn != "Read permission matrix" || capability.CategoryZh != "权限" || capability.CategoryEn != "Permissions" {
		t.Fatalf("capability localized labels = %+v", capability)
	}
	if capability.AccessMode != "read" {
		t.Fatalf("access mode = %q, want read for GET permission matrix: %+v", capability.AccessMode, capability)
	}
	manage := findCapability(t, body.Capabilities, http.MethodPost, "/api/workforce/members", operations.PermissionWorkforceManage)
	if manage.LabelZh != "人才编辑" || manage.LabelEn != "Create workforce member" {
		t.Fatalf("manage localized labels = %+v", manage)
	}
	if manage.AccessMode != "write" {
		t.Fatalf("access mode = %q, want write for workforce.manage route: %+v", manage.AccessMode, manage)
	}
}

func TestAccessPolicyRoleGrantAuthorizesRegisteredRouteWithoutHeaderScope(t *testing.T) {
	service := &fakeOperationsService{
		workforce: operations.WorkforcePageDTO{Members: []operations.WorkforceMemberDTO{{AgentID: "agent-ada"}}},
	}
	policy := accesscontrol.NewService(accesscontrol.NewMemoryStore(accesscontrol.PolicyDTO{
		Roles: []accesscontrol.RoleDTO{{
			RoleID:  "ops-viewer",
			LabelZh: "运营查看员",
			LabelEn: "Operations Viewer",
		}},
		RoleAssignments: []accesscontrol.RoleAssignmentDTO{{
			PrincipalKind: "user",
			PrincipalID:   "operator",
			RoleID:        "ops-viewer",
		}},
		Grants: []accesscontrol.PermissionGrantDTO{{
			SubjectKind:  "role",
			SubjectID:    "ops-viewer",
			CapabilityID: "get.api.workforce.members.workforce.read",
		}},
	}))
	handler := newTestRouter(RouterOptions{Operations: service, AccessControl: policy})
	req := authedRequest(http.MethodGet, "/api/workforce/members", "")
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Del("X-Permission-Scope")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(service.lastViewer.PermissionScope) != 1 || service.lastViewer.PermissionScope[0] != operations.PermissionWorkforceRead {
		t.Fatalf("policy-authorized route should pass required scope to service viewer: %+v", service.lastViewer)
	}
}

func TestAccessPolicyRootRoleCanReachOperationsServiceWithoutHeaderScope(t *testing.T) {
	service := &fakeOperationsService{
		workforce: operations.WorkforcePageDTO{Members: []operations.WorkforceMemberDTO{{AgentID: "agent-ada"}}},
	}
	policy := accesscontrol.NewService(accesscontrol.NewMemoryStore(accesscontrol.PolicyDTO{
		Roles: []accesscontrol.RoleDTO{{RoleID: "root"}},
		RoleAssignments: []accesscontrol.RoleAssignmentDTO{{
			PrincipalKind: "user",
			PrincipalID:   "root",
			RoleID:        "root",
		}},
		Grants: []accesscontrol.PermissionGrantDTO{{
			SubjectKind: "role",
			SubjectID:   "root",
			Scope:       operations.PermissionWorkforceRead,
		}},
	}))
	handler := newTestRouter(RouterOptions{Operations: service, AccessControl: policy})
	req := authedRequest(http.MethodGet, "/api/workforce/members", "")
	req.Header.Set("X-User-ID", "root")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Del("X-Permission-Scope")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(service.lastViewer.PermissionScope) != 1 || service.lastViewer.PermissionScope[0] != operations.PermissionWorkforceRead {
		t.Fatalf("root policy grant should hydrate workforce.read into service viewer: %+v", service.lastViewer)
	}
}

func TestAccessPolicyRejectsRegisteredRouteWhenUserAndRolesLackGrant(t *testing.T) {
	service := &fakeOperationsService{
		workforce: operations.WorkforcePageDTO{Members: []operations.WorkforceMemberDTO{{AgentID: "agent-ada"}}},
	}
	policy := accesscontrol.NewService(accesscontrol.NewMemoryStore(accesscontrol.PolicyDTO{
		Roles: []accesscontrol.RoleDTO{{
			RoleID:  "ops-viewer",
			LabelZh: "运营查看员",
			LabelEn: "Operations Viewer",
		}},
	}))
	handler := newTestRouter(RouterOptions{Operations: service, AccessControl: policy})
	req := authedRequest(http.MethodGet, "/api/workforce/members", "")
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Del("X-Permission-Scope")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestAuthorizationFacadeAuthorizesRegisteredRouteWithoutHeaderScope(t *testing.T) {
	service := &fakeOperationsService{
		workforce: operations.WorkforcePageDTO{Members: []operations.WorkforceMemberDTO{{AgentID: "agent-ada"}}},
	}
	authz := &fakeHTTPAuthorizationService{}
	roles := &fakeHTTPRoleAuthorizationService{allowed: true}
	bearer := humanRoleTestBearerAuth()
	handler := newTestRouter(RouterOptions{Operations: service, BearerAuth: bearer, Authorization: authz, RoleAuthorization: roles})
	req := humanRoleTestRequest(http.MethodGet, "/api/workforce/members", "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if roles.permission != operations.PermissionWorkforceRead || len(roles.objects) != 1 || roles.objects[0] != (identity.ObjectRef{Type: "platform", ID: "global", TenantID: "global"}) {
		t.Fatalf("role authorization call permission=%q objects=%+v", roles.permission, roles.objects)
	}
	if len(service.lastViewer.PermissionScope) != 1 || service.lastViewer.PermissionScope[0] != operations.PermissionWorkforceRead {
		t.Fatalf("authorization-authorized route should hydrate workforce.read into service viewer: %+v", service.lastViewer)
	}
}

func TestAuthenticatedTenantDoesNotScopeUnfilteredOrganizationGraph(t *testing.T) {
	service := &fakeOperationsService{graph: operations.OrgGraphDTO{}}
	roles := &fakeHTTPRoleAuthorizationService{allowed: true}
	handler := newTestRouter(RouterOptions{
		Operations: service,
		BearerAuth: &fakeBearerAuthService{principal: identity.Principal{
			SubjectProvider: "test",
			Subject:         "ding",
			UserID:          "ding",
			Username:        "ding",
			AgentProfileID:  "H000001",
			ViewerKind:      identity.ViewerKindUser,
			TenantID:        "minvia",
		}},
		Authorization:     &fakeHTTPAuthorizationService{},
		RoleAuthorization: roles,
	})
	req := humanRoleTestRequest(http.MethodGet, "/api/org/graph?include_members=true", "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(roles.objects) != 1 || roles.objects[0] != (identity.ObjectRef{Type: "platform", ID: "global", TenantID: "global"}) {
		t.Fatalf("unfiltered organization graph authorization objects = %+v", roles.objects)
	}
	if service.orgQuery.OrganizationID != "" || !service.orgQuery.IncludeMembers {
		t.Fatalf("organization graph query = %+v", service.orgQuery)
	}
}

func TestRouteAuthorizationUsesSystemPrincipalForBreakglassSuperadmin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/missions/mission-1/discussion", nil)
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         "superadmin",
		UserID:          "superadmin",
		Username:        "superadmin",
		AgentProfileID:  agentcore.SystemSuperadminPrincipalID,
		ViewerKind:      identity.ViewerKindUser,
	}))
	viewer := missioncontrol.ViewerRef{
		ViewerID:               agentcore.SystemSuperadminPrincipalID,
		ViewerKind:             missioncontrol.ViewerKindSystem,
		AuthorizationSubjectID: agentcore.SystemSuperadminPrincipalID,
	}

	principal := principalForRouteAuthorization(req, viewer)
	if principal.ViewerKind != identity.ViewerKindSystem || principal.UserID != agentcore.SystemSuperadminPrincipalID || principal.AgentProfileID != "" {
		t.Fatalf("route principal = %+v", principal)
	}
}

func TestAvatarMutationAllowsAuthenticatedUserForOwnProfile(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
		request    func(*testing.T, string, *LocalWorkforceAvatarStore) *http.Request
	}{
		{
			name:       "upload",
			method:     http.MethodPut,
			path:       "/api/workforce/members/human-operator/avatar",
			wantStatus: http.StatusOK,
			request: func(t *testing.T, path string, _ *LocalWorkforceAvatarStore) *http.Request {
				return humanRoleAvatarUploadRequest(t, path, testWorkforceAvatarPNG(t))
			},
		},
		{
			name:       "select default",
			method:     http.MethodPut,
			path:       "/api/workforce/members/human-operator/avatar/default",
			wantStatus: http.StatusOK,
			request: func(t *testing.T, path string, store *LocalWorkforceAvatarStore) *http.Request {
				defaults, err := store.ListDefaults(context.Background())
				if err != nil || len(defaults) == 0 {
					t.Fatalf("list defaults: items=%d error=%v", len(defaults), err)
				}
				req := humanRoleTestRequest(http.MethodPut, path, fmt.Sprintf(`{"default_id":%q}`, defaults[0].ID))
				req.Header.Set("Content-Type", "application/json")
				return req
			},
		},
		{
			name:       "reset",
			method:     http.MethodDelete,
			path:       "/api/workforce/members/human-operator/avatar",
			wantStatus: http.StatusNoContent,
			request: func(_ *testing.T, path string, _ *LocalWorkforceAvatarStore) *http.Request {
				return humanRoleTestRequest(http.MethodDelete, path, "")
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roles := &fakeHTTPRoleAuthorizationService{}
			store := NewLocalWorkforceAvatarStore(t.TempDir())
			handler := newTestRouter(RouterOptions{
				Operations:        &fakeOperationsService{},
				WorkforceAvatars:  store,
				BearerAuth:        humanRoleTestBearerAuth(),
				Authorization:     &fakeHTTPAuthorizationService{},
				RoleAuthorization: roles,
			})
			req := tt.request(t, tt.path, store)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("%s %s status = %d body = %s", tt.method, tt.path, rec.Code, rec.Body.String())
			}
			if len(roles.permissions) != 0 {
				t.Fatalf("self-service route called role authorization with %v", roles.permissions)
			}
		})
	}
}

func TestAvatarMutationRejectsNonSelfTargetsAndAgentPrincipals(t *testing.T) {
	tests := []struct {
		name      string
		targetID  string
		principal identity.Principal
	}{
		{
			name:     "user targets another profile",
			targetID: "human-reviewer",
			principal: identity.Principal{
				SubjectProvider: "test",
				Subject:         "operator",
				UserID:          "operator",
				AgentProfileID:  "human-operator",
				ViewerKind:      identity.ViewerKindUser,
			},
		},
		{
			name:     "agent targets its own profile",
			targetID: "agent-worker",
			principal: identity.Principal{
				SubjectProvider: "test",
				Subject:         "agent-worker",
				UserID:          "agent-worker",
				AgentProfileID:  "agent-worker",
				ViewerKind:      identity.ViewerKindAgent,
			},
		},
		{
			name:     "user has no bound profile",
			targetID: "human-operator",
			principal: identity.Principal{
				SubjectProvider: "test",
				Subject:         "operator",
				UserID:          "operator",
				ViewerKind:      identity.ViewerKindUser,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			roles := &fakeHTTPRoleAuthorizationService{}
			handler := newTestRouter(RouterOptions{
				Operations:        &fakeOperationsService{},
				BearerAuth:        &fakeBearerAuthService{principal: tt.principal},
				Authorization:     &fakeHTTPAuthorizationService{},
				RoleAuthorization: roles,
			})
			req := httptest.NewRequest(http.MethodDelete, "/api/workforce/members/"+tt.targetID+"/avatar", nil)
			req.Header.Set("Authorization", "Bearer human-role-test-token")
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
			}
			if len(roles.permissions) != 1 || roles.permissions[0] != operations.PermissionWorkforceManage {
				t.Fatalf("normal role authorization was not enforced: %v", roles.permissions)
			}
		})
	}
}

func TestNonAvatarWorkforceMutationDoesNotUseSelfService(t *testing.T) {
	roles := &fakeHTTPRoleAuthorizationService{}
	handler := newTestRouter(RouterOptions{
		Operations:        &fakeOperationsService{},
		BearerAuth:        humanRoleTestBearerAuth(),
		Authorization:     &fakeHTTPAuthorizationService{},
		RoleAuthorization: roles,
	})
	req := humanRoleTestRequest(http.MethodPatch, "/api/workforce/members/human-operator", `{}`)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(roles.permissions) != 1 || roles.permissions[0] != operations.PermissionWorkforceManage {
		t.Fatalf("non-avatar mutation did not enforce role authorization: %v", roles.permissions)
	}
}

func humanRoleAvatarUploadRequest(t *testing.T, path string, content []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "avatar.png")
	if err != nil {
		t.Fatalf("create avatar form file: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write avatar form file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close avatar multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPut, path, &body)
	req.Header.Set("Authorization", "Bearer human-role-test-token")
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestAuthorizationRouteCheckBuildsObjectRefsForHighValueRoutes(t *testing.T) {
	tests := []struct {
		name       string
		req        *http.Request
		capability PermissionCapabilityDTO
		action     string
		object     identity.ObjectRef
	}{
		{
			name: "mission path object",
			req:  httptest.NewRequest(http.MethodGet, "/api/missions/mission-1/control", nil),
			capability: PermissionCapabilityDTO{
				Method: http.MethodGet,
				Path:   "/api/missions/{mission_id}/control",
				Domain: "mission",
				Action: "read",
				Scope:  permissionMissionRead,
			},
			action: "mission.view",
			object: identity.ObjectRef{Type: "mission", ID: "mission-1", TenantID: "global"},
		},
		{
			name: "project path object",
			req:  httptest.NewRequest(http.MethodPatch, "/api/projects/project-1", nil),
			capability: PermissionCapabilityDTO{
				Method: http.MethodPatch,
				Path:   "/api/projects/{project_id}",
				Domain: "project",
				Action: "update",
				Scope:  permissionMissionWrite,
			},
			action: "project.edit",
			object: identity.ObjectRef{Type: "project", ID: "project-1", TenantID: "global"},
		},
		{
			name: "resource path object",
			req:  httptest.NewRequest(http.MethodGet, "/api/resources/mcp-servers/mcp.github?tenant_id=tenant-1", nil),
			capability: PermissionCapabilityDTO{
				Method: http.MethodGet,
				Path:   "/api/resources/mcp-servers/{tool_id}",
				Domain: "resource",
				Action: "read",
				Scope:  "resource.read",
			},
			action: "resource.view",
			object: identity.ObjectRef{Type: "resource", ID: "mcp.github", TenantID: "tenant-1"},
		},
		{
			name: "tool call body object",
			req:  httptest.NewRequest(http.MethodPost, "/api/resources/tools/call", bytes.NewBufferString(`{"tool_id":"mcp.github.search_issues"}`)),
			capability: PermissionCapabilityDTO{
				Method: http.MethodPost,
				Path:   "/api/resources/tools/call",
				Domain: "tool",
				Action: "call",
				Scope:  operations.PermissionToolCall,
			},
			action: "resource.use",
			object: identity.ObjectRef{Type: "resource", ID: "mcp.github.search_issues", TenantID: "global"},
		},
		{
			name: "platform fallback object",
			req:  httptest.NewRequest(http.MethodGet, "/api/workforce/members", nil),
			capability: PermissionCapabilityDTO{
				Method: http.MethodGet,
				Path:   "/api/workforce/members",
				Domain: "operations",
				Action: "read",
				Scope:  operations.PermissionOperationsRead,
			},
			action: operations.PermissionOperationsRead,
			object: identity.ObjectRef{Type: "platform", ID: "global", TenantID: "global"},
		},
		{
			name: "authenticated unfiltered organization graph is platform scoped",
			req: func() *http.Request {
				req := httptest.NewRequest(http.MethodGet, "/api/org/graph?include_members=true", nil)
				req.Header.Set("X-Tenant-ID", "minvia")
				req.Header.Set("X-Organization-ID", "minvia")
				return req
			}(),
			capability: PermissionCapabilityDTO{
				Method: http.MethodGet,
				Path:   "/api/org/graph",
				Domain: "organization",
				Action: "read",
				Scope:  operations.PermissionOrgRead,
			},
			action: operations.PermissionOrgRead,
			object: identity.ObjectRef{Type: "platform", ID: "global", TenantID: "global"},
		},
		{
			name: "explicitly scoped organization graph stays organization scoped",
			req:  httptest.NewRequest(http.MethodGet, "/api/org/graph?organization_id=org-1", nil),
			capability: PermissionCapabilityDTO{
				Method: http.MethodGet,
				Path:   "/api/org/graph",
				Domain: "organization",
				Action: "read",
				Scope:  operations.PermissionOrgRead,
			},
			action: "organization.read",
			object: identity.ObjectRef{Type: "organization", ID: "org-1", TenantID: "org-1"},
		},
		{
			name: "discussion path object",
			req:  httptest.NewRequest(http.MethodGet, "/api/discussions/discussion-1/messages", nil),
			capability: PermissionCapabilityDTO{
				Method: http.MethodGet,
				Path:   "/api/discussions/{discussion_id}/messages",
				Domain: "discussion",
				Action: "read",
				Scope:  permissionDiscussionRead,
			},
			action: "discussion.read",
			object: identity.ObjectRef{Type: "discussion", ID: "discussion-1", TenantID: "global"},
		},
		{
			name: "mission discussion path object",
			req:  httptest.NewRequest(http.MethodGet, "/api/missions/mission-1/discussion", nil),
			capability: PermissionCapabilityDTO{
				Method: http.MethodGet,
				Path:   "/api/missions/{mission_id}/discussion",
				Domain: "discussion",
				Action: "read",
				Scope:  permissionDiscussionRead,
			},
			action: "mission.view",
			object: identity.ObjectRef{Type: "mission", ID: "mission-1", TenantID: "global"},
		},
		{
			name: "mission discussion write uses parent visibility guard",
			req:  httptest.NewRequest(http.MethodPost, "/api/missions/mission-1/discussion/messages", strings.NewReader(`{"discussion_group_id":"discussion-1","content":"hello"}`)),
			capability: PermissionCapabilityDTO{
				Method: http.MethodPost,
				Path:   "/api/missions/{mission_id}/discussion/messages",
				Domain: "discussion",
				Action: "write",
				Scope:  permissionDiscussionWrite,
			},
			action: "mission.view",
			object: identity.ObjectRef{Type: "mission", ID: "mission-1", TenantID: "global"},
		},
		{
			name: "department path object",
			req:  httptest.NewRequest(http.MethodDelete, "/api/org/units/dept-1?tenant_id=org-1", nil),
			capability: PermissionCapabilityDTO{
				Method: http.MethodDelete,
				Path:   "/api/org/units/{department_id}",
				Domain: "organization",
				Action: "manage",
				Scope:  operations.PermissionOrgManage,
			},
			action: "department.manage",
			object: identity.ObjectRef{Type: "department", ID: "dept-1", TenantID: "org-1"},
		},
		{
			name: "development project path object",
			req:  httptest.NewRequest(http.MethodPatch, "/api/development-projects/dev-1", strings.NewReader(`{"name":"Updated"}`)),
			capability: PermissionCapabilityDTO{
				Method: http.MethodPatch,
				Path:   "/api/development-projects/{development_project_id}",
				Domain: "development_project",
				Action: "write",
				Scope:  permissionDevelopmentWrite,
			},
			action: "development_project.edit",
			object: identity.ObjectRef{Type: "development_project", ID: "dev-1", TenantID: "global"},
		},
		{
			name: "scm connection path object",
			req:  httptest.NewRequest(http.MethodGet, "/api/scm-connections/conn-1/repositories", nil),
			capability: PermissionCapabilityDTO{
				Method: http.MethodGet,
				Path:   "/api/scm-connections/{connection_id}/repositories",
				Domain: "scm_connection",
				Action: "use",
				Scope:  permissionConnectionUse,
			},
			action: "scm_connection.use",
			object: identity.ObjectRef{Type: "scm_connection", ID: "conn-1", TenantID: "global"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			object := authorizationObjectForCapability(tt.req, tt.capability)
			if object != tt.object {
				t.Fatalf("object = %+v, want %+v", object, tt.object)
			}
			if action := authorizationActionForCapability(tt.capability, object); action != tt.action {
				t.Fatalf("action = %q, want %q", action, tt.action)
			}
			if tt.name == "tool call body object" {
				var body map[string]string
				if err := json.NewDecoder(tt.req.Body).Decode(&body); err != nil {
					t.Fatalf("request body was not restored: %v", err)
				}
				if body["tool_id"] != "mcp.github.search_issues" {
					t.Fatalf("restored body = %+v", body)
				}
			}
		})
	}
}

func TestPermissionRegistryPrefersExactRouteOverTemplateRoute(t *testing.T) {
	router := &Router{permissions: defaultPermissionCapabilities()}
	tests := []struct {
		name     string
		method   string
		path     string
		wantPath string
	}{
		{
			name:     "mcp discovery is not treated as tool id",
			method:   http.MethodGet,
			path:     "/api/resources/mcp-servers/discover",
			wantPath: "/api/resources/mcp-servers/discover",
		},
		{
			name:     "personal memory profile is not treated as memory profile id",
			method:   http.MethodGet,
			path:     "/api/resources/memory-profiles/personal",
			wantPath: "/api/resources/memory-profiles/personal",
		},
		{
			name:     "real mcp server id still matches template",
			method:   http.MethodGet,
			path:     "/api/resources/mcp-servers/mcp.github",
			wantPath: "/api/resources/mcp-servers/{tool_id}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capabilities := router.permissionsForRequest(httptest.NewRequest(tt.method, tt.path, nil))
			if len(capabilities) != 1 {
				t.Fatalf("capabilities = %+v, want one", capabilities)
			}
			if capabilities[0].Path != tt.wantPath {
				t.Fatalf("capability path = %q, want %q", capabilities[0].Path, tt.wantPath)
			}
		})
	}
}

func TestAuthorizationFacadeDeniesRegisteredRouteWithoutFallback(t *testing.T) {
	service := &fakeOperationsService{
		workforce: operations.WorkforcePageDTO{Members: []operations.WorkforceMemberDTO{{AgentID: "agent-ada"}}},
	}
	authz := &fakeHTTPAuthorizationService{err: authorizationapp.ErrDenied}
	roles := &fakeHTTPRoleAuthorizationService{}
	handler := newTestRouter(RouterOptions{Operations: service, Authorization: authz, RoleAuthorization: roles})
	req := authedRequest(http.MethodGet, "/api/workforce/members", "")
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Del("X-Permission-Scope")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.lastViewer.ViewerID != "" {
		t.Fatalf("handler should not be reached, viewer=%+v", service.lastViewer)
	}
}

func TestAuthorizationFacadeRequiresPlatformRoleForPlatformRoute(t *testing.T) {
	authz := &fakeHTTPAuthorizationService{}
	roles := &fakeHTTPRoleAuthorizationService{}
	bearer := humanRoleTestBearerAuth()
	handler := newTestRouter(RouterOptions{BearerAuth: bearer, Authorization: authz, RoleAuthorization: roles})
	req := humanRoleTestRequest(http.MethodGet, "/api/permissions/capabilities", "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(roles.objects) != 1 || roles.objects[0].Type != "platform" || roles.permission != operations.PermissionPermissionsRead {
		t.Fatalf("platform role authorization = permission %q objects %+v", roles.permission, roles.objects)
	}
}

func TestAuthorizationFacadePreservesKratosPlatformAdminIdentityForPermissionRoles(t *testing.T) {
	service := &fakeOperationsService{roles: operations.RoleDefinitionPageDTO{}}
	roles := &fakeHTTPRoleAuthorizationService{allowed: true}
	handler := newTestRouter(RouterOptions{
		Operations: service,
		BearerAuth: &fakeBearerAuthService{principal: identity.Principal{
			SubjectProvider: identity.ProviderKratos,
			Subject:         "kratos-quzheng",
			UserID:          "quzheng",
			Username:        "quzheng",
			DisplayName:     "屈正",
			AgentProfileID:  "H000002",
			ViewerKind:      identity.ViewerKindUser,
		}},
		Authorization:     &fakeHTTPAuthorizationService{},
		RoleAuthorization: roles,
	})
	req := httptest.NewRequest(http.MethodGet, "/api/permissions/roles?limit=20", nil)
	req.Header.Set("Authorization", "Bearer platform-admin-test")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if roles.permission != operations.PermissionPermissionsRead {
		t.Fatalf("route role permission = %q", roles.permission)
	}
	if service.lastViewer.AgentProfileID != "H000002" || !viewerHasScope(service.lastViewer.PermissionScope, operations.PermissionPermissionsRead, operations.PermissionPermissionsRead) {
		t.Fatalf("operations viewer = %+v", service.lastViewer)
	}
}

func TestPermissionRolesRejectsForgedScopeWithoutPlatformRole(t *testing.T) {
	handler := newTestRouter(RouterOptions{
		BearerAuth: &fakeBearerAuthService{principal: identity.Principal{
			SubjectProvider: identity.ProviderKratos,
			Subject:         "kratos-basic-user",
			UserID:          "basic-user",
			Username:        "basic-user",
			AgentProfileID:  "H000099",
			ViewerKind:      identity.ViewerKindUser,
		}},
		Authorization:     &fakeHTTPAuthorizationService{},
		RoleAuthorization: &fakeHTTPRoleAuthorizationService{allowed: false},
	})
	req := httptest.NewRequest(http.MethodGet, "/api/permissions/roles?limit=20", nil)
	req.Header.Set("Authorization", "Bearer basic-user-test")
	req.Header.Set("X-Permission-Scope", operations.PermissionPermissionsManage)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestAuthorizationFacadeFailsClosedWithoutPlatformRoleAuthorizer(t *testing.T) {
	bearer := humanRoleTestBearerAuth()
	handler := newTestRouter(RouterOptions{BearerAuth: bearer, Authorization: &fakeHTTPAuthorizationService{}})
	req := humanRoleTestRequest(http.MethodGet, "/api/permissions/capabilities", "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestAuthorizationFacadeUsesSingleSpecificCapabilityWithoutHeaderFallback(t *testing.T) {
	service := &fakeOperationsService{
		workforce: operations.WorkforcePageDTO{Members: []operations.WorkforceMemberDTO{{AgentID: "agent-ada"}}},
	}
	authz := &fakeHTTPAuthorizationService{}
	roles := &fakeHTTPRoleAuthorizationService{
		allowedByPermission: map[string]bool{
			operations.PermissionWorkforceRead: true,
		},
	}
	bearer := humanRoleTestBearerAuth()
	handler := newTestRouter(RouterOptions{Operations: service, BearerAuth: bearer, Authorization: authz, RoleAuthorization: roles})
	req := humanRoleTestRequest(http.MethodGet, "/api/workforce/members", "")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if strings.Join(roles.permissions, ",") != operations.PermissionWorkforceRead {
		t.Fatalf("role authorization permissions = %+v", roles.permissions)
	}
	if len(service.lastViewer.PermissionScope) != 1 || service.lastViewer.PermissionScope[0] != operations.PermissionWorkforceRead {
		t.Fatalf("viewer scope = %+v", service.lastViewer.PermissionScope)
	}
}

func TestRouterRejectsUnregisteredAPIRouteBeforeHandlerFallback(t *testing.T) {
	handler := newTestRouter(RouterOptions{Authorization: &fakeHTTPAuthorizationService{}})
	req := authedRequest(http.MethodGet, "/api/unregistered-hard-boundary", "")
	req.Header.Set("X-Permission-Scope", operations.PermissionOperationsRead)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestPermissionPolicyCanPersistRolesAssignmentsAndRouterGrants(t *testing.T) {
	policy := accesscontrol.NewService(accesscontrol.NewMemoryStore(accesscontrol.PolicyDTO{
		Roles: []accesscontrol.RoleDTO{{RoleID: "policy-admin"}},
		RoleAssignments: []accesscontrol.RoleAssignmentDTO{{
			PrincipalKind: "user",
			PrincipalID:   "operator",
			RoleID:        "policy-admin",
		}},
		Grants: []accesscontrol.PermissionGrantDTO{{
			SubjectKind: "role",
			SubjectID:   "policy-admin",
			Scope:       operations.PermissionPermissionsManage,
		}},
	}))
	handler := newTestRouter(RouterOptions{AccessControl: policy})
	update := authedRequest(http.MethodPut, "/api/permissions/policy", `{
		"roles":[{"role_id":"ops-editor","label_zh":"运营编辑员","label_en":"Operations Editor"}],
		"role_assignments":[{"principal_kind":"user","principal_id":"operator","role_id":"ops-editor"}],
		"grants":[{"subject_kind":"role","subject_id":"ops-editor","scope":"permissions.manage"},{"subject_kind":"role","subject_id":"ops-editor","scope":"permissions.read"}]
	}`)
	update.Header.Set("X-User-ID", "operator")
	update.Header.Set("X-Viewer-Kind", "user")
	update.Header.Set("X-Permission-Scope", operations.PermissionPermissionsManage)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, update)

	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body = %s", rec.Code, rec.Body.String())
	}
	list := authedRequest(http.MethodGet, "/api/permissions/policy", "")
	list.Header.Set("X-User-ID", "operator")
	list.Header.Set("X-Viewer-Kind", "user")
	list.Header.Set("X-Permission-Scope", operations.PermissionPermissionsRead)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, list)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body accesscontrol.PolicyViewDTO
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode policy: %v", err)
	}
	if len(body.Policy.Roles) != 1 || body.Policy.Roles[0].LabelZh != "运营编辑员" {
		t.Fatalf("roles = %+v", body.Policy.Roles)
	}
	if len(body.Policy.Grants) != 2 || body.Policy.Grants[0].Scope != operations.PermissionPermissionsManage || body.Policy.Grants[1].Scope != operations.PermissionPermissionsRead {
		t.Fatalf("grants = %+v", body.Policy.Grants)
	}
	if len(body.Capabilities) == 0 || body.Capabilities[0].LabelZh == "" {
		t.Fatalf("capabilities missing from policy view: %+v", body.Capabilities)
	}
}

func TestPermissionPolicyIsReadOnlyWhenAuthorizationServiceIsAuthoritative(t *testing.T) {
	policy := accesscontrol.NewService(accesscontrol.NewMemoryStore(accesscontrol.PolicyDTO{
		Roles: []accesscontrol.RoleDTO{{RoleID: "stale-local-role"}},
	}))
	authorization := &fakeHTTPAuthorizationService{}
	handler := newTestRouter(RouterOptions{AccessControl: policy, Authorization: authorization})

	read := authedRequest(http.MethodGet, "/api/permissions/policy", "")
	read.Header.Set("X-Permission-Scope", operations.PermissionPermissionsRead)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, read)
	if rec.Code != http.StatusOK {
		t.Fatalf("read status = %d body = %s", rec.Code, rec.Body.String())
	}
	var view accesscontrol.PolicyViewDTO
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	if view.EnforcementMode != "authorization" || view.PolicyMutable || len(view.Policy.Roles) != 0 {
		t.Fatalf("authoritative policy view = %+v", view)
	}

	update := authedRequest(http.MethodPut, "/api/permissions/policy", `{"roles":[],"role_assignments":[],"grants":[]}`)
	update.Header.Set("X-Permission-Scope", operations.PermissionPermissionsManage)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, update)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), `"code":"conflict"`) {
		t.Fatalf("update status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestAuthoritativePermissionPolicyProjectsOperationsRoles(t *testing.T) {
	service := &fakeOperationsService{
		roles: operations.RoleDefinitionPageDTO{Items: []operations.RoleDefinitionDTO{
			{RoleID: "resource.owner", Name: "资源负责人", Description: "Resource owner", Status: "active", PermissionRefs: []string{"resource.read", "resource.manage"}},
			{RoleID: "retired", Name: "Retired", Status: "disabled", PermissionRefs: []string{"permissions.manage"}},
		}},
		roleAssignments: operations.RoleAssignmentPageDTO{Items: []operations.RoleAssignmentDTO{
			{Actor: operations.ActorRefDTO{ActorType: "human", ActorID: "human-operator"}, RoleID: "resource.owner"},
			{Actor: operations.ActorRefDTO{ActorType: "human", ActorID: "human-operator"}, RoleID: "resource.owner", OrganizationCode: "0101"},
		}},
	}
	handler := newTestRouter(RouterOptions{Operations: service, Authorization: &fakeHTTPAuthorizationService{}})
	req := authedRequest(http.MethodGet, "/api/permissions/policy", "")
	req.Header.Set("X-Permission-Scope", operations.PermissionPermissionsRead)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var view accesscontrol.PolicyViewDTO
	if err := json.NewDecoder(rec.Body).Decode(&view); err != nil {
		t.Fatalf("decode view: %v", err)
	}
	if len(view.Policy.Roles) != 1 || view.Policy.Roles[0].RoleID != "resource.owner" || view.Policy.Roles[0].LabelEn != "Resource owner" {
		t.Fatalf("roles = %+v", view.Policy.Roles)
	}
	if len(view.Policy.RoleAssignments) != 1 || view.Policy.RoleAssignments[0] != (accesscontrol.RoleAssignmentDTO{PrincipalKind: "user", PrincipalID: "human-operator", RoleID: "resource.owner"}) {
		t.Fatalf("assignments = %+v", view.Policy.RoleAssignments)
	}
	if len(view.Policy.Grants) != 2 || view.Policy.Grants[0].Scope != "resource.read" || view.Policy.Grants[1].Scope != "resource.manage" {
		t.Fatalf("grants = %+v", view.Policy.Grants)
	}
	if view.EnforcementMode != "authorization" || !view.PolicyMutable || !service.roleAssignmentQuery.EffectiveOnly {
		t.Fatalf("view = %+v query = %+v", view, service.roleAssignmentQuery)
	}
}

func TestAuthoritativePermissionPolicyUpdatesOperationsRoleDefinitions(t *testing.T) {
	service := &fakeOperationsService{
		roles: operations.RoleDefinitionPageDTO{Items: []operations.RoleDefinitionDTO{{
			RoleID:               "resource.owner",
			Name:                 "资源负责人",
			Description:          "Resource owner",
			Category:             "resource",
			Status:               "active",
			AssignableActorTypes: []string{"human", "agent"},
			BuiltIn:              true,
			PermissionRefs:       []string{"resource.read"},
		}}},
	}
	handler := newTestRouter(RouterOptions{Operations: service, Authorization: &fakeHTTPAuthorizationService{}})
	req := authedRequest(http.MethodPut, "/api/permissions/policy", `{
		"roles":[
			{"role_id":"resource.owner","label_zh":"资源主管","label_en":"Resource lead"},
			{"role_id":"custom.reviewer","label_zh":"自定义审核员","label_en":"Custom reviewer"}
		],
		"role_assignments":[],
		"grants":[
			{"subject_kind":"role","subject_id":"resource.owner","scope":"resource.manage"},
			{"subject_kind":"role","subject_id":"custom.reviewer","scope":"permissions.read"}
		]
	}`)
	req.Header.Set("X-Permission-Scope", operations.PermissionPermissionsManage)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(service.putRoleDefinitions.Roles) != 1 {
		t.Fatalf("command = %+v", service.putRoleDefinitions)
	}
	created := service.putRoleDefinitions.Roles[0]
	if created.RoleID != "custom.reviewer" || created.Category != "custom" || created.Status != "active" || len(created.AssignableActorTypes) != 3 || created.PermissionRefs[0] != "permissions.read" {
		t.Fatalf("created role = %+v", created)
	}
	if !viewerHasScope(service.lastViewer.PermissionScope, operations.PermissionRolesRead, operations.PermissionRolesRead) {
		t.Fatalf("viewer scopes = %+v", service.lastViewer.PermissionScope)
	}
}

type fakeHTTPAuthorizationService struct {
	err            error
	errorsByAction map[string]error
	principal      identity.Principal
	action         string
	actions        []string
	object         identity.ObjectRef
}

func humanRoleTestBearerAuth() *fakeBearerAuthService {
	return &fakeBearerAuthService{principal: identity.Principal{
		SubjectProvider: "test",
		Subject:         "operator",
		UserID:          "operator",
		AgentProfileID:  "human-operator",
		ViewerKind:      identity.ViewerKindUser,
		TenantID:        "system",
	}}
}

func humanRoleTestRequest(method string, path string, body string) *http.Request {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer human-role-test-token")
	return req
}

type fakeHTTPRoleAuthorizationService struct {
	allowed             bool
	allowedByPermission map[string]bool
	err                 error
	permission          string
	permissions         []string
	objects             []identity.ObjectRef
}

func (f *fakeHTTPRoleAuthorizationService) AuthorizeRolePermission(_ context.Context, _ identity.Principal, permission string, objects []identity.ObjectRef) (bool, error) {
	f.permission = permission
	f.permissions = append(f.permissions, permission)
	f.objects = append([]identity.ObjectRef(nil), objects...)
	if f.allowedByPermission != nil {
		return f.allowedByPermission[permission], f.err
	}
	return f.allowed, f.err
}

func (f *fakeHTTPAuthorizationService) Can(ctx context.Context, principal identity.Principal, action string, object identity.ObjectRef) error {
	_ = ctx
	f.principal = principal
	f.action = action
	f.actions = append(f.actions, action)
	f.object = object
	if f.errorsByAction != nil {
		if err, ok := f.errorsByAction[action]; ok {
			return err
		}
	}
	return f.err
}

func findCapability(t *testing.T, capabilities []PermissionCapabilityDTO, method, path, scope string) PermissionCapabilityDTO {
	t.Helper()
	for _, capability := range capabilities {
		if capability.Method == method && capability.Path == path && capability.Scope == scope {
			return capability
		}
	}
	t.Fatalf("missing capability method=%s path=%s scope=%s in %+v", method, path, scope, capabilities)
	return PermissionCapabilityDTO{}
}
