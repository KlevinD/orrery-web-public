package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestPersonalAssistantShellExecutorRunsInsideWorkspace(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workDir := filepath.Join(root, "assistant")
	executor := productionPersonalAssistantShellExecutor{}

	result, err := executor.ExecutePersonalAssistantShell(ctx, string(toolstd.PersonalAssistantShellExec), mcptransport.PersonalAssistantShellRequest{
		WorkspaceRoot:     root,
		WorkingDirectory:  workDir,
		Command:           testShellCommand("pwd"),
		TimeoutMs:         5000,
		MaxOutputLen:      4096,
		EnvironmentPolicy: "empty",
	})
	if err != nil {
		t.Fatalf("ExecutePersonalAssistantShell: %v", err)
	}
	if result.Output["exit_code"] != 0 {
		t.Fatalf("result = %+v", result.Output)
	}
	stdout := filepath.Clean(strings.TrimSpace(result.Output["stdout"].(string)))
	if stdout != filepath.Clean(workDir) {
		t.Fatalf("stdout cwd = %q, want %q", stdout, filepath.Clean(workDir))
	}
}

func TestPersonalAssistantShellExecutorRejectsCwdOutsideWorkspace(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	outside := t.TempDir()
	executor := productionPersonalAssistantShellExecutor{}

	_, err := executor.ExecutePersonalAssistantShell(ctx, string(toolstd.PersonalAssistantShellExec), mcptransport.PersonalAssistantShellRequest{
		WorkspaceRoot:    root,
		WorkingDirectory: filepath.Join(root, "assistant"),
		Cwd:              outside,
		Command:          testShellCommand("pwd"),
	})
	if err == nil || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("error = %v, want workspace boundary rejection", err)
	}
}

func TestPersonalAssistantShellExecutorTruncatesOutput(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	executor := productionPersonalAssistantShellExecutor{}

	result, err := executor.ExecutePersonalAssistantShell(ctx, string(toolstd.PersonalAssistantShellExec), mcptransport.PersonalAssistantShellRequest{
		WorkspaceRoot:    root,
		WorkingDirectory: filepath.Join(root, "assistant"),
		Command:          testShellCommand("long-output"),
		MaxOutputLen:     12,
		TimeoutMs:        5000,
	})
	if err != nil {
		t.Fatalf("ExecutePersonalAssistantShell: %v", err)
	}
	if result.Output["truncated"] != true {
		t.Fatalf("result should mark truncated output: %+v", result.Output)
	}
	if len(result.Output["stdout"].(string)) > 12 {
		t.Fatalf("stdout was not truncated: %+v", result.Output)
	}
}

func TestPersonalAssistantShellExecutorTerminatesChildProcessesOnTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process-group assertion")
	}
	root := t.TempDir()
	executor := productionPersonalAssistantShellExecutor{}

	_, err := executor.ExecutePersonalAssistantShell(context.Background(), string(toolstd.PersonalAssistantShellExec), mcptransport.PersonalAssistantShellRequest{
		WorkspaceRoot:    root,
		WorkingDirectory: root,
		Command:          "(sleep 0.4; printf leaked > child-marker) & wait",
		TimeoutMs:        50,
	})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("ExecutePersonalAssistantShell error = %v, want deadline exceeded", err)
	}
	time.Sleep(500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(root, "child-marker")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("timed-out child survived and wrote marker: %v", err)
	}
}

func TestPersonalAssistantShellTimeoutKeepsDefaultCeiling(t *testing.T) {
	if got := personalAssistantShellTimeout(int((3*time.Minute)/time.Millisecond), 0); got != 2*time.Minute {
		t.Fatalf("personalAssistantShellTimeout = %s, want 2m", got)
	}
}

func TestProductionRuntimeToolDispatcherAllowsUserWithBoundPersonalAssistantShellPermission(t *testing.T) {
	shell := &productionRuntimeTestPersonalAssistantShellExecutor{}
	dispatcher := productionRuntimeToolDispatcher{personalShell: shell}

	result, err := dispatcher.CallRuntimeTool(context.Background(), runtimecore.RuntimeToolCallRequest{
		Name:    "Shell",
		AgentID: "personal-assistant",
		ExecutionPrincipal: agentcore.ExecutionPrincipal{
			Kind: agentcore.ExecutionPrincipalUser, ID: "user-1",
			PermissionScope: []string{"mission.read"},
		},
		Input: map[string]any{"command": "pwd"},
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID:             toolstd.PersonalAssistantShellExec,
			Alias:              "Shell",
			GrantedPermissions: []string{toolstd.PermissionPersonalAssistantShell},
		}},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool(bound personal shell): %v", err)
	}
	if shell.toolID != "Shell" || shell.request.Command != "pwd" || result.Output["stdout"] != "shell ok" {
		t.Fatalf("bound shell execution = tool:%q request:%+v result:%+v", shell.toolID, shell.request, result.Output)
	}
}

func testShellCommand(kind string) string {
	if runtime.GOOS == "windows" {
		switch kind {
		case "pwd":
			return "cd"
		case "long-output":
			return "echo abcdefghijklmnop"
		}
	}
	switch kind {
	case "pwd":
		return "pwd"
	case "long-output":
		return "printf abcdefghijklmnop"
	}
	return kind
}

type productionRuntimeTestPersonalAssistantShellExecutor struct {
	toolID  string
	request mcptransport.PersonalAssistantShellRequest
}

func (e *productionRuntimeTestPersonalAssistantShellExecutor) ExecutePersonalAssistantShell(ctx context.Context, toolID string, req mcptransport.PersonalAssistantShellRequest) (mcptransport.PersonalAssistantShellResult, error) {
	e.toolID = toolID
	e.request = req
	return mcptransport.PersonalAssistantShellResult{Output: map[string]any{"stdout": "shell ok"}}, nil
}

type productionRuntimeTestMCPGateway struct {
	request mcptransport.CallToolRequest
	err     error
}

func (g *productionRuntimeTestMCPGateway) CallTool(ctx context.Context, req mcptransport.CallToolRequest) (mcptransport.CallToolResult, error) {
	if err := ctx.Err(); err != nil {
		return mcptransport.CallToolResult{}, err
	}
	g.request = req
	if g.err != nil {
		return mcptransport.CallToolResult{}, g.err
	}
	return mcptransport.CallToolResult{Output: map[string]any{"ok": true}}, nil
}
