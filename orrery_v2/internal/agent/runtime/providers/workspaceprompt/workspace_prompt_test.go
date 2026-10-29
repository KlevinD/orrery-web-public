package workspaceprompt

import (
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestBoundaryDescribesRootWorkDirAndApproval(t *testing.T) {
	prompt := Boundary(runtimecore.ExecOptions{
		WorkspacePolicy: agentcore.RuntimeWorkspacePolicy{
			Root:            "D:/workspace",
			WorkDir:         "D:/workspace/missions/m/tasks/t/runs/r",
			WritableRoots:   []string{"D:/workspace"},
			ApprovalPolicy:  "on-request",
			SandboxMode:     "workspace-write",
			EnforceBoundary: true,
		},
	})

	for _, want := range []string{
		"Workspace root: D:/workspace",
		"Current task work directory: D:/workspace/missions/m/tasks/t/runs/r",
		"Do not create, edit, move, or delete files outside the workspace root",
		"request approval",
		"workspace-write",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q: %s", want, prompt)
		}
	}
}
