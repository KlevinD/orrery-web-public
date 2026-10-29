package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
	missionprojection "orrery_v2/internal/mission/projection"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
	httptransport "orrery_v2/internal/transport/http"
)

func TestProductionResourceDiscoveryUsesConfiguredRegistry(t *testing.T) {
	registry := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v0/servers" {
			t.Fatalf("registry path = %s", req.URL.Path)
		}
		if req.URL.Query().Get("q") != "linear" {
			t.Fatalf("registry query = %s", req.URL.RawQuery)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"servers":[{"id":"linear","name":"Linear MCP","version":"1.0.0","packages":[{"registry_type":"npm","identifier":"@linear/mcp","version":"1.0.0"}],"tools":["issue.search"]}]}`))
	}))
	defer registry.Close()
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	setTestConfigOverride(t, "resource.mcp_registry_url", registry.URL)
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/resources/mcp-servers/discover?query=linear&limit=5", nil)
	req.Header.Set("X-User-ID", "superadmin")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Permission-Scope", "resource.read,resource.manage")
	rec := httptest.NewRecorder()

	serveServerTestHTTP(t, handler, rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"discovered_id":"linear"`) || !strings.Contains(rec.Body.String(), `"Linear MCP"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestProductionResourceCatalogSeedsSystemToolsAndStandardSkills(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()

	mcpReq := httptest.NewRequest(http.MethodGet, "/api/resources/mcp-servers", nil)
	mcpReq.Header.Set("X-User-ID", "superadmin")
	mcpReq.Header.Set("X-Viewer-Kind", "user")
	mcpReq.Header.Set("X-Permission-Scope", "resource.read")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, mcpReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("mcp status = %d body = %s", rec.Code, rec.Body.String())
	}
	var mcpServers resourceadmin.ResourceAdminViewDTO
	if err := json.NewDecoder(rec.Body).Decode(&mcpServers); err != nil {
		t.Fatalf("decode mcp servers: %v", err)
	}
	mcpByID := map[string]resourceadmin.MCPServerDTO{}
	for _, server := range mcpServers.MCPServers {
		mcpByID[server.ToolID] = server
	}
	systemMCP, ok := mcpByID["mcp.system"]
	if !ok {
		t.Fatalf("system mcp server was not seeded into resource catalog: %+v", mcpServers.MCPServers)
	}
	for _, id := range []string{
		"mission_prepare",
		"mission_coordinator_plan_parallel",
		"discussion_talent_list",
		"discussion_member_add",
		"discussion_ack",
		"discussion_close",
		"scheduled_task_create",
		"scheduled_task_update",
		"scheduled_task_cancel",
		"scheduled_task_list",
		"scheduled_task_run_now",
	} {
		if !containsString(systemMCP.ExposedTools, id) {
			t.Fatalf("system mcp omitted %s: %+v", id, systemMCP.ExposedTools)
		}
	}
	for _, id := range []string{"mission_context_list", "mission_context_select", "mission_brief_create", "mission_task_create_root", "mission_task_delegate"} {
		if containsString(systemMCP.ExposedTools, id) {
			t.Fatalf("deprecated system tool %s should not be exposed: %+v", id, systemMCP.ExposedTools)
		}
	}
	if systemMCP.AvailabilityStatus != "available" {
		t.Fatalf("system mcp availability status = %q", systemMCP.AvailabilityStatus)
	}
	legalMCP, ok := mcpByID["mcp.ieado_legal"]
	if !ok {
		t.Fatalf("legal mcp server was not seeded into resource catalog: %+v", mcpServers.MCPServers)
	}
	if !containsString(legalMCP.ExposedTools, "legal_enterprise_search") ||
		!containsString(legalMCP.ExposedTools, "legal_contract_review_run") ||
		!containsString(legalMCP.ExposedTools, "legal_intake_run") {
		t.Fatalf("legal mcp exposed tools = %+v", legalMCP.ExposedTools)
	}

	skillsReq := httptest.NewRequest(http.MethodGet, "/api/resources/skills", nil)
	skillsReq.Header.Set("X-User-ID", "superadmin")
	skillsReq.Header.Set("X-Viewer-Kind", "user")
	skillsReq.Header.Set("X-Permission-Scope", "resource.read")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, skillsReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("skills status = %d body = %s", rec.Code, rec.Body.String())
	}
	var skills resourceadmin.ResourceAdminViewDTO
	if err := json.NewDecoder(rec.Body).Decode(&skills); err != nil {
		t.Fatalf("decode skills: %v", err)
	}
	if !resourceSkillIDs(skills.Skills)["skill.mission-breakdown"] ||
		!resourceSkillIDs(skills.Skills)["skill.mission-task-breakdown"] ||
		!resourceSkillIDs(skills.Skills)["skill.frontend-design"] ||
		!resourceSkillIDs(skills.Skills)["skill.web-artifacts-builder"] {
		t.Fatalf("standard template skills were not seeded into resource catalog: %+v", skills.Skills)
	}
}

func TestBootstrapAdminScopesIncludeSchedulerKnowledgeMemoryAndLegalMCPPermissions(t *testing.T) {
	scopes := map[string]bool{}
	for _, scope := range bootstrapAdminScopes() {
		scopes[scope] = true
	}
	for _, want := range []string{
		"scheduler.read",
		"scheduler.write",
		"scheduler.run",
		"knowledge.read",
		"knowledge.write",
		"knowledge.manage",
		"memory.read",
		"memory.write",
		"memory.review",
		"memory.world.manage",
		"memory.distill",
		"memory.audit.read",
		"people.manage",
		"legal.manage",
		defaultLegalMCPPermissionScope,
		"development.manage",
		"value.manage",
	} {
		if !scopes[want] {
			t.Fatalf("bootstrap admin scopes missing %s: %+v", want, bootstrapAdminScopes())
		}
	}
}

func TestProductionSettingsPersistAcrossHandlerRestart(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", persistentProductionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}

	put := httptest.NewRequest(http.MethodPut, "/api/settings", jsonBody(`{"platform_name":"Orrery Production","organization_name":"Acme Production","language":"en-US","llm_profiles":[{"profile_id":"llm-openai-prod","display_name":"OpenAI Production","provider":"openai","base_url":"https://api.openai.com/v1","api_key_ref":"secret://llm/openai","model":"gpt-5","temperature":0.2,"context_length":128000,"enabled":true}]}`))
	put.Header.Set("X-User-ID", "superadmin")
	put.Header.Set("X-Viewer-Kind", "user")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, put)
	if rec.Code != http.StatusOK {
		cleanup()
		t.Fatalf("PUT /api/settings status = %d body = %s", rec.Code, rec.Body.String())
	}
	cleanup()

	handler, cleanup, err = newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("restart production handler: %v", err)
	}
	defer cleanup()

	get := httptest.NewRequest(http.MethodGet, "/api/settings", nil)
	get.Header.Set("X-User-ID", "superadmin")
	get.Header.Set("X-Viewer-Kind", "user")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, get)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/settings status = %d body = %s", rec.Code, rec.Body.String())
	}
	var settings httptransport.SettingsDTO
	if err := json.NewDecoder(rec.Body).Decode(&settings); err != nil {
		t.Fatalf("decode settings: %v", err)
	}
	if settings.PlatformName != "Orrery Production" || settings.OrganizationName != "Acme Production" || settings.Language != "en-US" {
		t.Fatalf("settings were not loaded from production storage after restart: %+v", settings)
	}
	if len(settings.LLMProfiles) != 1 {
		t.Fatalf("LLM profiles were not loaded from production storage after restart: %+v", settings.LLMProfiles)
	}
	profile := settings.LLMProfiles[0]
	if profile.ProfileID != "llm-openai-prod" || profile.Provider != "openai" || profile.Model != "gpt-5" || profile.ContextLength != 128000 {
		t.Fatalf("LLM profile was not persisted across production handler restart: %+v", profile)
	}
}

func TestProductionHandlerDeletesMissionFromProductionProjection(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	defer cleanup()

	create := httptest.NewRequest(http.MethodPost, "/api/missions", jsonBody(`{"title":"Delete me","final_goal":"Verify production delete"}`))
	create.Header.Set("X-User-ID", "superadmin")
	create.Header.Set("X-Viewer-Kind", "user")
	create.Header.Set("Idempotency-Key", "mission-delete-create")
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create mission status = %d body = %s", rec.Code, rec.Body.String())
	}
	var mission missioncontrol.MissionDTO
	if err := json.NewDecoder(rec.Body).Decode(&mission); err != nil {
		t.Fatalf("decode mission: %v", err)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/missions/"+mission.MissionID, nil)
	deleteReq.Header.Set("X-User-ID", "superadmin")
	deleteReq.Header.Set("X-Viewer-Kind", "user")
	deleteReq.Header.Set("Idempotency-Key", "mission-delete-1")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, deleteReq)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("delete mission status = %d body = %s", rec.Code, rec.Body.String())
	}

	get := httptest.NewRequest(http.MethodGet, "/api/missions/"+mission.MissionID+"/control", nil)
	get.Header.Set("X-User-ID", "superadmin")
	get.Header.Set("X-Viewer-Kind", "user")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, get)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleted mission control status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestProductionAPIDeleteMissionTaskRemovesTaskCardFromProjection(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()

	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	now := time.Date(2026, 6, 4, 10, 11, 12, 0, time.UTC)
	model := missionprojection.ReadModel{
		Mission: missionprojection.MissionView{
			MissionID:     "mission-delete-task",
			Title:         "Delete Task",
			FinalGoal:     "Verify task deletion",
			Status:        missionprojection.MissionStatusActive,
			CreatedByID:   "operator",
			CreatedByKind: "user",
			CreatedAt:     now,
		},
		TaskBoard: missionprojection.TaskBoardView{
			Tasks: []missionprojection.TaskCardView{
				{TaskID: "task-remove", Goal: "Remove this task", Status: missionprojection.TaskStatusCreated, UpdatedAt: now},
				{TaskID: "task-keep", Goal: "Keep this task", Status: missionprojection.TaskStatusCreated, UpdatedAt: now},
			},
			Dependencies: []missionprojection.TaskDependencyView{
				{TaskID: "task-keep", DependsOn: "task-remove"},
				{TaskID: "task-keep", DependsOn: "task-external"},
			},
		},
		Checkpoint: missionprojection.Checkpoint{
			MissionID: "mission-delete-task",
			Version:   2,
			UpdatedAt: now,
		},
		UpdatedAt: now,
	}
	if err := repo.Save(ctx, model); err != nil {
		t.Fatalf("save projection: %v", err)
	}
	taskRepo := missiontask.NewPlatformRepository()
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return taskRepo.SaveTask(ctx, tx, missiontask.TaskRecord{
			Contract: missiontask.TaskContract{ID: "task-remove", MissionID: "mission-delete-task"},
			Status:   missiontask.TaskStatusDraft, Version: 1, StatusChangedAt: now,
		})
	}); err != nil {
		t.Fatalf("seed task record: %v", err)
	}
	authz := newProductionMissionAuthorizationTestFixture()
	if err := authz.relations.WriteMissionCreated(ctx, authorization.SubjectRef{Kind: "user", ID: "operator"}, "mission-delete-task", "global", nil); err != nil {
		t.Fatalf("seed mission authorization: %v", err)
	}

	api := productionAPI{
		missionRepo:            repo,
		unitOfWork:             env.UnitOfWork,
		taskRepo:               taskRepo,
		authorizationService:   authz.service,
		authorizationRelations: authz.relations,
	}
	viewer := authorizedMissionTestViewer("operator", missioncontrol.ViewerKindUser)
	if err := api.DeleteMissionTask(ctx, viewer, "mission-delete-task", "task-remove"); err != nil {
		t.Fatalf("DeleteMissionTask: %v", err)
	}

	loaded, err := repo.GetMissionControl(ctx, "mission-delete-task")
	if err != nil {
		t.Fatalf("load projection: %v", err)
	}
	if len(loaded.TaskBoard.Tasks) != 1 || loaded.TaskBoard.Tasks[0].TaskID != "task-keep" {
		t.Fatalf("tasks after delete = %+v, want only task-keep", loaded.TaskBoard.Tasks)
	}
	if len(loaded.TaskBoard.Dependencies) != 1 ||
		loaded.TaskBoard.Dependencies[0].TaskID != "task-keep" ||
		loaded.TaskBoard.Dependencies[0].DependsOn != "task-external" {
		t.Fatalf("dependencies after delete = %+v, want only task-keep -> task-external", loaded.TaskBoard.Dependencies)
	}
	if !loaded.UpdatedAt.After(now) {
		t.Fatalf("projection UpdatedAt was not refreshed: got %s, want after %s", loaded.UpdatedAt, now)
	}
}

func TestProductionAPIDeleteMissionTaskProtectsTodoAttemptAndMissionBoundary(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	now := time.Date(2026, 6, 4, 10, 11, 12, 0, time.UTC)
	for _, model := range []missionprojection.ReadModel{
		{
			Mission:    missionprojection.MissionView{MissionID: "mission-todo-task", OriginKind: "todo", OriginTodoID: "todo-1", CreatedByID: "operator", CreatedByKind: "user", Status: missionprojection.MissionStatusActive, CreatedAt: now},
			TaskBoard:  missionprojection.TaskBoardView{Tasks: []missionprojection.TaskCardView{{TaskID: "task-todo", Status: missionprojection.TaskStatusCreated}}},
			Checkpoint: missionprojection.Checkpoint{MissionID: "mission-todo-task", Version: 1, UpdatedAt: now}, UpdatedAt: now,
		},
		{
			Mission:    missionprojection.MissionView{MissionID: "mission-other", CreatedByID: "operator", CreatedByKind: "user", Status: missionprojection.MissionStatusActive, CreatedAt: now},
			TaskBoard:  missionprojection.TaskBoardView{Tasks: []missionprojection.TaskCardView{{TaskID: "task-other", Status: missionprojection.TaskStatusCreated}}},
			Checkpoint: missionprojection.Checkpoint{MissionID: "mission-other", Version: 1, UpdatedAt: now}, UpdatedAt: now,
		},
	} {
		if err := repo.Save(ctx, model); err != nil {
			t.Fatal(err)
		}
	}
	taskRepo := missiontask.NewPlatformRepository()
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		for _, record := range []missiontask.TaskRecord{
			{Contract: missiontask.TaskContract{ID: "task-todo", MissionID: "mission-todo-task"}, Status: missiontask.TaskStatusDraft, Version: 1, StatusChangedAt: now},
			{Contract: missiontask.TaskContract{ID: "task-other", MissionID: "mission-other"}, Status: missiontask.TaskStatusDraft, Version: 1, StatusChangedAt: now},
		} {
			if err := taskRepo.SaveTask(ctx, tx, record); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	authz := newProductionMissionAuthorizationTestFixture()
	for _, missionID := range []string{"mission-todo-task", "mission-other"} {
		if err := authz.relations.WriteMissionCreated(ctx, authorization.SubjectRef{Kind: "user", ID: "operator"}, missionID, "global", nil); err != nil {
			t.Fatal(err)
		}
	}
	api := productionAPI{missionRepo: repo, unitOfWork: env.UnitOfWork, taskRepo: taskRepo, authorizationService: authz.service, authorizationRelations: authz.relations}
	viewer := authorizedMissionTestViewer("operator", missioncontrol.ViewerKindUser)

	if err := api.DeleteMissionTask(ctx, viewer, "mission-todo-task", "task-todo"); !errors.Is(err, missioncontrol.ErrConflict) {
		t.Fatalf("Todo task deletion error = %v, want ErrConflict", err)
	}
	if err := api.DeleteMissionTask(ctx, viewer, "mission-todo-task", "task-other"); !errors.Is(err, missioncontrol.ErrConflict) {
		t.Fatalf("Todo cross-Mission deletion error = %v, want ErrConflict", err)
	}
	if err := api.DeleteMissionTask(ctx, viewer, "mission-other", "task-todo"); !errors.Is(err, missioncontrol.ErrProjectionNotFound) {
		t.Fatalf("cross-Mission task deletion error = %v, want ErrProjectionNotFound", err)
	}
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		if _, err := taskRepo.GetTask(ctx, tx, "task-todo"); err != nil {
			return err
		}
		_, err := taskRepo.GetTask(ctx, tx, "task-other")
		return err
	}); err != nil {
		t.Fatalf("protected tasks were deleted: %v", err)
	}
}
