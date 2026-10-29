package runtimeapproval

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	runtimecore "orrery_v2/internal/agent/runtime/core"
)

type reviewServiceFunc func(context.Context, ReviewerSettings, ReviewInput) ReviewResult

func (f reviewServiceFunc) Review(ctx context.Context, settings ReviewerSettings, input ReviewInput) ReviewResult {
	return f(ctx, settings, input)
}

func TestDecisionServiceNeverSendsSystemToolsToHuman(t *testing.T) {
	policy := DefaultPolicy()
	policy.Tools.Allowlist = []ToolRule{{
		RuleID: "knowledge-write", ToolID: "knowledge_add", Effects: []string{"write"}, Risk: "medium",
	}}
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, nil)

	allowed := service.Decide(context.Background(), DecisionInput{
		Request:                 runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "knowledge_add"},
		ExplicitConversationHIL: "manual",
	})
	if allowed.Outcome != DecisionOutcomeApproveOnce {
		t.Fatalf("authorized system tool = %+v", allowed)
	}
	denied := service.Decide(context.Background(), DecisionInput{
		Request:                 runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "unknown_system_tool"},
		ExplicitConversationHIL: "manual",
	})
	if denied.Outcome != DecisionOutcomeDeny {
		t.Fatalf("unknown system tool = %+v", denied)
	}
}

func TestDecisionServiceKeepsBusinessActionsHuman(t *testing.T) {
	policy := DefaultPolicy()
	policy.Tools.Security = SecurityFull
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, nil)

	for _, toolName := range []string{"email_send", "mcp_marketing_marketing_wechat_draft_create"} {
		t.Run(toolName, func(t *testing.T) {
			got := service.Decide(context.Background(), DecisionInput{
				Request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: toolName},
			})
			if got.Outcome != DecisionOutcomeHuman {
				t.Fatalf("business action = %+v", got)
			}
		})
	}
}

func TestDecisionServiceAllowsBusinessApprovalBridgeWithoutRecursiveReview(t *testing.T) {
	service := NewDecisionService(StaticPolicyProvider{Policy: DefaultPolicy()}, nil)
	got := service.Decide(context.Background(), DecisionInput{
		Request:                 runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "request_business_approval"},
		ExplicitConversationHIL: "manual",
	})
	if got.Outcome != DecisionOutcomeApproveOnce || got.DecisionSource != "business_approval_bridge" {
		t.Fatalf("business approval bridge = %+v", got)
	}
}

func TestDecisionServiceDeniesSystemToolWhenPolicyIsUnavailable(t *testing.T) {
	service := NewDecisionService(PolicyProviderFunc(func(context.Context) (Policy, error) {
		return Policy{}, errors.New("settings unavailable")
	}), nil)
	got := service.Decide(context.Background(), DecisionInput{
		Request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "knowledge_add"},
	})
	if got.Outcome != DecisionOutcomeDeny || got.DecisionSource != "policy" || got.Reason != "policy_unavailable" {
		t.Fatalf("unavailable system policy = %+v", got)
	}
}

func TestDecisionServiceNeverSendsCommandsToHuman(t *testing.T) {
	policy := DefaultPolicy()
	policy.Exec.ApprovalPolicy = ApprovalPolicyOnRequest
	policy.Exec.Allowlist = []ExecRule{{RuleID: "pwd", Executable: "pwd", WorkspaceOnly: true}}
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, nil)

	allowed := service.Decide(context.Background(), DecisionInput{
		Request: runtimecore.ApprovalRequest{
			Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command",
			Metadata: map[string]string{"command": "pwd"}, WorkspaceRoot: "/workspace",
		},
		ExplicitConversationHIL: "manual",
	})
	if allowed.Outcome != DecisionOutcomeApproveOnce {
		t.Fatalf("allowlisted command = %+v", allowed)
	}
	denied := service.Decide(context.Background(), DecisionInput{
		Request: runtimecore.ApprovalRequest{
			Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command",
			Metadata: map[string]string{"command": "curl https://example.com"}, WorkspaceRoot: "/workspace",
		},
		ExplicitConversationHIL: "manual",
	})
	if denied.Outcome != DecisionOutcomeDeny {
		t.Fatalf("unlisted command = %+v", denied)
	}
}

