package core

import (
	"strings"
	"testing"
)

func TestScheduledTaskSurvivesReceiptAndHistoryTrimming(t *testing.T) {
	for _, blocks := range [][]RuntimePromptBlock{nil, {{BlockID: PromptBlockRuntimeContext, Enabled: true}, {BlockID: PromptBlockCurrentUserInput, Enabled: true}}} {
		request := AgentPromptMessagesRequest{Task: TaskInput{ID: "scheduled-task", Goal: "Produce a source-backed report.", WorkerPersonalGoal: "Write the report and record its concrete artifact path."},
			InputMessages: []RuntimeChatMessage{{Role: "assistant", Content: strings.Repeat("old history ", 5000)}, {Role: "user", Content: "Finish with the required result receipt."}}, Blocks: blocks}
		messages := BuildAgentPromptMessages(request)
		out, stats := LimitRuntimeChatMessages(messages, 3500)
		text := RuntimePromptMessagesFallbackPrompt(out)
		if stats.RequiredOverflow || !strings.Contains(text, request.Task.Goal) || !strings.Contains(text, request.Task.WorkerPersonalGoal) {
			t.Fatalf("current task lost or required context overflow: %+v", stats)
		}
		_, tiny := LimitRuntimeChatMessages(messages, 10)
		if !tiny.RequiredOverflow {
			t.Fatal("oversized task was silently dropped")
		}
	}
}
