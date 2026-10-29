package main

import (
	"context"
	"testing"
	"time"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontology"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	governanceview "orrery_v2/internal/app/ontologyview/governance"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/projectadmin"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
	toolres "orrery_v2/internal/resource/tool"
)

func TestProductionOntologyGovernanceReadAPIAppliesScopeEntityAndFieldPolicies(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.NewInitializedTestRuntime(t, OntologyModule())
	coreStore, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	organizations := operations.NewOrganizationCatalogStore(runtime.DBRouter)
	if err := organizations.SaveOrganization(ctx, operations.OrganizationDTO{OrganizationID: "tenant-1", Name: "Tenant One"}); err != nil {
		t.Fatalf("save organization: %v", err)
	}
	authorizer := authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}})
	scopes, err := newProductionOntologyScopeState(
		coreStore, organizations, projectadmin.NewMemoryRepository(), missioncontrol.NewMemoryProjectionRepository(), toolres.NewMemoryRegistry(),
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
	fieldPolicy := datapolicy.NewPlatformStore(coreStore)
	audit := operations.NewAuditStore(runtime.DBRouter, runtime.Clock, runtime.IDGenerator)
	service, err := newProductionOntologyGovernanceReadAPI(
		coreStore, runtime.DBRouter, scopes, cursors, concurrency, fieldPolicy, authorizer,
		knowledgestore.NewPlatformStore(coreStore), memstore.NewPlatformStore(coreStore), audit,
	)
	if err != nil {
		t.Fatalf("new governance read API: %v", err)
	}

	proposals := ontology.NewPlatformMetadataProposalStore(coreStore)
	proposed := productionGovernanceProposal("proposal-proposed", "tenant-1", ontology.MetadataProposalStatusProposed)
	accepted := productionGovernanceProposal("proposal-accepted", "tenant-1", ontology.MetadataProposalStatusAccepted)
	system := productionGovernanceProposal("proposal-system", "system", ontology.MetadataProposalStatusProposed)
	for _, proposal := range []ontology.OntologyMetadataProposal{proposed, accepted, system} {
		if err := proposals.SaveMetadataProposal(ctx, proposal); err != nil {
			t.Fatalf("SaveMetadataProposal(%s): %v", proposal.ID, err)
		}
	}
	accepted, err = proposals.GetMetadataProposalForTenant(ctx, "tenant-1", accepted.ID)
	if err != nil {
		t.Fatalf("GetMetadataProposalForTenant: %v", err)
	}
	receipt := ontology.MetadataProposalPromotionReceipt{
		ReceiptID: "receipt-accepted", TenantID: "tenant-1", ProposalID: accepted.ID,
		ProposalRevision: accepted.Revision, ProposalContentHash: accepted.ContentHash,
		ProposalBindingStatus: ontology.PromotionReceiptProposalBindingResolved,
		ActorID:               "reviewer-1", ActorKind: "user", IdempotencyKey: "apply:accepted",
		Status: ontology.PromotionReceiptStatusApplied, Applied: true,
	}
	if err := ontology.NewPlatformMetadataProposalPromotionReceiptStore(coreStore).SavePromotionReceipt(ctx, receipt); err != nil {
		t.Fatalf("SavePromotionReceipt: %v", err)
	}
	if err := ontology.NewPlatformMappingProjectionRefreshStore(coreStore).EnqueueMappingProjectionRefresh(ctx, receipt.ReceiptID, receipt.TenantID); err != nil {
		t.Fatalf("EnqueueMappingProjectionRefresh: %v", err)
	}
	if err := audit.RecordAuditEvent(ctx, operations.AuditEventDTO{
		AuditID: "audit-accepted", ActorID: "reviewer-1", EntityType: "ontology_metadata_proposal", EntityID: accepted.ID,
		Action: "ontology_metadata_promotion.apply", Result: "succeeded", Severity: "info", Summary: "proposal applied",
		Metadata: map[string]string{"tenant_id": "tenant-1", "actor_kind": "user"},
	}); err != nil {
		t.Fatalf("RecordAuditEvent: %v", err)
	}

	principal := identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "oidc", Subject: "subject-1", UserID: "user-1", ViewerKind: identity.ViewerKindUser,
		CoarseScopes: []string{"operations.read"},
	})
	queue, err := service.ListProposals(ctx, principal, governanceview.ProposalListQuery{TenantID: "tenant-1", Queue: governanceview.QueueActionable})
	if err != nil || len(queue.Items) != 1 || queue.Items[0].ID != proposed.ID {
		t.Fatalf("actionable queue = %+v, err=%v", queue, err)
	}
	summary, err := service.Summary(ctx, principal, "tenant-1")
	if err != nil || summary.Data.ActionableProposals.Value == nil || *summary.Data.ActionableProposals.Value != 1 ||
		summary.Data.LatestReceipt.Receipt == nil || summary.Data.LatestReceipt.Receipt.ReceiptID != receipt.ReceiptID {
		t.Fatalf("governance summary = %+v, err=%v", summary, err)
	}
	receipts, err := service.ListReceipts(ctx, principal, governanceview.ReceiptListQuery{TenantID: "tenant-1", ProposalID: accepted.ID})
	if err != nil || len(receipts.Items) != 1 || receipts.Items[0].ReceiptID != receipt.ReceiptID || receipts.Items[0].ProjectionRefreshStatus != ontology.MappingProjectionRefreshPending {
		t.Fatalf("receipt page = %+v, err=%v", receipts, err)
	}
	audits, err := service.ListAuditEvents(ctx, principal, governanceview.AuditEventListQuery{TenantID: "tenant-1", ProposalID: accepted.ID})
	if err != nil || len(audits.Items) != 1 || audits.Items[0].AuditID != "audit-accepted" || audits.Items[0].ActorKind != "user" {
		t.Fatalf("audit page = %+v, err=%v", audits, err)
	}
	if err := fieldPolicy.SaveFieldPolicy(ctx, datapolicy.FieldPolicy{
		ID: "mask-governance-value", TenantID: "tenant-1", Consumer: "ontology_workbench",
		Purpose: "ontology.governance.proposals.read", ObjectType: "organization", FieldPath: "proposed_value",
		Effect: datapolicy.EffectMask, Enabled: true,
	}); err != nil {
		t.Fatalf("SaveFieldPolicy: %v", err)
	}
	detail, err := service.GetProposal(ctx, principal, "tenant-1", proposed.ID)
	if err != nil || detail.ID != proposed.ID || detail.ProposedValue != "" {
		t.Fatalf("field-masked detail = %+v, err=%v", detail, err)
	}
	systemPage, err := service.ListProposals(ctx, principal, governanceview.ProposalListQuery{TenantID: "system", Queue: governanceview.QueueAll})
	if err != nil || len(systemPage.Items) != 1 || systemPage.Items[0].ID != system.ID {
		t.Fatalf("system scope page = %+v, err=%v", systemPage, err)
	}
}

