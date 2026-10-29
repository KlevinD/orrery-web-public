package httptransport

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
	devicedirectory "orrery_v2/internal/device/directory"
	skillres "orrery_v2/internal/resource/skill"
)

func TestHTTPRoutesOperationsReadModels(t *testing.T) {
	service := &fakeOperationsService{
		workforce:   operations.WorkforcePageDTO{Members: []operations.WorkforceMemberDTO{{AgentID: "profile-ada", AgentProfileID: "profile-ada", AgentInstanceID: "profile-ada", DisplayName: "Ada"}}},
		graph:       operations.OrgGraphDTO{Departments: []operations.DepartmentDTO{{DepartmentID: "engineering", Code: "0101", DisplayCode: "01.01", Status: "active"}}, OrganizationNodes: []operations.OrganizationNodeDTO{{ID: "org-1", NodeType: "company", Code: "01", DisplayCode: "01", Name: "Orrery", Level: 1, Status: "active", TotalMemberCount: 1}}, Summary: operations.OrgGraphSummaryDTO{RootCount: 1, OrganizationCount: 1, TotalMemberCount: 1}},
		matrix:      operations.PermissionMatrixDTO{Rows: []operations.EffectivePermissionRowDTO{{AgentID: "profile-ada"}}},
		audit:       operations.AuditEventPageDTO{Events: []operations.AuditEventDTO{{AuditID: "audit-1", ActorID: "agent-ada"}}},
		evaluations: operations.EvaluationPageDTO{Items: []operations.EvaluationItemDTO{{EntityType: "agent", EntityID: "agent-ada", QualityScore: 0.9}}},
	}
	personalAssistant := &fakePersonalAssistantService{}
	handler := newTestRouter(RouterOptions{Operations: service, PersonalAssistant: personalAssistant})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/workforce/members?organization_id=orrery&enabled_only=true", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("workforce status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.workforceQuery.OrganizationID != "orrery" || !service.workforceQuery.EnabledOnly {
		t.Fatalf("workforce query = %+v", service.workforceQuery)
	}
	if service.workforceQuery.Limit != 20 || service.workforceQuery.Offset != 0 {
		t.Fatalf("workforce default page = %+v", service.workforceQuery)
	}
	if personalAssistant.getScope != (devicedirectory.OwnerScope{}) {
		t.Fatalf("listing workforce must not provision a personal assistant, got scope %+v", personalAssistant.getScope)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/org/graph?organization_id=orrery", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("org graph status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.orgQuery.OrganizationID != "orrery" || service.orgQuery.IncludeMembers {
		t.Fatalf("org query = %+v", service.orgQuery)
	}
	if !strings.Contains(rec.Body.String(), `"organization_nodes"`) || !strings.Contains(rec.Body.String(), `"display_code":"01"`) || !strings.Contains(rec.Body.String(), `"organization_count":1`) {
		t.Fatalf("org graph response missing organization node metadata: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/org/graph?organization_id=orrery&include_members=true", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("org graph with members status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.orgQuery.OrganizationID != "orrery" || !service.orgQuery.IncludeMembers {
		t.Fatalf("org query with members = %+v", service.orgQuery)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/permissions/matrix?agent_id=profile-ada&tool_id=tool.git", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("permission matrix status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.permissionQuery.AgentID != "profile-ada" || service.permissionQuery.ToolID != "tool.git" {
		t.Fatalf("permission query = %+v", service.permissionQuery)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/audit-log?mission_id=mission-1&actor_id=agent-ada", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("audit status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.auditQuery.MissionID != "mission-1" || service.auditQuery.ActorID != "agent-ada" {
		t.Fatalf("audit query = %+v", service.auditQuery)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/evaluations?mission_id=mission-1&entity_type=agent", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("evaluations status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.evaluationQuery.MissionID != "mission-1" || service.evaluationQuery.EntityType != "agent" {
		t.Fatalf("evaluation query = %+v", service.evaluationQuery)
	}
}

func TestHTTPRoutesEvaluationStreamSendsSnapshotAndUpdates(t *testing.T) {
	service := &fakeOperationsService{
		evaluations: operations.EvaluationPageDTO{Items: []operations.EvaluationItemDTO{{EntityType: "agent", EntityID: "agent-ada", QualityScore: 0.4}}},
	}
	handler := newTestRouter(RouterOptions{Operations: service})
	server := httptest.NewServer(handler)
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/evaluations/stream?mission_id=mission-1&entity_type=agent", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext: %v", err)
	}
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", "operations.read")
	req.Header.Set("Accept", "text/event-stream")
	authorizeInternalViewerTestRequest(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d body = %s", resp.StatusCode, body)
	}

	reader := bufio.NewReader(resp.Body)
	first := readSSEEvent(t, reader)
	if !strings.Contains(first, "event: snapshot") || !strings.Contains(first, `"quality_score":0.4`) {
		t.Fatalf("first evaluation stream event = %s", first)
	}
	query := service.evaluationQuerySnapshot()
	if query.MissionID != "mission-1" || query.EntityType != "agent" {
		t.Fatalf("evaluation stream query = %+v", query)
	}

	service.setEvaluations(operations.EvaluationPageDTO{Items: []operations.EvaluationItemDTO{{EntityType: "agent", EntityID: "agent-ada", QualityScore: 0.9}}})
	second := readSSEEvent(t, reader)
	if !strings.Contains(second, "event: update") || !strings.Contains(second, `"quality_score":0.9`) {
		t.Fatalf("second evaluation stream event = %s", second)
	}
}

func TestHTTPRoutesOperationsWorkforceCommands(t *testing.T) {
	service := &fakeOperationsService{member: operations.WorkforceMemberDTO{AgentID: "profile-ops", AgentProfileID: "profile-ops", AgentInstanceID: "profile-ops", DepartmentID: "operations", Enabled: true}}
	handler := newTestRouter(RouterOptions{Operations: service})

	upsert := authedRequest(http.MethodPatch, "/api/workforce/members/profile-ops", `{"real_name":"Ops Agent","department_id":"operations","organization_id":"orrery","position_id":"operator","user_account":{"password":"new-secret-12"},"enabled":true}`)
	upsert.Header.Set("X-Request-ID", "corr-workforce-route")
	upsert.Header.Set("X-Permission-Scope", "workforce.manage")
	upsert.Header.Set("X-Agent-Profile-ID", "human-admin")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, upsert)
	if rec.Code != http.StatusOK {
		t.Fatalf("upsert status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.upsert.AgentProfileID != "profile-ops" || service.upsert.DepartmentID != "operations" {
		t.Fatalf("upsert command = %+v", service.upsert)
	}
	if !hasString(service.upsert.UpdateMask, "user_account.password") || hasString(service.upsert.UpdateMask, "user_account.permission_scope") {
		t.Fatalf("upsert update mask should contain only submitted user account fields: %+v", service.upsert.UpdateMask)
	}
	if service.lastViewer.RequestID != "corr-workforce-route" || service.lastViewer.AgentProfileID != "human-admin" || len(service.lastViewer.PermissionScope) != 1 || service.lastViewer.PermissionScope[0] != "workforce.manage" {
		t.Fatalf("viewer = %+v", service.lastViewer)
	}

	disable := authedRequest(http.MethodPost, "/api/workforce/members/profile-ops/disable", `{}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, disable)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("disable status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.enabled.AgentProfileID != "profile-ops" || service.enabled.Enabled {
		t.Fatalf("disable command = %+v", service.enabled)
	}

	deleteReq := authedRequest(http.MethodDelete, "/api/workforce/members/profile-ops", `{}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, deleteReq)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.release.AgentProfileID != "" {
		t.Fatalf("removed delete route must not release profile to market, got %+v", service.release)
	}
}

func TestAuthorizeWorkforceCreationAcceptsOnlyAuthenticatedSystemSuperadminWithoutProfile(t *testing.T) {
	router := &Router{agentProfiles: &fakeProfileWeixinDirectory{}}
	request := func(principal identity.Principal) *http.Request {
		req := httptest.NewRequest(http.MethodPut, "/api/workforce/members/agent-worker", nil)
		return req.WithContext(identity.ContextWithPrincipal(req.Context(), principal))
	}

	err := router.authorizeWorkforceCreation(request(identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         "superadmin",
		UserID:          "superadmin",
		Username:        "superadmin",
		AgentProfileID:  agentcore.SystemSuperadminPrincipalID,
		ViewerKind:      identity.ViewerKindUser,
	}))
	if err != nil {
		t.Fatalf("system superadmin should manage workforce without a human profile: %v", err)
	}

	err = router.authorizeWorkforceCreation(request(identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         "local-admin",
		UserID:          "local-admin",
		Username:        "local-admin",
		AgentProfileID:  agentcore.LegacyLocalAdminAgentProfileID,
		ViewerKind:      identity.ViewerKindUser,
	}))
	if err == nil {
		t.Fatal("legacy local placeholder must not manage workforce")
	}
}

func TestHTTPRoutesOperationsWorkforceCreateRejectsTrustedInternalViewer(t *testing.T) {
	service := &fakeOperationsService{member: operations.WorkforceMemberDTO{AgentProfileID: "profile-ops"}}
	handler := newTestRouter(RouterOptions{Operations: service})
	req := authedRequest(http.MethodPost, "/api/workforce/members", `{"real_name":"Ops Agent","agent_kind":"ai","enabled":true}`)
	req.Header.Set("X-Permission-Scope", "workforce.manage")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.createMember.RealName != "" {
		t.Fatalf("trusted internal viewer reached create service: %+v", service.createMember)
	}
}

func TestHTTPRoutesOperationsWorkforcePutRejectsTrustedInternalViewer(t *testing.T) {
	service := &fakeOperationsService{member: operations.WorkforceMemberDTO{AgentProfileID: "profile-fixed"}}
	handler := newTestRouter(RouterOptions{Operations: service})
	req := authedRequest(http.MethodPut, "/api/workforce/members/profile-fixed", `{"real_name":"Fixed Agent","agent_kind":"ai","organization_id":"orrery","department_id":"operations","position_id":"operator","enabled":true}`)
	req.Header.Set("X-Permission-Scope", "workforce.manage")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.upsert.RealName != "" {
		t.Fatalf("trusted internal viewer reached upsert service: %+v", service.upsert)
	}
}

func TestHTTPRoutesTalentMarketImportCommitRejectsTrustedInternalViewer(t *testing.T) {
	service := &fakeOperationsService{importCommit: operations.TalentMarketImportCommitResponseDTO{}}
	handler := newTestRouter(RouterOptions{Operations: service})
	req := authedRequest(http.MethodPost, "/api/talent-market/import/commit", `{"candidates":[{"real_name":"Imported Agent"}]}`)
	req.Header.Set("X-Permission-Scope", "workforce.manage")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(service.commitImport.Candidates) != 0 {
		t.Fatalf("trusted internal viewer reached import service: %+v", service.commitImport)
	}
}

func TestHTTPRoutesOperationsWorkforceCreateAllowsCanonicalHumanAdministrator(t *testing.T) {
	service := &fakeOperationsService{member: operations.WorkforceMemberDTO{AgentProfileID: "profile-ops"}}
	router := &Router{
		operations: service,
		agentProfiles: &fakeProfileWeixinDirectory{entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID: "human-admin",
			AgentKind:      agentcore.AgentKindHuman,
			Enabled:        true,
		}}},
	}
	req := workforceCreateRequest(identity.Principal{
		SubjectProvider: "local",
		Subject:         "admin",
		UserID:          "admin",
		AgentProfileID:  "human-admin",
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    []string{"workforce.manage"},
	})
	rec := httptest.NewRecorder()

	router.handleCreateWorkforceMember(rec, req)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.createMember.RealName != "Ops Agent" {
		t.Fatalf("create command = %+v", service.createMember)
	}
}

