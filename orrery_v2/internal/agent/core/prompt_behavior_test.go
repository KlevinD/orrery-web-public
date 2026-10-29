package core

import (
	"strings"
	"testing"
)

func TestSavedPlatformPromptIncludesWorkingInstructionsExactlyOnce(t *testing.T) {
	profile := AgentProfile{ID: "researcher", BaseAgent: BaseAgentSpec{SystemPrompt: "Verify the official sources and explain conflicting evidence."}}
	blocks := []RuntimePromptBlock{
		{BlockID: PromptBlockSystemPrinciples, Role: "system", Content: "Preserve tenant boundaries.", Enabled: true},
		{BlockID: PromptBlockAgentProfileTemplate, Role: "system", Content: "Agent <<agent_id>>", Enabled: true},
		{BlockID: PromptBlockCurrentUserInput, Role: "user", Enabled: true},
	}
	blocks = NormalizeRuntimePromptBlocks(NormalizeRuntimePromptBlocks(blocks))
	messages := BuildAgentPromptMessages(AgentPromptMessagesRequest{Profile: profile, FallbackCorePrompt: profile.BaseAgent.SystemPrompt, Blocks: blocks, InputMessages: []RuntimeChatMessage{{Role: "user", Content: "Compare the proposals."}}})
	prompt := RuntimePromptMessagesSystemPrompt(messages)
	if strings.Count(prompt, profile.BaseAgent.SystemPrompt) != 1 || !strings.Contains(prompt, "Preserve tenant boundaries.") {
		t.Fatalf("work instructions missing or duplicated: %s", prompt)
	}
	if strings.Count(prompt, DefaultApprovalBoundaryPrompt()) != 1 {
		t.Fatalf("saved platform prompt lost or duplicated approval boundaries: %s", prompt)
	}
	if messages[len(messages)-1].Content != "Compare the proposals." {
		t.Fatalf("current input changed: %+v", messages)
	}
}

func TestPersonalAssistantDoesNotInheritOrganizationWorkingInstructions(t *testing.T) {
	blocks := DefaultRuntimePromptBlocks()
	blocks = append(blocks, RuntimePromptBlock{BlockID: "personal_assistant_persona", Role: "system", Content: "Act for the current private owner.", Enabled: true})
	profile := AgentProfile{BaseAgent: BaseAgentSpec{SystemPrompt: "Organization-only working instructions."}}
	prompt := RuntimePromptMessagesSystemPrompt(BuildAgentPromptMessages(AgentPromptMessagesRequest{Profile: profile, Blocks: blocks}))
	if strings.Contains(prompt, profile.BaseAgent.SystemPrompt) || strings.Count(prompt, "Act for the current private owner.") != 1 {
		t.Fatalf("personal assistant identity mixed with organization instructions: %s", prompt)
	}
}

func TestExplicitlyDisabledWorkingInstructionsStayDisabled(t *testing.T) {
	blocks := append(DefaultRuntimePromptBlocks(), RuntimePromptBlock{})
	for index := range blocks {
		if blocks[index].BlockID == PromptBlockAgentWorkingInstructions {
			blocks[index].Enabled = false
		}
	}
	prompt := RuntimePromptMessagesSystemPrompt(BuildAgentPromptMessages(AgentPromptMessagesRequest{Profile: AgentProfile{BaseAgent: BaseAgentSpec{SystemPrompt: "disabled work policy"}}, Blocks: blocks}))
	if strings.Contains(prompt, "disabled work policy") {
		t.Fatal("disabled prompt block was re-enabled")
	}
}
