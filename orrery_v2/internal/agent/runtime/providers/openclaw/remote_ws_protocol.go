package openclaw

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"
	"unicode"

	runtimecore "orrery_v2/internal/agent/runtime/core"

	"github.com/gorilla/websocket"
)

type gatewayRequest struct {
	Type   string         `json:"type"`
	ID     string         `json:"id"`
	Method string         `json:"method"`
	Params map[string]any `json:"params,omitempty"`
}

type gatewayFrameHeader struct {
	Type string `json:"type"`
}

type gatewayEventFrame struct {
	Type    string          `json:"type"`
	Event   string          `json:"event"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type gatewayResponseFrame struct {
	Type    string          `json:"type"`
	ID      string          `json:"id"`
	OK      bool            `json:"ok"`
	Payload json.RawMessage `json:"payload,omitempty"`
	Error   *gatewayError   `json:"error,omitempty"`
}

type gatewayError struct {
	Code    string          `json:"code,omitempty"`
	Message string          `json:"message,omitempty"`
	Details json.RawMessage `json:"details,omitempty"`
}

type gatewayResponsePayload struct {
	Status     string          `json:"status,omitempty"`
	RunID      string          `json:"runId,omitempty"`
	SessionKey string          `json:"sessionKey,omitempty"`
	Summary    string          `json:"summary,omitempty"`
	Result     json.RawMessage `json:"result,omitempty"`
	Raw        json.RawMessage `json:"-"`
}

func waitForConnectChallenge(conn *websocket.Conn) (string, error) {
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return "", fmt.Errorf("openclaw remote_ws challenge: %w", err)
		}
		var event gatewayEventFrame
		if err := json.Unmarshal(raw, &event); err != nil {
			return "", fmt.Errorf("decode openclaw remote_ws challenge frame: %w", err)
		}
		if event.Type != "event" || event.Event != "connect.challenge" {
			continue
		}
		var payload struct {
			Nonce string `json:"nonce"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return "", fmt.Errorf("decode openclaw remote_ws challenge payload: %w", err)
		}
		if strings.TrimSpace(payload.Nonce) == "" {
			return "", fmt.Errorf("openclaw remote_ws connect challenge missing nonce")
		}
		return strings.TrimSpace(payload.Nonce), nil
	}
}

func waitForGatewayResponse(conn *websocket.Conn, requestID string, expectFinal bool, onAccepted func(gatewayResponsePayload), onEvent func(gatewayEventFrame) bool) (gatewayResponsePayload, error) {
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return gatewayResponsePayload{}, err
		}
		var header gatewayFrameHeader
		if err := json.Unmarshal(raw, &header); err != nil {
			return gatewayResponsePayload{}, fmt.Errorf("decode gateway frame: %w", err)
		}
		switch header.Type {
		case "event":
			if onEvent != nil {
				var event gatewayEventFrame
				if err := json.Unmarshal(raw, &event); err != nil {
					return gatewayResponsePayload{}, fmt.Errorf("decode gateway event frame: %w", err)
				}
				if len(event.Payload) > 0 && onEvent(event) {
					return gatewayResponsePayload{Status: "blocked", Raw: event.Payload}, nil
				}
			}
		case "res":
			payload, done, err := handleGatewayResponseFrame(raw, requestID, expectFinal, onAccepted)
			if err != nil || done {
				return payload, err
			}
		}
	}
}

func handleGatewayResponseFrame(raw []byte, requestID string, expectFinal bool, onAccepted func(gatewayResponsePayload)) (gatewayResponsePayload, bool, error) {
	var frame gatewayResponseFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		return gatewayResponsePayload{}, true, err
	}
	if frame.ID != requestID {
		return gatewayResponsePayload{}, false, nil
	}
	if !frame.OK {
		if frame.Error != nil && frame.Error.Message != "" {
			return gatewayResponsePayload{}, true, fmt.Errorf("%s", frame.Error.Message)
		}
		return gatewayResponsePayload{}, true, fmt.Errorf("gateway response was not ok")
	}
	payload, err := decodeGatewayPayload(frame.Payload)
	if err != nil {
		return gatewayResponsePayload{}, true, err
	}
	if expectFinal && payload.Status == "accepted" {
		if onAccepted != nil {
			onAccepted(payload)
		}
		return gatewayResponsePayload{}, false, nil
	}
	return payload, true, nil
}

