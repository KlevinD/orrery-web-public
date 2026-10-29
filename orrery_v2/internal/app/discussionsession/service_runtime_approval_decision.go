package discussionsession

import (
	"context"
	"strings"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

const discussionApprovalContinuationMode = "approval_continuation"

type RuntimeApprovalDecisionCommand struct {
	ConversationID string
	ApprovalID     string
	// HILDecisionID is populated only by the durable HIL resolution bridge.
	// Public discussion approval calls must not supply it.
	HILDecisionID     string
	HILDecisionStatus string
	HILReceiptID      string
	HILReceiptEffect  string
	// ResumeWithoutLiveResponder is set only by the durable HIL bridge after it
	// confirms that this process has no provider callback waiting for the result.
	ResumeWithoutLiveResponder bool
	ActionID                   string
	Message                    string
	ModifiedInput              string
	ChoiceID                   string
	Remember                   bool
	IdempotencyKey             string
}

func (s *Service) DecideRuntimeApproval(ctx context.Context, viewer ViewerRef, cmd RuntimeApprovalDecisionCommand) (ConversationDTO, error) {
	if s == nil || s.repo == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	if strings.TrimSpace(viewer.ViewerID) == "" ||
		strings.TrimSpace(cmd.ConversationID) == "" ||
		strings.TrimSpace(cmd.ApprovalID) == "" ||
		strings.TrimSpace(cmd.ActionID) == "" ||
		strings.TrimSpace(cmd.IdempotencyKey) == "" {
		return ConversationDTO{}, ErrInvalidRequest
	}
	if strings.TrimSpace(cmd.HILDecisionID) != "" {
		return ConversationDTO{}, ErrForbidden
	}
	if dto, decided, err := s.tryDecideLiveRuntimeApproval(ctx, viewer, cmd); decided || err != nil {
		return dto, err
	}
	unlock := s.lockRunLifecycle(cmd.ConversationID)
	defer unlock()

	view, err := s.repo.GetConversation(ctx, strings.TrimSpace(cmd.ConversationID))
	if err != nil {
		return ConversationDTO{}, err
	}
	event, ok := findRuntimeApprovalEvent(view.Events, cmd.ApprovalID)
	if !ok {
		return ConversationDTO{}, ErrInvalidRequest
	}
	if !runtimeApprovalAssignedToViewer(event, viewer) {
		return ConversationDTO{}, ErrForbidden
	}
	if runtimeApprovalIsHILBacked(event) {
		return ConversationDTO{}, ErrForbidden
	}
	if !runtimeApprovalIsPending(event.Status) {
		return mapConversationDTO(view), nil
	}
	run, ok := findRuntimeApprovalRun(view.Runs, event.Metadata)
	if !ok {
		return ConversationDTO{}, ErrInvalidRequest
	}
	if !strings.EqualFold(strings.TrimSpace(run.Status), "blocked") {
		if runtimeApprovalDecisionUnavailable(view, run) {
			return s.closeUnavailableRuntimeApproval(ctx, view, event, run, viewer, "unavailable")
		}
		if runtimeApprovalRunCanBeBlocked(run.Status) {
			blocked, err := s.blockRuntimeApprovalRun(ctx, view, run, "runtime-approval-blocked-"+event.EventID)
			if err != nil {
				return ConversationDTO{}, err
			}
			view = blocked
			if event, ok = findRuntimeApprovalEvent(view.Events, cmd.ApprovalID); !ok {
				return ConversationDTO{}, ErrInvalidRequest
			}
			if run, ok = findRuntimeApprovalRun(view.Runs, event.Metadata); !ok {
				return ConversationDTO{}, ErrInvalidRequest
			}
		}
		if runtimeApprovalDecisionUnavailable(view, run) {
			return s.closeUnavailableRuntimeApproval(ctx, view, event, run, viewer, "unavailable")
		}
		if !strings.EqualFold(strings.TrimSpace(run.Status), "blocked") {
			return ConversationDTO{}, ErrInvalidRequest
		}
	}
	decision := runtimeApprovalDecisionForAction(cmd.ActionID)
	metadata := cloneStringMap(event.Metadata)
	continuation := runtimecore.ApprovalContinuation{
		Decision:      decision,
		ActionID:      strings.TrimSpace(cmd.ActionID),
		Effect:        approvalContinuationEffect(cmd.ActionID),
		Message:       cmd.Message,
		ModifiedInput: cmd.ModifiedInput,
		ChoiceID:      cmd.ChoiceID,
		Remember:      cmd.Remember,
		Metadata:      metadata,
	}
	decisionStatus := runtimeApprovalDecisionStatus(decision, cmd.ActionID)
	if decisionStatus != "approved" {
		return s.finishRuntimeApprovalDecision(ctx, viewer, view, event, run, metadata, continuation, decisionStatus)
	}
	return s.resumeRuntimeApproval(ctx, viewer, view, event, run, metadata, continuation, cmd.IdempotencyKey)
}

// DecideHILRuntimeApproval applies a resolution that HIL already committed.
// It is intentionally not exposed through the discussion HTTP command: the
// linked decision ID prevents the legacy approval path from becoming a second
// authority after a runtime restart or provider cancellation.
func (s *Service) DecideHILRuntimeApproval(ctx context.Context, viewer ViewerRef, cmd RuntimeApprovalDecisionCommand) (ConversationDTO, error) {
	if s == nil || s.repo == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	if strings.TrimSpace(cmd.ConversationID) == "" || strings.TrimSpace(cmd.HILDecisionID) == "" || strings.TrimSpace(cmd.ActionID) == "" || strings.TrimSpace(cmd.IdempotencyKey) == "" {
		return ConversationDTO{}, ErrInvalidRequest
	}
	// A streamed provider turn owns the conversation lifecycle lock while it
	// waits for its approval responder. Deliver a committed HIL resolution to
	// that live responder before entering the restart-recovery path, otherwise
	// the decision waits on the very provider it is meant to release.
	if dto, decided, err := s.tryDecideLiveHILRuntimeApproval(ctx, viewer, cmd); decided || err != nil {
		return dto, err
	}
	unlock := s.lockRunLifecycle(cmd.ConversationID)
	defer unlock()

	view, err := s.repo.GetConversation(ctx, strings.TrimSpace(cmd.ConversationID))
	if err != nil {
		return ConversationDTO{}, err
	}
	event, ok := findRuntimeApprovalEventByHILDecisionID(view.Events, cmd.HILDecisionID)
	if !ok {
		return ConversationDTO{}, ErrInvalidRequest
	}
	if !runtimeApprovalIsPending(event.Status) {
		if strings.EqualFold(strings.TrimSpace(event.Status), "approved") &&
			strings.EqualFold(strings.TrimSpace(cmd.HILReceiptEffect), "authorize") && strings.TrimSpace(cmd.HILReceiptID) != "" {
			metadata := cloneStringMap(event.Metadata)
			if metadata == nil {
				metadata = map[string]string{}
			}
			if strings.TrimSpace(metadata["hil_receipt_id"]) != strings.TrimSpace(cmd.HILReceiptID) ||
				strings.TrimSpace(metadata["hil_receipt_effect"]) != strings.TrimSpace(cmd.HILReceiptEffect) {
				metadata["hil_decision_status"] = strings.TrimSpace(cmd.HILDecisionStatus)
				metadata["hil_receipt_id"] = strings.TrimSpace(cmd.HILReceiptID)
				metadata["hil_receipt_effect"] = strings.TrimSpace(cmd.HILReceiptEffect)
				event.Metadata = metadata
				if err := s.repo.SaveEvent(ctx, event); err != nil {
					return ConversationDTO{}, err
				}
				view, err = s.repo.GetConversation(ctx, strings.TrimSpace(cmd.ConversationID))
				if err != nil {
					return ConversationDTO{}, err
				}
			}
		}
		if runtimeApprovalDecisionStopsRun(event.Status) {
			if run, hasRun := findRuntimeApprovalRun(view.Runs, event.Metadata); hasRun && runtimeApprovalRunCanBeCompleted(run.Status) {
				updated, finishErr := s.finishRuntimeApprovalRunAfterDecision(ctx, view, run, firstTimeAfter(s.now(), event.CreatedAt), "hil-runtime-approval-reconcile-"+event.EventID)
				if finishErr != nil {
					return ConversationDTO{}, finishErr
				}
				return mapConversationDTO(updated), nil
			}
		}
		return mapConversationDTO(view), nil
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
	decidedAt := firstTimeAfter(s.now(), event.CreatedAt)
	metadata["approval_id"] = event.EventID
	metadata["approval_mode"] = "manual"
	metadata["approval_decided_at"] = decidedAt.UTC().Format(time.RFC3339Nano)
	if value := strings.TrimSpace(viewer.ViewerID); value != "" {
		metadata["approval_decided_by"] = value
	}
	decision := runtimeApprovalDecisionForAction(cmd.ActionID)
	continuation := runtimecore.ApprovalContinuation{
		Decision: decision,
		ActionID: strings.TrimSpace(cmd.ActionID),
		Effect:   approvalContinuationEffect(cmd.ActionID),
		Message:  strings.TrimSpace(cmd.Message),
		Metadata: metadata,
	}
	status := runtimeApprovalDecisionStatus(decision, cmd.ActionID)
	run, hasRun := findRuntimeApprovalRun(view.Runs, event.Metadata)
	if status != "approved" {
		if hasRun && runtimeApprovalRunCanBeCompleted(run.Status) {
			return s.finishRuntimeApprovalDecision(ctx, viewer, view, event, run, metadata, continuation, status)
		}
		return s.persistHILRuntimeApprovalDecision(ctx, view, event, metadata, continuation, status, viewer)
	}
	if hasRun && cmd.ResumeWithoutLiveResponder && strings.EqualFold(strings.TrimSpace(run.Status), "running") {
		recovered, recoverErr := s.blockRuntimeApprovalRun(ctx, view, run, "hil-runtime-approval-recovered-"+event.EventID)
		if recoverErr != nil {
			return ConversationDTO{}, recoverErr
		}
		view = recovered
		if event, ok = findRuntimeApprovalEventByHILDecisionID(view.Events, cmd.HILDecisionID); !ok {
			return ConversationDTO{}, ErrInvalidRequest
		}
		if run, hasRun = findRuntimeApprovalRun(view.Runs, event.Metadata); !hasRun {
			return ConversationDTO{}, ErrInvalidRequest
		}
	}
	if hasRun && s.runtimeApprovalRunCanBeResumedAfterHILResolution(view, run) {
		return s.resumeRuntimeApproval(ctx, viewer, view, event, run, metadata, continuation, cmd.IdempotencyKey)
	}
	metadata["approval_resume_required"] = "true"
	metadata["approval_resume_reason"] = "runtime responder is no longer available"
	return s.persistHILRuntimeApprovalDecision(ctx, view, event, metadata, continuation, status, viewer)
}

func (s *Service) persistHILRuntimeApprovalDecision(ctx context.Context, view ConversationRecordView, event EventRecord, metadata map[string]string, continuation runtimecore.ApprovalContinuation, status string, viewer ViewerRef) (ConversationDTO, error) {
	decided := decidedRuntimeApprovalEvent(event, metadata, continuation, status, viewer, "", firstTimeAfter(s.now(), event.CreatedAt))
	if err := s.repo.SaveEvent(ctx, decided); err != nil {
		return ConversationDTO{}, err
	}
	if err := s.appendRealtimeSnapshotInvalidated(ctx, view.ConversationID, "runtime_approval_decided", "hil-"+event.EventID); err != nil {
		return ConversationDTO{}, err
	}
	updated, err := s.repo.GetConversation(ctx, view.ConversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	return mapConversationDTO(updated), nil
}

// ExpireHILRuntimeApproval closes the local runtime bridge when the linked
// HIL hold has elapsed. It deliberately does not mutate the HIL request: HIL
// remains the authority for its own receipt, while the legacy discussion
// event and provider run cannot stay indefinitely actionable.
func (s *Service) ExpireHILRuntimeApproval(ctx context.Context, conversationID string, hilDecisionID string, reason string) (ConversationDTO, error) {
	if s == nil || s.repo == nil {
		return ConversationDTO{}, ErrServiceDependency
	}
	conversationID = strings.TrimSpace(conversationID)
	hilDecisionID = strings.TrimSpace(hilDecisionID)
	if conversationID == "" || hilDecisionID == "" {
		return ConversationDTO{}, ErrInvalidRequest
	}
	unlock := s.lockRunLifecycle(conversationID)
	defer unlock()
	view, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	event, ok := findRuntimeApprovalEventByHILDecisionID(view.Events, hilDecisionID)
	if !ok || !runtimeApprovalIsPending(event.Status) {
		return mapConversationDTO(view), nil
	}
	metadata := cloneStringMap(event.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["approval_decision"] = "expired"
	metadata["approval_closed_reason"] = firstNonBlank(strings.TrimSpace(reason), "hil_decision_expired")
	metadata["hil_decision_status"] = firstNonBlank(metadata["hil_decision_status"], "expired")
	metadata["approval_decided_by"] = "hil:expiry"
	closed := event
	closed.Status = "expired"
	closed.Metadata = metadata
	closed.CreatedAt = firstTimeAfter(s.now(), event.CreatedAt)
	if err := s.repo.SaveEvent(ctx, closed); err != nil {
		return ConversationDTO{}, err
	}
	if run, hasRun := findRuntimeApprovalRun(view.Runs, event.Metadata); hasRun && runtimeApprovalRunCanBeCompleted(run.Status) {
		updated, finishErr := s.finishRuntimeApprovalRunAfterDecision(ctx, view, run, closed.CreatedAt, "runtime-approval-expired-"+event.EventID)
		if finishErr != nil {
			return ConversationDTO{}, finishErr
		}
		view = updated
	}
	if err := s.appendRealtimeSnapshotInvalidated(ctx, conversationID, "runtime_approval_expired", event.EventID); err != nil {
		return ConversationDTO{}, err
	}
	updated, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	return mapConversationDTO(updated), nil
}

// RecoverExpiredHILRuntimeApprovals is a small startup sweep. The sweep is
// intentionally repository-backed so a process restart cannot strand runtime
// events whose in-memory coordinator timer was lost.
func (s *Service) RecoverExpiredHILRuntimeApprovals(ctx context.Context, now time.Time) error {
	if s == nil || s.repo == nil {
		return nil
	}
	if now.IsZero() {
		now = s.now()
	}
	conversations, err := s.repo.ListConversations(ctx)
	if err != nil {
		return err
	}
	for _, summary := range conversations {
		view, getErr := s.repo.GetConversation(ctx, summary.ConversationID)
		if getErr != nil {
			return getErr
		}
		for _, event := range view.Events {
			if event.Kind != discussionRuntimeApprovalEventKind || !runtimeApprovalIsPending(event.Status) {
				continue
			}
			hilDecisionID := strings.TrimSpace(event.Metadata["hil_decision_id"])
			if hilDecisionID == "" {
				continue
			}
			expiresAt := runtimeApprovalHILExpiry(event)
			if expiresAt.IsZero() || now.Before(expiresAt) {
				continue
			}
			if _, expireErr := s.ExpireHILRuntimeApproval(ctx, view.ConversationID, hilDecisionID, "hil_decision_expired"); expireErr != nil {
				return expireErr
			}
		}
	}
	return nil
}

func runtimeApprovalHILExpiry(event EventRecord) time.Time {
	if value := strings.TrimSpace(event.Metadata["hil_decision_expires_at"]); value != "" {
		if parsed, err := time.Parse(time.RFC3339Nano, value); err == nil {
			return parsed.UTC()
		}
	}
	if !event.CreatedAt.IsZero() {
		return event.CreatedAt.Add(defaultRuntimeApprovalHoldTimeout)
	}
	return time.Time{}
}

func runtimeApprovalIsHILBacked(event EventRecord) bool {
	return strings.TrimSpace(event.Metadata["hil_decision_id"]) != "" ||
		strings.EqualFold(strings.TrimSpace(event.Metadata["approval_hil_required"]), "true")
}

func findRuntimeApprovalEventByHILDecisionID(events []EventRecord, decisionID string) (EventRecord, bool) {
	decisionID = strings.TrimSpace(decisionID)
	if decisionID == "" {
		return EventRecord{}, false
	}
	var latest EventRecord
	found := false
	for _, event := range events {
		if event.Kind == discussionRuntimeApprovalEventKind && strings.EqualFold(strings.TrimSpace(event.Metadata["hil_decision_id"]), decisionID) {
			if runtimeApprovalIsPending(event.Status) {
				if !found || !runtimeApprovalIsPending(latest.Status) || event.CreatedAt.After(latest.CreatedAt) {
					latest, found = event, true
				}
			} else if !found || (!runtimeApprovalIsPending(latest.Status) && event.CreatedAt.After(latest.CreatedAt)) {
				latest, found = event, true
			}
		}
	}
	return latest, found
}

func (s *Service) tryDecideLiveRuntimeApproval(ctx context.Context, viewer ViewerRef, cmd RuntimeApprovalDecisionCommand) (ConversationDTO, bool, error) {
	if s == nil || s.repo == nil || s.runtimeApprovals == nil {
		return ConversationDTO{}, false, nil
	}
	view, err := s.repo.GetConversation(ctx, strings.TrimSpace(cmd.ConversationID))
	if err != nil {
		return ConversationDTO{}, false, err
	}
	event, ok := findRuntimeApprovalEvent(view.Events, cmd.ApprovalID)
	if !ok {
		return ConversationDTO{}, false, nil
	}
	if !runtimeApprovalAssignedToViewer(event, viewer) {
		return ConversationDTO{}, true, ErrForbidden
	}
	if runtimeApprovalIsHILBacked(event) {
		return ConversationDTO{}, true, ErrForbidden
	}
	if !runtimeApprovalIsPending(event.Status) {
		return mapConversationDTO(view), true, nil
	}
	if terminalConversationStatus(view.Status) {
		return ConversationDTO{}, false, nil
	}
	run, ok := findRuntimeApprovalRun(view.Runs, event.Metadata)
	if !ok || !strings.EqualFold(strings.TrimSpace(run.Status), "running") {
		return ConversationDTO{}, false, nil
	}
	decision := runtimeApprovalDecisionForAction(cmd.ActionID)
	metadata := cloneStringMap(event.Metadata)
	continuation := runtimecore.ApprovalContinuation{
		Decision:      decision,
		ActionID:      strings.TrimSpace(cmd.ActionID),
		Effect:        approvalContinuationEffect(cmd.ActionID),
		Message:       cmd.Message,
		ModifiedInput: cmd.ModifiedInput,
		ChoiceID:      cmd.ChoiceID,
		Remember:      cmd.Remember,
		Metadata:      metadata,
	}
	decisionStatus := runtimeApprovalDecisionStatus(decision, cmd.ActionID)
	decided, err := s.runtimeApprovals.TryDecide(ctx, liveRuntimeApprovalDecision{
		Event:        event,
		Run:          run,
		Metadata:     metadata,
		Continuation: continuation,
		Status:       decisionStatus,
		Viewer:       viewer,
	})
	if !decided || err != nil {
		return ConversationDTO{}, decided, err
	}
	updated, err := s.repo.GetConversation(ctx, view.ConversationID)
	if err != nil {
		return ConversationDTO{}, true, err
	}
	decidedEvent, ok := findRuntimeApprovalEvent(updated.Events, cmd.ApprovalID)
	if ok && runtimeApprovalDecisionStopsRun(decidedEvent.Status) {
		updated, err = s.finishLiveRuntimeApprovalRunAfterDecision(ctx, view.ConversationID, cmd.ApprovalID, "live-runtime-approval-decided-"+event.EventID)
		if err != nil {
			return ConversationDTO{}, true, err
		}
	}
	return mapConversationDTO(updated), true, nil
}

func runtimeApprovalAssignedToViewer(event EventRecord, viewer ViewerRef) bool {
	assignedTo := strings.TrimSpace(event.Metadata["approval_assigned_to"])
	return assignedTo == "" || strings.EqualFold(assignedTo, strings.TrimSpace(viewer.ViewerID))
}

func (s *Service) resumeRuntimeApproval(ctx context.Context, viewer ViewerRef, view ConversationRecordView, event EventRecord, blockedRun RunRecord, metadata map[string]string, continuation runtimecore.ApprovalContinuation, idempotencyKey string) (ConversationDTO, error) {
	if s.mentionResponder == nil {
		return ConversationDTO{}, ErrRuntimeUnavailable
	}
	source := sourceRecordForMessage(latestRuntimeApprovalSourceMessage(view.Messages))
	turn := s.newApprovalDiscussionTurnCommand(view, source, viewer, event, blockedRun, metadata, continuation, idempotencyKey)
	return s.executeDiscussionTurn(ctx, turn)
}
func (s *Service) finishRuntimeApprovalDecision(ctx context.Context, viewer ViewerRef, view ConversationRecordView, event EventRecord, blockedRun RunRecord, metadata map[string]string, continuation runtimecore.ApprovalContinuation, status string) (ConversationDTO, error) {
	now := firstTimeAfter(s.now(), blockedRun.CompletedAt)
	decisionEvent := decidedRuntimeApprovalEvent(event, metadata, continuation, status, viewer, "", now)
	if err := s.repo.SaveEvent(ctx, decisionEvent); err != nil {
		return ConversationDTO{}, err
	}
	updated, err := s.finishRuntimeApprovalRunAfterDecision(ctx, view, blockedRun, now, "runtime-approval-decided-"+event.EventID)
	if err != nil {
		return ConversationDTO{}, err
	}
	return mapConversationDTO(updated), nil
}

func (s *Service) blockRuntimeApprovalRun(ctx context.Context, view ConversationRecordView, run RunRecord, idempotency string) (ConversationRecordView, error) {
	now := firstTimeAfter(s.now(), firstTime(run.StartedAt, run.CreatedAt, view.UpdatedAt))
	run.Status = "blocked"
	run.CompletedAt = now
	if err := s.repo.SaveRun(ctx, run); err != nil {
		return ConversationRecordView{}, err
	}
	updated, err := s.repo.GetConversation(ctx, view.ConversationID)
	if err != nil {
		return ConversationRecordView{}, err
	}
	updated.Status = "blocked"
	updated.UpdatedAt = now
	if err := s.repo.SaveConversation(ctx, updated.ConversationRecord); err != nil {
		return ConversationRecordView{}, err
	}
	if err := s.appendRealtimeRunCompletedWithID(ctx, run, "discussion-run-blocked-"+run.RunID+"-"+idempotency); err != nil {
		return ConversationRecordView{}, err
	}
	if err := s.appendRealtimeSnapshotInvalidated(ctx, updated.ConversationID, "runtime_approval_requested", idempotency); err != nil {
		return ConversationRecordView{}, err
	}
	if err := s.appendRealtimeConversationListInvalidated(ctx, updated, idempotency); err != nil {
		return ConversationRecordView{}, err
	}
	return updated, nil
}

func (s *Service) closeUnavailableRuntimeApproval(ctx context.Context, view ConversationRecordView, event EventRecord, run RunRecord, viewer ViewerRef, status string) (ConversationDTO, error) {
	status = strings.TrimSpace(strings.ToLower(status))
	if status == "" {
		status = "unavailable"
	}
	metadata := cloneStringMap(event.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["approval_decision"] = status
	metadata["approval_closed_reason"] = runtimeApprovalUnavailableReason(view, run)
	if viewer.ViewerID != "" {
		metadata["approval_decided_by"] = viewer.ViewerID
	}
	now := firstTimeAfter(s.now(), firstTime(run.CompletedAt, event.CreatedAt, view.UpdatedAt))
	closed := event
	closed.Status = status
	closed.Metadata = metadata
	closed.CreatedAt = firstTime(now, event.CreatedAt)
	if err := s.repo.SaveEvent(ctx, closed); err != nil {
		return ConversationDTO{}, err
	}
	if err := s.appendRealtimeSnapshotInvalidated(ctx, view.ConversationID, "runtime_approval_decided", "runtime-approval-unavailable-"+event.EventID); err != nil {
		return ConversationDTO{}, err
	}
	updated, err := s.repo.GetConversation(ctx, view.ConversationID)
	if err != nil {
		return ConversationDTO{}, err
	}
	return mapConversationDTO(updated), nil
}

func (s *Service) finishLiveRuntimeApprovalRunAfterDecision(ctx context.Context, conversationID string, approvalID string, idempotency string) (ConversationRecordView, error) {
	unlock := s.lockRunLifecycle(conversationID)
	defer unlock()

	current, err := s.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return ConversationRecordView{}, err
	}
	event, ok := findRuntimeApprovalEvent(current.Events, approvalID)
	if !ok || !runtimeApprovalDecisionStopsRun(event.Status) {
		return current, nil
	}
	run, ok := findRuntimeApprovalRun(current.Runs, event.Metadata)
	if !ok || !strings.EqualFold(strings.TrimSpace(run.Status), "running") {
		return current, nil
	}
	return s.finishRuntimeApprovalRunAfterDecision(ctx, current, run, firstTimeAfter(s.now(), run.StartedAt), idempotency)
}

func (s *Service) finishRuntimeApprovalRunAfterDecision(ctx context.Context, view ConversationRecordView, run RunRecord, now time.Time, idempotency string) (ConversationRecordView, error) {
	now = firstTimeAfter(now, firstTime(run.CompletedAt, run.StartedAt, run.CreatedAt, view.UpdatedAt))
	run.Status = "completed"
	run.CompletedAt = now
	if err := s.repo.SaveRun(ctx, run); err != nil {
		return ConversationRecordView{}, err
	}
	updated, err := s.repo.GetConversation(ctx, view.ConversationID)
	if err != nil {
		return ConversationRecordView{}, err
	}
	updated.Status = "completed"
	updated.UpdatedAt = now
	updated.ViewedAt = now
	if err := s.repo.SaveConversation(ctx, updated.ConversationRecord); err != nil {
		return ConversationRecordView{}, err
	}
	if err := s.appendRealtimeRunCompletedWithID(ctx, run, "discussion-run-completed-"+run.RunID+"-"+idempotency); err != nil {
		return ConversationRecordView{}, err
	}
	if err := s.appendRealtimeSnapshotInvalidated(ctx, updated.ConversationID, "runtime_approval_decided", idempotency); err != nil {
		return ConversationRecordView{}, err
	}
	if err := s.appendRealtimeConversationListInvalidated(ctx, updated, idempotency); err != nil {
		return ConversationRecordView{}, err
	}
	return updated, nil
}

func findRuntimeApprovalEvent(events []EventRecord, approvalID string) (EventRecord, bool) {
	approvalID = strings.TrimSpace(approvalID)
	var requestIDMatch EventRecord
	requestIDMatches := 0
	for _, event := range events {
		if event.Kind != discussionRuntimeApprovalEventKind {
			continue
		}
		requestID := strings.TrimSpace(event.Metadata[runtimecore.ApprovalMetadataRequestID])
		if strings.EqualFold(event.EventID, approvalID) {
			return event, true
		}
		if strings.EqualFold(requestID, approvalID) {
			requestIDMatch = event
			requestIDMatches++
		}
	}
	if requestIDMatches == 1 {
		return requestIDMatch, true
	}
	return EventRecord{}, false
}

func findRuntimeApprovalEventForRun(events []EventRecord, runID string, requestID string) (EventRecord, bool) {
	runID = strings.TrimSpace(runID)
	requestID = strings.TrimSpace(requestID)
	for _, event := range events {
		if event.Kind != discussionRuntimeApprovalEventKind {
			continue
		}
		if !strings.EqualFold(strings.TrimSpace(event.Metadata["approval_run_id"]), runID) {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(event.Metadata[runtimecore.ApprovalMetadataRequestID]), requestID) {
			return event, true
		}
	}
	return EventRecord{}, false
}

func findRuntimeApprovalRun(runs []RunRecord, metadata map[string]string) (RunRecord, bool) {
	runID := strings.TrimSpace(metadata["approval_run_id"])
	for _, run := range runs {
		if strings.EqualFold(strings.TrimSpace(run.RunID), runID) {
			return run, true
		}
	}
	return RunRecord{}, false
}

func latestRuntimeApprovalSourceMessage(messages []MessageRecord) MessageDTO {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(messages[i].Role), "user") {
			return triggerMessageDTOFromRecord(messages[i])
		}
	}
	if len(messages) > 0 {
		return triggerMessageDTOFromRecord(messages[len(messages)-1])
	}
	return MessageDTO{}
}

func sourceRecordForMessage(message MessageDTO) MessageRecord {
	return MessageRecord{
		MessageID:      message.MessageID,
		ConversationID: message.ConversationID,
		Role:           message.Role,
		AuthorID:       message.AuthorID,
		AuthorName:     message.AuthorName,
		Content:        message.Content,
		Metadata:       cloneStringMap(message.Metadata),
		CreatedAt:      message.CreatedAt,
	}
}

func decidedRuntimeApprovalEvent(event EventRecord, metadata map[string]string, continuation runtimecore.ApprovalContinuation, status string, viewer ViewerRef, continuationRunID string, at time.Time) EventRecord {
	decided := event
	decided.Status = status
	decided.CreatedAt = firstTime(at, event.CreatedAt)
	decided.Metadata = cloneStringMap(metadata)
	if decided.Metadata == nil {
		decided.Metadata = map[string]string{}
	}
	decided.Metadata["approval_decision"] = status
	decided.Metadata["approval_action_id"] = continuation.ActionID
	if viewer.ViewerID != "" {
		decided.Metadata["approval_decided_by"] = viewer.ViewerID
	}
	if continuationRunID != "" {
		decided.Metadata["approval_continuation_run_id"] = continuationRunID
	}
	return decided
}

func runtimeApprovalDecisionForAction(actionID string) runtimecore.ApprovalDecision {
	switch strings.ToLower(strings.TrimSpace(actionID)) {
	case "deny", "reject", "dismiss":
		return runtimecore.ApprovalDecisionDenied
	case "approve_once", "approve_session", "approve", "approve_edited", "other":
		return runtimecore.ApprovalDecisionApproved
	default:
		return runtimecore.ApprovalDecisionApproved
	}
}

func runtimeApprovalDecisionStatus(decision runtimecore.ApprovalDecision, actionID string) string {
	if decision == runtimecore.ApprovalDecisionDenied {
		return "denied"
	}
	if strings.EqualFold(strings.TrimSpace(actionID), "needs_context") {
		return "needs_input"
	}
	return "approved"
}

func runtimeApprovalDecisionStopsRun(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "denied", "rejected", "dismissed", "needs_input":
		return true
	default:
		return false
	}
}

func runtimeApprovalDecisionUnavailable(view ConversationRecordView, run RunRecord) bool {
	conversationStatus := strings.TrimSpace(strings.ToLower(view.Status))
	runStatus := strings.TrimSpace(strings.ToLower(run.Status))
	if runtimeApprovalTerminalConversationUnavailable(conversationStatus) {
		return true
	}
	if conversationStatus == "completed" {
		return runStatus != "blocked" && runStatus != "running" && runStatus != "started"
	}
	return terminalRuntimeStatus(run.Status)
}

func runtimeApprovalUnavailableReason(view ConversationRecordView, run RunRecord) string {
	if terminalConversationStatus(view.Status) {
		return "conversation_" + strings.TrimSpace(strings.ToLower(view.Status))
	}
	status := strings.TrimSpace(strings.ToLower(run.Status))
	if status != "" {
		return "run_" + status
	}
	return "runtime_unavailable"
}

func runtimeApprovalTerminalConversationUnavailable(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "failed", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func runtimeApprovalRunCanBeBlocked(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "running", "started":
		return true
	default:
		return false
	}
}

// runtimeApprovalRunCanBeCompleted reports whether a durable decision can
// close the provider run without attempting a continuation. A blocked run is
// already stopped at the provider boundary, while running/started is closed
// by the same idempotent lifecycle mutation used by the legacy path.
func runtimeApprovalRunCanBeCompleted(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "running", "started", "blocked":
		return true
	default:
		return false
	}
}

// runtimeApprovalRunCanBeResumedAfterHILResolution allows a continuation after
// either a durable block or the provider's approval hold timeout. A live run
// remains owned by its provider goroutine and receives the decision through
// TryDecide instead of starting a competing continuation.
func (s *Service) runtimeApprovalRunCanBeResumedAfterHILResolution(view ConversationRecordView, run RunRecord) bool {
	if s == nil || s.mentionResponder == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(run.Status)) {
	case "blocked", "completed":
	default:
		return false
	}
	return !runtimeApprovalTerminalConversationUnavailable(view.Status)
}

func terminalConversationStatus(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "completed", "failed", "cancelled", "canceled":
		return true
	default:
		return false
	}
}

func terminalRuntimeStatus(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "completed", "failed", "cancelled", "canceled":
		return true
	default:
		return false
	}
}
