package authorization

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/rebac"
)

type countingTenantRelationStore struct {
	*rebac.MemoryStore
	tenantListCalls      int
	tenantBatchListCalls int
	fullListCalls        int
}

func (s *countingTenantRelationStore) ListRelationTuplesByTenant(ctx context.Context, tenantID string) ([]rebac.RelationTuple, error) {
	s.tenantListCalls++
	return s.MemoryStore.ListRelationTuplesByTenant(ctx, tenantID)
}

func (s *countingTenantRelationStore) ListRelationTuples(ctx context.Context) ([]rebac.RelationTuple, error) {
	s.fullListCalls++
	return s.MemoryStore.ListRelationTuples(ctx)
}

func (s *countingTenantRelationStore) ListRelationTuplesByTenants(ctx context.Context, tenantIDs []string) ([]rebac.RelationTuple, error) {
	s.tenantBatchListCalls++
	return s.MemoryStore.ListRelationTuplesByTenants(ctx, tenantIDs)
}

func TestScopeAdapterAllowsMappedActionFromPrincipalScope(t *testing.T) {
	service := NewService(NewScopeAdapter(nil))
	principal := identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         "superadmin",
		UserID:          "superadmin",
		Username:        "superadmin",
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    []string{"orrery:mission.write", "project.write", "tool.call"},
	}

	if err := service.Can(context.Background(), principal, "mission.edit", identity.ObjectRef{Type: "mission", ID: "mission-1"}); err != nil {
		t.Fatalf("Can mission.edit: %v", err)
	}
	if err := service.Can(context.Background(), principal, "project.edit", identity.ObjectRef{Type: "project", ID: "project-1"}); err != nil {
		t.Fatalf("Can project.edit: %v", err)
	}
	if err := service.Can(context.Background(), principal, "resource.use", identity.ObjectRef{Type: "resource", ID: "mcp.github"}); err != nil {
		t.Fatalf("Can resource.use: %v", err)
	}
	if err := service.Can(context.Background(), principal, "resource.manage", identity.ObjectRef{Type: "resource", ID: "mcp.github"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("resource.manage error = %v, want ErrDenied", err)
	}
}

func TestScopeAdapterDoesNotExpandOperationsScopesIntoSpecificDomains(t *testing.T) {
	service := NewService(NewScopeAdapter(nil))
	principal := identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         "superadmin",
		Username:        "superadmin",
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    []string{"operations.manage", "resource.manage", "approvals.decide"},
	}

	for _, action := range []string{"permissions.read", "roles.manage", "audit.read"} {
		if err := service.Can(context.Background(), principal, action, identity.ObjectRef{Type: "route", ID: action}); !errors.Is(err, ErrDenied) {
			t.Fatalf("Can %s error = %v, want ErrDenied", action, err)
		}
	}
	for _, action := range []string{"operations.read", "resource.read", "approvals.decide"} {
		if err := service.Can(context.Background(), principal, action, identity.ObjectRef{Type: "route", ID: action}); err != nil {
			t.Fatalf("Can %s: %v", action, err)
		}
	}
}

