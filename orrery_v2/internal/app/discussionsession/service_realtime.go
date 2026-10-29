package discussionsession

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/agent/outputpolicy"
)

func (s *Service) appendRealtimeEvent(ctx context.Context, event RealtimeEvent) error {
	if s == nil || s.realtime == nil {
		return nil
	}
	event.EventID = strings.TrimSpace(event.EventID)
	event.Type = strings.TrimSpace(event.Type)
	event.Channel = strings.TrimSpace(event.Channel)
	event.AggregateType = strings.TrimSpace(event.AggregateType)
	event.AggregateID = strings.TrimSpace(event.AggregateID)
	if event.EventID == "" || event.Type == "" || event.Channel == "" || event.AggregateType == "" || event.AggregateID == "" {
		return ErrInvalidRequest
	}
	if len(event.Payload) == 0 {
		event.Payload = json.RawMessage(`{}`)
	}
	if !json.Valid(event.Payload) {
		return ErrInvalidRequest
	}
	return s.realtime.AppendRealtimeEvent(ctx, event)
}

func (s *Service) appendRealtimeMessageCreated(ctx context.Context, message MessageRecord, viewer ViewerRef, createdConversation bool) error {
	if err := s.appendRealtimeConversationEvent(ctx, RealtimeEventDiscussionMessageCreated, "discussion-message-created-"+message.MessageID, message.ConversationID, map[string]string{
		"conversation_id": message.ConversationID,
		"message_id":      message.MessageID,
		"turn_id":         message.TurnID,
		"run_id":          message.RunID,
		"role":            message.Role,
		"author_id":       message.AuthorID,
	}); err != nil {
		return err
	}
	if createdConversation {
		return s.appendRealtimeUserDiscussionListInvalidated(ctx, viewer.ViewerID, message.ConversationID, "message-created-"+message.MessageID)
	}
	return nil
}

func (s *Service) appendRealtimeTurnStarted(ctx context.Context, turn TurnRecord) error {
	return s.appendRealtimeConversationEvent(ctx, RealtimeEventDiscussionTurnStarted, "discussion-turn-started-"+turn.TurnID, turn.ConversationID, map[string]string{
		"conversation_id": turn.ConversationID,
		"turn_id":         turn.TurnID,
		"root_message_id": turn.RootMessageID,
		"mode":            turn.Mode,
		"status":          turn.Status,
		"started_at":      realtimeTime(turn.StartedAt),
	})
}

func (s *Service) appendRealtimeTurnCompleted(ctx context.Context, turn TurnRecord) error {
	return s.appendRealtimeConversationEvent(ctx, RealtimeEventDiscussionTurnCompleted, "discussion-turn-completed-"+turn.TurnID, turn.ConversationID, map[string]string{
		"conversation_id": turn.ConversationID,
		"turn_id":         turn.TurnID,
		"root_message_id": turn.RootMessageID,
		"mode":            turn.Mode,
		"status":          turn.Status,
		"completed_at":    realtimeTime(turn.CompletedAt),
	})
}

func (s *Service) appendRealtimeTurnFailed(ctx context.Context, turn TurnRecord) error {
	return s.appendRealtimeConversationEvent(ctx, RealtimeEventDiscussionTurnFailed, "discussion-turn-failed-"+turn.TurnID, turn.ConversationID, map[string]string{
		"conversation_id": turn.ConversationID,
		"turn_id":         turn.TurnID,
		"root_message_id": turn.RootMessageID,
		"mode":            turn.Mode,
		"status":          turn.Status,
		"error_code":      turn.ErrorCode,
		"error":           publicOptionalText(turn.ErrorMessage, "任务执行失败，详细原因已记录到 Orrery。"),
		"completed_at":    realtimeTime(turn.CompletedAt),
	})
}

func (s *Service) appendRealtimeRunStarted(ctx context.Context, run RunRecord) error {
	return s.appendRealtimeConversationEvent(ctx, RealtimeEventDiscussionRunStarted, "discussion-run-started-"+run.RunID, run.ConversationID, map[string]string{
		"conversation_id":  run.ConversationID,
		"turn_id":          run.TurnID,
		"run_id":           run.RunID,
		"mode":             run.Mode,
		"agent_id":         run.AgentID,
		"logical_agent_id": run.LogicalAgentID,
		"agent_name":       run.AgentName,
		"status":           run.Status,
		"created_at":       realtimeTime(run.CreatedAt),
	})
}

func (s *Service) appendRealtimeRunCompleted(ctx context.Context, run RunRecord) error {
	return s.appendRealtimeRunCompletedWithID(ctx, run, "discussion-run-completed-"+run.RunID)
}

