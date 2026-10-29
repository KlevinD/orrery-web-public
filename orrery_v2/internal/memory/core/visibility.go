package core

import agentcore "orrery_v2/internal/agent/core"

type VisibilityMode string

const (
	VisibilityOwnerOnly     VisibilityMode = "owner_only"
	VisibilityScopedMembers VisibilityMode = "scoped_members"
	VisibilityOrganization  VisibilityMode = "organization"
)

type VisibilityPolicy struct {
	Mode      VisibilityMode `json:"mode"`
	ScopeKind ScopeKind      `json:"scope_kind,omitempty"`
	ScopeID   string         `json:"scope_id,omitempty"`
}

type Visibility struct {
	Mode      VisibilityMode `json:"mode"`
	ScopeKind ScopeKind      `json:"scope_kind,omitempty"`
	ScopeID   string         `json:"scope_id,omitempty"`
}

type AccessSubject struct {
	AgentID         agentcore.AgentID
	OrganizationIDs []string
	DepartmentIDs   []string
	ProjectIDs      []string
	MissionIDs      []string
	DiscussionIDs   []string
}

func (p MemoryProfile) CanRead(subject AccessSubject) bool {
	if !p.Enabled {
		return false
	}
	switch p.VisibilityPolicy.Mode {
	case "", VisibilityOwnerOnly:
		return p.OwnerRef.AgentID != "" && p.OwnerRef.AgentID == subject.AgentID
	case VisibilityScopedMembers:
		return subjectHasScope(subject, p.VisibilityPolicy.ScopeKind, p.VisibilityPolicy.ScopeID)
	case VisibilityOrganization:
		return contains(subject.OrganizationIDs, p.OwnerRef.OrganizationID)
	default:
		return false
	}
}

func subjectHasScope(subject AccessSubject, kind ScopeKind, id string) bool {
	switch kind {
	case ScopeOrganization:
		return contains(subject.OrganizationIDs, id)
	case ScopeDepartment:
		return contains(subject.DepartmentIDs, id)
	case ScopeProject:
		return contains(subject.ProjectIDs, id)
	case ScopeMission:
		return contains(subject.MissionIDs, id)
	case ScopeDiscussion:
		return contains(subject.DiscussionIDs, id)
	case ScopeAgent:
		return string(subject.AgentID) == id
	default:
		return false
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target && target != "" {
			return true
		}
	}
	return false
}