func TestReBACAdapterRequiresScopeAndRelation(t *testing.T) {
	ctx := context.Background()
	store := rebac.NewMemoryStore()
	service := NewService(NewReBACAdapter(store, nil))
	principal := identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         "operator",
		UserID:          "operator",
		Username:        "operator",
		ViewerKind:      identity.ViewerKindUser,
		TenantID:        "tenant-1",
		CoarseScopes:    []string{"mission.write"},
	}

	if err := service.WriteRelations(ctx, []RelationTuple{{
		TenantID: "tenant-1",
		Subject:  SubjectRef{Kind: "user", ID: "operator"},
		Relation: "editor",
		Object:   identity.ObjectRef{Type: "mission", ID: "mission-1"},
	}}); err != nil {
		t.Fatalf("WriteRelations: %v", err)
	}

	if err := service.Can(ctx, principal, "mission.edit", identity.ObjectRef{Type: "mission", ID: "mission-1", TenantID: "tenant-1"}); err != nil {
		t.Fatalf("Can mission.edit: %v", err)
	}
	if err := service.Can(ctx, principal, "mission.manage", identity.ObjectRef{Type: "mission", ID: "mission-1", TenantID: "tenant-1"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("mission.manage error = %v, want ErrDenied", err)
	}

	principal.CoarseScopes = nil
	if err := service.Can(ctx, principal, "mission.edit", identity.ObjectRef{Type: "mission", ID: "mission-1", TenantID: "tenant-1"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("mission.edit without scope error = %v, want ErrDenied", err)
	}
}

func TestReBACAdapterAlternativeCoarseScopeStillRequiresRelation(t *testing.T) {
	ctx := context.Background()
	service := NewService(NewReBACAdapter(rebac.NewMemoryStore(), nil))
	principal := identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         "agent-1",
		AgentProfileID:  "agent-1",
		ViewerKind:      identity.ViewerKindAgent,
		CoarseScopes:    []string{"enterprise_graph.read"},
	}
	request := CheckRequest{
		Principal:               principal,
		Action:                  "mission.view",
		Object:                  identity.ObjectRef{Type: "mission", ID: "mission-1", TenantID: "global"},
		TenantID:                "global",
		CoarseScopeAlternatives: []string{"enterprise_graph.read"},
	}

	decision, err := service.Check(ctx, request)
	if err != nil {
		t.Fatalf("Check without relation: %v", err)
	}
	if decision.Allowed || decision.Source != DecisionSourceReBAC {
		t.Fatalf("decision without relation = %+v", decision)
	}

	if err := service.WriteRelations(ctx, []RelationTuple{{
		TenantID: "global",
		Subject:  SubjectRef{Kind: "agent", ID: "agent-1"},
		Relation: "viewer",
		Object:   identity.ObjectRef{Type: "mission", ID: "mission-1"},
	}}); err != nil {
		t.Fatalf("WriteRelations: %v", err)
	}
	decision, err = service.Check(ctx, request)
	if err != nil {
		t.Fatalf("Check with relation: %v", err)
	}
	if !decision.Allowed || decision.Source != DecisionSourceReBAC {
		t.Fatalf("decision with relation = %+v", decision)
	}

	request.Principal.CoarseScopes = []string{"operations.read"}
	decision, err = service.Check(ctx, request)
	if err != nil {
		t.Fatalf("Check without required or alternative scope: %v", err)
	}
	if decision.Allowed || decision.Source != DecisionSourceScope {
		t.Fatalf("decision without required or alternative scope = %+v", decision)
	}
}

func TestReBACAdapterBatchCheckReusesTenantRelationTuples(t *testing.T) {
	ctx := context.Background()
	store := &countingTenantRelationStore{MemoryStore: rebac.NewMemoryStore()}
	service := NewService(NewReBACAdapter(store, nil))
	principal := identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         "operator",
		UserID:          "operator",
		Username:        "operator",
		ViewerKind:      identity.ViewerKindUser,
		TenantID:        "tenant-1",
		CoarseScopes:    []string{"mission.write"},
	}
	if err := service.WriteRelations(ctx, []RelationTuple{{
		TenantID: "tenant-1",
		Subject:  SubjectRef{Kind: "user", ID: "operator"},
		Relation: "editor",
		Object:   identity.ObjectRef{Type: "mission", ID: "mission-1"},
	}}); err != nil {
		t.Fatalf("WriteRelations: %v", err)
	}
	decisions, err := service.BatchCan(ctx, []CheckRequest{
		{Principal: principal, Action: "mission.edit", Object: identity.ObjectRef{Type: "mission", ID: "mission-1", TenantID: "tenant-1"}},
		{Principal: principal, Action: "mission.edit", Object: identity.ObjectRef{Type: "mission", ID: "mission-2", TenantID: "tenant-1"}},
	})
	if err != nil {
		t.Fatalf("BatchCan: %v", err)
	}
	if len(decisions) != 2 || !decisions[0].Allowed || decisions[1].Allowed {
		t.Fatalf("decisions = %+v", decisions)
	}
	if store.tenantBatchListCalls != 1 || store.tenantListCalls != 0 || store.fullListCalls != 0 {
		t.Fatalf("tenant batch calls = %d, tenant calls = %d, full list calls = %d", store.tenantBatchListCalls, store.tenantListCalls, store.fullListCalls)
	}
}

func TestReBACAdapterBatchCheckLoadsMultipleTenantsOnceWithoutCrossTenantDecisions(t *testing.T) {
	ctx := context.Background()
	store := &countingTenantRelationStore{MemoryStore: rebac.NewMemoryStore()}
	service := NewService(NewReBACAdapter(store, nil))
	principal := identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         "operator",
		UserID:          "operator",
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    []string{"mission.read"},
	}
	if err := service.WriteRelations(ctx, []RelationTuple{
		{TenantID: "tenant-1", Subject: SubjectRef{Kind: "user", ID: "operator"}, Relation: "viewer", Object: identity.ObjectRef{Type: "mission", ID: "mission-1"}},
		{TenantID: "tenant-2", Subject: SubjectRef{Kind: "user", ID: "other"}, Relation: "viewer", Object: identity.ObjectRef{Type: "mission", ID: "mission-2"}},
	}); err != nil {
		t.Fatalf("WriteRelations: %v", err)
	}
	decisions, err := service.BatchCan(ctx, []CheckRequest{
		{Principal: principal, Action: "mission.view", Object: identity.ObjectRef{Type: "mission", ID: "mission-1"}, TenantID: "tenant-1"},
		{Principal: principal, Action: "mission.view", Object: identity.ObjectRef{Type: "mission", ID: "mission-2"}, TenantID: "tenant-2"},
	})
	if err != nil {
		t.Fatalf("BatchCan: %v", err)
	}
	if len(decisions) != 2 || !decisions[0].Allowed || decisions[1].Allowed {
		t.Fatalf("decisions = %+v", decisions)
	}
	if store.tenantBatchListCalls != 1 || store.tenantListCalls != 0 || store.fullListCalls != 0 {
		t.Fatalf("tenant batch calls = %d, tenant calls = %d, full list calls = %d", store.tenantBatchListCalls, store.tenantListCalls, store.fullListCalls)
	}
}

