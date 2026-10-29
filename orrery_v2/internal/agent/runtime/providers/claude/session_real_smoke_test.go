package claude

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimeprocess "orrery_v2/internal/agent/runtime/process"
)

// TestNativeClaudeRealCLISmoke is opt-in because it uses the installed Claude
// Code CLI, configured credentials, and network. It never substitutes a fixture.
func TestNativeClaudeRealCLISmoke(t *testing.T) {
	if os.Getenv("ORRERY_CLAUDE_REAL_SMOKE") != "1" {
		t.Skip("set ORRERY_CLAUDE_REAL_SMOKE=1 to exercise the installed Claude Code CLI")
	}

	t.Run("fresh multi-turn resume and config", func(t *testing.T) {
		workspace := realClaudeWorkspace(t)
		connection := realClaudeConnection(t, workspace)
		backend := openRealClaude(t, connection, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "claude-real-fresh"})
		dispatchRealClaudePrompt(t, backend, "fresh-1", "Reply with exactly ORRERY_CLAUDE_BRIDGE_ONE.")
		if terminal := waitRealClaudeTerminal(t, backend.Events()); terminal.Event.TurnResult.IsError {
			t.Fatalf("fresh turn failed: %#v", terminal.Event.TurnResult)
		}

		caps := backend.Capabilities()
		if len(caps.AvailableModels) == 0 {
			t.Fatal("real initialize returned no model catalog")
		}
		model := caps.AvailableModels[0].ID
		if _, err := backend.Dispatch(context.Background(), agentcore.RuntimeCommand{CommandID: "set-model", Kind: agentcore.RuntimeCommandSetModel, SetModel: &agentcore.SetModelCommand{Model: model}}); err != nil {
			t.Fatal(err)
		}
		waitRealClaudeEvent(t, backend.Events(), agentcore.EventConfigChanged, time.Minute)

		dispatchRealClaudePrompt(t, backend, "fresh-2", "Reply with exactly ORRERY_CLAUDE_BRIDGE_TWO.")
		if terminal := waitRealClaudeTerminal(t, backend.Events()); terminal.Event.TurnResult.IsError || terminal.TurnGeneration != 2 {
			t.Fatalf("multi-turn failed: %#v", terminal)
		}
		backendID := backend.backendSessionID
		if backendID == "" {
			t.Fatal("real Claude session did not expose a resume id")
		}
		if err := connection.CloseSession(context.Background(), "claude-real-fresh"); err != nil {
			t.Fatal(err)
		}
		resumed := openRealClaude(t, connection, agentcore.SessionSpec{Mode: agentcore.SessionOpenResume, SessionID: "claude-real-resume", BackendSessionID: backendID})
		dispatchRealClaudePrompt(t, resumed, "resume-1", "Reply with exactly ORRERY_CLAUDE_BRIDGE_RESUMED.")
		if terminal := waitRealClaudeTerminal(t, resumed.Events()); terminal.Event.TurnResult.IsError {
			t.Fatalf("resume failed: %#v", terminal.Event.TurnResult)
		}
		_ = connection.CloseSession(context.Background(), "claude-real-resume")
	})

	t.Run("cancel immediate next and queued config", func(t *testing.T) {
		connection := realClaudeConnection(t, realClaudeWorkspace(t))
		backend := openRealClaude(t, connection, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "claude-real-cancel"})
		dispatchRealClaudePrompt(t, backend, "cancel-1", "Use Bash to wait for 60 seconds, then reply with the result.")
		waitRealClaudeEvent(t, backend.Events(), agentcore.EventToolCall, 2*time.Minute)
		if _, err := backend.Dispatch(context.Background(), agentcore.RuntimeCommand{CommandID: "mode", Kind: agentcore.RuntimeCommandSetMode, SetMode: &agentcore.SetModeCommand{Mode: "plan"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := backend.Dispatch(context.Background(), agentcore.RuntimeCommand{CommandID: "cancel", Kind: agentcore.RuntimeCommandCancel, Cancel: &agentcore.CancelCommand{Target: agentcore.CancelTargetTurn}}); err != nil {
			t.Fatal(err)
		}
		waitRealClaudeTerminal(t, backend.Events())
		dispatchRealClaudePrompt(t, backend, "cancel-2", "Reply with exactly ORRERY_CLAUDE_AFTER_CANCEL.")
		if terminal := waitRealClaudeTerminal(t, backend.Events()); terminal.TurnGeneration != 2 || terminal.Event.TurnResult.IsError {
			t.Fatalf("post-cancel turn failed: %#v", terminal)
		}
		_ = connection.CloseSession(context.Background(), "claude-real-cancel")
	})

	t.Run("permission recovery", func(t *testing.T) {
		connection := realClaudeConnection(t, realClaudeWorkspace(t))
		backend := openRealClaude(t, connection, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "claude-real-permission"})
		dispatchRealClaudePrompt(t, backend, "permission-1", "Use Bash to create permission-denied.tmp in the current directory, then report the result.")
		permission := waitRealClaudeEvent(t, backend.Events(), agentcore.EventPermission, 2*time.Minute)
		pending := backend.PendingPermissionRequests()
		if len(pending) != 1 || pending[0].RequestID != permission.Event.Permission.RequestID {
			t.Fatalf("permission reload view = %#v", pending)
		}
		answer := agentcore.RuntimeCommand{CommandID: "deny", Kind: agentcore.RuntimeCommandAnswerPermission, AnswerPermission: &agentcore.AnswerPermissionCommand{RequestID: pending[0].RequestID, Decision: agentcore.PermissionDenied}}
		if _, err := backend.Dispatch(context.Background(), answer); err != nil {
			t.Fatal(err)
		}
		waitRealClaudeTerminal(t, backend.Events())
		if len(backend.PendingPermissionRequests()) != 0 {
			t.Fatal("answered permission remained recoverable")
		}
		_ = connection.CloseSession(context.Background(), "claude-real-permission")
	})

	t.Run("crash", func(t *testing.T) {
		connection := realClaudeConnection(t, realClaudeWorkspace(t))
		backend := openRealClaude(t, connection, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "claude-real-crash"})
		dispatchRealClaudePrompt(t, backend, "crash-1", "Use Bash to wait for 60 seconds, then reply with the result.")
		waitRealClaudeEvent(t, backend.Events(), agentcore.EventToolCall, 2*time.Minute)
		binding := backend.currentBinding()
		if binding == nil || binding.cmd.Process == nil {
			t.Fatal("real Claude process is not running")
		}
		if err := runtimeprocess.TerminateProcessTree(binding.cmd); err != nil {
			t.Fatal(err)
		}
		terminal := waitRealClaudeTerminal(t, backend.Events())
		if !terminal.Event.TurnResult.IsError {
			t.Fatalf("crash terminal = %#v", terminal.Event.TurnResult)
		}
		waitRealClaudeEvent(t, backend.Events(), agentcore.EventDetached, time.Minute)
		_ = connection.CloseSession(context.Background(), "claude-real-crash")
	})
}

