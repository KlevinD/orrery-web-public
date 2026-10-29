package discussionsession

import (
	"context"
	"testing"
	"time"

	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/runtimeapproval"
)

type runtimeApprovalDecisionServiceFunc func(context.Context, runtimeapproval.DecisionInput) runtimeapproval.Decision

func (f runtimeApprovalDecisionServiceFunc) Decide(ctx context.Context, input runtimeapproval.DecisionInput) runtimeapproval.Decision {
	return f(ctx, input)
}

func TestRuntimeApprovalCoordinatorApprovesLowRiskAIReviewOnceInAutoMode(t *testing.T) {
	repo := newMemoryRepository(t)
	realtime := &recordingRealtimePublisher{}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository:        repo,
		RealtimePublisher: realtime,
		Clock:             fixedClock{now: testTime},
		Decisions: runtimeApprovalDecisionServiceFunc(func(_ context.Context, input runtimeapproval.DecisionInput) runtimeapproval.Decision {
			if input.ExplicitConversationHIL != DiscussionHILModeAuto {
				t.Fatalf("explicit HIL = %q", input.ExplicitConversationHIL)
			}
			return runtimeapproval.Decision{
				Outcome: runtimeapproval.DecisionOutcomeApproveOnce, EffectiveHIL: runtimeapproval.HILModeAuto,
				PolicySource: runtimeapproval.ResolutionSource{HILMode: "conversation_explicit", Exec: "platform_default", Tools: "platform_default"},
				PolicyDigest: "policy-digest", DecisionSource: "ai_reviewer", Reason: "tool_allowlist_match", InputDigest: "input-digest",
				Review: &runtimeapproval.ReviewResult{Decision: runtimeapproval.ReviewDecisionAllow, Risk: "low", Rationale: "Read-only verification.", ReasonCode: "reviewer_low_risk_allow", ModelProfile: "reviewer-profile", PromptVersion: runtimeapproval.ReviewerPromptVersion, Latency: 42 * time.Millisecond, InputDigest: "input-digest"},
			}
		}),
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-auto", HILMode: DiscussionHILModeAuto, Title: "auto", Status: "running", CreatedBy: "local-user", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	responseCh := make(chan runtimecore.ApprovalResponse, 1)
	err := coordinator.RequestApproval(contextWithRuntimeApproval(ctx, runtimeApprovalContext{ConversationID: "conv-auto", RunID: "run-auto", AgentID: "agent-1", RuntimeKind: "codex"}), runtimecore.ApprovalRequest{
		Source: "codex", RequestID: "approval-auto", Kind: runtimecore.ApprovalKindTool, ToolName: "custom_lookup", Metadata: map[string]string{"effects": "read", "risk": "low"},
	}, func(response runtimecore.ApprovalResponse) error {
		responseCh <- response
		return nil
	})
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	select {
	case response := <-responseCh:
		if response.Decision != runtimecore.ApprovalDecisionApproved {
			t.Fatalf("response = %#v, want approved", response)
		}
	default:
		t.Fatal("auto mode did not respond immediately")
	}
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-auto").Events, "approval-auto")
	if !ok || event.Status != "approved" || event.Metadata["approval_hil_mode"] != DiscussionHILModeAuto || event.Metadata["approval_decided_by"] != "runtime:ai_reviewer" || event.Metadata["approval_decision_source"] != "ai_reviewer" || event.Metadata["approval_reviewer_risk"] != "low" || event.Metadata["approval_reviewer_latency_ms"] != "42" {
		t.Fatalf("auto approval event = %+v, ok=%v", event, ok)
	}
	coordinator.mu.Lock()
	pendingCount := len(coordinator.pending)
	coordinator.mu.Unlock()
	if pendingCount != 0 || len(realtime.eventsByType(RealtimeEventDiscussionSnapshotInvalidated)) == 0 {
		t.Fatalf("pending approvals = %d, realtime events = %+v", pendingCount, realtime.events)
	}
}

