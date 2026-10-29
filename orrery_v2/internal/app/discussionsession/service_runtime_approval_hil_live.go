package discussionsession

import (
	"context"
	"strings"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

// tryDecideLiveHILRuntimeApproval releases a streamed provider that currently
// owns the conversation lifecycle lock. Restart recovery continues through the
// locked durable path when no provider turn is active.
func (s *Service) tryDecideLiveHILRuntimeApproval(ctx context.Context, viewer ViewerRef, cmd RuntimeApprovalDecisionCommand) (ConversationDTO, bool, error) {
	if s == nil || s.repo == nil || s.runtimeApprovals == nil || !s.runLifecycleIsActive(cmd.ConversationID) {
		return ConversationDTO{}, false, nil
	}
	view, err := s.repo.GetConversation(ctx, strings.TrimSpace(cmd.ConversationID))
	if err != nil {
		return ConversationDTO{}, false, err
	}
	event, ok := findRuntimeApprovalEventByHILDecisionID(view.Events, cmd.HILDecisionID)
	if !ok {
		return ConversationDTO{}, false, nil
	}
	if !runtimeApprovalIsPending(event.Status) {
		return mapConversationDTO(view), true, nil
	}
	metadata := cloneStringMap(event.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	if value := strings.TrimSpace(cmd.HILDecisionStatus); value != "" {
		metadata["hil_decision_status"] = value
	}
	if value := strings.TrimSpace(cmd.HILReceiptID); value != "" {
		metadata["hil_receipt_id"] = value
	}
	if value := strings.TrimSpace(cmd.HILReceiptEffect); value != "" {
		metadata["hil_receipt_effect"] = value
	}
	if value := strings.TrimSpace(cmd.Message); value != "" {
		metadata["approval_hil_resolution_reason"] = value
	}
	decision := runtimeApprovalDecisionForAction(cmd.ActionID)
	continuation := runtimecore.ApprovalContinuation{
		Decision: decision, ActionID: strings.TrimSpace(cmd.ActionID),
		Effect: approvalContinuationEffect(cmd.ActionID), Message: strings.TrimSpace(cmd.Message), Metadata: metadata,
	}
	decided, err := s.runtimeApprovals.TryDecide(ctx, liveRuntimeApprovalDecision{
		Event: event, Metadata: metadata, Continuation: continuation,
		Status: runtimeApprovalDecisionStatus(decision, cmd.ActionID), Viewer: viewer,
	})
	if !decided || err != nil {
		return ConversationDTO{}, decided, err
	}
	updated, err := s.repo.GetConversation(ctx, view.ConversationID)
	if err != nil {
		return ConversationDTO{}, true, err
	}
	return mapConversationDTO(updated), true, nil
}
