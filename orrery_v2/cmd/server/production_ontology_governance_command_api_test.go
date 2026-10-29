package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	governanceview "orrery_v2/internal/app/ontologyview/governance"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/projectadmin"
	"orrery_v2/internal/app/rebac"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
)

func TestProductionOntologyGovernanceMutationAPIDecidesPlansAndApplies(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	if err := organizations.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: "tenant-1", Name: "Tenant One"}); err != nil {
		t.Fatalf("SaveOrganization: %v", err)
	}
	authorizer := authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}})
	policy := datapolicy.NewPlatformStore(core)
	authorizationRevisions := rebac.NewPlatformStore(core)
	scopes, err := newProductionOntologyScopeState(
		core, organizations, projectadmin.NewMemoryRepository(), missioncontrol.NewMemoryProjectionRepository(), toolres.NewMemoryRegistry(),
		authorizer, authorization.NewSnapshotService(authorization.NewAuthorizationRevisionSource(authorizationRevisions), authorization.NewPolicyRevisionSource(policy)), &fixedProductionRoleAuthorizer{allowed: true},
	)
	if err != nil {
		t.Fatalf("newProductionOntologyScopeState: %v", err)
	}
	concurrency, err := ontologycontract.NewConcurrencyLimiter(nil)
	if err != nil {
		t.Fatalf("NewConcurrencyLimiter: %v", err)
	}
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("newProductionOntologyProjectionRunner: %v", err)
	}
	service, err := newProductionOntologyGovernanceCommandAPI(
		core, runtime.DBRouter, scopes, concurrency, policy, authorizer,
		knowledgestore.NewPlatformStore(core), memstore.NewPlatformStore(core), runner,
	)
	if err != nil {
		t.Fatalf("newProductionOntologyGovernanceCommandAPI: %v", err)
	}
	registry := ontology.NewPlatformRegistry(core)
	if err := registry.SaveMappingRule(ctx, ontology.MappingRule{
		ID: "custom.created", TenantID: "tenant-1", EventType: "custom.created", SourceDomain: "custom",
		SourceRef: "custom.events", EventSchemaVersion: "v1", Rule: map[string]any{"objects": []any{}}, Enabled: true,
	}); err != nil {
		t.Fatalf("SaveMappingRule: %v", err)
	}
	proposals := ontology.NewPlatformMetadataProposalStore(core)
	proposal := ontology.OntologyMetadataProposal{
		ID: "proposal-1", TenantID: "tenant-1", TargetKind: "mapping_rule", TargetID: "custom.created",
		MetadataKey: "source_domain", ProposedValue: "planning", Source: "operator_suggested.admin",
		SourceKind: ontology.MetadataProposalSourceOperatorSuggested, Confidence: 1, Status: ontology.MetadataProposalStatusProposed,
	}
	if err := proposals.SaveMetadataProposal(ctx, proposal); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	proposal, err = proposals.GetMetadataProposalForTenant(ctx, "tenant-1", proposal.ID)
	if err != nil {
		t.Fatalf("GetMetadataProposalForTenant: %v", err)
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "oidc", Subject: "reviewer-subject", UserID: "reviewer-1", ViewerKind: identity.ViewerKindUser,
		CoarseScopes: []string{"operations.manage"},
	})
	if _, err := service.DecideProposal(ctx, principal, governanceview.DecideProposalRequest{
		TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: proposal.Revision,
		Decision: ontology.MetadataProposalStatusAccepted, Reason: "unverifiable evidence",
		EvidenceRefs: []ontology.MetadataEvidenceRef{{Kind: "idea", ID: "missing-idea"}},
	}); !errors.Is(err, ontologyscope.ErrAccessDenied) {
		t.Fatalf("DecideProposal unverifiable evidence error = %v", err)
	}
	unchanged, err := proposals.GetMetadataProposalForTenant(ctx, "tenant-1", proposal.ID)
	if err != nil || unchanged.Status != ontology.MetadataProposalStatusProposed || len(unchanged.ReviewEvidenceRefs) != 0 {
		t.Fatalf("proposal changed after unverifiable evidence: %+v, err=%v", unchanged, err)
	}
	decided, err := service.DecideProposal(ctx, principal, governanceview.DecideProposalRequest{
		TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: proposal.Revision,
		Decision: ontology.MetadataProposalStatusAccepted, Reason: "reviewed",
	})
	if err != nil {
		t.Fatalf("DecideProposal: %v", err)
	}
	if decided.ReviewedBy != "reviewer-1" || decided.ReviewedByKind != identity.ViewerKindUser {
		t.Fatalf("decided proposal = %+v", decided)
	}
	denyPlan := datapolicy.PurposePolicy{
		ID: "deny-governance-plan", TenantID: "tenant-1", Consumer: "ontology_workbench",
		Purpose: "ontology.governance.plan", ObjectType: "mapping_rule", Effect: datapolicy.EffectDeny, Enabled: true,
	}
	if err := policy.SavePurposePolicy(ctx, denyPlan); err != nil {
		t.Fatalf("SavePurposePolicy deny: %v", err)
	}
	if _, err := service.CreatePromotionPlan(ctx, principal, governanceview.CreatePromotionPlanRequest{
		TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: decided.Revision,
	}); !errors.Is(err, ontologyscope.ErrAccessDenied) {
		t.Fatalf("CreatePromotionPlan purpose deny error = %v", err)
	}
	denyPlan.Enabled = false
	if err := policy.SavePurposePolicy(ctx, denyPlan); err != nil {
		t.Fatalf("SavePurposePolicy disable: %v", err)
	}
	plan, err := service.CreatePromotionPlan(ctx, principal, governanceview.CreatePromotionPlanRequest{
		TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: decided.Revision,
	})
	if err != nil {
		t.Fatalf("CreatePromotionPlan: %v", err)
	}
	if plan.PlanID == "" || len(plan.Changes) != 1 || plan.Changes[0].TargetKind != "mapping_rule" {
		t.Fatalf("promotion plan = %+v", plan)
	}
	receipt, err := service.ApplyPromotionPlan(ctx, principal, governanceview.ApplyPromotionPlanRequest{
		TenantID: "tenant-1", PlanID: plan.PlanID, IdempotencyKey: "apply-1", Confirmed: true,
	})
	if err != nil {
		t.Fatalf("ApplyPromotionPlan: %v", err)
	}
	if !receipt.Applied || receipt.ActorID != "reviewer-1" || receipt.ProposalRevision != decided.Revision {
		t.Fatalf("promotion receipt = %+v", receipt)
	}
	if receipt.ProjectionRefreshStatus != projectionRefreshSucceeded {
		t.Fatalf("projection refresh status = %q", receipt.ProjectionRefreshStatus)
	}
	rules, err := registry.ListMappingRules(ctx, "tenant-1")
	if err != nil || !productionGovernanceHasMappingSource(rules, "custom.created", "planning") {
		t.Fatalf("mapping rules = %+v err=%v", rules, err)
	}
}