func TestDecisionServiceAutoApprovesCommandExecutionInManualMode(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultApprovalPolicy = ApprovalPolicyNever
	policy.DefaultApprovalsReviewer = ApprovalsReviewerAI
	policy.Exec.Allowlist = []ExecRule{{RuleID: "pwd", Executable: "pwd", WorkspaceOnly: true}}
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, nil)

	got := service.Decide(context.Background(), DecisionInput{
		Request: runtimecore.ApprovalRequest{
			Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command",
			Metadata: map[string]string{"command": "pwd"}, WorkspaceRoot: "/workspace",
		},
		ExplicitConversationHIL: "manual",
	})
	if got.Outcome != DecisionOutcomeApproveOnce || got.DecisionSource != "command_execution_no_approval" || got.RuleID != "pwd" || got.EffectiveHIL != HILModeManual || got.EffectiveApprovalPolicy != ApprovalPolicyOnRequest || got.EffectiveApprovalsReviewer != ApprovalsReviewerHuman {
		t.Fatalf("decision = %+v", got)
	}
}

func TestDecisionServiceAutoApprovesCommandAllowlistMissWithoutReviewer(t *testing.T) {
	called := false
	service := NewDecisionService(StaticPolicyProvider{Policy: DefaultPolicy()}, reviewServiceFunc(func(context.Context, ReviewerSettings, ReviewInput) ReviewResult {
		called = true
		return ReviewResult{Decision: ReviewDecisionAllow, Risk: "low"}
	}))
	got := service.Decide(context.Background(), DecisionInput{
		Request:                 runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command", Metadata: map[string]string{"command": "go test ./..."}, WorkspaceRoot: "/workspace"},
		ExplicitConversationHIL: "manual",
	})
	if got.Outcome != DecisionOutcomeApproveOnce || got.DecisionSource != "command_execution_no_approval" || called || got.Review != nil {
		t.Fatalf("decision = %+v, reviewer called=%v", got, called)
	}
}

func TestDecisionServiceAutoApprovesTrustedCommandWithoutProviderMaterial(t *testing.T) {
	service := NewDecisionService(StaticPolicyProvider{Policy: DefaultPolicy()}, nil)
	got := service.Decide(context.Background(), DecisionInput{
		Request: runtimecore.ApprovalRequest{
			Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command", CommandMaterialUnavailable: true,
		},
		ExplicitConversationHIL: "manual",
	})
	if got.Outcome != DecisionOutcomeApproveOnce || got.DecisionSource != "command_execution_no_approval" || got.Reason != "exec_command_unavailable" {
		t.Fatalf("decision = %+v", got)
	}
}

func TestDecisionServiceDeniesUnresolvedCommandWhenManualExecRulesExist(t *testing.T) {
	policy := DefaultPolicy()
	policy.ForcedHuman.ExecExecutables = []string{"deploy"}
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, nil)
	got := service.Decide(context.Background(), DecisionInput{
		Request: runtimecore.ApprovalRequest{
			Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command", CommandMaterialUnavailable: true,
		},
	})
	if got.Outcome != DecisionOutcomeDeny || got.DecisionSource != "policy" || got.Reason != "system_tool_manual_review_not_supported" {
		t.Fatalf("decision = %+v", got)
	}
}

func TestDecisionServiceExecOnRequestUsesAutomaticPolicy(t *testing.T) {
	policy := DefaultPolicy()
	policy.Exec.ApprovalPolicy = ApprovalPolicyOnRequest
	policy.Exec.Allowlist = []ExecRule{{RuleID: "pwd", Executable: "pwd", WorkspaceOnly: true}}
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, nil)

	manual := service.Decide(context.Background(), DecisionInput{
		Request: runtimecore.ApprovalRequest{
			Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command",
			Metadata: map[string]string{"command": "pwd"}, WorkspaceRoot: "/workspace",
		},
		ExplicitConversationHIL: "manual",
	})
	if manual.Outcome != DecisionOutcomeApproveOnce || manual.DecisionSource != "allowlist" || manual.RuleID != "pwd" {
		t.Fatalf("manual decision = %+v", manual)
	}

	unresolved := service.Decide(context.Background(), DecisionInput{
		Request: runtimecore.ApprovalRequest{
			Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command", CommandMaterialUnavailable: true,
		},
	})
	if unresolved.Outcome != DecisionOutcomeDeny || unresolved.DecisionSource != "policy" || unresolved.Reason != "exec_command_unavailable" {
		t.Fatalf("unresolved decision = %+v", unresolved)
	}
}

