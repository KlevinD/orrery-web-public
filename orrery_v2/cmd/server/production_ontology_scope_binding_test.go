package main

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/identity"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/projectadmin"
	"orrery_v2/internal/app/rebac"
	missioncore "orrery_v2/internal/mission/core"
	missionprojection "orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
)

func TestProductionOntologyScopeBindingResolvesOnlyAuthoritativeDomainRecords(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	if err := organizations.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: "org-1", Name: "Organization 1"}); err != nil {
		t.Fatalf("save organization: %v", err)
	}
	projects := projectadmin.NewMemoryRepository()
	if err := projects.SaveProject(ctx, projectadmin.Project{ProjectID: "project-1", OrganizationID: "org-1", Title: "Project 1", Version: 3}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	missions := missioncontrol.NewMemoryProjectionRepository()
	if err := missions.Save(ctx, missionprojection.ReadModel{Mission: missionprojection.MissionView{MissionID: missioncore.MissionID("mission-1"), Title: "Mission 1"}}); err != nil {
		t.Fatalf("save mission: %v", err)
	}
	tools := toolres.NewMemoryRegistry()
	if err := tools.Register(ctx, toolres.ToolDefinition{ID: agentcore.ToolID("tool-1"), Kind: toolres.KindAPITool, Name: "Tool 1", Version: "1"}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	state, err := newProductionOntologyScopeState(
		core,
		organizations,
		projects,
		missions,
		tools,
		authorization.NewService(authorization.NewScopeAdapter(nil)),
		authorization.NewSnapshotService(nil, nil),
		&fixedProductionRoleAuthorizer{allowed: true},
	)
	if err != nil {
		t.Fatalf("new scope state: %v", err)
	}

	tests := []struct {
		tenantID            string
		kind                ontologyscope.Kind
		objectType          string
		objectID            string
		authorizationTenant string
		version             int64
	}{
		{tenantID: "org-1", kind: ontologyscope.KindOrganization, objectType: "organization", objectID: "org-1", authorizationTenant: "org-1", version: 1},
		{tenantID: "project:global/project-1", kind: ontologyscope.KindProject, objectType: "project", objectID: "project-1", authorizationTenant: "global", version: 1},
		{tenantID: "mission:global/mission-1", kind: ontologyscope.KindMission, objectType: "mission", objectID: "mission-1", authorizationTenant: "global", version: 1},
		{tenantID: "tool:tool-1", kind: ontologyscope.KindResource, objectType: "resource", objectID: "tool-1", authorizationTenant: "global", version: 1},
		{tenantID: "resource:tool-1", kind: ontologyscope.KindResource, objectType: "resource", objectID: "tool-1", authorizationTenant: "global", version: 1},
	}
	for _, test := range tests {
		binding, err := state.Resolver.Resolve(ctx, test.tenantID)
		if err != nil {
			t.Fatalf("Resolve(%s): %v", test.tenantID, err)
		}
		if binding.Kind != test.kind || binding.SourceObject.Type != test.objectType || binding.SourceObject.ID != test.objectID || binding.AuthorizationTenantID != test.authorizationTenant || binding.BindingVersion != test.version {
			t.Fatalf("Resolve(%s) = %+v", test.tenantID, binding)
		}
	}
	for _, tenantID := range []string{"project:missing", "mission:missing", "tool:missing", "guessed-custom"} {
		if _, err := state.Resolver.Resolve(ctx, tenantID); !errors.Is(err, ontologyscope.ErrBindingNotFound) {
			t.Fatalf("Resolve(%s) error = %v", tenantID, err)
		}
	}
}

func TestProductionOntologyScopeBindingRejectsAmbiguityAndRequiresRelationForObjectScopes(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	if err := organizations.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: "project:global/project-1", Name: "Collision"}); err != nil {
		t.Fatalf("save organization: %v", err)
	}
	projects := projectadmin.NewMemoryRepository()
	if err := projects.SaveProject(ctx, projectadmin.Project{ProjectID: "project-1", OrganizationID: "org-1", Title: "Project 1", Version: 1}); err != nil {
		t.Fatalf("save project: %v", err)
	}
	roles := &fixedProductionRoleAuthorizer{allowed: true}
	state, err := newProductionOntologyScopeState(
		core,
		organizations,
		projects,
		missioncontrol.NewMemoryProjectionRepository(),
		toolres.NewMemoryRegistry(),
		authorization.NewService(authorization.NewScopeAdapter(nil)),
		authorization.NewSnapshotService(nil, nil),
		roles,
	)
	if err != nil {
		t.Fatalf("new scope state: %v", err)
	}
	if _, err := state.Resolver.Resolve(ctx, "project:global/project-1"); !errors.Is(err, ontologyscope.ErrBindingConflict) {
		t.Fatalf("ambiguous scope error = %v", err)
	}

	if err := organizations.DeleteOrganization(ctx, "project:global/project-1"); err != nil {
		t.Fatalf("delete colliding organization: %v", err)
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "oidc",
		Subject:         "subject-1",
		UserID:          "user-1",
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    []string{"operations.read"},
	})
	if _, err := state.Gate.AuthorizeRead(ctx, principal, "project:global/project-1"); !errors.Is(err, ontologyscope.ErrAccessDenied) {
		t.Fatalf("missing project relation error = %v", err)
	}
	principal.CoarseScopes = append(principal.CoarseScopes, "mission.read")
	if _, err := state.Gate.AuthorizeRead(ctx, principal, "project:global/project-1"); !errors.Is(err, ontologyscope.ErrAccessDenied) {
		t.Fatalf("coarse local project permission error = %v, want access denied", err)
	}

	state, err = newProductionOntologyScopeState(
		core,
		organizations,
		projects,
		missioncontrol.NewMemoryProjectionRepository(),
		toolres.NewMemoryRegistry(),
		authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}}),
		authorization.NewSnapshotService(nil, nil),
		&fixedProductionRoleAuthorizer{allowed: true},
	)
	if err != nil {
		t.Fatalf("new relation-authorized scope state: %v", err)
	}
	if _, err := state.Gate.AuthorizeRead(ctx, principal, "project:global/project-1"); err != nil {
		t.Fatalf("relation-authorized project scope: %v", err)
	}
}