func TestRuntimeApprovalCoordinatorAutoApprovesCommandInManualMode(t *testing.T) {
	repo := newMemoryRepository(t)
	policy := runtimeapproval.DefaultPolicy()
	policy.Exec.Allowlist = []runtimeapproval.ExecRule{{RuleID: "pwd", Executable: "pwd", WorkspaceOnly: true}}
	hilService := &recordingRuntimeHILService{}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo, Clock: fixedClock{now: testTime}, ApprovalHoldTimeout: time.Hour,
		HIL: hilService, Decisions: runtimeapproval.NewDecisionService(runtimeapproval.StaticPolicyProvider{Policy: policy}, nil),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := repo.SaveConversation(ctx, ConversationRecord{TenantID: "tenant-1", ConversationID: "conv-manual-allow", HILMode: DiscussionHILModeManual, Title: "manual", Status: "running", CreatedBy: "local-user", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	responseCh := make(chan runtimecore.ApprovalResponse, 1)
	err := coordinator.RequestApproval(contextWithRuntimeApproval(ctx, runtimeApprovalContext{ConversationID: "conv-manual-allow", RunID: "run-manual-allow", AgentID: "agent-1", RuntimeKind: "pi"}), runtimecore.ApprovalRequest{
		Source: "pi", RequestID: "approval-manual-allow", Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command", Metadata: map[string]string{"command": "pwd"}, WorkspaceRoot: "/workspace",
	}, func(response runtimecore.ApprovalResponse) error {
		responseCh <- response
		return nil
	})
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	select {
	case response := <-responseCh:
		if response.Decision != runtimecore.ApprovalDecisionApproved {
			t.Fatalf("response = %+v", response)
		}
	default:
		t.Fatal("manual command did not respond immediately")
	}
	if hilService.Calls() != 0 {
		t.Fatalf("HIL create calls = %d, want 0", hilService.Calls())
	}
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-manual-allow").Events, "approval-manual-allow")
	if !ok || event.Status != "approved" || event.Metadata["approval_decision_source"] != "command_execution_no_approval" || event.Metadata["approval_rule_id"] != "pwd" || event.Metadata["hil_decision_id"] != "" {
		t.Fatalf("allowlist approval event = %+v, ok=%v", event, ok)
	}
}

func TestRuntimeApprovalCoordinatorAuditsNeverPolicy(t *testing.T) {
	repo := newMemoryRepository(t)
	policy := runtimeapproval.DefaultPolicy()
	policy.AgentOverrides = []runtimeapproval.AgentOverride{{
		LogicalAgentID: "personal-assistant-ding",
		PolicyOverride: runtimeapproval.PolicyOverride{
			ApprovalPolicy: runtimeapproval.ApprovalPolicyNever,
		},
	}}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo,
		Clock:      fixedClock{now: testTime},
		Decisions:  runtimeapproval.NewDecisionService(runtimeapproval.StaticPolicyProvider{Policy: policy}, nil),
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-never", ConversationKind: ConversationKindPersonalAssistant,
		HILMode: DiscussionHILModeAuto, Title: "never", Status: "running", CreatedBy: "ding", CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	responseCh := make(chan runtimecore.ApprovalResponse, 1)
	err := coordinator.RequestApproval(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-never", RunID: "run-never", AgentID: "lead-agent",
		LogicalAgentID: "personal-assistant-ding", ConversationKind: ConversationKindPersonalAssistant, RuntimeKind: "pi",
	}), runtimecore.ApprovalRequest{
		Source: "pi", RequestID: "approval-never", Kind: runtimecore.ApprovalKindCommandExecution,
		ToolName: "exec_command", Metadata: map[string]string{"command": "rm -rf ./workspace"},
	}, func(response runtimecore.ApprovalResponse) error {
		responseCh <- response
		return nil
	})
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	select {
	case response := <-responseCh:
		if response.Decision != runtimecore.ApprovalDecisionApproved {
			t.Fatalf("response = %+v", response)
		}
	default:
		t.Fatal("never policy did not respond immediately")
	}
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-never").Events, "approval-never")
	if !ok || event.Status != "approved" ||
		event.Metadata["approval_policy"] != string(runtimeapproval.ApprovalPolicyNever) ||
		event.Metadata["approval_reviewer"] != string(runtimeapproval.ApprovalsReviewerAI) ||
		event.Metadata["approval_decision_source"] != "command_execution_no_approval" {
		t.Fatalf("never approval event = %+v, ok=%v", event, ok)
	}
}

func TestRuntimeApprovalCoordinatorHonorsConversationNoApprovalMode(t *testing.T) {
	repo := newMemoryRepository(t)
	policy := runtimeapproval.DefaultPolicy()
	policy.Tools.Allowlist = []runtimeapproval.ToolRule{{
		RuleID: "custom-read", ToolID: "custom_lookup", Effects: []string{"read"}, Risk: "low",
	}}
	hilService := &recordingRuntimeHILService{}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo,
		Clock:      fixedClock{now: testTime},
		HIL:        hilService,
		Decisions:  runtimeapproval.NewDecisionService(runtimeapproval.StaticPolicyProvider{Policy: policy}, nil),
	})
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID: "conv-none", HILMode: DiscussionHILModeNone, Title: "none",
		Status: "running", CreatedBy: "owner", CreatedAt: testTime, UpdatedAt: testTime,
	}); err != nil {
		t.Fatal(err)
	}
	responseCh := make(chan runtimecore.ApprovalResponse, 1)
	err := coordinator.RequestApproval(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-none", RunID: "run-none", AgentID: "agent-1", RuntimeKind: "pi",
	}), runtimecore.ApprovalRequest{
		Source: "pi", RequestID: "approval-none", Kind: runtimecore.ApprovalKindTool,
		ToolName: "custom_lookup", Metadata: map[string]string{"effects": "read", "risk": "low"},
	}, func(response runtimecore.ApprovalResponse) error {
		responseCh <- response
		return nil
	})
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	select {
	case response := <-responseCh:
		if response.Decision != runtimecore.ApprovalDecisionApproved {
			t.Fatalf("response = %+v", response)
		}
	default:
		t.Fatal("no-approval mode did not respond immediately")
	}
	if hilService.Calls() != 0 {
		t.Fatalf("HIL create calls = %d, want 0", hilService.Calls())
	}
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-none").Events, "approval-none")
	if !ok || event.Status != "approved" ||
		event.Metadata["approval_effective_hil_mode"] != DiscussionHILModeAuto ||
		event.Metadata["approval_policy"] != string(runtimeapproval.ApprovalPolicyNever) ||
		event.Metadata["approval_reviewer"] != string(runtimeapproval.ApprovalsReviewerAI) ||
		event.Metadata["approval_decision_source"] != "approval_policy_never" ||
		event.Metadata["hil_decision_id"] != "" {
		t.Fatalf("no-approval event = %+v, ok=%v", event, ok)
	}
}

