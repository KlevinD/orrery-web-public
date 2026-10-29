package runtimeapproval

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestNativeFileChangeRequiresEveryPathWithinHostWriteRoots(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "agent")
	source := filepath.Join(root, "source")
	for _, p := range []string{workDir, source} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	req := runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindFileChange, ToolName: "patch_apply", Metadata: map[string]string{"approval_native_tool_name": "FileChange"}, WorkspaceRoot: root, WorkingDirectory: workDir, WorkspaceBoundaryEnforced: true, WorkspaceWriteAuthorized: true, WorkspaceWriteRoots: []string{source}, FileChangePaths: []string{filepath.Join(source, "one.go"), filepath.Join(source, "two.go")}}
	service := NewDecisionService(StaticPolicyProvider{Policy: DefaultPolicy()}, nil)
	decide := func(r runtimecore.ApprovalRequest) Decision {
		return service.Decide(context.Background(), DecisionInput{Request: r})
	}
	if got := decide(req); got.Outcome != DecisionOutcomeApproveOnce || got.DecisionSource != "mission_workspace" {
		t.Fatalf("host-authorized source edit: %+v", got)
	}
	for name, mutate := range map[string]func(*runtimecore.ApprovalRequest){
		"one outside target": func(r *runtimecore.ApprovalRequest) {
			r.FileChangePaths = []string{filepath.Join(source, "safe.go"), filepath.Join(root, "outside.go")}
		},
		"missing item paths":    func(r *runtimecore.ApprovalRequest) { r.FileChangePaths = nil },
		"no host authorization": func(r *runtimecore.ApprovalRequest) { r.WorkspaceWriteAuthorized = false },
		"no enforced boundary":  func(r *runtimecore.ApprovalRequest) { r.WorkspaceBoundaryEnforced = false },
		"outside write root":    func(r *runtimecore.ApprovalRequest) { r.WorkspaceWriteRoots = []string{filepath.Dir(root)} },
		"relative write root":   func(r *runtimecore.ApprovalRequest) { r.WorkspaceWriteRoots = []string{"."} },
		"forced review": func(r *runtimecore.ApprovalRequest) {
			r.Metadata = map[string]string{"approval_native_tool_name": "FileChange", "approval_force_manual": "true"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			r := req
			mutate(&r)
			if got := decide(r); got.Outcome != DecisionOutcomeDeny {
				t.Fatalf("invalid scope accepted: %+v", got)
			}
		})
	}
	t.Run("symlink target", func(t *testing.T) {
		outside := t.TempDir()
		link := filepath.Join(source, "escape")
		if err := os.Symlink(outside, link); err != nil {
			t.Skip(err)
		}
		r := req
		r.FileChangePaths = []string{filepath.Join(link, "outside.go")}
		if got := decide(r); got.Outcome != DecisionOutcomeDeny {
			t.Fatalf("symlink escape accepted: %+v", got)
		}
	})
	other := req
	other.FileChangePaths = []string{filepath.Join(source, "other.go")}
	if approvalInputDigest(req) == approvalInputDigest(other) {
		t.Fatal("approval digest did not bind changed paths")
	}
}