func TestProductionOntologyLocalAuthorizationAllowsCoarseReadScopesWithoutWideningOthers(t *testing.T) {
	authorizer := productionScopeAccessAuthorizer{
		service: authorization.NewService(authorization.NewScopeAdapter(nil)),
		roles:   &fixedProductionRoleAuthorizer{allowed: true},
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "local", Subject: "operator", AgentProfileID: "human-operator", ViewerKind: identity.ViewerKindUser,
		CoarseScopes: []string{"mission.read", "mission.write", "resource.read", "resource.manage", operations.PermissionOrgRead, operations.PermissionOperationsRead},
	})

	for _, test := range []struct {
		action string
		object identity.ObjectRef
	}{
		{action: "mission.view", object: identity.ObjectRef{Type: "mission", ID: "mission-1"}},
		{action: "resource.view", object: identity.ObjectRef{Type: "resource", ID: "tool-1"}},
	} {
		if err := authorizer.AuthorizeObject(context.Background(), principal, test.action, test.object, "global"); err != nil {
			t.Fatalf("AuthorizeObject(%s) read: %v", test.object.Type, err)
		}
	}
	if err := authorizer.AuthorizeObject(context.Background(), principal, "project.view", identity.ObjectRef{Type: "project", ID: "project-1"}, "global"); !errors.Is(err, authorization.ErrDenied) {
		t.Fatalf("project read without project or graph scope error = %v, want ErrDenied", err)
	}

	if err := authorizer.AuthorizeObject(context.Background(), principal, "organization.read", identity.ObjectRef{Type: "organization", ID: "org-1"}, "org-1"); err != nil {
		t.Fatalf("exact organization role authorization: %v", err)
	}

	graphPrincipal := principal
	graphPrincipal.CoarseScopes = []string{"enterprise_graph.read"}
	if err := authorizer.AuthorizeObject(context.Background(), graphPrincipal, "organization.read", identity.ObjectRef{Type: "organization", ID: "org-1"}, "org-1"); err != nil {
		t.Fatalf("enterprise graph read should enter the source visibility boundary: %v", err)
	}
}

