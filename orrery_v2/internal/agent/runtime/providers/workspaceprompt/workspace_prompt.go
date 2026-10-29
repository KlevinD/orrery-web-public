package workspaceprompt

import (
	"strings"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func Boundary(opts runtimecore.ExecOptions) string {
	policy := opts.WorkspacePolicy
	if !policy.EnforceBoundary || strings.TrimSpace(policy.Root) == "" {
		return ""
	}
	workDir := strings.TrimSpace(policy.WorkDir)
	if workDir == "" {
		workDir = strings.TrimSpace(opts.Cwd)
	}
	lines := []string{
		"Workspace boundary:",
		"- Workspace root: " + strings.TrimSpace(policy.Root),
	}
	if workDir != "" {
		lines = append(lines, "- Current task work directory: "+workDir)
	}
	lines = append(lines,
		"- Do not create, edit, move, or delete files outside the workspace root.",
		"- If the task requires reading or writing outside the workspace root, request approval before proceeding.",
	)
	if strings.TrimSpace(policy.SandboxMode) != "" {
		lines = append(lines, "- Sandbox mode: "+strings.TrimSpace(policy.SandboxMode))
	}
	if strings.TrimSpace(policy.ApprovalPolicy) != "" {
		lines = append(lines, "- Approval policy: "+strings.TrimSpace(policy.ApprovalPolicy))
	}
	return strings.Join(lines, "\n")
}

func SystemPrompt(systemPrompt string, opts runtimecore.ExecOptions) string {
	boundary := Boundary(opts)
	systemPrompt = strings.TrimSpace(systemPrompt)
	if boundary == "" {
		return systemPrompt
	}
	if systemPrompt == "" {
		return boundary
	}
	return systemPrompt + "\n\n" + boundary
}
