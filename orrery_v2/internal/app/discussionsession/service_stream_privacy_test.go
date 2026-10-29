package discussionsession

import (
	"context"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestServiceSendMessageStreamSuppressesRawAndTaggedReasoning(t *testing.T) {
	repo := newMemoryRepository(t)
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamThinkingDelta, Delta: "raw provider reasoning", ReasoningKind: agentcore.ReasoningContentRaw},
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "<th"},
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "ink>private "},
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "reasoning</thi"},
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "nk>Visible "},
		{Kind: DiscussionAgentStreamTokenDelta, Delta: "answer"},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: "<think>private reasoning</think>Visible answer",
			RuntimeResponse:  "trace <think>debug private</think>public trace",
			Status:           "completed",
			RuntimeKind:      "pi",
		}},
	}}
	service := newTestService(ServiceOptions{
		Repository:      repo,
		DiscussionAgent: runner,
		Clock:           fixedClock{now: testTime},
		IDGenerator:     fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})

	events, err := service.SendMessageStream(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, SendMessageCommand{Content: "Answer", IdempotencyKey: "send-private-reasoning"})
	if err != nil {
		t.Fatal(err)
	}
	var streamedText strings.Builder
	thinkingEvents := 0
	for event := range events {
		switch event.Kind {
		case StreamEventTokenDelta:
			streamedText.WriteString(event.Delta)
		case StreamEventThinkingDelta:
			thinkingEvents++
		}
		if strings.Contains(strings.ToLower(event.Delta), "private") || strings.Contains(strings.ToLower(event.Delta), "<think") {
			t.Fatalf("private reasoning escaped in stream event: %+v", event)
		}
	}
	if streamedText.String() != "Visible answer" || thinkingEvents != 0 {
		t.Fatalf("streamed text = %q, thinking events = %d", streamedText.String(), thinkingEvents)
	}

	detail, err := service.GetConversation(context.Background(), ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 2 || !strings.Contains(detail.Messages[1].Content, "Visible answer") || strings.Contains(strings.ToLower(detail.Messages[1].Content), "private") {
		t.Fatalf("persisted messages = %+v", detail.Messages)
	}
	if len(detail.Runs) != 1 || detail.Runs[0].ThinkingSummary != "" {
		t.Fatalf("public runs = %+v", detail.Runs)
	}

	run, err := repo.GetRun(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if run.ProviderMetadata[discussionOutputPrivacyPolicyKey] != discussionOutputPrivacyPolicyVersion || run.ProviderMetadata[discussionReasoningSuppressedKey] != "true" {
		t.Fatalf("run privacy audit = %+v", run.ProviderMetadata)
	}
	if run.Debug == nil || run.Debug.RuntimeResponse != "trace public trace" {
		t.Fatalf("run debug = %+v", run.Debug)
	}
}
