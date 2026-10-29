package kernelcompat

import (
	"context"
	agentcore "orrery_v2/internal/agent/core"
	"testing"
	"time"
)

type budgetRunner struct {
	testRunner
	timeout time.Duration
}

func (r *budgetRunner) TurnTimeout() time.Duration { return r.timeout }

func TestHostTaskBudgetIsBoundedAndRetainsDefaultForOrdinaryRuns(t *testing.T) {
	for _, budget := range []time.Duration{0, 30 * time.Second, 90 * time.Minute, 3 * time.Hour} {
		connection, err := NewConnection(Config{MaxTurnTimeout: 90 * time.Minute, Engine: EngineFunc(func(context.Context, agentcore.SessionSpec, agentcore.SessionConfig) (Runner, error) {
			return &budgetRunner{timeout: budget}, nil
		})})
		if err != nil {
			t.Fatal(err)
		}
		backend, err := connection.OpenSession(context.Background(), agentcore.SessionSpec{Mode: agentcore.SessionOpenFresh, SessionID: "budget"}, agentcore.SessionConfig{WorkspaceRef: "host-workspace"})
		if err != nil {
			t.Fatal(err)
		}
		want := min(budget, 90*time.Minute)
		if budget == 0 {
			want = 2 * time.Minute
		}
		if backend.(*Session).config.TurnTimeout != want {
			t.Fatal("host task budget ignored or extended")
		}
		_ = connection.Close()
	}
}
