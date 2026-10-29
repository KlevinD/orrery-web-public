package ontology

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/platform/graphprotocol"
)

type PromotionOperation string
type PromotionIssueCode string
type PromotionReceiptStatus string
type PromotionReceiptProposalBindingStatus string

const (
	PromotionOperationCreate PromotionOperation = "create"
	PromotionOperationAppend PromotionOperation = "append"
	PromotionOperationUpdate PromotionOperation = "update"
	PromotionOperationNoop   PromotionOperation = "noop"
)

const (
	PromotionReceiptProposalBindingResolved         PromotionReceiptProposalBindingStatus = "resolved"
	PromotionReceiptProposalBindingLegacyUnresolved PromotionReceiptProposalBindingStatus = "legacy-unresolved"
)

const (
	PromotionIssueProposalNotAccepted     PromotionIssueCode = "proposal_not_accepted"
	PromotionIssueUnsupportedTarget       PromotionIssueCode = "unsupported_target"
	PromotionIssueUnsupportedTargetField  PromotionIssueCode = "unsupported_target_field"
	PromotionIssueTargetNotFound          PromotionIssueCode = "target_not_found"
	PromotionIssueInvalidProposal         PromotionIssueCode = "invalid_proposal"
	PromotionIssueBlockingAnalysis        PromotionIssueCode = "blocking_analysis_issue"
	PromotionIssueRegistryVersionConflict PromotionIssueCode = "registry_version_conflict"
)

const (
	PromotionReceiptStatusApplied PromotionReceiptStatus = "applied"
	PromotionReceiptStatusNoop    PromotionReceiptStatus = "noop"
	PromotionReceiptStatusBlocked PromotionReceiptStatus = "blocked"
	PromotionReceiptStatusFailed  PromotionReceiptStatus = "failed"
)

type MetadataProposalPromotionReceiptStore interface {
	SavePromotionReceipt(context.Context, MetadataProposalPromotionReceipt) error
	GetPromotionReceipt(context.Context, string) (MetadataProposalPromotionReceipt, error)
	GetPromotionReceiptByIdempotencyKey(context.Context, string, string, string) (MetadataProposalPromotionReceipt, error)
	ListPromotionReceipts(context.Context, PromotionReceiptQuery) (PromotionReceiptPage, error)
}

type versionedRegistry interface {
	SaveObjectTypeWithExpectedVersion(context.Context, ObjectType, int) (ObjectType, error)
	SaveLinkTypeWithExpectedVersion(context.Context, LinkType, int) (LinkType, error)
	SavePropertyTypeWithExpectedVersion(context.Context, PropertyType, int) (PropertyType, error)
	SaveMappingRuleWithExpectedVersion(context.Context, MappingRule, int) (MappingRule, error)
}

type MetadataProposalPromotionDryRunRequest struct {
	Registry Registry
	Proposal OntologyMetadataProposal
}

type MetadataProposalPromotionApplyRequest struct {
	Registry                Registry                              `json:"-"`
	ReceiptStore            MetadataProposalPromotionReceiptStore `json:"-"`
	Proposal                OntologyMetadataProposal              `json:"-"`
	TenantID                string                                `json:"tenant_id,omitempty"`
	ActorID                 string                                `json:"actor_id"`
	ActorKind               string                                `json:"actor_kind,omitempty"`
	IdempotencyKey          string                                `json:"idempotency_key"`
	ConfirmationToken       string                                `json:"confirmation_token,omitempty"`
	RegistryExpectedVersion int                                   `json:"registry_expected_version,omitempty"`
}

type MetadataProposalPromotionBatchDryRunRequest struct {
	Registry    Registry                   `json:"-"`
	Proposals   []OntologyMetadataProposal `json:"-"`
	TenantID    string                     `json:"tenant_id,omitempty"`
	ProposalIDs []string                   `json:"proposal_ids,omitempty"`
}

type MetadataProposalPromotionBatchApplyRequest struct {
	Registry          Registry                              `json:"-"`
	ReceiptStore      MetadataProposalPromotionReceiptStore `json:"-"`
	Proposals         []OntologyMetadataProposal            `json:"-"`
	TenantID          string                                `json:"tenant_id,omitempty"`
	ProposalIDs       []string                              `json:"proposal_ids"`
	ActorID           string                                `json:"actor_id"`
	ActorKind         string                                `json:"actor_kind,omitempty"`
	IdempotencyKey    string                                `json:"idempotency_key"`
	ConfirmationToken string                                `json:"confirmation_token,omitempty"`
	ExpectedVersions  map[string]int                        `json:"expected_versions,omitempty"`
}

type MetadataProposalPromotionPlan struct {
	ProposalID string                            `json:"proposal_id,omitempty"`
	CanApply   bool                              `json:"can_apply"`
	Changes    []MetadataProposalPromotionChange `json:"changes,omitempty"`
	Issues     []MetadataProposalPromotionIssue  `json:"issues,omitempty"`
}

type MetadataProposalPromotionBatchPlan struct {
	CanApply bool                              `json:"can_apply"`
	Plans    []MetadataProposalPromotionPlan   `json:"plans"`
	Changes  []MetadataProposalPromotionChange `json:"changes,omitempty"`
	Issues   []MetadataProposalPromotionIssue  `json:"issues,omitempty"`
}

type MetadataProposalPromotionBatchReceipt struct {
	ReceiptID      string                             `json:"receipt_id"`
	TenantID       string                             `json:"tenant_id,omitempty"`
	ProposalIDs    []string                           `json:"proposal_ids"`
	ActorID        string                             `json:"actor_id"`
	ActorKind      string                             `json:"actor_kind,omitempty"`
	IdempotencyKey string                             `json:"idempotency_key"`
	Status         PromotionReceiptStatus             `json:"status"`
	Applied        bool                               `json:"applied"`
	Receipts       []MetadataProposalPromotionReceipt `json:"receipts,omitempty"`
	Changes        []MetadataProposalPromotionChange  `json:"changes,omitempty"`
	Issues         []MetadataProposalPromotionIssue   `json:"issues,omitempty"`
	ErrorMessage   string                             `json:"error_message,omitempty"`
	AppliedAt      time.Time                          `json:"applied_at"`
}

type MetadataProposalPromotionChange struct {
	Operation       PromotionOperation `json:"operation"`
	TargetKind      string             `json:"target_kind"`
	TargetID        string             `json:"target_id"`
	Field           string             `json:"field"`
	CurrentValue    string             `json:"current_value,omitempty"`
	ProposedValue   string             `json:"proposed_value,omitempty"`
	ExpectedVersion int                `json:"expected_version,omitempty"`
	CurrentVersion  int                `json:"current_version,omitempty"`
	AppliedVersion  int                `json:"applied_version,omitempty"`
}

type MetadataProposalPromotionIssue struct {
	Code    PromotionIssueCode `json:"code"`
	Message string             `json:"message"`
}

type MetadataProposalPromotionReceipt struct {
	ReceiptID               string                                `json:"receipt_id"`
	TenantID                string                                `json:"tenant_id,omitempty"`
	ProposalID              string                                `json:"proposal_id"`
	ProposalRevision        int64                                 `json:"proposal_revision"`
	ProposalContentHash     string                                `json:"proposal_content_hash"`
	ProposalBindingStatus   PromotionReceiptProposalBindingStatus `json:"proposal_binding_status"`
	ActorID                 string                                `json:"actor_id"`
	ActorKind               string                                `json:"actor_kind,omitempty"`
	IdempotencyKey          string                                `json:"idempotency_key"`
	RequestHash             string                                `json:"request_hash,omitempty"`
	Status                  PromotionReceiptStatus                `json:"status"`
	Applied                 bool                                  `json:"applied"`
	Changes                 []MetadataProposalPromotionChange     `json:"changes,omitempty"`
	Issues                  []MetadataProposalPromotionIssue      `json:"issues,omitempty"`
	ErrorMessage            string                                `json:"error_message,omitempty"`
	AppliedAt               time.Time                             `json:"applied_at"`
	CreatedAt               time.Time                             `json:"created_at,omitempty"`
	UpdatedAt               time.Time                             `json:"updated_at,omitempty"`
	ProjectionRefreshStatus string                                `json:"-"`
}

type PromotionReceiptQuery struct {
	TenantID       string
	ProposalID     string
	ActorID        string
	Status         PromotionReceiptStatus
	IdempotencyKey string
	Limit          int
	Offset         int
}

type PromotionReceiptPage struct {
	Receipts []MetadataProposalPromotionReceipt `json:"receipts"`
	Page     Page                               `json:"page"`
}

func DryRunMetadataProposalPromotion(ctx context.Context, req MetadataProposalPromotionDryRunRequest) (MetadataProposalPromotionPlan, error) {
	if err := ctx.Err(); err != nil {
		return MetadataProposalPromotionPlan{}, err
	}
	proposal, err := normalizeMetadataProposal(req.Proposal, nil)
	if err != nil {
		return MetadataProposalPromotionPlan{}, err
	}
	plan := MetadataProposalPromotionPlan{ProposalID: proposal.ID}
	if proposal.Status != MetadataProposalStatusAccepted {
		plan.addIssue(PromotionIssueProposalNotAccepted, "metadata proposal must be accepted before promotion dry-run can apply")
		return plan, nil
	}
	if issue, ok := blockingMetadataProposalPromotionAnalysisIssue(proposal); ok {
		plan.addIssue(PromotionIssueBlockingAnalysis, fmt.Sprintf("metadata proposal has blocking analysis issue %s: %s", issue.Code, issue.Message))
		return plan, nil
	}
	if req.Registry == nil {
		plan.addIssue(PromotionIssueInvalidProposal, "ontology registry is required")
		return plan, nil
	}

	switch proposal.TargetKind {
	case "object_type":
		planObjectTypePromotion(ctx, req.Registry, proposal, &plan)
	case "link_type":
		planLinkTypePromotion(ctx, req.Registry, proposal, &plan)
	case "property_type":
		planPropertyTypePromotion(ctx, req.Registry, proposal, &plan)
	case "mapping_rule":
		planMappingRulePromotion(ctx, req.Registry, proposal, &plan)
	default:
		plan.addIssue(PromotionIssueUnsupportedTarget, fmt.Sprintf("metadata proposal target kind %q is not supported", proposal.TargetKind))
	}
	plan.CanApply = len(plan.Issues) == 0 && len(plan.Changes) > 0
	return plan, nil
}

func blockingMetadataProposalPromotionAnalysisIssue(proposal OntologyMetadataProposal) (MetadataProposalIssue, bool) {
	proposed := proposal
	proposed.Status = MetadataProposalStatusProposed
	return firstBlockingMetadataProposalIssueForProposal(proposed)
}

