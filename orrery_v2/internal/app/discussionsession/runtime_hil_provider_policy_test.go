package discussionsession

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/runtimeapproval"
)

func TestDiscussionAgentRequestNormalizesLegacyEmptyHILMode(t *testing.T) {
	request := discussionAgentRequestFromConversation(
		ConversationRecordView{ConversationRecord: ConversationRecord{
			ConversationID:   "conv-legacy-hil",
			ConversationKind: "direct",
			CreatedBy:        "owner",
		}},
		RunRecord{RunID: "run-legacy-hil", AgentID: "agent-1"},
		ViewerRef{ViewerID: "owner", ViewerKind: ViewerKindUser},
		MessageRecord{MessageID: "msg-legacy-hil", Content: "inspect"},
		nil,
	)

	if request.HILMode != DiscussionHILModeManual {
		t.Fatalf("request HIL mode = %q, want %q", request.HILMode, DiscussionHILModeManual)
	}
}

func TestManualConversationForcesOnRequestRuntimePolicy(t *testing.T) {
	policy := agentcore.RuntimeWorkspacePolicy{
		ApprovalPolicy: string(agentcore.RuntimeApprovalNever),
		Profile: agentcore.RuntimeSandboxProfile{
			Approval: agentcore.RuntimeApprovalNever,
		},
	}

	manual := applyDiscussionHILRuntimeApprovalPolicy(policy, DiscussionAgentRequest{
		ConversationID: "conv-manual",
		HILMode:        DiscussionHILModeManual,
	})
	if manual.ApprovalPolicy != string(agentcore.RuntimeApprovalOnRequest) || manual.Profile.Approval != agentcore.RuntimeApprovalOnRequest {
		t.Fatalf("manual runtime policy = %+v", manual)
	}

	auto := applyDiscussionHILRuntimeApprovalPolicy(policy, DiscussionAgentRequest{
		ConversationID: "conv-auto",
		HILMode:        DiscussionHILModeAuto,
	})
	if auto.ApprovalPolicy != string(agentcore.RuntimeApprovalNever) || auto.Profile.Approval != agentcore.RuntimeApprovalNever {
		t.Fatalf("auto runtime policy changed = %+v", auto)
	}

	onRequest := agentcore.RuntimeWorkspacePolicy{
		ApprovalPolicy: string(agentcore.RuntimeApprovalOnRequest),
		Profile: agentcore.RuntimeSandboxProfile{
			Approval: agentcore.RuntimeApprovalOnRequest,
		},
	}
	none := applyDiscussionHILRuntimeApprovalPolicy(onRequest, DiscussionAgentRequest{
		ConversationID: "conv-none",
		HILMode:        DiscussionHILModeNone,
	})
	if none.ApprovalPolicy != string(agentcore.RuntimeApprovalNever) || none.Profile.Approval != agentcore.RuntimeApprovalNever {
		t.Fatalf("no-approval runtime policy = %+v", none)
	}
}

func TestDiscussionAgentInputWritesServerOwnedManualHILMarker(t *testing.T) {
	runner := NewBaseAgentDiscussionAgentRunner(BaseAgentDiscussionAgentRunnerOptions{
		AgentID:     "agent-1",
		RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindPi},
	})
	input, err := runner.agentInput(context.Background(), DiscussionAgentRequest{
		ConversationID: "conv-manual-marker",
		MessageID:      "msg-manual-marker",
		RunID:          "run-manual-marker",
		HILMode:        DiscussionHILModeManual,
		Content:        "inspect",
	})
	if err != nil {
		t.Fatalf("agentInput: %v", err)
	}
	if got := input.RuntimeOptions.ProviderSpecific[agentcore.RuntimeConversationHILModeProviderKey]; got != DiscussionHILModeManual {
		t.Fatalf("conversation HIL marker = %q, want %q", got, DiscussionHILModeManual)
	}
	if input.RuntimeOptions.WorkspacePolicy.ApprovalPolicy != string(agentcore.RuntimeApprovalOnRequest) || input.RuntimeOptions.WorkspacePolicy.Profile.Approval != agentcore.RuntimeApprovalOnRequest {
		t.Fatalf("manual runtime policy = %+v", input.RuntimeOptions.WorkspacePolicy)
	}

	autoInput, err := runner.agentInput(context.Background(), DiscussionAgentRequest{
		ConversationID: "conv-auto-marker",
		MessageID:      "msg-auto-marker",
		RunID:          "run-auto-marker",
		HILMode:        DiscussionHILModeAuto,
		Content:        "inspect",
		ProviderSpecific: map[string]string{
			agentcore.RuntimeConversationHILModeProviderKey: DiscussionHILModeManual,
		},
	})
	if err != nil {
		t.Fatalf("auto agentInput: %v", err)
	}
	if got := autoInput.RuntimeOptions.ProviderSpecific[agentcore.RuntimeConversationHILModeProviderKey]; got != "" {
		t.Fatalf("caller-controlled conversation HIL marker survived = %q", got)
	}
}

