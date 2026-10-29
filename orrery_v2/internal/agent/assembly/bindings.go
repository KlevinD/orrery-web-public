package assembly

import (
	"context"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	"orrery_v2/internal/resource/skill"
	"orrery_v2/internal/resource/tool"
)

type ToolResolveRequest = tool.ResolveRequest

type ToolResolver interface {
	Resolve(ctx context.Context, req ToolResolveRequest) (tool.ResolvedTool, error)
}

type SkillResolveRequest = skill.ResolveRequest

type SkillResolver interface {
	Resolve(ctx context.Context, req SkillResolveRequest) (skill.ResolvedSkill, error)
}

type MemoryBindRequest struct {
	AgentID               agentcore.AgentID
	MissionID             string
	TaskID                string
	Subject               memcore.AccessSubject
	PersonalProfileRef    agentcore.MemoryProfileRef
	CollectiveProfileRefs []agentcore.MemoryProfileRef
}

type MemoryBinding struct {
	PersonalDigest    memcore.MemoryDigest
	CollectiveDigests []memcore.MemoryDigest
	Warnings          []MemoryBindingWarning
}

type MemoryBindingWarning struct {
	Code    string
	Message string
}

type MemoryBinder interface {
	Bind(ctx context.Context, req MemoryBindRequest) (MemoryBinding, error)
}