func TestProductionOntologyGraphReadRequiresSourceObjectRelation(t *testing.T) {
	ctx := context.Background()
	store := rebac.NewMemoryStore()
	authorizer := productionScopeAccessAuthorizer{
		service: authorization.NewService(authorization.NewReBACAdapter(store, nil)),
		roles:   &fixedProductionRoleAuthorizer{allowed: true},
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "orrery",
		Subject:         "agent-1",
		AgentProfileID:  "agent-1",
		ViewerKind:      identity.ViewerKindAgent,
		CoarseScopes:    []string{"enterprise_graph.read"},
	})
	mission := identity.ObjectRef{Type: "mission", ID: "mission-1", TenantID: "global"}
	requests := []ontologyscope.ObjectAuthorizationRequest{{
		Action:                "mission.view",
		Object:                mission,
		AuthorizationTenantID: "global",
	}}

	if err := authorizer.AuthorizeObject(ctx, principal, "mission.view", mission, "global"); !errors.Is(err, authorization.ErrDenied) {
		t.Fatalf("graph read without source relation error = %v, want ErrDenied", err)
	}
	allowed, err := authorizer.AuthorizeObjects(ctx, principal, requests)
	if err != nil || len(allowed) != 1 || allowed[0] {
		t.Fatalf("batch graph read without source relation = %v, err=%v", allowed, err)
	}
	principalWithSourceScope := principal
	principalWithSourceScope.CoarseScopes = append(principalWithSourceScope.CoarseScopes, "mission.read")
	if err := authorizer.AuthorizeObject(ctx, principalWithSourceScope, "mission.view", mission, "global"); !errors.Is(err, authorization.ErrDenied) {
		t.Fatalf("graph and source scope without relation error = %v, want ErrDenied", err)
	}
	if err := store.SaveRelationTuple(ctx, rebac.RelationTuple{
		ID:       "ontology-agent-mission-viewer",
		TenantID: "global",
		Subject:  rebac.Ref{Kind: "agent", ID: "agent-1"},
		Relation: "viewer",
		Object:   rebac.Ref{Kind: "mission", ID: "mission-1"},
	}); err != nil {
		t.Fatalf("SaveRelationTuple: %v", err)
	}
	if err := authorizer.AuthorizeObject(ctx, principal, "mission.view", mission, "global"); err != nil {
		t.Fatalf("graph read with source relation: %v", err)
	}
	allowed, err = authorizer.AuthorizeObjects(ctx, principal, requests)
	if err != nil || len(allowed) != 1 || !allowed[0] {
		t.Fatalf("batch graph read with source relation = %v, err=%v", allowed, err)
	}
}

func TestProductionOntologyDoesNotTurnScopeDenialIntoGraphReadAccess(t *testing.T) {
	authorizer := productionScopeAccessAuthorizer{
		service: authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{
			Allowed: false,
			Source:  authorization.DecisionSourceScope,
			Reason:  "missing source scope",
		}}),
		roles: &fixedProductionRoleAuthorizer{allowed: true},
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "orrery",
		Subject:         "agent-1",
		ViewerKind:      identity.ViewerKindAgent,
		CoarseScopes:    []string{"enterprise_graph.read"},
	})
	err := authorizer.AuthorizeObject(
		context.Background(),
		principal,
		"mission.view",
		identity.ObjectRef{Type: "mission", ID: "mission-1", TenantID: "global"},
		"global",
	)
	if !errors.Is(err, authorization.ErrDenied) {
		t.Fatalf("scope denial error = %v, want ErrDenied", err)
	}
}

