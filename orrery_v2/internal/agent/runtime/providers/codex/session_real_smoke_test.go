package codex

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

// TestNativeRealCLISmoke is opt-in because it uses the installed Codex CLI,
// configured credentials, and network. It never substitutes the process fixture.
func TestNativeRealCLISmoke(t *testing.T) {
	if os.Getenv("ORRERY_CODEX_REAL_SMOKE") != "1" {
		t.Skip("set ORRERY_CODEX_REAL_SMOKE=1 to exercise the installed Codex CLI")
	}
	t.Run("fresh multi-turn and resume", func(t *testing.T) {
		workspace := realSmokeWorkspace(t)
		connection := realNativeConnection(t, workspace, "workspace-write", "never")
		backend := openRealNative(t, connection, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "real-fresh"})
		dispatchRealPrompt(t, backend, "fresh-1", "Reply with exactly ORRERY_BRIDGE_SMOKE_ONE.")
		first := waitRealTerminal(t, backend.Events())
		if first.Event.TurnResult.IsError {
			t.Fatalf("fresh turn failed: %#v", first.Event.TurnResult)
		}
		dispatchRealPrompt(t, backend, "fresh-2", "Reply with exactly ORRERY_BRIDGE_SMOKE_TWO.")
		second := waitRealTerminal(t, backend.Events())
		if second.Event.TurnResult.IsError || second.TurnGeneration != 2 {
			t.Fatalf("multi-turn failed: %#v", second)
		}
		threadID := backend.boundThread()
		if threadID == "" {
			t.Fatal("fresh session did not bind a backend thread")
		}
		if err := connection.CloseSession(context.Background(), "real-fresh"); err != nil {
			t.Fatal(err)
		}
		resumed := openRealNative(t, connection, agentcore.SessionSpec{Mode: agentcore.SessionOpenResume, SessionID: "real-resume", BackendSessionID: threadID})
		dispatchRealPrompt(t, resumed, "resume-1", "Reply with exactly ORRERY_BRIDGE_SMOKE_RESUMED.")
		if terminal := waitRealTerminal(t, resumed.Events()); terminal.Event.TurnResult.IsError || resumed.boundThread() != threadID {
			t.Fatalf("resume failed: terminal=%#v thread=%q want=%q", terminal, resumed.boundThread(), threadID)
		}
		_ = connection.CloseSession(context.Background(), "real-resume")
	})

	t.Run("steer", func(t *testing.T) {
		connection := realNativeConnection(t, realSmokeWorkspace(t), "workspace-write", "never")
		backend := openRealNative(t, connection, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "real-steer"})
		dispatchRealPrompt(t, backend, "steer-send", "Run a shell command that waits for 60 seconds using the native command for this operating system, then reply with the result.")
		waitRealEvent(t, backend.Events(), agentcore.EventToolCall, 2*time.Minute)
		command := agentcore.RuntimeCommand{CommandID: "steer", Kind: agentcore.RuntimeCommandSteer, Steer: &agentcore.SteerCommand{Content: []agentcore.ContentBlock{{Kind: agentcore.ContentBlockText, Text: "Instead reply with exactly ORRERY_BRIDGE_STEERED."}}}}
		if _, err := backend.Dispatch(context.Background(), command); err != nil {
			t.Fatal(err)
		}
		if terminal := waitRealTerminal(t, backend.Events()); terminal.Event.TurnResult.IsError {
			t.Fatalf("steered turn failed: %#v", terminal.Event.TurnResult)
		}
		_ = connection.CloseSession(context.Background(), "real-steer")
	})

	t.Run("cancel", func(t *testing.T) {
		connection := realNativeConnection(t, realSmokeWorkspace(t), "workspace-write", "never")
		backend := openRealNative(t, connection, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "real-cancel"})
		dispatchRealPrompt(t, backend, "cancel-send", "Run a shell command that waits for 60 seconds using the native command for this operating system, then reply with the result.")
		waitRealEvent(t, backend.Events(), agentcore.EventToolCall, 2*time.Minute)
		command := agentcore.RuntimeCommand{CommandID: "cancel", Kind: agentcore.RuntimeCommandCancel, Cancel: &agentcore.CancelCommand{Target: agentcore.CancelTargetTurn}}
		if _, err := backend.Dispatch(context.Background(), command); err != nil {
			t.Fatal(err)
		}
		terminal := waitRealTerminal(t, backend.Events())
		if terminal.Event.TurnResult.Outcome.Kind != agentcore.TurnOutcomeCancelled {
			t.Fatalf("cancel outcome = %#v", terminal.Event.TurnResult)
		}
		_ = connection.CloseSession(context.Background(), "real-cancel")
	})

	t.Run("approval denied", func(t *testing.T) {
		connection := realNativeConnection(t, realSmokeWorkspace(t), "read-only", "on-request")
		backend := openRealNative(t, connection, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "real-approval"})
		dispatchRealPrompt(t, backend, "approval-send", "Use a shell command to create bridge-smoke-denied.tmp in the current directory, then report the result.")
		permission := waitRealEvent(t, backend.Events(), agentcore.EventPermission, 2*time.Minute)
		answer := agentcore.RuntimeCommand{CommandID: "approval-deny", Kind: agentcore.RuntimeCommandAnswerPermission, AnswerPermission: &agentcore.AnswerPermissionCommand{RequestID: permission.Event.Permission.RequestID, Decision: agentcore.PermissionDenied}}
		if _, err := backend.Dispatch(context.Background(), answer); err != nil {
			t.Fatal(err)
		}
		waitRealTerminal(t, backend.Events())
		_ = connection.CloseSession(context.Background(), "real-approval")
	})

	t.Run("crash", func(t *testing.T) {
		connection := realNativeConnection(t, realSmokeWorkspace(t), "workspace-write", "never")
		backend := openRealNative(t, connection, agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "real-crash"})
		dispatchRealPrompt(t, backend, "crash-send", "Run a shell command that waits for 60 seconds using the native command for this operating system, then reply with the result.")
		waitRealEvent(t, backend.Events(), agentcore.EventToolCall, 2*time.Minute)
		binding := backend.currentBinding()
		if binding == nil || binding.cmd.Process == nil {
			t.Fatal("real Codex process is not running")
		}
		if err := runtimeprocess.TerminateProcessTree(binding.cmd); err != nil {
			t.Fatal(err)
		}
		terminal := waitRealTerminal(t, backend.Events())
		if !terminal.Event.TurnResult.IsError {
			t.Fatalf("crash terminal = %#v", terminal.Event.TurnResult)
		}
		waitRealEvent(t, backend.Events(), agentcore.EventDetached, time.Minute)
		_ = connection.CloseSession(context.Background(), "real-crash")
	})
}

