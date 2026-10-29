package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	contextgraph "orrery_v2/internal/app/contextgraph"
	contextconsumer "orrery_v2/internal/app/contextgraph/consumer"
	schedulerapp "orrery_v2/internal/app/scheduler"
	skillres "orrery_v2/internal/resource/skill"
	toolres "orrery_v2/internal/resource/tool"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestGatewayPermissionsUseCommonReadSemantics(t *testing.T) {
	required := []toolres.PermissionDeclaration{{Scope: toolstd.PermissionKnowledgeRead, Required: true}}
	if err := checkGatewayPermissions(required, []string{" ORRERY:KNOWLEDGE.MANAGE "}); err != nil {
		t.Fatalf("manage permission should include read: %v", err)
	}
	graph := []toolres.PermissionDeclaration{{Scope: toolstd.PermissionEnterpriseGraphRead, Required: true}}
	if err := checkGatewayPermissions(graph, []string{"ontology.read"}); err != nil {
		t.Fatalf("ontology read should include enterprise graph read: %v", err)
	}
	if err := checkGatewayPermissions(graph, []string{"operations.read"}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("legacy operations read should not call graph tools: %v", err)
	}
	if !responsibilityScopeAllows([]string{"ontology.read"}, toolstd.PermissionEnterpriseGraphRead) {
		t.Fatal("responsibility scopes should use the common ontology read semantics")
	}
}

