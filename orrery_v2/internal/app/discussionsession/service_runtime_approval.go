package discussionsession

import (
	"context"
	"strings"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

const discussionRuntimeApprovalEventKind = "discussion_runtime_approval_requested"

func conversationHasPendingRuntimeApproval(view ConversationRecordView) bool {
	for _, event := range view.Events {
		if event.Kind != discussionRuntimeApprovalEventKind || !runtimeApprovalIsPending(event.Status) {
			continue
		}
		run, ok := findRuntimeApprovalRun(view.Runs, event.Metadata)
		if ok && strings.EqualFold(strings.TrimSpace(run.Status), "blocked") {
			return true
		}
	}
	return false
}

func (s *Service) saveRunAndRuntimeApprovalEvent(ctx context.Context, conversationID string, run RunRecord, response DiscussionAgentResponse, at time.Time) error {
	s.applyResponseDebug(&run, response)
	if err := s.repo.SaveRun(ctx, run); err != nil {
		return err
	}
	return s.saveRuntimeApprovalEvent(ctx, conversationID, run, response, at)
}

func (s *Service) saveRuntimeApprovalEvent(ctx context.Context, conversationID string, run RunRecord, response DiscussionAgentResponse, at time.Time) error {
	if !strings.EqualFold(strings.TrimSpace(run.Status), "blocked") && !strings.EqualFold(strings.TrimSpace(response.Status), "blocked") {
		return nil
	}
	metadata := discussionRuntimeApprovalMetadata(conversationID, run, response)
	requestID := strings.TrimSpace(metadata[runtimecore.ApprovalMetadataRequestID])
	if requestID == "" {
		return nil
	}
	if existing, ok := s.existingRuntimeApprovalEvent(ctx, conversationID, run.RunID, requestID); ok && !strings.EqualFold(strings.TrimSpace(existing.Status), "pending") {
		return nil
	}
	event := EventRecord{
		EventID:        discussionRuntimeApprovalEventID(conversationID, run.RunID, requestID),
		ConversationID: conversationID,
		Kind:           discussionRuntimeApprovalEventKind,
		Status:         "pending",
		Summary:        discussionRuntimeApprovalSummary(metadata),
		Metadata:       metadata,
		CreatedAt:      firstTime(at, run.CompletedAt, s.now()),
	}
	if err := s.repo.SaveEvent(ctx, event); err != nil {
		return err
	}
	return s.appendRealtimeSnapshotInvalidated(ctx, conversationID, "runtime_approval_requested", event.EventID)
}

func (s *Service) existingRuntimeApprovalEvent(ctx context.Context, conversationID string, runID string, requestID string) (EventRecord, bool) {
	if s == nil || s.repo == nil {
		return EventRecord{}, false
	}
	view, err := s.repo.GetConversation(ctx, strings.TrimSpace(conversationID))
	if err != nil {
		return EventRecord{}, false
	}
	return findRuntimeApprovalEventForRun(view.Events, strings.TrimSpace(runID), strings.TrimSpace(requestID))
}

func discussionRuntimeApprovalMetadata(conversationID string, run RunRecord, response DiscussionAgentResponse) map[string]string {
	metadata := cloneStringMap(response.ProviderMetadata)
	if len(metadata) == 0 {
		metadata = cloneStringMap(run.ProviderMetadata)
	}
	if len(metadata) == 0 {
		return nil
	}
	putRuntimeApprovalMetadata(metadata, "approval_conversation_id", conversationID)
	putRuntimeApprovalMetadata(metadata, "approval_turn_id", run.TurnID)
	putRuntimeApprovalMetadata(metadata, "approval_run_id", run.RunID)
	putRuntimeApprovalMetadata(metadata, "approval_agent_id", run.AgentID)
	putRuntimeApprovalMetadata(metadata, "approval_runtime_kind", run.RuntimeKind)
	putRuntimeApprovalMetadata(metadata, runtimecore.ApprovalMetadataSessionID, run.SessionID)
	if strings.TrimSpace(metadata[runtimecore.ApprovalMetadataType]) == "" {
		kind := runtimecore.ApprovalKind(strings.TrimSpace(metadata[runtimecore.ApprovalMetadataKind]))
		toolName := strings.TrimSpace(metadata[runtimecore.ApprovalMetadataToolName])
		metadata[runtimecore.ApprovalMetadataType] = runtimecore.ApprovalTypeForKind(kind, toolName)
	}
	return metadata
}

func discussionRuntimeApprovalSummary(metadata map[string]string) string {
	toolName := firstNonBlank(metadata[runtimecore.ApprovalMetadataToolName], metadata["approval_native_tool_name"])
	if toolName != "" {
		return "Runtime approval required for " + toolName
	}
	return "Runtime approval required"
}

func discussionRuntimeApprovalEventID(conversationID string, runID string, requestID string) string {
	return "discussion-runtime-approval-" + discussionRuntimeApprovalIDSegment(conversationID) + "-" + discussionRuntimeApprovalIDSegment(runID) + "-" + discussionRuntimeApprovalIDSegment(requestID)
}

func discussionRuntimeApprovalIDSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	replacer := strings.NewReplacer(":", "-", "/", "-", "\\", "-", " ", "-", "\t", "-", "\n", "-")
	return replacer.Replace(value)
}

func putRuntimeApprovalMetadata(metadata map[string]string, key string, value string) {
	if metadata == nil || strings.TrimSpace(key) == "" || strings.TrimSpace(metadata[key]) != "" {
		return
	}
	if value = strings.TrimSpace(value); value != "" {
		metadata[key] = value
	}
}

func approvalContinuationEffect(actionID string) string {
	switch strings.ToLower(strings.TrimSpace(actionID)) {
	case "approve_session":
		return "session"
	case "approve_edited":
		return "modified"
	case "modify":
		return "modify"
	case "needs_context":
		return "needs_context"
	case "deny", "reject", "dismiss":
		return "deny"
	case "other":
		return "once"
	default:
		return "once"
	}
}
