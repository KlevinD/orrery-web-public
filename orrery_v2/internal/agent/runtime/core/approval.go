package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type ApprovalKind string

const (
	ApprovalKindCommandExecution ApprovalKind = "command_execution"
	ApprovalKindFileChange       ApprovalKind = "file_change"
	ApprovalKindMCPElicitation   ApprovalKind = "mcp_elicitation"
	ApprovalKindTool             ApprovalKind = "tool"
)

type ApprovalDecision string

const (
	ApprovalDecisionApproved ApprovalDecision = "approved"
	ApprovalDecisionDenied   ApprovalDecision = "denied"
)

type ApprovalRequest struct {
	Source string
	// Provider is retained for legacy CLI transport payloads. New callers use
	// Source; ApprovalSource normalizes both forms at the boundary.
	Provider  string
	RequestID string
	SessionID string
	TurnID    string
	Kind      ApprovalKind
	ToolName  string
	Payload   json.RawMessage
	Metadata  map[string]string
	// WorkspaceRoot is server-owned runtime context used to validate approval
	// material paths. It must not be copied into approval metadata.
	WorkspaceRoot string
	// WorkingDirectory and the workspace authorization flags are server-owned
	// execution context. They allow policy to distinguish an isolated Mission
	// workspace edit from an arbitrary host file change.
	WorkingDirectory          string
	WorkspaceBoundaryEnforced bool
	WorkspaceWriteAuthorized  bool
	// WorkspaceWriteRoots and FileChangePaths are supplied by the native adapter,
	// never by approval metadata. Every changed and moved path must be checked.
	WorkspaceWriteRoots []string
	FileChangePaths     []string
	// CommandMaterialUnavailable is set only by trusted runtime adapters whose
	// native approval protocol identifies a command without exposing its text.
	CommandMaterialUnavailable bool
}

const (
	ApprovalMetadataSource    = "approval_source"
	ApprovalMetadataProvider  = "approval_provider"
	ApprovalMetadataRequestID = "approval_request_id"
	ApprovalMetadataSessionID = "approval_session_id"
	ApprovalMetadataTurnID    = "approval_turn_id"
	ApprovalMetadataKind      = "approval_kind"
	ApprovalMetadataToolName  = "approval_tool_name"
	ApprovalMetadataType      = "approval_type"
	ApprovalMetadataRunID     = "approval_run_id"
)

const ApprovalTypePermissionRequest = "permission_request"

func ApprovalTypeForKind(kind ApprovalKind, toolName string) string {
	switch kind {
	case ApprovalKindCommandExecution:
		return "command_execution"
	case ApprovalKindFileChange:
		return "file_change"
	case ApprovalKindMCPElicitation:
		return "user_input"
	case ApprovalKindTool:
		if strings.Contains(strings.ToLower(strings.TrimSpace(toolName)), "browser") {
			return "network_access"
		}
		return "tool_use"
	default:
		return ApprovalTypePermissionRequest
	}
}

func ApprovalKindAndToolName(nativeTool string) (ApprovalKind, string) {
	tool := strings.TrimSpace(nativeTool)
	switch strings.ToLower(tool) {
	case "shell", "bash", "powershell", "exec", "exec_command", "command", "commandexecution", "command_execution", "cmd", "run_command", "execute_command", "shell_command", "run_shell_command", "terminal":
		return ApprovalKindCommandExecution, "exec_command"
	case "edit", "write", "patch", "apply_patch", "filechange", "file_change", "file_edit", "file_write", "replace":
		return ApprovalKindFileChange, "patch_apply"
	case "browser", "web", "fetch", "url", "http", "network", "web_fetch", "web_search":
		return ApprovalKindTool, firstApprovalContinuationValue(tool, "network")
	case "askuserquestion", "ask_user_question", "mcpelicitation", "mcp_elicitation", "elicitation":
		return ApprovalKindMCPElicitation, firstApprovalContinuationValue(tool, "mcp_elicitation")
	default:
		return ApprovalKindTool, firstApprovalContinuationValue(tool, "tool")
	}
}

