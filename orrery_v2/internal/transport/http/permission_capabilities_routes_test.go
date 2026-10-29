package httptransport

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/operations"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	"orrery_v2/internal/app/userauth"
)

func TestHTTPRoutesPermissionCapabilitiesExposeRegisteredRouteScopes(t *testing.T) {
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
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/resources/mcp-servers", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/agent-runs/{run_id}/trace", "agent.trace.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/agent-runs/{run_id}/trace/legal-hold", "agent.trace.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/resources/mcp-servers/{tool_id}", "resource.read")
	assertCapability(t, body.Capabilities, http.MethodDelete, "/api/resources/mcp-servers/{tool_id}", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/resources/mcp-servers/{tool_id}/health", "resource.read")
	assertNoCapability(t, body.Capabilities, http.MethodPost, "/api/resources/mcp-servers/import", "resource.manage")
	assertNoCapability(t, body.Capabilities, http.MethodPost, "/api/resources/mcp-servers/discovered", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/resources/skills", "resource.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/resources/skills", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/resources/skills/{skill_id}/versions", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/resources/skills/{skill_id}/versions/{version}/promote", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/resources/skills/{skill_id}/versions/{version}/rollback", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/resources/skills/import", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/resources/skills/{skill_id}", "resource.read")
	assertCapability(t, body.Capabilities, http.MethodPut, "/api/resources/skills/{skill_id}/skill-md", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/resources/skills/{skill_id}/folders", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/resources/skills/{skill_id}/files", "resource.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/resources/skills/{skill_id}/files/download", "resource.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/resources/skills/{skill_id}/files", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodPut, "/api/resources/skills/{skill_id}/files", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodDelete, "/api/resources/skills/{skill_id}/files", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodDelete, "/api/resources/skills/{skill_id}", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/resources/skills/{skill_id}/disable", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/resources/skills/{skill_id}/export", "resource.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/resources/skill-bundles", "resource.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/resources/skill-bundles", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/resources/skill-bundles/{bundle_id}/skills", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/resources/skill-bundles/{bundle_id}/skills/import", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodDelete, "/api/resources/skill-bundles/{bundle_id}/skills/{skill_id}", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodDelete, "/api/resources/skill-bundles/{bundle_id}", "resource.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/org/graph", "org.read")
	assertNoCapability(t, body.Capabilities, http.MethodGet, "/api/org/graph", "operations.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/workforce/members", "workforce.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/workforce/avatar-defaults", "workforce.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/workforce/avatar-defaults/{default_id}", "workforce.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/workforce/members/{agent_profile_id}/avatar", "workforce.read")
	assertCapability(t, body.Capabilities, http.MethodPut, "/api/workforce/members/{agent_profile_id}/avatar", "workforce.manage")
	assertCapability(t, body.Capabilities, http.MethodPut, "/api/workforce/members/{agent_profile_id}/avatar/default", "workforce.manage")
	assertCapability(t, body.Capabilities, http.MethodDelete, "/api/workforce/members/{agent_profile_id}/avatar", "workforce.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/workforce/members/{agent_profile_id}/channel-bindings", "workforce.read")
	assertCapability(t, body.Capabilities, http.MethodPut, "/api/workforce/members/{agent_profile_id}/channel-bindings/feishu/{account_id}", "workforce.manage")
	assertCapability(t, body.Capabilities, http.MethodDelete, "/api/workforce/members/{agent_profile_id}/channel-bindings/feishu/{account_id}", "workforce.manage")
	assertNoCapability(t, body.Capabilities, http.MethodGet, "/api/workforce/members", "operations.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/talent-market/members", "workforce.read")
	assertNoCapability(t, body.Capabilities, http.MethodGet, "/api/talent-market/members", "operations.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/talent-market/members/delete-batch", "workforce.manage")
	assertNoCapability(t, body.Capabilities, http.MethodPost, "/api/talent-market/members/delete-batch", "operations.manage")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/org", "org.manage")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/org/units", "org.manage")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/org/positions", "org.manage")
	assertCapability(t, body.Capabilities, http.MethodDelete, "/api/org/positions/{position_id}", "org.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/org/roles", "roles.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/org/role-assignments", "roles.read")
	assertCapability(t, body.Capabilities, http.MethodPut, "/api/org/role-assignments", "roles.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/permissions/matrix", "permissions.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/permissions/decisions", "permissions.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/permissions/policy", "permissions.read")
	assertCapability(t, body.Capabilities, http.MethodPut, "/api/permissions/policy", "permissions.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/authorization/relation-backfill/diagnostics", "permissions.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/authorization/enforce-transition/diagnostics", "permissions.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/identity/subjects", "permissions.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/identity/subjects", "permissions.manage")
	assertCapability(t, body.Capabilities, http.MethodDelete, "/api/identity/subjects/{subject_id}", "permissions.manage")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/identity/subjects/{subject_id}/enable", "permissions.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/oauth2/clients", "permissions.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/oauth2/clients", "permissions.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/audit-log", "audit.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/missions/{mission_id}/tasks/{task_id}/agent-runs", "agent.run")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/resources/tools/call", "tool.call")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/resources/bindings", "tool.call")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/settings/knowledge/pageindex/test", "settings.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/memory/candidates", "memory.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/memory/candidates", "memory.write")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/memory/candidates/{candidate_id}/accept", "memory.review")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/memory/candidates/{candidate_id}/accept", "memory.world.manage")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/memory/candidates/{candidate_id}/reject", "memory.review")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/memory/candidates/{candidate_id}/merge", "memory.review")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/memory/distill/experience", "memory.distill")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/memory/distill/world", "memory.distill")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/memory/records", "memory.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/memory/records/touch", "memory.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/memory/records/sweep-retention", "memory.review")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/memory/records/sweep-retention", "memory.world.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/memory/records/{memory_id}", "memory.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/memory/records/{memory_id}/archive", "memory.review")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/memory/records/{memory_id}/archive", "memory.world.manage")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/memory/records/{memory_id}/anonymize", "memory.review")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/memory/records/{memory_id}/anonymize", "memory.world.manage")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/memory/records/{memory_id}/delete", "memory.review")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/memory/records/{memory_id}/delete", "memory.world.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/approvals", "approvals.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/approvals/{approval_id}/decision", "approvals.decide")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/hil/decisions", "approvals.decide")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/hil/decisions", "approvals.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/hil/decisions/{decision_id}", "approvals.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/hil/decisions/{decision_id}/decision", "approvals.decide")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/hil/councils/{council_id}", "approvals.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/hil/councils/{council_id}/contributions", "approvals.decide")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/hil/councils/{council_id}/votes", "approvals.decide")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/hil/workflow-templates", "approvals.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/hil/workflow-templates", "approvals.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/hil/workflow-templates/{template_id}", "approvals.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/hil/workflow-instances", "approvals.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/hil/workflow-tasks/{task_id}/decision", "approvals.decide")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/hil/workflow-tasks/{task_id}/reassign", "approvals.manage")
	assertNoCapability(t, body.Capabilities, http.MethodPost, "/api/approvals/{approval_id}/decision", "approvals.manage")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/discussions/{discussion_id}/approvals/{approval_id}/decision", "approvals.decide")
	assertNoCapability(t, body.Capabilities, http.MethodPost, "/api/discussions/{discussion_id}/approvals/{approval_id}/decision", "discussion.write")
	approvalDecision := findCapabilityInCatalog(t, body.Capabilities, http.MethodPost, "/api/approvals/{approval_id}/decision", "approvals.decide")
	if approvalDecision.LabelZh != "审批处理" {
		t.Fatalf("approval decision capability should use a localized decision label: %+v", approvalDecision)
	}
	assertCapability(t, body.Capabilities, http.MethodDelete, "/api/discussions/{discussion_id}", "discussion.write")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/discussions/{discussion_id}/messages", "discussion.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/discussions/{discussion_id}/members", "discussion.write")
	assertCapability(t, body.Capabilities, http.MethodPatch, "/api/discussions/{discussion_id}/workspace", "discussion.write")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/discussions/{discussion_id}/messages/{message_id}/retry", "discussion.write")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/discussions/{discussion_id}/interjections", "discussion.write")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/missions", "mission.write")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/missions/{mission_id}/ops", "mission.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/missions/{mission_id}/ops", "development.monitor")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/missions/{mission_id}/stream", "development.monitor")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/development-projects/{development_project_id}/executions", "development.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/missions/{mission_id}/tasks/{task_id}/agent-runs", "mission.write")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/missions/{mission_id}/agent-runs/{run_id}/cancel", "mission.write")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/missions/{mission_id}/agent-runs/{run_id}/retry", "mission.write")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/ontology/scopes", "operations.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/ontology/schema/summary", "operations.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/ontology/context/summary", "operations.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/ontology/context/company-overview", "operations.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/ontology/context/overview", "operations.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/ontology/reliability/incidents/{incident_id}", "operations.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/ontology/reliability/incidents/{incident_id}/rules/{rule_id}/authorizations/{decision_id}/invoke", "operations.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/ontology/lineage/summary", "operations.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/ontology/governance/summary", "operations.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/ontology/projections/summary", "operations.read")
	assertNoCapability(t, body.Capabilities, http.MethodGet, "/api/projection-audits", "operations.read")
	assertNoCapability(t, body.Capabilities, http.MethodGet, "/api/ontology/registry", "operations.read")
	assertCapability(t, body.Capabilities, http.MethodPatch, "/api/knowledge-bases/{knowledge_base_id}", "knowledge.manage")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/scheduled-tasks", "scheduler.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/scheduled-tasks/summary", "scheduler.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/scheduled-runs", "scheduler.read")
	assertCapability(t, body.Capabilities, http.MethodPatch, "/api/scheduled-runs/{scheduled_run_id}/read", "scheduler.read")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/scheduled-tasks", "scheduler.write")
	assertCapability(t, body.Capabilities, http.MethodPatch, "/api/scheduled-tasks/{scheduled_task_id}", "scheduler.write")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/scheduled-tasks/{scheduled_task_id}/cancel", "scheduler.write")
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/scheduled-tasks/{scheduled_task_id}/run-now", "scheduler.run")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/scheduled-tasks/{scheduled_task_id}/runs", "scheduler.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/scheduled-tasks/{scheduled_task_id}/runs/{scheduled_run_id}/responsibility-evaluations", "scheduler.read")
	assertCapability(t, body.Capabilities, http.MethodGet, "/api/scheduled-tasks/{scheduled_task_id}/runs/{scheduled_run_id}/artifacts/content", "scheduler.read")
	scheduler := findCapabilityInCatalog(t, body.Capabilities, http.MethodGet, "/api/scheduled-tasks", "scheduler.read")
	if scheduler.CategoryZh != "自动化" || scheduler.LabelZh != "自动化查看" {
		t.Fatalf("scheduler capability should use automation labels: %+v", scheduler)
	}
	assertCapability(t, body.Capabilities, http.MethodPost, "/api/discussions/attachments", "discussion.write")
	discussion := findCapabilityInCatalog(t, body.Capabilities, http.MethodGet, "/api/discussions", "discussion.read")
	if discussion.CategoryZh != "聊天" || discussion.CategoryEn != "Chat" ||
		discussion.Domain != "discussion" ||
		strings.Contains(discussion.CategoryZh+discussion.CategoryEn+discussion.LabelZh+discussion.LabelEn, "PM") {
		t.Fatalf("discussion capability should use neutral labels: %+v", discussion)
	}
	assertNoDuplicateCapabilities(t, body.Capabilities)
}

