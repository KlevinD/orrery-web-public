package kernelcompat

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type resumableTestEngine struct{ runner Runner }

func (e resumableTestEngine) SupportsResume() bool { return true }
func (e resumableTestEngine) OpenSession(context.Context, agentcore.SessionSpec, agentcore.SessionConfig) (Runner, error) {
	return e.runner, nil
}

type timedTestRunner struct {
	testRunner
	timeout time.Duration
}

func (r *timedTestRunner) TurnTimeout() time.Duration { return r.timeout }
func (r *timedTestRunner) BackendSessionID() string   { return "scoped-checkpoint" }

func TestResumeCapabilityBackendIDAndTrustedTimeoutCeiling(t *testing.T) {
	for _, timeout := range []time.Duration{0, 15 * time.Minute, 45 * time.Minute} {
		t.Run(timeout.String(), func(t *testing.T) {
			runner := &timedTestRunner{timeout: timeout}
			connection, err := NewConnection(Config{Engine: resumableTestEngine{runner: runner}, MaxTurnTimeout: 20 * time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			if !connection.Capabilities(context.Background()).SupportsResume {
				t.Fatal("resume capability hidden")
			}
			backend, err := connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenResume, SessionID: "restored", BackendSessionID: "scoped-checkpoint"}, agentcore.SessionConfig{WorkspaceRef: "trusted-workspace"})
			if err != nil {
				t.Fatal(err)
			}
			session := backend.(*Session)
			want := min(timeout, 20*time.Minute)
			if timeout == 0 {
				want = 2 * time.Minute
			}
			if session.config.TurnTimeout != want || session.BackendSessionID() != "scoped-checkpoint" || !session.Capabilities().SupportsResume {
				t.Fatalf("session integration failed: timeout=%v", session.config.TurnTimeout)
			}
		})
	}
}
