package mcpruntime

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	toolres "orrery_v2/internal/resource/tool"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestRuntimeListsStreamableHTTPToolsWithDescriptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcpJSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-1")
			writeRuntimeTestRPC(w, map[string]any{"protocolVersion": "2025-06-18"})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") != "session-1" {
				t.Fatalf("missing session id header")
			}
			writeRuntimeTestRPC(w, map[string]any{"tools": []map[string]any{{
				"name":        "search_issues",
				"description": "Search GitHub issues by title or label",
				"inputSchema": map[string]any{"type": "object"},
			}}})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer server.Close()
	runtime := New(Options{Client: server.Client()})

	list, err := runtime.ListTools(context.Background(), runtimeHTTPTestDefinition(server.URL))
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(list.Tools) != 1 || list.Tools[0] != "search_issues" {
		t.Fatalf("tools = %+v", list.Tools)
	}
	if len(list.ToolDetails) != 1 || !strings.Contains(list.ToolDetails[0].Description, "GitHub") || list.ToolDetails[0].InputSchema["type"] != "object" {
		t.Fatalf("details = %+v", list.ToolDetails)
	}
}

func TestRuntimeSendsConfiguredHeadersToStreamableHTTPServer(t *testing.T) {
	var seenMethods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk_test" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"Unauthorized: Invalid or missing API Key"}}`))
			return
		}
		if r.Header.Get("X-Tenant") != "demo" {
			t.Fatalf("missing X-Tenant header")
		}
		var req mcpJSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		seenMethods = append(seenMethods, req.Method)
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-1")
			writeRuntimeTestRPC(w, map[string]any{"protocolVersion": "2025-06-18"})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeRuntimeTestRPC(w, map[string]any{"tools": []map[string]any{{"name": "search_jobs"}}})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer server.Close()
	runtime := New(Options{Client: server.Client()})
	definition := runtimeHTTPTestDefinition(server.URL)
	definition.MCP.Headers = map[string]string{
		"Authorization": "Bearer sk_test",
		"X-Tenant":      "demo",
	}

	list, err := runtime.ListTools(context.Background(), definition)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(list.Tools) != 1 || list.Tools[0] != "search_jobs" {
		t.Fatalf("tools = %+v", list.Tools)
	}
	if strings.Join(seenMethods, ",") != "initialize,notifications/initialized,tools/list" {
		t.Fatalf("seen methods = %+v", seenMethods)
	}
}

func TestRuntimeResolvesSecretTemplatesInStreamableHTTPHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer resolved-public-key" {
			t.Fatalf("authorization header = %q", got)
		}
		var req mcpJSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-secret")
			writeRuntimeTestRPC(w, map[string]any{"protocolVersion": "2025-06-18"})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeRuntimeTestRPC(w, map[string]any{"tools": []map[string]any{{"name": "search_documents"}}})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer server.Close()

	runtime := New(Options{
		Client: server.Client(),
		SecretResolver: staticRuntimeSecretResolver{
			"secret://mcp/orrery-public-readonly/api-key": "resolved-public-key",
		},
	})
	definition := runtimeHTTPTestDefinition(server.URL)
	definition.MCP.Headers = map[string]string{
		"Authorization": "Bearer ${secret://mcp/orrery-public-readonly/api-key}",
	}

	list, err := runtime.ListTools(context.Background(), definition)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(list.Tools) != 1 || list.Tools[0] != "search_documents" {
		t.Fatalf("tools = %+v", list.Tools)
	}
}

func TestRuntimeRejectsSecretHeaderTemplateWithoutResolver(t *testing.T) {
	runtime := New(Options{})
	definition := runtimeHTTPTestDefinition("https://mcp.example.test")
	definition.MCP.Headers = map[string]string{
		"Authorization": "Bearer ${secret://mcp/orrery-public-readonly/api-key}",
	}

	_, err := runtime.ListTools(context.Background(), definition)
	if err == nil || !strings.Contains(err.Error(), "secret resolver is not configured") {
		t.Fatalf("ListTools error = %v", err)
	}
}

