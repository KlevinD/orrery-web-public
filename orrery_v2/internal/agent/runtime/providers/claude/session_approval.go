package claude

import (
	"encoding/json"
	"strings"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func (s *NativeSession) PendingRuntimeApprovalRequest(requestID string) (runtimecore.ApprovalRequest, bool) {
	if s == nil {
		return runtimecore.ApprovalRequest{}, false
	}
	s.mu.Lock()
	pending, ok := s.pendingPerms[strings.TrimSpace(requestID)]
	input := cloneClaudeObject(pending.input)
	s.mu.Unlock()
	if !ok {
		return runtimecore.ApprovalRequest{}, false
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return runtimecore.ApprovalRequest{}, false
	}
	kind, toolName := runtimecore.ApprovalKindAndToolName(pending.toolName)
	metadata := runtimecore.ApprovalPayloadMetadata(payload)
	if metadata == nil {
		metadata = map[string]string{}
	}
	metadata["approval_native_tool_name"] = pending.toolName
	return runtimecore.ApprovalRequest{
		RequestID:                  pending.requestID,
		Kind:                       kind,
		ToolName:                   toolName,
		Payload:                    payload,
		Metadata:                   metadata,
		CommandMaterialUnavailable: kind == runtimecore.ApprovalKindCommandExecution && strings.TrimSpace(metadata["command"]) == "",
	}, true
}
