package core

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestNewScopeMemoryProfileBuildsCollectiveProfile(t *testing.T) {
	profile := NewScopeMemoryProfile(MemoryScope{Kind: ScopeDepartment, ID: "dept-1"}, OwnerRef{OrganizationID: "org-1", DepartmentID: "dept-1"})
	if profile.ID != "memory-department-dept-1" || profile.MemoryKind != agentcore.MemoryKindCollective {
		t.Fatalf("profile identity = %+v", profile)
	}
	if profile.VisibilityPolicy.Mode != VisibilityScopedMembers || profile.VisibilityPolicy.ScopeID != "dept-1" || !profile.Enabled {
		t.Fatalf("profile visibility = %+v", profile.VisibilityPolicy)
	}
	ref := MemoryProfileRefForScope(profile)
	if ref.MemoryProfileID != profile.ID || ref.AccessScope != "department:dept-1" {
		t.Fatalf("profile ref = %+v", ref)
	}
}

func TestNewScopeMemoryProfilePreservesPersonalDefaults(t *testing.T) {
	profile := NewScopeMemoryProfile(MemoryScope{Kind: ScopeAgent, ID: "agent-1"}, OwnerRef{})
	if profile.ID != "memory-agent-1" || profile.MemoryKind != agentcore.MemoryKindPersonal || profile.OwnerRef.AgentID != "agent-1" {
		t.Fatalf("personal profile = %+v", profile)
	}
	if profile.VisibilityPolicy.Mode != VisibilityOwnerOnly || profile.RetrievalPolicy.MaxRecords != 20 {
		t.Fatalf("personal profile policy = %+v", profile)
	}
}
