package discussionsession

import (
	"context"
	"errors"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *Service) forwardRuntimeStream(ctx context.Context, out chan<- StreamEventDTO, conversationID string, run RunRecord, request DiscussionAgentRequest, runtimeEvents <-chan DiscussionAgentStreamEvent) {
	defer close(out)
	defer s.unregisterActiveRun(run.RunID)
	send := func(event StreamEventDTO) bool {
		if !s.isRunCurrent(ctx, run) {
			return false
		}
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
	if snapshot, err := s.repo.GetConversation(ctx, conversationID); err == nil {
		dto := mapConversationDTO(snapshot)
		if !send(StreamEventDTO{Kind: StreamEventSnapshot, Conversation: &dto}) {
			return
		}
	}
	runDTO := mapRuns([]RunRecord{run})[0]
	if !send(StreamEventDTO{Kind: StreamEventRunStarted, Run: &runDTO}) {
		return
	}
	emitting := true
	displayEvents := make([]DiscussionAgentStreamEvent, 0)
	tokens := &discussionStreamTokenBuffer{}
	thinking := &discussionStreamThinkingBuffer{}
	s.registerDiscussionStreamTokenBuffer(run.RunID, tokens)
	defer s.unregisterDiscussionStreamTokenBuffer(run.RunID, tokens)
	defer tokens.close()
	defer thinking.close()
	deltaSequence := 0
	emit := func(event StreamEventDTO) {
		if emitting && !send(event) {
			emitting = false
		}
	}
	flushTokens := func() {
		delta := tokens.flush()
		if delta == "" {
			return
		}
		deltaSequence++
		displayEvents = append(displayEvents, DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamTokenDelta, Delta: delta})
		emit(StreamEventDTO{Kind: StreamEventTokenDelta, Delta: delta, RunID: run.RunID, Sequence: deltaSequence})
	}
	finishTokens := func() {
		tokens.finish()
		flushTokens()
	}
	flushThinking := func(flushCtx context.Context) error {
		return s.flushStreamedThinking(flushCtx, &run, thinking, &deltaSequence, emit)
	}
	persistFailure := func(response DiscussionAgentResponse, failure error) {
		response = partialDiscussionStreamResponse(response, tokens, displayEvents, run.RunID)
		partialMessage, err := s.persistFailedRunWithResponse(context.WithoutCancel(ctx), conversationID, run, response, failure)
		if err != nil {
			emit(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
			return
		}
		if partialMessage != nil {
			emit(StreamEventDTO{Kind: StreamEventMessageCompleted, Message: partialMessage, RunID: run.RunID})
		}
	}
	for {
		var event DiscussionAgentStreamEvent
		var ok bool
		select {
		case <-ctx.Done():
			finishTokens()
			flushErr := flushThinking(context.WithoutCancel(ctx))
			if s.isRunCurrent(context.WithoutCancel(ctx), run) {
				if flushErr != nil {
					persistFailure(DiscussionAgentResponse{}, flushErr)
				} else {
					persistFailure(DiscussionAgentResponse{}, ctx.Err())
				}
			}
			return
		case <-tokens.timerChannel():
			flushTokens()
			continue
		case <-thinking.timerChannel():
			if err := flushThinking(ctx); err != nil {
				persistFailure(DiscussionAgentResponse{}, err)
				emit(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			continue
		case event, ok = <-runtimeEvents:
			if !ok {
				if !s.isRunCurrent(ctx, run) {
					return
				}
				finishTokens()
				if err := flushThinking(ctx); err != nil {
					persistFailure(DiscussionAgentResponse{}, err)
					emit(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
					return
				}
				failure := errors.New("discussion runtime stream ended before completion")
				persistFailure(DiscussionAgentResponse{}, failure)
				emit(StreamEventDTO{Kind: StreamEventError, Error: failure.Error()})
				return
			}
		}
		switch event.Kind {
		case DiscussionAgentStreamTokenDelta:
			if err := flushThinking(ctx); err != nil {
				persistFailure(DiscussionAgentResponse{}, err)
				emit(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			if event.Delta == "" {
				continue
			}
			if tokens.append(event.Delta) {
				flushTokens()
			}
			continue
		case DiscussionAgentStreamThinkingDelta:
			if event.ReasoningKind != agentcore.ReasoningContentSummary {
				continue
			}
			flushTokens()
			if event.Delta == "" {
				continue
			}
			if thinking.append(event.Delta) {
				if err := flushThinking(ctx); err != nil {
					persistFailure(DiscussionAgentResponse{}, err)
					emit(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
					return
				}
			}
			continue
		case DiscussionAgentStreamToolCall:
			flushTokens()
			if err := flushThinking(ctx); err != nil {
				persistFailure(DiscussionAgentResponse{}, err)
				emit(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			tool := event.ToolCall
			if tool.CreatedAt.IsZero() {
				tool.CreatedAt = s.now()
			}
			if err := s.persistStreamedToolCall(ctx, &run, tool); err != nil {
				persistFailure(DiscussionAgentResponse{}, err)
				emit(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			tool = mergedStreamedToolCall(run.ToolCalls, tool)
			if err := s.appendRealtimeToolEvents(ctx, conversationID, run.RunID, []ToolCallDTO{tool}); err != nil {
				persistFailure(DiscussionAgentResponse{}, err)
				emit(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			displayEvents = append(displayEvents, DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamToolCall, ToolCall: tool})
			emit(StreamEventDTO{Kind: StreamEventToolCall, ToolCall: &tool, RunID: run.RunID})
		case DiscussionAgentStreamToolResult:
			flushTokens()
			if err := flushThinking(ctx); err != nil {
				persistFailure(DiscussionAgentResponse{}, err)
				emit(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			tool := event.ToolCall
			if tool.CreatedAt.IsZero() {
				tool.CreatedAt = s.now()
			}
			if err := s.persistStreamedToolCall(ctx, &run, tool); err != nil {
				persistFailure(DiscussionAgentResponse{}, err)
				emit(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			tool = mergedStreamedToolCall(run.ToolCalls, tool)
			if err := s.appendRealtimeToolEvents(ctx, conversationID, run.RunID, []ToolCallDTO{tool}); err != nil {
				persistFailure(DiscussionAgentResponse{}, err)
				emit(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			emit(StreamEventDTO{Kind: StreamEventToolResult, ToolCall: &tool, RunID: run.RunID})
		case DiscussionAgentStreamApprovalRequested:
			flushTokens()
			if err := flushThinking(ctx); err != nil {
				persistFailure(DiscussionAgentResponse{}, err)
				emit(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			if event.Approval == nil {
				continue
			}
			emit(StreamEventDTO{
				Kind:           StreamEventApprovalRequested,
				EventID:        event.Approval.EventID,
				ConversationID: conversationID,
				Approval:       event.Approval,
				RunID:          run.RunID,
			})
		case DiscussionAgentStreamPlan:
			flushTokens()
			if err := flushThinking(ctx); err != nil {
				persistFailure(DiscussionAgentResponse{}, err)
				emit(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			displayEvents = append(displayEvents, event)
			if err := s.persistStreamedPlan(ctx, &run, event.Plan); err != nil {
				persistFailure(DiscussionAgentResponse{}, err)
				emit(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			if err := s.appendRealtimePlanUpdated(ctx, conversationID, run.TurnID, run.RunID, event.Plan); err != nil {
				persistFailure(DiscussionAgentResponse{}, err)
				emit(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			emit(StreamEventDTO{Kind: StreamEventPlan, Plan: event.Plan, RunID: run.RunID})
		case DiscussionAgentStreamRuntimeError:
			finishTokens()
			if err := flushThinking(ctx); err != nil {
				persistFailure(DiscussionAgentResponse{}, err)
				emit(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			if !s.isRunCurrent(ctx, run) {
				return
			}
			failure := event.Error
			if failure == nil {
				failure = ErrRuntimeExecution
			}
			applyStreamOutputPrivacyMetadata(&event.Response, tokens)
			applyDiscussionResponseOutputPolicy(&event.Response)
			s.applyRuntimeFailureResponse(&run, event.Response)
			persistFailure(event.Response, failure)
			if event.Error != nil {
				emit(streamErrorEvent(event.Error))
			} else {
				emit(StreamEventDTO{Kind: StreamEventError})
			}
			return
		case DiscussionAgentStreamRunCompleted:
			finishTokens()
			if err := flushThinking(ctx); err != nil {
				persistFailure(DiscussionAgentResponse{}, err)
				emit(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			if !s.isRunCurrent(ctx, run) {
				return
			}
			applyStreamOutputPrivacyMetadata(&event.Response, tokens)
			applyDiscussionResponseOutputPolicy(&event.Response)
			event.Response.DisplayTimeline = completedStreamDisplayTimeline(displayEvents, run.RunID, event.Response.DisplayTimeline)
			message, completedRun, err := s.completeRunLocked(ctx, conversationID, run, event.Response, request)
			if err != nil {
				_ = send(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			if message != nil {
				if !send(StreamEventDTO{Kind: StreamEventMessageCompleted, Message: message}) {
					return
				}
			}
			if !send(StreamEventDTO{Kind: StreamEventRunCompleted, Run: completedRun}) {
				return
			}
			sourceMessage, ok, err := s.userSourceMessageForRun(ctx, conversationID, run)
			if err != nil {
				_ = send(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			var userMentionMessages []MessageDTO
			if ok {
				userMentionMessages, err = s.respondToDiscussionMentionsFromMessage(ctx, conversationID, sourceMessage, event.Response.LinkedMissions, completedRun.CompletedAt, discussionMentionDispatchOptions{
					SkipInitialTargetIDs: map[string]bool{strings.TrimSpace(run.AgentID): true},
					OnEvent:              send,
					ActiveRunCancel:      func() { s.invalidateRun(run.RunID) },
					OutputLanguage:       request.OutputLanguage,
				}, func(message MessageDTO) bool {
					return send(StreamEventDTO{Kind: StreamEventMessageCompleted, Message: &message})
				})
			}
			if err != nil {
				_ = send(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			latestMentionAt := completedRun.CompletedAt
			for _, message := range userMentionMessages {
				if message.CreatedAt.After(latestMentionAt) {
					latestMentionAt = message.CreatedAt
				}
			}
			_, err = s.respondToDiscussionAgentMentionsWithMessages(ctx, conversationID, discussionRunMessageAuthorID(run), event.Response.LinkedMissions, completedRun.CompletedAt, discussionMentionDispatchOptions{
				SkipInitialTargetIDs: messageAuthorIDs(userMentionMessages),
				InitialLatestAt:      latestMentionAt,
				OnEvent:              send,
				ActiveRunCancel:      func() { s.invalidateRun(run.RunID) },
				OutputLanguage:       request.OutputLanguage,
			}, func(message MessageDTO) bool {
				return send(StreamEventDTO{Kind: StreamEventMessageCompleted, Message: &message})
			})
			if err != nil {
				_ = send(StreamEventDTO{Kind: StreamEventError, Error: err.Error()})
				return
			}
			if snapshot, err := s.repo.GetConversation(ctx, conversationID); err == nil {
				dto := mapConversationDTO(snapshot)
				_ = send(StreamEventDTO{Kind: StreamEventSnapshot, Conversation: &dto})
			}
			return
		}
	}
}
