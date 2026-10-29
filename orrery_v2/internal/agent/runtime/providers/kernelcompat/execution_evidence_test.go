package kernelcompat

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

type evidenceRunner struct{ budgetRunner }

func (*evidenceRunner) RuntimeExecutionSnapshot() agentcore.RuntimeExecutionSnapshot {
	return agentcore.RuntimeExecutionSnapshot{Engine: "orrery-agentcore"}
}

func TestRuntimeExecutionCapturedAtDispatchIncludingPrewarmAndFailure(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "capture-failure"}[fail], func(t *testing.T) {
			observed, calls := 0, 0
			r := &evidenceRunner{budgetRunner: budgetRunner{timeout: 3 * time.Hour}}
			r.run = func(context.Context, agentcore.SendCommand, Emit) (agentcore.TurnResultEvent, error) {
				calls++
				if observed != 1 {
					t.Error("inference before capture")
				}
				return completed("answer"), nil
			}
			connection, err := NewConnection(Config{Engine: EngineFunc(func(context.Context, agentcore.SessionSpec, agentcore.SessionConfig) (Runner, error) { return r, nil }), MaxTurnTimeout: 90 * time.Minute})
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			// Opening with no observer simulates the production prewarm path.
			backend, err := connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "evidence"}, agentcore.SessionConfig{WorkspaceRef: "workspace"})
			if err != nil {
				t.Fatal(err)
			}
			ctx := agentcore.WithRuntimeExecutionObserver(context.Background(), func(_ context.Context, s agentcore.RuntimeExecutionSnapshot) error {
				observed++
				if s.TransportTimeoutNS != int64(90*time.Minute) || s.Policy.MaxTransportTimeoutNS != int64(90*time.Minute) {
					t.Error("transport clamp missing")
				}
				if fail {
					return errors.New("private capture error")
				}
				return nil
			})
			currentCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			current := &turn{generation: 1, ctx: currentCtx, cancel: cancel, stopLifetime: func() bool { return false }, stopExpiry: func() bool { return false }, tools: map[string]bool{}, permissions: map[string]bool{}}
			s := backend.(*Session)
			s.active = current
			s.run(current, agentcore.SendCommand{})
			if observed != 1 || (fail && calls != 0) || (!fail && calls != 1) {
				t.Fatalf("observed=%d calls=%d", observed, calls)
			}
		})
	}
}
