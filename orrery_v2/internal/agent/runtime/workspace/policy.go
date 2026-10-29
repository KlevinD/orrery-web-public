package workspace

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

var unsafePathSegment = regexp.MustCompile(`[^a-zA-Z0-9._-]+`)

func RuntimePolicyForRun(root, missionID, taskID, runID string) (agentcore.RuntimeWorkspacePolicy, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return agentcore.RuntimeWorkspacePolicy{}, nil
	}
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		return agentcore.RuntimeWorkspacePolicy{}, fmt.Errorf("workspace root must be an absolute path")
	}
	workDir := filepath.Join(
		root,
		"missions",
		safePathSegment(missionID, "mission"),
		"tasks",
		safePathSegment(taskID, "task"),
		"runs",
		safePathSegment(runID, "run"),
	)
	if !pathWithin(root, workDir) {
		return agentcore.RuntimeWorkspacePolicy{}, fmt.Errorf("workspace run directory escaped root")
	}
	profile := agentcore.RuntimeSandboxProfile{
		Mode:                 agentcore.RuntimeSandboxWorkspaceWrite,
		Approval:             agentcore.RuntimeApprovalOnRequest,
		Network:              agentcore.RuntimeNetworkDefault,
		RuntimeHomeIsolation: agentcore.RuntimeHomeIsolatedPerRun,
		Retention:            agentcore.RuntimeRetentionKeepOnFailure,
		Root:                 root,
		WorkDir:              workDir,
		WritableRoots:        []string{root},
		EnforceBoundary:      true,
	}
	return agentcore.RuntimeWorkspacePolicy{
		Root:            root,
		WorkDir:         workDir,
		WritableRoots:   []string{root},
		SandboxMode:     "workspace-write",
		ApprovalPolicy:  "on-request",
		EnforceBoundary: true,
		Profile:         profile,
	}, nil
}

func safePathSegment(value, fallback string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, string(filepath.Separator), "-")
	value = strings.Trim(unsafePathSegment.ReplaceAllString(value, "-"), ".-")
	if value == "" {
		return fallback
	}
	return value
}

func pathWithin(root, child string) bool {
	root = filepath.Clean(root)
	child = filepath.Clean(child)
	rel, err := filepath.Rel(root, child)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel))
}
