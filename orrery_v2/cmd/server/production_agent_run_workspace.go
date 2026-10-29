package main

import (
	"fmt"
	"os"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	runtimeworkspace "orrery_v2/internal/agent/runtime/workspace"
	"orrery_v2/internal/platform/config"
)

func prepareProductionAgentRunWorkspace(run *runtimedaemon.AgentRun, cfg config.Config, profileWorkspaceRoot string) error {
	if run == nil {
		return fmt.Errorf("agent run is required")
	}
	options := &run.Request.RuntimeOptions
	if strings.TrimSpace(options.WorkingDirectory) != "" || productionRuntimeWorkspaceConfigured(*options) {
		return nil
	}
	root := strings.TrimSpace(profileWorkspaceRoot)
	persistent := root != ""
	if root == "" {
		root = strings.TrimSpace(cfg.Runtime.ExecEnvRoot)
	}
	if root == "" {
		root = strings.TrimSpace(cfg.BaseDir)
	}
	absoluteRoot, err := productionRuntimeWorkingDirectory(cfg.BaseDir, root)
	if err != nil {
		return fmt.Errorf("resolve agent runtime workspace root: %w", err)
	}
	var policy agentcore.RuntimeWorkspacePolicy
	if persistent {
		profile := agentcore.RuntimeSandboxProfile{
			Mode:            agentcore.RuntimeSandboxWorkspaceWrite,
			Approval:        agentcore.RuntimeApprovalOnRequest,
			Root:            absoluteRoot,
			WorkDir:         absoluteRoot,
			WritableRoots:   []string{absoluteRoot},
			EnforceBoundary: true,
		}
		policy = profile.WorkspacePolicy()
	} else {
		policy, err = runtimeworkspace.RuntimePolicyForRun(absoluteRoot, missionIDFromRun(*run).String(), taskIDFromRun(*run).String(), run.RunID.String())
		if err != nil {
			return fmt.Errorf("build agent runtime workspace policy: %w", err)
		}
	}
	if strings.TrimSpace(policy.WorkDir) == "" {
		return fmt.Errorf("agent runtime workspace policy has no working directory")
	}
	if err := os.MkdirAll(policy.WorkDir, 0o755); err != nil {
		return fmt.Errorf("create agent runtime workspace: %w", err)
	}
	options.WorkingDirectory = policy.WorkDir
	options.WorkspacePolicy = policy
	options.SandboxProfile = policy.Profile.Clone()
	return nil
}

func productionRuntimeWorkspaceConfigured(options agentcore.RuntimeOptions) bool {
	policy := options.WorkspacePolicy
	return strings.TrimSpace(policy.Root) != "" ||
		strings.TrimSpace(policy.WorkDir) != "" ||
		len(policy.WritableRoots) > 0 ||
		strings.TrimSpace(policy.SandboxMode) != "" ||
		strings.TrimSpace(policy.ApprovalPolicy) != "" ||
		policy.EnforceBoundary ||
		!policy.Profile.Empty() ||
		!options.SandboxProfile.Empty()
}
