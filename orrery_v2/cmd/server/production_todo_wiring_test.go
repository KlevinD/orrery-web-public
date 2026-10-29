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

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/identity"
	todoapp "orrery_v2/internal/app/todo"
	"orrery_v2/internal/app/userauth"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestProductionHandlerWiresTodoService(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatalf("newServerHandler production: %v", err)
	}
	t.Cleanup(cleanup)

	createReq := httptest.NewRequest(http.MethodPost, "/api/todos", strings.NewReader(`{"title":"Prepare release notes","description":"Draft public changelog"}`))
	createReq.Header.Set("X-User-ID", "superadmin")
	createReq.Header.Set("X-Viewer-Kind", "user")
	createReq.Header.Set("X-Display-Name", "Super Admin")
	createRec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, createRec, createReq)
	if createRec.Code != http.StatusCreated {
		t.Fatalf("create todo status = %d body = %s", createRec.Code, createRec.Body.String())
	}

	var created todoapp.TodoItem
	if err := json.NewDecoder(createRec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created todo: %v", err)
	}
	if created.TodoID == "" || created.Title != "Prepare release notes" || created.CreatedBy != agentcore.SystemSuperadminPrincipalID || created.CreatedByKind != todoapp.ViewerKindSystem {
		t.Fatalf("created todo = %+v", created)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/todos", nil)
	listReq.Header.Set("X-User-ID", "superadmin")
	listReq.Header.Set("X-Viewer-Kind", "user")
	listRec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list todo status = %d body = %s", listRec.Code, listRec.Body.String())
	}
	var page todoapp.TodoPage
	if err := json.NewDecoder(listRec.Body).Decode(&page); err != nil {
		t.Fatalf("decode todo page: %v", err)
	}
	if page.Total != 1 || len(page.Todos) != 1 || page.Todos[0].TodoID != created.TodoID {
		t.Fatalf("todo page = %+v", page)
	}

	checkReq := httptest.NewRequest(http.MethodPost, "/api/todos/"+created.TodoID+"/automation/check", nil)
	checkReq.Header.Set("X-User-ID", "superadmin")
	checkReq.Header.Set("X-Viewer-Kind", "user")
	checkReq.Header.Set("Idempotency-Key", "check-manual-todo")
	checkRec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, checkRec, checkReq)
	if checkRec.Code != http.StatusOK {
		t.Fatalf("check Todo automation status = %d body = %s", checkRec.Code, checkRec.Body.String())
	}
	var checkResult todoapp.TodoAutomationScanResult
	if err := json.NewDecoder(checkRec.Body).Decode(&checkResult); err != nil {
		t.Fatalf("decode Todo automation check: %v", err)
	}
	if checkResult.ScannedCount != 1 || checkResult.SkippedCount != 1 || checkResult.EligibleCount != 0 || checkResult.PlannedCount != 0 {
		t.Fatalf("manual Todo automation check = %+v", checkResult)
	}

	ideaReq := httptest.NewRequest(http.MethodPost, "/api/ideas", strings.NewReader(`{"title":"Capture homepage idea","content":"Save an inspiration note"}`))
	ideaReq.Header.Set("X-User-ID", "superadmin")
	ideaReq.Header.Set("X-Viewer-Kind", "user")
	ideaRec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, ideaRec, ideaReq)
	if ideaRec.Code != http.StatusCreated {
		t.Fatalf("create idea status = %d body = %s", ideaRec.Code, ideaRec.Body.String())
	}
	var createdIdea todoapp.IdeaItem
	if err := json.NewDecoder(ideaRec.Body).Decode(&createdIdea); err != nil {
		t.Fatalf("decode created idea: %v", err)
	}
	if createdIdea.IdeaID == "" || createdIdea.Title != "Capture homepage idea" || createdIdea.CreatedBy != agentcore.SystemSuperadminPrincipalID {
		t.Fatalf("created idea = %+v", createdIdea)
	}

	ideaListReq := httptest.NewRequest(http.MethodGet, "/api/ideas", nil)
	ideaListReq.Header.Set("X-User-ID", "superadmin")
	ideaListReq.Header.Set("X-Viewer-Kind", "user")
	ideaListRec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, ideaListRec, ideaListReq)
	if ideaListRec.Code != http.StatusOK {
		t.Fatalf("list idea status = %d body = %s", ideaListRec.Code, ideaListRec.Body.String())
	}
	var ideaPage todoapp.IdeaPage
	if err := json.NewDecoder(ideaListRec.Body).Decode(&ideaPage); err != nil {
		t.Fatalf("decode idea page: %v", err)
	}
	if ideaPage.Total != 1 || len(ideaPage.Ideas) != 1 || ideaPage.Ideas[0].IdeaID != createdIdea.IdeaID {
		t.Fatalf("idea page = %+v", ideaPage)
	}
}

