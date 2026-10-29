package agentkernel

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestToolApprovalDeliveryFailureCannotDispatch(t *testing.T) {
	var calls atomic.Int32
	runner, input := openRunner(t, toolThenAnswerModel(), func(c *Config, _ *agentcore.RuntimeRequest) {
		c.ApprovalToolIDs = []string{"knowledge.search"}
		c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(context.Context, runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
			calls.Add(1)
			return runtimecore.RuntimeToolCallResult{}, nil
		})
	})
	_, err := runner.Run(context.Background(), input, func(event agentcore.SessionEvent) error {
		if event.Kind == agentcore.EventPermission {
			return ErrCapacity
		}
		return nil
	})
	if !errors.Is(err, ErrCapacity) || calls.Load() != 0 || len(runner.PendingPermissionRequests()) != 0 {
		t.Fatalf("undelivered approval dispatched: %v", err)
	}
}
