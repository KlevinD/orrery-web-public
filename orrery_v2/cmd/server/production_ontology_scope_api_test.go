package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/projectadmin"
	missioncore "orrery_v2/internal/mission/core"
	missionprojection "orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
)

func TestProductionOntologyScopeAPIListsBoundedAuthorizedDomainCatalog(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	projects := projectadmin.NewPlatformRepository(runtime.DBRouter)
	missions := missioncontrol.NewDBProjectionRepository(runtime.DBRouter)
	tools := toolres.NewPlatformRegistry(core)
	if err := organizations.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: "org-1", Name: "Organization One"}); err != nil {
		t.Fatalf("save organization: %v", err)
	}
	if err := projects.SaveProject(ctx, projectadmin.Project{ProjectID: "project-1", OrganizationID: "org-1", Title: "Project One", Version: 1, UpdatedAt: time.Unix(10, 0)}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	if err := missions.Save(ctx, missionprojection.ReadModel{Mission: missionprojection.MissionView{MissionID: missioncore.MissionID("mission-1"), Title: "Mission One"}, UpdatedAt: time.Unix(11, 0)}); err != nil {
		t.Fatalf("save mission: %v", err)
	}
	if err := tools.Register(ctx, toolres.ToolDefinition{ID: agentcore.ToolID("tool-1"), Kind: toolres.KindAPITool, Name: "Tool One", Version: "1", UpdatedAt: time.Unix(12, 0)}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	authorizer := authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}})
	scopeState, err := newProductionOntologyScopeState(core, organizations, projects, missions, tools, authorizer, authorization.NewSnapshotService(nil, nil), &fixedProductionRoleAuthorizer{allowed: true})
	if err != nil {
		t.Fatalf("new scope state: %v", err)
	}
	cursors, err := newProductionOntologyCursorState(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("new cursor state: %v", err)
	}
	service, err := newProductionOntologyScopeAPI(scopeState, cursors, organizations, projects, missions, tools)
	if err != nil {
		t.Fatalf("new scope API: %v", err)
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "oidc", Subject: "subject-1", UserID: "user-1", ViewerKind: identity.ViewerKindUser,
		CoarseScopes: []string{"operations.read", "operations.manage"},
	})
	page, err := service.List(ctx, principal, ontologyscope.CatalogQuery{Limit: 10})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"org-1", "project:global/project-1", "mission:global/mission-1", "resource:tool-1", "system"}
	if len(page.Items) != len(want) {
		t.Fatalf("items = %+v", page.Items)
	}
	for index, tenantID := range want {
		if page.Items[index].TenantID != tenantID {
			t.Fatalf("item %d tenant = %q, want %q", index, page.Items[index].TenantID, tenantID)
		}
	}
	resolved, err := service.Resolve(ctx, principal, "project:global/project-1")
	if err != nil || resolved.Label != "Project One" || resolved.ParentTenantID != "org-1" {
		t.Fatalf("Resolve = %+v, err=%v", resolved, err)
	}
}

func TestProductionOntologyScopeAPIFailsClosedWithoutDependencies(t *testing.T) {
	if _, err := newProductionOntologyScopeAPI(nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("missing dependencies returned nil error")
	}
}

func TestProductionOntologyScopeAPILocalModeIncludesCoarseReadScopes(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	projects := projectadmin.NewPlatformRepository(runtime.DBRouter)
	missions := missioncontrol.NewDBProjectionRepository(runtime.DBRouter)
	tools := toolres.NewPlatformRegistry(core)
	if err := organizations.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: "org-1", Name: "Organization One"}); err != nil {
		t.Fatalf("save organization: %v", err)
	}
	if err := projects.SaveProject(ctx, projectadmin.Project{ProjectID: "project-1", OrganizationID: "org-1", Title: "Project One"}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	if err := missions.Save(ctx, missionprojection.ReadModel{Mission: missionprojection.MissionView{MissionID: missioncore.MissionID("mission-1"), Title: "Mission One"}}); err != nil {
		t.Fatalf("save mission: %v", err)
	}
	if err := tools.Register(ctx, toolres.ToolDefinition{ID: agentcore.ToolID("tool-1"), Kind: toolres.KindAPITool, Name: "Tool One", Version: "1"}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	roleAuthorizer := &fixedProductionRoleAuthorizer{allowed: true}
	scopeState, err := newProductionOntologyScopeState(
		core, organizations, projects, missions, tools,
		authorization.NewService(authorization.NewScopeAdapter(nil)),
		authorization.NewSnapshotService(nil, nil), roleAuthorizer,
	)
	if err != nil {
		t.Fatalf("new scope state: %v", err)
	}
	cursors, err := newProductionOntologyCursorState(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("new cursor state: %v", err)
	}
	service, err := newProductionOntologyScopeAPI(scopeState, cursors, organizations, projects, missions, tools)
	if err != nil {
		t.Fatalf("new scope API: %v", err)
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "local", Subject: "operator", UserID: "operator", AgentProfileID: "human-operator", ViewerKind: identity.ViewerKindUser,
		CoarseScopes: []string{"operations.read", "operations.manage", "org.read", "org.manage", "mission.read", "mission.write", "resource.read", "resource.manage"},
	})
	page, err := service.List(ctx, principal, ontologyscope.CatalogQuery{Limit: 20})
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"org-1", "mission:global/mission-1", "resource:tool-1", "system"}
	if len(page.Items) != len(want) {
		t.Fatalf("local catalog items = %+v, want tenants %v", page.Items, want)
	}
	for index, tenantID := range want {
		if page.Items[index].TenantID != tenantID {
			t.Fatalf("local catalog items = %+v, want tenants %v", page.Items, want)
		}
	}
}
