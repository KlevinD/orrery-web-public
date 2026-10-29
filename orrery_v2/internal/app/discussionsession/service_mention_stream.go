package discussionsession

import (
	"context"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *Service) respondToDiscussionMention(ctx context.Context, run *RunRecord, request DiscussionAgentMentionRequest, emitEvent func(StreamEventDTO) bool) (DiscussionAgentResponse, error) {
	if s == nil || s.mentionResponder == nil {
		return DiscussionAgentResponse{}, ErrRuntimeUnavailable
	}
	if emitEvent == nil {
		response, err := s.mentionResponder.RespondToDiscussionMention(ctx, request)
		applyDiscussionResponseOutputPolicy(&response)
		return response, err
	}
	streamer, ok := s.mentionResponder.(DiscussionAgentMentionStreamResponder)
	if !ok {
		response, err := s.mentionResponder.RespondToDiscussionMention(ctx, request)
		applyDiscussionResponseOutputPolicy(&response)
		return response, err
	}
	events, err := streamer.RespondToDiscussionMentionStream(ctx, request)
	if err != nil {
		return DiscussionAgentResponse{}, err
	}
	return s.drainDiscussionMentionStream(ctx, run, events, emitEvent)
}

func (s *Service) drainDiscussionMentionStream(ctx context.Context, run *RunRecord, events <-chan DiscussionAgentStreamEvent, emitEvent func(StreamEventDTO) bool) (DiscussionAgentResponse, error) {
	if run == nil {
		return DiscussionAgentResponse{}, ErrInvalidRequest
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
	flushTokens := func() {
		delta := tokens.flush()
		if delta == "" {
			return
		}
		deltaSequence++
		displayEvents = append(displayEvents, DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamTokenDelta, Delta: delta})
		if emitting && !emitEvent(StreamEventDTO{Kind: StreamEventTokenDelta, Delta: delta, RunID: run.RunID, Sequence: deltaSequence}) {
			emitting = false
		}
	}
	finishTokens := func() {
		tokens.finish()
		flushTokens()
	}
	flushThinking := func(flushCtx context.Context) error {
		return s.flushStreamedThinking(flushCtx, run, thinking, &deltaSequence, func(event StreamEventDTO) {
			if emitting && !emitEvent(event) {
				emitting = false
			}
		})
	}
	for {
		var event DiscussionAgentStreamEvent
		var ok bool
		select {
		case <-ctx.Done():
			_ = flushThinking(context.WithoutCancel(ctx))
			return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), ctx.Err()
		case <-tokens.timerChannel():
			flushTokens()
			if !s.isRunCurrent(ctx, *run) {
				return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), ErrNotFound
			}
			continue
		case <-thinking.timerChannel():
			if err := flushThinking(ctx); err != nil {
				return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), err
			}
			if !s.isRunCurrent(ctx, *run) {
				return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), ErrNotFound
			}
			continue
		case event, ok = <-events:
			if !ok {
				finishTokens()
				if err := flushThinking(ctx); err != nil {
					return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), err
				}
				return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), NewRuntimeExecutionError(run.RuntimeKind, "runtime stream ended before completion")
			}
		}
		switch event.Kind {
		case DiscussionAgentStreamTokenDelta:
			if err := flushThinking(ctx); err != nil {
				return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), err
			}
			if event.Delta == "" {
				continue
			}
			if tokens.append(event.Delta) {
				flushTokens()
				if !s.isRunCurrent(ctx, *run) {
					return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), ErrNotFound
				}
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
					return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), err
				}
			}
			continue
		case DiscussionAgentStreamToolCall:
			flushTokens()
			if err := flushThinking(ctx); err != nil {
				return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), err
			}
			tool := event.ToolCall
			if tool.CreatedAt.IsZero() {
				tool.CreatedAt = s.now()
			}
			if err := s.persistStreamedToolCall(ctx, run, tool); err != nil {
				return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), err
			}
			tool = mergedStreamedToolCall(run.ToolCalls, tool)
			if err := s.appendRealtimeToolEvents(ctx, run.ConversationID, run.RunID, []ToolCallDTO{tool}); err != nil {
				return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), err
			}
			displayEvents = append(displayEvents, DiscussionAgentStreamEvent{Kind: DiscussionAgentStreamToolCall, ToolCall: tool})
			if emitting && !emitEvent(StreamEventDTO{Kind: StreamEventToolCall, ToolCall: &tool, RunID: run.RunID}) {
				emitting = false
			}
		case DiscussionAgentStreamToolResult:
			flushTokens()
			if err := flushThinking(ctx); err != nil {
				return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), err
			}
			tool := event.ToolCall
			if tool.CreatedAt.IsZero() {
				tool.CreatedAt = s.now()
			}
			if err := s.persistStreamedToolCall(ctx, run, tool); err != nil {
				return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), err
			}
			tool = mergedStreamedToolCall(run.ToolCalls, tool)
			if err := s.appendRealtimeToolEvents(ctx, run.ConversationID, run.RunID, []ToolCallDTO{tool}); err != nil {
				return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), err
			}
			if emitting && !emitEvent(StreamEventDTO{Kind: StreamEventToolResult, ToolCall: &tool, RunID: run.RunID}) {
				emitting = false
			}
		case DiscussionAgentStreamApprovalRequested:
			flushTokens()
			if err := flushThinking(ctx); err != nil {
				return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), err
			}
			if event.Approval != nil && emitting && !emitEvent(StreamEventDTO{
				Kind:           StreamEventApprovalRequested,
				EventID:        event.Approval.EventID,
				ConversationID: run.ConversationID,
				Approval:       event.Approval,
				RunID:          run.RunID,
			}) {
				emitting = false
			}
		case DiscussionAgentStreamPlan:
			flushTokens()
			if err := flushThinking(ctx); err != nil {
				return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), err
			}
			displayEvents = append(displayEvents, event)
			if err := s.persistStreamedPlan(ctx, run, event.Plan); err != nil {
				return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), err
			}
			if err := s.appendRealtimePlanUpdated(ctx, run.ConversationID, run.TurnID, run.RunID, event.Plan); err != nil {
				return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), err
			}
			if emitting && !emitEvent(StreamEventDTO{Kind: StreamEventPlan, Plan: event.Plan, RunID: run.RunID}) {
				emitting = false
			}
		case DiscussionAgentStreamRuntimeError:
			finishTokens()
			if err := flushThinking(ctx); err != nil {
				return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), err
			}
			response := partialDiscussionStreamResponse(event.Response, tokens, displayEvents, run.RunID)
			if event.Error != nil {
				return response, event.Error
			}
			return response, ErrRuntimeUnavailable
		case DiscussionAgentStreamRunCompleted:
			finishTokens()
			if err := flushThinking(ctx); err != nil {
				return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), err
			}
			applyStreamOutputPrivacyMetadata(&event.Response, tokens)
			applyDiscussionResponseOutputPolicy(&event.Response)
			event.Response.DisplayTimeline = completedStreamDisplayTimeline(displayEvents, run.RunID, event.Response.DisplayTimeline)
			return event.Response, nil
		}
		if !s.isRunCurrent(ctx, *run) {
			return partialDiscussionStreamResponse(DiscussionAgentResponse{}, tokens, displayEvents, run.RunID), ErrNotFound
		}
	}
}
