package agentkernel

import (
	"context"
	"errors"
	"orrery_v2/internal/agent/llm"
	"sync/atomic"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func TestExplicitToolScopesReachDispatcherOnlyWithinTaskGrants(t *testing.T) {
	for _, mode := range []string{"allowed", "allowed-hierarchy", "task-denied", "policy-denied", "forbidden", "selector-denied", "missing-grants", "empty-grant"} {
		t.Run(mode, func(t *testing.T) {
			var dispatched atomic.Int32
			expectedScope := "knowledge.read"
			if mode == "allowed-hierarchy" {
				expectedScope = "knowledge.manage"
			}
			runner, input := openRunner(t, parallelReply(toolCall("scoped", `{"kb":"public"}`)), func(c *Config, req *agentcore.RuntimeRequest) {
				c.ExplicitToolScopeFence = true
				req.ToolBindings[0].GrantedPermissions = []string{"knowledge.read"}
				scope := &agentcore.ResponsibilityContext{RequireExplicitToolScopes: true, TaskPermissionScopes: []string{"knowledge.read"}, PolicyToolScopes: []string{"knowledge.*"}, AllowedTools: []string{"knowledge.search"}}
				switch mode {
				case "allowed-hierarchy":
					scope.TaskPermissionScopes = []string{expectedScope}
					scope.PolicyToolScopes = []string{expectedScope}
					scope.AllowedTools = []string{expectedScope}
				case "task-denied":
					scope.TaskPermissionScopes = []string{"memory.read"}
				case "policy-denied":
					scope.PolicyToolScopes = []string{"knowledge.write"}
				case "forbidden":
					scope.ForbiddenActions = []string{"knowledge.*"}
				case "selector-denied":
					scope.AllowedTools = []string{"memory.search"}
				case "missing-grants":
					req.ToolBindings[0].GrantedPermissions = nil
				case "empty-grant":
					req.ToolBindings[0].GrantedPermissions = []string{""}
				}
				req.ContextPack.ResponsibilityContext = scope
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					dispatched.Add(1)
					if call.ResponsibilityContext == nil || call.ResponsibilityContext.TaskPermissionScopes[0] != expectedScope {
						t.Error("task authority not preserved")
					}
					call.ResponsibilityContext.TaskPermissionScopes[0] = "*"
					return runtimecore.RuntimeToolCallResult{Output: map[string]any{"ok": true}}, nil
				})
			})
			_, err := runner.Run(context.Background(), input, discard)
			if mode == "allowed" || mode == "allowed-hierarchy" {
				if err != nil || dispatched.Load() != 1 || runner.request.ContextPack.ResponsibilityContext.TaskPermissionScopes[0] != expectedScope {
					t.Fatalf("scoped run: %v calls=%d", err, dispatched.Load())
				}
			} else if !errors.Is(err, ErrToolDenied) || dispatched.Load() != 0 {
				t.Fatalf("task grant bypass: %v calls=%d", err, dispatched.Load())
			}
		})
	}
}

func TestUnrecordedToolOutcomeCannotContinueUnderAnotherCallID(t *testing.T) {
	for _, parallel := range []bool{false, true} {
		t.Run(map[bool]string{false: "serial", true: "parallel"}[parallel], func(t *testing.T) {
			calls := []llm.ToolCall{toolCall("first", `{"kb":"public"}`)}
			if parallel {
				calls = append(calls, toolCall("sibling", `{"kb":"public"}`))
			}
			m := parallelReply(calls...)
			var journal *SQLToolJournal
			runner, input := openRunner(t, m, func(c *Config, _ *agentcore.RuntimeRequest) {
				checkpointConfig(t, c)
				journal = c.ToolJournal.(*SQLToolJournal)
				if parallel {
					c.MaxParallelTools, c.ParallelToolIDs = 2, []string{"knowledge.search"}
				}
				c.Tools = runtimecore.RuntimeToolDispatcherFunc(func(_ context.Context, call runtimecore.RuntimeToolCallRequest) (runtimecore.RuntimeToolCallResult, error) {
					return runtimecore.RuntimeToolCallResult{OutcomeUncertain: call.CallID == "first", Output: map[string]any{"status": "failed", "retryable": false, "error_code": "tool_outcome_unrecorded"}}, nil
				})
			})
			if _, err := runner.Run(context.Background(), input, discard); !errors.Is(err, ErrTool) || m.calls.Load() != 1 || runner.checkpoint.Phase != CheckpointToolsPending {
				t.Fatalf("unrecorded outcome continued: %v model=%d phase=%s", err, m.calls.Load(), runner.checkpoint.Phase)
			}
			key := ToolExecutionKey{TenantID: runner.request.TenantID, RunID: runner.request.RunID, CallID: "first"}
			record, found, err := journal.Get(context.Background(), key)
			if err != nil || !found || record.State != ToolExecutionUnknown || record.Reason != "outcome_unrecorded" {
				t.Fatalf("uncertain receipt lost: %+v %v", record, err)
			}
			key.CallID = "replacement"
			if _, err := journal.Begin(context.Background(), ToolExecution{ToolExecutionKey: key, AgentID: runner.request.AgentID, ToolID: record.ToolID, Fingerprint: record.Fingerprint}); !errors.Is(err, ErrRunUnresolved) {
				t.Fatalf("replacement call escaped: %v", err)
			}
		})
	}
}
