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
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	projectionview "orrery_v2/internal/app/ontologyview/projection"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/projectadmin"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
)

func TestProductionOntologyProjectionReadAPIAppliesScopeEntityAndFieldPolicies(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	for _, organization := range []operations.OrganizationDTO{
		{OrganizationID: "tenant-1", Name: "Tenant One"},
		{OrganizationID: "tenant-2", Name: "Tenant Two"},
	} {
		if err := organizations.SaveOrganization(ctx, organization); err != nil {
			t.Fatalf("save organization %s: %v", organization.OrganizationID, err)
		}
	}
	authorizer := authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}})
	scopes, err := newProductionOntologyScopeState(
		core, organizations, projectadmin.NewMemoryRepository(), missioncontrol.NewMemoryProjectionRepository(), toolres.NewMemoryRegistry(),
		authorizer, authorization.NewSnapshotService(nil, nil), &fixedProductionRoleAuthorizer{allowed: true},
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
	fieldPolicy := datapolicy.NewPlatformStore(core)
	service, err := newProductionOntologyProjectionReadAPI(
		core, runtime.DBRouter, scopes, cursors, concurrency, fieldPolicy, authorizer,
		knowledgestore.NewPlatformStore(core), memstore.NewPlatformStore(core),
	)
	if err != nil {
		t.Fatalf("new projection read API: %v", err)
	}

	graph := contextgraph.NewPlatformStore(core)
	tenantOneRef := contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"}
	tenantTwoRef := contextgraph.AccessRef{Kind: "organization", ID: "tenant-2", TenantID: "tenant-2"}
	for _, audit := range []contextgraph.ProjectionAudit{
		{ID: "shared-audit", TenantID: "tenant-1", SourceEventID: "event-1", SourceEventType: "mission.updated", Status: "skipped", ReasonCode: "missing_target", Reason: "tenant one target missing", Details: map[string]any{"target_id": "secret-1"}, SourceAccessRef: tenantOneRef, ObservedAt: time.Unix(20, 0).UTC()},
		{ID: "shared-audit", TenantID: "tenant-2", SourceEventID: "event-2", SourceEventType: "mission.updated", Status: "replayed", ReasonCode: "missing_target", Reason: "tenant two", SourceAccessRef: tenantTwoRef, ObservedAt: time.Unix(30, 0).UTC()},
	} {
		if err := graph.SaveProjectionAudit(ctx, audit); err != nil {
			t.Fatalf("save projection audit %s/%s: %v", audit.TenantID, audit.ID, err)
		}
	}
	if err := graph.SaveProjectionAuditReplayHistory(ctx, contextgraph.ProjectionAuditReplayHistory{
		ID: "history-1", AuditID: "shared-audit", TenantID: "tenant-1", SourceEventID: "event-1", SourceEventType: "mission.updated",
		Status: "failed", Error: "target still missing", SourceAccessRef: tenantOneRef, AttemptedAt: time.Unix(25, 0).UTC(),
	}); err != nil {
		t.Fatalf("save replay history: %v", err)
	}

	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "oidc", Subject: "subject-1", UserID: "user-1", ViewerKind: identity.ViewerKindUser,
		CoarseScopes: []string{"operations.read"},
	})
	summary, err := service.Summary(ctx, principal, "tenant-1")
	if err != nil || summary.Data.UnresolvedAudits.Value == nil || *summary.Data.UnresolvedAudits.Value != 1 ||
		summary.Data.LatestActivity.ObservedAt == nil || !summary.Data.LatestActivity.ObservedAt.Equal(time.Unix(20, 0).UTC()) {
		t.Fatalf("projection summary = %+v, err=%v", summary, err)
	}
	page, err := service.ListAudits(ctx, principal, projectionview.AuditListQuery{TenantID: "tenant-1"})
	if err != nil || len(page.Items) != 1 || page.Items[0].ID != "shared-audit" || page.Items[0].Status != "skipped" {
		t.Fatalf("projection audit page = %+v, err=%v", page, err)
	}
	detail, err := service.GetAudit(ctx, principal, "tenant-1", "shared-audit")
	if err != nil || detail.Reason != "tenant one target missing" || detail.Details["target_id"] != "secret-1" {
		t.Fatalf("projection audit detail = %+v, err=%v", detail, err)
	}
	history, err := service.ListReplayHistory(ctx, principal, projectionview.ReplayHistoryListQuery{TenantID: "tenant-1", AuditID: "shared-audit"})
	if err != nil || len(history.Items) != 1 || history.Items[0].ID != "history-1" || history.Items[0].Error != "replay_failed" {
		t.Fatalf("projection replay history = %+v, err=%v", history, err)
	}

	for _, field := range []string{"reason", "details"} {
		if err := fieldPolicy.SaveFieldPolicy(ctx, datapolicy.FieldPolicy{
			ID: "mask-projection-" + field, TenantID: "tenant-1", Consumer: "ontology_workbench",
			Purpose: "ontology.projection.read", ObjectType: "mission.updated", FieldPath: field,
			Effect: datapolicy.EffectMask, Enabled: true,
		}); err != nil {
			t.Fatalf("save %s field policy: %v", field, err)
		}
	}
	detail, err = service.GetAudit(ctx, principal, "tenant-1", "shared-audit")
	if err != nil || detail.Reason != "" || detail.Details != nil {
		t.Fatalf("field-masked projection detail = %+v, err=%v", detail, err)
	}
}