func decodeGatewayPayload(raw json.RawMessage) (gatewayResponsePayload, error) {
	payload := gatewayResponsePayload{Raw: raw}
	if len(raw) == 0 {
		return payload, nil
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return gatewayResponsePayload{}, fmt.Errorf("decode gateway response payload: %w", err)
	}
	return payload, nil
}

func buildConnectParams(cfg RuntimeConfig, nonce string) map[string]any {
	params := map[string]any{
		"minProtocol": openClawGatewayProtocol,
		"maxProtocol": openClawGatewayProtocol,
		"client": map[string]any{
			"id":          "gateway-client",
			"displayName": "Orrery OpenClaw",
			"version":     openClawGatewayVersion,
			"platform":    runtime.GOOS,
			"mode":        "backend",
			"instanceId":  randomGatewayID(),
		},
		"caps":   []string{},
		"role":   "operator",
		"scopes": []string{"operator.admin"},
	}
	if token := cfg.tokenValue(); token != "" {
		params["auth"] = map[string]any{"token": token}
	}
	_ = nonce
	return params
}

func buildAgentParams(prompt, sessionID, agentID string, opts runtimecore.ExecOptions, cfg RuntimeConfig) map[string]any {
	timeoutSeconds := 600
	if opts.Timeout > 0 {
		timeoutSeconds = max(1, int(opts.Timeout.Seconds()))
	}
	params := map[string]any{
		"message":        prompt,
		"agentId":        agentID,
		"sessionId":      sessionID,
		"sessionKey":     openClawExplicitSessionKey(agentID, sessionID),
		"timeout":        timeoutSeconds,
		"idempotencyKey": randomGatewayID(),
	}
	if model := strings.TrimSpace(cfg.ModelID); model != "" {
		params["model"] = model
	}
	return params
}

func normalizeGatewayWebSocketURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("openclaw remote_ws gateway_url is required")
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("invalid openclaw remote_ws gateway_url: %w", err)
	}
	switch parsed.Scheme {
	case "http":
		parsed.Scheme = "ws"
	case "https":
		parsed.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("openclaw remote_ws gateway_url must use ws://, wss://, http://, or https://")
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("openclaw remote_ws gateway_url host is required")
	}
	return parsed.String(), nil
}

func remoteWSHeaders(cfg RuntimeConfig) http.Header {
	headers := http.Header{}
	if token := cfg.tokenValue(); token != "" {
		headers.Set("Authorization", "Bearer "+token)
	}
	return headers
}

func failedRemoteWSResult(sessionID, message string) runtimecore.RuntimeResult {
	return runtimecore.RuntimeResult{
		Status:    runtimecore.ResultStatusFailed,
		Error:     message,
		SessionID: sessionID,
		Metadata:  map[string]string{"transport": "remote_ws"},
	}
}

func requestRemoteApproval(ctx context.Context, broker runtimecore.ApprovalBroker, req runtimecore.ApprovalRequest) {
	if broker == nil {
		return
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = broker.RequestApproval(ctx, req, func(runtimecore.ApprovalResponse) error {
			return nil
		})
	}()
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
	}
}

func openClawExplicitSessionKey(agentID, sessionID string) string {
	return "agent:" + normalizeOpenClawAgentID(agentID) + ":explicit:" + strings.TrimSpace(sessionID)
}

func normalizeOpenClawAgentID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return "main"
	}
	var out strings.Builder
	for _, r := range strings.ToLower(trimmed) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			out.WriteRune(r)
			continue
		}
		if unicode.IsSpace(r) || r == '.' || r == '/' || r == ':' {
			out.WriteByte('-')
			continue
		}
		out.WriteByte('-')
	}
	normalized := strings.Trim(out.String(), "-")
	if len(normalized) > 64 {
		normalized = strings.Trim(normalized[:64], "-")
	}
	if normalized == "" {
		return "main"
	}
	return normalized
}

func randomGatewayID() string {
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("orrery-%d", time.Now().UnixNano())
}
