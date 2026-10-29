package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRuntimeRequestRecordProjectionRetainsOnlyPromptAndSafeIdentity(t *testing.T) {
	request := RuntimeRequest{
		TenantID: "tenant-private", RunID: "run-a", AgentID: "agent-a", MissionID: "mission-a", TaskID: "task-a",
		ContextPack: ContextPack{
			MissionID: "mission-a", AgentID: "agent-a", TaskID: "task-a", MissionFinalGoal: "ship safely",
			DiscussionGroupGoal: "review the bridge", WorkerPersonalGoal: "finish phase five",
			ToolSummaries: []ContextToolSummary{{Alias: "private-tool", SecretEnv: map[string]string{"TOKEN": "context-secret"}}},
			RawMemory:     "private-memory", RawDiscussionMessages: []string{"private-history"},
		},
		InputMessages: []RuntimeChatMessage{{Role: "user", Content: "prompt text", ToolCalls: []RuntimeToolCall{{ID: "call-a", Name: "inspect", Input: map[string]any{"nested": map[string]any{"value": "prompt argument"}}}}}},
		ToolBindings:  []RuntimeToolBinding{{Alias: "private-tool", SecretEnv: map[string]string{"TOKEN": "tool-secret"}}},
		SkillBindings: []RuntimeSkillBinding{{Alias: "private-skill"}},
		RuntimeOptions: RuntimeOptions{
			WorkingDirectory: `D:\private\workspace`, Model: "model-a", SystemPrompt: "system prompt", SystemContextPrompt: "context prompt",
			PromptBlocks: []RuntimePromptBlock{{BlockID: "prompt-a", Content: "prompt block"}},
			ExtraArgs:    []string{"--private-arg"}, CustomArgs: []string{"--private-custom"}, Environment: map[string]string{"TOKEN": "environment-secret"},
			ProviderSpecific: map[string]string{"runtime_kind": "codex", "executable": "private-executable", "credential_ref": "secret://tenants/tenant-private/auth"},
			ProviderConfig:   json.RawMessage(`{"executable":"private-executable","auth":"private-auth"}`),
			MCPConfig:        json.RawMessage(`{"command":"private-mcp"}`),
			WorkspacePolicy:  RuntimeWorkspacePolicy{Root: `D:\private\workspace`, WorkDir: `D:\private\workspace\agent`, WritableRoots: []string{`D:\private\write`}},
			SandboxProfile:   RuntimeSandboxProfile{Root: `D:\private\workspace`, WorkDir: `D:\private\workspace\agent`, WritableRoots: []string{`D:\private\write`}},
		},
	}

	projected := RuntimeRequestRecordProjection(request)
	if projected.RunID != request.RunID || projected.AgentID != request.AgentID || projected.MissionID != request.MissionID || projected.TaskID != request.TaskID {
		t.Fatalf("record identity = %#v", projected)
	}
	if projected.TenantID != "" || projected.RuntimeOptions.Model != "model-a" || projected.RuntimeOptions.SystemPrompt != "system prompt" || len(projected.InputMessages) != 1 {
		t.Fatalf("record projection = %#v", projected)
	}
	if len(projected.RuntimeOptions.ProviderSpecific) != 1 || projected.RuntimeOptions.ProviderSpecific["runtime_kind"] != "codex" {
		t.Fatalf("record provider metadata = %#v", projected.RuntimeOptions.ProviderSpecific)
	}
	encoded, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{
		"tenant-private", "context-secret", "private-memory", "private-history", "tool-secret", "private-tool", "private-skill",
		`D:\private\workspace`, "--private-arg", "--private-custom", "environment-secret", "private-executable", "secret://tenants/tenant-private/auth", "private-auth", "private-mcp",
	} {
		if strings.Contains(string(encoded), private) {
			t.Fatalf("runtime record retained private value %q: %s", private, encoded)
		}
	}

	projected.InputMessages[0].ToolCalls[0].Input["nested"].(map[string]any)["value"] = "mutated"
	if got := request.InputMessages[0].ToolCalls[0].Input["nested"].(map[string]any)["value"]; got != "prompt argument" {
		t.Fatalf("record projection aliases request prompt input: %v", got)
	}
}
