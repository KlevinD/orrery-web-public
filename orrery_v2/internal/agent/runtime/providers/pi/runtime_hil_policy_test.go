package pi

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestPiApprovalModeServerOnRequestOverridesProviderNever(t *testing.T) {
	opts := runtimecore.ExecOptions{
		ProviderConfig: json.RawMessage(`{"approval_mode":"never"}`),
		ProviderSpecific: map[string]string{
			agentcore.RuntimeConversationHILModeProviderKey: "manual",
		},
		WorkspacePolicy: agentcore.RuntimeWorkspacePolicy{
			ApprovalPolicy: string(agentcore.RuntimeApprovalOnRequest),
		},
	}
	if got := piApprovalMode(opts); got != "on-request" {
		t.Fatalf("pi approval mode = %q, want on-request", got)
	}
}

func TestPiApprovalModeAutoConversationPreservesProviderNever(t *testing.T) {
	opts := runtimecore.ExecOptions{
		ProviderConfig: json.RawMessage(`{"approval_mode":"never"}`),
		WorkspacePolicy: agentcore.RuntimeWorkspacePolicy{
			ApprovalPolicy: string(agentcore.RuntimeApprovalOnRequest),
		},
	}
	if got := piApprovalMode(opts); got != "never" {
		t.Fatalf("pi approval mode = %q, want never", got)
	}
}

func TestPrepareBridgeDoesNotInstallSystemToolGateForServerOnRequestPolicy(t *testing.T) {
	opts := runtimecore.ExecOptions{
		Cwd: t.TempDir(),
		Environment: map[string]string{
			"PI_CODING_AGENT_DIR": filepath.Join(t.TempDir(), "pi-home"),
		},
		ProviderConfig: json.RawMessage(`{"approval_mode":"never"}`),
		ProviderSpecific: map[string]string{
			agentcore.RuntimeConversationHILModeProviderKey: "manual",
		},
		WorkspacePolicy: agentcore.RuntimeWorkspacePolicy{
			ApprovalPolicy: string(agentcore.RuntimeApprovalOnRequest),
		},
	}
	broker := runtimecore.ApprovalBrokerFunc(func(context.Context, runtimecore.ApprovalRequest, runtimecore.ApprovalResponder) error {
		return nil
	})
	prepared, cleanup, err := prepareBridge(context.Background(), nil, broker, opts)
	defer cleanup()
	if err != nil {
		t.Fatalf("prepare bridge: %v", err)
	}
	if prepared.ConfigPath != "" || prepared.BaseURL != "" || prepared.Server != nil {
		t.Fatalf("system-only approval bridge was installed: %+v", prepared)
	}
}