func TestProductionOntologyGovernanceDefinitionPromotionRefreshesProjectionRuntime(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	if err := organizations.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: "tenant-1", Name: "Tenant One"}); err != nil {
		t.Fatalf("SaveOrganization: %v", err)
	}
	authorizer := authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}})
	policy := datapolicy.NewPlatformStore(core)
	authorizationRevisions := rebac.NewPlatformStore(core)
	scopes, err := newProductionOntologyScopeState(
		core, organizations, projectadmin.NewMemoryRepository(), missioncontrol.NewMemoryProjectionRepository(), toolres.NewMemoryRegistry(),
		authorizer, authorization.NewSnapshotService(authorization.NewAuthorizationRevisionSource(authorizationRevisions), authorization.NewPolicyRevisionSource(policy)), &fixedProductionRoleAuthorizer{allowed: true},
	)
	if err != nil {
		t.Fatalf("newProductionOntologyScopeState: %v", err)
	}
	concurrency, err := ontologycontract.NewConcurrencyLimiter(nil)
	if err != nil {
		t.Fatalf("NewConcurrencyLimiter: %v", err)
	}
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("newProductionOntologyProjectionRunner: %v", err)
	}
	service, err := newProductionOntologyGovernanceCommandAPI(
		core, runtime.DBRouter, scopes, concurrency, policy, authorizer,
		knowledgestore.NewPlatformStore(core), memstore.NewPlatformStore(core), runner,
	)
	if err != nil {
		t.Fatalf("newProductionOntologyGovernanceCommandAPI: %v", err)
	}

	registry := ontology.NewPlatformRegistry(core)
	for _, objectType := range []ontology.ObjectType{
		{ID: "runtime_project", TenantID: "tenant-1", TenantScope: ontology.TenantScopeTenant, DisplayName: "Runtime project", CanonicalKeyRule: "runtime_project:{id}"},
		{ID: "runtime_tool", TenantID: "tenant-1", TenantScope: ontology.TenantScopeTenant, DisplayName: "Runtime tool", CanonicalKeyRule: "runtime_tool:{id}"},
	} {
		if err := registry.SaveObjectType(ctx, objectType); err != nil {
			t.Fatalf("SaveObjectType(%s): %v", objectType.ID, err)
		}
	}
	if err := registry.SaveLinkType(ctx, ontology.LinkType{
		ID: "project_uses_tool", TenantID: "tenant-1", TenantScope: ontology.TenantScopeTenant,
		DisplayName: "Project uses tool", FromObjectType: "runtime_project", ToObjectType: "runtime_tool", Cardinality: ontology.CardinalityManyToMany,
	}); err != nil {
		t.Fatalf("SaveLinkType: %v", err)
	}
	definition, err := json.Marshal(ontology.MappingRule{
		ID: "project.tool.linked", TenantID: "tenant-1", EventType: "project.tool.linked",
		ObjectTypes: []string{"runtime_project", "runtime_tool"}, LinkTypes: []string{"project_uses_tool"}, Enabled: true,
		Rule: map[string]any{
			"objects": []any{
				map[string]any{"type": "runtime_project", "id": "$.project_id", "canonical_key": "runtime_project:{project_id}"},
				map[string]any{"type": "runtime_tool", "id": "$.tool_id", "canonical_key": "runtime_tool:{tool_id}"},
			},
			"links": []any{
				map[string]any{"type": "project_uses_tool", "from_type": "runtime_project", "from_id": "$.project_id", "to_type": "runtime_tool", "to_id": "$.tool_id"},
			},
		},
	})
	if err != nil {
		t.Fatalf("Marshal mapping rule: %v", err)
	}
	proposals := ontology.NewPlatformMetadataProposalStore(core)
	proposal := ontology.OntologyMetadataProposal{
		ID: "proposal-mapping-definition-runtime", TenantID: "tenant-1", TargetKind: "mapping_rule", TargetID: "project.tool.linked",
		MetadataKey: "definition", ProposedValue: string(definition), Source: "operator_suggested.ontology_admin",
		SourceKind: ontology.MetadataProposalSourceOperatorSuggested, Confidence: 1, Status: ontology.MetadataProposalStatusProposed,
	}
	if err := proposals.SaveMetadataProposal(ctx, proposal); err != nil {
		t.Fatalf("SaveMetadataProposal: %v", err)
	}
	proposal, err = proposals.GetMetadataProposalForTenant(ctx, "tenant-1", proposal.ID)
	if err != nil {
		t.Fatalf("GetMetadataProposalForTenant: %v", err)
	}
	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "oidc", Subject: "reviewer-subject", UserID: "reviewer-1", ViewerKind: identity.ViewerKindUser,
		CoarseScopes: []string{"operations.manage"},
	})
	decided, err := service.DecideProposal(ctx, principal, governanceview.DecideProposalRequest{
		TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: proposal.Revision,
		Decision: ontology.MetadataProposalStatusAccepted, Reason: "definition reviewed",
	})
	if err != nil {
		t.Fatalf("DecideProposal: %v", err)
	}
	plan, err := service.CreatePromotionPlan(ctx, principal, governanceview.CreatePromotionPlanRequest{
		TenantID: "tenant-1", ProposalID: proposal.ID, ExpectedRevision: decided.Revision,
	})
	if err != nil {
		t.Fatalf("CreatePromotionPlan: %v", err)
	}
	service.runner = failingMappingProjectionRefresher{}
	applyRequest := governanceview.ApplyPromotionPlanRequest{
		TenantID: "tenant-1", PlanID: plan.PlanID, IdempotencyKey: "test-idempotency-key", Confirmed: true,
	}
	receipt, err := service.ApplyPromotionPlan(ctx, principal, applyRequest)
	if err != nil {
		t.Fatalf("ApplyPromotionPlan: %v", err)
	}
	if !receipt.Applied || !metadataPromotionChangesRequireProjectionRefresh(receipt.Changes) || receipt.ProjectionRefreshStatus != projectionRefreshPending {
		t.Fatalf("promotion receipt = %+v", receipt)
	}
	refreshStore := ontology.NewPlatformMappingProjectionRefreshStore(core)
	pending, err := refreshStore.GetMappingProjectionRefresh(ctx, receipt.ReceiptID)
	if err != nil || pending.State != ontology.MappingProjectionRefreshPending || pending.Attempts != 1 {
		t.Fatalf("pending refresh = %+v err=%v", pending, err)
	}
	if err := refreshStore.RecordMappingProjectionRefreshFailure(ctx, receipt.ReceiptID, "projection_refresh_failed", pending.Attempts, time.Now().UTC().Add(time.Minute), 2); err != nil {
		t.Fatalf("abandon refresh: %v", err)
	}
	service.runner = runner
	retried, err := service.RetryProjectionRefresh(ctx, principal, governanceview.RetryProjectionRefreshRequest{
		TenantID: "tenant-1", ReceiptID: receipt.ReceiptID, IdempotencyKey: "retry-definition-1",
	})
	if err != nil || retried.Status != projectionRefreshPending || retried.ProposalID != receipt.ProposalID || retried.IdempotentReplay {
		t.Fatalf("RetryProjectionRefresh = %+v err=%v", retried, err)
	}
	replayed, err := service.RetryProjectionRefresh(ctx, principal, governanceview.RetryProjectionRefreshRequest{
		TenantID: "tenant-1", ReceiptID: receipt.ReceiptID, IdempotencyKey: "retry-definition-1",
	})
	if err != nil || replayed.Status != projectionRefreshPending || !replayed.IdempotentReplay {
		t.Fatalf("idempotent RetryProjectionRefresh = %+v err=%v", replayed, err)
	}
	completedAfterRetry, err := refreshStore.GetMappingProjectionRefresh(ctx, receipt.ReceiptID)
	if err != nil || completedAfterRetry.State != ontology.MappingProjectionRefreshSucceeded {
		t.Fatalf("completed refresh after retry = %+v err=%v", completedAfterRetry, err)
	}
	if _, err := service.RetryProjectionRefresh(ctx, principal, governanceview.RetryProjectionRefreshRequest{
		TenantID: "tenant-2", ReceiptID: receipt.ReceiptID, IdempotencyKey: "retry-definition-cross-tenant",
	}); err == nil {
		t.Fatal("cross-tenant projection refresh retry succeeded")
	}
	if _, err := core.Exec(ctx, `DELETE FROM ontology_mapping_projection_refreshes WHERE receipt_id = ?`, receipt.ReceiptID); err != nil {
		t.Fatalf("remove refresh to simulate a pre-outbox receipt: %v", err)
	}

	receipt, err = service.ApplyPromotionPlan(ctx, principal, applyRequest)
	if err != nil || receipt.ProjectionRefreshStatus != projectionRefreshSucceeded {
		t.Fatalf("idempotent refresh retry receipt = %+v err=%v", receipt, err)
	}
	completed, err := refreshStore.GetMappingProjectionRefresh(ctx, receipt.ReceiptID)
	if err != nil || completed.State != ontology.MappingProjectionRefreshSucceeded {
		t.Fatalf("completed refresh = %+v err=%v", completed, err)
	}

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID: "project-tool-linked:1", TenantID: "tenant-1", Type: "project.tool.linked", StreamID: "project:project-1", Version: 1,
		OccurredAt: time.Unix(30, 0).UTC(), PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "project"},
		Payload: map[string]any{"project_id": "project-1", "tool_id": "tool-1"},
	})
	if err != nil {
		t.Fatalf("ApplyEvent after promotion refresh: %v", err)
	}
	if !report.OK || report.MatchedProjectionCount != 1 || report.AppliedProjectionCount != 1 {
		t.Fatalf("projection report = %+v", report)
	}
	objects, links, _, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if !productionGovernanceHasObject(objects, "runtime_project", "project-1") || !productionGovernanceHasObject(objects, "runtime_tool", "tool-1") {
		t.Fatalf("objects = %+v", objects)
	}
	if !productionGovernanceHasLink(links, "project_uses_tool", "project-1", "tool-1") {
		t.Fatalf("links = %+v", links)
	}
	if len(evidence) != 1 || evidence[0].ID != "project-tool-linked:1" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

