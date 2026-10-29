package hil

import (
	"context"
	"encoding/hex"
	"sort"
	"strings"
	"time"

	"orrery_v2/internal/app/actioncontract"
)

const (
	MarketingWechatDraftApprovalActionType = string(actioncontract.ActionMarketingWechatDraftApproval)
	MarketingWechatGenerationResourceKind  = "marketing_wechat_generation"
)

type MarketingWechatApprovalInput struct {
	TenantID             string
	RequesterID          string
	GenerationID         string
	AccountID            string
	ContentHash          string
	AssetHashes          []string
	Material             BusinessApprovalMaterial
	Artifacts            []BusinessApprovalArtifact
	EvidenceRefs         []string
	IdempotencyKey       string
	SubmissionID         string
	ProjectID            string
	ContentRevision      int
	BundleHash           string
	SupersedesDecisionID string
}

type MarketingWechatApprovalResult struct {
	ArgsHash           string
	DecisionID         string
	WorkflowInstanceID string
	Status             DecisionStatus
	MaterialHash       string
	Revision           int
}

type MarketingWechatApprovalWorkflowReader interface {
	GetWorkflowInstanceByDecisionID(context.Context, string, string) (WorkflowInstanceDetail, error)
}

type MarketingWechatApprovalService struct {
	creator  BusinessApprovalDecisionCreator
	workflow MarketingWechatApprovalWorkflowReader
	now      func() time.Time
}

func NewMarketingWechatApprovalService(creator BusinessApprovalDecisionCreator, workflow MarketingWechatApprovalWorkflowReader, now func() time.Time) *MarketingWechatApprovalService {
	if now == nil {
		now = time.Now
	}
	return &MarketingWechatApprovalService{creator: creator, workflow: workflow, now: now}
}

