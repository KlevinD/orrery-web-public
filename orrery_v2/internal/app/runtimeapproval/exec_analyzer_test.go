package runtimeapproval

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecAnalyzerAllowsEveryMatchedPipelineSegment(t *testing.T) {
	policy := ExecPolicy{Security: SecurityAllowlist, Allowlist: []ExecRule{
		{RuleID: "list", Executable: "ls", SafeFlags: []string{"-la"}, WorkspaceOnly: true},
		{RuleID: "head", Executable: "head", SafeFlags: []string{"-n"}, WorkspaceOnly: true},
	}}

	got := AnalyzeExec(ExecRequest{
		Command:          "ls -la . | head -n 20",
		WorkingDirectory: "/workspace/project",
		WorkspaceRoots:   []string{"/workspace"},
	}, policy)
	if !got.Safe || got.Reason != "exec_allowlist_match" {
		t.Fatalf("analysis = %+v", got)
	}
	if len(got.Commands) != 2 || len(got.MatchedRuleIDs) != 2 || got.MatchedRuleIDs[0] != "list" || got.MatchedRuleIDs[1] != "head" {
		t.Fatalf("pipeline analysis = %+v", got)
	}

	miss := AnalyzeExec(ExecRequest{
		Command:          "ls -la . | tail -n 20",
		WorkingDirectory: "/workspace/project",
		WorkspaceRoots:   []string{"/workspace"},
	}, policy)
	if miss.Safe || miss.Reason != "exec_allowlist_miss" {
		t.Fatalf("unmatched pipeline analysis = %+v", miss)
	}
}

func TestExecAnalyzerAllowsEveryMatchedSequentialSegment(t *testing.T) {
	policy := ExecPolicy{Security: SecurityAllowlist, Allowlist: []ExecRule{
		{RuleID: "pwd", Executable: "pwd", WorkspaceOnly: true},
		{RuleID: "rg", Executable: "rg", SafeFlags: []string{"-n", "--hidden", "-g"}, WorkspaceOnly: true},
	}}
	for _, command := range []string{
		`pwd && rg -n "error" . --hidden -g '!node_modules'`,
		`pwd; rg -n "error" . --hidden -g '!node_modules'`,
	} {
		got := AnalyzeExec(ExecRequest{
			Command:          command,
			WorkingDirectory: "/workspace/project",
			WorkspaceRoots:   []string{"/workspace"},
		}, policy)
		if !got.Safe || got.Reason != "exec_allowlist_match" || len(got.Commands) != 2 {
			t.Fatalf("analysis for %q = %+v", command, got)
		}
	}

	miss := AnalyzeExec(ExecRequest{
		Command:          "pwd && curl https://example.com",
		WorkingDirectory: "/workspace/project",
		WorkspaceRoots:   []string{"/workspace"},
	}, policy)
	if miss.Safe || miss.Reason != "exec_allowlist_miss" {
		t.Fatalf("unmatched sequential analysis = %+v", miss)
	}
}

func TestExecAnalyzerRejectsWriteAndUnsupportedShellConstructs(t *testing.T) {
	policy := ExecPolicy{Security: SecurityFull}
	tests := []struct {
		name    string
		command string
		reason  string
	}{
		{name: "write redirect", command: "printf hi > output.txt", reason: "exec_redirection"},
		{name: "command substitution", command: "printf '%s' $(whoami)", reason: "exec_dynamic_word"},
		{name: "process substitution", command: "diff <(ls) <(ls)", reason: "exec_dynamic_word"},
		{name: "heredoc", command: "cat <<EOF\nsecret\nEOF", reason: "exec_redirection"},
		{name: "background", command: "sleep 1 &", reason: "exec_background"},
		{name: "or list", command: "pwd || ls", reason: "exec_compound"},
		{name: "assignment", command: "NAME=value env", reason: "exec_assignment"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := AnalyzeExec(ExecRequest{Command: tt.command, WorkingDirectory: "/workspace", WorkspaceRoots: []string{"/workspace"}}, policy)
			if got.Safe || got.Reason != tt.reason {
				t.Fatalf("analysis = %+v, want reason %q", got, tt.reason)
			}
		})
	}
}

func TestExecAnalyzerChecksDangerousSequentialSegments(t *testing.T) {
	got := AnalyzeExec(
		ExecRequest{Command: "pwd && rm -rf .", WorkingDirectory: "/workspace", WorkspaceRoots: []string{"/workspace"}},
		ExecPolicy{Security: SecurityFull},
	)
	if got.Safe || !got.ForcedHuman || got.Reason != "exec_destructive" {
		t.Fatalf("analysis = %+v, want destructive segment rejected", got)
	}
}