func DryRunMetadataProposalPromotionBatch(ctx context.Context, req MetadataProposalPromotionBatchDryRunRequest) (MetadataProposalPromotionBatchPlan, error) {
	if err := ctx.Err(); err != nil {
		return MetadataProposalPromotionBatchPlan{}, err
	}
	proposals, err := normalizePromotionBatchProposals(req.Proposals)
	if err != nil {
		return MetadataProposalPromotionBatchPlan{}, err
	}
	proposals = orderPromotionBatchProposals(proposals)
	if len(proposals) == 0 {
		return MetadataProposalPromotionBatchPlan{}, fmt.Errorf("%w: proposal_ids are required", ErrInvalidDefinition)
	}
	dryRunRegistry := req.Registry
	if overlay, err := newPromotionOverlayRegistry(ctx, req.Registry); err != nil {
		return MetadataProposalPromotionBatchPlan{}, err
	} else if overlay != nil {
		dryRunRegistry = overlay
	}
	batch := MetadataProposalPromotionBatchPlan{CanApply: true}
	targets := map[string]string{}
	for _, proposal := range proposals {
		plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{
			Registry: dryRunRegistry,
			Proposal: proposal,
		})
		if err != nil {
			return MetadataProposalPromotionBatchPlan{}, err
		}
		for _, change := range plan.Changes {
			targetKey := promotionChangeTargetKey(change)
			if previousProposalID, ok := targets[targetKey]; ok {
				plan.addIssue(PromotionIssueInvalidProposal, fmt.Sprintf("proposal %s targets the same metadata field as proposal %s", proposal.ID, previousProposalID))
			} else {
				targets[targetKey] = proposal.ID
			}
		}
		if len(plan.Issues) > 0 {
			plan.CanApply = false
		}
		if plan.CanApply && len(plan.Issues) == 0 {
			if err := applyPromotionPlanToOverlay(ctx, dryRunRegistry, proposal.TenantID, plan); err != nil {
				plan.addIssue(PromotionIssueInvalidProposal, err.Error())
				plan.CanApply = false
			}
		}
		batch.Plans = append(batch.Plans, plan)
		batch.Changes = append(batch.Changes, plan.Changes...)
		batch.Issues = append(batch.Issues, plan.Issues...)
		if !plan.CanApply {
			batch.CanApply = false
		}
	}
	if len(batch.Plans) == 0 || len(batch.Issues) > 0 || len(batch.Changes) == 0 {
		batch.CanApply = false
	}
	return batch, nil
}

func ApplyMetadataProposalPromotion(ctx context.Context, req MetadataProposalPromotionApplyRequest) (MetadataProposalPromotionReceipt, error) {
	if err := ctx.Err(); err != nil {
		return MetadataProposalPromotionReceipt{}, err
	}
	actorID := strings.TrimSpace(req.ActorID)
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if actorID == "" {
		return MetadataProposalPromotionReceipt{}, fmt.Errorf("%w: promotion actor_id is required", ErrInvalidDefinition)
	}
	if idempotencyKey == "" {
		return MetadataProposalPromotionReceipt{}, fmt.Errorf("%w: promotion idempotency_key is required", ErrInvalidDefinition)
	}
	requestTenantID := strings.TrimSpace(req.TenantID)
	proposal, err := normalizeMetadataProposal(req.Proposal, nil)
	if err != nil {
		return MetadataProposalPromotionReceipt{}, err
	}
	if requestTenantID != "" && strings.TrimSpace(proposal.TenantID) != "" && strings.TrimSpace(proposal.TenantID) != requestTenantID {
		return MetadataProposalPromotionReceipt{}, fmt.Errorf("%w: ontology metadata proposal %s", ErrNotFound, proposal.ID)
	}
	requestHash := metadataPromotionRequestHash(req, proposal, requestTenantID, actorID)
	if req.ReceiptStore != nil {
		existing, err := req.ReceiptStore.GetPromotionReceiptByIdempotencyKey(ctx, requestTenantID, proposal.ID, idempotencyKey)
		if err == nil {
			if existing.RequestHash != "" && requestHash != "" && existing.RequestHash != requestHash {
				legacyHash := legacyMetadataPromotionRequestHash(req, proposal, requestTenantID, actorID)
				if existing.ProposalBindingStatus != PromotionReceiptProposalBindingLegacyUnresolved || existing.RequestHash != legacyHash {
					return MetadataProposalPromotionReceipt{}, fmt.Errorf("%w: promotion receipt for proposal %s and idempotency key has different request fingerprint", ErrIdempotencyConflict, proposal.ID)
				}
			}
			return existing, nil
		}
		if !isOntologyNotFound(err) {
			return MetadataProposalPromotionReceipt{}, err
		}
	}
	plan, err := DryRunMetadataProposalPromotion(ctx, MetadataProposalPromotionDryRunRequest{
		Registry: req.Registry,
		Proposal: proposal,
	})
	if err != nil {
		return MetadataProposalPromotionReceipt{}, err
	}
	if err := validateMetadataPromotionConfirmationToken(req.ConfirmationToken, MetadataProposalPromotionConfirmationToken(plan)); err != nil {
		return MetadataProposalPromotionReceipt{}, err
	}
	applyRegistryExpectedVersion(&plan, req.RegistryExpectedVersion)
	receipt := MetadataProposalPromotionReceipt{
		ReceiptID:             metadataPromotionReceiptID(plan.ProposalID, idempotencyKey),
		TenantID:              proposal.TenantID,
		ProposalID:            plan.ProposalID,
		ProposalRevision:      proposal.Revision,
		ProposalContentHash:   proposal.ContentHash,
		ProposalBindingStatus: PromotionReceiptProposalBindingResolved,
		ActorID:               actorID,
		ActorKind:             strings.TrimSpace(req.ActorKind),
		IdempotencyKey:        idempotencyKey,
		RequestHash:           requestHash,
		Changes:               append([]MetadataProposalPromotionChange(nil), plan.Changes...),
		Issues:                append([]MetadataProposalPromotionIssue(nil), plan.Issues...),
		AppliedAt:             time.Now().UTC(),
	}
	if !plan.CanApply || len(plan.Issues) > 0 {
		receipt.Status = PromotionReceiptStatusBlocked
		receipt.ErrorMessage = "metadata proposal promotion dry-run is not clean"
		if err := savePromotionReceiptIfConfigured(ctx, req.ReceiptStore, receipt); err != nil {
			return receipt, err
		}
		if promotionPlanHasIssue(plan, PromotionIssueRegistryVersionConflict) {
			return receipt, fmt.Errorf("%w: %s", ErrVersionConflict, receipt.ErrorMessage)
		}
		return receipt, fmt.Errorf("%w: %s", ErrInvalidDefinition, receipt.ErrorMessage)
	}
	for i, change := range plan.Changes {
		appliedChange, applied, err := applyMetadataProposalPromotionChange(ctx, req.Registry, proposal.TenantID, change)
		receipt.Changes[i] = appliedChange
		if err != nil {
			receipt.Status = PromotionReceiptStatusFailed
			receipt.ErrorMessage = err.Error()
			if saveErr := savePromotionReceiptIfConfigured(ctx, req.ReceiptStore, receipt); saveErr != nil {
				return receipt, saveErr
			}
			return receipt, err
		}
		if applied {
			receipt.Applied = true
		}
	}
	if receipt.Applied {
		receipt.Status = PromotionReceiptStatusApplied
	} else {
		receipt.Status = PromotionReceiptStatusNoop
	}
	if err := savePromotionReceiptIfConfigured(ctx, req.ReceiptStore, receipt); err != nil {
		return receipt, err
	}
	return receipt, nil
}

func ApplyMetadataProposalPromotionBatch(ctx context.Context, req MetadataProposalPromotionBatchApplyRequest) (MetadataProposalPromotionBatchReceipt, error) {
	if err := ctx.Err(); err != nil {
		return MetadataProposalPromotionBatchReceipt{}, err
	}
	actorID := strings.TrimSpace(req.ActorID)
	idempotencyKey := strings.TrimSpace(req.IdempotencyKey)
	if actorID == "" {
		return MetadataProposalPromotionBatchReceipt{}, fmt.Errorf("%w: promotion actor_id is required", ErrInvalidDefinition)
	}
	if idempotencyKey == "" {
		return MetadataProposalPromotionBatchReceipt{}, fmt.Errorf("%w: promotion idempotency_key is required", ErrInvalidDefinition)
	}
	proposals, err := normalizePromotionBatchProposals(req.Proposals)
	if err != nil {
		return MetadataProposalPromotionBatchReceipt{}, err
	}
	proposals = orderPromotionBatchProposals(proposals)
	if len(proposals) == 0 {
		return MetadataProposalPromotionBatchReceipt{}, fmt.Errorf("%w: proposal_ids are required", ErrInvalidDefinition)
	}
	batch := MetadataProposalPromotionBatchReceipt{
		ReceiptID:      metadataPromotionBatchReceiptID(proposals, idempotencyKey),
		TenantID:       strings.TrimSpace(req.TenantID),
		ProposalIDs:    promotionBatchProposalIDs(proposals),
		ActorID:        actorID,
		ActorKind:      strings.TrimSpace(req.ActorKind),
		IdempotencyKey: idempotencyKey,
		AppliedAt:      time.Now().UTC(),
	}
	batchPlan, err := DryRunMetadataProposalPromotionBatch(ctx, MetadataProposalPromotionBatchDryRunRequest{
		Registry:  req.Registry,
		Proposals: proposals,
	})
	if err != nil {
		return MetadataProposalPromotionBatchReceipt{}, err
	}
	if err := validateMetadataPromotionConfirmationToken(req.ConfirmationToken, MetadataProposalPromotionBatchConfirmationToken(batchPlan)); err != nil {
		return MetadataProposalPromotionBatchReceipt{}, err
	}
	batchPlan.CanApply = true
	batchPlan.Changes = nil
	batchPlan.Issues = nil
	for i := range batchPlan.Plans {
		applyRegistryExpectedVersion(&batchPlan.Plans[i], req.ExpectedVersions[batchPlan.Plans[i].ProposalID])
		batchPlan.Issues = append(batchPlan.Issues, batchPlan.Plans[i].Issues...)
		batchPlan.Changes = append(batchPlan.Changes, batchPlan.Plans[i].Changes...)
		if !batchPlan.Plans[i].CanApply || len(batchPlan.Plans[i].Issues) > 0 {
			batchPlan.CanApply = false
		}
	}
	if len(batchPlan.Plans) == 0 || len(batchPlan.Changes) == 0 {
		batchPlan.CanApply = false
	}
	batch.Changes = append([]MetadataProposalPromotionChange(nil), batchPlan.Changes...)
	batch.Issues = append([]MetadataProposalPromotionIssue(nil), batchPlan.Issues...)
	if !batchPlan.CanApply || len(batchPlan.Issues) > 0 {
		batch.Status = PromotionReceiptStatusBlocked
		batch.ErrorMessage = "metadata proposal promotion batch dry-run is not clean"
		if promotionBatchHasIssue(batch, PromotionIssueRegistryVersionConflict) {
			return batch, fmt.Errorf("%w: %s", ErrVersionConflict, batch.ErrorMessage)
		}
		return batch, fmt.Errorf("%w: %s", ErrInvalidDefinition, batch.ErrorMessage)
	}
	for i, proposal := range proposals {
		receipt, err := ApplyMetadataProposalPromotion(ctx, MetadataProposalPromotionApplyRequest{
			Registry:                req.Registry,
			ReceiptStore:            req.ReceiptStore,
			Proposal:                proposal,
			TenantID:                req.TenantID,
			ActorID:                 actorID,
			ActorKind:               req.ActorKind,
			IdempotencyKey:          idempotencyKey + ":" + proposal.ID,
			ConfirmationToken:       MetadataProposalPromotionConfirmationToken(batchPlan.Plans[i]),
			RegistryExpectedVersion: req.ExpectedVersions[proposal.ID],
		})
		if err != nil {
			batch.Status = PromotionReceiptStatusFailed
			batch.ErrorMessage = err.Error()
			return batch, err
		}
		batch.Receipts = append(batch.Receipts, receipt)
		if batch.TenantID == "" {
			batch.TenantID = strings.TrimSpace(receipt.TenantID)
		}
		if receipt.Applied {
			batch.Applied = true
		}
		if i < len(batchPlan.Plans) {
			batchPlan.Plans[i].Changes = receipt.Changes
		}
	}
	batch.Changes = nil
	for _, receipt := range batch.Receipts {
		batch.Changes = append(batch.Changes, receipt.Changes...)
	}
	if batch.Applied {
		batch.Status = PromotionReceiptStatusApplied
	} else {
		batch.Status = PromotionReceiptStatusNoop
	}
	return batch, nil
}

