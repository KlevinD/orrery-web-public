package openclaw

import (
	"encoding/json"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

type Parser struct {
	messages             []runtimecore.RuntimeMessage
	output               strings.Builder
	gatewayAssistantText string
	sessionID            string
	model                string
	status               runtimecore.ResultStatus
	err                  string
	approval             *runtimecore.ApprovalRequest
	usage                agentcore.TokenUsage
}

func (p *Parser) handleGatewayAssistant(text, delta string, replace bool) {
	if replace {
		p.output.Reset()
		p.gatewayAssistantText = ""
	}
	chunk := delta
	if chunk == "" && text != "" {
		switch {
		case strings.HasPrefix(text, p.gatewayAssistantText):
			chunk = strings.TrimPrefix(text, p.gatewayAssistantText)
		case text != p.gatewayAssistantText:
			chunk = text
		}
	}
	if text != "" {
		p.gatewayAssistantText = text
	} else {
		p.gatewayAssistantText += chunk
	}
	if chunk == "" {
		return
	}
	p.output.WriteString(chunk)
	p.messages = append(p.messages, runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeText, Content: chunk, SessionID: p.sessionID})
}

func NewParser() *Parser {
	return &Parser{status: runtimecore.ResultStatusCompleted}
}

func (p *Parser) HandleLine(raw []byte) error {
	line := strings.TrimSpace(string(raw))
	if line == "" {
		return nil
	}
	if evt, ok := parseEvent(line); ok {
		return p.handleEvent(evt)
	}
	if result, ok := parseResult(line); ok {
		p.handleResult(result)
		return nil
	}
	if strings.HasPrefix(line, "{") {
		return fmt.Errorf("unrecognized openclaw JSON event")
	}
	return fmt.Errorf("openclaw stdout must contain JSON events")
}

func (p *Parser) handleEvent(evt event) error {
	if evt.SessionID != "" {
		p.sessionID = evt.SessionID
	}
	switch evt.Type {
	case "step_start":
		p.messages = append(p.messages, runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeStatus, Status: "running", SessionID: p.sessionID})
	case "text":
		if evt.Text != "" {
			p.output.WriteString(evt.Text)
			p.messages = append(p.messages, runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeText, Content: evt.Text, SessionID: p.sessionID})
		}
	case "tool_use":
		var input map[string]any
		if len(evt.Input) > 0 {
			if err := json.Unmarshal(evt.Input, &input); err != nil {
				return fmt.Errorf("decode openclaw tool input: %w", err)
			}
		}
		p.messages = append(p.messages, runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeToolUse, Tool: evt.Tool, CallID: evt.CallID, Input: input, SessionID: p.sessionID})
	case "tool_result":
		p.messages = append(p.messages, runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeToolResult, Tool: evt.Tool, CallID: evt.CallID, Output: evt.Text, SessionID: p.sessionID})
	case "approval_request", "permission_request", "exec_approval", "plugin_permission_request":
		p.setApproval(evt)
	case "error":
		p.status = runtimecore.ResultStatusFailed
		p.err = evt.errorMessage()
		p.messages = append(p.messages, runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeError, Content: p.err, SessionID: p.sessionID})
	case "lifecycle":
		if evt.Phase == "error" || evt.Phase == "failed" || evt.Phase == "cancelled" {
			p.status = runtimecore.ResultStatusFailed
			p.err = evt.errorMessage()
			p.messages = append(p.messages, runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeError, Content: p.err, SessionID: p.sessionID})
		}
	case "step_finish":
		if evt.Usage != nil {
			addUsage(&p.usage, parseUsage(evt.Usage))
		}
	}
	return nil
}

func (p *Parser) handleResult(result finalResult) {
	for _, payload := range result.Payloads {
		if payload.Text != "" {
			p.output.WriteString(payload.Text)
			p.messages = append(p.messages, runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeText, Content: payload.Text, SessionID: p.sessionID})
		}
	}
	if result.Meta.AgentMeta == nil {
		return
	}
	if sid, _ := result.Meta.AgentMeta["sessionId"].(string); sid != "" {
		p.sessionID = sid
	}
	if model, _ := result.Meta.AgentMeta["model"].(string); strings.TrimSpace(model) != "" {
		p.model = strings.TrimSpace(model)
	}
	if rawUsage, ok := result.Meta.AgentMeta["usage"].(map[string]any); ok {
		p.usage = parseUsage(rawUsage)
	}
}

func (p *Parser) Messages() []runtimecore.RuntimeMessage {
	return append([]runtimecore.RuntimeMessage(nil), p.messages...)
}

func (p *Parser) DrainMessages() []runtimecore.RuntimeMessage {
	out := p.Messages()
	p.messages = nil
	return out
}

