package assembly

import (
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/resource/tool"
)

func toolPrincipal(agentID agentcore.AgentID, permissions []string) tool.Principal {
	return tool.Principal{
		AgentID:     agentID,
		Permissions: append([]string(nil), permissions...),
	}
}