func TestProductionOntologyScopeBindingDoesNotTrustSystemKindAsAuthorization(t *testing.T) {
	authorizer := productionScopeAccessAuthorizer{
		service: authorization.NewService(authorization.NewScopeAdapter(nil)),
		roles:   &fixedProductionRoleAuthorizer{allowed: true},
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "internal", Subject: "scheduler", ViewerKind: identity.ViewerKindSystem,
		CoarseScopes: []string{operations.PermissionOperationsRead},
	})
	if err := authorizer.AuthorizeFeature(context.Background(), principal, operations.PermissionOperationsRead); !errors.Is(err, authorization.ErrDenied) {
		t.Fatalf("system feature authorization error = %v, want ErrDenied", err)
	}
	authorizer.service = authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}})
	if err := authorizer.AuthorizeFeature(context.Background(), principal, operations.PermissionOperationsRead); err != nil {
		t.Fatalf("relation-authorized system feature: %v", err)
	}
}

type fixedProductionAuthorizationAdapter struct {
	decision authorization.Decision
	err      error
}

func TestProductionOntologyFeatureAuthorizationRequiresPlatformRole(t *testing.T) {
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "local",
		Subject:         "operator",
		AgentProfileID:  "human-operator",
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    []string{operations.PermissionOperationsRead},
	})
	roles := &fixedProductionRoleAuthorizer{}
	authorizer := productionScopeAccessAuthorizer{
		service: authorization.NewService(authorization.NewScopeAdapter(nil)),
		roles:   roles,
	}
	dedicatedRead := principal
	dedicatedRead.CoarseScopes = []string{"enterprise_graph.read"}
	if err := authorizer.AuthorizeFeature(context.Background(), dedicatedRead, ontologyscope.FeatureReadAction); err != nil {
		t.Fatalf("dedicated ontology read should not require a platform role: %v", err)
	}

	if err := authorizer.AuthorizeFeature(context.Background(), principal, operations.PermissionOperationsRead); !errors.Is(err, authorization.ErrDenied) {
		t.Fatalf("AuthorizeFeature error = %v, want ErrDenied", err)
	}
	roles.allowed = true
	if err := authorizer.AuthorizeFeature(context.Background(), principal, operations.PermissionOperationsRead); err != nil {
		t.Fatalf("AuthorizeFeature with platform role: %v", err)
	}
}

func TestProductionOntologyFeatureAuthorizationFallsBackToTrustedInternalScope(t *testing.T) {
	authorizer := productionScopeAccessAuthorizer{
		service: authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{
			Allowed: false,
			Source:  authorization.DecisionSourceOpenFGA,
			Reason:  "missing platform relation",
		}}),
		roles: operations.NewRolePermissionAuthorizer(nil, nil, nil),
	}
	ctx := identity.ContextWithTrustedInternalService(context.Background())

	allowed := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "transport",
		Subject:         "development-service",
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    []string{operations.PermissionOperationsRead},
	})
	if err := authorizer.AuthorizeFeature(context.Background(), allowed, operations.PermissionOperationsRead); !errors.Is(err, authorization.ErrDenied) {
		t.Fatalf("untrusted feature authorization error = %v, want ErrDenied", err)
	}
	if err := authorizer.AuthorizeFeature(ctx, allowed, operations.PermissionOperationsRead); err != nil {
		t.Fatalf("trusted internal feature authorization: %v", err)
	}

	missingScope := allowed
	missingScope.CoarseScopes = []string{operations.PermissionOrgRead}
	if err := authorizer.AuthorizeFeature(ctx, missingScope, operations.PermissionOperationsRead); !errors.Is(err, authorization.ErrDenied) {
		t.Fatalf("missing-scope error = %v, want ErrDenied", err)
	}
}