func TestRuntimeListsStreamableHTTPToolsWithoutInitializedNotificationWhenSessionIsAbsent(t *testing.T) {
	var seenMethods []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcpJSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		seenMethods = append(seenMethods, req.Method)
		switch req.Method {
		case "initialize":
			writeRuntimeTestRPC(w, map[string]any{"protocolVersion": "2025-06-18"})
		case "notifications/initialized":
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32603,"message":"Cannot connect to MCP server"}}`))
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") != "" {
				t.Fatalf("unexpected session id header")
			}
			writeRuntimeTestRPC(w, map[string]any{"tools": []map[string]any{{"name": "search_jobs"}}})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer server.Close()
	runtime := New(Options{Client: server.Client()})

	list, err := runtime.ListTools(context.Background(), runtimeHTTPTestDefinition(server.URL))
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(list.Tools) != 1 || list.Tools[0] != "search_jobs" {
		t.Fatalf("tools = %+v", list.Tools)
	}
	if strings.Join(seenMethods, ",") != "initialize,tools/list" {
		t.Fatalf("seen methods = %+v", seenMethods)
	}
}

func TestRuntimeCheckHealthSendsConfiguredHeadersToStreamableHTTPInitialize(t *testing.T) {
	seenAuthorization := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenAuthorization = r.Header.Get("Authorization")
		if seenAuthorization != "Bearer sk_valid" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"Unauthorized: Invalid or missing API Key"}}`))
			return
		}
		var req mcpJSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		if req.Method != "initialize" {
			t.Fatalf("unexpected method %q", req.Method)
		}
		w.Header().Set("Mcp-Session-Id", "session-1")
		writeRuntimeTestRPC(w, map[string]any{"protocolVersion": "2025-06-18"})
	}))
	defer server.Close()
	runtime := New(Options{Client: server.Client()})
	definition := runtimeHTTPTestDefinition(server.URL)
	definition.MCP.Headers = map[string]string{"Authorization": "Bearer sk_valid"}

	health, err := runtime.CheckHealth(context.Background(), definition)
	if err != nil {
		t.Fatalf("CheckHealth: %v; seen authorization %q", err, seenAuthorization)
	}
	if health.Status != "available" {
		t.Fatalf("health = %+v", health)
	}
}