func MetadataProposalPromotionConfirmationToken(plan MetadataProposalPromotionPlan) string {
	return fmt.Sprintf("apply:%s:%d:%d", strings.TrimSpace(plan.ProposalID), len(plan.Changes), len(plan.Issues))
}

func MetadataProposalPromotionBatchConfirmationToken(plan MetadataProposalPromotionBatchPlan) string {
	proposalIDs := make([]string, 0, len(plan.Plans))
	for _, childPlan := range plan.Plans {
		proposalID := strings.TrimSpace(childPlan.ProposalID)
		if proposalID != "" {
			proposalIDs = append(proposalIDs, proposalID)
		}
	}
	return fmt.Sprintf("batch:%s:%d:%d", strings.Join(proposalIDs, ","), len(plan.Changes), len(plan.Issues))
}

func validateMetadataPromotionConfirmationToken(actual string, expected string) error {
	actual = strings.TrimSpace(actual)
	expected = strings.TrimSpace(expected)
	if expected == "" {
		return fmt.Errorf("%w: promotion confirmation token is not available", ErrInvalidDefinition)
	}
	if actual == "" {
		return fmt.Errorf("%w: promotion confirmation token is required", ErrInvalidDefinition)
	}
	if actual != expected {
		return fmt.Errorf("%w: promotion confirmation token does not match current dry-run", ErrInvalidDefinition)
	}
	return nil
}

func planObjectTypePromotion(ctx context.Context, registry Registry, proposal OntologyMetadataProposal, plan *MetadataProposalPromotionPlan) {
	if proposal.MetadataKey == "definition" {
		planObjectTypeDefinitionPromotion(ctx, registry, proposal, plan)
		return
	}
	objectType, err := registry.GetObjectType(ctx, proposal.TargetID)
	if err != nil {
		plan.addIssue(PromotionIssueTargetNotFound, err.Error())
		return
	}
	switch proposal.MetadataKey {
	case "allowed_action":
		planAllowedActionPromotion("object_type", objectType.ID, objectType.AllowedActions, objectType.Version, proposal, plan)
	default:
		plan.addIssue(PromotionIssueUnsupportedTargetField, fmt.Sprintf("object_type metadata key %q cannot be promoted yet", proposal.MetadataKey))
	}
}

func planObjectTypeDefinitionPromotion(ctx context.Context, registry Registry, proposal OntologyMetadataProposal, plan *MetadataProposalPromotionPlan) {
	definition, canonical, err := objectTypeDefinitionFromProposal(ctx, registry, proposal)
	if err != nil {
		plan.addIssue(PromotionIssueInvalidProposal, err.Error())
		return
	}
	current, err := registry.GetObjectType(ctx, definition.ID)
	if err != nil {
		if !isOntologyNotFound(err) {
			plan.addIssue(PromotionIssueInvalidProposal, err.Error())
			return
		}
		plan.Changes = append(plan.Changes, MetadataProposalPromotionChange{
			Operation:     PromotionOperationCreate,
			TargetKind:    "object_type",
			TargetID:      definition.ID,
			Field:         "definition",
			ProposedValue: canonical,
		})
		return
	}
	currentCanonical, err := canonicalObjectTypeDefinitionValue(current)
	if err != nil {
		plan.addIssue(PromotionIssueInvalidProposal, err.Error())
		return
	}
	operation := PromotionOperationUpdate
	if currentCanonical == canonical {
		operation = PromotionOperationNoop
	}
	plan.Changes = append(plan.Changes, MetadataProposalPromotionChange{
		Operation:       operation,
		TargetKind:      "object_type",
		TargetID:        definition.ID,
		Field:           "definition",
		CurrentValue:    currentCanonical,
		ProposedValue:   canonical,
		ExpectedVersion: current.Version,
		CurrentVersion:  current.Version,
	})
}

func planLinkTypePromotion(ctx context.Context, registry Registry, proposal OntologyMetadataProposal, plan *MetadataProposalPromotionPlan) {
	if proposal.MetadataKey == "definition" {
		planLinkTypeDefinitionPromotion(ctx, registry, proposal, plan)
		return
	}
	linkType, err := registry.GetLinkType(ctx, proposal.TargetID)
	if err != nil {
		plan.addIssue(PromotionIssueTargetNotFound, err.Error())
		return
	}
	switch proposal.MetadataKey {
	case "allowed_action":
		planAllowedActionPromotion("link_type", linkType.ID, linkType.AllowedActions, linkType.Version, proposal, plan)
	default:
		plan.addIssue(PromotionIssueUnsupportedTargetField, fmt.Sprintf("link_type metadata key %q cannot be promoted yet", proposal.MetadataKey))
	}
}

func planLinkTypeDefinitionPromotion(ctx context.Context, registry Registry, proposal OntologyMetadataProposal, plan *MetadataProposalPromotionPlan) {
	definition, canonical, err := linkTypeDefinitionFromProposal(ctx, registry, proposal)
	if err != nil {
		plan.addIssue(PromotionIssueInvalidProposal, err.Error())
		return
	}
	if err := validateLinkTypeDefinitionEndpoints(ctx, registry, definition); err != nil {
		plan.addIssue(PromotionIssueInvalidProposal, err.Error())
		return
	}
	current, err := registry.GetLinkType(ctx, definition.ID)
	if err != nil {
		if !isOntologyNotFound(err) {
			plan.addIssue(PromotionIssueInvalidProposal, err.Error())
			return
		}
		plan.Changes = append(plan.Changes, MetadataProposalPromotionChange{
			Operation:     PromotionOperationCreate,
			TargetKind:    "link_type",
			TargetID:      definition.ID,
			Field:         "definition",
			ProposedValue: canonical,
		})
		return
	}
	currentCanonical, err := canonicalLinkTypeDefinitionValue(current)
	if err != nil {
		plan.addIssue(PromotionIssueInvalidProposal, err.Error())
		return
	}
	operation := PromotionOperationUpdate
	if currentCanonical == canonical {
		operation = PromotionOperationNoop
	}
	plan.Changes = append(plan.Changes, MetadataProposalPromotionChange{
		Operation:       operation,
		TargetKind:      "link_type",
		TargetID:        definition.ID,
		Field:           "definition",
		CurrentValue:    currentCanonical,
		ProposedValue:   canonical,
		ExpectedVersion: current.Version,
		CurrentVersion:  current.Version,
	})
}

func planPropertyTypePromotion(ctx context.Context, registry Registry, proposal OntologyMetadataProposal, plan *MetadataProposalPromotionPlan) {
	if proposal.MetadataKey == "definition" {
		planPropertyTypeDefinitionPromotion(ctx, registry, proposal, plan)
		return
	}
	propertyType, err := registry.GetPropertyType(ctx, proposal.TargetID)
	if err != nil {
		plan.addIssue(PromotionIssueTargetNotFound, err.Error())
		return
	}
	switch proposal.MetadataKey {
	case "allowed_action":
		planAllowedActionPromotion("property_type", propertyType.ID, propertyType.AllowedActions, propertyType.Version, proposal, plan)
	default:
		plan.addIssue(PromotionIssueUnsupportedTargetField, fmt.Sprintf("property_type metadata key %q cannot be promoted yet", proposal.MetadataKey))
	}
}

func planPropertyTypeDefinitionPromotion(ctx context.Context, registry Registry, proposal OntologyMetadataProposal, plan *MetadataProposalPromotionPlan) {
	definition, canonical, err := propertyTypeDefinitionFromProposal(ctx, registry, proposal)
	if err != nil {
		plan.addIssue(PromotionIssueInvalidProposal, err.Error())
		return
	}
	current, err := registry.GetPropertyType(ctx, definition.ID)
	if err != nil {
		if !isOntologyNotFound(err) {
			plan.addIssue(PromotionIssueInvalidProposal, err.Error())
			return
		}
		plan.Changes = append(plan.Changes, MetadataProposalPromotionChange{
			Operation:     PromotionOperationCreate,
			TargetKind:    "property_type",
			TargetID:      definition.ID,
			Field:         "definition",
			ProposedValue: canonical,
		})
		return
	}
	currentCanonical, err := canonicalPropertyTypeDefinitionValue(current)
	if err != nil {
		plan.addIssue(PromotionIssueInvalidProposal, err.Error())
		return
	}
	operation := PromotionOperationUpdate
	if currentCanonical == canonical {
		operation = PromotionOperationNoop
	}
	plan.Changes = append(plan.Changes, MetadataProposalPromotionChange{
		Operation:       operation,
		TargetKind:      "property_type",
		TargetID:        definition.ID,
		Field:           "definition",
		CurrentValue:    currentCanonical,
		ProposedValue:   canonical,
		ExpectedVersion: current.Version,
		CurrentVersion:  current.Version,
	})
}

func planAllowedActionPromotion(targetKind string, targetID string, currentActions []string, currentVersion int, proposal OntologyMetadataProposal, plan *MetadataProposalPromotionPlan) {
	actionID := normalizeID(proposal.ProposedValue)
	if actionID == "" {
		plan.addIssue(PromotionIssueInvalidProposal, "allowed_action proposed value is required")
		return
	}
	operation := PromotionOperationAppend
	if stringSliceContainsNormalized(currentActions, actionID) {
		operation = PromotionOperationNoop
	}
	plan.Changes = append(plan.Changes, MetadataProposalPromotionChange{
		Operation:       operation,
		TargetKind:      targetKind,
		TargetID:        targetID,
		Field:           "allowed_actions",
		CurrentValue:    strings.Join(normalizeIDs(currentActions), ","),
		ProposedValue:   actionID,
		ExpectedVersion: currentVersion,
		CurrentVersion:  currentVersion,
	})
}

func planMappingRulePromotion(ctx context.Context, registry Registry, proposal OntologyMetadataProposal, plan *MetadataProposalPromotionPlan) {
	if proposal.MetadataKey == "definition" {
		planMappingRuleDefinitionPromotion(ctx, registry, proposal, plan)
		return
	}
	rules, err := registry.ListMappingRules(ctx, proposal.TenantID)
	if err != nil {
		plan.addIssue(PromotionIssueInvalidProposal, err.Error())
		return
	}
	var rule MappingRule
	found := false
	for _, candidate := range rules {
		if normalizeID(candidate.ID) == proposal.TargetID {
			rule = candidate
			found = true
			break
		}
	}
	if !found {
		plan.addIssue(PromotionIssueTargetNotFound, fmt.Sprintf("mapping rule %s was not found", proposal.TargetID))
		return
	}
	currentValue, ok := mappingRulePromotionFieldValue(rule, proposal.MetadataKey)
	if !ok {
		plan.addIssue(PromotionIssueUnsupportedTargetField, fmt.Sprintf("mapping_rule metadata key %q cannot be promoted yet", proposal.MetadataKey))
		return
	}
	proposedValue := strings.TrimSpace(proposal.ProposedValue)
	operation := PromotionOperationUpdate
	if currentValue == proposedValue {
		operation = PromotionOperationNoop
	}
	plan.Changes = append(plan.Changes, MetadataProposalPromotionChange{
		Operation:       operation,
		TargetKind:      "mapping_rule",
		TargetID:        rule.ID,
		Field:           proposal.MetadataKey,
		CurrentValue:    currentValue,
		ProposedValue:   proposedValue,
		ExpectedVersion: rule.Version,
		CurrentVersion:  rule.Version,
	})
}

