package hil

import (
	"context"
	"strings"
	"time"
)

type MarketingWechatApprovalStatus struct {
	DecisionID         string                             `json:"decision_id"`
	WorkflowInstanceID string                             `json:"workflow_instance_id"`
	Status             string                             `json:"status"`
	DecidedAt          *time.Time                         `json:"decided_at,omitempty"`
	Resolution         *MarketingWechatApprovalResolution `json:"resolution,omitempty"`
}

// GetMarketingWechatApprovalStatus returns committed approval facts for the
// owning business service. It never creates, approves, or reopens a decision.
func (s *MarketingWechatApprovalService) GetMarketingWechatApprovalStatus(ctx context.Context, tenantID, requesterID, decisionID string) (MarketingWechatApprovalStatus, error) {
	if s == nil || s.workflow == nil || strings.TrimSpace(decisionID) == "" {
		return MarketingWechatApprovalStatus{}, ErrInvalidRequest
	}
	detail, err := s.workflow.GetWorkflowInstanceByDecisionID(ctx, tenantID, decisionID)
	if err != nil {
		return MarketingWechatApprovalStatus{}, err
	}
	d := detail.Decision
	if d.ID != decisionID || d.TenantID != tenantID || d.Requester != (ActorRef{Kind: ActorService, ID: requesterID}) || d.ActionTypeID != MarketingWechatDraftApprovalActionType || d.Resource.Kind != MarketingWechatGenerationResourceKind {
		return MarketingWechatApprovalStatus{}, ErrNotFound
	}
	result := MarketingWechatApprovalStatus{DecisionID: d.ID, WorkflowInstanceID: detail.Instance.ID, Status: string(detail.Instance.Status)}
	if detail.Instance.Status == WorkflowRunning && d.Status == DecisionPending {
		result.Status = "pending"
		return result, nil
	}
	for i := len(detail.Receipts) - 1; i >= 0; i-- {
		receipt := detail.Receipts[i]
		if receipt.RequestID != d.ID || receipt.WorkflowInstanceID != detail.Instance.ID || receipt.ArgsHash != d.ArgsHash || receipt.ResolverActorID == "" {
			continue
		}
		resolution, buildErr := BuildMarketingWechatApprovalResolution(DecisionResolution{Decision: d, Receipt: receipt, Actor: ActorRef{Kind: receipt.ResolverActorKind, ID: receipt.ResolverActorID}, Reason: receipt.ResolverReason})
		if buildErr != nil || resolution.Outcome != result.Status {
			continue
		}
		result.Resolution = &resolution
		if !receipt.CreatedAt.IsZero() {
			at := receipt.CreatedAt
			result.DecidedAt = &at
		}
		return result, nil
	}
	return MarketingWechatApprovalStatus{}, ErrConflict
}