func TestReBACAdapterListObjectsEvaluatesComputedRelationsWithinTenant(t *testing.T) {
	ctx := context.Background()
	service := NewService(NewReBACAdapter(rebac.NewMemoryStore(), nil))
	if err := service.WriteRelations(ctx, []RelationTuple{
		{TenantID: "tenant-1", Subject: SubjectRef{Kind: "user", ID: "operator"}, Relation: "proposer", Object: identity.ObjectRef{Type: "todo", ID: "todo-b"}},
		{TenantID: "tenant-1", Subject: SubjectRef{Kind: "user", ID: "operator"}, Relation: "reviewer", Object: identity.ObjectRef{Type: "todo", ID: "todo-a"}},
		{TenantID: "tenant-1", Subject: SubjectRef{Kind: "user", ID: "other"}, Relation: "proposer", Object: identity.ObjectRef{Type: "todo", ID: "todo-private"}},
		{TenantID: "tenant-2", Subject: SubjectRef{Kind: "user", ID: "operator"}, Relation: "proposer", Object: identity.ObjectRef{Type: "todo", ID: "todo-other-tenant"}},
	}); err != nil {
		t.Fatalf("WriteRelations: %v", err)
	}
	principal := identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         "operator",
		UserID:          "operator",
		Username:        "operator",
		ViewerKind:      identity.ViewerKindUser,
		TenantID:        "tenant-1",
	}

	objects, err := service.ListObjects(ctx, principal, "reader", "todo", "tenant-1")
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(objects) != 2 || objects[0].ID != "todo-a" || objects[1].ID != "todo-b" {
		t.Fatalf("objects = %+v", objects)
	}
}

func TestReBACAdapterDeniesActionsWithoutRelationMapping(t *testing.T) {
	service := NewService(NewReBACAdapter(rebac.NewMemoryStore(), nil))
	principal := identity.Principal{
		SubjectProvider: identity.ProviderLocal,
		Subject:         "operator",
		UserID:          "operator",
		Username:        "operator",
		ViewerKind:      identity.ViewerKindUser,
		TenantID:        "tenant-1",
		CoarseScopes:    []string{"operations.read"},
	}

	err := service.Can(context.Background(), principal, "operations.read", identity.ObjectRef{Type: "route", ID: "GET /api/workforce/members", TenantID: "tenant-1"})

	if !errors.Is(err, ErrDenied) {
		t.Fatalf("unmapped action error = %v, want ErrDenied", err)
	}
}

func TestRelationMappingIncludesDiscussionActions(t *testing.T) {
	cases := map[string]relationMapping{
		"discussion.read":         {Scope: "discussion.read", Relation: "viewer"},
		"discussion.write":        {Scope: "discussion.write", Relation: "poster"},
		"discussion.manage":       {Scope: "discussion.write", Relation: "manager"},
		"organization.read":       {Scope: "org.read", Relation: "viewer"},
		"organization.manage":     {Scope: "org.manage", Relation: "admin"},
		"department.read":         {Scope: "org.read", Relation: "viewer"},
		"department.manage":       {Scope: "org.manage", Relation: "editor"},
		"workforce.member.read":   {Scope: "workforce.read", Relation: "viewer"},
		"workforce.member.manage": {Scope: "workforce.manage", Relation: "editor"},
		"role.read":               {Scope: "roles.read", Relation: "viewer"},
		"role.manage":             {Scope: "roles.manage", Relation: "admin"},
		"role.assignment.read":    {Scope: "roles.read", Relation: "viewer"},
	}
	for action, want := range cases {
		got, ok := relationMappingForAction(action)
		if !ok || got != want {
			t.Fatalf("relationMappingForAction(%q) = %+v ok=%v, want %+v", action, got, ok, want)
		}
	}
}

func TestRelationMappingSkipsRawOperationsRouteScopes(t *testing.T) {
	for _, action := range []string{"org.read", "org.manage", "workforce.read", "workforce.manage", "roles.read", "roles.manage"} {
		if mapping, ok := relationMappingForAction(action); ok {
			t.Fatalf("relationMappingForAction(%q) = %+v, want no relation mapping for raw route scope", action, mapping)
		}
	}
}