func TestHTTPRoutesOperationsWorkforceCreateRejectsNonCanonicalActors(t *testing.T) {
	tests := []struct {
		name      string
		principal identity.Principal
		profiles  []agentcore.AgentDirectoryEntry
	}{
		{
			name: "missing canonical profile id",
			principal: identity.Principal{
				SubjectProvider: "local", Subject: "admin", UserID: "admin", ViewerKind: identity.ViewerKindUser,
			},
		},
		{
			name: "agent principal",
			principal: identity.Principal{
				SubjectProvider: "runtime", Subject: "agent", UserID: "agent", AgentProfileID: "agent-admin", ViewerKind: identity.ViewerKindAgent,
			},
			profiles: []agentcore.AgentDirectoryEntry{{AgentProfileID: "agent-admin", AgentKind: agentcore.AgentKindAI, Enabled: true}},
		},
		{
			name: "disabled human profile",
			principal: identity.Principal{
				SubjectProvider: "local", Subject: "admin", UserID: "admin", AgentProfileID: "human-admin", ViewerKind: identity.ViewerKindUser,
			},
			profiles: []agentcore.AgentDirectoryEntry{{AgentProfileID: "human-admin", AgentKind: agentcore.AgentKindHuman, Enabled: false}},
		},
		{
			name: "missing directory profile",
			principal: identity.Principal{
				SubjectProvider: "local", Subject: "admin", UserID: "admin", AgentProfileID: "human-admin", ViewerKind: identity.ViewerKindUser,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			service := &fakeOperationsService{member: operations.WorkforceMemberDTO{AgentProfileID: "profile-ops"}}
			router := &Router{operations: service, agentProfiles: &fakeProfileWeixinDirectory{entries: test.profiles}}
			rec := httptest.NewRecorder()

			router.handleCreateWorkforceMember(rec, workforceCreateRequest(test.principal))

			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
			}
			if service.createMember.RealName != "" {
				t.Fatalf("non-canonical actor reached create service: %+v", service.createMember)
			}
		})
	}
}