func TestMissionDevelopmentMonitorCapabilityUsesDistinctAuthorizationAction(t *testing.T) {
	capability := findCapabilityInCatalog(t, defaultPermissionCapabilities(), http.MethodGet, "/api/missions/{mission_id}/control", permissionDevelopmentMonitor)
	req := httptest.NewRequest(http.MethodGet, "/api/missions/mission-1/control", nil)
	object := authorizationObjectForCapability(req, capability)
	if object.Type != "mission" || object.ID != "mission-1" {
		t.Fatalf("monitor object = %+v", object)
	}
	if action := authorizationActionForCapability(capability, object); action != "mission.monitor" {
		t.Fatalf("monitor action = %q, want mission.monitor", action)
	}
}

func TestUserAssignableBuiltInRolePermissionsUseKnownPlatformScopes(t *testing.T) {
	knownScopes := map[string]struct{}{}
	for _, scope := range userauth.SuperadminScopes() {
		knownScopes[scope] = struct{}{}
	}
	for _, role := range operations.UserAssignableRoleDefinitions(operations.BuiltInRoleDefinitions()) {
		for _, permission := range role.PermissionRefs {
			if _, ok := knownScopes[permission]; !ok {
				t.Fatalf("role %s references unknown platform permission scope %s", role.RoleID, permission)
			}
		}
	}
}

