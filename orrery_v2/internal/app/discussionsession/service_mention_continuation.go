package discussionsession

import (
	"context"
	"time"
)

func (s *Service) continueDiscussionMentionTools(ctx context.Context, conversationID string, run *RunRecord, request DiscussionAgentMentionRequest, source MessageRecord, target ConversationMemberRecord, response DiscussionAgentResponse, linkedMissions []LinkedMissionDTO, completedAt time.Time, emitEvent func(StreamEventDTO) bool, unregister func(RunRecord)) (DiscussionAgentResponse, []LinkedMissionDTO, bool, error) {
	if run == nil {
		return response, linkedMissions, false, ErrInvalidRequest
	}
	for {
		previousCalls := append([]ToolCallDTO(nil), response.ToolCalls...)
		continued, ok, err := s.continueDiscussionMentionAfterToolResults(ctx, request, response, linkedMissions)
		if err != nil {
			stop, saveErr := s.handleDiscussionMentionContinuationFailure(ctx, conversationID, run, source, target, response, err, completedAt, emitEvent, unregister)
			return response, linkedMissions, stop, saveErr
		}
		if !ok {
			return response, linkedMissions, false, nil
		}
		response = continued
		applyResponseTelemetry(run, continued)
		newCalls := newToolCallsAfterContinuation(previousCalls, response.ToolCalls)
		if len(newCalls) == 0 {
			response.ToolCalls = previousCalls
			return response, linkedMissions, false, nil
		}
		nextMissions, err := s.applyBoundDiscussionMentionToolCalls(ctx, conversationID, target.MemberID, source, target, newCalls, request.BoundTools, completedAt, request)
		if err != nil {
			stop, saveErr := s.handleDiscussionMentionContinuationFailure(ctx, conversationID, run, source, target, response, err, completedAt, emitEvent, unregister)
			return response, linkedMissions, stop, saveErr
		}
		response.ToolCalls = mergeAppliedContinuationToolCalls(response.ToolCalls, newCalls)
		linkedMissions = mergeLinkedMissions(linkedMissions, nextMissions)
	}
}

func (s *Service) handleDiscussionMentionContinuationFailure(ctx context.Context, conversationID string, run *RunRecord, source MessageRecord, target ConversationMemberRecord, response DiscussionAgentResponse, err error, completedAt time.Time, emitEvent func(StreamEventDTO) bool, unregister func(RunRecord)) (bool, error) {
	if run == nil {
		return false, ErrInvalidRequest
	}
	stop, saveErr := s.failDiscussionMentionRun(ctx, conversationID, *run, source, target, response, err, completedAt, emitEvent)
	run.Status = "failed"
	run.CompletedAt = completedAt
	if unregister != nil {
		unregister(*run)
	}
	if saveErr != nil {
		return stop, saveErr
	}
	return stop, nil
}