func planMappingRuleDefinitionPromotion(ctx context.Context, registry Registry, proposal OntologyMetadataProposal, plan *MetadataProposalPromotionPlan) {
	definition, canonical, err := mappingRuleDefinitionFromProposal(ctx, registry, proposal)
	if err != nil {
		plan.addIssue(PromotionIssueInvalidProposal, err.Error())
		return
	}
	current, found, err := findMappingRule(ctx, registry, definition.TenantID, definition.ID)
	if err != nil {
		plan.addIssue(PromotionIssueInvalidProposal, err.Error())
		return
	}
	if !found {
		plan.Changes = append(plan.Changes, MetadataProposalPromotionChange{
			Operation:     PromotionOperationCreate,
			TargetKind:    "mapping_rule",
			TargetID:      definition.ID,
			Field:         "definition",
			ProposedValue: canonical,
		})
		return
	}
	currentCanonical, err := canonicalMappingRuleDefinitionValue(current)
	if err != nil {
		plan.addIssue(PromotionIssueInvalidProposal, err.Error())
		return
	}
	operation := PromotionOperationUpdate
	if currentCanonical == canonical {
		operation = PromotionOperationNoop
	}
	plan.Changes = append(plan.Changes, MetadataProposalPromotionChange{
		Operation:       operation,
		TargetKind:      "mapping_rule",
		TargetID:        definition.ID,
		Field:           "definition",
		CurrentValue:    currentCanonical,
		ProposedValue:   canonical,
		ExpectedVersion: current.Version,
		CurrentVersion:  current.Version,
	})
}

func mappingRulePromotionFieldValue(rule MappingRule, key string) (string, bool) {
	switch normalizeID(key) {
	case "source_domain":
		return normalizeID(rule.SourceDomain), true
	case "source_ref":
		return strings.TrimSpace(rule.SourceRef), true
	case "event_schema_version":
		return strings.TrimSpace(rule.EventSchemaVersion), true
	case "rule_hash":
		return strings.TrimSpace(rule.RuleHash), true
	default:
		return "", false
	}
}

func (p *MetadataProposalPromotionPlan) addIssue(code PromotionIssueCode, message string) {
	p.Issues = append(p.Issues, MetadataProposalPromotionIssue{Code: code, Message: message})
}

