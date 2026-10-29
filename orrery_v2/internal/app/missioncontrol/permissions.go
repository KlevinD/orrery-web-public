package missioncontrol

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

type ViewerKind string

const (
	ViewerKindAgent  ViewerKind = "agent"
	ViewerKindHuman  ViewerKind = "human"
	ViewerKindUser   ViewerKind = "user"
	ViewerKindSystem ViewerKind = "system"
)

type ViewerRef struct {
	ViewerID               string
	ViewerKind             ViewerKind
	TenantID               string
	AuthorizationSubjectID string
	OrganizationScope      []string
	MissionRoles           []string
	PermissionScope        []string
}

func (v ViewerRef) Tenant() string {
	value := strings.TrimSpace(v.TenantID)
	if value == "" {
		return "global"
	}
	return value
}

func (v ViewerRef) Validate() error {
	if v.ViewerID == "" || v.ViewerKind == "" {
		return ErrInvalidViewer
	}
	return nil
}

func (v ViewerRef) CanSeeAny(required []string) bool {
	if len(required) == 0 {
		return false
	}
	scope := map[string]struct{}{}
	for _, value := range v.PermissionScope {
		scope[value] = struct{}{}
		if value == "mission.read" {
			scope["mission"] = struct{}{}
		}
	}
	for _, value := range v.MissionRoles {
		scope["role:"+value] = struct{}{}
	}
	for _, value := range required {
		if _, ok := scope[value]; ok {
			return true
		}
	}
	return false
}

func isBreakglassSystemViewer(viewer ViewerRef) bool {
	if viewer.ViewerKind != ViewerKindSystem || strings.TrimSpace(viewer.ViewerID) != agentcore.SystemSuperadminPrincipalID {
		return false
	}
	subjectID := strings.TrimSpace(viewer.AuthorizationSubjectID)
	return subjectID == "" || subjectID == agentcore.SystemSuperadminPrincipalID
}