type failingMappingProjectionRefresher struct{}

func (failingMappingProjectionRefresher) RefreshProjections(context.Context, ontology.Registry) error {
	return errors.New("refresh unavailable")
}

func TestProductionOntologyGovernanceRefreshReportsAbandonedJobWithoutRetrying(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	refreshes := ontology.NewPlatformMappingProjectionRefreshStore(core)
	if err := refreshes.EnqueueMappingProjectionRefresh(ctx, "receipt-abandoned", "tenant-1"); err != nil {
		t.Fatalf("enqueue refresh: %v", err)
	}
	job, err := refreshes.GetMappingProjectionRefresh(ctx, "receipt-abandoned")
	if err != nil {
		t.Fatalf("get refresh: %v", err)
	}
	if err := refreshes.RecordMappingProjectionRefreshFailure(ctx, job.ReceiptID, "projection_refresh_failed", job.Attempts, time.Now().UTC().Add(time.Minute), 1); err != nil {
		t.Fatalf("abandon refresh: %v", err)
	}
	runner := &countingMappingProjectionRefresher{}
	service := &productionOntologyGovernanceCommandAPI{core: core, runner: runner, refreshes: refreshes}
	status := service.deliverMappingProjectionRefresh(ctx, ontology.MetadataProposalPromotionReceipt{ReceiptID: job.ReceiptID, TenantID: job.TenantID})
	if status != projectionRefreshAbandoned || runner.calls != 0 {
		t.Fatalf("abandoned refresh status = %q calls = %d", status, runner.calls)
	}
}

