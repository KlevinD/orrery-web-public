package reliability

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type RepairDryRunRequest struct {
	ActionRunID string
	TenantID    string
	ArgsHash    string
	Incident    FailureIncident
	Rule        RepairRule
	Target      *RepairTarget
	ObservedAt  time.Time
}

type RepairDryRunResult struct {
	ProviderCallID string
	ResultID       string
	EvidenceID     string
	Checks         []string
}

type RepairDryRunProvider interface {
	Strategy() RepairStrategy
	DryRun(context.Context, RepairDryRunRequest) (RepairDryRunResult, error)
}

type RepairProviderRegistry struct {
	providers map[RepairStrategy]RepairDryRunProvider
}

func NewRepairProviderRegistry(providers ...RepairDryRunProvider) (*RepairProviderRegistry, error) {
	registry := &RepairProviderRegistry{providers: make(map[RepairStrategy]RepairDryRunProvider, len(providers))}
	for _, provider := range providers {
		if provider == nil || !validRepairStrategy(provider.Strategy()) {
			return nil, fmt.Errorf("%w: dry-run provider strategy is invalid", ErrInvalidRequest)
		}
		if _, exists := registry.providers[provider.Strategy()]; exists {
			return nil, fmt.Errorf("%w: duplicate dry-run provider for strategy %s", ErrConflict, provider.Strategy())
		}
		registry.providers[provider.Strategy()] = provider
	}
	return registry, nil
}

func NewDefaultRepairProviderRegistry() *RepairProviderRegistry {
	registry, err := NewRepairProviderRegistry(
		governedRepairDryRunProvider{strategy: RepairStrategyRetry},
		governedRepairDryRunProvider{strategy: RepairStrategyRestart},
		governedRepairDryRunProvider{strategy: RepairStrategyFallback},
	)
	if err != nil {
		panic(err)
	}
	return registry
}

func (r *RepairProviderRegistry) DryRun(ctx context.Context, request RepairDryRunRequest) (RepairDryRunResult, error) {
	if r == nil {
		return RepairDryRunResult{}, ErrProviderUnavailable
	}
	provider, ok := r.providers[request.Rule.Strategy]
	if !ok {
		return RepairDryRunResult{}, fmt.Errorf("%w: no dry-run provider for strategy %s", ErrProviderUnavailable, request.Rule.Strategy)
	}
	result, err := provider.DryRun(ctx, request)
	if err != nil {
		return RepairDryRunResult{}, err
	}
	if strings.TrimSpace(result.ProviderCallID) == "" || strings.TrimSpace(result.ResultID) == "" || strings.TrimSpace(result.EvidenceID) == "" || len(result.Checks) == 0 {
		return RepairDryRunResult{}, fmt.Errorf("%w: dry-run provider returned incomplete evidence", ErrInvalidRequest)
	}
	seenChecks := make(map[string]struct{}, len(result.Checks))
	normalizedChecks := make([]string, len(result.Checks))
	for index, check := range result.Checks {
		check = strings.TrimSpace(check)
		if check == "" {
			return RepairDryRunResult{}, fmt.Errorf("%w: dry-run provider returned an empty evidence check", ErrInvalidRequest)
		}
		if _, duplicate := seenChecks[check]; duplicate {
			return RepairDryRunResult{}, fmt.Errorf("%w: dry-run provider returned duplicate evidence check %s", ErrInvalidRequest, check)
		}
		seenChecks[check] = struct{}{}
		normalizedChecks[index] = check
	}
	result.Checks = normalizedChecks
	return result, nil
}

type governedRepairDryRunProvider struct {
	strategy RepairStrategy
}

func (p governedRepairDryRunProvider) Strategy() RepairStrategy {
	return p.strategy
}

