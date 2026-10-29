package scheduler

import (
	"context"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/userauth"
)

type AgentProfilePermissionSource interface {
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}

type AgentProfileCreatorPermissionResolver struct {
	profiles AgentProfilePermissionSource
}

func NewAgentProfileCreatorPermissionResolver(profiles AgentProfilePermissionSource) *AgentProfileCreatorPermissionResolver {
	return &AgentProfileCreatorPermissionResolver{profiles: profiles}
}

func (r *AgentProfileCreatorPermissionResolver) ResolveCreatorPermissions(ctx context.Context, ref CreatorRef) (CreatorPermissions, error) {
	if r == nil || r.profiles == nil {
		return CreatorPermissions{}, ErrServiceDependency
	}
	profileID := strings.TrimSpace(ref.ProfileID)
	if profileID == "" {
		return CreatorPermissions{}, ErrInvalidRequest
	}
	if ref.ViewerKind == ViewerKindSystem && userauth.IsSystemPrincipalID(profileID) {
		return CreatorPermissions{
			PermissionScope: userauth.SuperadminScopes(),
			AgentID:         strings.TrimSpace(ref.ViewerID),
		}, nil
	}
	profile, err := r.profiles.GetProfile(ctx, agentcore.AgentProfileID(profileID))
	if err != nil {
		return CreatorPermissions{}, err
	}
	if profile.ID.IsZero() {
		return CreatorPermissions{}, ErrInvalidRequest
	}
	permissions := cleanStringList(profile.UserAccount.PermissionScope)
	for _, toolRef := range profile.ToolRefs {
		permissions = appendMissingString(permissions, toolRef.PermissionScope)
	}
	return CreatorPermissions{
		PermissionScope: permissions,
		AgentID:         firstNonBlank(profile.PersonalMemoryProfileRef.OwnerAgentID.String(), ref.ViewerID),
		OrganizationIDs: cleanStringList([]string{string(profile.OrgIdentity.OrganizationRef.ID)}),
		DepartmentIDs:   cleanStringList([]string{string(profile.OrgIdentity.DepartmentRef.ID)}),
	}, nil
}

func appendMissingString(values []string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return values
	}
	for _, existing := range values {
		if strings.TrimSpace(existing) == value {
			return values
		}
	}
	return append(values, value)
}