func TestHTTPRoutesPermissionCapabilitiesRequirePermissionsRead(t *testing.T) {
	handler := newTestRouter(RouterOptions{})
	req := authedRequest(http.MethodGet, "/api/permissions/capabilities", "")
	req.Header.Set("X-Permission-Scope", "mission.read")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPRouteScopedPermissionDoesNotAuthorizeUnrelatedOperationsRoute(t *testing.T) {
	service := &fakeOperationsService{
		workforce: operations.WorkforcePageDTO{Members: []operations.WorkforceMemberDTO{{AgentID: "agent-ada"}}},
	}
	handler := newTestRouter(RouterOptions{Operations: service})
	req := authedRequest(http.MethodGet, "/api/workforce/members", "")
	req.Header.Set("X-Permission-Scope", "permissions.read")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestHTTPBroadOperationsScopeDoesNotFallbackToWorkforceRoute(t *testing.T) {
	service := &fakeOperationsService{
		workforce: operations.WorkforcePageDTO{Members: []operations.WorkforceMemberDTO{{AgentID: "agent-ada"}}},
	}
	handler := newTestRouter(RouterOptions{Operations: service})
	req := authedRequest(http.MethodGet, "/api/workforce/members", "")
	req.Header.Set("X-Permission-Scope", operations.PermissionOperationsRead)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.lastViewer.ViewerID != "" {
		t.Fatalf("handler should not be reached, viewer=%+v", service.lastViewer)
	}
}

func TestHTTPRoutePermissionRegistryGuardsRegisteredRoutesBeforeHandlers(t *testing.T) {
	command := &fakeResourceCommand{mcp: resourceadmin.MCPServerDTO{ToolDTO: resourceadmin.ToolDTO{ToolID: "mcp.github"}}}
	handler := newTestRouter(RouterOptions{ResourceCommand: command})
	req := authedRequest(http.MethodPost, "/api/resources/mcp-servers", `{"name":"GitHub MCP","server_name":"github"}`)
	req.Header.Set("X-Permission-Scope", "resource.read")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if command.registerMCP.ServerName != "" {
		t.Fatalf("handler should not be called without registered route permission: %+v", command.registerMCP)
	}
}

func TestSuperadminScopesCoverRegisteredRoutePermissions(t *testing.T) {
	superadminScopes := map[string]struct{}{}
	for _, scope := range userauth.SuperadminScopes() {
		superadminScopes[scope] = struct{}{}
	}
	routeScopes := map[string][]string{}
	for _, capability := range defaultPermissionCapabilities() {
		key := capability.Method + " " + capability.Path
		routeScopes[key] = append(routeScopes[key], capability.Scope)
	}
	for route, scopes := range routeScopes {
		covered := false
		for _, scope := range scopes {
			if _, ok := superadminScopes[scope]; ok {
				covered = true
				break
			}
		}
		if !covered {
			t.Fatalf("superadmin scopes do not cover route %s; route scopes=%+v superadmin=%+v", route, scopes, userauth.SuperadminScopes())
		}
	}
}

func assertCapability(t *testing.T, capabilities []PermissionCapabilityDTO, method, path, scope string) {
	t.Helper()
	_ = findCapabilityInCatalog(t, capabilities, method, path, scope)
}

func assertNoCapability(t *testing.T, capabilities []PermissionCapabilityDTO, method, path, scope string) {
	t.Helper()
	for _, capability := range capabilities {
		if capability.Method == method && capability.Path == path && capability.Scope == scope {
			t.Fatalf("unexpected capability method=%s path=%s scope=%s", method, path, scope)
		}
	}
}

func findCapabilityInCatalog(t *testing.T, capabilities []PermissionCapabilityDTO, method, path, scope string) PermissionCapabilityDTO {
	t.Helper()
	for _, capability := range capabilities {
		if capability.Method == method && capability.Path == path && capability.Scope == scope {
			if capability.CapabilityID == "" || capability.Domain == "" || capability.Action == "" {
				t.Fatalf("capability is missing catalog fields: %+v", capability)
			}
			return capability
		}
	}
	t.Fatalf("missing capability method=%s path=%s scope=%s in %+v", method, path, scope, capabilities)
	return PermissionCapabilityDTO{}
}

func assertNoDuplicateCapabilities(t *testing.T, capabilities []PermissionCapabilityDTO) {
	t.Helper()
	seen := map[string]struct{}{}
	for _, capability := range capabilities {
		key := capability.Method + " " + capability.Path + " " + capability.Scope
		if _, ok := seen[key]; ok {
			t.Fatalf("duplicate capability %s", key)
		}
		seen[key] = struct{}{}
	}
}