func TestProductionTodoConversationIncludesInternalQASupportOnlyThroughManagedMembership(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	for _, profile := range []struct {
		id   agentcore.AgentProfileID
		name string
		role string
	}{
		{id: defaultLeadAgentID, name: "顾明澈", role: "Lead"},
		{id: defaultCoordinatorAgentID, name: "林知远", role: "Coordinator"},
		{id: defaultQAPlanAgentID, name: "质检计划", role: "QAPlan"},
		{id: defaultQACheckAgentID, name: "质检执行", role: "QACheck"},
	} {
		seedProductionChatProfile(t, ctx, profiles, profile.id, profile.name, profile.role)
	}
	service := discussionsession.NewService(discussionsession.ServiceOptions{
		Repository:     discussionsession.NewRepository(env.DBRouter),
		AgentDirectory: profiles,
		HumanProfiles:  productionHumanProfileBootstrap{profiles: profiles},
		Clock:          env.Clock,
		IDGenerator:    env.IDGenerator,
	})
	for _, participant := range []discussionsession.ViewerRef{
		{ViewerID: "human-assignee", ViewerKind: discussionsession.ViewerKindUser, DisplayName: "Assignee", TenantID: "tenant-a"},
		{ViewerID: "human-reviewer", ViewerKind: discussionsession.ViewerKindUser, DisplayName: "Reviewer", TenantID: "tenant-a"},
	} {
		if err := profiles.SaveProfile(ctx, defaultHumanAgentProfile(agentcore.AgentProfileID(participant.ViewerID), participant.DisplayName)); err != nil {
			t.Fatalf("seed Todo human participant %s: %v", participant.ViewerID, err)
		}
	}
	owner := discussionsession.ViewerRef{ViewerID: "human-owner", AgentProfileID: "human-owner", ViewerKind: discussionsession.ViewerKindUser, DisplayName: "Owner", TenantID: "tenant-a"}
	if err := profiles.SaveProfile(ctx, defaultHumanAgentProfile(agentcore.AgentProfileID(owner.ViewerID), owner.DisplayName)); err != nil {
		t.Fatalf("seed Todo owner: %v", err)
	}
	conversation, err := service.EnsureTodoConversation(ctx, owner, discussionsession.EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-managed", Title: "Managed Todo",
		InitialMemberIDs: []string{defaultLeadAgentID, defaultCoordinatorAgentID, defaultQAPlanAgentID, defaultQACheckAgentID, "human-assignee", "human-reviewer"},
		ManagedSource:    "todo:tenant-a/todo-managed",
	})
	if err != nil {
		t.Fatalf("EnsureTodoConversation with production QA support: %v", err)
	}
	members := map[string]bool{}
	for _, member := range conversation.Members {
		members[member.ID] = true
	}
	for _, want := range []string{defaultLeadAgentID, defaultCoordinatorAgentID, defaultQAPlanAgentID, defaultQACheckAgentID, owner.ViewerID, "human-assignee", "human-reviewer"} {
		if !members[want] {
			t.Fatalf("managed Todo conversation is missing %q: %+v", want, conversation.Members)
		}
	}
	for _, participantID := range []string{owner.ViewerID, "human-assignee", "human-reviewer"} {
		if _, err := service.GetConversation(ctx, discussionsession.ViewerRef{ViewerID: participantID, ViewerKind: discussionsession.ViewerKindUser, TenantID: "tenant-a"}, conversation.ConversationID); err != nil {
			t.Fatalf("Todo participant %s cannot read the canonical conversation: %v", participantID, err)
		}
	}

	manualConversation, err := service.EnsureTodoConversation(ctx, owner, discussionsession.EnsureTodoConversationCommand{
		TenantID: "tenant-a", TodoID: "todo-manual-invite", Title: "Manual invite guard",
		InitialMemberIDs: []string{defaultLeadAgentID}, ManagedSource: "todo:tenant-a/todo-manual-invite",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AddConversationMembers(ctx, owner, discussionsession.AddConversationMembersCommand{
		ConversationID: manualConversation.ConversationID, TargetIDs: []string{defaultQAPlanAgentID}, IdempotencyKey: "manual-qa-invite",
	}); !errors.Is(err, discussionsession.ErrWorkerInternalQAMember) {
		t.Fatalf("manual QA invite error = %v, want ErrWorkerInternalQAMember", err)
	}
}

func TestProductionTodoDecisionPrincipalResolverReloadsCurrentAccountAndPermissions(t *testing.T) {
	ctx := context.Background()
	accounts := userauth.NewMemoryAccountStore()
	account := userauth.Account{
		UserID: "account-owner", Username: "owner-login", DisplayName: "Current Owner", AgentProfileID: "subject-owner",
		Enabled: true, PermissionScope: []string{"account.current"},
	}
	if err := accounts.SaveAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	scopes := &capturingTodoPermissionScopeResolver{resolved: []string{"todo.write", "development.execute"}}
	resolver := productionTodoDecisionPrincipalResolver{accounts: accounts, scopes: scopes}
	persisted := todoapp.ViewerRef{
		TenantID: "tenant-a", ViewerID: "owner-login", ViewerKind: todoapp.ViewerKindUser,
		AuthorizationSubjectID: "subject-owner", PermissionScope: []string{"historical.scope"},
	}
	resolved, err := resolver.ResolveTodoDecisionPrincipal(ctx, persisted)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.DisplayName != "Current Owner" || !containsString(resolved.PermissionScope, "todo.write") || containsString(resolved.PermissionScope, "historical.scope") {
		t.Fatalf("resolved principal = %+v", resolved)
	}
	if scopes.principal.AgentProfileID != "subject-owner" || scopes.principal.Username != "owner-login" || len(scopes.accountScopes) != 1 || scopes.accountScopes[0] != "account.current" {
		t.Fatalf("scope resolution principal=%+v accountScopes=%+v", scopes.principal, scopes.accountScopes)
	}

	account.Enabled = false
	if err := accounts.SaveAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveTodoDecisionPrincipal(ctx, persisted); !errors.Is(err, todoapp.ErrForbidden) {
		t.Fatalf("disabled account error = %v, want ErrForbidden", err)
	}
	persisted.AuthorizationSubjectID = "different-subject"
	account.Enabled = true
	if err := accounts.SaveAccount(ctx, account); err != nil {
		t.Fatal(err)
	}
	if _, err := resolver.ResolveTodoDecisionPrincipal(ctx, persisted); !errors.Is(err, todoapp.ErrForbidden) {
		t.Fatalf("changed identity error = %v, want ErrForbidden", err)
	}
}

type capturingTodoPermissionScopeResolver struct {
	principal     identity.Principal
	accountScopes []string
	resolved      []string
}

func (r *capturingTodoPermissionScopeResolver) ResolvePermissionScopes(_ context.Context, principal identity.Principal, accountScopes []string) ([]string, error) {
	r.principal = principal
	r.accountScopes = append([]string(nil), accountScopes...)
	return append([]string(nil), r.resolved...), nil
}

func TestProductionMCPGatewayRoutesTodoTools(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	for _, definition := range toolstd.Definitions(time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)) {
		if err := registry.Register(ctx, definition); err != nil {
			t.Fatalf("register %s: %v", definition.ID, err)
		}
	}
	executor := &serverTestTodoExecutor{}
	gateway := newProductionMCPGateway(productionMCPGatewayOptions{
		Registry:     registry,
		TodoExecutor: executor,
		Profiles: serverTodoTestProfileStore{
			profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
				"agent-ops": {
					ID:       "agent-ops",
					Enabled:  true,
					ToolRefs: []agentcore.ToolRef{{ToolID: toolstd.SystemMCPServer}},
				},
			},
			page: agentcore.AgentDirectoryPage{Entries: []agentcore.AgentDirectoryEntry{{
				AgentProfileID: "agent-ops",
				Enabled:        true,
			}}},
		},
		Now: func() time.Time { return time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC) },
	})

	result, err := gateway.CallTool(ctx, mcptransport.CallToolRequest{
		ToolID:      string(toolstd.TodoCreate),
		PrincipalID: "agent-ops",
		AgentID:     "agent-ops",
		Permissions: []string{toolstd.PermissionTodoWrite},
		Arguments: map[string]any{
			"title":       "Coordinate QA",
			"description": "Ask QA agent to verify homepage todo flows",
		},
	})
	if err != nil {
		t.Fatalf("CallTool(todo_create): %v", err)
	}
	if result.Output["todo_id"] != "todo-prod" || executor.request.ToolID != string(toolstd.TodoCreate) {
		t.Fatalf("todo result=%+v executor=%+v", result.Output, executor.request)
	}
	if executor.request.Viewer.ViewerID != "agent-ops" || executor.request.Viewer.ViewerKind != todoapp.ViewerKindAgent {
		t.Fatalf("todo viewer = %+v", executor.request.Viewer)
	}
}