func ApprovalPayloadMetadata(raw json.RawMessage) map[string]string {
	if len(raw) == 0 {
		return nil
	}
	var payload any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil
	}
	metadata := map[string]string{}
	if command := firstNestedString(payload, "command"); command != "" {
		metadata["command"] = command
		metadata["payload_kind"] = "command_execution"
		metadata["payload_language"] = "shell"
	}
	if diff := firstNestedString(payload, "diff", "patch"); diff != "" {
		metadata["payload_preview"] = diff
		metadata["payload_kind"] = "file_change"
		metadata["payload_language"] = "diff"
	}
	if prompt := firstNestedString(payload, "prompt", "question"); prompt != "" && metadata["payload_preview"] == "" {
		metadata["payload_preview"] = prompt
	}
	if path := firstNestedString(payload, "path", "file_path", "uri"); path != "" {
		metadata["path"] = path
	}
	if url := firstNestedString(payload, "url"); url != "" {
		metadata["url"] = url
	}
	for _, key := range []string{
		"title",
		"final_goal",
		"root_goal",
		"goal",
		"task_goal",
		"business_direction",
		"plan_summary",
		"query",
		"content",
		"summary",
		"description",
		"message",
		"reason",
		"role",
		"capability",
	} {
		if value := firstNestedString(payload, key); value != "" {
			metadata[key] = value
		}
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

func ApprovalMetadata(req ApprovalRequest) map[string]string {
	metadata := cloneApprovalMetadata(req.Metadata)
	source := ApprovalSource(req)
	putApprovalMetadata(metadata, ApprovalMetadataSource, source)
	putApprovalMetadata(metadata, ApprovalMetadataProvider, source)
	putApprovalMetadata(metadata, ApprovalMetadataRequestID, req.RequestID)
	putApprovalMetadata(metadata, ApprovalMetadataSessionID, req.SessionID)
	putApprovalMetadata(metadata, ApprovalMetadataTurnID, req.TurnID)
	putApprovalMetadata(metadata, ApprovalMetadataKind, string(req.Kind))
	putApprovalMetadata(metadata, ApprovalMetadataToolName, req.ToolName)
	putDefaultApprovalMetadata(metadata, ApprovalMetadataType, ApprovalTypeForKind(req.Kind, req.ToolName))
	return metadata
}

func ApprovalSource(req ApprovalRequest) string {
	if source := strings.TrimSpace(req.Source); source != "" {
		return source
	}
	return strings.TrimSpace(req.Provider)
}

type ApprovalResponse struct {
	Decision ApprovalDecision
	Reason   string
	Remember bool
	Content  any
	Metadata map[string]any
}

type ApprovalContinuation struct {
	Decision      ApprovalDecision
	ActionID      string
	Effect        string
	Message       string
	ModifiedInput string
	ChoiceID      string
	Remember      bool
	Metadata      map[string]string
}

func ApprovalContinuationPrompt(metadata map[string]string, continuation ApprovalContinuation) string {
	kind := strings.TrimSpace(metadata[ApprovalMetadataKind])
	if kind == "" {
		kind = strings.TrimSpace(metadata[ApprovalMetadataType])
	}
	message := strings.TrimSpace(continuation.Message)
	modified := strings.TrimSpace(continuation.ModifiedInput)
	choiceID := strings.TrimSpace(continuation.ChoiceID)
	if continuation.Decision == ApprovalDecisionDenied {
		if message == "" {
			message = "The user denied the pending approval."
		}
		return "The user denied the pending approval. Stop the blocked operation and respond with the denial reason: " + message
	}
	parts := []string{"The user approved the pending " + firstApprovalContinuationValue(kind, "approval") + "."}
	if effect := strings.TrimSpace(continuation.Effect); effect != "" {
		parts = append(parts, "Approval effect: "+effect+".")
	}
	if continuation.Remember {
		parts = append(parts, "Remember this approval for the current runtime session when the provider supports it.")
	}
	if choiceID != "" {
		parts = append(parts, "Selected option: "+choiceID+".")
	}
	if modified != "" {
		parts = append(parts, "Use this modified input instead of the original request:\n"+modified)
	}
	if message != "" {
		parts = append(parts, "User note:\n"+message)
	}
	parts = append(parts, "Resume from the prior provider session if available and continue the task.")
	return strings.Join(parts, "\n\n")
}

func ApprovalContinuationProviderSpecific(metadata map[string]string, continuation ApprovalContinuation) map[string]string {
	out := map[string]string{}
	for _, key := range []string{
		ApprovalMetadataSource,
		ApprovalMetadataProvider,
		ApprovalMetadataRequestID,
		ApprovalMetadataSessionID,
		ApprovalMetadataTurnID,
		ApprovalMetadataKind,
		ApprovalMetadataToolName,
		ApprovalMetadataType,
		"approval_tool_use_id",
		"approval_native_tool_name",
		"approval_native_event",
	} {
		if value := strings.TrimSpace(metadata[key]); value != "" {
			out[key] = value
		}
	}
	out["approval_continuation"] = "true"
	if continuation.Decision != "" {
		out["approval_decision"] = string(continuation.Decision)
	}
	if value := strings.TrimSpace(continuation.ActionID); value != "" {
		out["approval_action_id"] = value
	}
	if value := strings.TrimSpace(continuation.Effect); value != "" {
		out["approval_effect"] = value
	}
	if value := strings.TrimSpace(continuation.Message); value != "" {
		out["approval_message"] = value
	}
	if value := strings.TrimSpace(continuation.ModifiedInput); value != "" {
		out["approval_modified_input"] = value
	}
	if value := strings.TrimSpace(continuation.ChoiceID); value != "" {
		out["approval_choice_id"] = value
	}
	if continuation.Remember {
		out["approval_remember"] = "true"
	}
	for key, value := range continuation.Metadata {
		if strings.HasPrefix(key, "decision_") && strings.TrimSpace(value) != "" {
			out["approval_"+key] = strings.TrimSpace(value)
		}
	}
	return out
}

type ApprovalResponder func(ApprovalResponse) error

type ApprovalBroker interface {
	RequestApproval(ctx context.Context, req ApprovalRequest, respond ApprovalResponder) error
}

type ApprovalBrokerFunc func(ctx context.Context, req ApprovalRequest, respond ApprovalResponder) error

func (f ApprovalBrokerFunc) RequestApproval(ctx context.Context, req ApprovalRequest, respond ApprovalResponder) error {
	return f(ctx, req, respond)
}

type DeferredApprovalBroker struct{}

func (DeferredApprovalBroker) RequestApproval(ctx context.Context, _ ApprovalRequest, _ ApprovalResponder) error {
	return ctx.Err()
}

func cloneApprovalMetadata(src map[string]string) map[string]string {
	out := make(map[string]string, len(src)+7)
	for key, value := range src {
		if strings.TrimSpace(key) != "" && strings.TrimSpace(value) != "" {
			out[key] = value
		}
	}
	return out
}

func putApprovalMetadata(metadata map[string]string, key, value string) {
	if strings.TrimSpace(value) != "" {
		metadata[key] = strings.TrimSpace(value)
	}
}

func putDefaultApprovalMetadata(metadata map[string]string, key, value string) {
	if strings.TrimSpace(metadata[key]) == "" {
		putApprovalMetadata(metadata, key, value)
	}
}

func firstApprovalContinuationValue(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNestedString(value any, keys ...string) string {
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range keys {
			if raw, ok := typed[key]; ok {
				if text := stringifyApprovalPayloadValue(raw); text != "" {
					return text
				}
			}
		}
		for _, raw := range typed {
			if text := firstNestedString(raw, keys...); text != "" {
				return text
			}
		}
	case []any:
		for _, raw := range typed {
			if text := firstNestedString(raw, keys...); text != "" {
				return text
			}
		}
	}
	return ""
}

func stringifyApprovalPayloadValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}