func TestExecAnalyzerRejectsMutationFlags(t *testing.T) {
	policy := ExecPolicy{Security: SecurityFull}
	tests := []struct {
		command string
		reason  string
	}{
		{command: "sed -i s/old/new/ file.txt", reason: "exec_mutation_flag"},
		{command: "sed --in-place s/old/new/ file.txt", reason: "exec_mutation_flag"},
		{command: "find . -delete", reason: "exec_mutation_flag"},
		{command: "find . -exec touch {} ;", reason: "exec_mutation_flag"},
		{command: "find . -exec touch {} +", reason: "exec_mutation_flag"},
	}
	for _, tt := range tests {
		got := AnalyzeExec(ExecRequest{Command: tt.command, WorkingDirectory: "/workspace", WorkspaceRoots: []string{"/workspace"}}, policy)
		if got.Safe || got.Reason != tt.reason {
			t.Fatalf("analysis for %q = %+v, want reason %q", tt.command, got, tt.reason)
		}
	}
}

func TestExecAnalyzerEnforcesWorkspacePathBoundary(t *testing.T) {
	policy := ExecPolicy{Security: SecurityAllowlist, Allowlist: []ExecRule{{
		RuleID: "cat", Executable: "cat", WorkspaceOnly: true,
	}}}

	inside := AnalyzeExec(ExecRequest{Command: "cat docs/readme.md", WorkingDirectory: "/workspace/project", WorkspaceRoots: []string{"/workspace"}}, policy)
	if !inside.Safe {
		t.Fatalf("inside path analysis = %+v", inside)
	}
	out := AnalyzeExec(ExecRequest{Command: "cat ../../etc/passwd", WorkingDirectory: "/workspace/project", WorkspaceRoots: []string{"/workspace"}}, policy)
	if out.Safe || out.Reason != "exec_workspace_escape" {
		t.Fatalf("outside path analysis = %+v", out)
	}
	abs := AnalyzeExec(ExecRequest{Command: "cat /etc/passwd", WorkingDirectory: "/workspace/project", WorkspaceRoots: []string{"/workspace"}}, policy)
	if abs.Safe || abs.Reason != "exec_workspace_escape" {
		t.Fatalf("absolute outside path analysis = %+v", abs)
	}
}

func TestExecAnalyzerResolvesWorkspaceSymlinks(t *testing.T) {
	workspace := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(workspace, "outside")); err != nil {
		t.Skipf("symlink creation is unavailable: %v", err)
	}
	policy := ExecPolicy{Security: SecurityAllowlist, Allowlist: []ExecRule{{
		RuleID: "cat", Executable: "cat", WorkspaceOnly: true,
	}}}

	escape := AnalyzeExec(ExecRequest{
		Command: "cat outside/secret.txt", WorkingDirectory: workspace, WorkspaceRoots: []string{workspace},
	}, policy)
	if escape.Safe || escape.Reason != "exec_workspace_escape" {
		t.Fatalf("symlink escape analysis = %+v", escape)
	}

	missingInside := AnalyzeExec(ExecRequest{
		Command: "cat reports/not-created.txt", WorkingDirectory: workspace, WorkspaceRoots: []string{workspace},
	}, policy)
	if !missingInside.Safe || missingInside.Reason != "exec_allowlist_match" {
		t.Fatalf("missing inside path analysis = %+v", missingInside)
	}
}

func TestExecAnalyzerDoesNotAllowShellInterpreterByName(t *testing.T) {
	for _, command := range []string{"bash -c 'git status'", "sh -c 'pwd'", "python -c 'print(1)'", "node -e 'console.log(1)'"} {
		got := AnalyzeExec(ExecRequest{Command: command, WorkingDirectory: "/workspace", WorkspaceRoots: []string{"/workspace"}}, ExecPolicy{Security: SecurityFull})
		if got.Safe || got.Reason != "exec_inline_interpreter" {
			t.Fatalf("analysis for %q = %+v", command, got)
		}
	}
}

func TestExecAnalyzerHonorsConfiguredForcedHumanExecutable(t *testing.T) {
	got := AnalyzeExec(
		ExecRequest{Command: "curl https://example.com", WorkingDirectory: "/workspace", WorkspaceRoots: []string{"/workspace"}},
		ExecPolicy{Security: SecurityFull},
		ForcedHumanPolicy{ExecExecutables: []string{"curl"}},
	)
	if got.Safe || !got.ForcedHuman || got.Reason != "forced_configured_exec" {
		t.Fatalf("analysis = %+v", got)
	}
}
