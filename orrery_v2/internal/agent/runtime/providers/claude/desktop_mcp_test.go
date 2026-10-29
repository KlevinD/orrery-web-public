package claude

import (
	"context"
	"encoding/json"
	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"testing"
)

func TestClaudeDesktopToolsUseAuthenticatedMCPOnOriginalSessionConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &NativeSession{ctx: ctx, native: NativeConfig{ToolDispatcher: runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		return runtimecore.RuntimeToolCallResult{}, nil
	})}, workspace: NativeWorkspace{Request: agentcore.RuntimeRequest{TenantID: "tenant-test", RunID: "run-test", AgentID: "agent-test", ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: "desktop_read_excel", InputSchema: map[string]any{"type": "object"}}}}}}
	if err := session.configureDesktopMCP(); err != nil {
		t.Fatal(err)
	}
	encoded, err := nativeClaudeMCPJSON(session.config, session.workspace)
	if err != nil {
		t.Fatal(err)
	}
	var config map[string]map[string]map[string]any
	if json.Unmarshal([]byte(encoded), &config) != nil {
		t.Fatal("invalid MCP JSON")
	}
	entry := config["mcpServers"]["orrery_desktop"]
	if entry["type"] != "http" || entry["url"] == "" || entry["headers"].(map[string]any)["Authorization"] == "" {
		t.Fatalf("MCP not injected: %#v", config)
	}
}
