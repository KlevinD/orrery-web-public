package discussionsession

import (
	"context"
	"strings"
	"time"
)

func (s *Service) applyRuntimeFailureResponse(run *RunRecord, response DiscussionAgentResponse) {
	if run == nil {
		return
	}
	applyDiscussionResponseOutputPolicy(&response)
	if runtimeKind := strings.TrimSpace(response.RuntimeKind); runtimeKind != "" {
		run.RuntimeKind = runtimeKind
	}
	if sessionID := strings.TrimSpace(response.SessionID); sessionID != "" {
		run.SessionID = sessionID
	}
	applyResponseRunProgress(run, response)
	run.ProviderMetadata = mergeOutputPrivacyMetadata(run.ProviderMetadata, response.ProviderMetadata)
	applyResponseTelemetry(run, response)
	s.applyResponseDebug(run, response)
}

func (s *Service) saveRuntimeFailureEvent(ctx context.Context, conversationID string, run RunRecord, reason error, at time.Time) error {
	if reason == nil {
		return nil
	}
	summary := strings.TrimSpace(reason.Error())
	if summary == "" {
		return nil
	}
	event := EventRecord{
		EventID:        "discussion-runtime-failed-" + run.RunID,
		ConversationID: conversationID,
		Kind:           "discussion_runtime_failed",
		Status:         "failed",
		Summary:        summary,
		Metadata: map[string]string{
			"turn_id":  run.TurnID,
			"run_id":   run.RunID,
			"agent_id": run.AgentID,
		},
		CreatedAt: at,
	}
	if runtimeKind := strings.TrimSpace(run.RuntimeKind); runtimeKind != "" {
		event.Metadata["runtime_kind"] = runtimeKind
	}
	return s.repo.SaveEvent(ctx, event)
}

func (s *Service) failedConversationDTO(ctx context.Context, conversationID string, fallback error) (ConversationDTO, error) {
	snapshot, err := s.repo.GetConversation(ctx, conversationID)
	if err != nil {
		if fallback != nil {
			return ConversationDTO{}, fallback
		}
		return ConversationDTO{}, err
	}
	return mapConversationDTO(snapshot), nil
}

func (s *Service) forwardRuntimeStartFailure(ctx context.Context, out chan<- StreamEventDTO, conversationID string, run RunRecord, cause error) {
	defer close(out)
	send := func(event StreamEventDTO) bool {
		select {
		case <-ctx.Done():
			return false
		case out <- event:
			if s.streams != nil {
				s.streams.publish(conversationID, event)
			}
			return true
		}
	}
	if err := s.persistFailedRun(ctx, conversationID, run, cause); err != nil {
		_ = send(streamErrorEvent(err))
		return
	}
	if snapshot, err := s.repo.GetConversation(ctx, conversationID); err == nil {
		dto := mapConversationDTO(snapshot)
		if !send(StreamEventDTO{Kind: StreamEventSnapshot, Conversation: &dto}) {
			return
		}
	}
	if cause != nil {
		_ = send(streamErrorEvent(cause))
		return
	}
	_ = send(StreamEventDTO{Kind: StreamEventError})
}