func (p governedRepairDryRunProvider) DryRun(ctx context.Context, request RepairDryRunRequest) (RepairDryRunResult, error) {
	if err := ctx.Err(); err != nil {
		return RepairDryRunResult{}, err
	}
	now := request.ObservedAt.UTC()
	if now.IsZero() {
		return RepairDryRunResult{}, fmt.Errorf("%w: observed_at is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(request.ActionRunID) == "" || strings.TrimSpace(request.TenantID) == "" || strings.TrimSpace(request.ArgsHash) == "" ||
		request.Incident.ID == "" || request.Rule.ID == "" || request.Incident.TenantID != request.TenantID || request.Rule.TenantID != request.TenantID {
		return RepairDryRunResult{}, fmt.Errorf("%w: dry-run identity and tenant binding are required", ErrInvalidRequest)
	}
	if request.Rule.Strategy != p.strategy || request.Rule.SelectorFingerprint == "" || request.Rule.SelectorFingerprint != request.Incident.Fingerprint {
		return RepairDryRunResult{}, fmt.Errorf("%w: repair rule does not match the incident", ErrInvalidRequest)
	}
	if request.Rule.ActionTypeID != "reliability.repair.execute" || !request.Rule.Reversible || !request.Rule.Idempotent {
		return RepairDryRunResult{}, fmt.Errorf("%w: repair rule is outside the governed dry-run contract", ErrInvalidRequest)
	}
	if request.Rule.Status != RepairRuleStatusProposed && request.Rule.Status != RepairRuleStatusActive {
		return RepairDryRunResult{}, fmt.Errorf("%w: repair rule is not eligible for dry-run", ErrInvalidRequest)
	}
	if request.Rule.ExpiresAt != nil && !request.Rule.ExpiresAt.After(now) {
		return RepairDryRunResult{}, fmt.Errorf("%w: repair rule has expired", ErrInvalidRequest)
	}
	if request.Incident.Status != IncidentStatusOpen && request.Incident.Status != IncidentStatusContained && request.Incident.Status != IncidentStatusDiagnosed {
		return RepairDryRunResult{}, fmt.Errorf("%w: failure incident is not eligible for repair dry-run", ErrInvalidRequest)
	}
	checks := []string{"tenant_bound", "selector_matched", "rule_governed", "no_mutation"}
	switch p.strategy {
	case RepairStrategyRetry:
		if !request.Incident.Retryable {
			return RepairDryRunResult{}, fmt.Errorf("%w: incident is not retryable", ErrInvalidRequest)
		}
		if request.Target == nil || request.Target.Kind != RepairTargetMissionAgentRun ||
			request.Target.TenantID != request.TenantID || request.Target.IncidentID != request.Incident.ID ||
			strings.TrimSpace(request.Target.AuthorizationTenantID) == "" || strings.TrimSpace(request.Target.MissionID) == "" || strings.TrimSpace(request.Target.RunID) == "" {
			return RepairDryRunResult{}, fmt.Errorf("%w: retry repair target is not fully bound", ErrInvalidRequest)
		}
		checks = append(checks, "retryable", "repair_target_bound")
	case RepairStrategyRestart, RepairStrategyFallback:
		if strings.TrimSpace(request.Incident.Component) == "" {
			return RepairDryRunResult{}, fmt.Errorf("%w: incident component is required", ErrInvalidRequest)
		}
		checks = append(checks, "component_resolved")
	default:
		return RepairDryRunResult{}, fmt.Errorf("%w: no governed dry-run implementation for strategy %s", ErrProviderUnavailable, p.strategy)
	}
	digestParts := []string{
		request.TenantID, request.ActionRunID, request.Incident.ID, fmt.Sprint(request.Incident.Version),
		request.Rule.ID, fmt.Sprint(request.Rule.Version), string(request.Rule.Strategy), request.ArgsHash,
	}
	return RepairDryRunResult{
		ProviderCallID: "repair-dry-run-provider:" + string(p.strategy) + ":v1",
		ResultID:       stableID("repair-dry-run-result", digestParts...),
		EvidenceID:     stableID("repair-dry-run-evidence", digestParts...),
		Checks:         checks,
	}, nil
}