func promotionPlanHasIssue(plan MetadataProposalPromotionPlan, code PromotionIssueCode) bool {
	for _, issue := range plan.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func applyRegistryExpectedVersion(plan *MetadataProposalPromotionPlan, expectedVersion int) {
	if plan == nil || expectedVersion <= 0 {
		return
	}
	for i := range plan.Changes {
		plan.Changes[i].ExpectedVersion = expectedVersion
		if plan.Changes[i].CurrentVersion > 0 && plan.Changes[i].CurrentVersion != expectedVersion {
			plan.addIssue(PromotionIssueRegistryVersionConflict, fmt.Sprintf("%s %s version changed from %d to %d", plan.Changes[i].TargetKind, plan.Changes[i].TargetID, expectedVersion, plan.Changes[i].CurrentVersion))
		}
	}
	if len(plan.Issues) > 0 {
		plan.CanApply = false
	}
}

func applyMetadataProposalPromotionChange(ctx context.Context, registry Registry, tenantID string, change MetadataProposalPromotionChange) (MetadataProposalPromotionChange, bool, error) {
	switch change.Operation {
	case PromotionOperationNoop:
		return change, false, nil
	case PromotionOperationCreate:
		if change.TargetKind == "object_type" && change.Field == "definition" {
			return applyObjectTypeDefinitionPromotion(ctx, registry, tenantID, change)
		}
		if change.TargetKind == "link_type" && change.Field == "definition" {
			return applyLinkTypeDefinitionPromotion(ctx, registry, tenantID, change)
		}
		if change.TargetKind == "property_type" && change.Field == "definition" {
			return applyPropertyTypeDefinitionPromotion(ctx, registry, tenantID, change)
		}
		if change.TargetKind == "mapping_rule" && change.Field == "definition" {
			return applyMappingRuleDefinitionPromotion(ctx, registry, tenantID, change)
		}
		if change.Field != "definition" {
			return change, false, fmt.Errorf("%w: unsupported create target %s.%s", ErrInvalidDefinition, change.TargetKind, change.Field)
		}
		return change, false, fmt.Errorf("%w: unsupported create target %s.%s", ErrInvalidDefinition, change.TargetKind, change.Field)
	case PromotionOperationAppend:
		if change.Field != "allowed_actions" {
			return change, false, fmt.Errorf("%w: unsupported append field %s", ErrInvalidDefinition, change.Field)
		}
		return applyAllowedActionPromotion(ctx, registry, change)
	case PromotionOperationUpdate:
		if change.TargetKind == "object_type" && change.Field == "definition" {
			return applyObjectTypeDefinitionPromotion(ctx, registry, tenantID, change)
		}
		if change.TargetKind == "link_type" && change.Field == "definition" {
			return applyLinkTypeDefinitionPromotion(ctx, registry, tenantID, change)
		}
		if change.TargetKind == "property_type" && change.Field == "definition" {
			return applyPropertyTypeDefinitionPromotion(ctx, registry, tenantID, change)
		}
		if change.TargetKind == "mapping_rule" && change.Field == "definition" {
			return applyMappingRuleDefinitionPromotion(ctx, registry, tenantID, change)
		}
		if change.TargetKind != "mapping_rule" {
			return change, false, fmt.Errorf("%w: unsupported update target %s", ErrInvalidDefinition, change.TargetKind)
		}
		return applyMappingRulePromotion(ctx, registry, tenantID, change)
	default:
		return change, false, fmt.Errorf("%w: unsupported promotion operation %s", ErrInvalidDefinition, change.Operation)
	}
}

func applyObjectTypeDefinitionPromotion(ctx context.Context, registry Registry, tenantID string, change MetadataProposalPromotionChange) (MetadataProposalPromotionChange, bool, error) {
	objectType, canonical, err := parseCanonicalObjectTypeDefinition(ctx, registry, change.ProposedValue)
	if err != nil {
		return change, false, err
	}
	if normalizeID(objectType.ID) != normalizeID(change.TargetID) {
		return change, false, fmt.Errorf("%w: object type definition id must match promotion target_id", ErrInvalidDefinition)
	}
	if strings.TrimSpace(objectType.TenantID) == "" && objectType.TenantScope != TenantScopeGlobal {
		objectType.TenantID = strings.TrimSpace(tenantID)
	}
	if err := validateObjectTypeDefinition(ctx, registry, objectType); err != nil {
		return change, false, err
	}
	canonical, err = canonicalObjectTypeDefinitionValue(objectType)
	if err != nil {
		return change, false, err
	}
	change.ProposedValue = canonical
	switch change.Operation {
	case PromotionOperationCreate:
		if _, err := registry.GetObjectType(ctx, objectType.ID); err == nil {
			return change, false, fmt.Errorf("%w: object type %s already exists", ErrVersionConflict, objectType.ID)
		} else if !isOntologyNotFound(err) {
			return change, false, err
		}
		objectType.Version = 1
		if err := registry.SaveObjectType(ctx, objectType); err != nil {
			return change, false, err
		}
		applied, err := registry.GetObjectType(ctx, objectType.ID)
		if err != nil {
			return change, false, err
		}
		change.AppliedVersion = applied.Version
		return change, true, nil
	case PromotionOperationUpdate:
		current, err := registry.GetObjectType(ctx, objectType.ID)
		if err != nil {
			return change, false, err
		}
		change.CurrentVersion = current.Version
		if err := ensureRegistryVersionMatches(change); err != nil {
			return change, false, err
		}
		currentCanonical, err := canonicalObjectTypeDefinitionValue(current)
		if err != nil {
			return change, false, err
		}
		change.CurrentValue = currentCanonical
		if currentCanonical == canonical {
			return change, false, nil
		}
		objectType.Version = current.Version + 1
		applied, err := saveObjectTypeWithExpectedVersion(ctx, registry, objectType, change.ExpectedVersion)
		if err != nil {
			return change, false, err
		}
		change.AppliedVersion = applied.Version
		return change, true, nil
	default:
		return change, false, fmt.Errorf("%w: unsupported object type definition operation %s", ErrInvalidDefinition, change.Operation)
	}
}

func applyLinkTypeDefinitionPromotion(ctx context.Context, registry Registry, tenantID string, change MetadataProposalPromotionChange) (MetadataProposalPromotionChange, bool, error) {
	linkType, canonical, err := parseCanonicalLinkTypeDefinition(change.ProposedValue)
	if err != nil {
		return change, false, err
	}
	if normalizeID(linkType.ID) != normalizeID(change.TargetID) {
		return change, false, fmt.Errorf("%w: link type definition id must match promotion target_id", ErrInvalidDefinition)
	}
	if strings.TrimSpace(linkType.TenantID) == "" && linkType.TenantScope != TenantScopeGlobal {
		linkType.TenantID = strings.TrimSpace(tenantID)
	}
	if err := validateLinkTypeDefinitionEndpoints(ctx, registry, linkType); err != nil {
		return change, false, err
	}
	canonical, err = canonicalLinkTypeDefinitionValue(linkType)
	if err != nil {
		return change, false, err
	}
	change.ProposedValue = canonical
	switch change.Operation {
	case PromotionOperationCreate:
		if _, err := registry.GetLinkType(ctx, linkType.ID); err == nil {
			return change, false, fmt.Errorf("%w: link type %s already exists", ErrVersionConflict, linkType.ID)
		} else if !isOntologyNotFound(err) {
			return change, false, err
		}
		linkType.Version = 1
		if err := registry.SaveLinkType(ctx, linkType); err != nil {
			return change, false, err
		}
		applied, err := registry.GetLinkType(ctx, linkType.ID)
		if err != nil {
			return change, false, err
		}
		change.AppliedVersion = applied.Version
		return change, true, nil
	case PromotionOperationUpdate:
		current, err := registry.GetLinkType(ctx, linkType.ID)
		if err != nil {
			return change, false, err
		}
		change.CurrentVersion = current.Version
		if err := ensureRegistryVersionMatches(change); err != nil {
			return change, false, err
		}
		currentCanonical, err := canonicalLinkTypeDefinitionValue(current)
		if err != nil {
			return change, false, err
		}
		change.CurrentValue = currentCanonical
		if currentCanonical == canonical {
			return change, false, nil
		}
		linkType.Version = current.Version + 1
		applied, err := saveLinkTypeWithExpectedVersion(ctx, registry, linkType, change.ExpectedVersion)
		if err != nil {
			return change, false, err
		}
		change.AppliedVersion = applied.Version
		return change, true, nil
	default:
		return change, false, fmt.Errorf("%w: unsupported link type definition operation %s", ErrInvalidDefinition, change.Operation)
	}
}

func applyPropertyTypeDefinitionPromotion(ctx context.Context, registry Registry, tenantID string, change MetadataProposalPromotionChange) (MetadataProposalPromotionChange, bool, error) {
	propertyType, canonical, err := parseCanonicalPropertyTypeDefinition(ctx, registry, change.ProposedValue)
	if err != nil {
		return change, false, err
	}
	if normalizeID(propertyType.ID) != normalizeID(change.TargetID) {
		return change, false, fmt.Errorf("%w: property type definition id must match promotion target_id", ErrInvalidDefinition)
	}
	if strings.TrimSpace(propertyType.TenantID) == "" {
		propertyType.TenantID = strings.TrimSpace(tenantID)
	}
	if err := validatePropertyTypeDefinition(ctx, registry, propertyType); err != nil {
		return change, false, err
	}
	canonical, err = canonicalPropertyTypeDefinitionValue(propertyType)
	if err != nil {
		return change, false, err
	}
	change.ProposedValue = canonical
	switch change.Operation {
	case PromotionOperationCreate:
		if _, err := registry.GetPropertyType(ctx, propertyType.ID); err == nil {
			return change, false, fmt.Errorf("%w: property type %s already exists", ErrVersionConflict, propertyType.ID)
		} else if !isOntologyNotFound(err) {
			return change, false, err
		}
		propertyType.Version = 1
		if err := registry.SavePropertyType(ctx, propertyType); err != nil {
			return change, false, err
		}
		applied, err := registry.GetPropertyType(ctx, propertyType.ID)
		if err != nil {
			return change, false, err
		}
		change.AppliedVersion = applied.Version
		return change, true, nil
	case PromotionOperationUpdate:
		current, err := registry.GetPropertyType(ctx, propertyType.ID)
		if err != nil {
			return change, false, err
		}
		change.CurrentVersion = current.Version
		if err := ensureRegistryVersionMatches(change); err != nil {
			return change, false, err
		}
		currentCanonical, err := canonicalPropertyTypeDefinitionValue(current)
		if err != nil {
			return change, false, err
		}
		change.CurrentValue = currentCanonical
		if currentCanonical == canonical {
			return change, false, nil
		}
		propertyType.Version = current.Version + 1
		applied, err := savePropertyTypeWithExpectedVersion(ctx, registry, propertyType, change.ExpectedVersion)
		if err != nil {
			return change, false, err
		}
		change.AppliedVersion = applied.Version
		return change, true, nil
	default:
		return change, false, fmt.Errorf("%w: unsupported property type definition operation %s", ErrInvalidDefinition, change.Operation)
	}
}

func applyAllowedActionPromotion(ctx context.Context, registry Registry, change MetadataProposalPromotionChange) (MetadataProposalPromotionChange, bool, error) {
	actionID := normalizeID(change.ProposedValue)
	if actionID == "" {
		return change, false, fmt.Errorf("%w: allowed action proposed value is required", ErrInvalidDefinition)
	}
	switch change.TargetKind {
	case "object_type":
		objectType, err := registry.GetObjectType(ctx, change.TargetID)
		if err != nil {
			return change, false, err
		}
		change.CurrentVersion = objectType.Version
		if err := ensureRegistryVersionMatches(change); err != nil {
			return change, false, err
		}
		actions, appended := appendNormalizedID(objectType.AllowedActions, actionID)
		if !appended {
			return change, false, nil
		}
		objectType.AllowedActions = actions
		applied, err := saveObjectTypeWithExpectedVersion(ctx, registry, objectType, change.ExpectedVersion)
		if err != nil {
			return change, false, err
		}
		change.AppliedVersion = applied.Version
		return change, true, nil
	case "link_type":
		linkType, err := registry.GetLinkType(ctx, change.TargetID)
		if err != nil {
			return change, false, err
		}
		change.CurrentVersion = linkType.Version
		if err := ensureRegistryVersionMatches(change); err != nil {
			return change, false, err
		}
		actions, appended := appendNormalizedID(linkType.AllowedActions, actionID)
		if !appended {
			return change, false, nil
		}
		linkType.AllowedActions = actions
		applied, err := saveLinkTypeWithExpectedVersion(ctx, registry, linkType, change.ExpectedVersion)
		if err != nil {
			return change, false, err
		}
		change.AppliedVersion = applied.Version
		return change, true, nil
	case "property_type":
		propertyType, err := registry.GetPropertyType(ctx, change.TargetID)
		if err != nil {
			return change, false, err
		}
		change.CurrentVersion = propertyType.Version
		if err := ensureRegistryVersionMatches(change); err != nil {
			return change, false, err
		}
		actions, appended := appendNormalizedID(propertyType.AllowedActions, actionID)
		if !appended {
			return change, false, nil
		}
		propertyType.AllowedActions = actions
		applied, err := savePropertyTypeWithExpectedVersion(ctx, registry, propertyType, change.ExpectedVersion)
		if err != nil {
			return change, false, err
		}
		change.AppliedVersion = applied.Version
		return change, true, nil
	default:
		return change, false, fmt.Errorf("%w: unsupported allowed action target %s", ErrInvalidDefinition, change.TargetKind)
	}
}

func applyMappingRulePromotion(ctx context.Context, registry Registry, tenantID string, change MetadataProposalPromotionChange) (MetadataProposalPromotionChange, bool, error) {
	rules, err := registry.ListMappingRules(ctx, tenantID)
	if err != nil {
		return change, false, err
	}
	for _, rule := range rules {
		if normalizeID(rule.ID) != normalizeID(change.TargetID) {
			continue
		}
		change.CurrentVersion = rule.Version
		if err := ensureRegistryVersionMatches(change); err != nil {
			return change, false, err
		}
		updated, changed, err := applyMappingRulePromotionField(rule, change.Field, change.ProposedValue)
		if err != nil {
			return change, false, err
		}
		if !changed {
			return change, false, nil
		}
		applied, err := saveMappingRuleWithExpectedVersion(ctx, registry, updated, change.ExpectedVersion)
		if err != nil {
			return change, false, err
		}
		change.AppliedVersion = applied.Version
		return change, true, nil
	}
	return change, false, fmt.Errorf("%w: mapping rule %s", ErrNotFound, change.TargetID)
}

func applyMappingRuleDefinitionPromotion(ctx context.Context, registry Registry, tenantID string, change MetadataProposalPromotionChange) (MetadataProposalPromotionChange, bool, error) {
	rule, canonical, err := parseCanonicalMappingRuleDefinition(ctx, registry, change.ProposedValue)
	if err != nil {
		return change, false, err
	}
	if normalizeID(rule.ID) != normalizeID(change.TargetID) {
		return change, false, fmt.Errorf("%w: mapping rule definition id must match promotion target_id", ErrInvalidDefinition)
	}
	if strings.TrimSpace(rule.TenantID) == "" {
		rule.TenantID = strings.TrimSpace(tenantID)
	}
	if err := validateMappingRuleDefinition(ctx, registry, rule); err != nil {
		return change, false, err
	}
	canonical, err = canonicalMappingRuleDefinitionValue(rule)
	if err != nil {
		return change, false, err
	}
	change.ProposedValue = canonical
	switch change.Operation {
	case PromotionOperationCreate:
		if _, found, err := findMappingRule(ctx, registry, rule.TenantID, rule.ID); err != nil {
			return change, false, err
		} else if found {
			return change, false, fmt.Errorf("%w: mapping rule %s already exists", ErrVersionConflict, rule.ID)
		}
		rule.Version = 1
		if err := registry.SaveMappingRule(ctx, rule); err != nil {
			return change, false, err
		}
		applied, found, err := findMappingRule(ctx, registry, rule.TenantID, rule.ID)
		if err != nil {
			return change, false, err
		}
		if !found {
			return change, false, fmt.Errorf("%w: mapping rule %s", ErrNotFound, rule.ID)
		}
		change.AppliedVersion = applied.Version
		return change, true, nil
	case PromotionOperationUpdate:
		current, found, err := findMappingRule(ctx, registry, rule.TenantID, rule.ID)
		if err != nil {
			return change, false, err
		}
		if !found {
			return change, false, fmt.Errorf("%w: mapping rule %s", ErrNotFound, rule.ID)
		}
		change.CurrentVersion = current.Version
		if err := ensureRegistryVersionMatches(change); err != nil {
			return change, false, err
		}
		currentCanonical, err := canonicalMappingRuleDefinitionValue(current)
		if err != nil {
			return change, false, err
		}
		change.CurrentValue = currentCanonical
		if currentCanonical == canonical {
			return change, false, nil
		}
		rule.Version = current.Version + 1
		applied, err := saveMappingRuleWithExpectedVersion(ctx, registry, rule, change.ExpectedVersion)
		if err != nil {
			return change, false, err
		}
		change.AppliedVersion = applied.Version
		return change, true, nil
	default:
		return change, false, fmt.Errorf("%w: unsupported mapping rule definition operation %s", ErrInvalidDefinition, change.Operation)
	}
}

func ensureRegistryVersionMatches(change MetadataProposalPromotionChange) error {
	if change.ExpectedVersion <= 0 || change.CurrentVersion <= 0 || change.ExpectedVersion == change.CurrentVersion {
		return nil
	}
	return fmt.Errorf("%w: %s %s expected version %d, current version %d", ErrVersionConflict, change.TargetKind, change.TargetID, change.ExpectedVersion, change.CurrentVersion)
}

func saveObjectTypeWithExpectedVersion(ctx context.Context, registry Registry, objectType ObjectType, expectedVersion int) (ObjectType, error) {
	if versioned, ok := registry.(versionedRegistry); ok && expectedVersion > 0 {
		objectType.Version = expectedVersion + 1
		return versioned.SaveObjectTypeWithExpectedVersion(ctx, objectType, expectedVersion)
	}
	if objectType.Version <= 0 {
		objectType.Version = 1
	}
	if expectedVersion > 0 {
		objectType.Version = expectedVersion + 1
	}
	if err := registry.SaveObjectType(ctx, objectType); err != nil {
		return ObjectType{}, err
	}
	return registry.GetObjectType(ctx, objectType.ID)
}

func saveLinkTypeWithExpectedVersion(ctx context.Context, registry Registry, linkType LinkType, expectedVersion int) (LinkType, error) {
	if versioned, ok := registry.(versionedRegistry); ok && expectedVersion > 0 {
		linkType.Version = expectedVersion + 1
		return versioned.SaveLinkTypeWithExpectedVersion(ctx, linkType, expectedVersion)
	}
	if linkType.Version <= 0 {
		linkType.Version = 1
	}
	if expectedVersion > 0 {
		linkType.Version = expectedVersion + 1
	}
	if err := registry.SaveLinkType(ctx, linkType); err != nil {
		return LinkType{}, err
	}
	return registry.GetLinkType(ctx, linkType.ID)
}

func savePropertyTypeWithExpectedVersion(ctx context.Context, registry Registry, propertyType PropertyType, expectedVersion int) (PropertyType, error) {
	if versioned, ok := registry.(versionedRegistry); ok && expectedVersion > 0 {
		propertyType.Version = expectedVersion + 1
		return versioned.SavePropertyTypeWithExpectedVersion(ctx, propertyType, expectedVersion)
	}
	if propertyType.Version <= 0 {
		propertyType.Version = 1
	}
	if expectedVersion > 0 {
		propertyType.Version = expectedVersion + 1
	}
	if err := registry.SavePropertyType(ctx, propertyType); err != nil {
		return PropertyType{}, err
	}
	return registry.GetPropertyType(ctx, propertyType.ID)
}

func saveMappingRuleWithExpectedVersion(ctx context.Context, registry Registry, rule MappingRule, expectedVersion int) (MappingRule, error) {
	if versioned, ok := registry.(versionedRegistry); ok && expectedVersion > 0 {
		rule.Version = expectedVersion + 1
		return versioned.SaveMappingRuleWithExpectedVersion(ctx, rule, expectedVersion)
	}
	if rule.Version <= 0 {
		rule.Version = 1
	}
	if expectedVersion > 0 {
		rule.Version = expectedVersion + 1
	}
	if err := registry.SaveMappingRule(ctx, rule); err != nil {
		return MappingRule{}, err
	}
	rules, err := registry.ListMappingRules(ctx, rule.TenantID)
	if err != nil {
		return MappingRule{}, err
	}
	for _, candidate := range rules {
		if normalizeID(candidate.ID) == normalizeID(rule.ID) {
			return candidate, nil
		}
	}
	return MappingRule{}, fmt.Errorf("%w: mapping rule %s", ErrNotFound, rule.ID)
}

func objectTypeDefinitionFromProposal(ctx context.Context, registry Registry, proposal OntologyMetadataProposal) (ObjectType, string, error) {
	objectType, canonical, err := parseCanonicalObjectTypeDefinition(ctx, registry, proposal.ProposedValue)
	if err != nil {
		return ObjectType{}, "", err
	}
	if normalizeID(proposal.TargetID) == "" || normalizeID(objectType.ID) != normalizeID(proposal.TargetID) {
		return ObjectType{}, "", fmt.Errorf("%w: object type definition id must match proposal target_id", ErrInvalidDefinition)
	}
	if strings.TrimSpace(objectType.TenantID) == "" {
		if current, currentErr := registry.GetObjectType(ctx, objectType.ID); currentErr == nil {
			objectType.TenantID = strings.TrimSpace(current.TenantID)
		} else if !isOntologyNotFound(currentErr) {
			return ObjectType{}, "", currentErr
		} else if objectType.TenantScope != TenantScopeGlobal {
			objectType.TenantID = strings.TrimSpace(proposal.TenantID)
		}
	}
	if proposal.TenantID != "" && objectType.TenantID != "" && strings.TrimSpace(proposal.TenantID) != strings.TrimSpace(objectType.TenantID) {
		return ObjectType{}, "", fmt.Errorf("%w: object type definition tenant_id must match proposal tenant_id", ErrInvalidDefinition)
	}
	objectType, err = normalizeObjectType(objectType)
	if err != nil {
		return ObjectType{}, "", err
	}
	if err := validateObjectTypeDefinition(ctx, registry, objectType); err != nil {
		return ObjectType{}, "", err
	}
	canonical, err = canonicalObjectTypeDefinitionValue(objectType)
	if err != nil {
		return ObjectType{}, "", err
	}
	return objectType, canonical, nil
}

func parseCanonicalObjectTypeDefinition(ctx context.Context, registry Registry, value string) (ObjectType, string, error) {
	var objectType ObjectType
	if err := json.Unmarshal([]byte(strings.TrimSpace(value)), &objectType); err != nil {
		return ObjectType{}, "", fmt.Errorf("%w: object type definition must be valid JSON", ErrInvalidDefinition)
	}
	normalized, err := normalizeObjectType(objectType)
	if err != nil {
		return ObjectType{}, "", err
	}
	if err := validateObjectTypeDefinition(ctx, registry, normalized); err != nil {
		return ObjectType{}, "", err
	}
	canonical, err := canonicalObjectTypeDefinitionValue(normalized)
	if err != nil {
		return ObjectType{}, "", err
	}
	return normalized, canonical, nil
}

func canonicalObjectTypeDefinitionValue(objectType ObjectType) (string, error) {
	normalized, err := normalizeObjectType(objectType)
	if err != nil {
		return "", err
	}
	canonical := struct {
		ID               string               `json:"id"`
		TenantID         string               `json:"tenant_id,omitempty"`
		TenantScope      TenantScope          `json:"tenant_scope"`
		DisplayName      string               `json:"display_name"`
		CanonicalKeyRule string               `json:"canonical_key_rule"`
		OwnerDomain      string               `json:"owner_domain,omitempty"`
		Sensitivity      string               `json:"sensitivity,omitempty"`
		Lifecycle        string               `json:"lifecycle,omitempty"`
		TemporalMode     TemporalMode         `json:"temporal_mode,omitempty"`
		Properties       []PropertyDefinition `json:"properties,omitempty"`
		AllowedLinkTypes []string             `json:"allowed_link_types,omitempty"`
		AllowedActions   []string             `json:"allowed_actions,omitempty"`
	}{
		ID:               normalized.ID,
		TenantID:         normalized.TenantID,
		TenantScope:      normalized.TenantScope,
		DisplayName:      normalized.DisplayName,
		CanonicalKeyRule: normalized.CanonicalKeyRule,
		OwnerDomain:      normalized.OwnerDomain,
		Sensitivity:      normalized.Sensitivity,
		Lifecycle:        normalized.Lifecycle,
		TemporalMode:     normalized.TemporalMode,
		Properties:       normalized.Properties,
		AllowedLinkTypes: normalized.AllowedLinkTypes,
		AllowedActions:   normalized.AllowedActions,
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: object type definition cannot be encoded", ErrInvalidDefinition)
	}
	return string(raw), nil
}

func validateObjectTypeDefinition(ctx context.Context, registry Registry, objectType ObjectType) error {
	if registry == nil {
		return fmt.Errorf("%w: ontology registry is required", ErrInvalidDefinition)
	}
	switch objectType.TenantScope {
	case TenantScopeGlobal, TenantScopeTenant:
	default:
		return fmt.Errorf("%w: object type tenant_scope %q is not supported", ErrInvalidDefinition, objectType.TenantScope)
	}
	if objectType.TenantScope == TenantScopeGlobal && strings.TrimSpace(objectType.TenantID) != "" {
		return fmt.Errorf("%w: global object type definition cannot carry tenant_id", ErrInvalidDefinition)
	}
	if _, err := normalizeTemporalMode(objectType.TemporalMode); err != nil {
		return err
	}
	for _, property := range objectType.Properties {
		if err := validatePropertyDefinition(property); err != nil {
			return err
		}
	}
	for _, linkTypeID := range objectType.AllowedLinkTypes {
		linkType, err := registry.GetLinkType(ctx, linkTypeID)
		if err != nil {
			if isOntologyNotFound(err) {
				return fmt.Errorf("%w: object type allowed_link_type %s", ErrUnknownLinkType, linkTypeID)
			}
			return err
		}
		if !ontologyDefinitionVisibleToTenant(linkType.TenantID, objectType.TenantID) {
			return fmt.Errorf("%w: object type %s cannot reference link type %s from tenant %s", ErrInvalidDefinition, objectType.ID, linkType.ID, linkType.TenantID)
		}
	}
	return nil
}

func validatePropertyDefinition(property PropertyDefinition) error {
	if normalizeID(property.PropertyID) == "" {
		return fmt.Errorf("%w: object property_id is required", ErrInvalidDefinition)
	}
	if !isSupportedValueType(property.ValueType) {
		return fmt.Errorf("%w: object property value_type %q is not supported", ErrInvalidDefinition, property.ValueType)
	}
	if _, err := normalizeTemporalMode(property.TemporalMode); err != nil {
		return err
	}
	return nil
}

func linkTypeDefinitionFromProposal(ctx context.Context, registry Registry, proposal OntologyMetadataProposal) (LinkType, string, error) {
	linkType, canonical, err := parseCanonicalLinkTypeDefinition(proposal.ProposedValue)
	if err != nil {
		return LinkType{}, "", err
	}
	if normalizeID(proposal.TargetID) == "" || normalizeID(linkType.ID) != normalizeID(proposal.TargetID) {
		return LinkType{}, "", fmt.Errorf("%w: link type definition id must match proposal target_id", ErrInvalidDefinition)
	}
	if strings.TrimSpace(linkType.TenantID) == "" {
		if current, currentErr := registry.GetLinkType(ctx, linkType.ID); currentErr == nil {
			linkType.TenantID = strings.TrimSpace(current.TenantID)
		} else if !isOntologyNotFound(currentErr) {
			return LinkType{}, "", currentErr
		} else if linkType.TenantScope != TenantScopeGlobal {
			linkType.TenantID = strings.TrimSpace(proposal.TenantID)
		}
	}
	if proposal.TenantID != "" && linkType.TenantID != "" && strings.TrimSpace(proposal.TenantID) != strings.TrimSpace(linkType.TenantID) {
		return LinkType{}, "", fmt.Errorf("%w: link type definition tenant_id must match proposal tenant_id", ErrInvalidDefinition)
	}
	linkType, err = normalizeLinkType(linkType)
	if err != nil {
		return LinkType{}, "", err
	}
	canonical, err = canonicalLinkTypeDefinitionValue(linkType)
	if err != nil {
		return LinkType{}, "", err
	}
	return linkType, canonical, nil
}

func parseCanonicalLinkTypeDefinition(value string) (LinkType, string, error) {
	var linkType LinkType
	if err := json.Unmarshal([]byte(strings.TrimSpace(value)), &linkType); err != nil {
		return LinkType{}, "", fmt.Errorf("%w: link type definition must be valid JSON", ErrInvalidDefinition)
	}
	normalized, err := normalizeLinkType(linkType)
	if err != nil {
		return LinkType{}, "", err
	}
	canonical, err := canonicalLinkTypeDefinitionValue(normalized)
	if err != nil {
		return LinkType{}, "", err
	}
	return normalized, canonical, nil
}

func canonicalLinkTypeDefinitionValue(linkType LinkType) (string, error) {
	normalized, err := normalizeLinkType(linkType)
	if err != nil {
		return "", err
	}
	canonical := struct {
		ID               string       `json:"id"`
		TenantID         string       `json:"tenant_id,omitempty"`
		TenantScope      TenantScope  `json:"tenant_scope"`
		DisplayName      string       `json:"display_name,omitempty"`
		FromObjectType   string       `json:"from_object_type"`
		ToObjectType     string       `json:"to_object_type"`
		Cardinality      Cardinality  `json:"cardinality"`
		TemporalMode     TemporalMode `json:"temporal_mode,omitempty"`
		EvidenceRequired bool         `json:"evidence_required,omitempty"`
		SuggestedAllowed bool         `json:"suggested_allowed,omitempty"`
		AllowedActions   []string     `json:"allowed_actions,omitempty"`
	}{
		ID:               normalized.ID,
		TenantID:         normalized.TenantID,
		TenantScope:      normalized.TenantScope,
		DisplayName:      normalized.DisplayName,
		FromObjectType:   normalized.FromObjectType,
		ToObjectType:     normalized.ToObjectType,
		Cardinality:      normalized.Cardinality,
		TemporalMode:     normalized.TemporalMode,
		EvidenceRequired: normalized.EvidenceRequired,
		SuggestedAllowed: normalized.SuggestedAllowed,
		AllowedActions:   normalized.AllowedActions,
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: link type definition cannot be encoded", ErrInvalidDefinition)
	}
	return string(raw), nil
}

func validateLinkTypeDefinitionEndpoints(ctx context.Context, registry Registry, linkType LinkType) error {
	if registry == nil {
		return fmt.Errorf("%w: ontology registry is required", ErrInvalidDefinition)
	}
	if err := validateLinkTypeDefinitionEnums(linkType); err != nil {
		return err
	}
	fromObjectType, err := registry.GetObjectType(ctx, linkType.FromObjectType)
	if err != nil {
		if isOntologyNotFound(err) {
			return fmt.Errorf("%w: link type from_object_type %s", ErrUnknownObjectType, linkType.FromObjectType)
		}
		return err
	}
	toObjectType, err := registry.GetObjectType(ctx, linkType.ToObjectType)
	if err != nil {
		if isOntologyNotFound(err) {
			return fmt.Errorf("%w: link type to_object_type %s", ErrUnknownObjectType, linkType.ToObjectType)
		}
		return err
	}
	if !ontologyDefinitionVisibleToTenant(fromObjectType.TenantID, linkType.TenantID) {
		return fmt.Errorf("%w: link type %s cannot reference from_object_type %s from tenant %s", ErrInvalidDefinition, linkType.ID, fromObjectType.ID, fromObjectType.TenantID)
	}
	if !ontologyDefinitionVisibleToTenant(toObjectType.TenantID, linkType.TenantID) {
		return fmt.Errorf("%w: link type %s cannot reference to_object_type %s from tenant %s", ErrInvalidDefinition, linkType.ID, toObjectType.ID, toObjectType.TenantID)
	}
	return nil
}

func validateLinkTypeDefinitionEnums(linkType LinkType) error {
	switch linkType.TenantScope {
	case TenantScopeGlobal, TenantScopeTenant:
	default:
		return fmt.Errorf("%w: link type tenant_scope %q is not supported", ErrInvalidDefinition, linkType.TenantScope)
	}
	switch linkType.Cardinality {
	case CardinalityOneToOne, CardinalityOneToMany, CardinalityManyToOne, CardinalityManyToMany:
	default:
		return fmt.Errorf("%w: link type cardinality %q is not supported", ErrInvalidDefinition, linkType.Cardinality)
	}
	if linkType.TenantScope == TenantScopeGlobal && strings.TrimSpace(linkType.TenantID) != "" {
		return fmt.Errorf("%w: global link type definition cannot carry tenant_id", ErrInvalidDefinition)
	}
	if _, err := normalizeTemporalMode(linkType.TemporalMode); err != nil {
		return err
	}
	return nil
}

func propertyTypeDefinitionFromProposal(ctx context.Context, registry Registry, proposal OntologyMetadataProposal) (PropertyType, string, error) {
	propertyType, canonical, err := parseCanonicalPropertyTypeDefinition(ctx, registry, proposal.ProposedValue)
	if err != nil {
		return PropertyType{}, "", err
	}
	if normalizeID(proposal.TargetID) == "" || normalizeID(propertyType.ID) != normalizeID(proposal.TargetID) {
		return PropertyType{}, "", fmt.Errorf("%w: property type definition id must match proposal target_id", ErrInvalidDefinition)
	}
	if strings.TrimSpace(propertyType.TenantID) == "" {
		if current, currentErr := registry.GetPropertyType(ctx, propertyType.ID); currentErr == nil {
			propertyType.TenantID = strings.TrimSpace(current.TenantID)
		} else if !isOntologyNotFound(currentErr) {
			return PropertyType{}, "", currentErr
		} else {
			propertyType.TenantID = strings.TrimSpace(proposal.TenantID)
		}
	}
	if proposal.TenantID != "" && propertyType.TenantID != "" && strings.TrimSpace(proposal.TenantID) != strings.TrimSpace(propertyType.TenantID) {
		return PropertyType{}, "", fmt.Errorf("%w: property type definition tenant_id must match proposal tenant_id", ErrInvalidDefinition)
	}
	propertyType, err = normalizePropertyType(propertyType)
	if err != nil {
		return PropertyType{}, "", err
	}
	if err := validatePropertyTypeDefinition(ctx, registry, propertyType); err != nil {
		return PropertyType{}, "", err
	}
	canonical, err = canonicalPropertyTypeDefinitionValue(propertyType)
	if err != nil {
		return PropertyType{}, "", err
	}
	return propertyType, canonical, nil
}

func parseCanonicalPropertyTypeDefinition(ctx context.Context, registry Registry, value string) (PropertyType, string, error) {
	var propertyType PropertyType
	if err := json.Unmarshal([]byte(strings.TrimSpace(value)), &propertyType); err != nil {
		return PropertyType{}, "", fmt.Errorf("%w: property type definition must be valid JSON", ErrInvalidDefinition)
	}
	normalized, err := normalizePropertyType(propertyType)
	if err != nil {
		return PropertyType{}, "", err
	}
	if err := validatePropertyTypeDefinition(ctx, registry, normalized); err != nil {
		return PropertyType{}, "", err
	}
	canonical, err := canonicalPropertyTypeDefinitionValue(normalized)
	if err != nil {
		return PropertyType{}, "", err
	}
	return normalized, canonical, nil
}

func canonicalPropertyTypeDefinitionValue(propertyType PropertyType) (string, error) {
	normalized, err := normalizePropertyType(propertyType)
	if err != nil {
		return "", err
	}
	canonical := struct {
		ID              string         `json:"id"`
		TenantID        string         `json:"tenant_id,omitempty"`
		ObjectTypeID    string         `json:"object_type_id"`
		PropertyID      string         `json:"property_id"`
		DisplayName     string         `json:"display_name,omitempty"`
		ValueType       ValueType      `json:"value_type"`
		TemporalMode    TemporalMode   `json:"temporal_mode,omitempty"`
		AuthoritySource string         `json:"authority_source,omitempty"`
		ConflictPolicy  ConflictPolicy `json:"conflict_policy,omitempty"`
		Sensitivity     string         `json:"sensitivity,omitempty"`
		AllowedActions  []string       `json:"allowed_actions,omitempty"`
	}{
		ID:              normalized.ID,
		TenantID:        normalized.TenantID,
		ObjectTypeID:    normalized.ObjectTypeID,
		PropertyID:      normalized.PropertyID,
		DisplayName:     normalized.DisplayName,
		ValueType:       normalized.ValueType,
		TemporalMode:    normalized.TemporalMode,
		AuthoritySource: normalized.AuthoritySource,
		ConflictPolicy:  normalized.ConflictPolicy,
		Sensitivity:     normalized.Sensitivity,
		AllowedActions:  normalized.AllowedActions,
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: property type definition cannot be encoded", ErrInvalidDefinition)
	}
	return string(raw), nil
}

func validatePropertyTypeDefinition(ctx context.Context, registry Registry, propertyType PropertyType) error {
	if registry == nil {
		return fmt.Errorf("%w: ontology registry is required", ErrInvalidDefinition)
	}
	if !isSupportedValueType(propertyType.ValueType) {
		return fmt.Errorf("%w: property type value_type %q is not supported", ErrInvalidDefinition, propertyType.ValueType)
	}
	if _, err := normalizeTemporalMode(propertyType.TemporalMode); err != nil {
		return err
	}
	switch propertyType.ConflictPolicy {
	case ConflictPolicyAuthorityWins, ConflictPolicyLatestWins, ConflictPolicyManualReview:
	default:
		return fmt.Errorf("%w: property type conflict_policy %q is not supported", ErrInvalidDefinition, propertyType.ConflictPolicy)
	}
	objectType, err := registry.GetObjectType(ctx, propertyType.ObjectTypeID)
	if err != nil {
		if isOntologyNotFound(err) {
			return fmt.Errorf("%w: property type object_type_id %s", ErrUnknownObjectType, propertyType.ObjectTypeID)
		}
		return err
	}
	if !ontologyDefinitionVisibleToTenant(objectType.TenantID, propertyType.TenantID) {
		return fmt.Errorf("%w: property type %s cannot reference object_type_id %s from tenant %s", ErrInvalidDefinition, propertyType.ID, objectType.ID, objectType.TenantID)
	}
	return nil
}

func mappingRuleDefinitionFromProposal(ctx context.Context, registry Registry, proposal OntologyMetadataProposal) (MappingRule, string, error) {
	rule, canonical, err := parseCanonicalMappingRuleDefinition(ctx, registry, proposal.ProposedValue)
	if err != nil {
		return MappingRule{}, "", err
	}
	if normalizeID(proposal.TargetID) == "" || normalizeID(rule.ID) != normalizeID(proposal.TargetID) {
		return MappingRule{}, "", fmt.Errorf("%w: mapping rule definition id must match proposal target_id", ErrInvalidDefinition)
	}
	if strings.TrimSpace(rule.TenantID) == "" {
		if current, found, currentErr := findMappingRule(ctx, registry, proposal.TenantID, rule.ID); currentErr != nil {
			return MappingRule{}, "", currentErr
		} else if found {
			rule.TenantID = strings.TrimSpace(current.TenantID)
		} else {
			rule.TenantID = strings.TrimSpace(proposal.TenantID)
		}
	}
	if proposal.TenantID != "" && rule.TenantID != "" && strings.TrimSpace(proposal.TenantID) != strings.TrimSpace(rule.TenantID) {
		return MappingRule{}, "", fmt.Errorf("%w: mapping rule definition tenant_id must match proposal tenant_id", ErrInvalidDefinition)
	}
	rule, err = normalizeMappingRule(rule)
	if err != nil {
		return MappingRule{}, "", err
	}
	if err := validateMappingRuleDefinition(ctx, registry, rule); err != nil {
		return MappingRule{}, "", err
	}
	canonical, err = canonicalMappingRuleDefinitionValue(rule)
	if err != nil {
		return MappingRule{}, "", err
	}
	return rule, canonical, nil
}

func parseCanonicalMappingRuleDefinition(ctx context.Context, registry Registry, value string) (MappingRule, string, error) {
	var rule MappingRule
	if err := json.Unmarshal([]byte(strings.TrimSpace(value)), &rule); err != nil {
		return MappingRule{}, "", fmt.Errorf("%w: mapping rule definition must be valid JSON", ErrInvalidDefinition)
	}
	normalized, err := normalizeMappingRule(rule)
	if err != nil {
		return MappingRule{}, "", err
	}
	normalized.RuleHash = hashMappingRule(normalized.Rule)
	if err := validateMappingRuleDefinition(ctx, registry, normalized); err != nil {
		return MappingRule{}, "", err
	}
	canonical, err := canonicalMappingRuleDefinitionValue(normalized)
	if err != nil {
		return MappingRule{}, "", err
	}
	return normalized, canonical, nil
}

func validateMappingRuleDefinition(ctx context.Context, registry Registry, rule MappingRule) error {
	if registry == nil {
		return fmt.Errorf("%w: ontology registry is required", ErrInvalidDefinition)
	}
	for _, objectType := range rule.ObjectTypes {
		definition, err := registry.GetObjectType(ctx, objectType)
		if err != nil {
			if isOntologyNotFound(err) {
				return fmt.Errorf("%w: %s", ErrUnknownObjectType, objectType)
			}
			return err
		}
		if !ontologyDefinitionVisibleToTenant(definition.TenantID, rule.TenantID) {
			return fmt.Errorf("%w: mapping rule %s cannot reference object type %s from tenant %s", ErrInvalidDefinition, rule.ID, definition.ID, definition.TenantID)
		}
	}
	for _, linkType := range rule.LinkTypes {
		definition, err := registry.GetLinkType(ctx, linkType)
		if err != nil {
			if isOntologyNotFound(err) {
				return fmt.Errorf("%w: %s", ErrUnknownLinkType, linkType)
			}
			return err
		}
		if !ontologyDefinitionVisibleToTenant(definition.TenantID, rule.TenantID) {
			return fmt.Errorf("%w: mapping rule %s cannot reference link type %s from tenant %s", ErrInvalidDefinition, rule.ID, definition.ID, definition.TenantID)
		}
	}
	if err := validateMappingRuleTemplates(rule, mappingRuleLookup{
		objectTypeExists: func(id string) (bool, error) {
			return registry.HasObjectType(ctx, id)
		},
		linkType: func(id string) (LinkType, error) {
			linkType, err := registry.GetLinkType(ctx, id)
			if err != nil {
				return LinkType{}, err
			}
			return linkType, nil
		},
		propertyExists: func(objectTypeID string, propertyID string) (bool, error) {
			properties, err := registry.ListPropertyTypes(ctx, objectTypeID)
			if err != nil {
				return false, err
			}
			for _, propertyType := range properties {
				if propertyType.ObjectTypeID == normalizeID(objectTypeID) && propertyType.PropertyID == normalizeID(propertyID) {
					return true, nil
				}
			}
			return false, nil
		},
	}); err != nil {
		return err
	}
	if !rule.Enabled {
		return nil
	}
	projection, err := CompileMappingRuleProjection(rule)
	if err != nil {
		return err
	}
	report := graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(registry)).ValidateSpec(ctx, projection.Spec())
	if !report.OK {
		return fmt.Errorf("%w: mapping rule projection conformance failed: %s", ErrInvalidDefinition, report.Issues[0].Message)
	}
	return nil
}

