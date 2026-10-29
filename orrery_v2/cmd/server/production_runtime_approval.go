package main

import (
	"context"
	"fmt"
	"orrery_v2/internal/agent/llm"
	profilemodel "orrery_v2/internal/agent/runtime/providers/profilemodel"
	"orrery_v2/internal/app/runtimeapproval"
	httptransport "orrery_v2/internal/transport/http"
	"strings"
)

type productionRuntimeApprovalSettings interface {
	GetSettings(context.Context) (httptransport.SettingsDTO, error)
}

type productionRuntimeApprovalPolicyProvider struct {
	settings productionRuntimeApprovalSettings
}

func (provider productionRuntimeApprovalPolicyProvider) RuntimeApprovalPolicy(ctx context.Context) (runtimeapproval.Policy, error) {
	if provider.settings == nil {
		return runtimeapproval.Policy{}, fmt.Errorf("runtime approval settings are not configured")
	}
	settings, err := provider.settings.GetSettings(ctx)
	if err != nil {
		return runtimeapproval.Policy{}, err
	}
	policy := runtimeapproval.Normalize(settings.RuntimeApprovals)
	if !settings.HILDefaultEnabled {
		policy = runtimeapproval.WithoutHumanReview(policy)
	}
	return policy, nil
}

type productionRuntimeApprovalCompletion struct {
	models profilemodel.ModelFactory
}

func (completion productionRuntimeApprovalCompletion) Complete(ctx context.Context, request runtimeapproval.CompletionRequest) (string, error) {
	if completion.models == nil {
		return "", fmt.Errorf("runtime approval model factory is not configured")
	}
	chatModel, err := completion.models.ModelForRequest(ctx, profilemodel.ModelRequest{LLMProfileID: strings.TrimSpace(request.LLMProfileID)})
	if err != nil {
		return "", fmt.Errorf("select runtime approval reviewer model: %w", err)
	}
	if chatModel == nil {
		return "", fmt.Errorf("runtime approval reviewer model is unavailable")
	}
	message, err := chatModel.Generate(ctx, []*llm.Message{
		llm.SystemMessage(request.SystemPrompt),
		llm.UserMessage(request.UserPrompt),
	}, llm.WithTemperature(request.Temperature), llm.WithMaxTokens(request.MaxTokens))
	if err != nil {
		return "", fmt.Errorf("generate runtime approval review: %w", err)
	}
	if message == nil || strings.TrimSpace(message.Content) == "" {
		return "", fmt.Errorf("runtime approval reviewer returned an empty response")
	}
	return strings.TrimSpace(message.Content), nil
}

func newProductionRuntimeApprovalDecisionService(settings productionSettingsStore, secrets platformSecretResolver) *runtimeapproval.DecisionService {
	models := profilemodel.NewProfileModelFactory(profilemodel.ProfileModelFactoryConfig{
		Profiles: settings,
		Secrets:  secrets,
	})
	return newProductionRuntimeApprovalDecisionServiceWithModels(settings, models)
}

func newProductionPersonalAssistantApprovalCompletion(settings productionSettingsStore, secrets platformSecretResolver) runtimeapproval.Completion {
	return productionRuntimeApprovalCompletion{models: profilemodel.NewProfileModelFactory(profilemodel.ProfileModelFactoryConfig{
		Profiles: settings, Secrets: secrets,
	})}
}

func newProductionRuntimeApprovalDecisionServiceWithModels(settings productionRuntimeApprovalSettings, models profilemodel.ModelFactory) *runtimeapproval.DecisionService {
	provider := productionRuntimeApprovalPolicyProvider{settings: settings}
	reviewer := runtimeapproval.NewReviewer(productionRuntimeApprovalCompletion{models: models})
	return runtimeapproval.NewDecisionService(provider, reviewer)
}