func TestProductionBootstrapScopesIncludeTodoPermissions(t *testing.T) {
	assertScope := func(t *testing.T, scopes []string, want string) {
		t.Helper()
		for _, scope := range scopes {
			if scope == want {
				return
			}
		}
		t.Fatalf("scope %q missing from %+v", want, scopes)
	}
	assertScope(t, bootstrapAdminScopes(), toolstd.PermissionTodoRead)
	assertScope(t, bootstrapAdminScopes(), toolstd.PermissionTodoWrite)
	assertScope(t, bootstrapAdminScopes(), toolstd.PermissionIdeaRead)
	assertScope(t, bootstrapAdminScopes(), toolstd.PermissionIdeaWrite)
	assertScope(t, userauth.SuperadminScopes(), toolstd.PermissionTodoRead)
	assertScope(t, userauth.SuperadminScopes(), toolstd.PermissionTodoWrite)
	assertScope(t, userauth.SuperadminScopes(), toolstd.PermissionIdeaRead)
	assertScope(t, userauth.SuperadminScopes(), toolstd.PermissionIdeaWrite)
	assertScope(t, userauth.SuperadminScopes(), "knowledge.read")
	assertScope(t, userauth.SuperadminScopes(), "knowledge.write")
	assertScope(t, userauth.SuperadminScopes(), "knowledge.manage")
	assertScope(t, userauth.SuperadminScopes(), "memory.read")
	assertScope(t, userauth.SuperadminScopes(), "memory.write")
	assertScope(t, userauth.SuperadminScopes(), "memory.review")
	assertScope(t, userauth.SuperadminScopes(), "memory.world.manage")
	assertScope(t, userauth.SuperadminScopes(), "memory.distill")
	assertScope(t, userauth.SuperadminScopes(), "memory.audit.read")
	assertScope(t, userauth.SuperadminScopes(), "roles.read")
	assertScope(t, userauth.SuperadminScopes(), "roles.manage")
	assertScope(t, userauth.SuperadminScopes(), "permissions.read")
	assertScope(t, userauth.SuperadminScopes(), "permissions.manage")
	assertScope(t, userauth.SuperadminScopes(), "tool.call")
	assertScope(t, userauth.SuperadminScopes(), "agent.run")
	assertScope(t, userauth.SuperadminScopes(), toolstd.PermissionEvaluationReview)
	assertScope(t, userauth.SuperadminScopes(), toolstd.PermissionMemoryDiaryWrite)
	assertScope(t, userauth.SuperadminScopes(), toolstd.PermissionSkillRead)
}

