package ontology

import (
	"context"
	"fmt"

	"orrery_v2/internal/platform/graphprotocol"
)

func RegisterMappingRuleProjections(ctx context.Context, registry Registry, projections *graphprotocol.ProjectionRegistry, tenantID string) error {
	if projections == nil {
		return fmt.Errorf("%w: projection registry is required", ErrInvalidDefinition)
	}
	compiled, err := CompileMappingRuleProjections(ctx, registry, tenantID)
	if err != nil {
		return err
	}
	for _, projection := range compiled {
		if err := projections.Register(ctx, projection); err != nil {
			return err
		}
	}
	return nil
}

func SyncMappingRuleProjections(ctx context.Context, registry Registry, projections *graphprotocol.ProjectionRegistry) error {
	if projections == nil {
		return fmt.Errorf("%w: projection registry is required", ErrInvalidDefinition)
	}
	compiled, err := CompileMappingRuleProjections(ctx, registry, "")
	if err != nil {
		return err
	}
	return projections.ReplaceAll(ctx, compiled)
}

func CompileMappingRuleProjections(ctx context.Context, registry Registry, tenantID string) ([]graphprotocol.GraphProjection, error) {
	if registry == nil {
		return nil, fmt.Errorf("%w: ontology registry is required", ErrInvalidDefinition)
	}
	rules, err := registry.ListMappingRules(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	compiled := make([]graphprotocol.GraphProjection, 0, len(rules))
	for _, rule := range rules {
		if !rule.Enabled {
			continue
		}
		projection, err := CompileMappingRuleProjection(rule)
		if err != nil {
			return nil, err
		}
		compiled = append(compiled, projection)
	}
	return compiled, nil
}
