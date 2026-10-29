package assembly

import (
	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
)

func scopeKindToAgentMemoryKind(k memcore.ScopeKind) agentcore.MemoryKind {
	switch k {
	case memcore.ScopeOrganization:
		return agentcore.MemoryKindOrganization
	case memcore.ScopeDepartment, memcore.ScopeProject:
		return agentcore.MemoryKindProject
	case memcore.ScopeMission:
		return agentcore.MemoryKindMission
	case memcore.ScopeDiscussion:
		return agentcore.MemoryKindDiscussion
	default:
		return agentcore.MemoryKindCollective
	}
}
