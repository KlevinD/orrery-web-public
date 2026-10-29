package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/memoryadmin"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	operations "orrery_v2/internal/app/operations"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	missioncore "orrery_v2/internal/mission/core"
)

func TestDemoAPIPersistsUserDataInBackendDatabaseWithoutCodeSeed(t *testing.T) {
	databaseURL := "memory://" + t.Name()
	api := newDemoAPI()
	if err := api.attachDemoDatabase(context.Background(), databaseURL); err != nil {
		t.Fatalf("attachDemoDatabase: %v", err)
	}

	missionPage, err := api.ListMissions(context.Background(), missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.ListMissionsQuery{})
	if err != nil {
		t.Fatalf("ListMissions: %v", err)
	}
	if missionPage.Page.Total != 0 || len(missionPage.Missions) != 0 {
		t.Fatalf("new demo API should not code-seed missions: %+v", missionPage)
	}
	workforce, err := api.ListWorkforceMembers(context.Background(), operations.ViewerRef{ViewerID: "operator"}, operations.WorkforceQuery{})
	if err != nil {
		t.Fatalf("ListWorkforceMembers: %v", err)
	}
	if workforce.Page.Total != 0 || len(workforce.Members) != 0 {
		t.Fatalf("new demo API should not code-seed workforce: %+v", workforce)
	}

	member, err := api.UpsertWorkforceMember(context.Background(), operations.ViewerRef{ViewerID: "operator", PermissionScope: []string{operations.PermissionWorkforceManage}}, operations.UpsertWorkforceMemberCommand{
		AgentProfileID:  "AGT-TEST",
		AgentInstanceID: "AGT-TEST:runtime",
		RealName:        "Test Member",
		OrganizationID:  "ORG-ORRERY",
		DepartmentID:    "DEPT-PRODUCT",
		PositionID:      "POS-TEST",
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember: %v", err)
	}
	record, ok, err := api.router.GetRecord(context.Background(), demoWorkforceTable, "default")
	if err != nil || !ok {
		t.Fatalf("GetRecord(%s, default) record=%+v ok=%v err=%v", demoWorkforceTable, record, ok, err)
	}
	var workforcePage operations.WorkforcePageDTO
	if err := decodePayload(record, &workforcePage); err != nil {
		t.Fatalf("decode workforce payload: %v", err)
	}
	found := false
	for _, persisted := range workforcePage.Members {
		if persisted.AgentProfileID == member.AgentProfileID {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("persisted workforce payload missing %s: %+v", member.AgentProfileID, workforcePage.Members)
	}
	if err := api.ReleaseWorkforceMember(context.Background(), operations.ViewerRef{ViewerID: "operator"}, operations.ReleaseWorkforceMemberCommand{AgentProfileID: member.AgentProfileID, Reason: "available for other work"}); err != nil {
		t.Fatalf("ReleaseWorkforceMember: %v", err)
	}

	mission, err := api.CreateMission(context.Background(), missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.CreateMissionCommand{
		Title:     "Persisted mission",
		FinalGoal: "Verify mission index reloads newly persisted missions.",
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}
	if err := api.DeleteMission(context.Background(), missioncontrol.ViewerRef{ViewerID: "operator", ViewerKind: missioncontrol.ViewerKindUser}, missioncore.MissionID(mission.MissionID)); err != nil {
		t.Fatalf("DeleteMission: %v", err)
	}
	reloaded := newDemoAPI()
	if err := reloaded.attachDemoDatabase(context.Background(), databaseURL); err != nil {
		t.Fatalf("reload attachDemoDatabase: %v", err)
	}
	if _, ok := reloaded.missions[mission.MissionID]; ok {
		t.Fatalf("deleted mission reloaded from database: %+v", reloaded.missions)
	}
	internalWorkforce, err := reloaded.ListWorkforceMembers(context.Background(), operations.ViewerRef{ViewerID: "operator"}, operations.WorkforceQuery{})
	if err != nil {
		t.Fatalf("ListWorkforceMembers: %v", err)
	}
	if internalWorkforce.Page.Total != 0 || len(internalWorkforce.Members) != 0 {
		t.Fatalf("released workforce member should not remain internal after reload: %+v", internalWorkforce)
	}
	market, err := reloaded.ListTalentMarketMembers(context.Background(), operations.ViewerRef{ViewerID: "operator"}, operations.WorkforceQuery{})
	if err != nil {
		t.Fatalf("ListTalentMarketMembers: %v", err)
	}
	if market.Page.Total != 1 || market.Members[0].AgentProfileID != member.AgentProfileID || market.Members[0].PlacementStatus != "market" {
		t.Fatalf("released workforce member should reload in talent market: %+v", market)
	}
}

func TestDemoAPIRejectsWorkforceUpsertWithoutAgentInstance(t *testing.T) {
	api := newDemoAPI()
	_, err := api.UpsertWorkforceMember(context.Background(), operations.ViewerRef{ViewerID: "operator", PermissionScope: []string{operations.PermissionWorkforceManage}}, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "AGT-TEST",
		RealName:       "Test Member",
		OrganizationID: "ORG-ORRERY",
		DepartmentID:   "DEPT-PRODUCT",
		PositionID:     "POS-TEST",
		Enabled:        true,
	})
	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("UpsertWorkforceMember error = %v, want ErrInvalidRequest", err)
	}
	if len(api.workforce.Members) != 0 {
		t.Fatalf("workforce upsert without runtime instance should not persist: %+v", api.workforce.Members)
	}
}

func TestDemoAPIWorkforceUpsertUpdatesExistingMemberAndKeepsAgentInstance(t *testing.T) {
	api := newDemoAPI()
	viewer := operations.ViewerRef{ViewerID: "operator", PermissionScope: []string{operations.PermissionWorkforceManage}}
	if _, err := api.UpsertWorkforceMember(context.Background(), viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID:  "AGT-TEST",
		AgentInstanceID: "AGT-TEST:runtime",
		RealName:        "Test Member",
		OrganizationID:  "ORG-ORRERY",
		DepartmentID:    "DEPT-PRODUCT",
		PositionID:      "POS-TEST",
		Enabled:         true,
	}); err != nil {
		t.Fatalf("initial UpsertWorkforceMember: %v", err)
	}
	updated, err := api.UpsertWorkforceMember(context.Background(), viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "AGT-TEST",
		RealName:       "Updated Member",
		DisplayName:    "Updated",
		OrganizationID: "ORG-ORRERY",
		DepartmentID:   "DEPT-PRODUCT",
		PositionID:     "POS-TEST",
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("update UpsertWorkforceMember: %v", err)
	}
	if updated.AgentInstanceID != "AGT-TEST:runtime" || updated.DisplayName != "Updated" {
		t.Fatalf("updated member = %+v", updated)
	}
	if len(api.workforce.Members) != 1 || api.workforce.Members[0].AgentInstanceID != "AGT-TEST:runtime" || api.workforce.Members[0].DisplayName != "Updated" {
		t.Fatalf("workforce members = %+v", api.workforce.Members)
	}
}

