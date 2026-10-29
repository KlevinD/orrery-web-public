package main

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/datavisibility"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/knowledgeadmin"
	"orrery_v2/internal/app/ontology"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	governanceview "orrery_v2/internal/app/ontologyview/governance"
	"orrery_v2/internal/app/operations"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/db"
)

type productionGovernanceAuditReader struct {
	store *operations.AuditStore
}

type productionOntologyGovernanceReadService struct {
	*governanceview.Service
	refreshes *ontology.PlatformMappingProjectionRefreshStore
	scopes    *productionOntologyScopeState
}

func (s *productionOntologyGovernanceReadService) ListReceipts(ctx context.Context, principal identity.Principal, query governanceview.ReceiptListQuery) (ontologycontract.CursorPage[governanceview.ReceiptItem], error) {
	if s == nil || s.Service == nil || s.refreshes == nil || s.scopes == nil || s.scopes.Gate == nil {
		return ontologycontract.CursorPage[governanceview.ReceiptItem]{}, fmt.Errorf("ontology governance read service dependencies are required")
	}
	page, err := s.Service.ListReceipts(ctx, principal, query)
	if err != nil || len(page.Items) == 0 {
		return page, err
	}
	receiptIDs := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		receiptIDs = append(receiptIDs, item.ReceiptID)
	}
	refreshes, err := s.refreshes.ListMappingProjectionRefreshes(ctx, receiptIDs)
	if err != nil {
		return ontologycontract.CursorPage[governanceview.ReceiptItem]{}, err
	}
	for index := range page.Items {
		// Refresh state follows the receipt status visibility decision. A masked
		// receipt status must not be enriched with additional operational state.
		if page.Items[index].Status == "" {
			continue
		}
		if refresh, ok := refreshes[page.Items[index].ReceiptID]; ok {
			page.Items[index].ProjectionRefreshStatus = refresh.State
		}
	}
	if _, err := s.scopes.Gate.AuthorizeRead(ctx, principal, query.TenantID); err != nil {
		return ontologycontract.CursorPage[governanceview.ReceiptItem]{}, err
	}
	return page, nil
}

func (r productionGovernanceAuditReader) ListProposalAuditEvents(ctx context.Context, query governanceview.ProposalAuditQuery) (governanceview.ProposalAuditPage, error) {
	if r.store == nil {
		return governanceview.ProposalAuditPage{}, fmt.Errorf("governance audit store is required")
	}
	page, err := r.store.ListAuditEventCursorPage(ctx, operations.AuditEventCursorQuery{
		EntityType: "ontology_metadata_proposal",
		EntityID:   strings.TrimSpace(query.ProposalID),
		MetadataEquals: map[string]string{
			"tenant_id": strings.TrimSpace(query.TenantID),
		},
		Limit: query.Limit,
		After: operations.AuditEventSeek{OccurredAt: query.After.OccurredAt, AuditID: query.After.AuditID},
	})
	if err != nil {
		return governanceview.ProposalAuditPage{}, err
	}
	events := make([]governanceview.ProposalAuditRecord, 0, len(page.Events))
	for _, event := range page.Events {
		actorKind := strings.TrimSpace(event.Metadata["actor_kind"])
		if actorKind == "" {
			actorKind = strings.TrimSpace(event.Metadata["request_actor_kind"])
		}
		events = append(events, governanceview.ProposalAuditRecord{
			AuditID: event.AuditID, TenantID: query.TenantID, ProposalID: query.ProposalID,
			Action: event.Action, Result: event.Result, Severity: event.Severity,
			ActorID: event.ActorID, ActorKind: actorKind, Summary: event.Summary, OccurredAt: event.OccurredAt,
		})
	}
	return governanceview.ProposalAuditPage{Events: events, HasMore: page.HasMore}, nil
}

func newProductionOntologyGovernanceReadAPI(
	core *db.Store,
	records *db.Router,
	scopes *productionOntologyScopeState,
	cursors *productionOntologyCursorState,
	concurrency *ontologycontract.ConcurrencyLimiter,
	policy *datapolicy.PlatformStore,
	authorizer *authorization.Service,
	knowledge *knowledgestore.PlatformStore,
	memory *memstore.PlatformStore,
	audit *operations.AuditStore,
) (*productionOntologyGovernanceReadService, error) {
	if core == nil || records == nil || scopes == nil || scopes.Gate == nil || scopes.Resolver == nil || cursors == nil || cursors.Stateful == nil ||
		concurrency == nil || policy == nil || authorizer == nil || knowledge == nil || memory == nil || audit == nil {
		return nil, fmt.Errorf("ontology governance read API dependencies are required")
	}
	visibility, err := newProductionOntologyGovernanceVisibility(core, records, scopes, policy, authorizer, knowledge, memory)
	if err != nil {
		return nil, err
	}
	service, err := governanceview.NewService(
		scopes.Gate,
		ontology.NewPlatformMetadataProposalStore(core),
		ontology.NewPlatformMetadataProposalPromotionReceiptStore(core),
		productionGovernanceAuditReader{store: audit},
		visibility,
		governanceview.CursorSet{Encrypted: cursors.Encrypted, Stateful: cursors.Stateful},
		nil,
		concurrency,
	)
	if err != nil {
		return nil, err
	}
	return &productionOntologyGovernanceReadService{
		Service:   service,
		refreshes: ontology.NewPlatformMappingProjectionRefreshStore(core),
		scopes:    scopes,
	}, nil
}

func newProductionOntologyGovernanceVisibility(
	core *db.Store,
	records *db.Router,
	scopes *productionOntologyScopeState,
	policy *datapolicy.PlatformStore,
	authorizer *authorization.Service,
	knowledge *knowledgestore.PlatformStore,
	memory *memstore.PlatformStore,
) (*datavisibility.Service, error) {
	if core == nil || records == nil || scopes == nil || scopes.Gate == nil || scopes.Resolver == nil || policy == nil || authorizer == nil || knowledge == nil || memory == nil {
		return nil, fmt.Errorf("ontology governance visibility dependencies are required")
	}
	access := productionContextAccessPolicy{
		scopes: scopes, authorizer: authorizer, knowledge: knowledge, memory: memory,
		knowledgeACL: knowledgeadmin.NewAccessPolicy(knowledge), records: records, actions: actioncontract.NewPlatformRepository(core), snapshots: scopes.Snapshots,
	}
	data := productionContextDataPolicy{provider: policy, revisions: authorization.NewPolicyRevisionSource(policy)}
	registrations := make([]datavisibility.Registration, 0, 8)
	for _, kind := range []datavisibility.ResourceKind{
		datavisibility.ResourceProposal, datavisibility.ResourceReceipt,
		datavisibility.ResourceAudit, datavisibility.ResourceEvidence,
	} {
		registrations = append(registrations, datavisibility.Register(kind, access), datavisibility.Register(kind, data))
	}
	registrations = append(registrations,
		datavisibility.Register(datavisibility.ResourceDefinition, productionSchemaDefinitionPolicy{}),
		datavisibility.Register(datavisibility.ResourceDefinition, productionSchemaDataPolicy{provider: policy, revisions: authorization.NewPolicyRevisionSource(policy)}),
	)
	return datavisibility.NewService(registrations...), nil
}