func TestRuntimeApprovalCoordinatorPassesLegacyVisibleManualAsExplicit(t *testing.T) {
	repo := newMemoryRepository(t)
	ctx := context.Background()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID:   "conv-visible-manual",
		ConversationKind: "direct",
		CreatedBy:        "owner",
		Status:           "running",
		CreatedAt:        testTime,
		UpdatedAt:        testTime,
	}); err != nil {
		t.Fatal(err)
	}

	var captured runtimeapproval.DecisionInput
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo,
		Clock:      fixedClock{now: testTime},
		Decisions: runtimeApprovalDecisionServiceFunc(func(_ context.Context, input runtimeapproval.DecisionInput) runtimeapproval.Decision {
			captured = input
			return runtimeapproval.Decision{
				Outcome:                    runtimeapproval.DecisionOutcomeApproveOnce,
				EffectiveHIL:               runtimeapproval.HILModeManual,
				EffectiveApprovalPolicy:    runtimeapproval.ApprovalPolicyOnRequest,
				EffectiveApprovalsReviewer: runtimeapproval.ApprovalsReviewerHuman,
			}
		}),
	})

	err := coordinator.RequestApproval(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID: "conv-visible-manual",
		RunID:          "run-visible-manual",
		AgentID:        "agent-1",
		RuntimeKind:    "pi",
	}), runtimecore.ApprovalRequest{
		Source:    "pi",
		RequestID: "approval-visible-manual",
		Kind:      runtimecore.ApprovalKindTool,
		ToolName:  "mcp_server_browser_take_screenshot",
	}, func(runtimecore.ApprovalResponse) error { return nil })
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	if captured.ExplicitConversationHIL != DiscussionHILModeManual {
		t.Fatalf("explicit conversation HIL = %q, want %q", captured.ExplicitConversationHIL, DiscussionHILModeManual)
	}
}

func TestLegacyEmptyDirectConversationAutoApprovesScreenshotWithoutHIL(t *testing.T) {
	repo := newMemoryRepository(t)
	policy := runtimeapproval.DefaultPolicy()
	personalTools := *policy.ConversationDefaults[ConversationKindPersonalAssistant].Tools
	policy.AgentOverrides = []runtimeapproval.AgentOverride{{
		LogicalAgentID: "assistant:H000001",
		PolicyOverride: runtimeapproval.PolicyOverride{
			HILMode:           runtimeapproval.HILModeAuto,
			ApprovalPolicy:    runtimeapproval.ApprovalPolicyNever,
			ApprovalsReviewer: runtimeapproval.ApprovalsReviewerAI,
			Tools:             &personalTools,
		},
	}}
	hilService := &recordingRuntimeHILService{result: hil.CreateDecisionResult{Decision: hil.DecisionRequest{
		ID:        "hil-browser-screenshot",
		TenantID:  "tenant-hil",
		Status:    hil.DecisionPending,
		Route:     hil.RouteDecision{Mode: hil.ModeHIL},
		ExpiresAt: testTime.Add(time.Hour),
	}}}
	coordinator := NewRuntimeApprovalCoordinator(RuntimeApprovalCoordinatorOptions{
		Repository: repo,
		Clock:      fixedClock{now: testTime},
		HIL:        hilService,
		Decisions:  runtimeapproval.NewDecisionService(runtimeapproval.StaticPolicyProvider{Policy: policy}, nil),
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := repo.SaveConversation(ctx, ConversationRecord{
		ConversationID:   "conv-direct-screenshot",
		TenantID:         "tenant-hil",
		ConversationKind: "direct",
		CreatedBy:        "H000001",
		Status:           "running",
		CreatedAt:        testTime,
		UpdatedAt:        testTime,
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveRun(ctx, RunRecord{
		RunID:          "run-direct-screenshot",
		ConversationID: "conv-direct-screenshot",
		AgentID:        "A000001",
		LogicalAgentID: "assistant:H000001",
		Status:         "running",
		RuntimeKind:    "pi",
		CreatedAt:      testTime,
		StartedAt:      testTime,
	}); err != nil {
		t.Fatal(err)
	}

	responses := make(chan runtimecore.ApprovalResponse, 1)
	err := coordinator.RequestApproval(contextWithRuntimeApproval(ctx, runtimeApprovalContext{
		ConversationID:   "conv-direct-screenshot",
		RunID:            "run-direct-screenshot",
		AgentID:          "A000001",
		LogicalAgentID:   "assistant:H000001",
		ConversationKind: "direct",
		RuntimeKind:      "pi",
		AssignedTo:       "H000001",
	}), runtimecore.ApprovalRequest{
		Source:    "pi",
		RequestID: "approval-direct-screenshot",
		Kind:      runtimecore.ApprovalKindTool,
		ToolName:  "mcp_server_browser_take_screenshot",
	}, func(response runtimecore.ApprovalResponse) error {
		responses <- response
		return nil
	})
	if err != nil {
		t.Fatalf("RequestApproval: %v", err)
	}
	select {
	case response := <-responses:
		if response.Decision != runtimecore.ApprovalDecisionApproved {
			t.Fatalf("screenshot response = %+v, want approved", response)
		}
	default:
		t.Fatal("system screenshot remained pending")
	}
	if hilService.Calls() != 0 {
		t.Fatalf("HIL create calls = %d, want 0", hilService.Calls())
	}
	event, ok := findRuntimeApprovalEventForRun(mustConversationView(t, repo, "conv-direct-screenshot").Events, "run-direct-screenshot", "approval-direct-screenshot")
	if !ok || event.Status != "approved" ||
		event.Metadata["approval_effective_hil_mode"] != DiscussionHILModeManual ||
		event.Metadata["approval_policy"] != string(runtimeapproval.ApprovalPolicyOnRequest) ||
		event.Metadata["approval_reviewer"] != string(runtimeapproval.ApprovalsReviewerHuman) ||
		event.Metadata["approval_decision_source"] != "allowlist" || event.Metadata["hil_decision_id"] != "" {
		t.Fatalf("approved screenshot event = %+v, ok=%v", event, ok)
	}
}
