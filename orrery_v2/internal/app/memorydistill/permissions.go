package memorydistill

import (
	"strings"

	memcore "orrery_v2/internal/memory/core"
)

const (
	PermissionMemoryDistill   = "memory.distill"
	permissionMemoryAuditRead = "memory.audit.read"
)

type ViewerRef struct {
	ViewerID        string
	ViewerKind      string
	AgentID         string
	OrganizationIDs []string
	DepartmentIDs   []string
	ProjectIDs      []string
	MissionIDs      []string
	DiscussionIDs   []string
	PermissionScope []string
	Language        string
	RequestID       string
	IdempotencyKey  string
}

func (v ViewerRef) Validate() error {
	if strings.TrimSpace(v.ViewerID) == "" {
		return ErrInvalidViewer
	}
	return nil
}

func (v ViewerRef) Can(permission string) bool {
	for _, granted := range v.PermissionScope {
		if strings.TrimSpace(granted) == permission {
			return true
		}
	}
	return false
}

func requirePermission(viewer ViewerRef, permission string) error {
	if err := viewer.Validate(); err != nil {
		return err
	}
	if !viewer.Can(permission) {
		return ErrPermissionDenied
	}
	return nil
}

func viewerHasScope(viewer ViewerRef, kind memcore.ScopeKind, id string) bool {
	id = strings.TrimSpace(id)
	if id == "" {
		return false
	}
	switch kind {
	case memcore.ScopeOrganization:
		return containsString(viewer.OrganizationIDs, id)
	case memcore.ScopeDepartment:
		return containsString(viewer.DepartmentIDs, id)
	case memcore.ScopeProject:
		return containsString(viewer.ProjectIDs, id)
	case memcore.ScopeMission:
		return containsString(viewer.MissionIDs, id)
	case memcore.ScopeDiscussion:
		return containsString(viewer.DiscussionIDs, id)
	case memcore.ScopeAgent:
		return viewer.AgentID != "" && strings.TrimSpace(viewer.AgentID) == id || strings.TrimSpace(viewer.ViewerID) == id
	default:
		return false
	}
}

func canViewRecord(viewer ViewerRef, record memcore.MemoryRecord) bool {
	if !canViewSensitivity(viewer, record.Sensitivity) {
		return false
	}
	return viewer.Can(permissionMemoryAuditRead) || canViewVisibility(viewer, record.Visibility, record.Scope)
}

func canViewSensitivity(viewer ViewerRef, sensitivity memcore.Sensitivity) bool {
	sensitivity = memcore.NormalizeSensitivity(sensitivity)
	return sensitivity == memcore.SensitivityNormal || viewer.Can(permissionMemoryAuditRead)
}

func canViewVisibility(viewer ViewerRef, visibility memcore.Visibility, fallback memcore.MemoryScope) bool {
	switch visibility.Mode {
	case "", memcore.VisibilityOwnerOnly:
		return viewer.AgentID != "" && strings.TrimSpace(viewer.AgentID) == fallback.ID || strings.TrimSpace(viewer.ViewerID) == fallback.ID
	case memcore.VisibilityScopedMembers:
		scope := memcore.MemoryScope{Kind: visibility.ScopeKind, ID: visibility.ScopeID}
		if scope.Kind == "" || strings.TrimSpace(scope.ID) == "" {
			scope = fallback
		}
		return viewerHasScope(viewer, scope.Kind, scope.ID)
	case memcore.VisibilityOrganization:
		scopeID := strings.TrimSpace(visibility.ScopeID)
		if scopeID == "" && fallback.Kind == memcore.ScopeOrganization {
			scopeID = fallback.ID
		}
		return containsString(viewer.OrganizationIDs, scopeID)
	default:
		return false
	}
}

func containsString(values []string, target string) bool {
	target = strings.TrimSpace(target)
	if target == "" {
		return false
	}
	for _, value := range values {
		if strings.TrimSpace(value) == target {
			return true
		}
	}
	return false
}
