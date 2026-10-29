package openclaw

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/cliinternal"
)

type gatewayAgentEvent struct {
	RunID  string          `json:"runId,omitempty"`
	Stream string          `json:"stream,omitempty"`
	Data   json.RawMessage `json:"data,omitempty"`
}

type gatewayAgentEventData struct {
	Phase      string          `json:"phase,omitempty"`
	Text       string          `json:"text,omitempty"`
	Delta      string          `json:"delta,omitempty"`
	Replace    bool            `json:"replace,omitempty"`
	Error      string          `json:"error,omitempty"`
	Message    string          `json:"message,omitempty"`
	Reason     string          `json:"reason,omitempty"`
	ApprovalID string          `json:"approvalId,omitempty"`
	ToolCallID string          `json:"toolCallId,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	Raw        json.RawMessage `json:"-"`
}

type gatewayFeedState struct {
	Blocked  bool
	Terminal bool
}

func feedGatewayPayload(ctx context.Context, parser *Parser, payload json.RawMessage, expectedRunID string, messages chan<- runtimecore.RuntimeMessage) (gatewayFeedState, error) {
	if len(payload) == 0 {
		return gatewayFeedState{}, nil
	}
	if agentEvent, ok := decodeGatewayAgentEvent(payload); ok {
		if expectedRunID != "" && agentEvent.RunID != expectedRunID {
			return gatewayFeedState{}, nil
		}
		state, err := feedGatewayAgentEvent(parser, agentEvent)
		if err != nil {
			return gatewayFeedState{}, err
		}
		for _, msg := range parser.DrainMessages() {
			if err := cliinternal.Send(ctx, messages, msg); err != nil {
				return gatewayFeedState{}, err
			}
		}
		return state, nil
	}
	decoded, err := decodeGatewayPayload(payload)
	if err != nil {
		return gatewayFeedState{}, err
	}
	target := payload
	if len(decoded.Result) > 0 {
		target = decoded.Result
	}
	if err := parser.HandleLine(target); err != nil {
		return gatewayFeedState{}, fmt.Errorf("parse openclaw gateway event: %w", err)
	}
	for _, msg := range parser.DrainMessages() {
		if err := cliinternal.Send(ctx, messages, msg); err != nil {
			return gatewayFeedState{}, err
		}
	}
	if req, ok := parser.PendingApproval(); ok {
		return gatewayFeedState{Blocked: req.RequestID != ""}, nil
	}
	return gatewayFeedState{}, nil
}

func decodeGatewayAgentEvent(payload json.RawMessage) (gatewayAgentEvent, bool) {
	var event gatewayAgentEvent
	if json.Unmarshal(payload, &event) != nil || strings.TrimSpace(event.RunID) == "" || strings.TrimSpace(event.Stream) == "" {
		return gatewayAgentEvent{}, false
	}
	return event, true
}

func feedGatewayAgentEvent(parser *Parser, agentEvent gatewayAgentEvent) (gatewayFeedState, error) {
	var data gatewayAgentEventData
	if len(agentEvent.Data) > 0 {
		if err := json.Unmarshal(agentEvent.Data, &data); err != nil {
			return gatewayFeedState{}, fmt.Errorf("decode openclaw gateway agent event: %w", err)
		}
		data.Raw = agentEvent.Data
	}
	switch strings.ToLower(strings.TrimSpace(agentEvent.Stream)) {
	case "assistant":
		parser.handleGatewayAssistant(data.Text, data.Delta, data.Replace)
	case "lifecycle":
		phase := strings.ToLower(strings.TrimSpace(data.Phase))
		switch phase {
		case "start":
			if err := parser.handleEvent(eventFromGateway("step_start", data)); err != nil {
				return gatewayFeedState{}, err
			}
		case "end":
			return gatewayFeedState{Terminal: true}, nil
		case "error", "failed", "cancelled":
			if err := parser.handleEvent(eventFromGateway("lifecycle", data)); err != nil {
				return gatewayFeedState{}, err
			}
			return gatewayFeedState{Terminal: true}, nil
		}
	case "error":
		if err := parser.handleEvent(eventFromGateway("error", data)); err != nil {
			return gatewayFeedState{}, err
		}
		return gatewayFeedState{Terminal: true}, nil
	case "approval":
		if strings.EqualFold(data.Phase, "requested") {
			if err := parser.handleEvent(event{
				Type:      "approval_request",
				RequestID: data.ApprovalID,
				CallID:    data.ToolCallID,
				Tool:      data.Kind,
				Input:     data.Raw,
				Message:   firstNonEmpty(data.Message, data.Text),
			}); err != nil {
				return gatewayFeedState{}, err
			}
			return gatewayFeedState{Blocked: true}, nil
		}
	}
	return gatewayFeedState{}, nil
}

func eventFromGateway(eventType string, data gatewayAgentEventData) event {
	return event{
		Type:    eventType,
		Phase:   data.Phase,
		Text:    data.Text,
		Message: firstNonEmpty(data.Error, data.Message, data.Reason),
	}
}