func TestDecisionServiceApprovesLowRiskAIReviewOnceInAutoMode(t *testing.T) {
	policy := DefaultPolicy()
	policy.Reviewer.LLMProfileID = "reviewer"
	policy.Tools.Allowlist = []ToolRule{{RuleID: "custom-read", ToolID: "custom_lookup", Effects: []string{"read"}, Risk: "low"}}
	var reviewed ReviewInput
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, reviewServiceFunc(func(_ context.Context, _ ReviewerSettings, input ReviewInput) ReviewResult {
		reviewed = input
		return ReviewResult{
			Decision: ReviewDecisionAllow, Risk: "low", Rationale: "Read-only test discovery.",
			ReasonCode: "reviewer_low_risk_allow", ModelProfile: "reviewer", PromptVersion: ReviewerPromptVersion, InputDigest: "digest",
		}
	}))
	got := service.Decide(context.Background(), DecisionInput{
		Request:                 runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "custom_lookup", Payload: []byte(`{"input":{"query":"value"}}`)},
		ExplicitConversationHIL: "auto",
		LogicalAgentID:          "assistant:human-1",
	})
	if got.Outcome != DecisionOutcomeApproveOnce || got.DecisionSource != "ai_reviewer" || got.Review == nil || got.Review.Risk != "low" {
		t.Fatalf("decision = %+v", got)
	}
	if reviewed.Kind != ReviewKindTool || reviewed.LogicalAgentID != "assistant:human-1" || reviewed.ToolID != "custom_lookup" || len(reviewed.MatchedRuleIDs) != 1 || reviewed.MatchedRuleIDs[0] != "custom-read" || !reviewed.Deterministic {
		t.Fatalf("review input = %+v", reviewed)
	}
}

func TestDecisionServiceAIDenyDeniesRequest(t *testing.T) {
	policy := DefaultPolicy()
	policy.DefaultApprovalsReviewer = ApprovalsReviewerAI
	policy.Reviewer.LLMProfileID = "reviewer"
	policy.Tools.Allowlist = []ToolRule{{RuleID: "custom-read", ToolID: "custom_lookup", Effects: []string{"read"}, Risk: "low"}}
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, reviewServiceFunc(func(context.Context, ReviewerSettings, ReviewInput) ReviewResult {
		return ReviewResult{
			Decision: ReviewDecisionDeny, Risk: "high", Rationale: "The request is not appropriate.",
			ReasonCode: "reviewer_deny", ModelProfile: "reviewer", PromptVersion: ReviewerPromptVersion,
		}
	}))

	got := service.Decide(context.Background(), DecisionInput{
		Request: runtimecore.ApprovalRequest{
			Kind: runtimecore.ApprovalKindTool, ToolName: "custom_lookup",
			Payload: []byte(`{"input":{"query":"value"}}`),
		},
	})
	if got.Outcome != DecisionOutcomeDeny || got.DecisionSource != "ai_reviewer" || got.Reason != "reviewer_deny" || got.Review == nil {
		t.Fatalf("decision = %+v", got)
	}
}

func TestDecisionServiceExecAllowlistMissDoesNotReachAIReviewer(t *testing.T) {
	policy := DefaultPolicy()
	policy.Reviewer.LLMProfileID = "reviewer"
	called := false
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, reviewServiceFunc(func(context.Context, ReviewerSettings, ReviewInput) ReviewResult {
		called = true
		return ReviewResult{Decision: ReviewDecisionAllow, Risk: "low"}
	}))
	got := service.Decide(context.Background(), DecisionInput{
		Request:                 runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command", Metadata: map[string]string{"command": "curl https://example.com"}, WorkspaceRoot: "/workspace"},
		ExplicitConversationHIL: "auto",
	})
	if got.Outcome != DecisionOutcomeApproveOnce || got.DecisionSource != "command_execution_no_approval" || called || got.Review != nil {
		t.Fatalf("decision = %+v, reviewer called=%v", got, called)
	}
}

func TestDecisionServiceDoesNotLetAIExpandToolAllowlist(t *testing.T) {
	policy := DefaultPolicy()
	policy.Reviewer.LLMProfileID = "reviewer"
	called := false
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, reviewServiceFunc(func(context.Context, ReviewerSettings, ReviewInput) ReviewResult {
		called = true
		return ReviewResult{Decision: ReviewDecisionAllow, Risk: "low"}
	}))
	got := service.Decide(context.Background(), DecisionInput{
		Request:                 runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "custom_lookup", Payload: []byte(`{"input":{"query":"value"}}`)},
		ExplicitConversationHIL: "auto",
	})
	if got.Outcome != DecisionOutcomeDeny || got.DecisionSource != "policy" || called || got.Review != nil {
		t.Fatalf("decision = %+v, reviewer called=%v", got, called)
	}
}

