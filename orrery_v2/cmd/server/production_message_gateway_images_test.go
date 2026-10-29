package main

import (
	"context"
	"testing"

	"orrery_v2/internal/app/discussionsession"
	gwprotocol "orrery_v2/internal/gateway/protocol"
)

type imageFailureDiscussionService struct {
	fakeProductionGatewayDiscussionService
	explanations     int
	explanation      discussionsession.ExplainOperationalFailureCommand
	explanationError error
}

func (f *imageFailureDiscussionService) ExplainOperationalFailureStream(_ context.Context, _ discussionsession.ViewerRef, cmd discussionsession.ExplainOperationalFailureCommand) (<-chan discussionsession.StreamEventDTO, error) {
	f.explanations++
	f.explanation = cmd
	if f.explanationError != nil {
		return nil, f.explanationError
	}
	events := make(chan discussionsession.StreamEventDTO, 1)
	events <- discussionsession.StreamEventDTO{Kind: discussionsession.StreamEventMessageCompleted, Message: &discussionsession.MessageDTO{Role: "assistant", Content: "图片已保存，回传没有完成。可以在会话附件查看。"}}
	close(events)
	return events, nil
}

func TestProductionGatewayImageFailureUsesOneAgentExplanationWithoutResendingTask(t *testing.T) {
	service := &imageFailureDiscussionService{}
	runner := personalAssistantApprovalReplyTestRunner(t, &service.fakeProductionGatewayDiscussionService, &recordingPersonalAssistantApprovalReplyHandler{})
	runner.discussions = service
	result := map[string]string{"conversation_id": "conv-image", "attachment_id": "image-1", "status": "completed"}
	service.events = []discussionsession.StreamEventDTO{
		{Kind: discussionsession.StreamEventSnapshot, Conversation: &discussionsession.ConversationDTO{ConversationID: "conv-image", Messages: []discussionsession.MessageDTO{{MessageID: "original-user", Role: "user", Content: "生成图片"}}}},
		{Kind: discussionsession.StreamEventToolResult, ToolCall: &discussionsession.ToolCallDTO{Name: "image_generate", Result: result}},
		{Kind: discussionsession.StreamEventMessageCompleted, Message: &discussionsession.MessageDTO{Role: "assistant", Content: "图片已经发给你了。"}},
		{Kind: discussionsession.StreamEventRunCompleted, Run: &discussionsession.RunDTO{Status: "completed", ToolCalls: []discussionsession.ToolCallDTO{{Name: "image_generate", Result: result}}}},
	}
	input := voiceTestInput()
	input.InputModality, input.Message = "", "生成图片"
	events, err := runner.RunTurn(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	got := collectGatewayEvents(events)
	texts := []string{}
	for _, event := range got {
		if event.Type == gwprotocol.EventAssistantTextDelta {
			texts = append(texts, event.Text)
		}
	}
	if len(texts) != 1 || texts[0] != "图片已保存，回传没有完成。可以在会话附件查看。" || len(service.cmds) != 1 || service.explanations != 1 {
		t.Fatalf("unexpected replies or replay: texts=%+v original=%d explanations=%d", texts, len(service.cmds), service.explanations)
	}
	if service.explanation.MessageID != "original-user" || service.explanation.ConversationID != "conv-image" {
		t.Fatalf("incorrect source: %+v", service.explanation)
	}
}
