package core

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

// ScopeMemoryProfileID returns the stable profile ID used for a memory scope.
func ScopeMemoryProfileID(scope MemoryScope) agentcore.MemoryProfileID {
	id := profileIDSegment(scope.ID)
	if id == "" {
		return ""
	}
	if scope.Kind == ScopeAgent {
		return agentcore.MemoryProfileID("memory-" + id)
	}
	if !IsCollectiveScope(scope.Kind) {
		return ""
	}
	return agentcore.MemoryProfileID("memory-" + string(scope.Kind) + "-" + id)
}

func IsCollectiveScope(kind ScopeKind) bool {
	switch kind {
	case ScopeOrganization, ScopeDepartment, ScopeProject, ScopeMission, ScopeDiscussion:
		return true
	default:
		return false
	}
}

func MemoryKindForScope(kind ScopeKind) agentcore.MemoryKind {
	switch kind {
	case ScopeAgent:
		return agentcore.MemoryKindPersonal
	case ScopeOrganization:
		return agentcore.MemoryKindOrganization
	case ScopeProject:
		return agentcore.MemoryKindProject
	case ScopeMission:
		return agentcore.MemoryKindMission
	case ScopeDiscussion:
		return agentcore.MemoryKindDiscussion
	case ScopeDepartment:
		return agentcore.MemoryKindCollective
	default:
		return ""
	}
}

func NewScopeMemoryProfile(scope MemoryScope, owner OwnerRef) MemoryProfile {
	scope.ID = strings.TrimSpace(scope.ID)
	kind := MemoryKindForScope(scope.Kind)
	profile := MemoryProfile{
		ID:                ScopeMemoryProfileID(scope),
		OwnerRef:          owner,
		MemoryKind:        kind,
		DefaultScope:      scope,
		RetrievalPolicy:   RetrievalPolicy{MaxRecords: 100, Tags: []string{"collective", string(scope.Kind)}},
		CompressionPolicy: CompressionPolicy{MaxSummaryBytes: 8192},
		VisibilityPolicy:  VisibilityPolicy{Mode: VisibilityScopedMembers, ScopeKind: scope.Kind, ScopeID: scope.ID},
		Enabled:           scope.ID != "" && kind.IsValid(),
	}
	if scope.Kind == ScopeAgent {
		if profile.OwnerRef.AgentID == "" {
			profile.OwnerRef.AgentID = agentcore.AgentID(scope.ID)
		}
		profile.RetrievalPolicy = RetrievalPolicy{MaxRecords: 20}
		profile.CompressionPolicy = CompressionPolicy{MaxSummaryBytes: 4096}
		profile.VisibilityPolicy = VisibilityPolicy{Mode: VisibilityOwnerOnly, ScopeKind: ScopeAgent, ScopeID: scope.ID}
	}
	return profile
}

func MemoryProfileMatchesScope(profile MemoryProfile, scope MemoryScope) bool {
	return profile.DefaultScope.Kind == scope.Kind && strings.EqualFold(strings.TrimSpace(profile.DefaultScope.ID), strings.TrimSpace(scope.ID))
}

func MemoryProfileRefForScope(profile MemoryProfile) agentcore.MemoryProfileRef {
	return agentcore.MemoryProfileRef{
		MemoryProfileID: profile.ID,
		MemoryKind:      profile.MemoryKind,
		AccessScope:     string(profile.DefaultScope.Kind) + ":" + profile.DefaultScope.ID,
	}
}

func profileIDSegment(value string) string {
	value = strings.TrimSpace(value)
	return strings.NewReplacer("/", "_", "\\", "_", " ", "_").Replace(value)
}
