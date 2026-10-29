package workspace

import (
	"path/filepath"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestRuntimePolicyForRunCreatesWorkspaceBoundWorkDir(t *testing.T) {
	root := filepath.Join(t.TempDir(), "Orrery Workspace")

	policy, err := RuntimePolicyForRun(root, "mission/../../alpha", "task:one", "run 1")
	if err != nil {
		t.Fatalf("RuntimePolicyForRun: %v", err)
	}

	if policy.Root != filepath.Clean(root) {
		t.Fatalf("Root = %q, want %q", policy.Root, filepath.Clean(root))
	}
	if policy.WorkDir == "" || !strings.HasPrefix(policy.WorkDir, filepath.Clean(root)+string(filepath.Separator)) {
		t.Fatalf("WorkDir = %q, want under %q", policy.WorkDir, root)
	}
	for _, banned := range []string{"..", "/", "\\"} {
		if strings.Contains(filepath.Base(policy.WorkDir), banned) {
			t.Fatalf("WorkDir base %q contains unsafe segment %q", filepath.Base(policy.WorkDir), banned)
		}
	}
	if len(policy.WritableRoots) != 1 || policy.WritableRoots[0] != filepath.Clean(root) {
		t.Fatalf("WritableRoots = %#v, want only %q", policy.WritableRoots, filepath.Clean(root))
	}
	if policy.SandboxMode != "workspace-write" || policy.ApprovalPolicy != "on-request" || !policy.EnforceBoundary {
		t.Fatalf("unexpected boundary policy: %+v", policy)
	}
	if policy.Profile.Mode != agentcore.RuntimeSandboxWorkspaceWrite ||
		policy.Profile.Approval != agentcore.RuntimeApprovalOnRequest ||
		policy.Profile.Network != agentcore.RuntimeNetworkDefault ||
		policy.Profile.RuntimeHomeIsolation != agentcore.RuntimeHomeIsolatedPerRun ||
		policy.Profile.Retention != agentcore.RuntimeRetentionKeepOnFailure {
		t.Fatalf("sandbox profile = %+v", policy.Profile)
	}
}

func TestRuntimePolicyForRunRejectsRelativeWorkspaceRoot(t *testing.T) {
	if _, err := RuntimePolicyForRun("relative/workspace", "mission", "task", "run"); err == nil {
		t.Fatalf("RuntimePolicyForRun should reject relative roots")
	}
}
