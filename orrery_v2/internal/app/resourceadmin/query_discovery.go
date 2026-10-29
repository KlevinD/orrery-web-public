package resourceadmin

import (
	"context"

	toolres "orrery_v2/internal/resource/tool"
)

func (s *QueryService) DiscoverMCPServers(ctx context.Context, viewer ViewerRef, req DiscoverMCPRequest) (DiscoverMCPResponseDTO, error) {
	if s == nil || s.discoverer == nil {
		return DiscoverMCPResponseDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceRead); err != nil {
		return DiscoverMCPResponseDTO{}, err
	}
	results, err := s.discoverer.Search(ctx, toolres.DiscoverRequest{Query: req.Query, Limit: req.Limit})
	if err != nil {
		return DiscoverMCPResponseDTO{}, err
	}
	response := DiscoverMCPResponseDTO{Servers: make([]DiscoveredMCPServerDTO, 0, len(results))}
	for _, result := range results {
		response.Servers = append(response.Servers, mapDiscoveredMCP(result))
	}
	return response, nil
}
