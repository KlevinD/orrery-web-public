package codex

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
	pending, ok := s.pendingPermissions[strings.TrimSpace(requestID)]
	s.mu.Unlock()
	if !ok {
		return runtimecore.ApprovalRequest{}, false
	}
	payload := append(json.RawMessage(nil), pending.payload...)
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
		WorkspaceRoot:              s.workspace.Root,
		WorkingDirectory:           s.workspace.Directory,
		WorkspaceBoundaryEnforced:  s.workspace.EnforceBoundary,
		WorkspaceWriteAuthorized:   s.workspace.EnforceBoundary && s.workspace.Sandbox == "workspace-write",
		WorkspaceWriteRoots:        append([]string(nil), s.workspace.WritableRoots...),
		FileChangePaths:            append([]string(nil), pending.filePaths...),
		CommandMaterialUnavailable: kind == runtimecore.ApprovalKindCommandExecution && strings.TrimSpace(metadata["command"]) == "",
	}, true
}