func TestDemoServerStartsWithEmptyInstanceData(t *testing.T) {
	handler := newDemoHandlerWithSecurity(true, testInternalViewerAuthorization)
	req := httptest.NewRequest(http.MethodGet, "/api/missions?limit=1", nil)
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", "mission.read,operations.manage,resources.manage,approvals.decide")
	rec := httptest.NewRecorder()

	serveServerTestHTTP(t, handler, rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var page missioncontrol.MissionListDTO
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode mission list: %v", err)
	}
	if page.Page.Total != 0 || len(page.Missions) != 0 {
		t.Fatalf("mission page = %+v", page)
	}
}

func TestDemoAPIReportsMemoryProfilesUnavailableInsteadOfFakeResources(t *testing.T) {
	api := newDemoAPI()

	_, err := api.ListResourcesFiltered(context.Background(), resourceadmin.ViewerRef{
		ViewerID:        "operator",
		PermissionScope: []string{resourceadmin.PermissionResourceRead},
	}, resourceadmin.ListResourcesRequest{Kind: "memory_profile"})

	if !errors.Is(err, resourceadmin.ErrServiceDependency) {
		t.Fatalf("expected memory profile demo query to report service dependency, got %v", err)
	}
}

func TestDemoAPIReportsMCPRuntimeNotConnectedInsteadOfFakeStatus(t *testing.T) {
	api := newDemoAPI()
	viewer := resourceadmin.ViewerRef{
		ViewerID:        "operator",
		PermissionScope: []string{resourceadmin.PermissionResourceManage, resourceadmin.PermissionResourceRead},
	}

	health, err := api.GetMCPServerHealth(context.Background(), viewer, "mcp.demo")
	if err != nil {
		t.Fatalf("GetMCPServerHealth: %v", err)
	}
	if health.Status != "not_connected" || health.FailureReason == "" {
		t.Fatalf("demo health should report not_connected: %+v", health)
	}

	sync, err := api.SyncMCPServer(context.Background(), viewer, "mcp.demo")
	if err != nil {
		t.Fatalf("SyncMCPServer: %v", err)
	}
	if sync.Status != "not_connected" || sync.FailureReason == "" || sync.ExposedToolCount != 0 {
		t.Fatalf("demo sync should report not_connected: %+v", sync)
	}
}

