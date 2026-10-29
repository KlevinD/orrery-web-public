package main

import (
	"context"
	"errors"
	"orrery_v2/internal/agent/llm"
	profilemodel "orrery_v2/internal/agent/runtime/providers/profilemodel"
	"orrery_v2/internal/app/responsibility"
	"strings"
	"time"
)

const productionResponsibilityAutoplanTimeout = time.Minute

const responsibilityAutoplanModelOutputFallback = "model_output_contract_fallback"

type productionResponsibilityAutoplanPlanner struct {
	primary  responsibility.AutoplanPlanner
	fallback responsibility.AutoplanPlanner
}

func (p productionResponsibilityAutoplanPlanner) PlanAutoplan(ctx context.Context, request responsibility.AutoplanPlanningRequest) (responsibility.AutoplanProposal, error) {
	proposal, err := p.primary.PlanAutoplan(ctx, request)
	if err == nil || !errors.Is(err, responsibility.ErrModelAutoplanOutput) {
		return proposal, err
	}
	proposal, fallbackErr := p.fallback.PlanAutoplan(ctx, request)
	if fallbackErr != nil {
		return responsibility.AutoplanProposal{}, errors.Join(err, fallbackErr)
	}
	proposal.Rationale = strings.TrimSpace(proposal.Rationale + " The model proposal failed strict output validation, so the deterministic read-only baseline was used.")
	proposal.RiskSummary = append(proposal.RiskSummary, responsibilityAutoplanModelOutputFallback)
	return proposal.Normalized(), nil
}

// newProductionResponsibilityAutoplanPlanner keeps model selection separate
// from accountable Agent identity. An empty ModelRequest deliberately selects
// the configured global default LLM profile; the responsibility Agent ID is
// validated only inside the structured plan proposal.
func newProductionResponsibilityAutoplanPlanner(settings productionSettingsStore, secrets platformSecretResolver) (responsibility.AutoplanPlanner, error) {
	profiles := profilemodel.NewProfileModelFactory(profilemodel.ProfileModelFactoryConfig{
		Profiles: settings,
		Secrets:  secrets,
	})
	modelFactory := responsibility.AutoplanChatModelFactoryFunc(func(ctx context.Context) (llm.ChatModel, error) {
		return profiles.ModelForRequest(ctx, profilemodel.ModelRequest{})
	})
	modelPlanner, err := responsibility.NewModelAutoplanPlanner(productionResponsibilityAutoplanPlannerOptions(modelFactory))
	if err != nil {
		return nil, err
	}
	return productionResponsibilityAutoplanPlanner{
		primary:  modelPlanner,
		fallback: responsibility.BaselineAutoplanPlanner{},
	}, nil
}

func productionResponsibilityAutoplanPlannerOptions(modelFactory responsibility.AutoplanChatModelFactory) responsibility.ModelAutoplanPlannerOptions {
	return responsibility.ModelAutoplanPlannerOptions{
		ModelFactory: modelFactory,
		Timeout:      productionResponsibilityAutoplanTimeout,
	}
}
