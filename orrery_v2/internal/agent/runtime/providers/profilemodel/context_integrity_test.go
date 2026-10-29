package profilemodel

import (
	"orrery_v2/internal/agent/llm"
	"strings"
	"testing"

	"orrery_v2/internal/agent/runtime/llmprofile"
)

func TestModelBudgetNeverClipsSystemInstructions(t *testing.T) {
	system := "Keep the tenant boundary. " + strings.Repeat("Detailed working instructions. ", 35) + " Never claim an unverified repair."
	messages := []openAICompatibleChatMessage{{Role: "system", Content: system}, {Role: "user", Content: strings.Repeat("old history ", 500)}, {Role: "user", Content: "Explain the observed failure."}}
	limited := limitOpenAICompatibleMessages(messages, 1024, nil)
	if len(limited) != 2 || limited[0].Content != system || limited[1].Content != messages[2].Content {
		t.Fatalf("required instructions changed: %+v", limited)
	}
}

func TestModelRejectsRequiredContextOverflowInsteadOfSendingClippedInstructions(t *testing.T) {
	m := &openAICompatibleChatModel{profile: llmprofile.LLMProfile{Model: "test-model", ContextLength: 128}}
	_, err := m.buildRequest([]*llm.Message{llm.SystemMessage(strings.Repeat("policy ", 100)), llm.UserMessage(strings.Repeat("current requirement ", 100))})
	if err == nil {
		t.Fatal("oversized instructions would have been silently clipped or omitted")
	}
}

func TestManagedModelKeepsTaskBeforeLaterReceiptsAndToolHistory(t *testing.T) {
	m := &openAICompatibleChatModel{managedContext: true, profile: llmprofile.LLMProfile{Model: "test-model", ContextLength: 64000}}
	input := []*llm.Message{llm.SystemMessage("Keep task and permission boundaries."), llm.UserMessage("CURRENT TASK: produce the report."), llm.UserMessage(strings.Repeat("optional memory ", 6000)), llm.UserMessage("Return the receipt after doing the task.")}
	body, err := m.buildRequest(input)
	if err != nil || len(body.Messages) != len(input) || body.Messages[1].Content != input[1].Content {
		t.Fatalf("task lost at provider boundary: %v", err)
	}
	m.profile.ContextLength = 1000
	if _, err = m.buildRequest(input); err == nil {
		t.Fatal("smaller model window silently discarded current work")
	}
}