func TestProductionBootstrapScopesIncludeEverySuperadminScope(t *testing.T) {
	bootstrapScopes := map[string]struct{}{}
	for _, scope := range bootstrapAdminScopes() {
		bootstrapScopes[scope] = struct{}{}
	}
	for _, scope := range userauth.SuperadminScopes() {
		if _, ok := bootstrapScopes[scope]; !ok {
			t.Fatalf("bootstrap admin scope %q missing from %+v", scope, bootstrapAdminScopes())
		}
	}
}

func TestEnsureDefaultSystemUserProfilesRemovesLegacySuperadminProfile(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	legacySuperadmin := defaultHumanAgentProfile(agentcore.AgentProfileID(userauth.LegacySuperadminAgentProfileID), userauth.SuperadminUsername)
	legacySuperadmin.UserAccount = agentcore.UserAccountSpec{
		Username:        userauth.SuperadminUsername,
		AccountEnabled:  true,
		PermissionScope: []string{"mission.read", "custom.scope"},
	}
	if err := saveRawAgentProfile(ctx, env.UnitOfWork, legacySuperadmin); err != nil {
		t.Fatalf("save legacy superadmin: %v", err)
	}

	if err := ensureDefaultSystemUserProfiles(ctx, profiles); err != nil {
		t.Fatalf("ensure system users: %v", err)
	}

	if _, err := profiles.GetProfile(ctx, agentcore.AgentProfileID(userauth.LegacySuperadminAgentProfileID)); !errors.Is(err, agentcore.ErrNotFound) {
		t.Fatalf("legacy superadmin profile lookup error = %v, want ErrNotFound", err)
	}
	deleted, err := profiles.IsProfileDeleted(ctx, agentcore.AgentProfileID(userauth.LegacySuperadminAgentProfileID))
	if err != nil || !deleted {
		t.Fatalf("legacy superadmin tombstone: deleted=%t err=%v", deleted, err)
	}
}

type serverTestTodoExecutor struct {
	request todoapp.ToolExecutionRequest
}

func (e *serverTestTodoExecutor) Execute(ctx context.Context, req todoapp.ToolExecutionRequest) (todoapp.ToolExecutionResult, error) {
	e.request = req
	return todoapp.ToolExecutionResult{Output: map[string]any{
		"todo_id": "todo-prod",
		"title":   req.Arguments["title"],
		"status":  string(todoapp.TodoStatusInbox),
	}}, nil
}

type serverTodoTestProfileStore struct {
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
	page     agentcore.AgentDirectoryPage
}

func (s serverTodoTestProfileStore) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := s.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, mcptransport.ErrToolNotFound
	}
	return profile, nil
}

func (s serverTodoTestProfileStore) QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return s.page, nil
}