func newPromotionOverlayRegistry(ctx context.Context, registry Registry) (Registry, error) {
	if registry == nil {
		return nil, nil
	}
	overlay := NewMemoryRegistry()
	objects, err := registry.ListObjectTypes(ctx)
	if err != nil {
		return nil, err
	}
	for _, objectType := range objects {
		if err := overlay.SaveObjectType(ctx, objectType); err != nil {
			return nil, err
		}
	}
	links, err := registry.ListLinkTypes(ctx)
	if err != nil {
		return nil, err
	}
	for _, linkType := range links {
		if err := overlay.SaveLinkType(ctx, linkType); err != nil {
			return nil, err
		}
	}
	properties, err := registry.ListPropertyTypes(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, propertyType := range properties {
		if err := overlay.SavePropertyType(ctx, propertyType); err != nil {
			return nil, err
		}
	}
	rules, err := registry.ListMappingRules(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, rule := range rules {
		if err := overlay.SaveMappingRule(ctx, rule); err != nil {
			return nil, err
		}
	}
	return overlay, nil
}

func applyPromotionPlanToOverlay(ctx context.Context, registry Registry, tenantID string, plan MetadataProposalPromotionPlan) error {
	if registry == nil {
		return nil
	}
	for _, change := range plan.Changes {
		if change.Operation == PromotionOperationNoop {
			continue
		}
		if _, _, err := applyMetadataProposalPromotionChange(ctx, registry, tenantID, change); err != nil {
			return err
		}
	}
	return nil
}

func canonicalMappingRuleDefinitionValue(rule MappingRule) (string, error) {
	normalized, err := normalizeMappingRule(rule)
	if err != nil {
		return "", err
	}
	normalized.RuleHash = hashMappingRule(normalized.Rule)
	canonical := struct {
		ID                 string         `json:"id"`
		TenantID           string         `json:"tenant_id,omitempty"`
		EventType          string         `json:"event_type"`
		SourceDomain       string         `json:"source_domain,omitempty"`
		SourceRef          string         `json:"source_ref,omitempty"`
		EventSchemaVersion string         `json:"event_schema_version,omitempty"`
		RuleHash           string         `json:"rule_hash,omitempty"`
		ObjectTypes        []string       `json:"object_types,omitempty"`
		LinkTypes          []string       `json:"link_types,omitempty"`
		Rule               map[string]any `json:"rule,omitempty"`
		Enabled            bool           `json:"enabled"`
	}{
		ID:                 normalized.ID,
		TenantID:           normalized.TenantID,
		EventType:          normalized.EventType,
		SourceDomain:       normalized.SourceDomain,
		SourceRef:          normalized.SourceRef,
		EventSchemaVersion: normalized.EventSchemaVersion,
		RuleHash:           normalized.RuleHash,
		ObjectTypes:        normalized.ObjectTypes,
		LinkTypes:          normalized.LinkTypes,
		Rule:               normalized.Rule,
		Enabled:            normalized.Enabled,
	}
	raw, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("%w: mapping rule definition cannot be encoded", ErrInvalidDefinition)
	}
	return string(raw), nil
}

func findMappingRule(ctx context.Context, registry Registry, tenantID string, id string) (MappingRule, bool, error) {
	rules, err := registry.ListMappingRules(ctx, tenantID)
	if err != nil {
		return MappingRule{}, false, err
	}
	for _, rule := range rules {
		if normalizeID(rule.ID) == normalizeID(id) {
			return rule, true, nil
		}
	}
	return MappingRule{}, false, nil
}

func applyMappingRulePromotionField(rule MappingRule, field string, value string) (MappingRule, bool, error) {
	switch normalizeID(field) {
	case "source_domain":
		next := normalizeID(value)
		if rule.SourceDomain == next {
			return rule, false, nil
		}
		rule.SourceDomain = next
	case "source_ref":
		next := strings.TrimSpace(value)
		if rule.SourceRef == next {
			return rule, false, nil
		}
		rule.SourceRef = next
	case "event_schema_version":
		next := strings.TrimSpace(value)
		if rule.EventSchemaVersion == next {
			return rule, false, nil
		}
		rule.EventSchemaVersion = next
	case "rule_hash":
		next := strings.TrimSpace(value)
		if rule.RuleHash == next {
			return rule, false, nil
		}
		rule.RuleHash = next
	default:
		return MappingRule{}, false, fmt.Errorf("%w: unsupported mapping rule field %s", ErrInvalidDefinition, field)
	}
	return rule, true, nil
}

func appendNormalizedID(values []string, value string) ([]string, bool) {
	normalized := normalizeIDs(values)
	value = normalizeID(value)
	if value == "" || stringSliceContainsNormalized(normalized, value) {
		return normalized, false
	}
	return append(normalized, value), true
}

func metadataPromotionReceiptID(proposalID string, idempotencyKey string) string {
	parts := []string{
		strings.TrimSpace(proposalID),
		strings.TrimSpace(idempotencyKey),
	}
	return "ontology_metadata_promotion_receipt:" + hashMappingRule(map[string]any{"parts": parts})
}

func metadataPromotionBatchReceiptID(proposals []OntologyMetadataProposal, idempotencyKey string) string {
	return "ontology_metadata_promotion_batch_receipt:" + hashMappingRule(map[string]any{"proposal_ids": promotionBatchProposalIDs(proposals), "idempotency_key": strings.TrimSpace(idempotencyKey)})
}

func metadataPromotionRequestHash(req MetadataProposalPromotionApplyRequest, proposal OntologyMetadataProposal, requestTenantID string, actorID string) string {
	payload := legacyMetadataPromotionRequestHashPayload(req, proposal, requestTenantID, actorID)
	payload["proposal_revision"] = proposal.Revision
	payload["proposal_content_hash"] = proposal.ContentHash
	return hashMappingRule(payload)
}

func legacyMetadataPromotionRequestHash(req MetadataProposalPromotionApplyRequest, proposal OntologyMetadataProposal, requestTenantID string, actorID string) string {
	return hashMappingRule(legacyMetadataPromotionRequestHashPayload(req, proposal, requestTenantID, actorID))
}

func legacyMetadataPromotionRequestHashPayload(req MetadataProposalPromotionApplyRequest, proposal OntologyMetadataProposal, requestTenantID string, actorID string) map[string]any {
	return map[string]any{
		"tenant_id":                 strings.TrimSpace(requestTenantID),
		"proposal_id":               strings.TrimSpace(proposal.ID),
		"proposal_tenant_id":        strings.TrimSpace(proposal.TenantID),
		"proposal_target_kind":      strings.TrimSpace(proposal.TargetKind),
		"proposal_target_id":        strings.TrimSpace(proposal.TargetID),
		"proposal_metadata_key":     strings.TrimSpace(proposal.MetadataKey),
		"proposal_proposed_value":   strings.TrimSpace(proposal.ProposedValue),
		"proposal_source":           strings.TrimSpace(proposal.Source),
		"proposal_source_kind":      strings.TrimSpace(string(proposal.SourceKind)),
		"proposal_source_ref":       strings.TrimSpace(proposal.SourceRef),
		"proposal_source_version":   strings.TrimSpace(proposal.SourceVersion),
		"proposal_status":           strings.TrimSpace(string(proposal.Status)),
		"proposal_confidence":       proposal.Confidence,
		"proposal_evidence_refs":    proposal.EvidenceRefs,
		"actor_id":                  strings.TrimSpace(actorID),
		"actor_kind":                strings.TrimSpace(req.ActorKind),
		"confirmation_token":        strings.TrimSpace(req.ConfirmationToken),
		"registry_expected_version": req.RegistryExpectedVersion,
	}
}

func normalizePromotionBatchProposals(proposals []OntologyMetadataProposal) ([]OntologyMetadataProposal, error) {
	out := make([]OntologyMetadataProposal, 0, len(proposals))
	seen := map[string]bool{}
	for _, proposal := range proposals {
		normalized, err := normalizeMetadataProposal(proposal, nil)
		if err != nil {
			return nil, err
		}
		if normalized.ID == "" {
			return nil, fmt.Errorf("%w: proposal id is required", ErrInvalidDefinition)
		}
		if seen[normalized.ID] {
			return nil, fmt.Errorf("%w: duplicate proposal id %s", ErrInvalidDefinition, normalized.ID)
		}
		seen[normalized.ID] = true
		out = append(out, normalized)
	}
	return out, nil
}

func orderPromotionBatchProposals(proposals []OntologyMetadataProposal) []OntologyMetadataProposal {
	out := append([]OntologyMetadataProposal(nil), proposals...)
	sort.SliceStable(out, func(i, j int) bool {
		left := promotionBatchProposalPriority(out[i])
		right := promotionBatchProposalPriority(out[j])
		if left == right {
			return false
		}
		return left < right
	})
	return out
}

func promotionBatchProposalPriority(proposal OntologyMetadataProposal) int {
	targetKind := normalizeID(proposal.TargetKind)
	metadataKey := normalizeID(proposal.MetadataKey)
	switch {
	case metadataKey == "definition" && targetKind == "object_type":
		return 10
	case metadataKey == "definition" && targetKind == "property_type":
		return 20
	case metadataKey == "definition" && targetKind == "link_type":
		return 30
	case metadataKey == "definition" && targetKind == "mapping_rule":
		return 40
	case metadataKey == "allowed_action":
		return 50
	default:
		return 60
	}
}

func promotionBatchProposalIDs(proposals []OntologyMetadataProposal) []string {
	ids := make([]string, 0, len(proposals))
	for _, proposal := range proposals {
		ids = append(ids, strings.TrimSpace(proposal.ID))
	}
	return ids
}

func promotionChangeTargetKey(change MetadataProposalPromotionChange) string {
	return normalizeID(change.TargetKind) + ":" + normalizeID(change.TargetID) + ":" + normalizeID(change.Field)
}

func promotionBatchHasIssue(batch MetadataProposalPromotionBatchReceipt, code PromotionIssueCode) bool {
	for _, issue := range batch.Issues {
		if issue.Code == code {
			return true
		}
	}
	return false
}

func savePromotionReceiptIfConfigured(ctx context.Context, store MetadataProposalPromotionReceiptStore, receipt MetadataProposalPromotionReceipt) error {
	if store == nil {
		return nil
	}
	return store.SavePromotionReceipt(ctx, receipt)
}

func isOntologyNotFound(err error) bool {
	return errors.Is(err, ErrNotFound)
}

func ontologyDefinitionVisibleToTenant(definitionTenantID string, requestedTenantID string) bool {
	definitionTenantID = strings.TrimSpace(definitionTenantID)
	requestedTenantID = strings.TrimSpace(requestedTenantID)
	return requestedTenantID == "" ||
		definitionTenantID == "" ||
		definitionTenantID == "system" ||
		definitionTenantID == requestedTenantID
}

func stringSliceContainsNormalized(values []string, value string) bool {
	value = normalizeID(value)
	for _, item := range values {
		if normalizeID(item) == value {
			return true
		}
	}
	return false
}
