package discussionsession

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestOperationalFailureDisablesToolsAndSurvivesSessionResume(t *testing.T) {
	request := applyOperationalFailure(WithOperationalFailure(context.Background(), "Speech recognition failed. No approval executed."), DiscussionAgentRequest{Content: "同意"})
	for _, sessionID := range []string{"", "resumed-pi-session"} {
		prompt := discussionExternalCLIPrompt(request, discussionExternalCLISession{SessionID: sessionID, ContextFingerprint: discussionExternalCLIContextFingerprint(request)})
		if !strings.Contains(prompt, "Speech recognition failed") || !strings.Contains(prompt, "do not execute tools") || !strings.Contains(prompt, "do not approve") && !strings.Contains(prompt, "or approve anything") {
			t.Fatalf("explanation constraints missing on resume: %s", prompt)
		}
	}
	if request.ProviderSpecific[agentcore.RuntimeAllowedToolIDsProviderKey] != "[]" || request.ProviderSpecific[agentcore.RuntimeDisableSkillResourcesProviderKey] != "true" {
		t.Fatalf("runtime is not restricted: %+v", request.ProviderSpecific)
	}
}

func TestOperationalFailureRuntimeDoesNotRestoreProfileToolsOrSkills(t *testing.T) {
	agent := newLightweightTurnTestAgent()
	runner := NewBaseAgentDiscussionAgentRunner(BaseAgentDiscussionAgentRunnerOptions{Agent: agent, AgentID: "personal-assistant", RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi}})
	ctx := WithOperationalFailure(context.Background(), "Image delivery preparation failed. Do not regenerate.")
	if _, err := runner.RunDiscussion(ctx, lightweightTurnTestRequest("Generate the image and send it now")); err != nil {
		t.Fatal(err)
	}
	if len(agent.request.ToolBindings) != 0 || len(agent.request.SkillBindings) != 0 || agent.request.RuntimeOptions.ProviderSpecific[agentcore.RuntimeAllowedToolIDsProviderKey] != "[]" {
		t.Fatalf("profile resources restored for explanation: %+v", agent.request)
	}
}

func TestOperationalFailureExplanationReusesSourceAndIsIdempotent(t *testing.T) {
	runner := &streamingDiscussionAgentRunner{events: []DiscussionAgentStreamEvent{{Kind: DiscussionAgentStreamRunCompleted, Response: DiscussionAgentResponse{AssistantMessage: "Agent explanation", Status: "completed", ToolLoopCompleted: true}}}}
	service := newTestService(ServiceOptions{Repository: newMemoryRepository(t), DiscussionAgent: runner, Clock: fixedClock{now: testTime}, IDGenerator: fixedIDGen("conv-1", "msg-user", "run-1", "msg-assistant", "run-2", "msg-explanation")})
	viewer := ViewerRef{ViewerID: "user-1", ViewerKind: ViewerKindUser}
	stream, err := service.SendMessageStream(context.Background(), viewer, SendMessageCommand{Content: "生成图片", IdempotencyKey: "original"})
	if err != nil {
		t.Fatal(err)
	}
	for range stream {
	}
	cmd := ExplainOperationalFailureCommand{ConversationID: "conv-1", MessageID: "msg-user", IdempotencyKey: "explain-once", Facts: "Image delivery preparation failed. Do not regenerate."}
	for i := 0; i < 2; i++ {
		stream, err = service.ExplainOperationalFailureStream(context.Background(), viewer, cmd)
		if err != nil {
			t.Fatal(err)
		}
		answers := 0
		for event := range stream {
			if event.Kind == StreamEventError {
				t.Fatalf("explanation failed: %+v", event)
			}
			if event.Kind == StreamEventMessageCompleted {
				answers++
			}
		}
		if answers != 1 {
			t.Fatalf("replayed explanation count = %d", answers)
		}
	}
	if runner.request.ProviderSpecific[agentcore.RuntimeAllowedToolIDsProviderKey] != "[]" {
		t.Fatalf("explanation runner not restricted: %+v", runner.request)
	}
	detail, err := service.GetConversation(context.Background(), viewer, "conv-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Messages) != 3 || len(detail.Runs) != 2 {
		t.Fatalf("explanation replay duplicated user message or run: messages=%+v runs=%+v", detail.Messages, detail.Runs)
	}
	_, err = service.ExplainOperationalFailureStream(context.Background(), ViewerRef{ViewerID: "other", ViewerKind: ViewerKindUser}, cmd)
	if !errors.Is(err, ErrForbidden) && !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign viewer could explain private conversation: %v", err)
	}
}
