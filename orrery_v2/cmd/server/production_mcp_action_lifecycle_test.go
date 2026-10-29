package main

import (
	"context"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/platform/idgen"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
	mcptransport "orrery_v2/internal/transport/mcp"
)

func TestProductionMCPActionLifecyclePersistsCompletedRunAndWriteback(t *testing.T) {
	ctx := context.Background()
	repository := actioncontract.NewMemoryRepository()
	service := actioncontract.NewService(repository, actioncontract.WithClock(func() time.Time {
		return time.Date(2026, 8, 6, 10, 0, 0, 0, time.UTC)
	}))
	if err := actioncontract.InstallDefaultActionTypes(ctx, service); err != nil {
		t.Fatal(err)
	}
	lifecycle := newProductionMCPActionLifecycle(service, idgen.NewDeterministic("mcp-action"))
	attempt, err := lifecycle.StartToolCall(ctx, mcptransport.ToolCallLifecycleStart{
		TenantID: "tenant-a", RunID: "agent-run-1", ToolID: "mcp.github.search_issues", PrincipalID: "human-1", AgentID: "agent-1",
		Source: mcptransport.ToolSourceExternalMCP, ServerToolID: "mcp.github", DownstreamToolName: "search_issues",
		CorrelationID: "corr-1", Arguments: map[string]any{"query": "is:open", "idempotency_key": "idem-1"},
		ResponsibilityContext: &agentcore.ResponsibilityContext{
			ContextID: "context-1", ResponsibilityID: "responsibility-1", ResponsibilityVersion: 2,
			PolicyID: "policy-1", PolicyVersion: 4, AgentProfileID: "agent-1",
		},
	})
	if err != nil {
		t.Fatalf("StartToolCall: %v", err)
	}
	if err := attempt.CompleteToolCall(ctx, mcptransport.CallToolResult{Output: map[string]any{"issues": []any{"ORR-1"}}}); err != nil {
		t.Fatalf("CompleteToolCall: %v", err)
	}

	run, err := repository.GetActionRun(ctx, "action-run:mcp-action-000001")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != actioncontract.RunStatusCompleted || run.ActionTypeID != "mcp.tool.call" || run.Actor.Kind != actioncontract.SubjectAgent || run.Actor.ID != "agent-1" || run.EffectiveUser.ID != "human-1" {
		t.Fatalf("completed action run = %+v", run)
	}
	if run.ProviderCallRef.ID != "mcp-call:mcp-action-000001" || run.ResultRef.Kind != "mcp_tool_result" || run.ArgsHash == "" {
		t.Fatalf("completed action refs = %+v", run)
	}
	wantTargets := []actioncontract.ObjectRef{
		{Kind: "tool", ID: "mcp.github.search_issues"},
		{Kind: "mcp_server", ID: "mcp.github"},
		{Kind: "agent_run", ID: "agent-run:agent-run-1"},
		{Kind: "responsibility_assignment", ID: "responsibility-1"},
		{Kind: "responsibility_context", ID: "context-1"},
		{Kind: "responsibility_policy", ID: "policy-1:v4"},
	}
	if len(run.ObjectRefs) != len(wantTargets) {
		t.Fatalf("completed action targets = %+v", run.ObjectRefs)
	}
	for index, want := range wantTargets {
		if run.ObjectRefs[index] != want {
			t.Fatalf("completed action target[%d] = %+v, want %+v", index, run.ObjectRefs[index], want)
		}
	}
	if run.Purpose != "responsibility_mcp_tool_execution" {
		t.Fatalf("completed action purpose = %q", run.Purpose)
	}
	receipts, err := repository.ListWritebackReceipts(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || receipts[0].ID != "writeback-receipt:mcp-action-000001" || receipts[0].TargetSystem != "mcp.github" || receipts[0].IdempotencyKey != "idem-1" || receipts[0].AfterHash != run.ResultRef.ID {
		t.Fatalf("writeback receipts = %+v", receipts)
	}
}

func TestProductionMCPActionLifecyclePersistsSanitizedFailure(t *testing.T) {
	ctx := context.Background()
	repository := actioncontract.NewMemoryRepository()
	service := actioncontract.NewService(repository)
	if err := actioncontract.InstallDefaultActionTypes(ctx, service); err != nil {
		t.Fatal(err)
	}
	lifecycle := newProductionMCPActionLifecycle(service, idgen.NewDeterministic("mcp-failure"))
	attempt, err := lifecycle.StartToolCall(ctx, mcptransport.ToolCallLifecycleStart{
		ToolID: "unknown.tool", PrincipalID: "gateway-service", Arguments: map[string]any{"secret": "not-persisted"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := attempt.FailToolCall(ctx, mcptransport.ToolCallFailure{Code: "permission_denied", Message: "MCP tool call was denied"}); err != nil {
		t.Fatal(err)
	}
	run, err := repository.GetActionRun(ctx, "action-run:mcp-failure-000001")
	if err != nil {
		t.Fatal(err)
	}
	if run.TenantID != "system" || run.Status != actioncontract.RunStatusFailed || run.ErrorCode != "permission_denied" || run.ErrorMessage != "MCP tool call was denied" || run.CompletedAt == nil {
		t.Fatalf("failed action run = %+v", run)
	}
}

func TestProductionMCPActionLifecycleClosesRunForInvalidToolResult(t *testing.T) {
	ctx := context.Background()
	repository := actioncontract.NewMemoryRepository()
	service := actioncontract.NewService(repository)
	if err := actioncontract.InstallDefaultActionTypes(ctx, service); err != nil {
		t.Fatal(err)
	}
	lifecycle := newProductionMCPActionLifecycle(service, idgen.NewDeterministic("invalid-result"))
	attempt, err := lifecycle.StartToolCall(ctx, mcptransport.ToolCallLifecycleStart{ToolID: "tool-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := attempt.CompleteToolCall(ctx, mcptransport.CallToolResult{Output: map[string]any{"invalid": make(chan int)}}); err == nil {
		t.Fatal("CompleteToolCall accepted a non-JSON tool result")
	}
	run, err := repository.GetActionRun(ctx, "action-run:invalid-result-000001")
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != actioncontract.RunStatusFailed || run.ErrorCode != "invalid_tool_result" {
		t.Fatalf("closed action run = %+v", run)
	}
}

func TestProductionMCPGatewayWiresActionLifecycleAroundExecution(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	for _, definition := range toolstd.Definitions(time.Date(2026, 8, 6, 9, 0, 0, 0, time.UTC)) {
		if err := registry.Register(ctx, definition); err != nil {
			t.Fatalf("register %s: %v", definition.ID, err)
		}
	}
	repository := actioncontract.NewMemoryRepository()
	service := actioncontract.NewService(repository)
	if err := actioncontract.InstallDefaultActionTypes(ctx, service); err != nil {
		t.Fatal(err)
	}
	gateway := newProductionMCPGateway(productionMCPGatewayOptions{
		Registry:          registry,
		SchedulerExecutor: &serverTestSchedulerExecutor{},
		Lifecycle:         newProductionMCPActionLifecycle(service, idgen.NewDeterministic("wired-action")),
	})

	_, err := gateway.CallTool(ctx, mcptransport.CallToolRequest{
		TenantID: "tenant-a", ToolID: string(toolstd.ScheduledTaskList), PrincipalID: "scheduler-service",
		Permissions: []string{toolstd.PermissionSchedulerRead}, Arguments: map[string]any{"limit": 5},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	page, err := repository.ListActionRuns(ctx, actioncontract.ActionRunQuery{TenantID: "tenant-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Runs) != 1 || page.Runs[0].Status != actioncontract.RunStatusCompleted {
		t.Fatalf("action runs = %+v", page.Runs)
	}
	receipts, err := repository.ListWritebackReceipts(ctx, page.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts) != 1 || receipts[0].TargetSystem != "orrery" {
		t.Fatalf("writeback receipts = %+v", receipts)
	}
}

func TestProductionMCPGatewayFailsClosedWhenActionLifecycleCannotStart(t *testing.T) {
	ctx := context.Background()
	registry := toolres.NewMemoryRegistry()
	for _, definition := range toolstd.Definitions(time.Now().UTC()) {
		if err := registry.Register(ctx, definition); err != nil {
			t.Fatal(err)
		}
	}
	executor := &serverTestSchedulerExecutor{}
	gateway := newProductionMCPGateway(productionMCPGatewayOptions{
		Registry: registry, SchedulerExecutor: executor,
		Lifecycle: newProductionMCPActionLifecycle(nil, idgen.NewDeterministic("missing-service")),
	})
	_, err := gateway.CallTool(ctx, mcptransport.CallToolRequest{
		ToolID: string(toolstd.ScheduledTaskList), Permissions: []string{toolstd.PermissionSchedulerRead},
	})
	if err == nil || !strings.Contains(err.Error(), "dependencies are required") {
		t.Fatalf("CallTool error = %v, want lifecycle dependency failure", err)
	}
}