func TestRuntimeCheckHealthReportsUnauthorizedHTTPStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"Unauthorized: Invalid or missing API Key"}}`))
	}))
	defer server.Close()
	runtime := New(Options{Client: server.Client()})

	health, err := runtime.CheckHealth(context.Background(), runtimeHTTPTestDefinition(server.URL))
	if err == nil {
		t.Fatalf("expected unauthorized health error")
	}
	if health.HTTPStatus != http.StatusUnauthorized {
		t.Fatalf("health should include 401 status: %+v", health)
	}
}

func TestRuntimeCallsStreamableHTTPTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcpJSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-1")
			writeRuntimeTestRPC(w, map[string]any{"protocolVersion": "2025-06-18"})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			writeRuntimeTestRPC(w, map[string]any{
				"content":           []map[string]any{{"type": "text", "text": "ok"}},
				"structuredContent": map[string]any{"status": "ok"},
			})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer server.Close()
	runtime := New(Options{Client: server.Client()})

	result, err := runtime.CallTool(context.Background(), mcptransport.ExternalMCPCallRequest{
		Server:             runtimeHTTPTestDefinition(server.URL),
		DownstreamToolName: "search_issues",
		Arguments:          map[string]any{"query": "bug"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.Output["status"] != "ok" {
		t.Fatalf("output = %+v", result.Output)
	}
}

func TestRuntimePreservesImageContentAlongsideStructuredOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcpJSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-1")
			writeRuntimeTestRPC(w, map[string]any{"protocolVersion": "2025-06-18"})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			writeRuntimeTestRPC(w, map[string]any{
				"content": []map[string]any{
					{"type": "text", "text": "Took a screenshot."},
					{"type": "image", "data": "aW1hZ2U=", "mimeType": "image/jpeg"},
				},
				"structuredContent": map[string]any{"message": "Took a screenshot."},
			})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer server.Close()
	runtime := New(Options{Client: server.Client()})

	result, err := runtime.CallTool(context.Background(), mcptransport.ExternalMCPCallRequest{
		Server:             runtimeHTTPTestDefinition(server.URL),
		DownstreamToolName: "take_screenshot",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.Output["message"] != "Took a screenshot." {
		t.Fatalf("structured output = %+v", result.Output)
	}
	content, ok := result.Output["content"].([]mcpContent)
	if !ok || len(content) != 2 {
		t.Fatalf("image content = %#v", result.Output["content"])
	}
	if content[1].Type != "image" || content[1].Data != "aW1hZ2U=" || content[1].MIMEType != "image/jpeg" {
		t.Fatalf("image content = %+v", content[1])
	}
}

func TestRuntimeReturnsExternalMCPToolErrorDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcpJSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-1")
			writeRuntimeTestRPC(w, map[string]any{"protocolVersion": "2025-06-18"})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			writeRuntimeTestRPC(w, map[string]any{
				"isError": true,
				"content": []map[string]any{{
					"type": "text",
					"text": "product_fact_refs contains an unknown evidence reference",
				}},
			})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer server.Close()
	runtime := New(Options{Client: server.Client()})

	_, err := runtime.CallTool(context.Background(), mcptransport.ExternalMCPCallRequest{
		Server:             runtimeHTTPTestDefinition(server.URL),
		DownstreamToolName: "record_plan",
	})
	if err == nil {
		t.Fatal("expected tool error")
	}
	if !strings.Contains(err.Error(), "product_fact_refs contains an unknown evidence reference") {
		t.Fatalf("error = %q", err)
	}
}

func TestRuntimeUsesServerCallTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req mcpJSONRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-1")
			writeRuntimeTestRPC(w, map[string]any{"protocolVersion": "2025-06-18"})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			time.Sleep(40 * time.Millisecond)
			writeRuntimeTestRPC(w, map[string]any{"structuredContent": map[string]any{"status": "ok"}})
		default:
			t.Fatalf("unexpected method %q", req.Method)
		}
	}))
	defer server.Close()
	runtime := New(Options{Client: server.Client(), Timeout: 10 * time.Millisecond})
	definition := runtimeHTTPTestDefinition(server.URL)
	definition.MCP.CallTimeoutSeconds = 1

	result, err := runtime.CallTool(context.Background(), mcptransport.ExternalMCPCallRequest{
		Server:             definition,
		DownstreamToolName: "slow_search",
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.Output["status"] != "ok" {
		t.Fatalf("output = %+v", result.Output)
	}
}

func TestDecodeJSONRPCPayloadSkipsSSEHeartbeatComments(t *testing.T) {
	payload := []byte(": ping - 2026-07-24T12:00:00Z\n\n" +
		"event: message\n" +
		`data: {"jsonrpc":"2.0","id":"1","result":{"structuredContent":{"status":"ok"}}}` + "\n\n")
	var result mcpToolsCallResult

	if err := decodeJSONRPCPayload(payload, &result); err != nil {
		t.Fatalf("decodeJSONRPCPayload: %v", err)
	}
	if result.StructuredContent["status"] != "ok" {
		t.Fatalf("structured content = %+v", result.StructuredContent)
	}
}

func TestRuntimeListsSSEToolsWithDescriptions(t *testing.T) {
	server := newRuntimeSSETestServer(t)
	defer server.Close()
	runtime := New(Options{Client: server.Client()})

	list, err := runtime.ListTools(context.Background(), runtimeSSETestDefinition(server.URL+"/sse"))
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(list.Tools) != 1 || list.Tools[0] != "search_web" {
		t.Fatalf("tools = %+v", list.Tools)
	}
	if len(list.ToolDetails) != 1 || !strings.Contains(list.ToolDetails[0].Description, "SSE") || list.ToolDetails[0].InputSchema["type"] != "object" {
		t.Fatalf("details = %+v", list.ToolDetails)
	}
}

func TestRuntimeSendsConfiguredHeadersToSSEConnectAndPost(t *testing.T) {
	server := newRuntimeSSEHeaderTestServer(t)
	defer server.Close()
	runtime := New(Options{Client: server.Client()})
	definition := runtimeSSETestDefinition(server.URL + "/sse")
	definition.MCP.Headers = map[string]string{"Authorization": "Bearer sk_test"}

	list, err := runtime.ListTools(context.Background(), definition)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(list.Tools) != 1 || list.Tools[0] != "search_web" {
		t.Fatalf("tools = %+v", list.Tools)
	}
}

func TestRuntimeChecksSSEHealth(t *testing.T) {
	server := newRuntimeSSETestServer(t)
	defer server.Close()
	runtime := New(Options{Client: server.Client()})

	health, err := runtime.CheckHealth(context.Background(), runtimeSSETestDefinition(server.URL+"/sse"))
	if err != nil {
		t.Fatalf("CheckHealth: %v", err)
	}
	if health.Status != "available" || !strings.Contains(health.Message, "sse") {
		t.Fatalf("health = %+v", health)
	}
}

func TestRuntimeCallsSSETool(t *testing.T) {
	server := newRuntimeSSETestServer(t)
	defer server.Close()
	runtime := New(Options{Client: server.Client()})

	result, err := runtime.CallTool(context.Background(), mcptransport.ExternalMCPCallRequest{
		Server:             runtimeSSETestDefinition(server.URL + "/sse"),
		DownstreamToolName: "search_web",
		Arguments:          map[string]any{"query": "orrery"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.Output["status"] != "ok" {
		t.Fatalf("output = %+v", result.Output)
	}
}

func TestRuntimeListsStdioToolsWithDescriptions(t *testing.T) {
	t.Setenv("ORRERY_MCP_STDIO_TEST_SERVER", "1")
	runtime := New(Options{})

	list, err := runtime.ListTools(context.Background(), runtimeStdioTestDefinition())
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	if len(list.Tools) != 1 || list.Tools[0] != "bing_search" {
		t.Fatalf("tools = %+v", list.Tools)
	}
	if len(list.ToolDetails) != 1 || !strings.Contains(list.ToolDetails[0].Description, "Bing") || list.ToolDetails[0].InputSchema["type"] != "object" {
		t.Fatalf("details = %+v", list.ToolDetails)
	}
}

func TestRuntimeChecksStdioHealth(t *testing.T) {
	t.Setenv("ORRERY_MCP_STDIO_TEST_SERVER", "1")
	runtime := New(Options{})

	health, err := runtime.CheckHealth(context.Background(), runtimeStdioTestDefinition())
	if err != nil {
		t.Fatalf("CheckHealth: %v", err)
	}
	if health.Status != "available" || !strings.Contains(health.Message, "stdio") {
		t.Fatalf("health = %+v", health)
	}
}

func TestRuntimeCallsStdioTool(t *testing.T) {
	t.Setenv("ORRERY_MCP_STDIO_TEST_SERVER", "1")
	runtime := New(Options{})

	result, err := runtime.CallTool(context.Background(), mcptransport.ExternalMCPCallRequest{
		Server:             runtimeStdioTestDefinition(),
		DownstreamToolName: "bing_search",
		Arguments:          map[string]any{"query": "orrery"},
		RequestMeta:        map[string]any{"proof": "native-hil"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.Output["status"] != "ok" {
		t.Fatalf("output = %+v", result.Output)
	}
	if result.Output["proof"] != "native-hil" {
		t.Fatalf("request metadata output = %+v", result.Output)
	}
}

func TestRuntimeInjectsStdioSecretEnv(t *testing.T) {
	t.Setenv("ORRERY_MCP_STDIO_TEST_SERVER", "1")
	runtime := New(Options{SecretResolver: staticRuntimeSecretResolver{"mcp.bing.token": "secret-token"}})
	definition := runtimeStdioTestDefinition()
	definition.MCP.EnvSecretRefs = []toolres.SecretRef{{Name: "mcp.bing.token", Env: "BING_API_KEY"}}

	result, err := runtime.CallTool(context.Background(), mcptransport.ExternalMCPCallRequest{
		Server:             definition,
		DownstreamToolName: "bing_search",
		Arguments:          map[string]any{"query": "orrery"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.Output["secret"] != "present" {
		t.Fatalf("output = %+v", result.Output)
	}
}

func TestRuntimeReportsStdioSecretResolveFailure(t *testing.T) {
	t.Setenv("ORRERY_MCP_STDIO_TEST_SERVER", "1")
	runtime := New(Options{SecretResolver: staticRuntimeSecretResolver{}})
	definition := runtimeStdioTestDefinition()
	definition.MCP.EnvSecretRefs = []toolres.SecretRef{{Name: "mcp.bing.missing", Env: "BING_API_KEY"}}

	_, err := runtime.CheckHealth(context.Background(), definition)
	if err == nil || !strings.Contains(err.Error(), "mcp.bing.missing") {
		t.Fatalf("CheckHealth error = %v", err)
	}
}

func TestRuntimeRejectsUnsupportedTransport(t *testing.T) {
	runtime := New(Options{})
	definition := runtimeHTTPTestDefinition("https://mcp.example.test")
	definition.MCP.Transport = toolres.MCPTransport("websocket")

	_, err := runtime.ListTools(context.Background(), definition)
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("ListTools error = %v", err)
	}
}

func runtimeSSETestDefinition(url string) toolres.ToolDefinition {
	return toolres.ToolDefinition{
		ID:      agentcore.ToolID("mcp.search-sse"),
		Kind:    toolres.KindMCPServer,
		Name:    "Search SSE MCP",
		Version: "1.0.0",
		Enabled: true,
		MCP: &toolres.MCPServerDefinition{
			ServerName: "search-sse",
			Transport:  toolres.TransportSSE,
			URL:        url,
		},
	}
}

func runtimeHTTPTestDefinition(url string) toolres.ToolDefinition {
	return toolres.ToolDefinition{
		ID:      agentcore.ToolID("mcp.github"),
		Kind:    toolres.KindMCPServer,
		Name:    "GitHub MCP",
		Version: "1.0.0",
		Enabled: true,
		MCP: &toolres.MCPServerDefinition{
			ServerName: "github",
			Transport:  toolres.TransportStreamHTTP,
			URL:        url,
		},
	}
}

func runtimeStdioTestDefinition() toolres.ToolDefinition {
	return toolres.ToolDefinition{
		ID:      agentcore.ToolID("mcp.bing-cn"),
		Kind:    toolres.KindMCPServer,
		Name:    "Bing CN MCP",
		Version: "1.0.0",
		Enabled: true,
		MCP: &toolres.MCPServerDefinition{
			ServerName: "bing-cn-mcp-server",
			Transport:  toolres.TransportStdio,
			Command:    os.Args[0],
			Args:       []string{"-test.run=TestRuntimeStdioServerHelper"},
		},
	}
}

func writeRuntimeTestRPC(w http.ResponseWriter, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": "1", "result": result})
}

func newRuntimeSSETestServer(t *testing.T) *httptest.Server {
	t.Helper()
	messages := make(chan mcpJSONRPCResponse, 16)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatalf("response writer does not flush")
			}
			_, _ = fmt.Fprint(w, "event: endpoint\ndata: /messages?session=1\n\n")
			flusher.Flush()
			for {
				select {
				case msg := <-messages:
					raw, err := json.Marshal(msg)
					if err != nil {
						t.Errorf("encode sse message: %v", err)
						return
					}
					_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", raw)
					flusher.Flush()
				case <-r.Context().Done():
					return
				case <-time.After(5 * time.Second):
					t.Errorf("timed out waiting for sse message")
					return
				}
			}
		case "/messages":
			var req mcpJSONRPCRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode sse post: %v", err)
			}
			switch req.Method {
			case "initialize":
				enqueueRuntimeSSETestRPC(t, messages, req.ID, map[string]any{"protocolVersion": "2025-06-18"})
			case "notifications/initialized":
			case "tools/list":
				enqueueRuntimeSSETestRPC(t, messages, req.ID, map[string]any{"tools": []map[string]any{{
					"name":        "search_web",
					"description": "Search the web through SSE MCP",
					"inputSchema": map[string]any{"type": "object"},
				}}})
			case "tools/call":
				enqueueRuntimeSSETestRPC(t, messages, req.ID, map[string]any{
					"content":           []map[string]any{{"type": "text", "text": "ok"}},
					"structuredContent": map[string]any{"status": "ok"},
				})
			default:
				enqueueRuntimeSSETestError(t, messages, req.ID, -32601, "method not found")
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
}

func newRuntimeSSEHeaderTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	messages := make(chan mcpJSONRPCResponse, 16)
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sk_test" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"jsonrpc":"2.0","id":null,"error":{"code":-32600,"message":"Unauthorized: Invalid or missing API Key"}}`))
			return
		}
		switch r.URL.Path {
		case "/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			flusher, ok := w.(http.Flusher)
			if !ok {
				t.Fatalf("response writer does not flush")
			}
			_, _ = fmt.Fprint(w, "event: endpoint\ndata: /messages?session=1\n\n")
			flusher.Flush()
			for {
				select {
				case msg := <-messages:
					raw, err := json.Marshal(msg)
					if err != nil {
						t.Errorf("encode sse message: %v", err)
						return
					}
					_, _ = fmt.Fprintf(w, "event: message\ndata: %s\n\n", raw)
					flusher.Flush()
				case <-r.Context().Done():
					return
				case <-time.After(5 * time.Second):
					t.Errorf("timed out waiting for sse message")
					return
				}
			}
		case "/messages":
			var req mcpJSONRPCRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				t.Fatalf("decode sse post: %v", err)
			}
			switch req.Method {
			case "initialize":
				enqueueRuntimeSSETestRPC(t, messages, req.ID, map[string]any{"protocolVersion": "2025-06-18"})
			case "notifications/initialized":
			case "tools/list":
				enqueueRuntimeSSETestRPC(t, messages, req.ID, map[string]any{"tools": []map[string]any{{"name": "search_web"}}})
			default:
				enqueueRuntimeSSETestError(t, messages, req.ID, -32601, "method not found")
			}
			w.WriteHeader(http.StatusAccepted)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
}

