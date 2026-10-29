package toolbridge

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

type desktopRPCIDKey struct{}
type DesktopMCP struct {
	URL, Authorization string
	Close              func()
}

// StartDesktopMCP exposes only the run's explicit desktop tool bindings on an
// authenticated loopback MCP endpoint. Credentials never enter model context.
func StartDesktopMCP(lifetime context.Context, dispatcher runtimecore.RuntimeToolDispatcher, request agentcore.RuntimeRequest) (*DesktopMCP, error) {
	var tools []agentcore.RuntimeToolBinding
	for _, binding := range request.ToolBindings {
		switch string(binding.ToolID) {
		case "desktop_list_files", "desktop_read_excel", "desktop_write_excel":
			tools = append(tools, binding)
		}
	}
	if len(tools) == 0 {
		return nil, nil
	}
	if dispatcher == nil {
		return nil, fmt.Errorf("desktop MCP requires runtime dispatcher")
	}
	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return nil, err
	}
	authorization := "Bearer " + hex.EncodeToString(token)
	server := mcp.NewServer(&mcp.Implementation{Name: "orrery-desktop-tools", Version: "1"}, nil)
	names := map[string]bool{}
	for _, binding := range tools {
		name := RuntimeToolName(binding)
		if names[name] {
			return nil, fmt.Errorf("duplicate desktop MCP tool name")
		}
		names[name] = true
		schema := binding.InputSchema
		if len(schema) == 0 {
			return nil, fmt.Errorf("desktop tool schema required")
		}
		server.AddTool(&mcp.Tool{Name: name, Description: binding.Summary, InputSchema: schema}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			var args map[string]any
			if len(req.Params.Arguments) > 131072 || json.Unmarshal(req.Params.Arguments, &args) != nil {
				return nil, fmt.Errorf("invalid desktop arguments")
			}
			// MCP has a request ID separate from the native model's tool-use ID. Preserve
			// it as metadata. Writes use a stable run+arguments identity across reconnects.
			rpcID, _ := ctx.Value(desktopRPCIDKey{}).(string)
			identity := []any{request.RunID, name, args}
			if string(binding.ToolID) != "desktop_write_excel" {
				identity = append(identity, rpcID, hex.EncodeToString(token[:8]))
			}
			raw, _ := json.Marshal(identity)
			sum := sha256.Sum256(raw)
			callID := "mcp_" + hex.EncodeToString(sum[:24])
			callCtx, cancel := context.WithCancel(lifetime)
			stop := context.AfterFunc(ctx, cancel)
			defer func() { stop(); cancel() }()
			if !request.Deadline.IsZero() {
				var end context.CancelFunc
				callCtx, end = context.WithDeadline(callCtx, request.Deadline)
				defer end()
			}
			result, err := dispatcher.CallRuntimeTool(callCtx, runtimecore.RuntimeToolCallRequest{
				TenantID: request.TenantID, RunID: request.RunID, AgentID: request.AgentID, ExecutionEnvelope: agentcore.CloneExecutionEnvelope(request.ExecutionEnvelope), ExecutionPrincipal: request.ExecutionPrincipal,
				Name: name, CallID: callID, Input: args, RequestMeta: map[string]any{"mcp_request_id": rpcID}, ToolBindings: request.ToolBindings, SkillBindings: request.SkillBindings,
				ResponsibilityContext: agentcore.CloneResponsibilityContext(request.ContextPack.ResponsibilityContext), ProviderSpecific: request.RuntimeOptions.ProviderSpecific, WorkspaceRoot: request.RuntimeOptions.WorkspacePolicy.Root, WorkingDirectory: request.RuntimeOptions.WorkingDirectory, WorkspacePolicy: request.RuntimeOptions.WorkspacePolicy,
			})
			if err != nil {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "desktop tool interrupted; reconcile any write before retrying"}}}, nil
			}
			output := result.Output
			if output == nil {
				output = map[string]any{}
			}
			if result.OutcomeUncertain {
				output["outcome_uncertain"] = true
				output["retryable"] = false
			}
			encoded, _ := json.Marshal(output)
			return &mcp.CallToolResult{IsError: output["status"] == "failed", StructuredContent: output, Content: []mcp.Content{&mcp.TextContent{Text: string(encoded)}}}, nil
		})
	}
	transport := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actual := r.Header.Get("Authorization")
		if subtle.ConstantTimeCompare([]byte(actual), []byte(authorization)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.URL.Path != "/mcp" || r.Header.Get("Origin") != "" {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if r.Method == http.MethodPost {
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 196608))
			if err != nil {
				http.Error(w, "request too large", 413)
				return
			}
			var wire struct {
				ID json.RawMessage `json:"id"`
			}
			if json.Unmarshal(body, &wire) != nil {
				http.Error(w, "invalid request", 400)
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), desktopRPCIDKey{}, strings.TrimSpace(string(wire.ID))))
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		transport.ServeHTTP(w, r)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	httpServer := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 8192}
	bridge := &DesktopMCP{URL: "http://" + listener.Addr().String() + "/mcp", Authorization: authorization, Close: func() { _ = httpServer.Close() }}
	go func() { _ = httpServer.Serve(listener) }()
	context.AfterFunc(lifetime, bridge.Close)
	return bridge, nil
}
