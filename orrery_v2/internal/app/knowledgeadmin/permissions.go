package knowledgeadmin

import (
	"context"
	"strings"

	"orrery_v2/internal/knowledge/provider"
)

const (
	PermissionKnowledgeRead   = "knowledge.read"
	PermissionKnowledgeWrite  = "knowledge.write"
	PermissionKnowledgeManage = "knowledge.manage"
)

type ViewerRef struct {
	ViewerID        string
	AgentIDs        []string
	HumanIDs        []string
	OrganizationIDs []string
	PositionIDs     []string
	RoleIDs         []string
	PermissionScope []string
	RequestID       string
	IdempotencyKey  string
}

func (v ViewerRef) Validate() error {
	if v.ViewerID == "" {
		return ErrInvalidViewer
	}
	return nil
}

func (v ViewerRef) Can(permission string) bool {
	for _, granted := range v.PermissionScope {
		if granted == permission || granted == PermissionKnowledgeManage {
			return true
		}
	}
	return false
}

func (v ViewerRef) AccessSubject() AccessSubject {
	return AccessSubject{
		ViewerID:        v.ViewerID,
		AgentIDs:        append([]string(nil), v.AgentIDs...),
		HumanIDs:        append([]string(nil), v.HumanIDs...),
		OrganizationIDs: append([]string(nil), v.OrganizationIDs...),
		PositionIDs:     append([]string(nil), v.PositionIDs...),
		RoleIDs:         append([]string(nil), v.RoleIDs...),
	}
}

func withProviderRequestMetadata(ctx context.Context, viewer ViewerRef) context.Context {
	actor := "viewer:" + strings.TrimSpace(viewer.ViewerID)
	if len(viewer.AgentIDs) > 0 && strings.TrimSpace(viewer.AgentIDs[0]) != "" {
		actor = "agent:" + strings.TrimSpace(viewer.AgentIDs[0])
	}
	if len(viewer.HumanIDs) > 0 && strings.TrimSpace(viewer.HumanIDs[0]) != "" {
		actor = "human:" + strings.TrimSpace(viewer.HumanIDs[0])
	}
	return provider.WithRequestMetadata(ctx, provider.RequestMetadata{
		RequestID: viewer.RequestID,
		Actor:     actor,
	})
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