func TestDecisionServiceAutoApprovesPersonalAssistantContextReads(t *testing.T) {
	policy := DefaultPolicy()
	policy.Reviewer.LLMProfileID = "reviewer"
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, reviewServiceFunc(func(_ context.Context, _ ReviewerSettings, _ ReviewInput) ReviewResult {
		t.Fatal("auto-approved context read reached AI reviewer")
		return ReviewResult{}
	}))

	for _, toolID := range []string{
		"memory_search", "knowledge_search", "enterprise_graph.build_context_bundle", "enterprise_graph.search_objects", "web_search", "web_fetch",
		"mcp_server_browser_navigate_page", "mcp_server_browser_take_screenshot",
	} {
		t.Run(toolID, func(t *testing.T) {
			got := service.Decide(context.Background(), DecisionInput{
				Request:                 runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: toolID, Payload: []byte(`{"query":"relevant context"}`)},
				LogicalAgentID:          "personal-assistant-ding",
				ConversationKind:        "personal_assistant",
				ExplicitConversationHIL: "auto",
			})
			if got.Outcome != DecisionOutcomeApproveOnce || got.DecisionSource != "allowlist" || got.Reason != "tool_allowlist_match" || got.RuleID == "" || got.Review != nil {
				t.Fatalf("decision = %+v", got)
			}
		})
	}
}

func TestDecisionServiceExplicitManualDoesNotOverrideSystemToolPolicy(t *testing.T) {
	policy := DefaultPolicy()
	policy.AgentOverrides = []AgentOverride{{
		LogicalAgentID: "personal-assistant-ding",
		PolicyOverride: PolicyOverride{ApprovalPolicy: ApprovalPolicyNever},
	}}
	called := false
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, reviewServiceFunc(func(context.Context, ReviewerSettings, ReviewInput) ReviewResult {
		called = true
		return ReviewResult{Decision: ReviewDecisionAllow, Risk: "low"}
	}))

	got := service.Decide(context.Background(), DecisionInput{
		Request: runtimecore.ApprovalRequest{
			Kind: runtimecore.ApprovalKindTool, ToolName: "mcp_server_browser_take_screenshot",
			Payload: []byte(`{"input":{}}`),
		},
		LogicalAgentID:          "personal-assistant-ding",
		ConversationKind:        "personal_assistant",
		ExplicitConversationHIL: "manual",
	})
	if got.Outcome != DecisionOutcomeApproveOnce || got.DecisionSource != "allowlist" || got.Reason != "tool_allowlist_match" || got.RuleID != "pa-server-browser-screenshot" {
		t.Fatalf("decision = %+v", got)
	}
	if got.EffectiveHIL != HILModeManual || got.EffectiveApprovalPolicy != ApprovalPolicyOnRequest || got.EffectiveApprovalsReviewer != ApprovalsReviewerHuman || got.PolicySource.ApprovalPolicy != "conversation_explicit" || got.Review != nil || called {
		t.Fatalf("manual decision policy = %+v, reviewer called=%v", got, called)
	}
}

