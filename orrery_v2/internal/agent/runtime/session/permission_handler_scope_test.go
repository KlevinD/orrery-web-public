package session

import (
	"context"
	"testing"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestApprovalContextClonesNativeWriteScope(t *testing.T) {
	original := runtimecore.ApprovalRequest{RequestID: "patch", WorkspaceWriteRoots: []string{"/workspace/source"}, FileChangePaths: []string{"/workspace/source/code.go"}}
	backend := &approvalContextBackend{scriptedSessionBackend: newScriptedSessionBackend(), request: original}
	ctx := sessionPermissionContext(context.Background(), backend, "patch")
	original.WorkspaceWriteRoots[0] = "changed"
	original.FileChangePaths[0] = "changed"
	first, ok := providerRuntimeApprovalRequest(ctx)
	if !ok || first.WorkspaceWriteRoots[0] != "/workspace/source" || first.FileChangePaths[0] != "/workspace/source/code.go" {
		t.Fatal("context aliases provider scope")
	}
	first.WorkspaceWriteRoots[0] = "changed"
	first.FileChangePaths[0] = "changed"
	second, _ := providerRuntimeApprovalRequest(ctx)
	if second.WorkspaceWriteRoots[0] != "/workspace/source" || second.FileChangePaths[0] != "/workspace/source/code.go" {
		t.Fatal("reader can mutate retained approval scope")
	}
}