func (p *Parser) Result() runtimecore.RuntimeResult {
	usage := map[string]agentcore.TokenUsage{}
	if p.usage.InputTokens > 0 || p.usage.OutputTokens > 0 || p.usage.CacheReadTokens > 0 || p.usage.CacheWriteTokens > 0 {
		model := p.model
		if model == "" {
			model = "openclaw"
		}
		usage[model] = p.usage
	}
	var metadata map[string]string
	if p.approval != nil {
		metadata = runtimecore.ApprovalMetadata(*p.approval)
	}
	return runtimecore.RuntimeResult{Status: p.status, Output: p.output.String(), Error: p.err, SessionID: p.sessionID, Usage: usage, Metadata: metadata}
}

func (p *Parser) PendingApproval() (runtimecore.ApprovalRequest, bool) {
	if p.approval == nil {
		return runtimecore.ApprovalRequest{}, false
	}
	return *p.approval, true
}

type event struct {
	Type      string          `json:"type"`
	SessionID string          `json:"sessionId,omitempty"`
	RequestID string          `json:"requestId,omitempty"`
	ID        string          `json:"id,omitempty"`
	Text      string          `json:"text,omitempty"`
	Tool      string          `json:"tool,omitempty"`
	CallID    string          `json:"callId,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Usage     map[string]any  `json:"usage,omitempty"`
	Phase     string          `json:"phase,omitempty"`
	Error     *eventError     `json:"error,omitempty"`
	Message   string          `json:"message,omitempty"`
}

func (p *Parser) setApproval(evt event) {
	req := openClawApprovalRequest(evt, p.sessionID)
	p.approval = &req
	p.status = runtimecore.ResultStatusBlocked
	p.err = firstNonEmpty(evt.approvalMessage(), "openclaw waiting for user approval")
	p.messages = append(p.messages, runtimecore.RuntimeMessage{Type: runtimecore.MessageTypeStatus, Status: "blocked", Content: p.err, SessionID: p.sessionID})
}

type eventError struct {
	Name    string          `json:"name,omitempty"`
	Message string          `json:"message,omitempty"`
	Data    *eventErrorData `json:"data,omitempty"`
}

type eventErrorData struct {
	Message string `json:"message,omitempty"`
}

func (e event) errorMessage() string {
	if e.Error != nil {
		if e.Error.Data != nil && e.Error.Data.Message != "" {
			return e.Error.Data.Message
		}
		if e.Error.Message != "" {
			return e.Error.Message
		}
		if e.Error.Name != "" {
			return e.Error.Name
		}
	}
	if e.Text != "" {
		return e.Text
	}
	if e.Message != "" {
		return e.Message
	}
	return "unknown openclaw error"
}

func (e event) approvalMessage() string {
	if e.Text != "" {
		return e.Text
	}
	if e.Message != "" {
		return e.Message
	}
	return ""
}

type finalResult struct {
	Payloads []payload `json:"payloads"`
	Meta     meta      `json:"meta"`
}

type payload struct {
	Text string `json:"text"`
}

type meta struct {
	DurationMs int64          `json:"durationMs"`
	AgentMeta  map[string]any `json:"agentMeta"`
}

func parseEvent(line string) (event, bool) {
	var evt event
	if len(line) == 0 || line[0] != '{' || json.Unmarshal([]byte(line), &evt) != nil || evt.Type == "" {
		return event{}, false
	}
	return evt, true
}

func parseResult(line string) (finalResult, bool) {
	var result finalResult
	if len(line) == 0 || line[0] != '{' || json.Unmarshal([]byte(line), &result) != nil {
		return finalResult{}, false
	}
	if result.Payloads == nil && result.Meta.DurationMs == 0 && result.Meta.AgentMeta == nil {
		return finalResult{}, false
	}
	return result, true
}

func openClawApprovalRequest(evt event, sessionID string) runtimecore.ApprovalRequest {
	nativeTool := firstNonEmpty(evt.Tool, "tool")
	kind, toolName := runtimecore.ApprovalKindAndToolName(nativeTool)
	requestID := firstNonEmpty(evt.RequestID, evt.CallID, evt.ID, "openclaw:"+nativeTool)
	payload, _ := json.Marshal(evt)
	metadata := map[string]string{
		"approval_native_event":     evt.Type,
		"approval_native_tool_name": nativeTool,
		"approval_tool_use_id":      evt.CallID,
	}
	for key, value := range runtimecore.ApprovalPayloadMetadata(evt.Input) {
		metadata[key] = value
	}
	return runtimecore.ApprovalRequest{
		Source:    "openclaw",
		Provider:  "openclaw",
		RequestID: requestID,
		SessionID: sessionID,
		Kind:      kind,
		ToolName:  toolName,
		Payload:   payload,
		Metadata:  metadata,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