func TestDecisionServiceApprovesOnlyAuthorizedMissionWorkspaceFileChanges(t *testing.T) {
	root := t.TempDir()
	workDir := filepath.Join(root, "agents", "builder")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	service := NewDecisionService(StaticPolicyProvider{Policy: DefaultPolicy()}, nil)
	request := func(path string) runtimecore.ApprovalRequest {
		payload, err := json.Marshal(map[string]any{"tool": "write", "input": map[string]any{"path": path, "content": "game"}})
		if err != nil {
			t.Fatal(err)
		}
		return runtimecore.ApprovalRequest{
			Kind: runtimecore.ApprovalKindFileChange, ToolName: "patch_apply", Payload: payload,
			Metadata:      map[string]string{"approval_native_tool_name": "write"},
			WorkspaceRoot: root, WorkingDirectory: workDir,
			WorkspaceBoundaryEnforced: true, WorkspaceWriteAuthorized: true,
		}
	}

	approved := service.Decide(context.Background(), DecisionInput{Request: request("index.html"), ExplicitConversationHIL: "manual"})
	if approved.Outcome != DecisionOutcomeApproveOnce || approved.DecisionSource != "mission_workspace" || approved.RuleID != "mission-canonical-workspace-write" || approved.Intent.RiskLevel != "low" {
		t.Fatalf("authorized workspace decision = %+v", approved)
	}

	for name, mutate := range map[string]func(*runtimecore.ApprovalRequest){
		"outside workdir": func(req *runtimecore.ApprovalRequest) {
			*req = request(filepath.Join(root, "other-agent.html"))
		},
		"authorization absent": func(req *runtimecore.ApprovalRequest) { req.WorkspaceWriteAuthorized = false },
		"boundary absent":      func(req *runtimecore.ApprovalRequest) { req.WorkspaceBoundaryEnforced = false },
		"unknown native tool": func(req *runtimecore.ApprovalRequest) {
			req.Metadata["approval_native_tool_name"] = "custom_write"
		},
		"forced manual": func(req *runtimecore.ApprovalRequest) { req.Metadata["approval_force_manual"] = "true" },
	} {
		t.Run(name, func(t *testing.T) {
			req := request("index.html")
			mutate(&req)
			decision := service.Decide(context.Background(), DecisionInput{Request: req, ExplicitConversationHIL: "manual"})
			if decision.Outcome != DecisionOutcomeDeny || decision.DecisionSource == "mission_workspace" {
				t.Fatalf("decision = %+v", decision)
			}
		})
	}

	t.Run("symlink escape", func(t *testing.T) {
		outside := t.TempDir()
		link := filepath.Join(workDir, "escape")
		if err := os.Symlink(outside, link); err != nil {
			t.Skipf("create symlink: %v", err)
		}
		decision := service.Decide(context.Background(), DecisionInput{
			Request:                 request(filepath.Join(link, "index.html")),
			ExplicitConversationHIL: "manual",
		})
		if decision.Outcome != DecisionOutcomeDeny || decision.DecisionSource == "mission_workspace" {
			t.Fatalf("decision = %+v", decision)
		}
	})
}

func TestDecisionServiceExplicitManualStillHonorsHardPolicyFailures(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Policy)
		request   runtimecore.ApprovalRequest
		reason    string
	}{
		{
			name:      "exec deny",
			configure: func(policy *Policy) { policy.Exec.Security = SecurityDeny },
			request:   runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command", Metadata: map[string]string{"command": "pwd"}},
			reason:    "exec_policy_deny",
		},
		{
			name:      "tool deny",
			configure: func(policy *Policy) { policy.Tools.Security = SecurityDeny },
			request:   runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "custom_tool"},
			reason:    "tool_policy_deny",
		},
		{
			name:      "invalid tool payload",
			configure: func(*Policy) {},
			request:   runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "custom_tool", Payload: []byte(`{`)},
			reason:    "tool_payload_invalid",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := DefaultPolicy()
			test.configure(&policy)
			got := NewDecisionService(StaticPolicyProvider{Policy: policy}, nil).Decide(context.Background(), DecisionInput{
				Request: test.request, ExplicitConversationHIL: "manual",
			})
			if got.Outcome != DecisionOutcomeDeny || got.DecisionSource != "policy" || got.Reason != test.reason {
				t.Fatalf("decision = %+v", got)
			}
		})
	}
}

func TestDecisionServiceDoesNotSendExecInputToReviewer(t *testing.T) {
	policy := DefaultPolicy()
	policy.Reviewer.LLMProfileID = "reviewer"
	policy.Exec.Allowlist = []ExecRule{{RuleID: "rg", Executable: "rg", SafeFlags: []string{"-n"}, WorkspaceOnly: true}}
	called := false
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, reviewServiceFunc(func(_ context.Context, _ ReviewerSettings, _ ReviewInput) ReviewResult {
		called = true
		return ReviewResult{Decision: ReviewDecisionAsk, Risk: "unknown"}
	}))
	got := service.Decide(context.Background(), DecisionInput{
		Request:                 runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command", Metadata: map[string]string{"command": "rg -n raw-exec-review-secret ."}, WorkspaceRoot: "/workspace"},
		ExplicitConversationHIL: "auto",
	})
	if got.Outcome != DecisionOutcomeApproveOnce || got.DecisionSource != "command_execution_no_approval" || called || got.Review != nil {
		t.Fatalf("decision = %+v, reviewer called=%v", got, called)
	}
}

