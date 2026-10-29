package memoryadmin

import (
	"strings"

	memcore "orrery_v2/internal/memory/core"
)

const (
	PermissionMemoryRead        = "memory.read"
	PermissionMemoryWrite       = "memory.write"
	PermissionMemoryReview      = "memory.review"
	PermissionMemoryWorldManage = "memory.world.manage"
	PermissionMemoryAuditRead   = "memory.audit.read"
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
		granted = strings.TrimSpace(granted)
		if granted == permission {
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

func CanReadRecord(viewer ViewerRef, record memcore.MemoryRecord) bool {
	return viewer.Validate() == nil && viewer.Can(PermissionMemoryRead) && canReadRecord(viewer, record)
}