func TestDemoAPIRejectsResourceCommandsInsteadOfFakeSuccess(t *testing.T) {
	api := newDemoAPI()
	ctx := context.Background()
	viewer := resourceadmin.ViewerRef{
		ViewerID:        "operator",
		PermissionScope: []string{resourceadmin.PermissionResourceManage, resourceadmin.PermissionResourceRead},
	}

	resourceChecks := []struct {
		name string
		run  func() error
	}{
		{name: "RegisterMCPServer", run: func() error {
			_, err := api.RegisterMCPServer(ctx, viewer, resourceadmin.RegisterMCPServerRequest{ToolID: "mcp.demo", Name: "Demo MCP", ServerName: "demo", Command: "demo-mcp"})
			return err
		}},
		{name: "UpdateMCPServer", run: func() error {
			_, err := api.UpdateMCPServer(ctx, viewer, resourceadmin.RegisterMCPServerRequest{ToolID: "mcp.demo", Name: "Demo MCP", ServerName: "demo", Command: "demo-mcp"})
			return err
		}},
		{name: "RegisterTool", run: func() error {
			_, err := api.RegisterTool(ctx, viewer, resourceadmin.ToolDefinitionRequest{ToolID: "tool.demo", Name: "Demo Tool", Kind: "cli_tool", Command: "demo"})
			return err
		}},
		{name: "UpdateTool", run: func() error {
			_, err := api.UpdateTool(ctx, viewer, resourceadmin.ToolDefinitionRequest{ToolID: "tool.demo", Name: "Demo Tool", Kind: "cli_tool", Command: "demo"})
			return err
		}},
		{name: "ImportSkillPackage", run: func() error {
			_, err := api.ImportSkillPackage(ctx, viewer, strings.NewReader("demo"), int64(len("demo")))
			return err
		}},
		{name: "SaveSkillMarkdown", run: func() error {
			_, err := api.SaveSkillMarkdown(ctx, viewer, "skill.demo", "# Demo")
			return err
		}},
		{name: "GetSkillFile", run: func() error {
			_, err := api.GetSkillFile(ctx, viewer, resourceadmin.SkillFileRequest{SkillID: "skill.demo", Path: "resources/demo.txt"})
			return err
		}},
		{name: "DisableTool", run: func() error {
			return api.DisableTool(ctx, viewer, "tool.demo")
		}},
		{name: "DisableSkill", run: func() error {
			return api.DisableSkill(ctx, viewer, "skill.demo")
		}},
		{name: "DeleteMCPServer", run: func() error {
			return api.DeleteMCPServer(ctx, viewer, "mcp.demo")
		}},
		{name: "DeleteSkill", run: func() error {
			return api.DeleteSkill(ctx, viewer, "skill.demo")
		}},
		{name: "ExportSkillPackage", run: func() error {
			_, err := api.ExportSkillPackage(ctx, viewer, "skill.demo")
			return err
		}},
		{name: "BindResourceToAgent", run: func() error {
			_, err := api.BindResourceToAgent(ctx, viewer, resourceadmin.BindResourceRequest{AgentID: "agent.demo", ResourceType: "tool", ResourceID: "tool.demo"})
			return err
		}},
		{name: "UnbindResourceFromAgent", run: func() error {
			return api.UnbindResourceFromAgent(ctx, viewer, resourceadmin.UnbindResourceRequest{AgentID: "agent.demo", ResourceType: "tool", ResourceID: "tool.demo"})
		}},
	}
	for _, check := range resourceChecks {
		if err := check.run(); !errors.Is(err, resourceadmin.ErrServiceDependency) {
			t.Fatalf("%s error = %v, want ErrServiceDependency", check.name, err)
		}
	}
}