func TestDecisionServiceDoesNotExposeRawToolInputToReviewer(t *testing.T) {
	policy := DefaultPolicy()
	policy.Reviewer.LLMProfileID = "reviewer"
	policy.Tools.Allowlist = []ToolRule{{RuleID: "custom-read", ToolID: "custom_lookup", Effects: []string{"read"}, Risk: "low"}}
	var reviewed ReviewInput
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, reviewServiceFunc(func(_ context.Context, _ ReviewerSettings, input ReviewInput) ReviewResult {
		reviewed = input
		return ReviewResult{Decision: ReviewDecisionAsk, Risk: "unknown"}
	}))
	service.Decide(context.Background(), DecisionInput{
		Request:                 runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "custom_lookup", Payload: []byte(`{"input":{"content":"raw-tool-review-secret"}}`)},
		ExplicitConversationHIL: "auto",
	})
	encoded, err := json.Marshal(reviewed)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "raw-tool-review-secret") {
		t.Fatalf("review input contains raw Tool data: %s", encoded)
	}
}

func TestDecisionServiceDeniesReviewerFailureWithoutHumanEscalation(t *testing.T) {
	policy := DefaultPolicy()
	policy.Reviewer.LLMProfileID = "reviewer"
	policy.Tools.Allowlist = []ToolRule{{RuleID: "custom-read", ToolID: "custom_lookup", Effects: []string{"read"}, Risk: "low"}}
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, reviewServiceFunc(func(context.Context, ReviewerSettings, ReviewInput) ReviewResult {
		return ReviewResult{Decision: ReviewDecisionAsk, Risk: "unknown", ReasonCode: "reviewer_timeout", ModelProfile: "reviewer"}
	}))
	got := service.Decide(context.Background(), DecisionInput{
		Request:                 runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "custom_lookup", Payload: []byte(`{"input":{"query":"value"}}`)},
		ExplicitConversationHIL: "auto",
	})
	if got.Outcome != DecisionOutcomeDeny || got.DecisionSource != "ai_reviewer" || got.Review == nil || got.Review.ReasonCode != "reviewer_timeout" {
		t.Fatalf("decision = %+v", got)
	}
}

func TestDecisionServiceKeepsForcedActionsHumanInFullMode(t *testing.T) {
	policy := DefaultPolicy()
	policy.Tools.Security = SecurityFull
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, nil)
	got := service.Decide(context.Background(), DecisionInput{
		Request: runtimecore.ApprovalRequest{
			Kind: runtimecore.ApprovalKindTool, ToolName: "marketing_wechat_draft_create",
			Metadata: map[string]string{"approval_force_manual": "true"},
		},
		ExplicitConversationHIL: "auto",
	})
	if got.Outcome != DecisionOutcomeHuman || got.DecisionSource != "business_decision" || got.Reason != "business_decision_required" {
		t.Fatalf("decision = %+v", got)
	}
}

func TestDecisionServiceKeepsBreakGlassFullModeDirectInAuto(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Policy)
		request   runtimecore.ApprovalRequest
		reason    string
		source    string
	}{
		{
			name:      "exec",
			configure: func(policy *Policy) { policy.Exec.Security = SecurityFull },
			request:   runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command", Metadata: map[string]string{"command": "pwd"}},
			reason:    "exec_full",
			source:    "command_execution_no_approval",
		},
		{
			name:      "tool",
			configure: func(policy *Policy) { policy.Tools.Security = SecurityFull },
			request:   runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "custom_lookup"},
			reason:    "tool_full",
			source:    "full",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			policy := DefaultPolicy()
			tt.configure(&policy)
			called := false
			service := NewDecisionService(StaticPolicyProvider{Policy: policy}, reviewServiceFunc(func(context.Context, ReviewerSettings, ReviewInput) ReviewResult {
				called = true
				return ReviewResult{Decision: ReviewDecisionAsk, Risk: "unknown"}
			}))
			got := service.Decide(context.Background(), DecisionInput{Request: tt.request, ExplicitConversationHIL: "auto"})
			if got.Outcome != DecisionOutcomeApproveOnce || got.DecisionSource != tt.source || got.Reason != tt.reason || called {
				t.Fatalf("decision = %+v, reviewer called=%v", got, called)
			}
		})
	}
}