func workforceCreateRequest(principal identity.Principal) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/workforce/members", strings.NewReader(`{"real_name":"Ops Agent","agent_kind":"ai","enabled":true}`))
	return req.WithContext(identity.ContextWithPrincipal(req.Context(), principal))
}

func TestHTTPRoutesTalentMarketWorkforcePlacement(t *testing.T) {
	service := &fakeOperationsService{
		workforce: operations.WorkforcePageDTO{Members: []operations.WorkforceMemberDTO{{AgentID: "profile-internal", AgentProfileID: "profile-internal", AgentInstanceID: "profile-internal", PlacementStatus: "internal"}}},
		market:    operations.WorkforcePageDTO{Members: []operations.WorkforceMemberDTO{{AgentID: "profile-market", AgentProfileID: "profile-market", AgentInstanceID: "profile-market", PlacementStatus: "market"}}},
		member:    operations.WorkforceMemberDTO{AgentID: "profile-market", AgentProfileID: "profile-market", AgentInstanceID: "profile-market", OrganizationID: "org-1", DepartmentID: "dept-ops", PositionID: "pos-operator", PlacementStatus: "internal", Enabled: true},
	}
	handler := newTestRouter(RouterOptions{Operations: service})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/talent-market/members?enabled_only=true", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("talent market status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !service.marketQuery.EnabledOnly {
		t.Fatalf("market query = %+v", service.marketQuery)
	}

	release := authedRequest(http.MethodPost, "/api/workforce/members/profile-internal/release-to-market", `{"reason":"team changed"}`)
	release.Header.Set("X-Permission-Scope", "workforce.manage")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, release)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), "released_to_market") {
		t.Fatalf("release status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.release.AgentProfileID != "profile-internal" || service.release.Reason != "team changed" {
		t.Fatalf("release command = %+v", service.release)
	}

	hire := authedRequest(http.MethodPost, "/api/talent-market/members/profile-market/hire", `{"organization_id":"org-1","department_id":"dept-ops","position_id":"pos-operator","role_ids":["operator"],"manager_agent_id":"agent-manager","reporting_line_id":"ops-line"}`)
	hire.Header.Set("X-Permission-Scope", "workforce.manage")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, hire)
	if rec.Code != http.StatusOK {
		t.Fatalf("hire status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.hire.AgentProfileID != "profile-market" || service.hire.OrganizationID != "org-1" || service.hire.DepartmentID != "dept-ops" || service.hire.PositionID != "pos-operator" {
		t.Fatalf("hire command = %+v", service.hire)
	}

	deleteMarket := authedRequest(http.MethodDelete, "/api/talent-market/members/profile-market", `{"reason":"bad import"}`)
	deleteMarket.Header.Set("X-Permission-Scope", "workforce.manage")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, deleteMarket)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), "deleted") {
		t.Fatalf("delete market status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.deleteMarket.AgentProfileID != "profile-market" || service.deleteMarket.Reason != "bad import" {
		t.Fatalf("delete market command = %+v", service.deleteMarket)
	}

	batchDelete := authedRequest(http.MethodPost, "/api/talent-market/members/delete-batch", `{"agent_profile_ids":["profile-market","profile-other"],"reason":"cleanup selected"}`)
	batchDelete.Header.Set("X-Permission-Scope", "workforce.manage")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, batchDelete)
	if rec.Code != http.StatusAccepted || !strings.Contains(rec.Body.String(), `"deleted_count":2`) {
		t.Fatalf("batch delete market status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(service.deleteMarketBatch.AgentProfileIDs) != 2 || service.deleteMarketBatch.AgentProfileIDs[0] != "profile-market" || service.deleteMarketBatch.Reason != "cleanup selected" {
		t.Fatalf("batch delete command = %+v", service.deleteMarketBatch)
	}
}

