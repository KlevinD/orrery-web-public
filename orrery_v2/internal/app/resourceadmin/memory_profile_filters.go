package resourceadmin

import (
	"strings"

	memcore "orrery_v2/internal/memory/core"
)

func matchesMemoryProfileFilter(req ListResourcesRequest, profile memcore.MemoryProfile) bool {
	tags := append([]string(nil), profile.RetrievalPolicy.Tags...)
	tags = append(tags,
		string(profile.OwnerRef.AgentID),
		profile.OwnerRef.OrganizationID,
		profile.OwnerRef.DepartmentID,
		profile.OwnerRef.ProjectID,
		string(profile.MemoryKind),
		string(profile.DefaultScope.Kind),
		profile.DefaultScope.ID,
	)
	if !matchesResourceFilter(req, "memory_profile", string(profile.ID), string(profile.OwnerRef.AgentID), string(profile.MemoryKind), tags) {
		return false
	}
	if !matchesResourceID(req.ResourceID, string(profile.ID)) {
		return false
	}
	if req.Enabled != nil && profile.Enabled != *req.Enabled {
		return false
	}
	if category := strings.TrimSpace(req.Category); category != "" {
		if !strings.EqualFold(string(profile.MemoryKind), category) && !strings.EqualFold(string(profile.DefaultScope.Kind), category) && !hasStringFold(profile.RetrievalPolicy.Tags, category) {
			return false
		}
	}
	return true
}

func memoryProfileBelongsToAgent(profile memcore.MemoryProfile, agentID string) bool {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return true
	}
	if strings.EqualFold(string(profile.OwnerRef.AgentID), agentID) {
		return true
	}
	return profile.DefaultScope.Kind == memcore.ScopeAgent && strings.EqualFold(profile.DefaultScope.ID, agentID)
}

func agentIDsForMemoryProfile(profile memcore.MemoryProfile) []string {
	ids := []string{}
	ids = appendUnique(ids, string(profile.OwnerRef.AgentID))
	if profile.DefaultScope.Kind == memcore.ScopeAgent {
		ids = appendUnique(ids, profile.DefaultScope.ID)
	}
	return ids
}
