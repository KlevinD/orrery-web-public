package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyscope"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
	governanceview "orrery_v2/internal/app/ontologyview/governance"
	knowledgestore "orrery_v2/internal/knowledge/store"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/db"
)

type productionOntologyGovernanceCommandAPI struct {
	commands  *governanceview.CommandService
	core      *db.Store
	runner    mappingProjectionRefresher
	refreshes *ontology.PlatformMappingProjectionRefreshStore
	receipts  *ontology.PlatformMetadataProposalPromotionReceiptStore
	scopes    *productionOntologyScopeState
}

type mappingProjectionRefresher interface {
	RefreshProjections(context.Context, ontology.Registry) error
}

const (
	projectionRefreshNotRequired = "not_required"
	projectionRefreshSucceeded   = "succeeded"
	projectionRefreshPending     = "pending"
	projectionRefreshAbandoned   = "abandoned"
)

func newProductionOntologyGovernanceCommandAPI(
	core *db.Store,
	records *db.Router,
	scopes *productionOntologyScopeState,
	concurrency *ontologycontract.ConcurrencyLimiter,
	policy *datapolicy.PlatformStore,
	authorizer *authorization.Service,
	knowledge *knowledgestore.PlatformStore,
	memory *memstore.PlatformStore,
	runner *ontology.MappingRuleProjectionRunner,
) (*productionOntologyGovernanceCommandAPI, error) {
	if core == nil || records == nil || scopes == nil || scopes.Gate == nil || concurrency == nil || policy == nil || authorizer == nil || knowledge == nil || memory == nil || runner == nil {
		return nil, fmt.Errorf("ontology governance command API dependencies are required")
	}
	visibility, err := newProductionOntologyGovernanceVisibility(core, records, scopes, policy, authorizer, knowledge, memory)
	if err != nil {
		return nil, err
	}
	commands, err := governanceview.NewCommandService(governanceview.CommandServiceOptions{
		Gate: scopes.Gate, Proposals: ontology.NewPlatformMetadataProposalStore(core), Registry: ontology.NewPlatformRegistry(core),
		Plans: ontology.NewPlatformMetadataPromotionPlanStore(core), UnitOfWork: ontology.NewPlatformMetadataPromotionUnitOfWork(core),
		Policy: governanceview.NewVisibilityCommandPolicy(visibility), Concurrency: concurrency,
	})
	if err != nil {
		return nil, err
	}
	return &productionOntologyGovernanceCommandAPI{
		commands: commands, core: core, runner: runner, refreshes: ontology.NewPlatformMappingProjectionRefreshStore(core),
		receipts: ontology.NewPlatformMetadataProposalPromotionReceiptStore(core), scopes: scopes,
	}, nil
}

func (p *productionOntologyGovernanceCommandAPI) DecideProposal(ctx context.Context, principal identity.Principal, request governanceview.DecideProposalRequest) (ontology.OntologyMetadataProposal, error) {
	if p == nil || p.commands == nil {
		return ontology.OntologyMetadataProposal{}, fmt.Errorf("ontology governance command API is unavailable")
	}
	return p.commands.DecideProposal(ctx, principal, request)
}

func (p *productionOntologyGovernanceCommandAPI) IngestModelInferenceProposals(ctx context.Context, principal identity.Principal, request ontology.ModelInferenceMetadataProposalRequest) (ontology.MetadataProposalIngestResult, error) {
	if p == nil || p.core == nil || p.scopes == nil || p.scopes.Gate == nil {
		return ontology.MetadataProposalIngestResult{}, fmt.Errorf("ontology governance command API is unavailable")
	}
	if _, err := p.scopes.Gate.AuthorizeManage(ctx, principal, strings.TrimSpace(request.TenantID)); err != nil {
		return ontology.MetadataProposalIngestResult{}, err
	}
	return ontology.IngestModelInferenceMetadataProposalsWithLedger(
		ctx,
		ontology.NewPlatformMetadataProposalStore(p.core),
		ontology.NewPlatformModelRunLedger(p.core),
		request,
	)
}

