package codex

import (
	"context"
	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"testing"
)

func TestCodexDesktopToolsUseAuthenticatedMCPOnOriginalThreadConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	session := &NativeSession{ctx: ctx, native: NativeConfig{ToolDispatcher: runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		return runtimecore.RuntimeToolCallResult{}, nil
	})}, workspace: NativeWorkspace{MCPServers: map[string]NativeMCPServer{}, Request: agentcore.RuntimeRequest{TenantID: "tenant-test", RunID: "run-test", AgentID: "agent-test", ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: "desktop_read_excel", InputSchema: map[string]any{"type": "object"}}}}}}
	if err := session.configureDesktopMCP(); err != nil {
		t.Fatal(err)
	}
	servers := session.nativeMCPConfig()
	entry, ok := servers["orrery_desktop"].(map[string]any)
	if !ok || entry["url"] == "" || entry["http_headers"].(map[string]string)["Authorization"] == "" {
		t.Fatalf("MCP not injected: %#v", servers)
	}
	if len(session.config.MCPServers) != 1 || session.workspace.Request.RunID != "run-test" {
		t.Fatal("changed runtime identity")
	}
}
