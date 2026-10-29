package main

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/identity"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyreasoning"
	contextview "orrery_v2/internal/app/ontologyview/context"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/projectadmin"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
)

func TestProductionOntologyContextAPIAppliesScopeSourceAndFieldPolicies(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store("core")
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	for _, organization := range []operations.OrganizationDTO{
		{OrganizationID: "tenant-1", Name: "Tenant One"},
		{OrganizationID: "tenant-2", Name: "Tenant Two"},
		{OrganizationID: "tenant-action", Name: "Action Tenant"},
	} {
		if err := organizations.SaveOrganization(ctx, organization); err != nil {
			t.Fatalf("save organization %s: %v", organization.OrganizationID, err)
		}
	}
	authorizer := authorization.NewService(selectiveProductionAuthorizationAdapter{
		fixedProductionAuthorizationAdapter: fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}},
		deniedType:                          "organization", deniedID: "foreign-org",
	})
	tools := toolres.NewMemoryRegistry()
	if err := tools.Register(ctx, toolres.ToolDefinition{ID: agentcore.ToolID("context-tool"), Kind: toolres.KindAPITool, Name: "Context Tool", Version: "1"}); err != nil {
		t.Fatalf("register context tool: %v", err)
	}
	scopes, err := newProductionOntologyScopeState(
		core,
		organizations,
		projectadmin.NewMemoryRepository(),
		missioncontrol.NewMemoryProjectionRepository(),
		tools,
		authorizer,
		authorization.NewSnapshotService(nil, nil),
		&fixedProductionRoleAuthorizer{allowed: true},
	)
	if err != nil {
		t.Fatalf("new scope state: %v", err)
	}
	cursors, err := newProductionOntologyCursorState(ctx, nil, nil, false)
	if err != nil {
		t.Fatalf("new cursor state: %v", err)
	}
	concurrency, err := ontologycontract.NewConcurrencyLimiter(nil)
	if err != nil {
		t.Fatalf("new concurrency limiter: %v", err)
	}
	policy := datapolicy.NewPlatformStore(core)
	service, err := newProductionOntologyContextAPI(core, runtime.DBRouter, scopes, cursors, concurrency, policy, authorizer, knowledgestore.NewPlatformStore(core), memstore.NewPlatformStore(core))
	if err != nil {
		t.Fatalf("new context API: %v", err)
	}
	graph := contextgraph.NewPlatformStore(core)
	actions := actioncontract.NewPlatformRepository(core)
	now := time.Unix(4, 0).UTC()
	if err := actions.SaveActionType(ctx, actioncontract.ActionType{
		ID: "action.test", TenantID: "tenant-action", Name: "Test action", Category: actioncontract.CategorySystem,
		ProviderKind: actioncontract.ProviderInternalService, RiskLevel: actioncontract.RiskLow,
		Enabled: true, Version: "1", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save action type: %v", err)
	}
	if err := actions.SaveActionRun(ctx, actioncontract.ActionRun{
		ID: "action-run-visible", TenantID: "tenant-action", ActionTypeID: "action.test", ActionCategory: actioncontract.CategorySystem,
		Actor:    actioncontract.ActionSubject{Kind: actioncontract.SubjectAgent, ID: "agent-1"},
		Runtime:  actioncontract.RuntimeRef{Kind: actioncontract.RuntimeSystem, ID: "runtime-1"},
		ArgsHash: "sha256:test", RiskLevel: actioncontract.RiskLow, Status: actioncontract.RunStatusCompleted, CreatedAt: now,
	}); err != nil {
		t.Fatalf("save action run: %v", err)
	}
	if err := graph.SaveObject(ctx, contextgraph.Object{
		ID: "action-object", TenantID: "tenant-action", ObjectType: "action_run", CanonicalKey: "action_run:visible", Label: "Visible action run",
		OwnerAccessRef: contextgraph.AccessRef{Kind: "action_run", ID: "action-run-visible", TenantID: "tenant-action"}, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save action-run object: %v", err)
	}
	objects := []contextgraph.Object{
		{ID: "shared", TenantID: "tenant-1", ObjectType: "mission", CanonicalKey: "mission:shared-one", Label: "Tenant One", Properties: map[string]any{"public": "visible", "secret": "masked"}, OwnerAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"}, UpdatedAt: time.Unix(10, 0).UTC()},
		{ID: "shared", TenantID: "tenant-2", ObjectType: "mission", CanonicalKey: "mission:shared-two", Label: "Tenant Two", OwnerAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant-2", TenantID: "tenant-2"}, UpdatedAt: time.Unix(11, 0).UTC()},
		{ID: "idea-hidden", TenantID: "tenant-1", ObjectType: "idea", CanonicalKey: "idea:hidden", Label: "Idea requiring its original permission", OwnerAccessRef: contextgraph.AccessRef{Kind: "idea", ID: "idea-hidden", TenantID: "tenant-1"}, UpdatedAt: time.Unix(9, 0).UTC()},
		{ID: "unknown-hidden", TenantID: "tenant-1", ObjectType: "unknown", CanonicalKey: "unknown:hidden", Label: "Unknown source", OwnerAccessRef: contextgraph.AccessRef{Kind: "unknown", ID: "unknown-hidden", TenantID: "tenant-1"}, UpdatedAt: time.Unix(8, 0).UTC()},
		{ID: "idea-org", TenantID: "tenant-1", ObjectType: "idea", CanonicalKey: "idea:org", Label: "Organization idea", OwnerAccessRef: contextgraph.AccessRef{Kind: "idea", ID: "idea-org", TenantID: "tenant-1"}, UpdatedAt: time.Unix(7, 0).UTC()},
		{ID: "idea-private", TenantID: "tenant-1", ObjectType: "idea", CanonicalKey: "idea:private", Label: "Private idea", OwnerAccessRef: contextgraph.AccessRef{Kind: "idea", ID: "idea-private", TenantID: "tenant-1"}, UpdatedAt: time.Unix(6, 0).UTC()},
		{ID: "idea-foreign", TenantID: "tenant-1", ObjectType: "idea", CanonicalKey: "idea:foreign", Label: "Foreign organization idea", OwnerAccessRef: contextgraph.AccessRef{Kind: "idea", ID: "idea-foreign", TenantID: "tenant-1"}, UpdatedAt: time.Unix(5, 0).UTC()},
	}
	for _, object := range objects {
		if err := graph.SaveObject(ctx, object); err != nil {
			t.Fatalf("SaveObject(%s/%s): %v", object.TenantID, object.ID, err)
		}
	}
	if err := graph.SaveObject(ctx, contextgraph.Object{
		ID: "system-tool", TenantID: "system", ObjectType: "tool", CanonicalKey: "tool:context-tool", Label: "Context Tool",
		OwnerAccessRef: contextgraph.AccessRef{Kind: "resource", ID: "context-tool", TenantID: "system"}, UpdatedAt: time.Unix(12, 0).UTC(),
	}); err != nil {
		t.Fatalf("SaveObject(system/system-tool): %v", err)
	}
	for _, sourceKind := range []string{"scheduled_task", "agent_run", "action_writeback"} {
		if err := graph.SaveObject(ctx, contextgraph.Object{
			ID: "system-" + sourceKind, TenantID: "system", ObjectType: sourceKind,
			CanonicalKey: sourceKind + ":system-source", Label: "System " + sourceKind,
			OwnerAccessRef: contextgraph.AccessRef{Kind: sourceKind, ID: "source-" + sourceKind, TenantID: "system"}, UpdatedAt: time.Unix(13, 0).UTC(),
		}); err != nil {
			t.Fatalf("SaveObject(system/%s): %v", sourceKind, err)
		}
	}
	if err := graph.SaveObject(ctx, contextgraph.Object{
		ID: "mismatched-scheduled-task", TenantID: "system", ObjectType: "scheduled_task",
		CanonicalKey: "scheduled_task:mismatched", Label: "Mismatched scheduled task",
		OwnerAccessRef: contextgraph.AccessRef{Kind: "scheduled_task", ID: "source-mismatch", TenantID: "tenant-1"}, UpdatedAt: time.Unix(14, 0).UTC(),
	}); err != nil {
		t.Fatalf("SaveObject(system/mismatched-scheduled-task): %v", err)
	}
	for key, record := range map[string]db.Record{
		"tenant-1::idea-org": {
			"tenant_id": "tenant-1", "idea_id": "idea-org", "visibility": "organization", "payload": `{"created_by":"other-user"}`,
		},
		"tenant-1::idea-private": {
			"tenant_id": "tenant-1", "idea_id": "idea-private", "visibility": "private", "payload": `{"created_by":"other-user"}`,
		},
		"tenant-1::idea-foreign": {
			"tenant_id": "tenant-1", "organization_id": "foreign-org", "idea_id": "idea-foreign", "visibility": "organization", "payload": `{"created_by":"other-user"}`,
		},
	} {
		if err := runtime.DBRouter.PutRecord(ctx, "core.ideas", key, record); err != nil {
			t.Fatalf("put idea source %s: %v", key, err)
		}
	}
	if err := policy.SaveFieldPolicy(ctx, datapolicy.FieldPolicy{
		ID: "context-mask-secret", TenantID: "tenant-1", Consumer: "ontology_workbench", Purpose: "ontology.context.read",
		ObjectType: "mission", FieldPath: "properties.secret", Effect: datapolicy.EffectMask, Enabled: true,
	}); err != nil {
		t.Fatalf("SaveFieldPolicy: %v", err)
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "oidc", Subject: "subject-1", UserID: "user-1", ViewerKind: identity.ViewerKindUser,
		CoarseScopes: []string{"operations.read", "idea.read"},
	})
	page, err := service.ListObjects(ctx, principal, contextview.ObjectListQuery{TenantID: "tenant-1", Limit: 20})
	if err != nil {
		t.Fatalf("ListObjects: %v", err)
	}
	if len(page.Items) != 2 || page.Items[0].ID != "shared" || page.Items[1].ID != "idea-org" {
		t.Fatalf("source-filtered objects = %+v", page.Items)
	}
	detail, err := service.GetObject(ctx, principal, "tenant-1", "shared")
	if err != nil {
		t.Fatalf("GetObject: %v", err)
	}
	if detail.Label != "Tenant One" || detail.Properties["public"] != "visible" || detail.Properties["secret"] != nil {
		t.Fatalf("field-filtered detail = %+v", detail)
	}
	t.Run("inference respects production source and field policies", func(t *testing.T) {
		registry := ontology.NewMemoryRegistry()
		if err := registry.SaveObjectType(ctx, ontology.ObjectType{ID: "mission", DisplayName: "Mission", TenantScope: ontology.TenantScopeGlobal, CanonicalKeyRule: "mission:{id}"}); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"public", "secret", "risk"} {
			if err := registry.SavePropertyType(ctx, ontology.PropertyType{ID: "mission." + key, ObjectTypeID: "mission", PropertyID: key, ValueType: ontology.ValueTypeString}); err != nil {
				t.Fatal(err)
			}
		}
		ref := contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"}
		if err := graph.SaveFact(ctx, contextgraph.Fact{ID: "secret-fact", TenantID: "tenant-1", Subject: contextgraph.Ref{Kind: "mission", ID: "shared"}, Predicate: "secret", Value: "masked", SourceAccessRef: ref, SubjectAccessRef: ref, SourceStreamID: "inference-test-secret", SourceVersion: 1}); err != nil {
			t.Fatal(err)
		}
		api := &productionOntologyInferenceAPI{gate: scopes.Gate, context: service, registry: registry, policy: policy, concurrency: concurrency, service: ontologyreasoning.NewService(ontologyreasoning.ServiceOptions{Store: ontologyreasoning.NewPlatformStore(core)})}
		rules := []ontologyreasoning.Rule{{ID: "public-rule", Version: "1", When: []ontologyreasoning.Atom{{Kind: "fact", Subject: "?m", Predicate: "public", Value: "visible"}}, Then: ontologyreasoning.Atom{Kind: "fact", Subject: "?m", Predicate: "risk", Value: "visible-risk"}}, {ID: "secret-rule", Version: "1", When: []ontologyreasoning.Atom{{Kind: "fact", Subject: "?m", Predicate: "secret", Value: "masked"}}, Then: ontologyreasoning.Atom{Kind: "fact", Subject: "?m", Predicate: "risk", Value: "secret-risk"}}}
		run, err := api.Evaluate(ctx, principal, ontologyreasoning.EvaluateRequest{TenantID: "tenant-1", RootID: "shared", Rules: rules})
		if err != nil {
			t.Fatal(err)
		}
		if len(run.Candidates) != 1 || run.Candidates[0].Value != "visible-risk" || len(run.Proposals) != 1 {
			t.Fatalf("production inference did not preserve masks: %+v", run.Candidates)
		}
		for _, premise := range run.Request.Evaluation.Closure.Premises {
			if premise.Atom.Predicate == "secret" {
				t.Fatal("masked same-name fact reached inference ledger")
			}
		}
		if _, err := api.Evaluate(ctx, principal, ontologyreasoning.EvaluateRequest{TenantID: "tenant-1", RootID: "idea-private", Rules: rules}); err == nil {
			t.Fatal("private source was used for inference")
		}
	})
	if _, err := service.GetObject(ctx, principal, "tenant-1", "idea-hidden"); !errors.Is(err, ontologycontract.ErrUnavailable) {
		t.Fatalf("missing source idea error = %v", err)
	}
	if _, err := service.GetObject(ctx, principal, "tenant-1", "idea-private"); !errors.Is(err, ontologycontract.ErrUnavailable) {
		t.Fatalf("private source idea error = %v", err)
	}
	if _, err := service.GetObject(ctx, principal, "tenant-1", "idea-foreign"); !errors.Is(err, ontologycontract.ErrUnavailable) {
		t.Fatalf("foreign organization idea error = %v", err)
	}
	other, err := service.GetObject(ctx, principal, "tenant-2", "shared")
	if err != nil || other.Label != "Tenant Two" {
		t.Fatalf("tenant-scoped duplicate = %+v, err=%v", other, err)
	}
	localAuthorizer := authorization.NewService(authorization.NewScopeAdapter(nil))
	localScopes, err := newProductionOntologyScopeState(
		core, organizations, projectadmin.NewMemoryRepository(), missioncontrol.NewMemoryProjectionRepository(), tools,
		localAuthorizer, authorization.NewSnapshotService(nil, authorization.NewPolicyRevisionSource(policy)), &fixedProductionRoleAuthorizer{allowed: true},
	)
	if err != nil {
		t.Fatalf("new local scope state: %v", err)
	}
	localService, err := newProductionOntologyContextAPI(
		core, runtime.DBRouter, localScopes, cursors, concurrency, policy, localAuthorizer,
		knowledgestore.NewPlatformStore(core), memstore.NewPlatformStore(core),
	)
	if err != nil {
		t.Fatalf("new local context API: %v", err)
	}
	localPrincipal := principal
	localPrincipal.CoarseScopes = append(localPrincipal.CoarseScopes, operations.PermissionOrgRead)
	localPage, err := localService.ListObjects(ctx, localPrincipal, contextview.ObjectListQuery{TenantID: "tenant-1", Limit: 20})
	if err != nil || len(localPage.Items) != 3 || localPage.Items[0].ID != "shared" || localPage.Items[1].ID != "idea-org" || localPage.Items[2].ID != "idea-foreign" {
		t.Fatalf("role-authorized local objects = %+v, err=%v", localPage.Items, err)
	}
	graphPrincipal := principal
	graphPrincipal.CoarseScopes = []string{"enterprise_graph.read"}
	graphPage, err := localService.ListObjects(ctx, graphPrincipal, contextview.ObjectListQuery{TenantID: "tenant-1", Limit: 20})
	if err != nil || len(graphPage.Items) != 1 || graphPage.Items[0].ID != "shared" {
		t.Fatalf("enterprise-graph read objects = %+v, err=%v", graphPage.Items, err)
	}
	actionPage, err := localService.ListObjects(ctx, graphPrincipal, contextview.ObjectListQuery{TenantID: "tenant-action", Limit: 20})
	if err != nil || len(actionPage.Items) != 1 || actionPage.Items[0].ID != "action-object" {
		t.Fatalf("enterprise-graph action-run objects = %+v, err=%v", actionPage.Items, err)
	}
	deniedOrganizationAuthorizer := authorization.NewService(selectiveProductionAuthorizationAdapter{
		fixedProductionAuthorizationAdapter: fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}},
		deniedType:                          "organization", deniedID: "tenant-1",
	})
	trustedScopes, err := newProductionOntologyScopeState(
		core, organizations, projectadmin.NewMemoryRepository(), missioncontrol.NewMemoryProjectionRepository(), tools,
		deniedOrganizationAuthorizer, authorization.NewSnapshotService(nil, authorization.NewPolicyRevisionSource(policy)), operations.NewRolePermissionAuthorizer(nil, nil, nil),
	)
	if err != nil {
		t.Fatalf("new trusted internal scope state: %v", err)
	}
	trustedService, err := newProductionOntologyContextAPI(
		core, runtime.DBRouter, trustedScopes, cursors, concurrency, policy, deniedOrganizationAuthorizer,
		knowledgestore.NewPlatformStore(core), memstore.NewPlatformStore(core),
	)
	if err != nil {
		t.Fatalf("new trusted internal context API: %v", err)
	}
	trustedPrincipal := principal
	trustedPrincipal.CoarseScopes = append(trustedPrincipal.CoarseScopes, operations.PermissionOrgRead)
	trustedCtx := identity.ContextWithTrustedInternalService(ctx)
	trustedObject, err := trustedService.GetObject(trustedCtx, trustedPrincipal, "tenant-1", "shared")
	if err != nil || trustedObject.Label != "Tenant One" {
		t.Fatalf("trusted internal organization object = %+v, err=%v", trustedObject, err)
	}
	systemTool, err := service.GetObject(ctx, principal, "system", "system-tool")
	if err != nil || systemTool.Label != "Context Tool" {
		t.Fatalf("system resource source = %+v, err=%v", systemTool, err)
	}
	for _, sourceKind := range []string{"scheduled_task", "agent_run", "action_writeback"} {
		object, err := service.GetObject(ctx, principal, "system", "system-"+sourceKind)
		if err != nil || object.ID != "system-"+sourceKind {
			t.Fatalf("trusted %s source = %+v, err=%v", sourceKind, object, err)
		}
	}
	if _, err := service.GetObject(ctx, principal, "system", "mismatched-scheduled-task"); !errors.Is(err, ontologycontract.ErrUnavailable) {
		t.Fatalf("mismatched trusted projection source error = %v", err)
	}
	summary, err := service.Summary(ctx, principal, "tenant-1")
	if err != nil || summary.Data.Objects.Value == nil || *summary.Data.Objects.Value != 2 {
		t.Fatalf("visible summary = %+v, err=%v", summary, err)
	}
	if err := policy.SavePurposePolicy(ctx, datapolicy.PurposePolicy{
		ID: "context-deny-idea", TenantID: "tenant-1", Consumer: "ontology_workbench", Purpose: "ontology.context.read",
		ObjectType: "idea", Effect: datapolicy.EffectDeny, Enabled: true,
	}); err != nil {
		t.Fatalf("SavePurposePolicy: %v", err)
	}
	page, err = service.ListObjects(ctx, principal, contextview.ObjectListQuery{TenantID: "tenant-1", Limit: 20})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "shared" {
		t.Fatalf("type-filtered objects = %+v, err=%v", page.Items, err)
	}
	summary, err = service.Summary(ctx, principal, "tenant-1")
	if err != nil || summary.Data.Objects.Status != ontologycontract.MetricUnavailable {
		t.Fatalf("policy-limited summary = %+v, err=%v", summary, err)
	}
}

type selectiveProductionAuthorizationAdapter struct {
	fixedProductionAuthorizationAdapter
	deniedType string
	deniedID   string
}

func (a selectiveProductionAuthorizationAdapter) Check(ctx context.Context, request authorization.CheckRequest) (authorization.Decision, error) {
	if request.Object.Type == a.deniedType && request.Object.ID == a.deniedID {
		return authorization.Decision{Allowed: false, Source: authorization.DecisionSourceOpenFGA}, nil
	}
	return a.fixedProductionAuthorizationAdapter.Check(ctx, request)
}

func (a selectiveProductionAuthorizationAdapter) BatchCheck(ctx context.Context, requests []authorization.CheckRequest) ([]authorization.Decision, error) {
	decisions := make([]authorization.Decision, len(requests))
	for index, request := range requests {
		decision, err := a.Check(ctx, request)
		if err != nil {
			return nil, err
		}
		decisions[index] = decision
	}
	return decisions, nil
}

func TestProductionOntologyContextAPIFailsClosedWithoutDependencies(t *testing.T) {
	if _, err := newProductionOntologyContextAPI(nil, nil, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("missing dependencies returned nil error")
	}
}