func enqueueRuntimeSSETestRPC(t *testing.T, messages chan<- mcpJSONRPCResponse, id any, result any) {
	t.Helper()
	raw, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal sse result: %v", err)
	}
	messages <- mcpJSONRPCResponse{JSONRPC: "2.0", ID: id, Result: raw}
}

func enqueueRuntimeSSETestError(t *testing.T, messages chan<- mcpJSONRPCResponse, id any, code int, message string) {
	t.Helper()
	messages <- mcpJSONRPCResponse{JSONRPC: "2.0", ID: id, Error: &mcpRPCError{Code: code, Message: message}}
}

func TestRuntimeStdioServerHelper(t *testing.T) {
	if os.Getenv("ORRERY_MCP_STDIO_TEST_SERVER") != "1" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintf(os.Stderr, "read request: %v\n", err)
			os.Exit(2)
		}
		line = []byte(strings.TrimSpace(string(line)))
		if len(line) == 0 {
			continue
		}
		var req mcpJSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			fmt.Fprintf(os.Stderr, "decode request: %v\n", err)
			os.Exit(2)
		}
		switch req.Method {
		case "initialize":
			writeStdioRuntimeTestRPC(req.ID, map[string]any{"protocolVersion": "2025-06-18"})
		case "notifications/initialized":
		case "tools/list":
			writeStdioRuntimeTestRPC(req.ID, map[string]any{"tools": []map[string]any{{
				"name":        "bing_search",
				"description": "Search Bing China results",
				"inputSchema": map[string]any{"type": "object"},
			}}})
		case "tools/call":
			params, _ := req.Params.(map[string]any)
			metadata, _ := params["_meta"].(map[string]any)
			writeStdioRuntimeTestRPC(req.ID, map[string]any{
				"content":           []map[string]any{{"type": "text", "text": "ok"}},
				"structuredContent": map[string]any{"status": "ok", "secret": stdioSecretState(), "proof": metadata["proof"]},
			})
		default:
			writeStdioRuntimeTestError(req.ID, -32601, "method not found")
		}
	}
}

func writeStdioRuntimeTestRPC(id any, result any) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

func writeStdioRuntimeTestError(id any, code int, message string) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}})
}

func stdioSecretState() string {
	if os.Getenv("BING_API_KEY") == "secret-token" {
		return "present"
	}
	return "missing"
}

type staticRuntimeSecretResolver map[string]string

func (r staticRuntimeSecretResolver) ResolveSecret(ctx context.Context, ref string) (string, error) {
	value, ok := r[ref]
	if !ok {
		return "", fmt.Errorf("missing secret %s", ref)
	}
	return value, nil
}
