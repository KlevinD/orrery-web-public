package main

import (
	"context"
	"fmt"

	agentcore "orrery_v2/internal/agent/core"
)

const legacyBusinessGraphTenantCaseAlias = "Minvia"

type retiredPrincipalGraphState interface {
	DeleteTenant(context.Context, string) (int, error)
}

type legacyBusinessGraphState interface {
	ReassignTenant(context.Context, string, string) (int, error)
}

func reconcileRetiredPrincipalOntologyTenants(ctx context.Context, graph retiredPrincipalGraphState) error {
	if graph == nil {
		return fmt.Errorf("retired principal graph state is required")
	}
	for _, tenantID := range []string{
		agentcore.LegacySuperadminAgentProfileID,
		agentcore.LegacyLocalAdminAgentProfileID,
		agentcore.LegacyLocalUserAgentProfileID,
	} {
		if _, err := graph.DeleteTenant(ctx, tenantID); err != nil {
			return fmt.Errorf("remove retired principal ontology tenant %s: %w", tenantID, err)
		}
	}
	return nil
}

func reconcileLegacyBusinessGraphAliases(ctx context.Context, graph legacyBusinessGraphState) error {
	if graph == nil {
		return fmt.Errorf("legacy business graph state is required")
	}
	if _, err := graph.ReassignTenant(ctx, legacyBusinessGraphTenantCaseAlias, defaultBusinessOrganizationID); err != nil {
		return fmt.Errorf("normalize business ontology tenant %s: %w", legacyBusinessGraphTenantCaseAlias, err)
	}
	return nil
}
