package kernelcompat

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func TestConnectionRequiresEngineAndRejectsResume(t *testing.T) {
	if _, err := NewConnection(Config{}); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeWorkspaceUnavailable) {
		t.Fatalf("missing engine = %v", err)
	}
	var calls atomic.Int32
	connection, err := NewConnection(Config{Engine: EngineFunc(func(context.Context, agentcore.SessionSpec, agentcore.SessionConfig) (Runner, error) {
		calls.Add(1)
		return nil, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	_, err = connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenResume, SessionID: "session", BackendSessionID: "previous"}, agentcore.SessionConfig{WorkspaceRef: "workspace"})
	if !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeUnsupportedCommand) || calls.Load() != 0 {
		t.Fatalf("resume = %v, opens = %d", err, calls.Load())
	}
}

func TestConnectionOpeningFailureNeverBecomesUsableSession(t *testing.T) {
	for _, mode := range []string{"error", "nil", "panic"} {
		t.Run(mode, func(t *testing.T) {
			connection, err := NewConnection(Config{Engine: EngineFunc(func(context.Context, agentcore.SessionSpec, agentcore.SessionConfig) (Runner, error) {
				switch mode {
				case "error":
					return nil, errors.New("private credential")
				case "panic":
					panic("private credential")
				default:
					return nil, nil
				}
			})})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = connection.Close() })
			for attempt := 0; attempt < 2; attempt++ {
				backend, err := connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "session"}, agentcore.SessionConfig{WorkspaceRef: "workspace"})
				if backend != nil || !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeBackendRejected) || strings.Contains(err.Error(), "private credential") {
					t.Fatalf("open = %T, %v", backend, err)
				}
			}
		})
	}
}

func TestConnectionCloseCancelsOpeningAndRejectsLateRunner(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	openResult := make(chan error, 1)
	runner := &testRunner{run: func(context.Context, agentcore.SendCommand, Emit) (agentcore.TurnResultEvent, error) {
		return completed("done"), nil
	}}
	connection, err := NewConnection(Config{Engine: EngineFunc(func(context.Context, agentcore.SessionSpec, agentcore.SessionConfig) (Runner, error) {
		close(started)
		<-release
		return runner, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_, err := connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "session"}, agentcore.SessionConfig{WorkspaceRef: "workspace"})
		openResult <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("engine did not begin opening")
	}
	if _, err := connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "session"}, agentcore.SessionConfig{WorkspaceRef: "workspace"}); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeCommandConflict) {
		t.Fatalf("duplicate open = %v", err)
	}
	if err := connection.Close(); err != nil {
		t.Fatal(err)
	}
	close(release)
	select {
	case err := <-openResult:
		if err == nil {
			t.Fatal("late runner became active after close")
		}
	case <-time.After(time.Second):
		t.Fatal("late open did not settle")
	}
	if runner.closes.Load() != 1 {
		t.Fatalf("late runner closed %d times", runner.closes.Load())
	}
	if _, err := connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "next"}, agentcore.SessionConfig{WorkspaceRef: "workspace"}); !agentcore.IsRuntimeBridgeError(err, agentcore.RuntimeBridgeTransportClosed) {
		t.Fatalf("closed connection reused: %v", err)
	}
}

func TestConnectionSessionConfigIsAnIsolatedCopy(t *testing.T) {
	runner := &testRunner{run: func(context.Context, agentcore.SendCommand, Emit) (agentcore.TurnResultEvent, error) {
		return completed("done"), nil
	}}
	connection, err := NewConnection(Config{Engine: EngineFunc(func(_ context.Context, _ agentcore.SessionSpec, config agentcore.SessionConfig) (Runner, error) {
		config.ConfigOptions["key"] = "changed"
		return runner, nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.Close() })
	config := agentcore.SessionConfig{WorkspaceRef: "workspace", ConfigOptions: map[string]string{"key": "original"}}
	if _, err := connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "session"}, config); err != nil {
		t.Fatal(err)
	}
	if config.ConfigOptions["key"] != "original" {
		t.Fatal("engine changed host session config")
	}
}