func (s *MarketingWechatApprovalService) RequestMarketingWechatApproval(ctx context.Context, input MarketingWechatApprovalInput) (MarketingWechatApprovalResult, error) {
	if s == nil || s.creator == nil || s.workflow == nil {
		return MarketingWechatApprovalResult{}, ErrInvalidRequest
	}
	input.TenantID = strings.TrimSpace(input.TenantID)
	input.RequesterID = strings.TrimSpace(input.RequesterID)
	input.GenerationID = strings.TrimSpace(input.GenerationID)
	input.AccountID = strings.TrimSpace(input.AccountID)
	input.ContentHash = strings.TrimSpace(input.ContentHash)
	input.IdempotencyKey = strings.TrimSpace(input.IdempotencyKey)
	if input.TenantID == "" || input.RequesterID == "" || input.GenerationID == "" || input.AccountID == "" ||
		!validMarketingWechatApprovalHash(input.ContentHash) || input.IdempotencyKey == "" {
		return MarketingWechatApprovalResult{}, ErrInvalidRequest
	}
	material, err := NormalizeBusinessApprovalMaterial(input.Material)
	if err != nil {
		return MarketingWechatApprovalResult{}, err
	}
	versioned := strings.TrimSpace(input.SubmissionID) != ""
	if versioned && (!validMarketingVersionIdentifier(input.SubmissionID) || !validMarketingVersionIdentifier(input.ProjectID) || input.ContentRevision < 1 || input.ContentRevision > 2147483647 || !validMarketingBundleHash(input.BundleHash) || (input.SupersedesDecisionID != "" && !validMarketingVersionIdentifier(input.SupersedesDecisionID))) {
		return MarketingWechatApprovalResult{}, ErrInvalidRequest
	}
	if !versioned && (input.ProjectID != "" || input.ContentRevision != 0 || input.BundleHash != "" || input.SupersedesDecisionID != "") {
		return MarketingWechatApprovalResult{}, ErrInvalidRequest
	}
	assetHashes := normalizedBusinessApprovalStrings(input.AssetHashes)
	for _, hash := range assetHashes {
		if !validMarketingWechatApprovalHash(hash) {
			return MarketingWechatApprovalResult{}, ErrInvalidRequest
		}
	}
	evidenceRefs := normalizedBusinessApprovalStrings(input.EvidenceRefs)
	sort.Strings(evidenceRefs)
	artifacts := normalizeBusinessApprovalArtifacts(input.Artifacts)
	materialHash := HashBusinessApprovalMaterial(material)
	resource := ObjectRef{Kind: MarketingWechatGenerationResourceKind, ID: input.GenerationID}
	argsHash := "sha256:" + stableFingerprint(struct {
		GenerationID string
		AccountID    string
		ContentHash  string
		AssetHashes  []string
		MaterialHash string
		EvidenceRefs []string
		Artifacts    []BusinessApprovalArtifact
	}{input.GenerationID, input.AccountID, input.ContentHash, assetHashes, materialHash, evidenceRefs, artifacts})
	if versioned {
		argsHash = "sha256:" + stableFingerprint([]any{argsHash, input.SubmissionID, input.ProjectID, input.ContentRevision, input.BundleHash, input.SupersedesDecisionID})
	}
	payload := map[string]any{
		"approval_request_kind":      "business",
		"approval_source":            "marketing_workflow",
		"business_approval_material": businessApprovalMaterialMap(material),
		"generation_id":              input.GenerationID,
		"account_id":                 input.AccountID,
		"content_hash":               input.ContentHash,
		"asset_hashes":               assetHashes,
		"material_hash":              materialHash,
		"evidence_refs":              evidenceRefs,
		"artifacts":                  artifacts,
	}
	businessKey := "marketing-wechat-draft-approval:" + input.GenerationID
	if versioned {
		businessKey = "marketing-wechat-submission:" + input.SubmissionID
		if lookup, ok := s.creator.(interface {
			LookupMarketingWechatApproval(context.Context, string, string) (DecisionRequest, bool, error)
		}); ok {
			existing, found, lookupErr := lookup.LookupMarketingWechatApproval(ctx, input.TenantID, businessKey)
			if lookupErr != nil {
				return MarketingWechatApprovalResult{}, lookupErr
			}
			if found {
				if existing.ArgsHash != argsHash || existing.Requester != (ActorRef{Kind: ActorService, ID: input.RequesterID}) || existing.Resource != resource {
					return MarketingWechatApprovalResult{}, ErrIdempotencyConflict
				}
				detail, readErr := s.workflow.GetWorkflowInstanceByDecisionID(ctx, input.TenantID, existing.ID)
				if readErr != nil {
					return MarketingWechatApprovalResult{}, readErr
				}
				return MarketingWechatApprovalResult{ArgsHash: existing.ArgsHash, DecisionID: existing.ID, WorkflowInstanceID: detail.Instance.ID, Status: existing.Status, MaterialHash: materialHash, Revision: detail.Instance.Revision}, nil
			}
		}
		payload["submission_id"] = input.SubmissionID
		payload["project_id"] = input.ProjectID
		payload["content_revision"] = input.ContentRevision
		payload["bundle_hash"] = input.BundleHash
		payload["operation"] = "create_draft"
		payload["supersedes_decision_id"] = input.SupersedesDecisionID
		if input.SupersedesDecisionID != "" {
			lifecycle, ok := s.creator.(interface {
				SupersedeMarketingApproval(context.Context, MarketingWechatApprovalInput) error
			})
			if !ok {
				return MarketingWechatApprovalResult{}, ErrInvalidRequest
			}
			if err := lifecycle.SupersedeMarketingApproval(ctx, input); err != nil {
				return MarketingWechatApprovalResult{}, err
			}
		}
	}
	created, err := s.creator.CreateDecision(ctx, CreateDecisionInput{
		TenantID: input.TenantID, Requester: ActorRef{Kind: ActorService, ID: input.RequesterID},
		Action: MarketingWechatDraftApprovalActionType, ActionTypeID: MarketingWechatDraftApprovalActionType,
		Resource: resource, ObjectRefs: []ObjectRef{resource}, SubjectRefs: []ObjectRef{resource},
		ArgsHash: argsHash, Purpose: material.DecisionRequest, RiskLevel: RiskHigh, ApprovalRequired: true,
		PayloadPreview: payload, BusinessKey: businessKey,
		IdempotencyKey: input.IdempotencyKey, ExpiresAt: s.now().UTC().Add(7 * 24 * time.Hour),
	})
	if err != nil {
		return MarketingWechatApprovalResult{}, err
	}
	if versioned && created.Decision.ArgsHash != argsHash {
		return MarketingWechatApprovalResult{}, ErrIdempotencyConflict
	}
	detail, err := s.workflow.GetWorkflowInstanceByDecisionID(ctx, input.TenantID, created.Decision.ID)
	if err != nil {
		return MarketingWechatApprovalResult{}, err
	}
	return MarketingWechatApprovalResult{
		ArgsHash:   argsHash,
		DecisionID: created.Decision.ID, WorkflowInstanceID: detail.Instance.ID, Status: created.Decision.Status,
		MaterialHash: materialHash, Revision: detail.Instance.Revision,
	}, nil
}

func validMarketingBundleHash(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	_, err := hex.DecodeString(value[7:])
	return err == nil
}

func validMarketingVersionIdentifier(value string) bool {
	if len(value) == 0 || len(value) > 500 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("._:-", character) {
			continue
		}
		return false
	}
	return value != "." && value != ".."
}

func validMarketingWechatApprovalHash(value string) bool {
	prefix, digest, ok := strings.Cut(strings.TrimSpace(value), ":")
	return ok && strings.EqualFold(prefix, "sha256") && strings.TrimSpace(digest) != ""
}
