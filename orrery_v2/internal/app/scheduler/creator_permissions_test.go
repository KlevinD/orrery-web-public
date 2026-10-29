package scheduler

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
)

func TestAgentProfileCreatorPermissionResolverLoadsCurrentProfilePermissions(t *testing.T) {
	store := fakeAgentProfilePermissionSource{profile: agentcore.AgentProfile{
		ID: agentcore.AgentProfileID("human-operator"),
		UserAccount: agentcore.UserAccountSpec{
			PermissionScope: []string{"scheduler.write", "memory.review"},
		},
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "org-1"},
			DepartmentRef:   agentcore.DepartmentRef{ID: "dept-1"},
		},
		PersonalMemoryProfileRef: agentcore.MemoryProfileRef{OwnerAgentID: "operator"},
		ToolRefs: []agentcore.ToolRef{{
			ToolID:          "scheduled_task.create",
			PermissionScope: "memory.distill",
		}},
	}}
	resolver := NewAgentProfileCreatorPermissionResolver(store)

	permissions, err := resolver.ResolveCreatorPermissions(context.Background(), CreatorRef{
		ViewerID:  "operator",
		ProfileID: "human-operator",
	})
	if err != nil {
		t.Fatal(err)
	}

	if permissions.AgentID != "operator" || !containsTestString(permissions.PermissionScope, "memory.distill") || !containsTestString(permissions.PermissionScope, "memory.review") {
		t.Fatalf("permissions = %+v", permissions)
	}
	if len(permissions.OrganizationIDs) != 1 || permissions.OrganizationIDs[0] != "org-1" || len(permissions.DepartmentIDs) != 1 || permissions.DepartmentIDs[0] != "dept-1" {
		t.Fatalf("permission scopes = %+v", permissions)
	}
}

func TestAgentProfileCreatorPermissionResolverResolvesSystemPrincipalWithoutWorkforceProfile(t *testing.T) {
	resolver := NewAgentProfileCreatorPermissionResolver(fakeAgentProfilePermissionSource{err: ErrServiceDependency})

	permissions, err := resolver.ResolveCreatorPermissions(context.Background(), CreatorRef{
		ViewerID:   "system:orrery-native-memory-migration",
		ViewerKind: ViewerKindSystem,
		ProfileID:  agentcore.SystemSuperadminPrincipalID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if permissions.AgentID != "system:orrery-native-memory-migration" ||
		!containsTestString(permissions.PermissionScope, "memory.distill") ||
		!containsTestString(permissions.PermissionScope, "memory.review") {
		t.Fatalf("permissions = %+v", permissions)
	}
}

func TestAgentProfileCreatorPermissionResolverDoesNotGrantSystemScopesToNonSystemViewer(t *testing.T) {
	resolver := NewAgentProfileCreatorPermissionResolver(fakeAgentProfilePermissionSource{err: ErrServiceDependency})

	_, err := resolver.ResolveCreatorPermissions(context.Background(), CreatorRef{
		ViewerID:   "operator",
		ViewerKind: ViewerKindUser,
		ProfileID:  agentcore.SystemSuperadminPrincipalID,
	})
	if err != ErrServiceDependency {
		t.Fatalf("error = %v, want service dependency from profile lookup", err)
	}
}

type fakeAgentProfilePermissionSource struct {
	profile agentcore.AgentProfile
	err     error
}

func (s fakeAgentProfilePermissionSource) GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	if s.err != nil {
		return agentcore.AgentProfile{}, s.err
	}
	if id != s.profile.ID {
		return agentcore.AgentProfile{}, ErrInvalidRequest
	}
	return s.profile, nil
}