func realSmokeWorkspace(t *testing.T) string {
	t.Helper()
	directory, err := os.MkdirTemp("", "orrery-codex-real-smoke-")
	if err != nil {
		t.Fatal(err)
	}
	directory, err = filepath.Abs(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for attempt := 0; attempt < 50; attempt++ {
			if err := os.RemoveAll(directory); err == nil {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
	})
	return directory
}

func realNativeConnection(t *testing.T, workspace, sandbox, approval string) *NativeConnection {
	t.Helper()
	connection, err := NewNativeConnection(NativeConfig{
		Executable: "codex", HandshakeTimeout: 30 * time.Second,
		ResolveWorkspace: WorkspaceResolverFunc(func(context.Context, string) (NativeWorkspace, error) {
			return NativeWorkspace{Directory: workspace, Sandbox: sandbox, ApprovalPolicy: approval, MCPServers: map[string]NativeMCPServer{}}, nil
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	return connection
}

func openRealNative(t *testing.T, connection *NativeConnection, spec agentcore.SessionSpec) *NativeSession {
	t.Helper()
	backend, err := connection.OpenSession(context.Background(), spec, agentcore.SessionConfig{WorkspaceRef: "real-smoke-workspace"})
	if err != nil {
		t.Fatal(err)
	}
	return backend.(*NativeSession)
}

func dispatchRealPrompt(t *testing.T, backend *NativeSession, id, prompt string) {
	t.Helper()
	receipt, err := backend.Dispatch(context.Background(), nativeSend(id, id+"-client", prompt))
	if err != nil || !receipt.Accepted || receipt.Admission != agentcore.AdmissionStarted {
		t.Fatalf("real send receipt = %#v, %v", receipt, err)
	}
}

func waitRealTerminal(t *testing.T, events <-chan agentcore.SessionEnvelope) agentcore.SessionEnvelope {
	t.Helper()
	return waitRealEvent(t, events, agentcore.EventTurnResult, 5*time.Minute)
}

func waitRealEvent(t *testing.T, events <-chan agentcore.SessionEnvelope, kind agentcore.SessionEventKind, timeout time.Duration) agentcore.SessionEnvelope {
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
				t.Fatalf("real Codex detached before %s: %s", kind, strings.TrimSpace(event.Event.Detached.RedactedSummary))
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for real Codex %s", kind)
		}
	}
}