func TestProductionGovernanceAuditReaderFiltersTenantAndKeepsStableSeek(t *testing.T) {
	ctx := context.Background()
	runtime := testkit.New(t)
	defer runtime.Close()
	store := operations.NewAuditStore(runtime.DBRouter, runtime.Clock, runtime.IDGenerator)
	for _, event := range []operations.AuditEventDTO{
		{AuditID: "audit-b", ActorID: "reviewer", EntityType: "ontology_metadata_proposal", EntityID: "proposal-1", Action: "review", Metadata: map[string]string{"tenant_id": "tenant-1"}},
		{AuditID: "audit-a", ActorID: "reviewer", EntityType: "ontology_metadata_proposal", EntityID: "proposal-1", Action: "review", Metadata: map[string]string{"tenant_id": "tenant-1"}},
		{AuditID: "audit-other", ActorID: "reviewer", EntityType: "ontology_metadata_proposal", EntityID: "proposal-1", Action: "review", Metadata: map[string]string{"tenant_id": "tenant-2"}},
	} {
		if err := store.RecordAuditEvent(ctx, event); err != nil {
			t.Fatalf("RecordAuditEvent(%s): %v", event.AuditID, err)
		}
	}
	reader := productionGovernanceAuditReader{store: store}
	first, err := reader.ListProposalAuditEvents(ctx, governanceview.ProposalAuditQuery{TenantID: "tenant-1", ProposalID: "proposal-1", Limit: 1})
	if err != nil || !first.HasMore || len(first.Events) != 1 || first.Events[0].AuditID != "audit-a" {
		t.Fatalf("first audit page = %+v, err=%v", first, err)
	}
	second, err := reader.ListProposalAuditEvents(ctx, governanceview.ProposalAuditQuery{
		TenantID: "tenant-1", ProposalID: "proposal-1", Limit: 1,
		After: governanceview.ProposalAuditSeek{OccurredAt: first.Events[0].OccurredAt, AuditID: first.Events[0].AuditID},
	})
	if err != nil || second.HasMore || len(second.Events) != 1 || second.Events[0].AuditID != "audit-b" {
		t.Fatalf("second audit page = %+v, err=%v", second, err)
	}
}

func TestProductionOntologyGovernanceReadAPIFailsClosedWithoutDependencies(t *testing.T) {
	if _, err := newProductionOntologyGovernanceReadAPI(nil, nil, nil, nil, nil, nil, nil, nil, nil, nil); err == nil {
		t.Fatal("missing dependencies returned nil error")
	}
}

func productionGovernanceProposal(id, tenantID string, status ontology.MetadataProposalStatus) ontology.OntologyMetadataProposal {
	return ontology.OntologyMetadataProposal{
		ID: id, TenantID: tenantID, TargetKind: "object_type", TargetID: "task", MetadataKey: "owner_domain",
		ProposedValue: "mission", Source: "deterministic.owner", SourceKind: ontology.MetadataProposalSourceDeterministic,
		Confidence: 1, Status: status, CreatedAt: time.Unix(10, 0).UTC(), UpdatedAt: time.Unix(10, 0).UTC(),
	}
}
