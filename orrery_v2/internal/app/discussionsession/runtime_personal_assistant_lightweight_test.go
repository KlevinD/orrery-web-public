package discussionsession

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestPersonalAssistantLightweightTurnClassification(t *testing.T) {
	for _, content := range []string{"Hi!", "  HELLO  ", "你好！", "你是谁？", "谢谢。"} {
		request := lightweightTurnTestRequest(content)
		if !isPersonalAssistantLightweightTurn(request) {
			t.Errorf("content %q was not classified as lightweight", content)
		}
	}
	for _, content := range []string{"Hi, search the web", "查一下天气", "帮我创建任务"} {
		request := lightweightTurnTestRequest(content)
		if isPersonalAssistantLightweightTurn(request) {
			t.Errorf("action content %q was classified as lightweight", content)
		}
	}
	regular := lightweightTurnTestRequest("Hi")
	regular.ConversationKind = ConversationKindGroup
	if isPersonalAssistantLightweightTurn(regular) {
		t.Fatal("regular discussion was classified as a personal-assistant lightweight turn")
	}
}

func TestPersonalAssistantLightweightTurnRemovesRuntimeResources(t *testing.T) {
	agent := newLightweightTurnTestAgent()
	runner := NewBaseAgentDiscussionAgentRunner(BaseAgentDiscussionAgentRunnerOptions{
		Agent: agent, AgentID: "personal-assistant", RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi},
	})

	_, err := runner.RunDiscussion(context.Background(), lightweightTurnTestRequest("Hi!"))
	if err != nil {
		t.Fatalf("RunDiscussion: %v", err)
	}
	if len(agent.request.ToolBindings) != 0 || len(agent.request.SkillBindings) != 0 {
		t.Fatalf("lightweight resources = tools %+v skills %+v", agent.request.ToolBindings, agent.request.SkillBindings)
	}
	provider := agent.request.RuntimeOptions.ProviderSpecific
	if provider[agentcore.RuntimeAllowedToolIDsProviderKey] != "[]" ||
		provider[agentcore.RuntimeDisableSkillResourcesProviderKey] != "true" ||
		provider[agentcore.RuntimeRequireExplicitToolScopesProviderKey] != "true" ||
		provider[personalAssistantLightweightTurnProviderKey] != "true" {
		t.Fatalf("lightweight provider metadata = %+v", provider)
	}
}

func TestPersonalAssistantActionTurnKeepsRuntimeResources(t *testing.T) {
	agent := newLightweightTurnTestAgent()
	runner := NewBaseAgentDiscussionAgentRunner(BaseAgentDiscussionAgentRunnerOptions{
		Agent: agent, AgentID: "personal-assistant", RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi},
	})

	_, err := runner.RunDiscussion(context.Background(), lightweightTurnTestRequest("Please search the web for today's weather."))
	if err != nil {
		t.Fatalf("RunDiscussion: %v", err)
	}
	if !runtimeHasToolBinding(agent.request.ToolBindings, toolstd.WebSearch) ||
		!runtimeHasToolBinding(agent.request.ToolBindings, "load_skill") ||
		len(agent.request.SkillBindings) != 1 {
		t.Fatalf("action resources = tools %+v skills %+v", agent.request.ToolBindings, agent.request.SkillBindings)
	}
	if agent.request.RuntimeOptions.ProviderSpecific[personalAssistantLightweightTurnProviderKey] != "" {
		t.Fatalf("action turn marked lightweight: %+v", agent.request.RuntimeOptions.ProviderSpecific)
	}
}

func TestPersonalAssistantReadOnlyReviewRemovesRuntimeResources(t *testing.T) {
	agent := newLightweightTurnTestAgent()
	runner := NewBaseAgentDiscussionAgentRunner(BaseAgentDiscussionAgentRunnerOptions{
		Agent: agent, AgentID: "personal-assistant", RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi},
	})
	request := lightweightTurnTestRequest("Review this pending approval and return the requested analysis.")
	request.ProviderSpecific = map[string]string{PersonalAssistantReadOnlyReviewProviderKey: "true"}

	if _, err := runner.RunDiscussion(context.Background(), request); err != nil {
		t.Fatalf("RunDiscussion: %v", err)
	}
	if len(agent.request.ToolBindings) != 0 || len(agent.request.SkillBindings) != 0 {
		t.Fatalf("review resources = tools %+v skills %+v", agent.request.ToolBindings, agent.request.SkillBindings)
	}
	provider := agent.request.RuntimeOptions.ProviderSpecific
	if provider[agentcore.RuntimeAllowedToolIDsProviderKey] != "[]" ||
		provider[agentcore.RuntimeDisableSkillResourcesProviderKey] != "true" ||
		provider[agentcore.RuntimeRequireExplicitToolScopesProviderKey] != "true" ||
		provider[PersonalAssistantReadOnlyReviewProviderKey] != "true" {
		t.Fatalf("review provider metadata = %+v", provider)
	}
	if provider[personalAssistantLightweightTurnProviderKey] != "" {
		t.Fatalf("review was mislabeled as a lightweight utterance: %+v", provider)
	}
}

func newLightweightTurnTestAgent() *fakeAssembledAgent {
	return newFakeAssembledAgent(fakeAssembledAgentOptions{
		profile: agentcore.AgentProfile{ID: "personal-assistant", RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi}},
		bindings: agentcore.AgentBindings{
			Tools:  []agentcore.ToolBinding{{ToolID: toolstd.WebSearch, Summary: "Search the web."}},
			Skills: []agentcore.SkillBinding{{SkillID: "web-research", Summary: "Research current information."}},
		},
	})
}

func lightweightTurnTestRequest(content string) DiscussionAgentRequest {
	return DiscussionAgentRequest{
		ConversationID: "conv-personal-assistant", ConversationKind: ConversationKindPersonalAssistant,
		ConversationTenantID: "tenant-a", ConversationOwnerID: "owner-a", MessageID: "message-1", RunID: "run-1",
		AgentID: "personal-assistant", LogicalAgentID: PersonalAssistantAgentID("tenant-a", "owner-a"), Content: content,
	}
}