func realClaudeWorkspace(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "orrery-claude-real-smoke-")
	if err != nil {
		t.Fatal(err)
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(directory) })
	return directory
}

func realClaudeConnection(t *testing.T, workspace string) *NativeConnection {
	t.Helper()
	connection, err := NewNativeConnection(NativeConfig{
		Executable: "claude", HandshakeTimeout: 30 * time.Second,
		ResolveWorkspace: NativeWorkspaceResolverFunc(func(context.Context, string) (NativeWorkspace, error) {
			return NativeWorkspace{Directory: workspace, MCPServers: map[string]NativeMCPServer{}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

func openRealClaude(t *testing.T, connection *NativeConnection, spec agentcore.SessionSpec) *NativeSession {
	t.Helper()
	backend, err := connection.OpenSession(context.Background(), spec, agentcore.SessionConfig{WorkspaceRef: "real-claude-workspace", Mode: "default"})
	if err != nil {
		t.Fatal(err)
	}
	return backend.(*NativeSession)
}

func dispatchRealClaudePrompt(t *testing.T, backend *NativeSession, id, prompt string) {
	t.Helper()
	receipt, err := backend.Dispatch(context.Background(), claudeSend(id, id+"-client", prompt))
	if err != nil || !receipt.Accepted || receipt.Admission != agentcore.AdmissionStarted {
		t.Fatalf("real send receipt = %#v, %v", receipt, err)
	}
}

func waitRealClaudeTerminal(t *testing.T, events <-chan agentcore.SessionEnvelope) agentcore.SessionEnvelope {
	t.Helper()
	return waitRealClaudeEvent(t, events, agentcore.EventTurnResult, 5*time.Minute)
}

func waitRealClaudeEvent(t *testing.T, events <-chan agentcore.SessionEnvelope, kind agentcore.SessionEventKind, timeout time.Duration) agentcore.SessionEnvelope {
	t.Helper()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for {
		select {
		case event, ok := <-events:
			if !ok {
				t.Fatalf("real event stream closed before %s", kind)
			}
			if event.Event.Kind == kind {
				return event
			}
			if event.Event.Kind == agentcore.EventDetached && kind != agentcore.EventDetached {
				t.Fatalf("real Claude detached before %s: %s", kind, strings.TrimSpace(event.Event.Detached.RedactedSummary))
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for real Claude %s", kind)
		}
	}
}
