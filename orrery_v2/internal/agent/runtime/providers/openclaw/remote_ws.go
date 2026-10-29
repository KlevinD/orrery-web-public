package openclaw

import (
	"context"
	"fmt"
	"strings"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/cliinternal"
	"orrery_v2/internal/agent/runtime/providers/workspaceprompt"
)

const (
	openClawGatewayProtocol = 4
	openClawGatewayVersion  = "orrery"
)

func executeRemoteWS(ctx context.Context, prompt string, opts runtimecore.ExecOptions, cfg RuntimeConfig, broker runtimecore.ApprovalBroker) (*runtimecore.RuntimeSession, error) {
	gatewayURL, err := normalizeGatewayWebSocketURL(cfg.remoteURL())
	if err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(cfg.AgentID)
	if agentID == "" {
		return nil, fmt.Errorf("openclaw remote_ws agent_id is required")
	}
	sessionID := opts.ResumeSessionID
	if sessionID == "" {
		sessionID = fmt.Sprintf("orrery-%d", time.Now().UnixNano())
	}
	if systemPrompt := workspaceprompt.SystemPrompt(opts.SystemPrompt, opts); systemPrompt != "" {
		prompt = systemPrompt + "\n\n" + prompt
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Minute
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	messages := make(chan runtimecore.RuntimeMessage, 256)
	results := make(chan runtimecore.RuntimeResult, 1)
	go func() {
		defer cancel()
		defer close(messages)
		defer close(results)
		started := time.Now()
		result := runRemoteWS(runCtx, gatewayURL, prompt, sessionID, agentID, opts, cfg, broker, messages)
		result.DurationMs = time.Since(started).Milliseconds()
		if result.Metadata == nil {
			result.Metadata = map[string]string{}
		}
		result.Metadata["transport"] = "remote_ws"
		results <- result
	}()
	return &runtimecore.RuntimeSession{Messages: messages, Result: results}, nil
}

func runRemoteWS(ctx context.Context, gatewayURL, prompt, sessionID, agentID string, opts runtimecore.ExecOptions, cfg RuntimeConfig, broker runtimecore.ApprovalBroker, messages chan<- runtimecore.RuntimeMessage) runtimecore.RuntimeResult {
	parser := NewParser()
	operationTimeout := opts.Timeout
	if operationTimeout <= 0 {
		operationTimeout = 20 * time.Minute
	}
	dialer, err := remoteWSDialer(operationTimeout, cfg)
	if err != nil {
		return failedRemoteWSResult(sessionID, err.Error())
	}
	conn, _, err := dialer.DialContext(ctx, gatewayURL, remoteWSHeaders(cfg))
	if err != nil {
		return failedRemoteWSResult(sessionID, fmt.Sprintf("openclaw remote_ws connect: %v", err))
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		_ = conn.Close()
	}()

	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(deadline)
		_ = conn.SetWriteDeadline(deadline)
	}
	challenge, err := waitForConnectChallenge(conn)
	if err != nil {
		return failedRemoteWSResult(sessionID, err.Error())
	}
	connectID := randomGatewayID()
	if err := conn.WriteJSON(gatewayRequest{
		Type:   "req",
		ID:     connectID,
		Method: "connect",
		Params: buildConnectParams(cfg, challenge),
	}); err != nil {
		return failedRemoteWSResult(sessionID, fmt.Sprintf("openclaw remote_ws connect request: %v", err))
	}
	if _, err := waitForGatewayResponse(conn, connectID, false, nil, nil); err != nil {
		return failedRemoteWSResult(sessionID, fmt.Sprintf("openclaw remote_ws connect failed: %v", err))
	}

	agentID = normalizeOpenClawAgentID(agentID)
	agentRequestID := randomGatewayID()
	agentParams := buildAgentParams(prompt, sessionID, agentID, opts, cfg)
	if err := conn.WriteJSON(gatewayRequest{
		Type:   "req",
		ID:     agentRequestID,
		Method: "agent",
		Params: agentParams,
	}); err != nil {
		return failedRemoteWSResult(sessionID, fmt.Sprintf("openclaw remote_ws agent request: %v", err))
	}

	var acceptedRunID string
	var acceptedSessionKey string
	var protocolErr error
	var stoppedByEvent bool
	var terminalAgentEvent bool
	payload, err := waitForGatewayResponse(conn, agentRequestID, true, func(payload gatewayResponsePayload) {
		acceptedRunID = firstNonEmpty(payload.RunID, acceptedRunID)
		acceptedSessionKey = firstNonEmpty(payload.SessionKey, acceptedSessionKey)
	}, func(gatewayEvent gatewayEventFrame) bool {
		if gatewayEvent.Event != "agent" {
			return false
		}
		state, err := feedGatewayPayload(ctx, parser, gatewayEvent.Payload, acceptedRunID, messages)
		if err != nil {
			protocolErr = err
			return true
		}
		stoppedByEvent = state.Blocked || state.Terminal
		terminalAgentEvent = state.Terminal
		if req, ok := parser.PendingApproval(); ok {
			requestRemoteApproval(ctx, broker, req)
			return true
		}
		return stoppedByEvent
	})
	if protocolErr != nil {
		return failedRemoteWSResult(sessionID, fmt.Sprintf("openclaw remote_ws protocol error: %v", protocolErr))
	}
	if err != nil {
		return failedRemoteWSResult(sessionID, fmt.Sprintf("openclaw remote_ws agent failed: %v", err))
	}
	if terminalAgentEvent {
		payload.Status = "ok"
		payload.Raw = nil
	}
	if !stoppedByEvent {
		if _, err := feedGatewayPayload(ctx, parser, payload.Raw, acceptedRunID, messages); err != nil {
			return failedRemoteWSResult(sessionID, fmt.Sprintf("openclaw remote_ws protocol error: %v", err))
		}
	}
	result := parser.Result()
	if result.SessionID == "" {
		result.SessionID = sessionID
	}
	if payload.Status != "" && payload.Status != "ok" && payload.Status != "accepted" && result.Status == runtimecore.ResultStatusCompleted {
		result.Status = runtimecore.ResultStatusFailed
		result.Error = firstNonEmpty(payload.Summary, "openclaw remote_ws returned status "+payload.Status)
	}
	if req, ok := parser.PendingApproval(); ok {
		requestRemoteApproval(ctx, broker, req)
		result.Status = runtimecore.ResultStatusBlocked
		if result.Error == "" {
			result.Error = "openclaw waiting for user approval"
		}
	}
	if result.Status == runtimecore.ResultStatusCompleted && strings.TrimSpace(result.Output) == "" {
		result.Status = runtimecore.ResultStatusFailed
		result.Error = "openclaw remote_ws completed without assistant output"
	}
	if result.Metadata == nil {
		result.Metadata = map[string]string{}
	}
	if acceptedRunID != "" {
		result.Metadata["run_id"] = acceptedRunID
	}
	if acceptedSessionKey != "" {
		result.Metadata["session_key"] = acceptedSessionKey
	}
	for _, msg := range parser.DrainMessages() {
		if cliinternal.Send(ctx, messages, msg) != nil {
			break
		}
	}
	return result
}