func (s *Service) appendRealtimeRunCompletedWithID(ctx context.Context, run RunRecord, eventID string) error {
	return s.appendRealtimeConversationPayload(ctx, RealtimeEventDiscussionRunCompleted, eventID, run.ConversationID, map[string]any{
		"conversation_id":  run.ConversationID,
		"turn_id":          run.TurnID,
		"run_id":           run.RunID,
		"mode":             run.Mode,
		"has_plan":         run.HasPlan,
		"plan":             run.Plan,
		"agent_id":         run.AgentID,
		"logical_agent_id": run.LogicalAgentID,
		"agent_name":       run.AgentName,
		"status":           run.Status,
		"runtime_kind":     run.RuntimeKind,
		"session_id":       run.SessionID,
		"thinking_summary": publicRunThinkingSummary(run),
		"tool_calls":       run.ToolCalls,
		"usage":            run.Usage,
		"duration_ms":      run.DurationMs,
		"created_at":       realtimeTime(run.CreatedAt),
		"completed_at":     realtimeTime(run.CompletedAt),
	})
}

func (s *Service) appendRealtimeRunFailed(ctx context.Context, run RunRecord) error {
	eventID := "discussion-run-failed-" + run.RunID
	if strings.EqualFold(strings.TrimSpace(run.Status), "cancelled") {
		eventID = "discussion-run-cancelled-" + run.RunID
	}
	return s.appendRealtimeConversationPayload(ctx, RealtimeEventDiscussionRunFailed, eventID, run.ConversationID, map[string]any{
		"conversation_id":  run.ConversationID,
		"turn_id":          run.TurnID,
		"run_id":           run.RunID,
		"mode":             run.Mode,
		"has_plan":         run.HasPlan,
		"plan":             run.Plan,
		"agent_id":         run.AgentID,
		"logical_agent_id": run.LogicalAgentID,
		"agent_name":       run.AgentName,
		"status":           run.Status,
		"runtime_kind":     run.RuntimeKind,
		"session_id":       run.SessionID,
		"thinking_summary": publicRunThinkingSummary(run),
		"tool_calls":       run.ToolCalls,
		"usage":            run.Usage,
		"duration_ms":      run.DurationMs,
		"error":            publicRuntimeError(run.ProviderMetadata["error"], run.Status),
		"created_at":       realtimeTime(run.CreatedAt),
		"completed_at":     realtimeTime(run.CompletedAt),
	})
}

func (s *Service) appendRealtimeThinkingDelta(ctx context.Context, run RunRecord, delta string, sequence int) error {
	if strings.TrimSpace(run.ConversationID) == "" || strings.TrimSpace(run.RunID) == "" || delta == "" || sequence <= 0 {
		return nil
	}
	eventID := fmt.Sprintf("discussion-thinking-delta-%s-%d", run.RunID, sequence)
	return s.appendRealtimeConversationPayload(ctx, RealtimeEventDiscussionThinkingDelta, eventID, run.ConversationID, map[string]any{
		"conversation_id": run.ConversationID,
		"turn_id":         run.TurnID,
		"run_id":          run.RunID,
		"sequence":        sequence,
		"delta":           delta,
	})
}

func (s *Service) appendRealtimeMessageCompleted(ctx context.Context, message MessageRecord) error {
	payload := map[string]string{
		"conversation_id": message.ConversationID,
		"message_id":      message.MessageID,
		"turn_id":         message.TurnID,
		"run_id":          message.RunID,
		"role":            message.Role,
		"author_id":       message.AuthorID,
		"author_name":     message.AuthorName,
		"content":         publicMessageContent(message),
	}
	for key, value := range publicMessageMetadata(message) {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		payload["metadata."+key] = value
	}
	return s.appendRealtimeConversationEvent(ctx, RealtimeEventDiscussionMessageCompleted, "discussion-message-completed-"+message.MessageID, message.ConversationID, payload)
}

func publicRuntimeError(value, lifecycle string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	return outputpolicy.PublicRuntimeText(value, lifecycle)
}

func (s *Service) appendRealtimeToolEvents(ctx context.Context, conversationID string, runID string, calls []ToolCallDTO) error {
	turnID := ""
	if s != nil && s.repo != nil {
		if run, err := s.repo.GetRun(ctx, runID); err == nil {
			turnID = run.TurnID
		}
	}
	for i, call := range calls {
		toolID := realtimeToolCallID(call, i)
		payload := map[string]any{
			"conversation_id": conversationID,
			"turn_id":         turnID,
			"run_id":          runID,
			"tool_call_id":    toolID,
			"name":            call.Name,
			"status":          call.Status,
			"arguments":       call.Arguments,
			"result":          call.Result,
			"created_at":      realtimeTime(call.CreatedAt),
		}
		version := realtimePayloadDigest(payload)
		toolCallEventID := "discussion-tool-call-" + runID + "-" + toolID + "-" + version
		if err := s.appendRealtimeConversationPayload(ctx, RealtimeEventDiscussionToolCall, toolCallEventID, conversationID, payload); err != nil {
			return err
		}
		if len(call.Result) == 0 && !strings.EqualFold(strings.TrimSpace(call.Status), "completed") {
			continue
		}
		if err := s.appendRealtimeConversationPayload(ctx, RealtimeEventDiscussionToolResult, "discussion-tool-result-"+runID+"-"+toolID+"-"+version, conversationID, payload); err != nil {
			return err
		}
	}
	return nil
}

func realtimePayloadDigest(payload any) string {
	raw, err := json.Marshal(payload)
	if err != nil {
		return "unknown"
	}
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("%x", digest[:8])
}

