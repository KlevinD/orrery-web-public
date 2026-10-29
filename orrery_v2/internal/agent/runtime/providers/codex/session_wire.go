package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

const nativeClientVersion = "0.1.0"

type nativeRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type nativeRPCResponse struct {
	result json.RawMessage
	rpcErr *nativeRPCError
	err    error
}

type nativePendingRPC struct {
	method            string
	response          chan nativeRPCResponse
	promptClientMsgID string
	promptGeneration  uint64
}

type nativeWireEnvelope struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *nativeRPCError `json:"error,omitempty"`
}

func (s *NativeSession) runHandshake(ctx context.Context, resumeID string) error {
	initialize := map[string]any{
		"clientInfo":   map[string]any{"name": "orrery-runtime-bridge", "version": nativeClientVersion},
		"capabilities": map[string]any{"experimentalApi": true, "requestAttestation": false},
	}
	if _, err := s.callRPC(ctx, "initialize", initialize); err != nil {
		return handshakeError(err)
	}
	if err := s.writeNotification("initialized", nil); err != nil {
		return handshakeError(err)
	}

	params := s.threadOpenParams()
	method := "thread/start"
	if strings.TrimSpace(resumeID) != "" {
		method = "thread/resume"
		params["threadId"] = strings.TrimSpace(resumeID)
		s.mu.Lock()
		s.threadID = strings.TrimSpace(resumeID)
		s.mu.Unlock()
	}
	result, err := s.callRPC(ctx, method, params)
	if err != nil {
		if method == "thread/resume" {
			s.mu.Lock()
			s.threadID = ""
			s.mu.Unlock()
		}
		return handshakeError(err)
	}
	if threadID := extractNativeThreadID(result); threadID != "" {
		s.bindThread(threadID)
	}
	if s.boundThread() == "" {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeHandshakeTimeout, "codex handshake did not bind a thread", true)
	}

	models, modelErr := s.callRPC(ctx, "model/list", map[string]any{"includeHidden": false})
	if modelErr == nil {
		s.applyModelDiscovery(models)
	} else {
		s.emitNotice("codex model discovery was rejected")
	}
	modes, modeErr := s.callRPC(ctx, "permissionProfile/list", map[string]any{})
	if modeErr == nil {
		s.applyModeDiscovery(modes)
	} else {
		s.emitNotice("codex permission profile discovery was rejected")
	}
	s.emitCatalog()
	s.reconcileRequestedSettings(ctx)
	return nil
}

func (s *NativeSession) threadOpenParams() map[string]any {
	params := map[string]any{
		"approvalPolicy": firstNativeValue(s.workspace.ApprovalPolicy, "on-request"),
		"sandbox":        firstNativeValue(s.workspace.Sandbox, "workspace-write"),
		"cwd":            s.workspace.Directory,
	}
	if model := strings.TrimSpace(s.config.Model); model != "" {
		params["model"] = model
	}
	if strings.TrimSpace(s.config.Preset) != "" {
		params["baseInstructions"] = s.config.Preset
	}
	config := map[string]any{}
	if servers := s.nativeMCPConfig(); len(servers) != 0 {
		config["mcp_servers"] = servers
	}
	if len(s.workspace.WritableRoots) != 0 && firstNativeValue(s.workspace.Sandbox, "workspace-write") == "workspace-write" {
		config["sandbox_workspace_write"] = map[string]any{"writable_roots": append([]string(nil), s.workspace.WritableRoots...)}
	}
	if len(config) != 0 {
		params["config"] = config
	}
	return params
}

func (s *NativeSession) nativeMCPConfig() map[string]any {
	servers := make(map[string]any, len(s.config.MCPServers))
	for _, declared := range s.config.MCPServers {
		resolved, ok := s.workspace.MCPServers[declared.Name]
		if !ok {
			continue
		}
		switch declared.Transport {
		case agentcore.MCPTransportStdio:
			servers[declared.Name] = map[string]any{
				"command": resolved.Command,
				"args":    append([]string(nil), resolved.Args...),
				"env":     mergeNativeEnvironment(nil, resolved.Environment),
			}
		case agentcore.MCPTransportHTTP, agentcore.MCPTransportSSE:
			entry := map[string]any{"url": resolved.URL}
			if len(resolved.HTTPHeaders) != 0 {
				entry["http_headers"] = mergeNativeEnvironment(nil, resolved.HTTPHeaders)
			}
			if strings.TrimSpace(resolved.BearerTokenEnvVar) != "" {
				entry["bearer_token_env_var"] = resolved.BearerTokenEnvVar
			}
			servers[declared.Name] = entry
		}
	}
	return servers
}

