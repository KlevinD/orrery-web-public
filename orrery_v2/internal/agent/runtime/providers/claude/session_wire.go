package claude

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *NativeSession) processArgs(resume bool) ([]string, error) {
	args := []string{
		"--print",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
		"--include-partial-messages",
		"--replay-user-messages",
		"--permission-prompt-tool", "stdio",
	}
	if resume {
		args = append(args, "--resume", s.claudeID)
	} else {
		args = append(args, "--session-id", s.claudeID)
	}
	if preset := strings.TrimSpace(s.config.Preset); preset != "" {
		args = append(args, "--system-prompt", preset)
	}
	mcpConfig, err := nativeClaudeMCPJSON(s.config, s.workspace)
	if err != nil {
		return nil, err
	}
	if mcpConfig != "" {
		args = append(args, "--mcp-config", mcpConfig, "--strict-mcp-config")
	}
	mode := claudeNormalizedMode(s.config.Mode)
	if mode == "" {
		mode = "default"
	}
	if !validClaudeMode(mode) {
		return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "claude permission mode is invalid", false)
	}
	args = append(args, "--permission-mode", mode, "--allow-dangerously-skip-permissions")
	if model := strings.TrimSpace(s.config.Model); model != "" {
		args = append(args, "--model", model)
	}
	args = append(args, nativeClaudeArgs(s.native.ExtraArgs)...)
	return args, nil
}

func nativeClaudeArgs(extra []string) []string {
	blocked := map[string]bool{
		"-p": true, "--print": true, "--input-format": true, "--output-format": true,
		"--permission-prompt-tool": true, "--session-id": true, "--resume": true,
		"--system-prompt": true, "--mcp-config": true, "--strict-mcp-config": true,
		"--permission-mode": true, "--allow-dangerously-skip-permissions": true,
		"--model": true,
	}
	out := make([]string, 0, len(extra))
	for index := 0; index < len(extra); index++ {
		argument := strings.TrimSpace(extra[index])
		name := argument
		if before, _, ok := strings.Cut(argument, "="); ok {
			name = before
		}
		if blocked[name] {
			if argument == name && index+1 < len(extra) && !strings.HasPrefix(strings.TrimSpace(extra[index+1]), "-") {
				index++
			}
			continue
		}
		if argument != "" {
			out = append(out, argument)
		}
	}
	return out
}

func (s *NativeSession) runHandshake(ctx context.Context) error {
	_, err := s.callControl(ctx, "initialize", nil)
	if err != nil {
		return claudeHandshakeError(err)
	}
	s.reconcileClaudeRequestedSettings()
	return nil
}

func (s *NativeSession) callControl(ctx context.Context, subtype string, request map[string]any) (map[string]any, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	id := s.nextControlID("ctl")
	if request == nil {
		request = make(map[string]any)
	}
	request["subtype"] = subtype
	response := make(chan claudeControlResponse, 1)
	s.mu.Lock()
	s.pendingControl[id] = &claudePendingControl{response: response}
	s.mu.Unlock()
	if err := s.writeFrame(map[string]any{"type": "control_request", "request_id": id, "request": request}); err != nil {
		s.removePendingControl(id)
		return nil, err
	}
	select {
	case <-ctx.Done():
		s.removePendingControl(id)
		return nil, ctx.Err()
	case reply, ok := <-response:
		if !ok {
			return nil, agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "claude control response channel closed", true)
		}
		if reply.err != nil {
			return nil, reply.err
		}
		return reply.response, nil
	}
}

func (s *NativeSession) writeFrame(frame any) error {
	encoded, err := json.Marshal(frame)
	if err != nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "claude frame could not be encoded", false)
	}
	encoded = append(encoded, '\n')
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	binding := s.currentBinding()
	if binding == nil || binding.stdin == nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "claude stdin is unavailable", true)
	}
	if _, err := binding.stdin.Write(encoded); err != nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "claude stdin write failed", true)
	}
	return nil
}

func (s *NativeSession) removePendingControl(id string) {
	s.mu.Lock()
	delete(s.pendingControl, id)
	s.mu.Unlock()
}

func (s *NativeSession) nextControlID(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, s.nextID.Add(1))
}

func claudePromptFrame(content []agentcore.ContentBlock, clientID string) map[string]any {
	blocks := make([]map[string]any, 0, len(content))
	for _, block := range content {
		switch block.Kind {
		case agentcore.ContentBlockText:
			blocks = append(blocks, map[string]any{"type": "text", "text": block.Text})
		case agentcore.ContentBlockImage:
			blocks = append(blocks, map[string]any{"type": "image", "source": map[string]any{"type": "base64", "media_type": block.MediaType, "data": base64.StdEncoding.EncodeToString(block.Data)}})
		case agentcore.ContentBlockResourceLink:
			blocks = append(blocks, map[string]any{"type": "text", "text": "[Attached file: " + strings.TrimPrefix(block.URI, "file://") + "]"})
		}
	}
	frame := map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": blocks}}
	if strings.TrimSpace(clientID) != "" {
		frame["uuid"] = strings.TrimSpace(clientID)
	}
	return frame
}

func claudeHandshakeError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeHandshakeTimeout, "claude handshake timed out", true)
	}
	var bridgeErr *agentcore.RuntimeBridgeError
	if errors.As(err, &bridgeErr) {
		return err
	}
	return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeTransportClosed, "claude handshake failed", true)
}

func claudeNormalizedMode(mode string) string {
	mode = strings.TrimSpace(mode)
	if mode == "normal" {
		return "default"
	}
	return mode
}

func validClaudeMode(mode string) bool {
	switch claudeNormalizedMode(mode) {
	case "default", "acceptEdits", "bypassPermissions", "plan", "dontAsk", "auto":
		return true
	default:
		return false
	}
}