func TestPlatformChecksMapScopedActionsToMembership(t *testing.T) {
	mapping, ok := relationMappingForCheck("settings.manage", "platform")
	if !ok || mapping != (relationMapping{Scope: "settings.manage", Relation: "member"}) {
		t.Fatalf("platform mapping = %+v ok=%v", mapping, ok)
	}
	if mapping, ok := relationMappingForCheck("settings.manage", "route"); ok {
		t.Fatalf("route mapping = %+v, want none", mapping)
	}
}

func TestChecksUseAgentProfileAsCanonicalHumanSubject(t *testing.T) {
	principal := identity.Principal{
		SubjectProvider: "local",
		Subject:         "operator",
		UserID:          "operator",
		Username:        "operator",
		AgentProfileID:  "human-operator",
		ViewerKind:      identity.ViewerKindUser,
	}
	if got := subjectRefFromPrincipalForObject(principal, "platform"); got != (SubjectRef{Kind: "user", ID: "human-operator"}) {
		t.Fatalf("platform subject = %+v", got)
	}
	if got := subjectRefFromPrincipalForObject(principal, "mission"); got != (SubjectRef{Kind: "user", ID: "human-operator"}) {
		t.Fatalf("mission subject = %+v", got)
	}
}

func TestSubjectKindAliasesNormalizeToAuthorizationModelTypes(t *testing.T) {
	for raw, want := range map[string]string{"human": "user", "user": "user", "ai": "agent", "agent": "agent", "organization": "organization"} {
		if got := normalizeSubjectKind(raw); got != want {
			t.Fatalf("normalizeSubjectKind(%q) = %q, want %q", raw, got, want)
		}
	}
	tuple := normalizeRelationTuple(RelationTuple{Subject: SubjectRef{Kind: "human", ID: "human-operator"}})
	if tuple.Subject.Kind != "user" {
		t.Fatalf("normalized tuple subject = %+v", tuple.Subject)
	}
}

func TestPlatformRoleAssignmentGrantsPlatformMembership(t *testing.T) {
	service := NewService(NewReBACAdapter(rebac.NewMemoryStore(), nil))
	projector := NewRelationProjector(service)
	if err := projector.WriteRoleAssignment(context.Background(), RoleAssignmentRelationSnapshot{
		AssignmentID: "platform-admin-1", Assignee: SubjectRef{Kind: "user", ID: "human-admin"},
		RoleID: "platform.admin", OrganizationID: "global", ScopeMode: "platform", TenantID: "global",
	}); err != nil {
		t.Fatalf("WriteRoleAssignment: %v", err)
	}
	principal := identity.Principal{
		SubjectProvider: identity.ProviderLocal, Subject: "admin", UserID: "admin", Username: "admin",
		AgentProfileID: "human-admin", ViewerKind: identity.ViewerKindUser, CoarseScopes: []string{"operations.read"},
	}
	decision, err := service.Check(context.Background(), CheckRequest{
		Principal: principal, Action: "operations.read",
		Object: identity.ObjectRef{Type: "platform", ID: "global", TenantID: "global"}, TenantID: "global",
	})
	if err != nil || !decision.Allowed || decision.Source != DecisionSourceReBAC {
		t.Fatalf("platform membership decision = %+v, err=%v", decision, err)
	}
}

func TestOrganizationMemberWithScopeCanCreateDevelopmentProject(t *testing.T) {
	ctx := context.Background()
	service := NewService(NewReBACAdapter(rebac.NewMemoryStore(), nil))
	if err := service.WriteRelations(ctx, []RelationTuple{{
		TenantID: "tenant-a", Subject: SubjectRef{Kind: "user", ID: "member-1"}, Relation: "member",
		Object: identity.ObjectRef{Type: "organization", ID: "org-1", TenantID: "tenant-a"},
	}}); err != nil {
		t.Fatalf("WriteRelations: %v", err)
	}
	principal := identity.Principal{
		SubjectProvider: identity.ProviderLocal, Subject: "member-1", UserID: "member-1", Username: "member-1",
		ViewerKind: identity.ViewerKindUser, TenantID: "tenant-a", CoarseScopes: []string{"development.create"},
	}
	if err := service.Can(ctx, principal, "development_project.create", identity.ObjectRef{Type: "organization", ID: "org-1", TenantID: "tenant-a"}); err != nil {
		t.Fatalf("organization member create: %v", err)
	}
	principal.CoarseScopes = nil
	if err := service.Can(ctx, principal, "development_project.create", identity.ObjectRef{Type: "organization", ID: "org-1", TenantID: "tenant-a"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("create without scope error = %v, want ErrDenied", err)
	}
}
