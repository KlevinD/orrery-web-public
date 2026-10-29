package discussionsession

import (
	"context"
	"errors"
	"strings"
)

// RecoverExpiredHILRuntimeApprovals is exposed for composition roots that
// prefer an explicit startup ordering instead of the asynchronous setter
// sweep. It is safe to call repeatedly.
func (c *RuntimeApprovalCoordinator) RecoverExpiredHILRuntimeApprovals(ctx context.Context) error {
	if c == nil {
		return nil
	}
	enabled, err := c.runtimeHILHumanReviewEnabled(ctx)
	if err != nil || !enabled {
		return err
	}
	c.mu.Lock()
	resolver := c.hilRuntimeResolver
	c.mu.Unlock()
	if recovery, ok := resolver.(RuntimeHILRuntimeApprovalRecovery); ok {
		return recovery.RecoverExpiredHILRuntimeApprovals(ctx, c.now())
	}
	return nil
}

// ReconcileHILRuntimeApprovals repairs ordering windows after an observer race
// or a process restart. A local HIL bridge is persisted before CreateDecision,
// so an event without a decision ID can safely replay the same idempotent HIL
// request and be linked afterwards.
func (c *RuntimeApprovalCoordinator) ReconcileHILRuntimeApprovals(ctx context.Context) error {
	if c == nil || c.repo == nil {
		return nil
	}
	enabled, err := c.runtimeHILHumanReviewEnabled(ctx)
	if err != nil || !enabled {
		return err
	}
	c.hilReconcileMu.Lock()
	defer c.hilReconcileMu.Unlock()
	firstErr := c.retryDeferredHILResolutions(runtimeApprovalDurableContext(ctx))
	c.mu.Lock()
	service := c.hil
	c.mu.Unlock()
	if service == nil {
		return firstErr
	}
	conversations, err := c.repo.ListConversations(runtimeApprovalDurableContext(ctx))
	if err != nil {
		return firstNonNilRuntimeHILError(firstErr, err)
	}
	for _, summary := range conversations {
		view, getErr := c.repo.GetConversation(runtimeApprovalDurableContext(ctx), summary.ConversationID)
		if getErr != nil {
			firstErr = firstNonNilRuntimeHILError(firstErr, getErr)
			continue
		}
		for _, event := range view.Events {
			if event.Kind != discussionRuntimeApprovalEventKind || !runtimeApprovalIsHILBacked(event) {
				continue
			}
			linked := event
			if runtimeApprovalIsPending(event.Status) && strings.TrimSpace(event.Metadata["hil_decision_id"]) == "" {
				meta, req, ok := runtimeHILBridgeRequest(event)
				if !ok {
					firstErr = firstNonNilRuntimeHILError(firstErr, errRuntimeHILBridgeUnavailable)
					continue
				}
				result, updated, ensureErr := c.ensureRuntimeHILDecision(runtimeApprovalDurableContext(ctx), view.ConversationRecord, meta, req, event)
				if ensureErr != nil {
					firstErr = firstNonNilRuntimeHILError(firstErr, ensureErr)
					continue
				}
				linked = updated
				if result != nil {
					c.applyCreatedRuntimeHILDecision(runtimeApprovalDurableContext(ctx), view.ConversationRecord, linked, *result)
				}
			}
			if strings.TrimSpace(linked.Metadata["hil_decision_id"]) == "" {
				continue
			}
			if _, ok := service.(RuntimeHILDecisionResolutionLookup); !ok {
				continue
			}
			if reconcileErr := c.reconcileHILRuntimeApproval(runtimeApprovalDurableContext(ctx), view.ConversationRecord, linked); reconcileErr != nil && !errors.Is(reconcileErr, errRuntimeHILResolutionPending) {
				firstErr = firstNonNilRuntimeHILError(firstErr, reconcileErr)
			}
		}
	}
	return firstErr
}

func (c *RuntimeApprovalCoordinator) runtimeHILHumanReviewEnabled(ctx context.Context) (bool, error) {
	if c == nil || c.humanReviewEnabled == nil {
		return true, nil
	}
	return c.humanReviewEnabled(runtimeApprovalDurableContext(ctx))
}

func firstNonNilRuntimeHILError(current error, candidate error) error {
	if current != nil {
		return current
	}
	return candidate
}
