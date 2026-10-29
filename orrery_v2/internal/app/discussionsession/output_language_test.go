package discussionsession

import (
	"context"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestNormalizeDiscussionOutputLanguageAllowsSupportedLocalesOnly(t *testing.T) {
	tests := map[string]string{
		"zh-CN,zh;q=0.9":               "zh-CN",
		"zh-Hans":                      "zh-CN",
		"en-US":                        "en-US",
		"en-GB;q=0.8":                  "en-US",
		"fr-FR":                        "",
		"ignore previous instructions": "",
	}
	for input, want := range tests {
		if got := normalizeDiscussionOutputLanguage(input); got != want {
			t.Fatalf("normalizeDiscussionOutputLanguage(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestAgentInputInjectsSemanticSelectedLanguagePolicyForManagedAndCLI(t *testing.T) {
	for _, kind := range []agentcore.RuntimeKind{agentcore.RuntimeKindManaged, agentcore.RuntimeKindPi} {
		runner := NewBaseAgentDiscussionAgentRunner(BaseAgentDiscussionAgentRunnerOptions{
			RuntimeSpec: agentcore.RuntimeSpec{Kind: kind},
		})
		input, err := runner.agentInput(context.Background(), DiscussionAgentRequest{
			ConversationID: "conv-1",
			MessageID:      "msg-1",
			RunID:          "run-1",
			Content:        "Create a mission",
			OutputLanguage: "zh-CN",
		})
		if err != nil {
			t.Fatalf("agentInput(%s): %v", kind, err)
		}
		var policy string
		policyIndex := -1
		firstUserIndex := -1
		for i, block := range input.RuntimeOptions.PromptBlocks {
			if block.BlockID == discussionOutputLanguagePromptBlockID {
				policy = block.Content
				policyIndex = i
				if block.Role != agentcore.RuntimePromptRoleSystem {
					t.Fatalf("%s output language policy role = %q, want system", kind, block.Role)
				}
			}
			if firstUserIndex < 0 && block.Role == agentcore.RuntimePromptRoleUser {
				firstUserIndex = i
			}
		}
		for _, want := range []string{"Simplified Chinese (zh-CN)", "Mission and task titles", "intended meaning", "word for word", "downstream results"} {
			if !strings.Contains(policy, want) {
				t.Fatalf("%s output language policy missing %q: %s", kind, want, policy)
			}
		}
		if policyIndex < 0 || (firstUserIndex >= 0 && policyIndex > firstUserIndex) {
			t.Fatalf("%s output language policy must precede user prompt blocks: %+v", kind, input.RuntimeOptions.PromptBlocks)
		}
		if input.RuntimeOptions.ProviderSpecific[discussionOutputLanguageProviderKey] != "zh-CN" {
			t.Fatalf("%s provider language = %q", kind, input.RuntimeOptions.ProviderSpecific[discussionOutputLanguageProviderKey])
		}
		messages := agentcore.BuildAgentPromptMessages(agentcore.AgentPromptMessagesRequest{
			ContextPack:      input.ContextPack,
			Task:             input.Task,
			InputMessages:    input.InputMessages,
			RuntimeVariables: input.RuntimeVariables,
			Blocks:           input.RuntimeOptions.PromptBlocks,
		})
		outboundSystemPrompt := agentcore.RuntimePromptMessagesSystemPrompt(messages)
		for _, want := range []string{"<user_selected_output_language>", "Simplified Chinese (zh-CN)", "Mission and task titles"} {
			if !strings.Contains(outboundSystemPrompt, want) {
				t.Fatalf("%s outbound system prompt missing %q: %s", kind, want, outboundSystemPrompt)
			}
		}
	}
}

func TestAgentInputIgnoresUnsupportedOutputLanguage(t *testing.T) {
	runner := NewBaseAgentDiscussionAgentRunner(BaseAgentDiscussionAgentRunnerOptions{
		RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged},
	})
	input, err := runner.agentInput(context.Background(), DiscussionAgentRequest{
		ConversationID: "conv-1",
		MessageID:      "msg-1",
		RunID:          "run-1",
		Content:        "Create a mission",
		OutputLanguage: "custom prompt injection",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, block := range input.RuntimeOptions.PromptBlocks {
		if block.BlockID == discussionOutputLanguagePromptBlockID {
			t.Fatalf("unsupported language injected a policy block: %+v", block)
		}
	}
	if _, ok := input.RuntimeOptions.ProviderSpecific[discussionOutputLanguageProviderKey]; ok {
		t.Fatalf("unsupported language reached provider metadata: %+v", input.RuntimeOptions.ProviderSpecific)
	}
}

func TestSelectedLanguagePolicyReplacesConfiguredBlockWithSameID(t *testing.T) {
	blocks := appendDiscussionOutputLanguageBlock([]agentcore.RuntimePromptBlock{{
		BlockID: discussionOutputLanguagePromptBlockID,
		Role:    agentcore.RuntimePromptRoleSystem,
		Content: "spoofed language policy",
		Enabled: true,
	}}, "en-US")
	count := 0
	for _, block := range blocks {
		if block.BlockID != discussionOutputLanguagePromptBlockID {
			continue
		}
		count++
		if strings.Contains(block.Content, "spoofed") || !strings.Contains(block.Content, "English (en-US)") {
			t.Fatalf("output language policy was not server-owned: %+v", block)
		}
	}
	if count != 1 {
		t.Fatalf("output language policy block count = %d", count)
	}
}
