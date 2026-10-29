package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestDesktopMCPRoundTripKeepsRunIdentityAndRejectsUnauthenticatedCalls(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var calls []runtimecore.RuntimeToolCallRequest
	dispatcher := runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		calls = append(calls, call)
		return runtimecore.RuntimeToolCallResult{Output: map[string]any{"status": "completed", "rows": [][]any{{"甲", 150}}}}, nil
	})
	request := agentcore.RuntimeRequest{TenantID: "tenant-test", RunID: "run-test", AgentID: "agent-test", ExecutionPrincipal: agentcore.ExecutionPrincipal{Kind: agentcore.ExecutionPrincipalUser, ID: "user-test", TenantID: "tenant-test"}, ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: "desktop_read_excel", Alias: "desktop_read_excel", InputSchema: map[string]any{"type": "object"}}, {ToolID: "desktop_write_excel", InputSchema: map[string]any{"type": "object"}}, {ToolID: "knowledge_search", InputSchema: map[string]any{"type": "object"}}}}
	bridge, err := StartDesktopMCP(ctx, dispatcher, request)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	post := func(auth, method string, id int, args map[string]any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": args})
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, bridge.URL, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Authorization", auth)
		req.Header.Set("MCP-Protocol-Version", "2025-11-25")
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		if auth == "" {
			if response.StatusCode != 401 {
				t.Fatal(response.StatusCode)
			}
			return nil
		}
		if response.StatusCode != 200 {
			t.Fatalf("MCP status=%d body=%s", response.StatusCode, data)
		}
		var result map[string]any
		if json.Unmarshal(data, &result) != nil || result["error"] != nil {
			t.Fatalf("MCP response=%s", data)
		}
		return result["result"].(map[string]any)
	}
	post("", "tools/list", 1, map[string]any{})
	listed := post(bridge.Authorization, "tools/list", 2, map[string]any{})
	if len(listed["tools"].([]any)) != 2 {
		t.Fatal("exposed non-desktop/unbound tool", listed)
	}
	for _, id := range []int{3, 4} {
		post(bridge.Authorization, "tools/call", id, map[string]any{"name": "desktop_write_excel", "arguments": map[string]any{"relative_name": "result.xlsx"}})
	}
	if len(calls) != 2 || calls[0].TenantID != "tenant-test" || calls[0].RunID != "run-test" || calls[0].AgentID != "agent-test" || calls[0].ExecutionPrincipal.ID != "user-test" || calls[0].CallID != calls[1].CallID || calls[0].RequestMeta["mcp_request_id"] != "3" {
		t.Fatalf("lost identity or unsafe write replay: %#v", calls)
	}
	post(bridge.Authorization, "tools/call", 5, map[string]any{"name": "desktop_read_excel", "arguments": map[string]any{}})
	if calls[2].CallID == calls[0].CallID {
		t.Fatal("read/write identity collision")
	}
}