func TestRuntimeApprovalCoordinatorSendsReviewerFailureToHumanWithoutReturningError(t *testing.T) {
	repo := newMemoryRepository(t)
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo,
		Clock:      fixedClock{now: testTime},
		Decisions: runtimeApprovalDecisionServiceFunc(func(context.Context, runtimeapproval.DecisionInput) runtimeapproval.Decision {
			return runtimeapproval.Decision{
				Outcome: runtimeapproval.DecisionOutcomeHuman, EffectiveHIL: runtimeapproval.HILModeAuto,
				PolicySource: runtimeapproval.ResolutionSource{HILMode: "conversation_explicit", Exec: "platform_default", Tools: "platform_default"},
				PolicyDigest: "policy-digest", DecisionSource: "ai_reviewer", Reason: "tool_allowlist_match", InputDigest: "input-digest",
				Review: &runtimeapproval.ReviewResult{Decision: runtimeapproval.ReviewDecisionAsk, Risk: "unknown", ReasonCode: "reviewer_timeout", ModelProfile: "reviewer-profile", PromptVersion: runtimeapproval.ReviewerPromptVersion},
			}
		}),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-review-timeout", HILMode: DiscussionHILModeAuto, Title: "timeout", Status: "running", CreatedBy: "local-user", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	responseCh := make(chan runtimecore.ApprovalResponse, 1)
	err := coordinator.RequestApproval(contextWithRuntimeApproval(ctx, runtimeApprovalContext{ConversationID: "conv-review-timeout", RunID: "run-review-timeout", AgentID: "agent-1", RuntimeKind: "pi"}), runtimecore.ApprovalRequest{
		Source: "pi", RequestID: "approval-review-timeout", Kind: runtimecore.ApprovalKindTool, ToolName: "custom_lookup", Metadata: map[string]string{"effects": "read", "risk": "low"},
	}, func(response runtimecore.ApprovalResponse) error {
		responseCh <- response
		return nil
	})
	if err != nil {
		t.Fatalf("RequestApproval returned reviewer failure: %v", err)
	}
	select {
	case response := <-responseCh:
		t.Fatalf("reviewer timeout responded automatically: %+v", response)
	default:
	}
	event, ok := findRuntimeApprovalEvent(mustConversationView(t, repo, "conv-review-timeout").Events, "approval-review-timeout")
	if !ok || event.Status != "pending" || event.Metadata["approval_decision_source"] != "ai_reviewer" || event.Metadata["approval_reviewer_reason"] != "reviewer_timeout" {
		t.Fatalf("reviewer timeout event = %+v, ok=%v", event, ok)
	}
}

func TestRuntimeApprovalCoordinatorPassesLogicalIdentityAndConversationKind(t *testing.T) {
	repo := newMemoryRepository(t)
	var captured runtimeapproval.DecisionInput
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo,
		Clock:      fixedClock{now: testTime},
		Decisions: runtimeApprovalDecisionServiceFunc(func(_ context.Context, input runtimeapproval.DecisionInput) runtimeapproval.Decision {
			captured = input
			return runtimeapproval.Decision{Outcome: runtimeapproval.DecisionOutcomeHuman, EffectiveHIL: runtimeapproval.HILModeAuto, DecisionSource: "human"}
		}),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := repo.SaveConversation(ctx, ConversationRecord{ConversationID: "conv-logical", ConversationKind: ConversationKindPersonalAssistant, HILMode: DiscussionHILModeAuto, Title: "assistant", Status: "running", CreatedBy: "human-1", CreatedAt: testTime, UpdatedAt: testTime}); err != nil {
		t.Fatal(err)
	}
	err := coordinator.RequestApproval(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-logical", RunID: "run-logical", AgentID: "lead-agent", LogicalAgentID: "assistant:human-1", ConversationKind: ConversationKindPersonalAssistant, RuntimeKind: "pi",
	}), runtimecore.ApprovalRequest{Source: "pi", RequestID: "approval-logical", Kind: runtimecore.ApprovalKindTool, ToolName: "custom_lookup"}, func(runtimecore.ApprovalResponse) error { return nil })
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if captured.LogicalAgentID != "assistant:human-1" || captured.ConversationKind != ConversationKindPersonalAssistant || captured.ExplicitConversationHIL != DiscussionHILModeAuto {
		t.Fatalf("decision input = %+v", captured)
	}
}