func TestDecisionServiceNeverApprovesPermissionsButNotUserInput(t *testing.T) {
	policy := DefaultPolicy()
	policy.AgentOverrides = []AgentOverride{{
		LogicalAgentID: "personal-assistant-ding",
		PolicyOverride: PolicyOverride{ApprovalPolicy: ApprovalPolicyNever},
	}}
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, nil)

	tests := []struct {
		name    string
		request runtimecore.ApprovalRequest
		outcome DecisionOutcome
	}{
		{name: "exec forced human", request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command", Metadata: map[string]string{"command": "rm -rf ./workspace"}}, outcome: DecisionOutcomeApproveOnce},
		{name: "sequential read exec", request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command", Metadata: map[string]string{"command": `pwd && rg -n "error" . --hidden -g '!node_modules'`}}, outcome: DecisionOutcomeApproveOnce},
		{name: "tool forced human", request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "publish_contract", Metadata: map[string]string{"approval_force_manual": "true", "effects": "publish", "risk": "critical"}}, outcome: DecisionOutcomeApproveOnce},
		{name: "file change", request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindFileChange, ToolName: "patch_apply", Metadata: map[string]string{"effects": "write"}}, outcome: DecisionOutcomeApproveOnce},
		{name: "network", request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "network", Metadata: map[string]string{"effects": "network"}}, outcome: DecisionOutcomeApproveOnce},
		{name: "elicitation", request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindMCPElicitation, ToolName: "AskUserQuestion"}, outcome: DecisionOutcomeHuman},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := service.Decide(context.Background(), DecisionInput{Request: test.request, LogicalAgentID: "personal-assistant-ding"})
			if got.Outcome != test.outcome {
				t.Fatalf("decision for %s = %+v", test.request.Kind, got)
			}
			if test.outcome == DecisionOutcomeApproveOnce {
				if test.request.Kind == runtimecore.ApprovalKindCommandExecution {
					if got.DecisionSource != "command_execution_no_approval" {
						t.Fatalf("command decision = %+v", got)
					}
				} else if got.DecisionSource != "approval_policy_never" || got.Reason != "approval_policy_never" {
					t.Fatalf("decision for %s = %+v", test.request.Kind, got)
				}
			}
			if test.outcome == DecisionOutcomeHuman && got.DecisionSource == "approval_policy_never" {
				t.Fatalf("elicitation decision = %+v", got)
			}
		})
	}
}

func TestDecisionServiceConfiguredForcedHumanOverridesNeverPolicy(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Policy)
		request   runtimecore.ApprovalRequest
		outcome   DecisionOutcome
		source    string
		reason    string
	}{
		{
			name: "exec",
			configure: func(policy *Policy) {
				policy.ForcedHuman.ExecExecutables = []string{"deploy"}
			},
			request: runtimecore.ApprovalRequest{
				Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command",
				Metadata: map[string]string{"command": "deploy production"},
			},
			outcome: DecisionOutcomeDeny,
			source:  "policy",
			reason:  "system_tool_manual_review_not_supported",
		},
		{
			name: "tool",
			configure: func(policy *Policy) {
				policy.ForcedHuman.ToolIDs = []string{"mcp_marketing_marketing_wechat_draft_create"}
			},
			request: runtimecore.ApprovalRequest{
				Kind: runtimecore.ApprovalKindTool, ToolName: "mcp_marketing_marketing_wechat_draft_create",
			},
			outcome: DecisionOutcomeHuman,
			source:  "business_decision",
			reason:  "business_decision_required",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := DefaultPolicy()
			policy.DefaultApprovalPolicy = ApprovalPolicyNever
			test.configure(&policy)
			service := NewDecisionService(StaticPolicyProvider{Policy: policy}, nil)
			got := service.Decide(context.Background(), DecisionInput{Request: test.request})
			if got.Outcome != test.outcome || got.DecisionSource != test.source || got.Reason != test.reason {
				t.Fatalf("decision = %+v", got)
			}
		})
	}
}

func TestDecisionServiceLegacyBypassUsesNeverPolicy(t *testing.T) {
	bypass := true
	policy := DefaultPolicy()
	policy.AgentOverrides = []AgentOverride{{
		LogicalAgentID: "personal-assistant-ding",
		PolicyOverride: PolicyOverride{ApprovalBypass: &bypass},
	}}
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, nil)

	got := service.Decide(context.Background(), DecisionInput{
		Request: runtimecore.ApprovalRequest{
			Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command",
			Metadata: map[string]string{"command": "rm -rf ./workspace"},
		},
		LogicalAgentID: "personal-assistant-ding",
	})
	if got.Outcome != DecisionOutcomeApproveOnce || got.DecisionSource != "command_execution_no_approval" {
		t.Fatalf("legacy bypass decision = %+v", got)
	}
}