func TestProductionOntologyProjectionAPIWiresReadAndReplayRoutes(t *testing.T) {
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
	req := httptest.NewRequest(http.MethodGet, "/api/ontology/projections/summary?tenant_id=system", nil)
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "human-superadmin")
	req.Header.Set("X-Permission-Scope", operations.PermissionOperationsRead)
	rec := httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("ontology projection summary status = %d body = %s", rec.Code, rec.Body.String())
	}
	var summary ontologycontract.Summary[projectionview.SummaryData]
	if err := json.NewDecoder(rec.Body).Decode(&summary); err != nil {
		t.Fatalf("decode ontology projection summary: %v", err)
	}
	if summary.Data.UnresolvedAudits.Value == nil || *summary.Data.UnresolvedAudits.Value != 0 || summary.Meta.DataRevision == "" {
		t.Fatalf("ontology projection summary = %+v", summary)
	}

	req = httptest.NewRequest(http.MethodPost, "/api/ontology/projections/audits/missing/replays?tenant_id=system", strings.NewReader(`{}`))
	req.Header.Set("X-User-ID", "operator")
	req.Header.Set("X-Viewer-Kind", "user")
	req.Header.Set("X-Agent-Profile-ID", "human-superadmin")
	req.Header.Set("X-Permission-Scope", operations.PermissionOperationsManage)
	req.Header.Set("Idempotency-Key", "replay-missing")
	rec = httptest.NewRecorder()
	serveServerTestHTTP(t, handler, rec, req)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"code":"unavailable"`) {
		t.Fatalf("ontology projection replay status = %d body = %s", rec.Code, rec.Body.String())
	}
}

func TestProductionOntologyProjectionReadAPIFailsClosedWithoutDependencies(t *testing.T) {
	if _, err := newProductionOntologyProjectionReadAPI(nil, nil, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("missing dependencies returned nil error")
	}
}

type recordingProductionProjectionExecutor struct {
	audit  contextgraph.ProjectionAudit
	actor  projectionview.CommandActor
	reason string
	calls  int
}

func (e *recordingProductionProjectionExecutor) PrepareProjectionAuditReplay(ctx context.Context, audit contextgraph.ProjectionAudit, actor projectionview.CommandActor) (projectionview.ReplayPreparation, error) {
	e.audit, e.actor = audit, actor
	e.calls++
	audit.Status = "replayed"
	audit.ObservedAt = audit.ObservedAt.Add(time.Second)
	return projectionview.ReplayPreparation{Result: audit, Effect: func(context.Context, contextgraph.WriterStore) error { return nil }}, nil
}

func (e *recordingProductionProjectionExecutor) AcknowledgeProjectionAudit(ctx context.Context, audit contextgraph.ProjectionAudit, actor projectionview.CommandActor, reason string) (contextgraph.ProjectionAudit, error) {
	e.audit, e.actor, e.reason = audit, actor, reason
	e.calls++
	audit.Status = "acknowledged"
	audit.ObservedAt = audit.ObservedAt.Add(time.Second)
	return audit, nil
}

func TestProductionOntologyProjectionMutationAPIReplayAndAcknowledgeUseTenantScopedAudit(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	for _, organization := range []operations.OrganizationDTO{
		{OrganizationID: "tenant-1", Name: "Tenant One"},
		{OrganizationID: "tenant-2", Name: "Tenant Two"},
	} {
		if err := organizations.SaveOrganization(ctx, organization); err != nil {
			t.Fatalf("save organization: %v", err)
		}
	}
	authorizer := authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}})
	scopes, err := newProductionOntologyScopeState(
		core, organizations, projectadmin.NewMemoryRepository(), missioncontrol.NewMemoryProjectionRepository(), toolres.NewMemoryRegistry(),
		authorizer, productionGovernanceCommandSnapshots{}, &fixedProductionRoleAuthorizer{allowed: true},
	)
	if err != nil {
		t.Fatalf("new scope state: %v", err)
	}
	executor := &recordingProductionProjectionExecutor{}
	policy := datapolicy.NewPlatformStore(core)
	service, err := newProductionOntologyProjectionCommandAPI(
		core, runtime.DBRouter, scopes, policy, authorizer,
		knowledgestore.NewPlatformStore(core), memstore.NewPlatformStore(core), executor,
	)
	if err != nil {
		t.Fatalf("new projection command API: %v", err)
	}
	graph := contextgraph.NewPlatformStore(core)
	for _, audit := range []contextgraph.ProjectionAudit{
		{ID: "shared-audit", TenantID: "tenant-1", SourceEventID: "event-1", SourceEventType: "project.organization.linked", Status: "skipped", ReasonCode: "missing_target", SourceAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"}, ObservedAt: time.Unix(10, 0).UTC()},
		{ID: "ack-audit", TenantID: "tenant-1", SourceEventID: "event-ack", SourceEventType: "project.organization.linked", Status: "skipped", ReasonCode: "missing_target", SourceAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"}, ObservedAt: time.Unix(15, 0).UTC()},
		{ID: "shared-audit", TenantID: "tenant-2", SourceEventID: "event-2", SourceEventType: "project.organization.linked", Status: "skipped", ReasonCode: "missing_target", SourceAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant-2", TenantID: "tenant-2"}, ObservedAt: time.Unix(20, 0).UTC()},
		{ID: "tenant-two-only", TenantID: "tenant-2", SourceEventID: "event-3", SourceEventType: "project.organization.linked", Status: "skipped", ReasonCode: "missing_target", SourceAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant-2", TenantID: "tenant-2"}, ObservedAt: time.Unix(30, 0).UTC()},
	} {
		if err := graph.SaveProjectionAudit(ctx, audit); err != nil {
			t.Fatalf("save audit %s/%s: %v", audit.TenantID, audit.ID, err)
		}
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "oidc", Subject: "operator-subject", UserID: "operator-1", ViewerKind: identity.ViewerKindUser,
		CoarseScopes: []string{"operations.manage"},
	})
	replayed, err := service.Replay(ctx, principal, projectionview.ReplayRequest{TenantID: "tenant-1", AuditID: "shared-audit", IdempotencyKey: "replay-shared"})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if replayed.Status != "replayed" || executor.audit.SourceEventID != "event-1" || executor.actor != (projectionview.CommandActor{Kind: identity.ViewerKindUser, ID: "operator-1"}) {
		t.Fatalf("replayed=%+v executor=%+v", replayed, executor)
	}
	retried, err := service.Replay(ctx, principal, projectionview.ReplayRequest{TenantID: "tenant-1", AuditID: "shared-audit", IdempotencyKey: "replay-shared"})
	if err != nil || retried.Status != "replayed" || executor.calls != 1 {
		t.Fatalf("idempotent replay=%+v err=%v executor calls=%d", retried, err, executor.calls)
	}
	acknowledged, err := service.Acknowledge(ctx, principal, projectionview.AcknowledgeRequest{TenantID: "tenant-1", AuditID: "ack-audit", Reason: "reviewed", IdempotencyKey: "ack-shared"})
	if err != nil {
		t.Fatalf("Acknowledge: %v", err)
	}
	if acknowledged.Status != "acknowledged" || executor.audit.SourceEventID != "event-ack" || executor.reason != "reviewed" {
		t.Fatalf("acknowledged=%+v executor=%+v", acknowledged, executor)
	}
	if err := policy.SavePurposePolicy(ctx, datapolicy.PurposePolicy{
		ID: "deny-projection-replay", TenantID: "tenant-1", Consumer: "ontology_workbench",
		Purpose: "ontology.projection.replay", ObjectType: "project.organization.linked", Effect: datapolicy.EffectDeny, Enabled: true,
	}); err != nil {
		t.Fatalf("save replay purpose policy: %v", err)
	}
	callsBeforeDeny := executor.calls
	if _, err := service.Replay(ctx, principal, projectionview.ReplayRequest{TenantID: "tenant-1", AuditID: "shared-audit", IdempotencyKey: "replay-denied"}); !errors.Is(err, ontologycontract.ErrUnavailable) || executor.calls != callsBeforeDeny {
		t.Fatalf("data-policy replay error=%v executor calls=%d", err, executor.calls)
	}
	if _, err := service.Replay(ctx, principal, projectionview.ReplayRequest{TenantID: "tenant-1", AuditID: "tenant-two-only", IdempotencyKey: "replay-wrong-tenant"}); !errors.Is(err, ontologycontract.ErrUnavailable) {
		t.Fatalf("wrong-tenant replay error = %v", err)
	}
}

func TestProductionOntologyProjectionMutationAPIReplaysProjectOrganizationAuditThroughScopedCommand(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("newProductionOntologyProjectionRunner: %v", err)
	}
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	organizationCommand := operations.NewCommandService(&organizationOntologyProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}})
	organizationCommand.SetOrganizationCatalogStore(organizations)
	organizationCommand.SetIDGenerator(sequenceOperationsIDs{"org-1"})
	organizationQuery := operations.NewQueryService(&organizationOntologyProfileStore{}, toolres.NewMemoryRegistry())
	organizationQuery.SetOrganizationCatalogStore(organizations)
	projects := projectadmin.NewMemoryRepository()
	viewer := authorizedProjectTestViewer("planner-1")
	api := productionAPI{
		operations:       operations.NewService(organizationQuery, organizationCommand),
		projectCommand:   newAuthorizedProjectTestService(t, projects, sequenceProjectIDs{"project-1"}, fixedProjectClock{now: time.Date(2026, 6, 20, 20, 0, 0, 0, time.UTC)}, viewer, "org-1"),
		ontologyProject:  runner,
		ontologyEvents:   newProductionOntologyEventLog(core),
		contextGraphCore: core,
	}
	project, err := api.CreateProject(ctx, viewer, projectadmin.CreateProjectCommand{
		Title: "Replay organization project", OrganizationID: "org-1",
	})
	if err != nil {
		t.Fatalf("CreateProject: %v", err)
	}
	auditID := "projection-skipped:project-organization-linked:" + project.ProjectID + ":org-1:rev-1:missing-organization-target"
	graph := contextgraph.NewPlatformStore(core)
	audit, err := graph.GetProjectionAudit(ctx, "org-1", auditID)
	if err != nil {
		t.Fatalf("missing organization projection target is audited: %v", err)
	}
	if audit.Status != "skipped" || audit.SourceEventType != "project.organization.linked" || audit.ReasonCode != "missing_organization_target" {
		t.Fatalf("projection audit = %+v", audit)
	}
	organization, err := api.CreateOrganization(ctx, operations.ViewerRef{
		ViewerID: "admin-1", ViewerKind: "user", PermissionScope: []string{operations.PermissionOrgManage},
	}, operations.CreateOrganizationCommand{Name: "Platform"})
	if err != nil {
		t.Fatalf("CreateOrganization: %v", err)
	}

	authorizer := authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}})
	scopes, err := newProductionOntologyScopeState(
		core, organizations, projects, missioncontrol.NewMemoryProjectionRepository(), toolres.NewMemoryRegistry(),
		authorizer, productionGovernanceCommandSnapshots{}, &fixedProductionRoleAuthorizer{allowed: true},
	)
	if err != nil {
		t.Fatalf("newProductionOntologyScopeState: %v", err)
	}
	service, err := newProductionOntologyProjectionCommandAPI(
		core, runtime.DBRouter, scopes, datapolicy.NewPlatformStore(core), authorizer,
		knowledgestore.NewPlatformStore(core), memstore.NewPlatformStore(core),
		productionOntologyProjectionExecutor{api: api},
	)
	if err != nil {
		t.Fatalf("newProductionOntologyProjectionCommandAPI: %v", err)
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "oidc", Subject: "operator-subject", UserID: "operator-1", ViewerKind: identity.ViewerKindUser,
		CoarseScopes: []string{"operations.manage"},
	})
	replayed, err := service.Replay(ctx, principal, projectionview.ReplayRequest{TenantID: "org-1", AuditID: auditID, IdempotencyKey: "replay-project-organization"})
	if err != nil {
		t.Fatalf("project organization projection audit can be replayed through the scoped command API: %v", err)
	}
	if replayed.Status != "replayed" {
		t.Fatalf("replayed audit = %+v", replayed)
	}
	objects, links, facts, evidence, err := graph.Snapshot(ctx, organization.OrganizationID)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "project", "project:"+project.ProjectID)
	assertContextGraphLink(t, links, "project_belongs_to_organization", "project:"+project.ProjectID, organization.OrganizationID)
	assertContextGraphFact(t, facts, "project", "project:"+project.ProjectID, "title", "Replay organization project")
	assertContextGraphEvidence(t, evidence, "project-organization-linked:"+project.ProjectID+":org-1:rev-1")
}

func TestProductionOntologyProjectionExecutorDoesNotPersistReplayLifecycleArtifacts(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	audit := contextgraph.ProjectionAudit{
		ID: "audit-actor", TenantID: "tenant-1", SourceEventID: "event-1", SourceEventType: "unsupported.event",
		Status: "skipped", ReasonCode: "unsupported", SourceAccessRef: contextgraph.AccessRef{Kind: "organization", ID: "tenant-1", TenantID: "tenant-1"},
		ObservedAt: time.Unix(40, 0).UTC(),
	}
	graph := contextgraph.NewPlatformStore(core)
	if err := graph.SaveProjectionAudit(ctx, audit); err != nil {
		t.Fatalf("save audit: %v", err)
	}
	stored, err := graph.GetProjectionAudit(ctx, audit.TenantID, audit.ID)
	if err != nil {
		t.Fatalf("get audit: %v", err)
	}
	executor := productionOntologyProjectionExecutor{api: productionAPI{contextGraphCore: core}}
	_, err = executor.PrepareProjectionAuditReplay(ctx, stored, projectionview.CommandActor{Kind: identity.ViewerKindAgent, ID: "agent-1"})
	if !errors.Is(err, ontologycontract.ErrStateConflict) {
		t.Fatalf("replay error = %v", err)
	}
	current, err := graph.GetProjectionAudit(ctx, audit.TenantID, audit.ID)
	if err != nil {
		t.Fatalf("get projection audit after compute-only replay: %v", err)
	}
	if current.Revision != stored.Revision || current.Status != stored.Status || !current.ObservedAt.Equal(stored.ObservedAt) {
		t.Fatalf("compute-only executor persisted audit mutation: before=%+v after=%+v", stored, current)
	}
	var historyCount int
	if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_projection_audit_replay_history WHERE tenant_id = ? AND audit_id = ?`, audit.TenantID, audit.ID).Scan(&historyCount); err != nil {
		t.Fatalf("count replay history: %v", err)
	}
	if historyCount != 0 {
		t.Fatalf("compute-only executor persisted %d replay history rows", historyCount)
	}
}

