package discussionsession

import (
	"context"
	"strings"
)

type streamingDiscussionAgentMentionResponder struct {
	request       DiscussionAgentMentionRequest
	events        []DiscussionAgentStreamEvent
	streamEvents  <-chan DiscussionAgentStreamEvent
	response      DiscussionAgentResponse
	err           error
	called        bool
	streamCalled  bool
	streamStarted chan<- struct{}
}

func (r *streamingDiscussionAgentMentionResponder) RespondToDiscussionMention(_ context.Context, request DiscussionAgentMentionRequest) (DiscussionAgentResponse, error) {
	r.called = true
	r.request = request
	if r.err != nil {
		return DiscussionAgentResponse{}, r.err
	}
	if strings.TrimSpace(r.response.Status) != "" || strings.TrimSpace(r.response.AssistantMessage) != "" || len(r.response.ToolCalls) > 0 {
		return r.response, nil
	}
	for _, event := range r.events {
		if event.Kind == DiscussionAgentStreamRunCompleted {
			return event.Response, nil
		}
	}
	return DiscussionAgentResponse{}, nil
}

func (r *streamingDiscussionAgentMentionResponder) RespondToDiscussionMentionStream(_ context.Context, request DiscussionAgentMentionRequest) (<-chan DiscussionAgentStreamEvent, error) {
	r.streamCalled = true
	r.request = request
	if r.streamStarted != nil {
		select {
		case r.streamStarted <- struct{}{}:
		default:
		}
	}
	if r.err != nil {
		return nil, r.err
	}
	if r.streamEvents != nil {
		return r.streamEvents, nil
	}
	events := make(chan DiscussionAgentStreamEvent, len(r.events))
	for _, event := range r.events {
		events <- event
	}
	close(events)
	return events, nil
}