func TestToolRouterRoutesSchedulerToolsThroughExecutor(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	scheduler := &recordingSchedulerExecutor{
		result: schedulerapp.ToolExecutionResult{Output: map[string]any{"total": 0}},
	}
	audit := &recordingAuditRecorder{}
	router := NewToolRouter(ToolRouterOptions{
		Registry:          registry,
		SchedulerExecutor: scheduler,
		AuditRecorder:     audit,
		Now:               fixedGatewayTestNow,
	})

	result, err := router.CallTool(ctx, CallToolRequest{
		ToolID:      string(toolstd.ScheduledTaskList),
		PrincipalID: "agent-ops",
		ProfileID:   "profile-ops",
		Permissions: []string{toolstd.PermissionSchedulerRead},
		Arguments:   map[string]any{"limit": 25},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if scheduler.calledToolID != string(toolstd.ScheduledTaskList) || scheduler.viewer.ViewerID != "agent-ops" || scheduler.viewer.ProfileID != "profile-ops" {
		t.Fatalf("scheduler request = tool:%q viewer:%+v", scheduler.calledToolID, scheduler.viewer)
	}
	if len(scheduler.viewer.PermissionScope) != 1 || scheduler.viewer.PermissionScope[0] != toolstd.PermissionSchedulerRead {
		t.Fatalf("scheduler viewer permissions = %+v", scheduler.viewer.PermissionScope)
	}
	if scheduler.arguments["limit"] != 25 {
		t.Fatalf("scheduler arguments = %+v", scheduler.arguments)
	}
	if result.Output["total"] != 0 {
		t.Fatalf("result = %+v", result.Output)
	}
	if len(audit.events) != 1 || audit.events[0].Outcome != AuditOutcomeAllowed {
		t.Fatalf("audit events = %+v", audit.events)
	}
}

func TestToolRouterRoutesEvaluationReviewThroughExecutor(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	evaluation := &recordingEvaluationToolExecutor{
		result: EvaluationToolResult{Output: map[string]any{"review_count": 1}},
	}
	router := NewToolRouter(ToolRouterOptions{
		Registry:           registry,
		EvaluationExecutor: evaluation,
	})

	result, err := router.CallTool(ctx, CallToolRequest{
		ToolID:      string(toolstd.EvaluationReview),
		PrincipalID: "review-agent",
		AgentID:     "review-agent",
		Permissions: []string{toolstd.PermissionEvaluationReview},
		Arguments: map[string]any{
			"mode":                 "agent",
			"agent_id":             "profile-agent-1",
			"summary":              "结论：交付稳定。",
			"estimated_human_days": 1.5,
			"window_days":          7,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if evaluation.calledToolID != string(toolstd.EvaluationReview) ||
		evaluation.request.PrincipalID != "review-agent" ||
		evaluation.request.AgentID != "review-agent" ||
		evaluation.request.Mode != "agent" ||
		evaluation.request.TargetAgentID != "profile-agent-1" ||
		evaluation.request.Summary != "结论：交付稳定。" ||
		evaluation.request.EstimatedHumanDays != 1.5 ||
		evaluation.request.WindowDays != 7 {
		t.Fatalf("evaluation request = tool:%q req:%+v", evaluation.calledToolID, evaluation.request)
	}
	if result.Output["review_count"] != 1 {
		t.Fatalf("result = %+v", result.Output)
	}
}

func TestToolRouterRoutesEmailSendThroughExecutor(t *testing.T) {
	executor := &recordingEmailToolExecutor{result: EmailToolResult{Output: map[string]any{"status": "sent"}}}
	router := NewToolRouter(ToolRouterOptions{Registry: newGatewayTestRegistry(t), EmailExecutor: executor})

	result, err := router.CallTool(context.Background(), CallToolRequest{
		TenantID: "tenant-a", ToolID: string(toolstd.EmailSend), PrincipalID: "agent-1", AgentID: "agent-1", ProfileID: "profile-1",
		Permissions: []string{toolstd.PermissionEmailSend},
		Arguments: map[string]any{
			"to": []any{"user@example.test"}, "cc": []any{"reviewer@example.test"}, "subject": "Status",
			"text": " Plain body\n", "html": "<p>HTML body</p>", "idempotency_key": "mail-1",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if result.Output["status"] != "sent" || executor.calledToolID != string(toolstd.EmailSend) || executor.request.ProfileID != "profile-1" || executor.request.TenantID != "tenant-a" || executor.request.Text != " Plain body\n" || executor.request.IdempotencyKey != "mail-1" || len(executor.request.To) != 1 || len(executor.request.Cc) != 1 {
		t.Fatalf("email result=%+v tool=%q request=%+v", result.Output, executor.calledToolID, executor.request)
	}
}

func TestToolRouterRoutesMemoryDiaryWriteThroughExecutor(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	diary := &recordingMemoryDiaryToolExecutor{
		result: MemoryDiaryToolResult{Output: map[string]any{"written_count": 1}},
	}
	router := NewToolRouter(ToolRouterOptions{
		Registry:            registry,
		MemoryDiaryExecutor: diary,
	})

	result, err := router.CallTool(ctx, CallToolRequest{
		ToolID:      string(toolstd.MemoryDiaryWrite),
		PrincipalID: "diary-agent",
		AgentID:     "diary-agent",
		Permissions: []string{toolstd.PermissionMemoryDiaryWrite},
		Arguments: map[string]any{
			"mode":            "agent",
			"agent_id":        "profile-agent-1",
			"date":            "2026-06-16",
			"force":           true,
			"limit":           20,
			"idempotency_key": "diary-2026-06-16",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if diary.calledToolID != string(toolstd.MemoryDiaryWrite) ||
		diary.request.PrincipalID != "diary-agent" ||
		diary.request.AgentID != "diary-agent" ||
		diary.request.Mode != "agent" ||
		diary.request.TargetAgentID != "profile-agent-1" ||
		diary.request.Date != "2026-06-16" ||
		!diary.request.Force ||
		diary.request.Limit != 20 ||
		diary.request.IdempotencyKey != "diary-2026-06-16" {
		t.Fatalf("memory diary request = tool:%q req:%+v", diary.calledToolID, diary.request)
	}
	if result.Output["written_count"] != 1 {
		t.Fatalf("result = %+v", result.Output)
	}
}

func TestToolRouterRoutesMemorySearchThroughExecutor(t *testing.T) {
	memory := &recordingMemorySearchToolExecutor{result: MemorySearchToolResult{Output: map[string]any{"personal_digest": map[string]any{"summary": "Use concise plans."}}}}
	router := NewToolRouter(ToolRouterOptions{Registry: newGatewayTestRegistry(t), MemorySearchExecutor: memory})

	result, err := router.CallTool(context.Background(), CallToolRequest{
		TenantID:       "tenant-a",
		ToolID:         string(toolstd.MemorySearch),
		PrincipalID:    "user-1",
		AgentID:        "lead-agent",
		LogicalAgentID: "personal-assistant-owner-1",
		Permissions:    []string{toolstd.PermissionMemoryRead},
		Arguments:      map[string]any{"query": "How should I structure plans?"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if memory.request.TenantID != "tenant-a" || memory.request.PrincipalID != "user-1" ||
		memory.request.AgentID != "lead-agent" || memory.request.LogicalAgentID != "personal-assistant-owner-1" ||
		memory.request.Query != "How should I structure plans?" {
		t.Fatalf("memory search request = %+v", memory.request)
	}
	if result.Output["personal_digest"] == nil {
		t.Fatalf("memory search result = %+v", result.Output)
	}
}

func TestToolRouterRoutesTenantScopedDiaryAppendThroughExecutor(t *testing.T) {
	ctx := context.Background()
	diary := &recordingMemoryDiaryToolExecutor{result: MemoryDiaryToolResult{Output: map[string]any{"status": "written"}}}
	router := NewToolRouter(ToolRouterOptions{Registry: newGatewayTestRegistry(t), MemoryDiaryExecutor: diary})

	_, err := router.CallTool(ctx, CallToolRequest{
		TenantID:    "tenant-a",
		ToolID:      string(toolstd.MemoryDiaryAppend),
		PrincipalID: "agent-1",
		AgentID:     "agent-1",
		Maintenance: true,
		Permissions: []string{toolstd.PermissionMemoryDiaryWrite},
		Arguments: map[string]any{
			"path":             "memory/2026-07-17.md",
			"content":          "- Durable result.",
			"date":             "2026-07-17",
			"session_id":       "session-1",
			"context_revision": "revision-1",
			"trigger":          "run_terminal",
			"idempotency_key":  "append-1",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if diary.calledToolID != string(toolstd.MemoryDiaryAppend) ||
		diary.request.TenantID != "tenant-a" || diary.request.AgentID != "agent-1" ||
		diary.request.Path != "memory/2026-07-17.md" || diary.request.Content != "- Durable result." ||
		diary.request.SessionID != "session-1" || diary.request.ContextRevision != "revision-1" ||
		diary.request.Trigger != "run_terminal" {
		t.Fatalf("diary append request = tool:%q req:%+v", diary.calledToolID, diary.request)
	}
}

func TestToolRouterRejectsDiaryMaintenanceWithoutTrustedHostFlag(t *testing.T) {
	diary := &recordingMemoryDiaryToolExecutor{}
	router := NewToolRouter(ToolRouterOptions{Registry: newGatewayTestRegistry(t), MemoryDiaryExecutor: diary})
	req := CallToolRequest{
		TenantID: "tenant-a", ToolID: string(toolstd.MemoryDiaryAppend), PrincipalID: "agent-1", AgentID: "agent-1",
		Permissions: []string{toolstd.PermissionMemoryDiaryWrite},
	}
	if _, err := router.CallTool(context.Background(), req); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("ordinary maintenance tool error = %v, want ErrPermissionDenied", err)
	}
	req.Maintenance = true
	req.PrincipalID = "agent-2"
	if _, err := router.CallTool(context.Background(), req); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("cross-Agent maintenance tool error = %v, want ErrPermissionDenied", err)
	}
	if diary.calledToolID != "" {
		t.Fatalf("maintenance executor was called for denied request: %q", diary.calledToolID)
	}
}

func TestToolRouterAutoplanDiaryMaintenanceRequiresDiaryTaskScope(t *testing.T) {
	diary := &recordingMemoryDiaryToolExecutor{result: MemoryDiaryToolResult{Output: map[string]any{"status": "written"}}}
	router := NewToolRouter(ToolRouterOptions{Registry: newGatewayTestRegistry(t), MemoryDiaryExecutor: diary})
	newRequest := func(scopes []string) CallToolRequest {
		return CallToolRequest{
			RunID: "run-autoplan", TenantID: "tenant-a", ToolID: string(toolstd.MemoryDiaryAppend), PrincipalID: "agent-1", AgentID: "agent-1", ProfileID: "agent-1",
			Maintenance: true, Permissions: []string{toolstd.PermissionMemoryDiaryWrite},
			ResponsibilityContext: &agentcore.ResponsibilityContext{
				ContextID: "context-autoplan", ResponsibilityID: "responsibility-autoplan", ResponsibilityVersion: 1,
				PolicyID: "policy-autoplan", PolicyVersion: 1, AgentProfileID: "agent-1",
				AllowedTools: []string{toolstd.PermissionMemoryDiaryWrite}, PolicyToolScopes: []string{toolstd.PermissionMemoryDiaryWrite},
				RequireExplicitToolScopes: true, TaskPermissionScopes: scopes,
			},
			Arguments: map[string]any{
				"path": "memory/2026-08-14.md", "content": "- durable result", "date": "2026-08-14",
				"session_id": "session-1", "context_revision": "revision-1", "trigger": "run_terminal", "idempotency_key": "append-autoplan",
			},
		}
	}
	if _, err := router.CallTool(context.Background(), newRequest([]string{"github.read"})); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("unscoped diary maintenance error = %v, want ErrPermissionDenied", err)
	}
	if diary.calledToolID != "" {
		t.Fatalf("diary executor was called without a diary task scope: %q", diary.calledToolID)
	}
	if _, err := router.CallTool(context.Background(), newRequest([]string{toolstd.PermissionMemoryDiaryWrite})); err != nil {
		t.Fatalf("explicitly scoped diary maintenance: %v", err)
	}
	if diary.calledToolID != string(toolstd.MemoryDiaryAppend) {
		t.Fatalf("diary executor was not called for explicit scope: %q", diary.calledToolID)
	}
}

func TestToolRouterProxiesRegisteredExternalMCPServerTools(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	registerGatewayTestExternalMCP(t, ctx, registry)
	external := &recordingExternalMCPClient{
		result: CallToolResult{Output: map[string]any{"issues": []any{"IDE-1"}}},
	}
	audit := &recordingAuditRecorder{}
	router := NewToolRouter(ToolRouterOptions{
		Registry:      registry,
		ExternalMCP:   external,
		AuditRecorder: audit,
		Now:           fixedGatewayTestNow,
	})

	result, err := router.CallTool(ctx, CallToolRequest{
		TenantID:    "tenant-a",
		ToolID:      "mcp.github.search_issues",
		PrincipalID: "agent-ops",
		Permissions: []string{"github.read"},
		Arguments: map[string]any{
			"query":          "repo:orrery bug",
			"correlation_id": "corr-commerce-1",
			"secret":         "must-not-be-audited",
		},
		RequestMeta: map[string]any{"com.minvia/orrery-hil-receipt": map[string]any{"approval_id": "approval-1"}},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if external.serverID != "mcp.github" || external.toolName != "search_issues" {
		t.Fatalf("external call = server:%q tool:%q", external.serverID, external.toolName)
	}
	if external.arguments["query"] != "repo:orrery bug" {
		t.Fatalf("external args = %+v", external.arguments)
	}
	if external.requestMeta["com.minvia/orrery-hil-receipt"].(map[string]any)["approval_id"] != "approval-1" {
		t.Fatalf("external request metadata = %+v", external.requestMeta)
	}
	if result.Output["issues"] == nil {
		t.Fatalf("result = %+v", result.Output)
	}
	if len(audit.events) != 1 || audit.events[0].Source != ToolSourceExternalMCP || audit.events[0].TenantID != "tenant-a" || audit.events[0].CorrelationID != "corr-commerce-1" {
		t.Fatalf("audit events = %+v", audit.events)
	}
}

func TestToolRouterAutoplanRequiresExplicitTaskToolScopes(t *testing.T) {
	newResponsibility := func() *agentcore.ResponsibilityContext {
		return &agentcore.ResponsibilityContext{
			ContextID: "autoplan-context", ResponsibilityID: "responsibility-autoplan", ResponsibilityVersion: 1,
			PolicyID: "policy-autoplan", PolicyVersion: 1, AgentProfileID: "profile-autoplan",
			AllowedTools: []string{"github.read"}, PolicyToolScopes: []string{"github.read"},
			RequireExplicitToolScopes: true,
		}
	}
	newRequest := func(responsibility *agentcore.ResponsibilityContext) CallToolRequest {
		return CallToolRequest{
			RunID: "run-autoplan", TenantID: "tenant-a", ToolID: "mcp.github.search_issues",
			PrincipalID: "agent-autoplan", AgentID: "agent-autoplan", ProfileID: "profile-autoplan",
			Permissions: []string{"github.read"}, ResponsibilityContext: responsibility,
			Arguments: map[string]any{"query": "repo:orrery"},
		}
	}

	t.Run("default scope denies external tool", func(t *testing.T) {
		registry := newGatewayTestRegistry(t)
		registerGatewayTestExternalMCP(t, context.Background(), registry)
		external := &recordingExternalMCPClient{}
		audit := &recordingAuditRecorder{}
		router := NewToolRouter(ToolRouterOptions{Registry: registry, ExternalMCP: external, AuditRecorder: audit, Now: fixedGatewayTestNow})

		_, err := router.CallTool(context.Background(), newRequest(newResponsibility()))
		if !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("CallTool error = %v, want ErrPermissionDenied", err)
		}
		if external.toolName != "" || len(audit.events) != 1 || audit.events[0].Reason != "responsibility_permission_denied" {
			t.Fatalf("external=%+v audit=%+v", external, audit.events)
		}
	})

	t.Run("explicit scope remains subject to policy", func(t *testing.T) {
		registry := newGatewayTestRegistry(t)
		registerGatewayTestExternalMCP(t, context.Background(), registry)
		external := &recordingExternalMCPClient{result: CallToolResult{Output: map[string]any{"issues": 1}}}
		audit := &recordingAuditRecorder{}
		router := NewToolRouter(ToolRouterOptions{Registry: registry, ExternalMCP: external, AuditRecorder: audit, Now: fixedGatewayTestNow})
		responsibility := newResponsibility()
		responsibility.TaskPermissionScopes = []string{"github.read"}

		if _, err := router.CallTool(context.Background(), newRequest(responsibility)); err != nil {
			t.Fatalf("explicit scoped CallTool: %v", err)
		}
		if external.toolName != "search_issues" || len(audit.events) != 1 || audit.events[0].Outcome != AuditOutcomeAllowed {
			t.Fatalf("external=%+v audit=%+v", external, audit.events)
		}

		responsibility.PolicyToolScopes = []string{"github.write"}
		if _, err := router.CallTool(context.Background(), newRequest(responsibility)); !errors.Is(err, ErrPermissionDenied) {
			t.Fatalf("policy-gated CallTool error = %v, want ErrPermissionDenied", err)
		}
		if len(audit.events) != 2 || audit.events[1].Reason != "responsibility_permission_denied" {
			t.Fatalf("policy gate audit=%+v", audit.events)
		}
	})
}

func TestToolRouterRejectsCallsWithoutRequiredPermissionAndAudits(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	scheduler := &recordingSchedulerExecutor{}
	audit := &recordingAuditRecorder{}
	router := NewToolRouter(ToolRouterOptions{
		Registry:          registry,
		SchedulerExecutor: scheduler,
		AuditRecorder:     audit,
		Now:               fixedGatewayTestNow,
	})

	_, err := router.CallTool(ctx, CallToolRequest{
		ToolID:      string(toolstd.ScheduledTaskRunNow),
		PrincipalID: "agent-ops",
		Permissions: []string{toolstd.PermissionSchedulerRead},
		Arguments:   map[string]any{"scheduled_task_id": "task-1"},
	})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("CallTool error = %v, want ErrPermissionDenied", err)
	}
	if scheduler.calledToolID != "" {
		t.Fatalf("scheduler should not be called, got %q", scheduler.calledToolID)
	}
	if len(audit.events) != 1 || audit.events[0].Outcome != AuditOutcomeDenied ||
		audit.events[0].Reason != "permission_denied" {
		t.Fatalf("audit events = %+v", audit.events)
	}
}

func TestToolRouterAllowsOnlyIntersectionOfResponsibilityAuthorities(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	scheduler := &recordingSchedulerExecutor{result: schedulerapp.ToolExecutionResult{Output: map[string]any{"total": 0}}}
	audit := &recordingAuditRecorder{}
	router := NewToolRouter(ToolRouterOptions{Registry: registry, SchedulerExecutor: scheduler, AuditRecorder: audit, Now: fixedGatewayTestNow})
	responsibility := schedulerReadResponsibilityContext()

	_, err := router.CallTool(ctx, CallToolRequest{
		RunID: "run-1", ToolID: string(toolstd.ScheduledTaskList), AgentID: "agent-1", ProfileID: "profile-1",
		Permissions: []string{toolstd.PermissionSchedulerRead}, ResponsibilityContext: responsibility,
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if scheduler.calledToolID != string(toolstd.ScheduledTaskList) {
		t.Fatalf("scheduler tool = %q", scheduler.calledToolID)
	}
	if len(audit.events) != 1 || audit.events[0].Outcome != AuditOutcomeAllowed || audit.events[0].RunID != "run-1" || audit.events[0].ResponsibilityPolicyVersion != 4 {
		t.Fatalf("responsibility audit = %+v", audit.events)
	}
}

func TestToolRouterDeniesWhenAnyResponsibilityAuthorityExcludesTool(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*agentcore.ResponsibilityContext)
	}{
		{name: "people boundary", mutate: func(value *agentcore.ResponsibilityContext) {
			value.AllowedTools = []string{toolstd.PermissionSchedulerRun}
		}},
		{name: "forbidden action", mutate: func(value *agentcore.ResponsibilityContext) {
			value.ForbiddenActions = []string{toolstd.PermissionSchedulerRead}
		}},
		{name: "policy scope", mutate: func(value *agentcore.ResponsibilityContext) {
			value.PolicyToolScopes = []string{toolstd.PermissionSchedulerRun}
		}},
		{name: "task scope", mutate: func(value *agentcore.ResponsibilityContext) {
			value.TaskPermissionScopes = []string{toolstd.PermissionSchedulerRun}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scheduler := &recordingSchedulerExecutor{}
			audit := &recordingAuditRecorder{}
			router := NewToolRouter(ToolRouterOptions{Registry: newGatewayTestRegistry(t), SchedulerExecutor: scheduler, AuditRecorder: audit, Now: fixedGatewayTestNow})
			responsibility := schedulerReadResponsibilityContext()
			test.mutate(responsibility)

			_, err := router.CallTool(context.Background(), CallToolRequest{
				RunID: "run-1", ToolID: string(toolstd.ScheduledTaskList), AgentID: "agent-1", ProfileID: "profile-1",
				Permissions: []string{toolstd.PermissionSchedulerRead}, ResponsibilityContext: responsibility,
			})
			if !errors.Is(err, ErrPermissionDenied) {
				t.Fatalf("CallTool error = %v, want ErrPermissionDenied", err)
			}
			if scheduler.calledToolID != "" || len(audit.events) != 1 || audit.events[0].Reason != "responsibility_permission_denied" {
				t.Fatalf("scheduler=%q audit=%+v", scheduler.calledToolID, audit.events)
			}
		})
	}
}

func schedulerReadResponsibilityContext() *agentcore.ResponsibilityContext {
	return &agentcore.ResponsibilityContext{
		ContextID: "context-1", ResponsibilityID: "responsibility-1", ResponsibilityVersion: 2,
		PolicyID: "policy-1", PolicyVersion: 4, AgentProfileID: "profile-1",
		AllowedTools: []string{toolstd.PermissionSchedulerRead}, PolicyToolScopes: []string{toolstd.PermissionSchedulerRead}, TaskPermissionScopes: []string{toolstd.PermissionSchedulerRead},
	}
}

func TestToolRouterRoutesSkillToolsThroughSkillExecutor(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	executor := &recordingSkillToolExecutor{
		result: skillres.RuntimeToolResult{Output: map[string]any{"content": "# TDD\nUse red-green-refactor."}},
	}
	router := NewToolRouter(ToolRouterOptions{
		Registry:      registry,
		SkillExecutor: executor,
	})

	result, err := router.CallTool(ctx, CallToolRequest{
		ToolID:      string(toolstd.SkillLoad),
		PrincipalID: "agent-worker",
		AgentID:     "agent-worker",
		Permissions: []string{toolstd.PermissionSkillRead},
		SkillBindings: []agentcore.RuntimeSkillBinding{{
			SkillID: "skill.tdd",
			Alias:   "tdd",
		}},
		Arguments: map[string]any{"skill_id": "skill.tdd"},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if executor.calledToolID != string(toolstd.SkillLoad) || executor.request.SkillID != "skill.tdd" {
		t.Fatalf("skill executor request = tool:%q req:%+v", executor.calledToolID, executor.request)
	}
	if len(executor.request.SkillBindings) != 1 || executor.request.SkillBindings[0].SkillID != "skill.tdd" {
		t.Fatalf("skill bindings were not forwarded: %+v", executor.request.SkillBindings)
	}
	if !strings.Contains(result.Output["content"].(string), "red-green-refactor") {
		t.Fatalf("result = %+v", result.Output)
	}
}

func TestToolRouterRoutesKnowledgeSearchThroughExecutor(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	knowledge := &recordingKnowledgeToolExecutor{
		result: KnowledgeToolResult{Output: map[string]any{"results": []any{map[string]any{"document_id": "doc-1"}}}},
	}
	router := NewToolRouter(ToolRouterOptions{
		Registry:          registry,
		KnowledgeExecutor: knowledge,
	})

	result, err := router.CallTool(ctx, CallToolRequest{
		ToolID:      string(toolstd.KnowledgeSearch),
		PrincipalID: "agent-reader",
		AgentID:     "agent-reader",
		Permissions: []string{toolstd.PermissionKnowledgeRead},
		Arguments: map[string]any{
			"query":              "release",
			"retrieval_mode":     "agentic",
			"knowledge_base_ids": []string{"kb-agent"},
			"owner_type":         "agent",
			"owner_id":           "agent-reader",
			"limit":              3,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if knowledge.calledToolID != string(toolstd.KnowledgeSearch) ||
		knowledge.request.PrincipalID != "agent-reader" ||
		knowledge.request.AgentID != "agent-reader" ||
		knowledge.request.Query != "release" ||
		knowledge.request.RetrievalMode != "agentic" ||
		len(knowledge.request.KnowledgeBaseIDs) != 1 ||
		knowledge.request.KnowledgeBaseIDs[0] != "kb-agent" ||
		knowledge.request.OwnerType != "agent" ||
		knowledge.request.OwnerID != "agent-reader" ||
		knowledge.request.Limit != 3 {
		t.Fatalf("knowledge executor request = tool:%q req:%+v", knowledge.calledToolID, knowledge.request)
	}
	results, ok := result.Output["results"].([]any)
	if !ok || len(results) != 1 {
		t.Fatalf("result = %+v", result.Output)
	}
}

func TestToolRouterRoutesKnowledgeAddThroughExecutor(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	knowledge := &recordingKnowledgeToolExecutor{
		result: KnowledgeToolResult{Output: map[string]any{"document_id": "kdoc-1"}},
	}
	router := NewToolRouter(ToolRouterOptions{
		Registry:          registry,
		KnowledgeExecutor: knowledge,
	})

	result, err := router.CallTool(ctx, CallToolRequest{
		ToolID:      string(toolstd.KnowledgeAdd),
		PrincipalID: "agent-writer",
		AgentID:     "agent-writer",
		Permissions: []string{toolstd.PermissionKnowledgeWrite},
		Arguments: map[string]any{
			"title":             "Release note",
			"content":           "Ship knowledge add.",
			"knowledge_base_id": "kb-explicit",
			"filename":          "release-note.md",
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if knowledge.calledToolID != string(toolstd.KnowledgeAdd) ||
		knowledge.request.PrincipalID != "agent-writer" ||
		knowledge.request.AgentID != "agent-writer" ||
		knowledge.request.Title != "Release note" ||
		knowledge.request.Content != "Ship knowledge add." ||
		knowledge.request.KnowledgeBaseID != "kb-explicit" ||
		knowledge.request.Filename != "release-note.md" {
		t.Fatalf("knowledge executor request = tool:%q req:%+v", knowledge.calledToolID, knowledge.request)
	}
	if result.Output["document_id"] != "kdoc-1" {
		t.Fatalf("result = %+v", result.Output)
	}
}

func TestToolRouterRoutesWebToolsThroughExecutor(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	web := &recordingWebToolExecutor{result: WebToolResult{Output: map[string]any{"results": []any{}}}}
	router := NewToolRouter(ToolRouterOptions{Registry: registry, WebExecutor: web})

	_, err := router.CallTool(ctx, CallToolRequest{
		ToolID: string(toolstd.WebSearch), PrincipalID: "agent-reader", AgentID: "agent-reader",
		Permissions: []string{toolstd.PermissionWebRead},
		Arguments:   map[string]any{"query": "current Orrery news", "limit": 4},
	})
	if err != nil {
		t.Fatalf("CallTool(web_search): %v", err)
	}
	if web.calledToolID != string(toolstd.WebSearch) || web.request.Query != "current Orrery news" || web.request.Limit != 4 {
		t.Fatalf("web search executor request = tool:%q req:%+v", web.calledToolID, web.request)
	}

	_, err = router.CallTool(ctx, CallToolRequest{
		ToolID: string(toolstd.WebFetch), PrincipalID: "agent-reader", AgentID: "agent-reader",
		Permissions: []string{toolstd.PermissionWebRead},
		Arguments:   map[string]any{"url": "https://example.com/article", "max_characters": 12000},
	})
	if err != nil {
		t.Fatalf("CallTool(web_fetch): %v", err)
	}
	if web.calledToolID != string(toolstd.WebFetch) || web.request.URL != "https://example.com/article" || web.request.MaxCharacters != 12000 {
		t.Fatalf("web fetch executor request = tool:%q req:%+v", web.calledToolID, web.request)
	}
}

func TestToolRouterRoutesPersonalAssistantShellThroughExecutor(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	shell := &recordingPersonalAssistantShellExecutor{
		result: PersonalAssistantShellResult{Output: map[string]any{"exit_code": 0, "stdout": "ok"}},
	}
	router := NewToolRouter(ToolRouterOptions{
		Registry:                       registry,
		PersonalAssistantShellExecutor: shell,
	})

	result, err := router.CallTool(ctx, CallToolRequest{
		ToolID:      string(toolstd.PersonalAssistantShellExec),
		PrincipalID: "assistant-user-1",
		AgentID:     "personal-assistant",
		Permissions: []string{toolstd.PermissionPersonalAssistantShell},
		Arguments: map[string]any{
			"command":        "pwd",
			"cwd":            "workspace",
			"timeout_ms":     5000,
			"max_output_len": 256,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if shell.calledToolID != string(toolstd.PersonalAssistantShellExec) ||
		shell.request.PrincipalID != "assistant-user-1" ||
		shell.request.AgentID != "personal-assistant" ||
		shell.request.Command != "pwd" ||
		shell.request.Cwd != "workspace" ||
		shell.request.TimeoutMs != 5000 ||
		shell.request.MaxOutputLen != 256 {
		t.Fatalf("shell executor request = tool:%q req:%+v", shell.calledToolID, shell.request)
	}
	if result.Output["stdout"] != "ok" {
		t.Fatalf("result = %+v", result.Output)
	}
}

func TestToolRouterRoutesEnterpriseGraphBuildContextBundleThroughExecutor(t *testing.T) {
	ctx := context.Background()
	registry := newGatewayTestRegistry(t)
	enterpriseGraph := &recordingEnterpriseGraphExecutor{
		result: contextgraph.ContextBundle{
			BundleID: "bundle-1",
			TenantID: "tenant-1",
			Consumer: "iris",
			Purpose:  "mission_assignment",
			ActorRef: contextgraph.Ref{Kind: "human", ID: "user-1"},
			Objects: []contextgraph.Object{{
				ID:         "mission:mission-1",
				TenantID:   "tenant-1",
				ObjectType: "mission",
				Label:      "Mission 1",
				Properties: map[string]any{
					"title": "Ship ontology integration",
				},
			}},
			PermissionConstraints: []contextgraph.PermissionConstraint{{
				Scope:        "context.read",
				ObjectRef:    contextgraph.Ref{Kind: "mission", ID: "mission-1"},
				MaskedFields: []string{"secret_notes"},
				Reason:       "field_policy",
			}},
			Freshness:  contextgraph.Freshness{AsOf: fixedGatewayTestNow()},
			Confidence: 1,
		},
	}
	router := NewToolRouter(ToolRouterOptions{
		Registry:                registry,
		EnterpriseGraphExecutor: enterpriseGraph,
	})

	result, err := router.CallTool(ctx, CallToolRequest{
		ToolID:      string(toolstd.EnterpriseGraphBuildContextBundle),
		PrincipalID: "iris-runtime",
		AgentID:     "iris",
		Permissions: []string{toolstd.PermissionEnterpriseGraphRead},
		Arguments: map[string]any{
			"tenant_id": "tenant-1",
			"consumer":  "iris",
			"purpose":   "mission_assignment",
			"actor_ref": map[string]any{"kind": "human", "id": "user-1"},
			"subject_refs": []any{
				map[string]any{"kind": "mission", "id": "mission-1"},
			},
			"include_kinds": []any{"mission"},
			"max_depth":     1,
			"token_budget":  8000,
		},
	})
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}
	if enterpriseGraph.request.TenantID != "tenant-1" ||
		enterpriseGraph.request.Consumer != "iris" ||
		enterpriseGraph.request.Purpose != "mission_assignment" ||
		enterpriseGraph.request.ActorRef != (contextgraph.Ref{Kind: "human", ID: "user-1"}) ||
		len(enterpriseGraph.request.SubjectRefs) != 1 ||
		enterpriseGraph.request.SubjectRefs[0] != (contextgraph.Ref{Kind: "mission", ID: "mission-1"}) ||
		len(enterpriseGraph.request.IncludeKinds) != 1 ||
		enterpriseGraph.request.IncludeKinds[0] != "mission" ||
		enterpriseGraph.request.Query != "" ||
		enterpriseGraph.request.MaxDepth != 1 ||
		enterpriseGraph.request.TokenBudget != 8000 {
		t.Fatalf("enterprise graph request = %+v", enterpriseGraph.request)
	}
	if result.Output["bundle_id"] != "bundle-1" || result.Output["tenant_id"] != "tenant-1" {
		t.Fatalf("result = %+v", result.Output)
	}
	if _, ok := result.Output["objects"].([]any); !ok {
		t.Fatalf("objects should be returned as structured output: %+v", result.Output["objects"])
	}
	if _, ok := result.Output["permission_constraints"].([]any); !ok {
		t.Fatalf("permission constraints should be returned as structured output: %+v", result.Output["permission_constraints"])
	}
}

func TestToolRouterRoutesEnterpriseGraphSearchObjectsThroughExecutor(t *testing.T) {
	registry := newGatewayTestRegistry(t)
	executor := &recordingEnterpriseGraphExecutor{searchResult: contextconsumer.SearchObjectsResult{
		Items:      []contextconsumer.SearchObjectItem{{Ref: contextgraph.Ref{Kind: "mission", ID: "mission-1"}, Label: "Mission 1"}},
		NextCursor: "opaque-next",
	}}
	router := NewToolRouter(ToolRouterOptions{Registry: registry, EnterpriseGraphExecutor: executor})
	result, err := router.CallTool(context.Background(), CallToolRequest{
		TenantID: "tenant-trusted", ToolID: string(toolstd.EnterpriseGraphSearchObjects), PrincipalID: "agent-1", AgentID: "agent-1",
		Permissions: []string{toolstd.PermissionEnterpriseGraphRead},
		Arguments: map[string]any{
			"tenant_id": "tenant-untrusted", "actor_ref": map[string]any{"kind": "agent", "id": "agent-1"},
			"query": "launch", "include_kinds": []any{"mission"}, "limit": 20, "cursor": "opaque-current",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if executor.searchRequest.TenantID != "tenant-trusted" || executor.searchRequest.Query != "launch" || executor.searchRequest.Limit != 20 || executor.searchRequest.Cursor != "opaque-current" || len(executor.searchRequest.IncludeKinds) != 1 {
		t.Fatalf("search request = %+v", executor.searchRequest)
	}
	if result.Output["next_cursor"] != "opaque-next" {
		t.Fatalf("search output = %+v", result.Output)
	}
}

type recordingSchedulerExecutor struct {
	calledToolID string
	viewer       schedulerapp.ViewerRef
	arguments    map[string]any
	result       schedulerapp.ToolExecutionResult
	err          error
}

func (e *recordingSchedulerExecutor) Execute(ctx context.Context, req schedulerapp.ToolExecutionRequest) (schedulerapp.ToolExecutionResult, error) {
	e.calledToolID = req.ToolID
	e.viewer = req.Viewer
	e.arguments = req.Arguments
	return e.result, e.err
}

type recordingEvaluationToolExecutor struct {
	calledToolID string
	request      EvaluationToolRequest
	result       EvaluationToolResult
	err          error
}

func (e *recordingEvaluationToolExecutor) ExecuteEvaluationTool(ctx context.Context, toolID string, req EvaluationToolRequest) (EvaluationToolResult, error) {
	e.calledToolID = toolID
	e.request = req
	return e.result, e.err
}

type recordingMemoryDiaryToolExecutor struct {
	calledToolID string
	request      MemoryDiaryToolRequest
	result       MemoryDiaryToolResult
	err          error
}

type recordingMemorySearchToolExecutor struct {
	request MemorySearchToolRequest
	result  MemorySearchToolResult
	err     error
}

func (e *recordingMemorySearchToolExecutor) ExecuteMemorySearchTool(_ context.Context, req MemorySearchToolRequest) (MemorySearchToolResult, error) {
	e.request = req
	return e.result, e.err
}

func (e *recordingMemoryDiaryToolExecutor) ExecuteMemoryDiaryTool(ctx context.Context, toolID string, req MemoryDiaryToolRequest) (MemoryDiaryToolResult, error) {
	e.calledToolID = toolID
	e.request = req
	return e.result, e.err
}

type recordingKnowledgeToolExecutor struct {
	calledToolID string
	request      KnowledgeToolRequest
	result       KnowledgeToolResult
	err          error
}

type recordingWebToolExecutor struct {
	calledToolID string
	request      WebToolRequest
	result       WebToolResult
	err          error
}

func (e *recordingWebToolExecutor) ExecuteWebTool(_ context.Context, toolID string, req WebToolRequest) (WebToolResult, error) {
	e.calledToolID = toolID
	e.request = req
	return e.result, e.err
}

type recordingEmailToolExecutor struct {
	calledToolID string
	request      EmailToolRequest
	result       EmailToolResult
	err          error
}

func (e *recordingEmailToolExecutor) ExecuteEmailTool(ctx context.Context, toolID string, req EmailToolRequest) (EmailToolResult, error) {
	e.calledToolID = toolID
	e.request = req
	return e.result, e.err
}

func (e *recordingKnowledgeToolExecutor) ExecuteKnowledgeTool(ctx context.Context, toolID string, req KnowledgeToolRequest) (KnowledgeToolResult, error) {
	e.calledToolID = toolID
	e.request = req
	return e.result, e.err
}

type recordingPersonalAssistantShellExecutor struct {
	calledToolID string
	request      PersonalAssistantShellRequest
	result       PersonalAssistantShellResult
	err          error
}

func (e *recordingPersonalAssistantShellExecutor) ExecutePersonalAssistantShell(ctx context.Context, toolID string, req PersonalAssistantShellRequest) (PersonalAssistantShellResult, error) {
	e.calledToolID = toolID
	e.request = req
	return e.result, e.err
}

type recordingEnterpriseGraphExecutor struct {
	request       contextconsumer.BuildContextBundleInput
	result        contextgraph.ContextBundle
	searchRequest contextconsumer.SearchObjectsInput
	searchResult  contextconsumer.SearchObjectsResult
	err           error
}

func (e *recordingEnterpriseGraphExecutor) BuildContextBundle(ctx context.Context, req contextconsumer.BuildContextBundleInput) (contextgraph.ContextBundle, error) {
	e.request = req
	return e.result, e.err
}

func (e *recordingEnterpriseGraphExecutor) SearchObjects(_ context.Context, request contextconsumer.SearchObjectsInput) (contextconsumer.SearchObjectsResult, error) {
	e.searchRequest = request
	return e.searchResult, e.err
}

type recordingSkillToolExecutor struct {
	calledToolID string
	request      skillres.RuntimeSkillRequest
	result       skillres.RuntimeToolResult
	err          error
}

func (e *recordingSkillToolExecutor) ExecuteSkillTool(ctx context.Context, toolID string, req skillres.RuntimeSkillRequest) (skillres.RuntimeToolResult, error) {
	e.calledToolID = toolID
	e.request = req
	return e.result, e.err
}

type recordingExternalMCPClient struct {
	serverID    string
	toolName    string
	arguments   map[string]any
	headers     map[string]string
	requestMeta map[string]any
	result      CallToolResult
	err         error
}

func (c *recordingExternalMCPClient) CallTool(ctx context.Context, req ExternalMCPCallRequest) (CallToolResult, error) {
	c.serverID = string(req.Server.ID)
	c.toolName = req.DownstreamToolName
	c.arguments = req.Arguments
	c.requestMeta = req.RequestMeta
	c.headers = map[string]string{}
	if req.Server.MCP != nil {
		for name, value := range req.Server.MCP.Headers {
			c.headers[name] = value
		}
	}
	return c.result, c.err
}

type recordingAuditRecorder struct {
	events []AuditEvent
}

func (r *recordingAuditRecorder) RecordMCPToolAudit(ctx context.Context, event AuditEvent) error {
	r.events = append(r.events, event)
	return nil
}

func fixedGatewayTestNow() time.Time {
	return time.Date(2026, 6, 8, 12, 0, 0, 0, time.UTC)
}