func TestProductionOntologyProjectionMutationAPIDeniesManagePermission(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	if err := organizations.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: "tenant-1", Name: "Tenant One"}); err != nil {
		t.Fatalf("save organization: %v", err)
	}
	authorizer := authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: false, Source: authorization.DecisionSourceOpenFGA}})
	scopes, err := newProductionOntologyScopeState(
		core, organizations, projectadmin.NewMemoryRepository(), missioncontrol.NewMemoryProjectionRepository(), toolres.NewMemoryRegistry(),
		authorizer, productionGovernanceCommandSnapshots{}, &fixedProductionRoleAuthorizer{allowed: true},
	)
	if err != nil {
		t.Fatalf("new scope state: %v", err)
	}
	executor := &recordingProductionProjectionExecutor{}
	service, err := newProductionOntologyProjectionCommandAPI(
		core, runtime.DBRouter, scopes, datapolicy.NewPlatformStore(core), authorizer,
		knowledgestore.NewPlatformStore(core), memstore.NewPlatformStore(core), executor,
	)
	if err != nil {
		t.Fatalf("new projection command API: %v", err)
	}
	principal := identity.NormalizePrincipal(identity.Principal{SubjectProvider: "oidc", Subject: "operator", UserID: "operator", ViewerKind: identity.ViewerKindUser})
	_, err = service.Replay(ctx, principal, projectionview.ReplayRequest{TenantID: "tenant-1", AuditID: "audit-1", IdempotencyKey: "replay-denied"})
	if !errors.Is(err, ontologyscope.ErrAccessDenied) || executor.calls != 0 {
		t.Fatalf("permission error=%v executor calls=%d", err, executor.calls)
	}
}

func TestProductionOntologyProjectionMutationAPIFailsClosedWithoutDependencies(t *testing.T) {
	if _, err := newProductionOntologyProjectionCommandAPI(nil, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("missing dependencies returned nil error")
	}
}