func TestHTTPRoutesTalentMarketImportExport(t *testing.T) {
	service := &fakeOperationsService{
		importPreview: operations.TalentMarketImportPreviewResponseDTO{
			Items: []operations.TalentMarketImportPreviewItemDTO{{CandidateIndex: 0, Status: "create"}},
		},
		importCommit: operations.TalentMarketImportCommitResponseDTO{
			Imported: []operations.WorkforceMemberDTO{{AgentProfileID: "profile-import", PlacementStatus: "market"}},
		},
		exportPackage: operations.TalentMarketExportPackageDTO{
			Manifest: operations.TalentMarketExportManifestDTO{Format: "orrery.talent-market", Version: 1, Count: 1},
			Agents:   []operations.TalentMarketImportCandidateDTO{{AgentProfileID: "profile-market", RealName: "市场专家"}},
			Skills: []operations.TalentMarketSkillDefinitionDTO{{
				SkillID: "skill.market",
				Name:    "市场分析",
				Version: "1.0.0",
				PromptAssets: []skillres.PromptAsset{{
					Path:    "SKILL.md",
					Kind:    skillres.PromptAssetMarkdown,
					Content: "市场分析 skill 完整提示词。",
				}},
				Enabled: true,
			}},
		},
	}
	handler := newTestRouter(RouterOptions{Operations: service})

	previewBody, previewContentType := talentMarketImportMultipartBody(t, map[string]string{
		"engineering/engineering-code-reviewer.md": strings.Join([]string{
			"---",
			"name: 代码审查员",
			"description: 负责代码审查、风险识别和质量建议。",
			"---",
			"# 代码审查员",
			"",
			"## 工作原则",
			"保持谨慎，必须指出风险。",
		}, "\n"),
	})
	preview := authedRequest(http.MethodPost, "/api/talent-market/import/preview", previewBody.String())
	preview.Header.Set("Content-Type", previewContentType)
	preview.Header.Set("X-Permission-Scope", "workforce.manage")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, preview)
	if rec.Code != http.StatusOK {
		t.Fatalf("preview status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(service.previewImport.Candidates) != 1 || service.previewImport.Candidates[0].RealName != "代码审查员" || service.previewImport.Candidates[0].SourcePath != "engineering/engineering-code-reviewer.md" {
		t.Fatalf("preview request = %+v", service.previewImport)
	}

	commit := httptest.NewRequest(http.MethodPost, "/api/talent-market/import/commit", strings.NewReader(`{"candidates":[{"agent_profile_id":"profile-import","real_name":"导入专家","conflict_resolution":"rename","resolved_real_name":"导入专家二号","skill_refs":[{"skill_id":"skill.imported"}]}],"skills":[{"skill_id":"skill.imported","name":"导入技能","version":"1.0.0","enabled":true,"prompt_assets":[{"path":"SKILL.md","kind":"markdown","content":"导入 skill 正文。"}]}]}`))
	commit = commit.WithContext(identity.ContextWithPrincipal(commit.Context(), identity.Principal{
		SubjectProvider: "local", Subject: "admin", UserID: "admin", AgentProfileID: "human-admin", ViewerKind: identity.ViewerKindUser,
		CoarseScopes: []string{"workforce.manage"},
	}))
	rec = httptest.NewRecorder()
	router := &Router{
		operations: service,
		agentProfiles: &fakeProfileWeixinDirectory{entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID: "human-admin", AgentKind: agentcore.AgentKindHuman, Enabled: true,
		}}},
	}
	router.handleCommitTalentMarketImport(rec, commit)
	if rec.Code != http.StatusOK {
		t.Fatalf("commit status = %d body = %s", rec.Code, rec.Body.String())
	}
	if len(service.commitImport.Candidates) != 1 || service.commitImport.Candidates[0].ResolvedRealName != "导入专家二号" {
		t.Fatalf("commit request = %+v", service.commitImport)
	}
	if len(service.commitImport.Skills) != 1 || service.commitImport.Skills[0].SkillID != "skill.imported" {
		t.Fatalf("commit skills = %+v", service.commitImport.Skills)
	}

	exportReq := authedRequest(http.MethodPost, "/api/talent-market/export", `{"agent_profile_ids":["profile-market"]}`)
	exportReq.Header.Set("X-Permission-Scope", "workforce.read")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, exportReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("export status = %d body = %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "application/zip") {
		t.Fatalf("export content-type = %q", got)
	}
	if len(service.exportMarket.AgentProfileIDs) != 1 || service.exportMarket.AgentProfileIDs[0] != "profile-market" {
		t.Fatalf("export request = %+v", service.exportMarket)
	}
	reader, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatalf("read export zip: %v", err)
	}
	if !zipHasFile(reader, "manifest.json") || !zipHasFile(reader, "agents/profile-market.json") || !zipHasFile(reader, "skills/skill-market.json") {
		t.Fatalf("export zip files = %+v", reader.File)
	}
}

