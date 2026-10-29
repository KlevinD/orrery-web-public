package resourceadmin

import (
	"context"
	"errors"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	toolres "orrery_v2/internal/resource/tool"
)

func (s *QueryService) GetMCPServer(ctx context.Context, viewer ViewerRef, toolID string) (MCPServerDTO, error) {
	if s == nil || s.tools == nil {
		return MCPServerDTO{}, ErrServiceDependency
	}
	if err := requirePermission(viewer, PermissionResourceRead); err != nil {
		return MCPServerDTO{}, err
	}
	id := strings.TrimSpace(toolID)
	if id == "" {
		return MCPServerDTO{}, ErrInvalidRequest
	}
	definition, err := s.tools.Get(ctx, agentcore.ToolID(id))
	if err != nil {
		if errors.Is(err, toolres.ErrNotFound) {
			return MCPServerDTO{}, ErrNotFound
		}
		return MCPServerDTO{}, err
	}
	if definition.Kind != toolres.KindMCPServer || definition.MCP == nil {
		return MCPServerDTO{}, ErrInvalidRequest
	}
	return mapMCP(definition), nil
}