func (s *NativeSession) callRPC(ctx context.Context, method string, params any) (json.RawMessage, error) {
	return s.callRPCWithPromptAcceptance(ctx, method, params, "", 0)
}

func (s *NativeSession) callRPCWithPromptAcceptance(ctx context.Context, method string, params any, clientMsgID string, generation uint64) (json.RawMessage, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	id := s.nextID.Add(1)
	key := fmt.Sprintf("%d", id)
	response := make(chan nativeRPCResponse, 1)
	s.mu.Lock()
	s.pendingRPC[key] = &nativePendingRPC{
		method: method, response: response,
		promptClientMsgID: strings.TrimSpace(clientMsgID), promptGeneration: generation,
	}
	s.mu.Unlock()
	if err := s.writeFrame(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		s.removePendingRPC(key)
		return nil, err
	}
	select {
	case <-ctx.Done():
		s.removePendingRPC(key)
		return nil, ctx.Err()
	case reply, ok := <-response:
		if !ok {
			return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "codex response channel closed", true)
		}
		if reply.err != nil {
			return nil, reply.err
		}
		if reply.rpcErr != nil {
			return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeBackendRejected, "codex rejected "+method, false)
		}
		return append(json.RawMessage(nil), reply.result...), nil
	}
}

func (s *NativeSession) writeNotification(method string, params any) error {
	frame := map[string]any{"jsonrpc": "2.0", "method": method}
	if params != nil {
		frame["params"] = params
	}
	return s.writeFrame(frame)
}

func (s *NativeSession) writeFrame(frame any) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "codex frame could not be encoded", false)
	}
	data = append(data, '\n')
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	binding := s.currentBinding()
	if binding == nil || binding.stdin == nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "codex stdin is unavailable", true)
	}
	if _, err := binding.stdin.Write(data); err != nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "codex stdin write failed", true)
	}
	return nil
}

func (s *NativeSession) writeReverseResult(id json.RawMessage, result any) error {
	if len(id) == 0 || string(id) == "null" {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "codex reverse request id is missing", false)
	}
	var decoded any
	if err := json.Unmarshal(id, &decoded); err != nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "codex reverse request id is invalid", false)
	}
	return s.writeFrame(map[string]any{"jsonrpc": "2.0", "id": decoded, "result": result})
}

func (s *NativeSession) writeReverseError(id json.RawMessage, code int, message string) error {
	if len(id) == 0 || string(id) == "null" {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(id, &decoded); err != nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeProtocolError, "codex reverse request id is invalid", false)
	}
	return s.writeFrame(map[string]any{"jsonrpc": "2.0", "id": decoded, "error": map[string]any{"code": code, "message": message}})
}

func (s *NativeSession) removePendingRPC(key string) {
	s.mu.Lock()
	delete(s.pendingRPC, key)
	s.mu.Unlock()
}

func nativeRPCIDKey(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", false
	}
	var id any
	if err := json.Unmarshal(raw, &id); err != nil {
		return "", false
	}
	switch value := id.(type) {
	case string:
		return value, value != ""
	case float64:
		if value != float64(uint64(value)) {
			return "", false
		}
		return fmt.Sprintf("%.0f", value), true
	default:
		return "", false
	}
}

func extractNativeThreadID(raw json.RawMessage) string {
	var result map[string]any
	if json.Unmarshal(raw, &result) != nil {
		return ""
	}
	if id, _ := result["threadId"].(string); strings.TrimSpace(id) != "" {
		return strings.TrimSpace(id)
	}
	if thread, _ := result["thread"].(map[string]any); thread != nil {
		if id, _ := thread["id"].(string); strings.TrimSpace(id) != "" {
			return strings.TrimSpace(id)
		}
	}
	return ""
}

func handshakeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeHandshakeTimeout, "codex handshake timed out", true)
	}
	var bridgeErr *agentcore.RuntimeBridgeError
	if errors.As(err, &bridgeErr) {
		return err
	}
	return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "codex handshake failed", true)
}

func firstNativeValue(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
