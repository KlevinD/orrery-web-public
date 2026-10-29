package discussionsession

import (
	"context"
	"time"
)

func (s *Service) failDiscussionMentionRun(ctx context.Context, conversationID string, run RunRecord, source MessageRecord, target ConversationMemberRecord, response DiscussionAgentResponse, err error, completedAt time.Time, emitEvent func(StreamEventDTO) bool) (bool, error) {
	if !s.isRunCurrent(ctx, run) {
		return true, nil
	}
	run.CompletedAt = completedAt
	s.applyRuntimeFailureResponse(&run, response)
	failRunProgress(&run)
	terminateResponseProgress(&response, "failed")
	partialMessage, saveErr := s.persistPartialAssistantMessage(ctx, conversationID, run, response, completedAt, &source, &target)
	if saveErr != nil {
		return false, saveErr
	}
	if partialMessage != nil && emitEvent != nil {
		if !emitEvent(StreamEventDTO{Kind: StreamEventMessageCompleted, Message: partialMessage, RunID: run.RunID}) {
			emitEvent = nil
		}
	}
	run.Status = "failed"
	if saveErr := s.repo.SaveRun(ctx, run); saveErr != nil {
		return false, saveErr
	}
	if saveErr := s.appendRealtimeRunFailed(ctx, run); saveErr != nil {
		return false, saveErr
	}
	if emitEvent != nil {
		runDTO := mapRuns([]RunRecord{run})[0]
		if !emitEvent(StreamEventDTO{Kind: StreamEventRunCompleted, Run: &runDTO}) {
			return true, nil
		}
	}
	if saveErr := s.saveDiscussionMentionFailure(ctx, conversationID, run, source, target, err, completedAt); saveErr != nil {
		return false, saveErr
	}
	return false, nil
}

func (s *Service) saveDiscussionMentionFailure(ctx context.Context, conversationID string, run RunRecord, source MessageRecord, target ConversationMemberRecord, cause error, at time.Time) error {
	if cause == nil {
		return nil
	}
	eventID, err := s.newID()
	if err != nil {
		return err
	}
	event := EventRecord{
		EventID:        eventID,
		ConversationID: conversationID,
		Kind:           "discussion_agent_mention_failed",
		Status:         "failed",
		Summary:        cause.Error(),
		Metadata: map[string]string{
			"turn_id":           run.TurnID,
			"run_id":            run.RunID,
			"source_message_id": source.MessageID,
			"source_agent_id":   source.AuthorID,
			"target_agent_id":   target.MemberID,
		},
		CreatedAt: at,
	}
	return s.repo.SaveEvent(ctx, event)
}