func (s *Service) appendRealtimePlanUpdated(ctx context.Context, conversationID, turnID, runID string, plan any) error {
	raw, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("marshal discussion plan: %w", err)
	}
	digest := sha256.Sum256(raw)
	eventID := fmt.Sprintf("discussion-plan-updated-%s-%x", runID, digest[:8])
	return s.appendRealtimeConversationPayload(ctx, RealtimeEventDiscussionPlanUpdated, eventID, conversationID, map[string]any{
		"conversation_id": conversationID,
		"turn_id":         turnID,
		"run_id":          runID,
		"plan":            plan,
	})
}

func (s *Service) appendRealtimeSnapshotInvalidated(ctx context.Context, conversationID string, reason string, idempotency string) error {
	conversationID = strings.TrimSpace(conversationID)
	reason = strings.TrimSpace(reason)
	idempotency = strings.TrimSpace(idempotency)
	if idempotency == "" {
		idempotency = reason
	}
	return s.appendRealtimeConversationEvent(ctx, RealtimeEventDiscussionSnapshotInvalidated, "discussion-snapshot-invalidated-"+conversationID+"-"+idempotency, conversationID, map[string]string{
		"conversation_id": conversationID,
		"reason":          reason,
	})
}

func (s *Service) appendRealtimeUserDiscussionListInvalidated(ctx context.Context, viewerID string, conversationID string, idempotency string) error {
	viewerID = strings.TrimSpace(viewerID)
	conversationID = strings.TrimSpace(conversationID)
	idempotency = strings.TrimSpace(idempotency)
	if idempotency == "" {
		idempotency = conversationID
	}
	payload, err := realtimePayload(map[string]string{
		"conversation_id": conversationID,
		"reason":          "conversation_list_changed",
	})
	if err != nil {
		return err
	}
	return s.appendRealtimeEvent(ctx, RealtimeEvent{
		EventID:       "user-discussion-list-invalidated-" + viewerID + "-" + idempotency,
		Type:          RealtimeEventUserDiscussionListInvalidated,
		Channel:       "user:" + viewerID,
		AggregateType: RealtimeAggregateUser,
		AggregateID:   viewerID,
		Payload:       payload,
	})
}

func (s *Service) appendRealtimeConversationListInvalidated(ctx context.Context, view ConversationRecordView, idempotency string) error {
	ownerID := strings.TrimSpace(view.ConversationRecord.CreatedBy)
	if ownerID != "" {
		return s.appendRealtimeUserDiscussionListInvalidated(ctx, ownerID, view.ConversationRecord.ConversationID, idempotency)
	}
	for _, member := range view.Members {
		if strings.EqualFold(strings.TrimSpace(member.MemberKind), "user") && strings.TrimSpace(member.MemberID) != "" {
			return s.appendRealtimeUserDiscussionListInvalidated(ctx, member.MemberID, view.ConversationRecord.ConversationID, idempotency)
		}
	}
	return nil
}

func (s *Service) appendRealtimeConversationEvent(ctx context.Context, eventType string, eventID string, conversationID string, payload map[string]string) error {
	if s == nil {
		return nil
	}
	payload = cloneStringMap(payload)
	if payload == nil {
		payload = map[string]string{}
	}
	if _, ok := payload["revision"]; !ok && s.repo != nil {
		if view, err := s.repo.GetConversation(ctx, conversationID); err == nil && view.Revision > 0 {
			payload["revision"] = fmt.Sprint(view.Revision)
		}
	}
	return appendRealtimeConversationEvent(ctx, s.realtime, eventType, eventID, conversationID, payload)
}

func (s *Service) appendRealtimeConversationPayload(ctx context.Context, eventType string, eventID string, conversationID string, payload any) error {
	if s == nil || s.realtime == nil {
		return nil
	}
	conversationID = strings.TrimSpace(conversationID)
	if values, ok := payload.(map[string]any); ok {
		if _, exists := values["revision"]; !exists && s.repo != nil {
			if view, viewErr := s.repo.GetConversation(ctx, conversationID); viewErr == nil && view.Revision > 0 {
				values["revision"] = view.Revision
			}
		}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal realtime payload: %w", err)
	}
	return s.appendRealtimeEvent(ctx, RealtimeEvent{
		EventID:       strings.TrimSpace(eventID),
		Type:          strings.TrimSpace(eventType),
		Channel:       "conversation:" + conversationID,
		AggregateType: RealtimeAggregateConversation,
		AggregateID:   conversationID,
		Payload:       raw,
	})
}

func realtimePayload(payload map[string]string) (json.RawMessage, error) {
	if payload == nil {
		return json.RawMessage(`{}`), nil
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal realtime payload: %w", err)
	}
	return raw, nil
}

func realtimeToolCallID(call ToolCallDTO, index int) string {
	id := strings.TrimSpace(call.ToolCallID)
	if id != "" {
		return id
	}
	name := strings.TrimSpace(call.Name)
	if name == "" {
		name = "tool"
	}
	return fmt.Sprintf("%s-%d", name, index)
}

func realtimeTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Format(time.RFC3339Nano)
}
