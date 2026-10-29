package discussionsession

import (
	"context"
	"strings"
	"testing"
)

func TestServiceSendMessageStreamDeliversAgentExplanationAfterWebTimeout(t *testing.T) {
	const answer = "[@本地用户](mention://user/user-1) 网页访问超时，我还没有取得页面内容，无法确认其中的说法。可以换一个来源继续查证。"
	tool := ToolCallDTO{
		ToolCallID: "fetch-1", Name: "web_fetch", Status: "failed",
		Arguments: map[string]string{"url": "https://example.org/source"},
		Result:    map[string]string{"error": "context deadline exceeded"},
	}
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{
		{Kind: DiscussionAgentStreamToolResult, ToolCall: tool},
		{Kind: DiscussionAgentStreamTokenDelta, Delta: answer},
		{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{
			AssistantMessage: answer, Status: "completed", RuntimeKind: "pi",
			ToolLoopCompleted: true, ToolCalls: []ToolCallDTO{tool},
		}},
	}}
	repo := newMemoryRepository(t)
	service := newTestService(ServiceOptions{
		Repository: repo, DiscussionAgent: runner, Clock: fixedClock{now: testTime},
		IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant"),
	})
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}
	events, err := service.SendMessageStream(context.Background(), viewer, SendMessageCommand{
		Content: "核实网页内容", IdempotencyKey: "web-timeout-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	completed := false
	for event := range events {
		if event.Kind == StreamEventError {
			t.Fatalf("handled web failure must not become a gateway retry: %s", event.Error)
		}
		if event.Kind == StreamEventMessageCompleted && event.Message != nil && event.Message.Content != answer {
			t.Fatalf("Agent answer was replaced: %q", event.Message.Content)
		}
		completed = completed || event.Kind == StreamEventRunCompleted
	}
	if !completed {
		t.Fatal("Agent turn did not complete")
	}
	detail, err := service.GetConversation(context.Background(), viewer, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 2 || detail.Messages[1].Content != answer {
		t.Fatalf("Agent answer was not persisted unchanged: %+v", detail.Messages)
	}
	if len(detail.Runs) != 1 || detail.Runs[0].Status != "completed" || len(detail.Runs[0].ToolCalls) != 1 || detail.Runs[0].ToolCalls[0].Status != "failed" {
		t.Fatalf("turn completion must preserve the tool failure: %+v", detail.Runs)
	}
}

func TestDiscussionWebFailurePolicySurvivesSessionResume(t *testing.T) {
	request := DiscussionAgentRequest{Content: "继续核实网页内容"}
	for _, sessionID := range []string{"", "existing-pi-session"} {
		t.Run("session="+sessionID, func(t *testing.T) {
			prompt := discussionExternalCLIPrompt(request, discussionExternalCLISession{
				SessionID: sessionID, ContextFingerprint: discussionExternalCLIContextFingerprint(request),
			})
			if !strings.Contains(prompt, "外部依赖错误由你向用户解释") || !strings.Contains(prompt, "不得声称访问成功") {
				t.Fatalf("Agent did not receive web failure handling guidance: %s", prompt)
			}
		})
	}
}