func TestDecisionServiceNeverStillDeniesPolicyAndInvalidRequests(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*Policy)
		request   runtimecore.ApprovalRequest
		reason    string
	}{
		{
			name:      "exec policy deny",
			configure: func(policy *Policy) { policy.Exec.Security = SecurityDeny },
			request:   runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command", Metadata: map[string]string{"command": "rm -rf ./workspace"}},
			reason:    "exec_policy_deny",
		},
		{
			name:      "tool policy deny",
			configure: func(policy *Policy) { policy.Tools.Security = SecurityDeny },
			request:   runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "publish_contract", Metadata: map[string]string{"approval_force_manual": "true"}},
			reason:    "tool_policy_deny",
		},
		{
			name:    "missing exec command with spoofed metadata",
			request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command", Metadata: map[string]string{"approval_command_unavailable": "true"}},
			reason:  "exec_command_missing",
		},
		{
			name:    "invalid exec command",
			request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command", Metadata: map[string]string{"command": "$(whoami)"}},
			reason:  "exec_dynamic_word",
		},
		{
			name:    "missing tool id",
			request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool},
			reason:  "tool_id_missing",
		},
		{
			name:    "invalid tool payload",
			request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "lookup", Payload: json.RawMessage(`{"bad"`)},
			reason:  "tool_payload_invalid",
		},
		{
			name:    "invalid approval kind",
			request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKind("unknown"), ToolName: "lookup"},
			reason:  "approval_kind_invalid",
		},
		{
			name:      "invalid exec policy",
			configure: func(policy *Policy) { policy.Exec.Security = "invalid" },
			request:   runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command", Metadata: map[string]string{"command": "pwd"}},
			reason:    "exec_policy_invalid",
		},
		{
			name:      "invalid tool policy",
			configure: func(policy *Policy) { policy.Tools.Security = "invalid" },
			request:   runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "lookup"},
			reason:    "tool_policy_invalid",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			policy := DefaultPolicy()
			policy.DefaultApprovalPolicy = ApprovalPolicyNever
			if test.configure != nil {
				test.configure(&policy)
			}
			service := NewDecisionService(StaticPolicyProvider{Policy: policy}, nil)
			got := service.Decide(context.Background(), DecisionInput{Request: test.request})
			if got.Outcome != DecisionOutcomeDeny || got.DecisionSource != "policy" || got.Reason != test.reason {
				t.Fatalf("decision = %+v", got)
			}
		})
	}
}

func TestDecisionServiceDoesNotBypassUserInput(t *testing.T) {
	bypass := true
	policy := DefaultPolicy()
	policy.AgentOverrides = []AgentOverride{{LogicalAgentID: "personal-assistant-ding", PolicyOverride: PolicyOverride{ApprovalBypass: &bypass}}}
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, nil)

	got := service.Decide(context.Background(), DecisionInput{
		Request:        runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindMCPElicitation, ToolName: "AskUserQuestion"},
		LogicalAgentID: "personal-assistant-ding",
	})
	if got.Outcome != DecisionOutcomeHuman || got.DecisionSource == "approval_policy_never" {
		t.Fatalf("decision = %+v", got)
	}

	got = service.Decide(context.Background(), DecisionInput{
		Request:                            runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindMCPElicitation, ToolName: "AskUserQuestion"},
		ExplicitConversationHIL:            string(HILModeAuto),
		ExplicitConversationApprovalPolicy: string(ApprovalPolicyNever),
	})
	if got.Outcome != DecisionOutcomeHuman || got.DecisionSource == "approval_policy_never" {
		t.Fatalf("conversation no-approval decision = %+v", got)
	}
}

func TestDecisionServiceDeniesPolicyLevelDeny(t *testing.T) {
	policy := DefaultPolicy()
	policy.Exec.Security = SecurityDeny
	service := NewDecisionService(StaticPolicyProvider{Policy: policy}, nil)
	got := service.Decide(context.Background(), DecisionInput{
		Request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command", Metadata: map[string]string{"command": "pwd"}},
	})
	if got.Outcome != DecisionOutcomeDeny || got.DecisionSource != "policy" || got.Reason != "exec_policy_deny" {
		t.Fatalf("decision = %+v", got)
	}
}

func TestDecisionServicePolicyLoadFailureDeniesSystemTool(t *testing.T) {
	service := NewDecisionService(PolicyProviderFunc(func(context.Context) (Policy, error) {
		return Policy{}, errors.New("settings unavailable")
	}), nil)
	got := service.Decide(context.Background(), DecisionInput{Request: runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindTool, ToolName: "lookup"}})
	if got.Outcome != DecisionOutcomeDeny || got.DecisionSource != "policy" || got.Reason != "policy_unavailable" {
		t.Fatalf("decision = %+v", got)
	}
}
