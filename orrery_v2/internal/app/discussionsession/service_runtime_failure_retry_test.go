package discussionsession

import (
	"context"
	"errors"
	"testing"
)

func TestServiceRuntimeFailureRetriesSameInboundMessageUntilSuccess(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		name := "synchronous"
		if streaming {
			name = "streaming"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			repo := newMemoryRepository(t)
			runner := &streamingDiscussionAgentRunner{}
			service := newTestService(ServiceOptions{
				Repository: repo, DiscussionAgent: runner, Clock: fixedClock{now: testTime},
				IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "run-2", "run-3", "msg-assistant"),
			})
			viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}
			cmd := SendMessageCommand{Content: "Explain the invoice types", IdempotencyKey: "gateway-inbound-1"}
			for attempt := 0; attempt < 3; attempt++ {
				failure := NewRuntimeExecutionError("pi", "OpenAI API error (525)")
				runner.response = DiscussionAgentResponse{Status: "failed", RuntimeKind: "pi"}
				runner.err = failure
				runner.events = []DiscussionAgentStreamEvent{{Kind: DiscussionAgentStreamRuntimeError, Error: failure, Response: runner.response}}
				if streaming {
					runner.err = nil
				}
				if attempt == 2 {
					runner.err = nil
					runner.response = DiscussionAgentResponse{AssistantMessage: "Recovered answer", Status: "completed", RuntimeKind: "pi"}
					runner.events = []DiscussionAgentStreamEvent{{Kind: DiscussionAgentStreamRunCompleted, Response: runner.response}}
				}
				if streaming {
					stream, err := service.SendMessageStream(ctx, viewer, cmd)
					if err != nil {
						t.Fatalf("attempt %d stream start: %v", attempt, err)
					}
					failed := false
					for event := range stream {
						failed = failed || event.Kind == StreamEventError
					}
					if failed != (attempt < 2) {
						t.Fatalf("attempt %d error event = %v", attempt, failed)
					}
				} else {
					_, err := service.SendMessage(ctx, viewer, cmd)
					if attempt < 2 && !errors.Is(err, ErrRuntimeExecution) {
						t.Fatalf("attempt %d error = %v", attempt, err)
					}
					if attempt == 2 && err != nil {
						t.Fatalf("recovered attempt: %v", err)
					}
				}
				command, err := repo.GetCommand(ctx, "send_message", viewer, cmd.IdempotencyKey)
				want := discussionCommandFailed
				if attempt == 2 {
					want = discussionCommandCompleted
				}
				if err != nil || command.Status != want || command.RetryCount != attempt {
					t.Fatalf("attempt %d command = %+v, err=%v", attempt, command, err)
				}
				if attempt < 2 && command.ErrorCode != "runtime_execution" {
					t.Fatalf("attempt %d must keep its retryable error code: %+v", attempt, command)
				}
			}
			view, err := repo.GetConversation(ctx, "conv-1")
			if err != nil || len(view.Messages) != 2 || view.Messages[0].MessageID != "msg-user" || view.Messages[1].Role != "assistant" {
				t.Fatalf("recovery must reuse the user message and persist one answer: view=%+v err=%v", view, err)
			}
		})
	}
}