type countingMappingProjectionRefresher struct{ calls int }

func (r *countingMappingProjectionRefresher) RefreshProjections(context.Context, ontology.Registry) error {
	r.calls++
	return nil
}

func productionGovernanceHasObject(objects []contextgraph.Object, objectType, id string) bool {
	for _, object := range objects {
		if object.ObjectType == objectType && object.ID == id {
			return true
		}
	}
	return false
}

func productionGovernanceHasLink(links []contextgraph.Link, linkType, fromID, toID string) bool {
	for _, link := range links {
		if link.LinkType == linkType && link.FromObjectID == fromID && link.ToObjectID == toID {
			return true
		}
	}
	return false
}

func productionGovernanceHasMappingSource(rules []ontology.MappingRule, id, source string) bool {
	for _, rule := range rules {
		if rule.ID == id && rule.SourceDomain == source {
			return true
		}
	}
	return false
}

func TestProductionOntologyGovernanceMutationAPIFailsClosedWithoutDependencies(t *testing.T) {
	if _, err := newProductionOntologyGovernanceCommandAPI(nil, nil, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("missing dependencies returned nil error")
	}
}

type productionGovernanceCommandSnapshots struct{}

func (productionGovernanceCommandSnapshots) Capture(_ context.Context, principal identity.Principal) (authorization.AuthorizationSnapshot, error) {
	return authorization.AuthorizationSnapshot{
		PrincipalKey: authorization.PrincipalCacheKey(principal), AuthorizationRevision: "authorization:0", PolicyRevision: "policy:0",
	}, nil
}
