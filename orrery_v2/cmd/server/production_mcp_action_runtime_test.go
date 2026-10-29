package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestProductionRuntimeToolDispatcherDoesNotRetryUnrecordedMCPOutcome(t *testing.T) {
	gateway := &productionRuntimeTestMCPGateway{err: fmt.Errorf("%w: database unavailable", mcptransport.ErrToolCallOutcomeUnrecorded)}
	dispatcher := productionRuntimeToolDispatcher{mcpGateway: gateway}

	result, err := dispatcher.CallRuntimeTool(context.Background(), runtimecore.RuntimeToolCallRequest{
		TenantID: "tenant-yunji",
		Name:     "mcp_yunji_candidate_advance",
		AgentID:  "agent-yunji",
		Input:    map[string]any{"candidateId": "cand-1"},
		ToolBindings: []agentcore.RuntimeToolBinding{{
			ToolID:             "mcp.yunji.candidate:advance",
			Alias:              "mcp_yunji_candidate_advance",
			Source:             "mcp_server",
			ServerToolID:       "mcp.yunji",
			DownstreamToolName: "candidate:advance",
		}},
	})
	if err != nil {
		t.Fatalf("CallRuntimeTool: %v", err)
	}
	if result.Output["status"] != "failed" || result.Output["error_code"] != "tool_outcome_unrecorded" || result.Output["retryable"] != false || !result.OutcomeUncertain {
		t.Fatalf("failure result = %+v", result.Output)
	}
	if strings.Contains(fmt.Sprint(result.Output["error"]), "database unavailable") || !strings.Contains(fmt.Sprint(result.Output["recovery_hint"]), "Do not call") {
		t.Fatalf("unrecorded outcome should be sanitized and require reconciliation: %+v", result.Output)
	}
}