func (p *productionOntologyGovernanceCommandAPI) CreatePromotionPlan(ctx context.Context, principal identity.Principal, request governanceview.CreatePromotionPlanRequest) (governanceview.PromotionPlanResult, error) {
	if p == nil || p.commands == nil {
		return governanceview.PromotionPlanResult{}, fmt.Errorf("ontology governance command API is unavailable")
	}
	return p.commands.CreatePromotionPlan(ctx, principal, request)
}

func (p *productionOntologyGovernanceCommandAPI) ApplyPromotionPlan(ctx context.Context, principal identity.Principal, request governanceview.ApplyPromotionPlanRequest) (ontology.MetadataProposalPromotionReceipt, error) {
	if p == nil || p.commands == nil || p.core == nil || p.runner == nil {
		return ontology.MetadataProposalPromotionReceipt{}, fmt.Errorf("ontology governance command API is unavailable")
	}
	receipt, err := p.commands.ApplyPromotionPlan(ctx, principal, request)
	if err != nil {
		return ontology.MetadataProposalPromotionReceipt{}, err
	}
	receipt.ProjectionRefreshStatus = projectionRefreshNotRequired
	if !ontology.MetadataPromotionRequiresMappingProjectionRefresh(receipt.Changes) {
		return receipt, nil
	}
	receipt.ProjectionRefreshStatus = p.deliverMappingProjectionRefresh(ctx, receipt)
	return receipt, nil
}

