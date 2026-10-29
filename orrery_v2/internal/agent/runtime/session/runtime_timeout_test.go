package session

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestFinishResponseClassifiesDetachedDeadlineAsTimeout(t *testing.T) {
	runtime := &Runtime{records: map[agentcore.AgentRunID]*runtimeRecord{"run-1": {}}}
	response := runtime.finishResponse("run-1", agentcore.RuntimeResponse{
		RunID: "run-1", Status: agentcore.RuntimeStatusFailed,
		Error: agentcore.RuntimeFailure{Code: "runtime_detached", Message: "runtime transport detached"},
	}, true)

	if response.Status != agentcore.RuntimeStatusTimeout || response.Error.Code != "runtime_timeout" || !response.Error.Retryable {
		t.Fatalf("timeout response = %+v", response)
	}
	if stored := runtime.records["run-1"].response; stored.Status != agentcore.RuntimeStatusTimeout || stored.Error.Code != "runtime_timeout" {
		t.Fatalf("stored timeout response = %+v", stored)
	}
}