func TestDemoAPIRejectsOrganizationDeleteInsteadOfFakeSuccess(t *testing.T) {
	api := newDemoAPI()

	err := api.DeleteOrganization(context.Background(), operations.ViewerRef{
		ViewerID:        "operator",
		PermissionScope: []string{operations.PermissionOrgManage},
	}, "org-demo")

	if !errors.Is(err, operations.ErrServiceDependency) {
		t.Fatalf("DeleteOrganization error = %v, want ErrServiceDependency", err)
	}
}

func TestDefaultDemoModeIsDisabledEvenWhenEnvRequestsDemo(t *testing.T) {
	t.Setenv("ORRERY_API_DEMO", "")
	if defaultDemoMode() {
		t.Fatal("default demo mode should be disabled")
	}
	t.Setenv("ORRERY_API_DEMO", "false")
	if defaultDemoMode() {
		t.Fatal("default demo mode should stay disabled when ORRERY_API_DEMO=false")
	}
	t.Setenv("ORRERY_API_DEMO", "true")
	if defaultDemoMode() {
		t.Fatal("ORRERY_API_DEMO=true must not enable demo handler")
	}
}

func TestServerHandlerDefaultsToProductionServices(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	executable := writeCodexTextRuntimeFixture(t)
	setTestConfigOverride(t, "discussion.agent.runtime_kind", "codex")
	setTestConfigOverride(t, "discussion.agent.runtime_config", `{"executable":"`+jsonPath(executable)+`","extra_args":["--model","gpt-5-codex"]}`)
	setTestConfigOverride(t, "discussion.agent.timeout", "2s")
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()
	configureRuntimeFixtureLLMProfile(t, handler, "gpt-5-codex")

	memoryCandidates := httptest.NewRequest(http.MethodGet, "/api/memory/candidates?limit=1", nil)
	memoryCandidates.Header.Set("X-User-ID", "operator")
	memoryCandidates.Header.Set("X-Viewer-Kind", "user")
	memoryCandidates.Header.Set("X-Permission-Scope", "memory.read,memory.audit.read")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, memoryCandidates)
	if rec.Code != http.StatusOK {
		t.Fatalf("memory candidates status = %d body = %s", rec.Code, rec.Body.String())
	}
	var memoryPage memoryadmin.MemoryCandidatePageDTO
	if err := json.NewDecoder(rec.Body).Decode(&memoryPage); err != nil {
		t.Fatalf("decode memory candidates: %v", err)
	}
	if memoryPage.Total != 0 || len(memoryPage.Candidates) != 0 {
		t.Fatalf("new production handler should start with empty memory candidates: %+v", memoryPage)
	}

	distillReq := httptest.NewRequest(http.MethodPost, "/api/memory/distill/experience", jsonBody(`{"target_scope":{"kind":"project","id":"project-1"},"source_scope":{"kind":"mission","id":"mission-1"},"topic":"qa"}`))
	distillReq.Header.Set("X-User-ID", "operator")
	distillReq.Header.Set("X-Viewer-Kind", "user")
	distillReq.Header.Set("X-Permission-Scope", "memory.distill")
	distillReq.Header.Set("X-Project-ID", "project-1")
	distillReq.Header.Set("X-Mission-ID", "mission-1")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, distillReq)
	if rec.Code == http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "memory_distillation_dependency_unavailable") {
		t.Fatalf("memory distill service was not wired: status=%d body=%s", rec.Code, rec.Body.String())
	}

	create := httptest.NewRequest(http.MethodPost, "/api/org", jsonBody(`{"name":"Platform"}`))
	create.Header.Set("X-User-ID", "operator")
	create.Header.Set("X-Viewer-Kind", "user")
	create.Header.Set("X-Permission-Scope", "org.manage")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create org status = %d body = %s", rec.Code, rec.Body.String())
	}
	var organization operations.OrganizationDTO
	if err := json.NewDecoder(rec.Body).Decode(&organization); err != nil {
		t.Fatalf("decode organization: %v", err)
	}
	if !opaqueID("org").MatchString(organization.OrganizationID) {
		t.Fatalf("organization id = %q", organization.OrganizationID)
	}
	if organization.DefaultDepartmentID != "" || organization.DefaultPositionID != "" {
		t.Fatalf("new organizations must not create default department or position: %+v", organization)
	}

	createUnit := httptest.NewRequest(http.MethodPost, "/api/org/units", jsonBody(`{"organization_id":"`+organization.OrganizationID+`","name":"Operations"}`))
	createUnit.Header.Set("X-User-ID", "operator")
	createUnit.Header.Set("X-Viewer-Kind", "user")
	createUnit.Header.Set("X-Permission-Scope", "org.manage")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, createUnit)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create unit status = %d body = %s", rec.Code, rec.Body.String())
	}
	var department operations.DepartmentDTO
	if err := json.NewDecoder(rec.Body).Decode(&department); err != nil {
		t.Fatalf("decode department: %v", err)
	}
	createPosition := httptest.NewRequest(http.MethodPost, "/api/org/positions", jsonBody(`{"organization_id":"`+organization.OrganizationID+`","department_id":"`+department.DepartmentID+`","name":"Operator"}`))
	createPosition.Header.Set("X-User-ID", "operator")
	createPosition.Header.Set("X-Viewer-Kind", "user")
	createPosition.Header.Set("X-Permission-Scope", "org.manage")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, createPosition)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create position status = %d body = %s", rec.Code, rec.Body.String())
	}

	list := httptest.NewRequest(http.MethodGet, "/api/org/graph?organization_id="+organization.OrganizationID, nil)
	list.Header.Set("X-User-ID", "operator")
	list.Header.Set("X-Viewer-Kind", "user")
	list.Header.Set("X-Permission-Scope", "org.read")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, list)
	if rec.Code != http.StatusOK {
		t.Fatalf("org graph status = %d body = %s", rec.Code, rec.Body.String())
	}
	var graph operations.OrgGraphDTO
	if err := json.NewDecoder(rec.Body).Decode(&graph); err != nil {
		t.Fatalf("decode org graph: %v", err)
	}
	if len(graph.Organizations) != 1 || graph.Organizations[0].Name != "Platform" {
		t.Fatalf("production graph did not persist organization catalog: %+v", graph)
	}
	if len(graph.Departments) != 1 || graph.Departments[0].Name != "Operations" {
		t.Fatalf("production graph did not persist created department: %+v", graph.Departments)
	}
	if len(graph.Positions) != 1 || graph.Positions[0].Name != "Operator" {
		t.Fatalf("production graph did not persist created position: %+v", graph.Positions)
	}

	workforceReq := httptest.NewRequest(http.MethodGet, "/api/workforce/members?enabled_only=true", nil)
	workforceReq.Header.Set("X-User-ID", "operator")
	workforceReq.Header.Set("X-Viewer-Kind", "user")
	workforceReq.Header.Set("X-Permission-Scope", "workforce.read")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, workforceReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("workforce status = %d body = %s", rec.Code, rec.Body.String())
	}
	var workforce operations.WorkforcePageDTO
	if err := json.NewDecoder(rec.Body).Decode(&workforce); err != nil {
		t.Fatalf("decode workforce: %v", err)
	}
	defaultAgents := map[string]bool{}
	defaultAgentNames := map[string]string{}
	for _, member := range workforce.Members {
		defaultAgents[member.AgentProfileID] = true
		defaultAgentNames[member.AgentProfileID] = member.DisplayName
	}
	for _, want := range []string{defaultLeadAgentID, defaultCoordinatorAgentID} {
		if !defaultAgents[want] {
			t.Fatalf("default agent %s missing from production workforce: %+v", want, workforce.Members)
		}
	}
	if defaultAgentNames[defaultLeadAgentID] == "" || defaultAgentNames[defaultLeadAgentID] == defaultLeadAgentID ||
		defaultAgentNames[defaultCoordinatorAgentID] == "" || defaultAgentNames[defaultCoordinatorAgentID] == defaultCoordinatorAgentID {
		t.Fatalf("default coordinator agents should have readable names, got %+v", defaultAgentNames)
	}

	namedUserReq := httptest.NewRequest(http.MethodGet, "/api/workforce/members?enabled_only=true", nil)
	namedUserReq.Header.Set("X-User-ID", "local-user")
	namedUserReq.Header.Set("X-User-Display-Name", "%E6%9C%AC%E5%9C%B0%E7%94%A8%E6%88%B7")
	namedUserReq.Header.Set("X-Viewer-Kind", "user")
	namedUserReq.Header.Set("X-Permission-Scope", "workforce.read")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, namedUserReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("named workforce status = %d body = %s", rec.Code, rec.Body.String())
	}
	if err := json.NewDecoder(rec.Body).Decode(&workforce); err != nil {
		t.Fatalf("decode named workforce: %v", err)
	}
	for _, member := range workforce.Members {
		if member.AgentProfileID == "human-local-user" {
			t.Fatalf("workforce read created current user profile: %+v", member)
		}
	}

	discussionList := httptest.NewRequest(http.MethodGet, "/api/discussions", nil)
	discussionList.Header.Set("X-User-ID", "operator")
	discussionList.Header.Set("X-Viewer-Kind", "user")
	discussionList.Header.Set("X-Permission-Scope", "discussion.read")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, discussionList)
	if rec.Code != http.StatusOK {
		t.Fatalf("discussion list status = %d body = %s", rec.Code, rec.Body.String())
	}

	discussionSend := httptest.NewRequest(http.MethodPost, "/api/discussions/messages", jsonBody(`{"content":"Plan a launch"}`))
	discussionSend.Header.Set("X-User-ID", "superadmin")
	discussionSend.Header.Set("X-Viewer-Kind", "user")
	discussionSend.Header.Set("X-User-Display-Name", "superadmin")
	discussionSend.Header.Set("X-Permission-Scope", "discussion.write")
	discussionSend.Header.Set("Idempotency-Key", "discussion-send-1")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, discussionSend)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("discussion send should use default discussion AgentProfile runtime, status = %d body = %s", rec.Code, rec.Body.String())
	}
	var conversation map[string]any
	if err := json.NewDecoder(rec.Body).Decode(&conversation); err != nil {
		t.Fatalf("decode discussion: %v", err)
	}
	conversationID, _ := conversation["discussion_id"].(string)
	if conversationID == "" || conversation["status"] != "completed" {
		t.Fatalf("discussion did not complete through default runtime: %+v", conversation)
	}

	debugReq := httptest.NewRequest(http.MethodGet, "/api/discussions/"+conversationID+"/debug/latest", nil)
	debugReq.Header.Set("X-User-ID", "superadmin")
	debugReq.Header.Set("X-Viewer-Kind", "user")
	debugReq.Header.Set("X-Permission-Scope", "discussion.read")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, debugReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("discussion debug status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"runtime_kind":"codex"`) || !strings.Contains(rec.Body.String(), "Plan a launch") {
		t.Fatalf("discussion debug did not capture selected default profile runtime and prompt: %s", rec.Body.String())
	}
}

func TestServerHandlerRejectsExplicitDemoMode(t *testing.T) {
	handler, cleanup, err := newServerHandler(context.Background(), true)
	if err == nil {
		if cleanup != nil {
			cleanup()
		}
		t.Fatalf("newServerHandler demo returned handler %T, want explicit failure", handler)
	}
	if !strings.Contains(err.Error(), "demo API handler is disabled") {
		t.Fatalf("newServerHandler demo error = %v", err)
	}
}
