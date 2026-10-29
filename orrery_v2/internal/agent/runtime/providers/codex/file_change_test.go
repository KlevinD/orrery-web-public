package codex

import (
	"encoding/json"
	"path/filepath"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestNativePatchApprovalBindsWorkspaceAndAllItemPaths(t *testing.T) {
	s := newNativeMapperSession(t)
	root := s.workspace.Directory
	s.workspace = NativeWorkspace{Root: root, Directory: filepath.Join(root, "agent"), Sandbox: "workspace-write", EnforceBoundary: true, WritableRoots: []string{root}}
	source, moved := filepath.Join(root, "source.go"), filepath.Join(root, "moved.go")
	item := map[string]any{"id": "patch-1", "type": "fileChange", "changes": []any{map[string]any{"path": source, "kind": map[string]any{"type": "update", "move_path": moved}, "diff": "change"}}}
	s.mapNativeItem(map[string]any{"item": item, "turnId": "turn-fixture"}, false)
	raw := json.RawMessage(`{"itemId":"patch-1","turnId":"turn-fixture","grantRoot":"/","reason":"retry"}`)
	if err := s.handleNativeReverseRPC(json.RawMessage(`100`), "item/fileChange/requestApproval", raw); err != nil {
		t.Fatal(err)
	}
	permission := waitNativeEvent(t, s.Events(), agentcore.EventPermission)
	req, ok := s.PendingRuntimeApprovalRequest(permission.Event.Permission.RequestID)
	if !ok || !req.WorkspaceBoundaryEnforced || !req.WorkspaceWriteAuthorized || req.WorkspaceRoot != root || req.WorkingDirectory != s.workspace.Directory || len(req.FileChangePaths) != 2 || req.FileChangePaths[0] != source || req.FileChangePaths[1] != moved || len(req.WorkspaceWriteRoots) != 1 || req.WorkspaceWriteRoots[0] != root {
		t.Fatalf("approval lost host scope/item paths: %#v", req)
	}
	req.FileChangePaths[0] = "changed"
	req.WorkspaceWriteRoots[0] = "changed"
	again, _ := s.PendingRuntimeApprovalRequest(permission.Event.Permission.RequestID)
	if again.FileChangePaths[0] != source || again.WorkspaceWriteRoots[0] != root {
		t.Fatal("approval aliases mutable native state")
	}
	s.workspace.Sandbox = "read-only"
	again, _ = s.PendingRuntimeApprovalRequest(permission.Event.Permission.RequestID)
	if again.WorkspaceWriteAuthorized {
		t.Fatal("read-only policy gained write authorization")
	}
	// An arbitrary grantRoot or mismatched item never supplies edit material.
	if err := s.handleNativeReverseRPC(json.RawMessage(`101`), "item/fileChange/requestApproval", json.RawMessage(`{"itemId":"unknown","grantRoot":"/"}`)); err != nil {
		t.Fatal(err)
	}
	permission = waitNativeEvent(t, s.Events(), agentcore.EventPermission)
	unknown, _ := s.PendingRuntimeApprovalRequest(permission.Event.Permission.RequestID)
	if len(unknown.FileChangePaths) != 0 {
		t.Fatal("unbound approval inherited paths")
	}
	item["status"] = "declined"
	s.mapNativeItem(map[string]any{"item": item, "turnId": "turn-fixture"}, true)
	if len(s.fileChangePaths) != 0 {
		t.Fatal("completed patch retained approval material")
	}
}

func TestNativeDeclinedPatchIsFailureWithoutChangedFileEvidence(t *testing.T) {
	s := newNativeMapperSession(t)
	for _, status := range []string{"completed", "failed", "declined", "denied", "cancelled"} {
		t.Run(status, func(t *testing.T) {
			item := map[string]any{"status": status, "changes": []any{map[string]any{"path": "result.json", "diff": "not applied"}}}
			event := s.nativeToolResult(item, "fileChange", "patch").ToolResult
			if event.IsError != (status != "completed") {
				t.Fatalf("status %s error=%v", status, event.IsError)
			}
			for _, content := range event.Content {
				if event.IsError && content.Kind == agentcore.ToolResultFilePath {
					t.Fatal("failed edit emitted file-change evidence")
				}
			}
			if event.IsError && len(event.Content) == 0 {
				t.Fatal("failure reason was lost")
			}
		})
	}
}
