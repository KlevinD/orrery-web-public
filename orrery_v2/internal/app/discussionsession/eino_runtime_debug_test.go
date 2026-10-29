package discussionsession

import (
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestOutboundPromptPrefersCapturedRuntimeMessages(t *testing.T) {
	req := agentcore.RuntimeRequest{
		RunID:     "run-1",
		MissionID: "discussion-conv-1",
		TaskID:    "conv-1",
		RuntimeOptions: agentcore.RuntimeOptions{
			SystemPrompt: "fallback system should not be reconstructed",
		},
		ContextPack: agentcore.ContextPack{
			MissionFinalGoal: "fallback user should not be reconstructed",
		},
	}
	response := agentcore.RuntimeResponse{
		RunID:  "run-1",
		Status: agentcore.RuntimeStatusCompleted,
		OutboundMessages: []agentcore.RuntimeChatMessage{
			{Role: "system", Content: "<system>exact system</system>"},
			{Role: "user", Content: "<current_discussion_group>exact group</current_discussion_group>"},
			{Role: "assistant", Content: "model draft", ToolCalls: []agentcore.RuntimeToolCall{{
				ID: "call-1", Name: "mission_prepare", Input: map[string]any{"title": "Launch"},
			}}},
			{Role: "tool", ToolName: "mission_prepare", ToolCallID: "call-1", Content: `{"mission_id":"mission-1"}`},
		},
		CompletedAt: time.Now().UTC(),
	}

	got := outboundPromptFromRuntimeExchange(req, response, agentcore.RuntimeKindManaged)
	for _, want := range []string{
		`role: system`,
		`<system>exact system</system>`,
		`role: user`,
		`<current_discussion_group>exact group</current_discussion_group>`,
		`tool_call mission_prepare`,
		`role: tool`,
		`{"mission_id":"mission-1"}`,
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("debug prompt missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "fallback system") || strings.Contains(got, "fallback user") {
		t.Fatalf("debug prompt should use captured runtime messages, got:\n%s", got)
	}
}

func TestOutboundPromptUsesExactRuntimeInputMessagesWithoutProviderCapture(t *testing.T) {
	req := agentcore.RuntimeRequest{
		RunID: "run-pi",
		RuntimeOptions: agentcore.RuntimeOptions{
			WorkingDirectory: "D:/workspace/agent",
			WorkspacePolicy: agentcore.RuntimeWorkspacePolicy{
				Root:            "D:/workspace",
				WorkDir:         "D:/workspace/agent",
				EnforceBoundary: true,
			},
		},
		InputMessages: []agentcore.RuntimeChatMessage{{
			Role:    "user",
			Content: "<discussion_context>\nexact context\n</discussion_context>\n\n<current_user_input>\nexact input\n</current_user_input>",
		}},
		ContextPack: agentcore.ContextPack{
			MissionFinalGoal:   "reconstructed fallback",
			WorkerPersonalGoal: "reconstructed fallback",
		},
	}

	got := outboundPromptFromRuntimeRequest(req, agentcore.RuntimeKindPi)
	for _, want := range []string{"Workspace boundary:", "<discussion_context>", "exact context", "exact input"} {
		if !strings.Contains(got, want) {
			t.Fatalf("debug prompt missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "reconstructed fallback") {
		t.Fatalf("debug prompt reconstructed a prompt that was not sent:\n%s", got)
	}
}
