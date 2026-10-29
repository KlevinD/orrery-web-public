package session

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

type pendingRuntimeApprovalRequestProvider interface {
	PendingRuntimeApprovalRequest(string) (runtimecore.ApprovalRequest, bool)
}

type runtimeApprovalRequestContextKey struct{}

func sessionPermissionContext(ctx context.Context, backend agentcore.SessionBackend, requestID string) context.Context {
	provider, ok := backend.(pendingRuntimeApprovalRequestProvider)
	if !ok {
		return ctx
	}
	request, ok := provider.PendingRuntimeApprovalRequest(strings.TrimSpace(requestID))
	if !ok || strings.TrimSpace(request.RequestID) != strings.TrimSpace(requestID) {
		return ctx
	}
	request.Payload = append([]byte(nil), request.Payload...)
	request.Metadata = cloneStringMap(request.Metadata)
	request.WorkspaceWriteRoots = append([]string(nil), request.WorkspaceWriteRoots...)
	request.FileChangePaths = append([]string(nil), request.FileChangePaths...)
	return context.WithValue(ctx, runtimeApprovalRequestContextKey{}, request)
}

func providerRuntimeApprovalRequest(ctx context.Context) (runtimecore.ApprovalRequest, bool) {
	if ctx == nil {
		return runtimecore.ApprovalRequest{}, false
	}
	request, ok := ctx.Value(runtimeApprovalRequestContextKey{}).(runtimecore.ApprovalRequest)
	if !ok {
		return runtimecore.ApprovalRequest{}, false
	}
	request.Payload = append([]byte(nil), request.Payload...)
	request.Metadata = cloneStringMap(request.Metadata)
	request.WorkspaceWriteRoots = append([]string(nil), request.WorkspaceWriteRoots...)
	request.FileChangePaths = append([]string(nil), request.FileChangePaths...)
	return request, true
}

// ApprovalPermissionHandler adapts the existing application approval
// coordinator to canonical AnswerPermission commands. It never enters a
// provider package and does not expose native payloads.
type ApprovalPermissionHandler struct {
	Broker runtimecore.ApprovalBroker
}

func (h ApprovalPermissionHandler) RequestSessionPermission(ctx context.Context, request agentcore.SessionPermissionRequest, respond agentcore.SessionPermissionResponder) error {
	if h.Broker == nil || respond == nil || request.Envelope.Event.Permission == nil {
		return nil
	}
	permission := request.Envelope.Event.Permission
	kind, toolName := runtimecore.ApprovalKindAndToolName(permission.ToolName)
	approval, preserved := providerRuntimeApprovalRequest(ctx)
	if !preserved || strings.TrimSpace(approval.RequestID) != strings.TrimSpace(permission.RequestID) {
		payload, _ := json.Marshal(permission)
		approval = runtimecore.ApprovalRequest{
			Source: string(request.RuntimeKind), RequestID: permission.RequestID,
			SessionID: string(request.Envelope.SessionID), TurnID: uintToString(request.Envelope.TurnGeneration),
			Kind: kind, ToolName: toolName, Payload: payload,
		}
	}
	approval.Source = firstNonBlank(strings.TrimSpace(approval.Source), string(request.RuntimeKind))
	approval.RequestID = strings.TrimSpace(permission.RequestID)
	approval.SessionID = firstNonBlank(strings.TrimSpace(approval.SessionID), string(request.Envelope.SessionID))
	approval.TurnID = firstNonBlank(strings.TrimSpace(approval.TurnID), uintToString(request.Envelope.TurnGeneration))
	if approval.Kind == "" {
		approval.Kind = kind
	}
	approval.ToolName = firstNonBlank(strings.TrimSpace(approval.ToolName), toolName)
	metadata := cloneStringMap(approval.Metadata)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata[runtimecore.ApprovalMetadataRunID] = request.RunID.String()
	metadata["approval_agent_id"] = request.AgentID.String()
	metadata["approval_runtime_kind"] = string(request.RuntimeKind)
	approval.Metadata = metadata
	if len(permission.Questions) > 0 {
		if encoded, err := json.Marshal(permission.Questions); err == nil {
			approval.Metadata["approval_questions"] = string(encoded)
		}
	}
	immutableInput := preserved && approval.Metadata["approval_immutable_input"] == "true"
	return h.Broker.RequestApproval(ctx, approval, func(response runtimecore.ApprovalResponse) error {
		// Immutable tool approvals cannot silently discard an edited operation.
		// This marker comes from the preserved server request, never tool text.
		if immutableInput && response.Decision == runtimecore.ApprovalDecisionApproved {
			modified := response.Metadata["modified_input"]
			if response.Content != nil || (modified != nil && modified != "") {
				return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "runtime requires a new approval for modified tool arguments", false)
			}
		}
		answer := agentcore.AnswerPermissionCommand{RequestID: permission.RequestID, Decision: agentcore.PermissionDenied}
		if response.Decision == runtimecore.ApprovalDecisionApproved {
			answer.Decision = agentcore.PermissionApproved
			if response.Remember {
				answer.Decision = agentcore.PermissionAllowAlways
			}
		}
		if selected, ok := response.Metadata["selected"].(string); ok {
			answer.Selected = strings.TrimSpace(selected)
		}
		answer.Answers = canonicalQuestionAnswers(permission.Questions, response)
		return respond(answer)
	})
}

func canonicalQuestionAnswers(questions []agentcore.PermissionQuestion, response runtimecore.ApprovalResponse) []agentcore.QuestionAnswer {
	if len(questions) == 0 || response.Decision != runtimecore.ApprovalDecisionApproved {
		return nil
	}
	var raw any = response.Content
	if value, ok := response.Metadata["modified_input"]; ok {
		raw = value
	}
	if text, ok := raw.(string); ok {
		var decoded any
		if json.Unmarshal([]byte(strings.TrimSpace(text)), &decoded) == nil {
			raw = decoded
		}
	}
	values, _ := raw.(map[string]any)
	answers := make([]agentcore.QuestionAnswer, 0, len(questions))
	for _, question := range questions {
		labels := stringLabels(values[question.Question])
		if len(labels) == 0 {
			continue
		}
		answers = append(answers, agentcore.QuestionAnswer{Question: question.Question, Labels: labels})
	}
	return answers
}

func stringLabels(value any) []string {
	switch typed := value.(type) {
	case string:
		if value := strings.TrimSpace(typed); value != "" {
			return []string{value}
		}
	case []any:
		out := make([]string, 0, len(typed))
		for _, raw := range typed {
			if value, ok := raw.(string); ok && strings.TrimSpace(value) != "" {
				out = append(out, strings.TrimSpace(value))
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(typed))
		for _, value := range typed {
			if strings.TrimSpace(value) != "" {
				out = append(out, strings.TrimSpace(value))
			}
		}
		return out
	}
	return nil
}

func uintToString(value uint64) string {
	if value == 0 {
		return ""
	}
	return strconv.FormatUint(value, 10)
}

var _ agentcore.SessionPermissionHandler = ApprovalPermissionHandler{}
