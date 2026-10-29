package operations

import (
	"context"
	"errors"
	"strings"

	"orrery_v2/internal/app/knowledgeadmin"
	knowledgecore "orrery_v2/internal/knowledge/core"
)

func (s *CommandService) createLifecycleKnowledgeBase(ctx context.Context, viewer ViewerRef, ownerType knowledgecore.OwnerType, ownerID, name, description string, visibility knowledgecore.Visibility) (string, error) {
	if s == nil || s.knowledge == nil || strings.TrimSpace(ownerID) == "" {
		return "", nil
	}
	dto, err := s.knowledge.CreateKnowledgeBase(ctx, operationsKnowledgeViewer(viewer, ownerType, ownerID), knowledgeadmin.CreateKnowledgeBaseCommand{
		Name:        strings.TrimSpace(name),
		Description: strings.TrimSpace(description),
		OwnerType:   string(ownerType),
		OwnerID:     strings.TrimSpace(ownerID),
		Visibility:  string(visibility),
	})
	if err != nil {
		return "", err
	}
	return dto.ID, nil
}

func (s *CommandService) deleteLifecycleKnowledgeBase(ctx context.Context, viewer ViewerRef, ownerType knowledgecore.OwnerType, ownerID, knowledgeBaseID string) error {
	if s == nil || s.knowledge == nil || strings.TrimSpace(knowledgeBaseID) == "" {
		return nil
	}
	return s.knowledge.DeleteKnowledgeBase(ctx, operationsKnowledgeViewer(viewer, ownerType, ownerID), strings.TrimSpace(knowledgeBaseID))
}

func (s *CommandService) cleanupLifecycleKnowledgeBaseOnError(ctx context.Context, viewer ViewerRef, ownerType knowledgecore.OwnerType, ownerID, knowledgeBaseID string, cause error) error {
	if cause == nil {
		return nil
	}
	if strings.TrimSpace(knowledgeBaseID) == "" {
		return cause
	}
	if err := s.deleteLifecycleKnowledgeBase(ctx, viewer, ownerType, ownerID, knowledgeBaseID); err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func profileKnowledgeOwnerType(agentKind string) knowledgecore.OwnerType {
	if strings.TrimSpace(agentKind) == "human" {
		return knowledgecore.OwnerHuman
	}
	return knowledgecore.OwnerAgent
}

func personalKnowledgeBaseName(displayName string) string {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		displayName = "Agent"
	}
	return displayName + " 个人知识库"
}

func organizationKnowledgeBaseName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "组织"
	}
	return name + " 组织知识库"
}

func positionKnowledgeBaseName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "岗位"
	}
	return name + " 岗位知识库"
}