func TestHTTPRoutesCreateSystemManagedAgentInheritsGlobalDefaultLLMProfile(t *testing.T) {
	settings := newMemorySettingsService(DefaultSettings())
	_, err := settings.SaveSettings(context.Background(), SettingsDTO{
		PlatformName:           "Orrery",
		OrganizationName:       "Example",
		Language:               "zh-CN",
		AutoCreateAgentProfile: true,
		AutoLinkEvidence:       true,
		HILDefaultEnabled:      true,
		LLMProfiles: []SettingsLLMProfileDTO{
			{
				ProfileID:     "llm-openai",
				DisplayName:   "OpenAI Primary",
				Provider:      "openai",
				BaseURL:       "https://api.openai.com/v1",
				APIKeyRef:     "secret://llm/openai",
				Model:         "gpt-5",
				Temperature:   0.2,
				ContextLength: 128000,
				Enabled:       true,
				GlobalDefault: true,
			},
		},
		NotificationRules: DefaultSettings().NotificationRules,
	})
	if err != nil {
		t.Fatalf("seed settings: %v", err)
	}
	service := &fakeOperationsService{member: operations.WorkforceMemberDTO{AgentID: "profile-ops", AgentProfileID: "profile-ops", AgentInstanceID: "profile-ops", DepartmentID: "operations", Enabled: true}}
	router := &Router{
		operations: service,
		settings:   settings,
		agentProfiles: &fakeProfileWeixinDirectory{entries: []agentcore.AgentDirectoryEntry{{
			AgentProfileID: "human-admin",
			AgentKind:      agentcore.AgentKindHuman,
			Enabled:        true,
		}}},
	}
	create := workforceCreateRequest(identity.Principal{
		SubjectProvider: "local", Subject: "admin", UserID: "admin", AgentProfileID: "human-admin",
		ViewerKind: identity.ViewerKindUser, CoarseScopes: []string{"workforce.manage"},
	})
	rec := httptest.NewRecorder()
	router.handleCreateWorkforceMember(rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create workforce status = %d body = %s", rec.Code, rec.Body.String())
	}

	llm := service.createMember.BaseAgentLLMProfile
	if service.createMember.RuntimeKind != "" || service.createMember.RuntimeConfigRef != "" || len(service.createMember.RuntimeConfig) != 0 {
		t.Fatalf("system-managed create must not invent runtime config: %+v", service.createMember)
	}
	if llm.ProfileID != "platform-default" || llm.Provider != "" || llm.BaseURL != "" || llm.APIKeyRef != "" || llm.Model != "" || llm.ContextLength != 0 {
		t.Fatalf("create command must reference the live default without copying provider settings: %+v", llm)
	}
}

func TestHTTPRoutesOperationsCreateOrganization(t *testing.T) {
	service := &fakeOperationsService{organization: operations.OrganizationDTO{OrganizationID: "org-new", Name: "New Org"}}
	handler := newTestRouter(RouterOptions{Operations: service})

	req := authedRequest(http.MethodPost, "/api/org", `{"name":"New Org"}`)
	req.Header.Set("X-Permission-Scope", "org.manage")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create org status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.createdOrg.Name != "New Org" || service.createdOrg.DefaultDepartmentName != "" || service.createdOrg.DefaultPositionName != "" {
		t.Fatalf("create org command = %+v", service.createdOrg)
	}
	if service.lastViewer.PermissionScope[0] != "org.manage" {
		t.Fatalf("viewer = %+v", service.lastViewer)
	}
}