func (p *productionOntologyGovernanceCommandAPI) RetryProjectionRefresh(ctx context.Context, principal identity.Principal, request governanceview.RetryProjectionRefreshRequest) (governanceview.ProjectionRefreshResult, error) {
	request.TenantID = strings.TrimSpace(request.TenantID)
	request.ReceiptID = strings.TrimSpace(request.ReceiptID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if p == nil || p.scopes == nil || p.scopes.Gate == nil || p.receipts == nil || p.refreshes == nil || p.runner == nil || p.core == nil {
		return governanceview.ProjectionRefreshResult{}, fmt.Errorf("ontology governance command API is unavailable")
	}
	if request.TenantID == "" || request.ReceiptID == "" || request.IdempotencyKey == "" ||
		len(request.TenantID) > 256 || len(request.ReceiptID) > 256 || len(request.IdempotencyKey) > 256 || !utf8.ValidString(request.IdempotencyKey) {
		return governanceview.ProjectionRefreshResult{}, ontologycontract.ErrInvalidRequest
	}
	authorized, err := p.scopes.Gate.AuthorizeManage(ctx, principal, request.TenantID)
	if err != nil {
		return governanceview.ProjectionRefreshResult{}, err
	}
	receipt, err := p.receipts.GetPromotionReceiptForTenant(ctx, request.TenantID, request.ReceiptID)
	if errors.Is(err, ontology.ErrNotFound) {
		return governanceview.ProjectionRefreshResult{}, governanceview.ErrUnavailable
	}
	if err != nil {
		return governanceview.ProjectionRefreshResult{}, err
	}
	result := governanceview.ProjectionRefreshResult{ReceiptID: receipt.ReceiptID, ProposalID: receipt.ProposalID, Status: projectionRefreshNotRequired}
	if !ontology.MetadataPromotionRequiresMappingProjectionRefresh(receipt.Changes) {
		return result, nil
	}
	job, err := p.refreshes.GetMappingProjectionRefresh(ctx, receipt.ReceiptID)
	if errors.Is(err, ontology.ErrNotFound) {
		if err := p.refreshes.EnqueueMappingProjectionRefresh(ctx, receipt.ReceiptID, receipt.TenantID); err != nil {
			return governanceview.ProjectionRefreshResult{}, err
		}
		job, err = p.refreshes.GetMappingProjectionRefresh(ctx, receipt.ReceiptID)
	}
	if err != nil {
		return governanceview.ProjectionRefreshResult{}, err
	}
	if job.TenantID != request.TenantID {
		return governanceview.ProjectionRefreshResult{}, governanceview.ErrUnavailable
	}
	fresh, err := p.scopes.Gate.AuthorizeManage(ctx, principal, request.TenantID)
	if err != nil {
		return governanceview.ProjectionRefreshResult{}, err
	}
	if !sameProjectionRefreshAuthorization(authorized, fresh) {
		return governanceview.ProjectionRefreshResult{}, ontologyscope.ErrDependencyUnavailable
	}
	retryReceipt, err := p.refreshes.ExecuteMappingProjectionRefreshRetry(ctx, receipt.TenantID, receipt.ReceiptID, request.IdempotencyKey)
	if err != nil {
		return governanceview.ProjectionRefreshResult{}, err
	}
	result.Status = projectionRefreshStatus(retryReceipt.Status)
	result.IdempotentReplay = retryReceipt.IdempotentReplay
	if retryReceipt.Requeued {
		p.deliverMappingProjectionRefresh(ctx, receipt)
	}
	return result, nil
}

func sameProjectionRefreshAuthorization(left, right ontologyscope.AuthorizedScopeContext) bool {
	leftSnapshot, rightSnapshot := left.Snapshot(), right.Snapshot()
	return leftSnapshot.PrincipalKey == rightSnapshot.PrincipalKey &&
		leftSnapshot.AuthorizationRevision == rightSnapshot.AuthorizationRevision &&
		leftSnapshot.PolicyRevision == rightSnapshot.PolicyRevision &&
		left.Binding().TenantID == right.Binding().TenantID &&
		left.Binding().BindingVersion == right.Binding().BindingVersion
}

func projectionRefreshStatus(state ontology.MappingProjectionRefreshState) string {
	switch state {
	case ontology.MappingProjectionRefreshSucceeded:
		return projectionRefreshSucceeded
	case ontology.MappingProjectionRefreshAbandoned:
		return projectionRefreshAbandoned
	default:
		return projectionRefreshPending
	}
}

func metadataPromotionChangesRequireProjectionRefresh(changes []ontology.MetadataProposalPromotionChange) bool {
	return ontology.MetadataPromotionRequiresMappingProjectionRefresh(changes)
}

func (p *productionOntologyGovernanceCommandAPI) deliverMappingProjectionRefresh(ctx context.Context, receipt ontology.MetadataProposalPromotionReceipt) string {
	if p == nil || p.runner == nil || p.core == nil || p.refreshes == nil {
		return projectionRefreshPending
	}
	job, err := p.refreshes.GetMappingProjectionRefresh(ctx, receipt.ReceiptID)
	if errors.Is(err, ontology.ErrNotFound) {
		// Receipts created before the durable refresh outbox was introduced do
		// not have a job. Repair that gap on an idempotent apply retry.
		if err := p.refreshes.EnqueueMappingProjectionRefresh(ctx, receipt.ReceiptID, receipt.TenantID); err != nil {
			return projectionRefreshPending
		}
		job, err = p.refreshes.GetMappingProjectionRefresh(ctx, receipt.ReceiptID)
	}
	if err != nil {
		return projectionRefreshPending
	}
	if job.TenantID != receipt.TenantID {
		return projectionRefreshPending
	}
	if job.State == ontology.MappingProjectionRefreshSucceeded {
		return projectionRefreshSucceeded
	}
	if job.State == ontology.MappingProjectionRefreshAbandoned {
		return projectionRefreshAbandoned
	}
	now := time.Now().UTC()
	if job.NextAttemptAt.After(now) {
		return projectionRefreshPending
	}
	refreshCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err := p.runner.RefreshProjections(refreshCtx, ontology.NewPlatformRegistry(p.core)); err != nil {
		recordErr := p.refreshes.RecordMappingProjectionRefreshFailure(
			refreshCtx, receipt.ReceiptID, "projection_refresh_failed", job.Attempts,
			mappingProjectionRefreshNextAttemptAt(job.Attempts, now), productionMappingProjectionRefreshMaxAttempts,
		)
		if recordErr == nil && job.Attempts+1 >= productionMappingProjectionRefreshMaxAttempts {
			return projectionRefreshAbandoned
		}
		return projectionRefreshPending
	}
	if err := p.refreshes.CompleteMappingProjectionRefresh(refreshCtx, receipt.ReceiptID); err != nil {
		completed, getErr := p.refreshes.GetMappingProjectionRefresh(refreshCtx, receipt.ReceiptID)
		if getErr != nil || completed.State != ontology.MappingProjectionRefreshSucceeded {
			return projectionRefreshPending
		}
	}
	return projectionRefreshSucceeded
}
