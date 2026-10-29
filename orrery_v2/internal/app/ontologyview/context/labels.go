package contextview

import (
	"context"
	"strings"

	"orrery_v2/internal/app/ontology"
	ontologycontract "orrery_v2/internal/app/ontologyview/contract"
)

func (s *Service) populateRelationLabels(ctx context.Context, tenantID string, items []RelationItem, budget *ontologycontract.BudgetTracker) error {
	labels, err := s.linkTypeLabels(ctx, tenantID, relationLinkTypes(items), budget)
	if err != nil {
		return err
	}
	for index := range items {
		items[index].LinkTypeLabel = labels[items[index].LinkType]
	}
	return nil
}

func (s *Service) populateNeighborhoodEdgeLabels(ctx context.Context, tenantID string, edges []NeighborhoodEdge, budget *ontologycontract.BudgetTracker) error {
	ids := make([]string, 0, len(edges))
	for _, edge := range edges {
		ids = append(ids, edge.LinkType)
	}
	labels, err := s.linkTypeLabels(ctx, tenantID, ids, budget)
	if err != nil {
		return err
	}
	for index := range edges {
		edges[index].LinkTypeLabel = labels[edges[index].LinkType]
	}
	return nil
}

func relationLinkTypes(items []RelationItem) []string {
	ids := make([]string, 0, len(items))
	for _, item := range items {
		ids = append(ids, item.LinkType)
	}
	return ids
}

func (s *Service) linkTypeLabels(ctx context.Context, tenantID string, ids []string, budget *ontologycontract.BudgetTracker) (map[string]string, error) {
	unique := make([]string, 0, len(ids))
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		unique = append(unique, id)
	}
	labels := make(map[string]string, len(unique))
	if len(unique) == 0 {
		return labels, nil
	}
	if err := budget.ConsumeStoreReads(1); err != nil {
		return nil, err
	}
	definitions, err := s.definitions.GetRegistryDefinitions(ctx, ontology.RegistryDefinitionLink, unique)
	if err != nil {
		return nil, dependencyError(err)
	}
	tenantID = strings.TrimSpace(tenantID)
	for _, definition := range definitions {
		if definition.LinkType == nil || definition.TenantID != "" && definition.TenantID != tenantID {
			continue
		}
		label := strings.TrimSpace(definition.LinkType.DisplayName)
		if label != "" {
			labels[definition.ID] = label
		}
	}
	for _, id := range unique {
		if labels[id] == "" {
			labels[id] = id
		}
	}
	return labels, nil
}