func TestProductionOntologyObjectAuthorizationFallsBackToTrustedInternalOrganizationScope(t *testing.T) {
	authorizer := productionScopeAccessAuthorizer{
		service: authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{
			Allowed: false,
			Source:  authorization.DecisionSourceOpenFGA,
			Reason:  "missing organization relation",
		}}),
		roles: operations.NewRolePermissionAuthorizer(nil, nil, nil),
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "transport",
		Subject:         "development-service",
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    []string{operations.PermissionOrgRead},
	})
	organization := identity.ObjectRef{Type: "organization", ID: "org-1", TenantID: "org-1"}

	if err := authorizer.AuthorizeObject(context.Background(), principal, "organization.read", organization, "org-1"); !errors.Is(err, authorization.ErrDenied) {
		t.Fatalf("untrusted object authorization error = %v, want ErrDenied", err)
	}
	trusted := identity.ContextWithTrustedInternalService(context.Background())
	if err := authorizer.AuthorizeObject(trusted, principal, "organization.read", organization, "org-1"); err != nil {
		t.Fatalf("trusted internal organization authorization: %v", err)
	}

	missingScope := principal
	missingScope.CoarseScopes = []string{operations.PermissionOperationsRead}
	if err := authorizer.AuthorizeObject(trusted, missingScope, "organization.read", organization, "org-1"); !errors.Is(err, authorization.ErrDenied) {
		t.Fatalf("missing organization scope error = %v, want ErrDenied", err)
	}
	project := identity.ObjectRef{Type: "project", ID: "project-1", TenantID: "org-1"}
	if err := authorizer.AuthorizeObject(trusted, principal, "project.view", project, "org-1"); !errors.Is(err, authorization.ErrDenied) {
		t.Fatalf("unsupported object fallback error = %v, want ErrDenied", err)
	}
}

func TestProductionOntologyFeatureAuthorizationFailsClosedWithoutRoleAuthorizer(t *testing.T) {
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "local",
		Subject:         "operator",
		AgentProfileID:  "human-operator",
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    []string{operations.PermissionOperationsRead},
	})
	authorizer := productionScopeAccessAuthorizer{
		service: authorization.NewService(authorization.NewScopeAdapter(nil)),
	}

	if err := authorizer.AuthorizeFeature(context.Background(), principal, operations.PermissionOperationsRead); !errors.Is(err, authorization.ErrServiceDependency) {
		t.Fatalf("AuthorizeFeature error = %v, want ErrServiceDependency", err)
	}
}

type fixedProductionRoleAuthorizer struct {
	allowed bool
	err     error
}

func (a *fixedProductionRoleAuthorizer) AuthorizeRolePermission(context.Context, identity.Principal, string, []identity.ObjectRef) (bool, error) {
	return a.allowed, a.err
}

func (a fixedProductionAuthorizationAdapter) Check(context.Context, authorization.CheckRequest) (authorization.Decision, error) {
	return a.decision, a.err
}

func (a fixedProductionAuthorizationAdapter) BatchCheck(_ context.Context, requests []authorization.CheckRequest) ([]authorization.Decision, error) {
	decisions := make([]authorization.Decision, len(requests))
	for index := range decisions {
		decisions[index] = a.decision
	}
	return decisions, a.err
}

func (fixedProductionAuthorizationAdapter) ListObjects(context.Context, identity.Principal, string, string, string) ([]identity.ObjectRef, error) {
	return nil, authorization.ErrUnsupported
}

func (fixedProductionAuthorizationAdapter) WriteRelations(context.Context, []authorization.RelationTuple) error {
	return authorization.ErrUnsupported
}

func (fixedProductionAuthorizationAdapter) DeleteRelations(context.Context, []authorization.RelationTuple) error {
	return authorization.ErrUnsupported
}

func TestProductionOntologyScopeBindingFailsClosedWithoutDependencies(t *testing.T) {
	if _, err := newProductionOntologyScopeState(nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("missing dependencies returned nil error")
	}
}

func TestRunRejectsConflictingOntologyScopeBindingBackfillModes(t *testing.T) {
	err := run(context.Background(), []string{"-ontology-scope-binding-backfill", "-ontology-scope-binding-backfill-apply"}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Fatalf("run error = %v", err)
	}
}
