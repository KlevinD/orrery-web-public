package core

import (
	"strings"
	"testing"
)

func TestStructuredContextRetainsBoundariesAndNewestHistory(t *testing.T) {
	parts := []RuntimeContextPart{
		{Content: "<identity>private tenant boundary</identity>", Required: true},
		{Content: "<resources>" + strings.Repeat("tool documentation ", 500) + "</resources>"},
		{Content: "<history>", Required: true},
		{Content: "<message>old " + strings.Repeat("old material ", 150) + "</message>", Priority: 100},
		{Content: "<message>recent fact: sample account alias ALPHA</message>", Priority: 101},
		{Content: "</history>", Required: true},
		{Content: "<current>Use ALPHA, do not send externally</current>", Required: true},
	}
	messages := []RuntimeChatMessage{{Role: "user", Content: RenderRuntimeContextParts(parts), ContextParts: parts}}
	out, stats := LimitRuntimeChatMessages(messages, 200)
	if !stats.Truncated || stats.RequiredOverflow || stats.EstimatedTokensAfter > 200 {
		t.Fatalf("invalid context statistics: %+v", stats)
	}
	for _, text := range []string{"private tenant boundary", "<history>", "recent fact", "</history>", "do not send externally"} {
		if !strings.Contains(out[0].Content, text) {
			t.Fatalf("lost %q", text)
		}
	}
	if strings.Contains(out[0].Content, "old material") || strings.Contains(out[0].Content, "context truncated") || len(out[0].ContextParts) != 0 {
		t.Fatal("structured context was sliced or omitted text leaked into metadata")
	}
	if parts[1].Content == "" {
		t.Fatal("input parts were mutated")
	}
}

func TestStructuredContextReportsRequiredOverflowWithoutCuttingInstructions(t *testing.T) {
	parts := []RuntimeContextPart{{Content: strings.Repeat("mandatory policy ", 200), Required: true}}
	out, stats := LimitRuntimeChatMessages([]RuntimeChatMessage{{Role: "user", Content: RenderRuntimeContextParts(parts), ContextParts: parts}}, 30)
	if !stats.RequiredOverflow || out[0].Content != parts[0].Content[:len(parts[0].Content)-1] {
		t.Fatalf("required instructions were not retained with overflow: %+v", stats)
	}
}

func TestToolTokenEstimateExcludesServerOnlyMaterial(t *testing.T) {
	binding := RuntimeToolBinding{ToolID: "read", Summary: "read a document", InputSchema: map[string]any{"type": "object"}}
	baseline := EstimateRuntimeToolBindingsTokens([]RuntimeToolBinding{binding})
	binding.SecretEnv = map[string]string{"KEY": strings.Repeat("secret", 1000)}
	binding.GrantedPermissions = []string{strings.Repeat("policy", 1000)}
	binding.OutputSchema = map[string]any{"description": strings.Repeat("output", 1000)}
	if EstimateRuntimeToolBindingsTokens([]RuntimeToolBinding{binding}) != baseline {
		t.Fatal("server-only metadata consumes model budget")
	}
}

func TestNativeContextBudgetDoesNotSubtractServerToolCatalog(t *testing.T) {
	for _, kind := range []RuntimeKind{RuntimeKindPi, RuntimeKindCodex, RuntimeKindClaude, RuntimeKindOpenClaw, RuntimeKindACP} {
		t.Run(string(kind), func(t *testing.T) {
			agent := NewBaseAgent(BaseAgentConfig{Profile: AgentProfile{RuntimeSpec: RuntimeSpec{Kind: kind}}})
			content := strings.Repeat("recent conversation ", 500)
			request := agent.BuildRuntimeRequest(AgentInput{
				RuntimeContextParts: []RuntimeContextPart{{Content: content, Required: true}},
				RuntimeOptions:      RuntimeOptions{PromptBlocks: []RuntimePromptBlock{{BlockID: PromptBlockRuntimeContext, Enabled: true}}},
				RuntimeToolBindings: []RuntimeToolBinding{{ToolID: "tool", InputSchema: map[string]any{"description": strings.Repeat("schema", 20000)}}},
			})
			if request.RuntimeOptions.ProviderSpecific[RuntimeContextOverflowProviderKey] != "" || RuntimePromptMessagesFallbackPrompt(request.InputMessages) != strings.TrimSpace(content) {
				t.Fatal("native tool catalog erased the injected conversation")
			}
		})
	}
}
