package main

import (
	"context"
	"errors"
	"orrery_v2/internal/agent/llm"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	profilemodel "orrery_v2/internal/agent/runtime/providers/profilemodel"
	"orrery_v2/internal/app/runtimeapproval"
	httptransport "orrery_v2/internal/transport/http"
	"sync"
	"testing"
)

type runtimeApprovalSettingsStub struct {
	mu       sync.RWMutex
	settings httptransport.SettingsDTO
	err      error
}

func (stub *runtimeApprovalSettingsStub) GetSettings(context.Context) (httptransport.SettingsDTO, error) {
	stub.mu.RLock()
	defer stub.mu.RUnlock()
	return stub.settings, stub.err
}

func (stub *runtimeApprovalSettingsStub) setPolicy(policy runtimeapproval.Policy) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.settings.RuntimeApprovals = policy
}

type runtimeApprovalModelFactoryFunc func(context.Context, profilemodel.ModelRequest) (llm.ChatModel, error)

func (f runtimeApprovalModelFactoryFunc) ModelForRequest(ctx context.Context, request profilemodel.ModelRequest) (llm.ChatModel, error) {
	return f(ctx, request)
}

type runtimeApprovalChatModel struct {
	generate func(context.Context, []*llm.Message, ...llm.Option) (*llm.Message, error)
}

func (chat runtimeApprovalChatModel) Generate(ctx context.Context, messages []*llm.Message, options ...llm.Option) (*llm.Message, error) {
	return chat.generate(ctx, messages, options...)
}

func (runtimeApprovalChatModel) Stream(context.Context, []*llm.Message, ...llm.Option) (*llm.MessageStream, error) {
	return nil, errors.New("stream is not used by the reviewer")
}

func TestProductionRuntimeApprovalLoadsCurrentSettingsPerDecision(t *testing.T) {
	settings := &runtimeApprovalSettingsStub{settings: httptransport.DefaultSettings()}
	settings.settings.RuntimeApprovals.Exec.Allowlist = []runtimeapproval.ExecRule{{RuleID: "pwd", Executable: "pwd"}}
	service := newProductionRuntimeApprovalDecisionServiceWithModels(settings, nil)
	request := runtimecore.ApprovalRequest{Kind: runtimecore.ApprovalKindCommandExecution, ToolName: "exec_command", Metadata: map[string]string{"command": "pwd"}}

	first := service.Decide(context.Background(), runtimeapproval.DecisionInput{Request: request})
	if first.Outcome != runtimeapproval.DecisionOutcomeApproveOnce {
		t.Fatalf("first decision = %+v", first)
	}
	policy := settings.settings.RuntimeApprovals
	policy.Exec.Security = runtimeapproval.SecurityDeny
	settings.setPolicy(policy)
	second := service.Decide(context.Background(), runtimeapproval.DecisionInput{Request: request})
	if second.Outcome != runtimeapproval.DecisionOutcomeDeny {
		t.Fatalf("second decision = %+v", second)
	}
}

func TestProductionRuntimeApprovalSkipsHumanReviewWhenDisabled(t *testing.T) {
	settings := httptransport.DefaultSettings()
	settings.HILDefaultEnabled = false
	settings.RuntimeApprovals.ForcedHuman = runtimeapproval.ForcedHumanPolicy{ToolIDs: []string{"publish_contract"}}
	service := newProductionRuntimeApprovalDecisionServiceWithModels(&runtimeApprovalSettingsStub{settings: settings}, nil)

	decision := service.Decide(context.Background(), runtimeapproval.DecisionInput{
		Request: runtimecore.ApprovalRequest{
			Kind: runtimecore.ApprovalKindTool, ToolName: "publish_contract",
			Metadata: map[string]string{"approval_force_manual": "true", "risk": "critical", "effects": "publish"},
		},
		ExplicitConversationHIL: "manual",
	})
	if decision.Outcome != runtimeapproval.DecisionOutcomeApproveOnce || decision.DecisionSource != "approval_policy_never" {
		t.Fatalf("decision = %+v", decision)
	}
	if decision.EffectiveHIL != runtimeapproval.HILModeAuto || decision.EffectiveApprovalPolicy != runtimeapproval.ApprovalPolicyNever {
		t.Fatalf("effective policy = %+v", decision)
	}
}

func TestProductionRuntimeApprovalSelectsConfiguredReviewerProfile(t *testing.T) {
	var captured profilemodel.ModelRequest
	chat := runtimeApprovalChatModel{generate: func(context.Context, []*llm.Message, ...llm.Option) (*llm.Message, error) {
		return llm.AssistantMessage(`{"decision":"allow","risk":"low","rationale":"Read-only inspection."}`, nil), nil
	}}
	completion := productionRuntimeApprovalCompletion{models: runtimeApprovalModelFactoryFunc(func(_ context.Context, request profilemodel.ModelRequest) (llm.ChatModel, error) {
		captured = request
		return chat, nil
	})}
	_, err := completion.Complete(context.Background(), runtimeapproval.CompletionRequest{LLMProfileID: "approval-reviewer", SystemPrompt: "system", UserPrompt: "user", MaxTokens: 360})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if captured.LLMProfileID != "approval-reviewer" || captured.Model != "" {
		t.Fatalf("model request = %+v", captured)
	}
}

func TestProductionRuntimeApprovalPassesTemperatureAndMaxTokens(t *testing.T) {
	var options llm.GenerationOptions
	var messages []*llm.Message
	chat := runtimeApprovalChatModel{generate: func(_ context.Context, input []*llm.Message, opts ...llm.Option) (*llm.Message, error) {
		messages = input
		options = llm.Apply(opts...)
		return llm.AssistantMessage(`{"decision":"ask","risk":"unknown","rationale":"Ask a human."}`, nil), nil
	}}
	completion := productionRuntimeApprovalCompletion{models: runtimeApprovalModelFactoryFunc(func(context.Context, profilemodel.ModelRequest) (llm.ChatModel, error) {
		return chat, nil
	})}
	_, err := completion.Complete(context.Background(), runtimeapproval.CompletionRequest{LLMProfileID: "reviewer", SystemPrompt: "system", UserPrompt: "user", Temperature: 0, MaxTokens: 321})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(messages) != 2 || messages[0].Role != llm.System || messages[1].Role != llm.User {
		t.Fatalf("messages = %+v", messages)
	}
	if options.Temperature == nil || *options.Temperature != 0 || options.MaxTokens == nil || *options.MaxTokens != 321 {
		t.Fatalf("model options = %+v", options)
	}
}

func TestProductionRuntimeApprovalModelFailureReturnsAsk(t *testing.T) {
	completion := productionRuntimeApprovalCompletion{models: runtimeApprovalModelFactoryFunc(func(context.Context, profilemodel.ModelRequest) (llm.ChatModel, error) {
		return nil, errors.New("model unavailable")
	})}
	result := runtimeapproval.NewReviewer(completion).Review(context.Background(), runtimeapproval.ReviewerSettings{LLMProfileID: "reviewer", TimeoutMS: 30000, MaxTokens: 360}, runtimeapproval.ReviewInput{Kind: runtimeapproval.ReviewKindTool, ToolID: "lookup"})
	if result.Decision != runtimeapproval.ReviewDecisionAsk || result.ReasonCode != "reviewer_error" {
		t.Fatalf("review result = %+v", result)
	}
}