func TestHTTPRoutesOrganizationRoles(t *testing.T) {
	service := &fakeOperationsService{
		roles: operations.RoleDefinitionPageDTO{Items: operations.BuiltInRoleDefinitions(), Page: operations.PageDTO{Total: len(operations.BuiltInRoleDefinitions()), Returned: len(operations.BuiltInRoleDefinitions())}},
	}
	handler := newTestRouter(RouterOptions{Operations: service})

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/org/roles", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("roles status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"role_id":"org.owner"`) || !strings.Contains(rec.Body.String(), `"approval_capability":true`) {
		t.Fatalf("roles response = %s", rec.Body.String())
	}
}

func TestHTTPRoutesOrganizationRoleAssignments(t *testing.T) {
	service := &fakeOperationsService{
		roleAssignments: operations.RoleAssignmentPageDTO{Items: []operations.RoleAssignmentDTO{{
			AssignmentID:       "assign-1",
			Actor:              operations.ActorRefDTO{ActorType: "human", ActorID: "profile-1"},
			RoleID:             "org.manager",
			OrganizationNodeID: "dept-ai",
			OrganizationCode:   "0101",
			ScopeMode:          operations.RoleScopeSelfAndDescendants,
			Source:             operations.RoleSourceDirect,
		}}},
	}
	handler := newTestRouter(RouterOptions{Operations: service})

	put := authedRequest(http.MethodPut, "/api/org/role-assignments", `{"assignments":[{"assignment_id":"assign-1","actor":{"actor_type":"human","actor_id":"profile-1"},"role_id":"org.manager","organization_node_id":"dept-ai","organization_code":"01.01","scope_mode":"self_and_descendants","source":"direct"}]}`)
	put.Header.Set("X-Permission-Scope", "roles.manage")
	put.Header.Set("Idempotency-Key", "assign-key")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, put)
	if rec.Code != http.StatusOK {
		t.Fatalf("put role assignments status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.putRoleAssignments.Assignments[0].OrganizationCode != "01.01" || service.lastViewer.IdempotencyKey != "assign-key" {
		t.Fatalf("put role assignments command=%+v viewer=%+v", service.putRoleAssignments, service.lastViewer)
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/org/role-assignments?organization_code=01.01", ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("list role assignments status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.roleAssignmentQuery.OrganizationCode != "01.01" || !strings.Contains(rec.Body.String(), `"assignment_id":"assign-1"`) {
		t.Fatalf("role assignment query=%+v response=%s", service.roleAssignmentQuery, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodDelete, "/api/org/role-assignments/assign-1", ""))
	if rec.Code != http.StatusNoContent || service.deletedRoleAssignment != "assign-1" {
		t.Fatalf("delete status=%d body=%s assignment=%q", rec.Code, rec.Body.String(), service.deletedRoleAssignment)
	}
}

func TestHTTPPermissionDecisionRoute(t *testing.T) {
	service := &fakeOperationsService{
		permissionDecision: operations.PermissionDecisionDTO{
			Decision: "needs_hil",
			Reason:   "risk policy requires human review",
			Sources: []operations.PermissionDecisionSourceDTO{{
				SourceType:       "risk_policy",
				OrganizationCode: "01",
				ResourceID:       "tool.deploy",
				Effect:           "needs_hil",
				Reason:           "risk policy requires human review",
			}},
			RequiredApprovals: []operations.ApprovalRequirementDTO{{
				RequirementID:    "approval:agent-ops:tool.deploy:tool.call",
				PolicyID:         "risk:high",
				ApprovalType:     "hil",
				OrganizationCode: "01",
				ApproverType:     "org_owner",
				Reason:           "risk policy requires human review",
			}},
		},
	}
	handler := newTestRouter(RouterOptions{Operations: service})

	req := authedRequest(http.MethodPost, "/api/permissions/decisions", `{"subject":{"actor_type":"agent","actor_id":"agent-ops"},"action":"tool.call","resource_type":"tool","resource_id":"tool.deploy","organization_code":"01","risk_level":"high","metadata":{"resource_binding":"allow"}}`)
	req.Header.Set("X-Permission-Scope", "permissions.read")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("permission decision status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.permissionDecisionReq.Subject.ActorID != "agent-ops" || service.permissionDecisionReq.OrganizationCode != "01" || service.permissionDecisionReq.Metadata["resource_binding"] != "allow" {
		t.Fatalf("permission decision request = %+v", service.permissionDecisionReq)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `"decision":"needs_hil"`) || !strings.Contains(body, `"source_type":"risk_policy"`) || !strings.Contains(body, `"approval_type":"hil"`) {
		t.Fatalf("permission decision response = %s", body)
	}
}

func TestHTTPRoutesOperationsCreateOrganizationUnit(t *testing.T) {
	service := &fakeOperationsService{unit: operations.DepartmentDTO{DepartmentID: "dept-ai", OrganizationID: "org-new", Name: "AI 小组", ParentDepartmentID: "dept-core"}}
	handler := newTestRouter(RouterOptions{Operations: service})

	req := authedRequest(http.MethodPost, "/api/org/units", `{"organization_id":"org-new","name":"AI 小组","parent_department_id":"dept-core"}`)
	req.Header.Set("X-Permission-Scope", "org.manage")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create unit status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.createdUnit.OrganizationID != "org-new" || service.createdUnit.Name != "AI 小组" || service.createdUnit.ParentDepartmentID != "dept-core" || service.createdUnit.DefaultPositionName != "" {
		t.Fatalf("create unit command = %+v", service.createdUnit)
	}
}

func TestHTTPRoutesOperationsCreateOrganizationPosition(t *testing.T) {
	service := &fakeOperationsService{position: operations.PositionDTO{PositionID: "position-ai", DepartmentID: "dept-ai", Name: "算法工程师"}}
	handler := newTestRouter(RouterOptions{Operations: service})

	req := authedRequest(http.MethodPost, "/api/org/positions", `{"organization_id":"org-new","department_id":"dept-ai","name":"算法工程师"}`)
	req.Header.Set("X-Permission-Scope", "org.manage")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create position status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.createdPosition.OrganizationID != "org-new" || service.createdPosition.DepartmentID != "dept-ai" || service.createdPosition.Name != "算法工程师" {
		t.Fatalf("create position command = %+v", service.createdPosition)
	}
	if !strings.Contains(rec.Body.String(), `"position_id":"position-ai"`) {
		t.Fatalf("create position response = %s", rec.Body.String())
	}
}

func TestHTTPRoutesOperationsDeleteOrganizationUnit(t *testing.T) {
	service := &fakeOperationsService{}
	handler := newTestRouter(RouterOptions{Operations: service})

	req := authedRequest(http.MethodDelete, "/api/org/units/dept-ai", "")
	req.Header.Set("X-Permission-Scope", "org.manage")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete unit status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.deletedUnitID != "dept-ai" {
		t.Fatalf("deleted unit = %s", service.deletedUnitID)
	}
}

func TestHTTPRoutesOperationsDeleteOrganizationPosition(t *testing.T) {
	service := &fakeOperationsService{}
	handler := newTestRouter(RouterOptions{Operations: service})

	req := authedRequest(http.MethodDelete, "/api/org/positions/position-ai", "")
	req.Header.Set("X-Permission-Scope", "org.manage")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete position status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.deletedPositionID != "position-ai" {
		t.Fatalf("deleted position = %s", service.deletedPositionID)
	}
}

func TestHTTPRoutesOperationsDeleteOrganization(t *testing.T) {
	service := &fakeOperationsService{}
	handler := newTestRouter(RouterOptions{Operations: service})

	req := authedRequest(http.MethodDelete, "/api/org/org-1", "")
	req.Header.Set("X-Permission-Scope", "org.manage")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete organization status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.deletedOrganizationID != "org-1" {
		t.Fatalf("deleted organization = %s", service.deletedOrganizationID)
	}
}

func TestHTTPRoutesOperationsUpdateOrganizationOwner(t *testing.T) {
	service := &fakeOperationsService{ownerNode: operations.OrganizationNodeDTO{
		ID: "dept-ai", OwnerProfileID: "human-owner", OwnerName: "Owner",
	}}
	handler := newTestRouter(RouterOptions{Operations: service})

	req := authedRequest(http.MethodPut, "/api/org/nodes/dept-ai/owner", `{"organization_node_id":"forged","owner_profile_id":"human-owner","owner_name":"Owner"}`)
	req.Header.Set("X-Permission-Scope", "org.manage")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("update owner status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.updatedOwner.OrganizationNodeID != "dept-ai" || service.updatedOwner.OwnerProfileID != "human-owner" {
		t.Fatalf("update owner command = %+v", service.updatedOwner)
	}
	if !strings.Contains(rec.Body.String(), `"owner_profile_id":"human-owner"`) {
		t.Fatalf("update owner response = %s", rec.Body.String())
	}
}

type fakeOperationsService struct {
	mu                    sync.Mutex
	workforce             operations.WorkforcePageDTO
	market                operations.WorkforcePageDTO
	graph                 operations.OrgGraphDTO
	matrix                operations.PermissionMatrixDTO
	permissionDecision    operations.PermissionDecisionDTO
	audit                 operations.AuditEventPageDTO
	evaluations           operations.EvaluationPageDTO
	roles                 operations.RoleDefinitionPageDTO
	roleAssignments       operations.RoleAssignmentPageDTO
	member                operations.WorkforceMemberDTO
	workforceQuery        operations.WorkforceQuery
	marketQuery           operations.WorkforceQuery
	orgQuery              operations.OrgGraphQuery
	permissionQuery       operations.PermissionMatrixQuery
	permissionDecisionReq operations.PermissionDecisionRequestDTO
	auditQuery            operations.AuditLogQuery
	evaluationQuery       operations.EvaluationQuery
	roleAssignmentQuery   operations.RoleAssignmentQuery
	putRoleAssignments    operations.PutRoleAssignmentsCommand
	putRoleDefinitions    operations.PutRoleDefinitionsCommand
	deletedRoleAssignment string
	createMember          operations.UpsertWorkforceMemberCommand
	upsert                operations.UpsertWorkforceMemberCommand
	enabled               operations.SetWorkforceMemberEnabledCommand
	release               operations.ReleaseWorkforceMemberCommand
	hire                  operations.HireTalentFromMarketCommand
	deleteMarket          operations.DeleteTalentMarketMemberCommand
	deleteMarketBatch     operations.DeleteTalentMarketMembersCommand
	previewImport         operations.TalentMarketImportPreviewRequest
	commitImport          operations.TalentMarketImportCommitRequest
	exportMarket          operations.TalentMarketExportRequest
	importPreview         operations.TalentMarketImportPreviewResponseDTO
	importCommit          operations.TalentMarketImportCommitResponseDTO
	exportPackage         operations.TalentMarketExportPackageDTO
	organization          operations.OrganizationDTO
	createdOrg            operations.CreateOrganizationCommand
	unit                  operations.DepartmentDTO
	createdUnit           operations.CreateOrganizationUnitCommand
	position              operations.PositionDTO
	createdPosition       operations.CreateOrganizationPositionCommand
	deletedOrganizationID string
	deletedUnitID         string
	deletedPositionID     string
	ownerNode             operations.OrganizationNodeDTO
	updatedOwner          operations.UpdateOrganizationOwnerCommand
	lastViewer            operations.ViewerRef
}

func (f *fakeOperationsService) ListWorkforceMembers(ctx context.Context, viewer operations.ViewerRef, query operations.WorkforceQuery) (operations.WorkforcePageDTO, error) {
	f.lastViewer = viewer
	f.workforceQuery = query
	return f.workforce, nil
}

func (f *fakeOperationsService) ListTalentMarketMembers(ctx context.Context, viewer operations.ViewerRef, query operations.WorkforceQuery) (operations.WorkforcePageDTO, error) {
	f.lastViewer = viewer
	f.marketQuery = query
	return f.market, nil
}

func (f *fakeOperationsService) GetOrgGraph(ctx context.Context, viewer operations.ViewerRef, query operations.OrgGraphQuery) (operations.OrgGraphDTO, error) {
	f.orgQuery = query
	return f.graph, nil
}

func (f *fakeOperationsService) CreateOrganization(ctx context.Context, viewer operations.ViewerRef, cmd operations.CreateOrganizationCommand) (operations.OrganizationDTO, error) {
	f.lastViewer = viewer
	f.createdOrg = cmd
	return f.organization, nil
}

func (f *fakeOperationsService) CreateOrganizationUnit(ctx context.Context, viewer operations.ViewerRef, cmd operations.CreateOrganizationUnitCommand) (operations.DepartmentDTO, error) {
	f.lastViewer = viewer
	f.createdUnit = cmd
	return f.unit, nil
}

func (f *fakeOperationsService) CreateOrganizationPosition(ctx context.Context, viewer operations.ViewerRef, cmd operations.CreateOrganizationPositionCommand) (operations.PositionDTO, error) {
	f.lastViewer = viewer
	f.createdPosition = cmd
	return f.position, nil
}

func (f *fakeOperationsService) DeleteOrganizationUnit(ctx context.Context, viewer operations.ViewerRef, departmentID string) error {
	f.lastViewer = viewer
	f.deletedUnitID = departmentID
	return nil
}

func (f *fakeOperationsService) DeleteOrganizationPosition(ctx context.Context, viewer operations.ViewerRef, positionID string) error {
	f.lastViewer = viewer
	f.deletedPositionID = positionID
	return nil
}

func (f *fakeOperationsService) DeleteOrganization(ctx context.Context, viewer operations.ViewerRef, organizationID string) error {
	f.lastViewer = viewer
	f.deletedOrganizationID = organizationID
	return nil
}

func (f *fakeOperationsService) UpdateOrganizationOwner(ctx context.Context, viewer operations.ViewerRef, cmd operations.UpdateOrganizationOwnerCommand) (operations.OrganizationNodeDTO, error) {
	f.lastViewer = viewer
	f.updatedOwner = cmd
	return f.ownerNode, nil
}

func (f *fakeOperationsService) GetPermissionMatrix(ctx context.Context, viewer operations.ViewerRef, query operations.PermissionMatrixQuery) (operations.PermissionMatrixDTO, error) {
	f.permissionQuery = query
	return f.matrix, nil
}

func (f *fakeOperationsService) DecidePermission(ctx context.Context, viewer operations.ViewerRef, req operations.PermissionDecisionRequestDTO) (operations.PermissionDecisionDTO, error) {
	f.lastViewer = viewer
	f.permissionDecisionReq = req
	return f.permissionDecision, nil
}

func (f *fakeOperationsService) ListAuditLog(ctx context.Context, viewer operations.ViewerRef, query operations.AuditLogQuery) (operations.AuditEventPageDTO, error) {
	f.auditQuery = query
	return f.audit, nil
}

func (f *fakeOperationsService) ListEvaluations(ctx context.Context, viewer operations.ViewerRef, query operations.EvaluationQuery) (operations.EvaluationPageDTO, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.evaluationQuery = query
	return f.evaluations, nil
}

func (f *fakeOperationsService) ListRoleDefinitions(ctx context.Context, viewer operations.ViewerRef) (operations.RoleDefinitionPageDTO, error) {
	f.lastViewer = viewer
	return f.roles, nil
}

func (f *fakeOperationsService) ListRoleAssignments(ctx context.Context, viewer operations.ViewerRef, query operations.RoleAssignmentQuery) (operations.RoleAssignmentPageDTO, error) {
	f.lastViewer = viewer
	f.roleAssignmentQuery = query
	return f.roleAssignments, nil
}

func (f *fakeOperationsService) PutRoleAssignments(ctx context.Context, viewer operations.ViewerRef, cmd operations.PutRoleAssignmentsCommand) (operations.RoleAssignmentPageDTO, error) {
	f.lastViewer = viewer
	f.putRoleAssignments = cmd
	return f.roleAssignments, nil
}

func (f *fakeOperationsService) PutRoleDefinitions(ctx context.Context, viewer operations.ViewerRef, cmd operations.PutRoleDefinitionsCommand) (operations.RoleDefinitionPageDTO, error) {
	f.lastViewer = viewer
	f.putRoleDefinitions = cmd
	f.roles = operations.RoleDefinitionPageDTO{Items: append([]operations.RoleDefinitionDTO(nil), cmd.Roles...), Page: operations.PageDTO{Total: len(cmd.Roles), Returned: len(cmd.Roles)}}
	return f.roles, nil
}

func (f *fakeOperationsService) DeleteRoleAssignment(ctx context.Context, viewer operations.ViewerRef, assignmentID string) error {
	f.lastViewer = viewer
	f.deletedRoleAssignment = assignmentID
	return nil
}

func (f *fakeOperationsService) setEvaluations(page operations.EvaluationPageDTO) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.evaluations = page
}

func (f *fakeOperationsService) evaluationQuerySnapshot() operations.EvaluationQuery {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.evaluationQuery
}

func (f *fakeOperationsService) UpsertWorkforceMember(ctx context.Context, viewer operations.ViewerRef, cmd operations.UpsertWorkforceMemberCommand) (operations.WorkforceMemberDTO, error) {
	f.lastViewer = viewer
	f.upsert = cmd
	return f.member, nil
}

func (f *fakeOperationsService) CreateWorkforceMember(ctx context.Context, viewer operations.ViewerRef, cmd operations.UpsertWorkforceMemberCommand) (operations.WorkforceMemberDTO, error) {
	f.lastViewer = viewer
	f.createMember = cmd
	return f.member, nil
}

func (f *fakeOperationsService) SetWorkforceMemberEnabled(ctx context.Context, viewer operations.ViewerRef, cmd operations.SetWorkforceMemberEnabledCommand) error {
	f.lastViewer = viewer
	f.enabled = cmd
	return nil
}

func (f *fakeOperationsService) ReleaseWorkforceMember(ctx context.Context, viewer operations.ViewerRef, cmd operations.ReleaseWorkforceMemberCommand) error {
	f.lastViewer = viewer
	f.release = cmd
	return nil
}

func (f *fakeOperationsService) HireTalentFromMarket(ctx context.Context, viewer operations.ViewerRef, cmd operations.HireTalentFromMarketCommand) (operations.WorkforceMemberDTO, error) {
	f.lastViewer = viewer
	f.hire = cmd
	return f.member, nil
}

func (f *fakeOperationsService) DeleteTalentMarketMember(ctx context.Context, viewer operations.ViewerRef, cmd operations.DeleteTalentMarketMemberCommand) error {
	f.lastViewer = viewer
	f.deleteMarket = cmd
	return nil
}

func (f *fakeOperationsService) DeleteTalentMarketMembers(ctx context.Context, viewer operations.ViewerRef, cmd operations.DeleteTalentMarketMembersCommand) (operations.DeleteTalentMarketMembersResponseDTO, error) {
	f.lastViewer = viewer
	f.deleteMarketBatch = cmd
	return operations.DeleteTalentMarketMembersResponseDTO{
		Deleted:      append([]string(nil), cmd.AgentProfileIDs...),
		DeletedCount: len(cmd.AgentProfileIDs),
	}, nil
}

func (f *fakeOperationsService) PreviewTalentMarketImport(ctx context.Context, viewer operations.ViewerRef, req operations.TalentMarketImportPreviewRequest) (operations.TalentMarketImportPreviewResponseDTO, error) {
	f.lastViewer = viewer
	f.previewImport = req
	return f.importPreview, nil
}

func (f *fakeOperationsService) CommitTalentMarketImport(ctx context.Context, viewer operations.ViewerRef, req operations.TalentMarketImportCommitRequest) (operations.TalentMarketImportCommitResponseDTO, error) {
	f.lastViewer = viewer
	f.commitImport = req
	return f.importCommit, nil
}

func (f *fakeOperationsService) ExportTalentMarket(ctx context.Context, viewer operations.ViewerRef, req operations.TalentMarketExportRequest) (operations.TalentMarketExportPackageDTO, error) {
	f.lastViewer = viewer
	f.exportMarket = req
	return f.exportPackage, nil
}

func talentMarketImportMultipartBody(t *testing.T, files map[string]string) (*bytes.Buffer, string) {
	t.Helper()
	var zipBody bytes.Buffer
	zipWriter := zip.NewWriter(&zipBody)
	for name, content := range files {
		fileWriter, err := zipWriter.Create(name)
		if err != nil {
			t.Fatalf("create zip entry: %v", err)
		}
		if _, err := fileWriter.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry: %v", err)
		}
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("close zip: %v", err)
	}

	var body bytes.Buffer
	multipartWriter := multipart.NewWriter(&body)
	part, err := multipartWriter.CreateFormFile("file", "agents.zip")
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(zipBody.Bytes()); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return &body, multipartWriter.FormDataContentType()
}

func zipHasFile(reader *zip.Reader, name string) bool {
	for _, file := range reader.File {
		if file.Name == name {
			return true
		}
	}
	return false
}
