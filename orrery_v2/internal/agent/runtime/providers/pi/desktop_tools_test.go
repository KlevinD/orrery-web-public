package pi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"strings"
	"testing"
)

func TestPiDesktopToolPreservesCallIdentityAndUncertainWrite(t *testing.T) {
	var received runtimecore.RuntimeToolCallRequest
	dispatcher := runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
		received = call
		return runtimecore.RuntimeToolCallResult{Output: map[string]any{"status": "failed", "code": "desktop_timeout"}, OutcomeUncertain: true}, nil
	})
	server := newBridgeServer(context.Background(), dispatcher, nil, runtimecore.ExecOptions{TenantID: "tenant-test", RunID: "run-test", AgentID: "agent-test", ExecutionPrincipal: agentcore.ExecutionPrincipal{ID: "user-test", Kind: agentcore.ExecutionPrincipalUser, TenantID: "tenant-test"}, ToolBindings: []agentcore.RuntimeToolBinding{{ToolID: "desktop_write_excel", InputSchema: map[string]any{"type": "object"}}}})
	request := httptest.NewRequest(http.MethodPost, "/tool-call", strings.NewReader(`{"tool_call_id":"call-original","tool":"desktop_write_excel","input":{}}`))
	response := httptest.NewRecorder()
	server.ServeHTTP(response, request)
	var body struct {
		Output map[string]any `json:"output"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	if response.Code != 200 || received.CallID != "call-original" || received.ExecutionPrincipal.ID != "user-test" || body.Output["outcome_uncertain"] != true || body.Output["retryable"] != false {
		t.Fatalf("identity/uncertainty missing: call=%#v body=%s", received, response.Body.String())
	}
}
