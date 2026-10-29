package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/knowledgeadmin"
	knowledgecore "orrery_v2/internal/knowledge/core"
)

type defaultKnowledgeEnsurer interface {
	EnsureDefaultPersonalKnowledgeBase(ctx context.Context, viewer knowledgeadmin.ViewerRef, cmd knowledgeadmin.EnsureDefaultCommand) (knowledgeadmin.KnowledgeBaseDTO, error)
}

type defaultKnowledgeBaseLookup interface {
	GetKnowledgeBase(ctx context.Context, id knowledgecore.KnowledgeBaseID) (knowledgecore.KnowledgeBase, error)
}

func ensureDefaultCoordinatorKnowledgeBases(ctx context.Context, profiles *agentcore.PlatformProfileRepository, knowledge defaultKnowledgeEnsurer, bases defaultKnowledgeBaseLookup, vectorRoot string) error {
	if profiles == nil || knowledge == nil || bases == nil {
		return nil
	}
	for offset := 0; ; {
		page, err := profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
			EnabledOnly: true, IncludeAI: true, IncludeHumans: true, IncludePersonalAssistants: true,
			Page: agentcore.Page{Limit: 200, Offset: offset},
		})
		if err != nil {
			return err
		}
		for _, entry := range page.Entries {
			profile, err := profiles.GetProfile(ctx, entry.AgentProfileID)
			if err != nil {
				return fmt.Errorf("load agent %s while ensuring default knowledge: %w", entry.AgentProfileID, err)
			}
			knowledgeBaseID := strings.TrimSpace(profile.PersonalKnowledgeBaseID)
			if knowledgeBaseID == "" && !isDefaultSystemWorkflowEntry(entry) {
				continue
			}
			ownerType := knowledgecore.OwnerAgent
			if profile.AgentKind == agentcore.AgentKindHuman {
				ownerType = knowledgecore.OwnerHuman
			}
			if knowledgeBaseID != "" {
				base, lookupErr := bases.GetKnowledgeBase(ctx, knowledgecore.KnowledgeBaseID(knowledgeBaseID))
				if lookupErr == nil && base.OwnerType == ownerType && strings.TrimSpace(base.OwnerID) == profile.ID.String() {
					continue
				}
				if lookupErr != nil && !errors.Is(lookupErr, knowledgecore.ErrNotFound) {
					return fmt.Errorf("verify default knowledge for agent %s: %w", entry.AgentProfileID, lookupErr)
				}
			}
			base, err := ensureDefaultKnowledgeBase(ctx, knowledge, ownerType, profile.ID.String(), vectorRoot)
			if err != nil {
				return fmt.Errorf("ensure default knowledge for agent %s: %w", entry.AgentProfileID, err)
			}
			if strings.TrimSpace(base.ID) == "" {
				return fmt.Errorf("ensure default knowledge for agent %s returned an empty id", entry.AgentProfileID)
			}
			if _, _, err := profiles.MutateProfile(ctx, profile.ID, func(current agentcore.AgentProfile) (agentcore.AgentProfile, bool, error) {
				if strings.TrimSpace(current.PersonalKnowledgeBaseID) == strings.TrimSpace(base.ID) {
					return current, false, nil
				}
				current.PersonalKnowledgeBaseID = strings.TrimSpace(base.ID)
				return current, true, nil
			}); err != nil {
				return fmt.Errorf("bind default knowledge for agent %s: %w", entry.AgentProfileID, err)
			}
		}
		offset += len(page.Entries)
		if len(page.Entries) == 0 || offset >= page.Total {
			return nil
		}
	}
}

func ensureDefaultKnowledgeBase(ctx context.Context, knowledge defaultKnowledgeEnsurer, ownerType knowledgecore.OwnerType, ownerID string, vectorRoot string) (knowledgeadmin.KnowledgeBaseDTO, error) {
	if knowledge == nil {
		return knowledgeadmin.KnowledgeBaseDTO{}, nil
	}
	ownerID = strings.TrimSpace(ownerID)
	if ownerID == "" {
		return knowledgeadmin.KnowledgeBaseDTO{}, nil
	}
	return knowledge.EnsureDefaultPersonalKnowledgeBase(ctx, knowledgeBootstrapViewer(), knowledgeadmin.EnsureDefaultCommand{
		OwnerType:  string(ownerType),
		OwnerID:    ownerID,
		VectorRoot: vectorRoot,
	})
}

func knowledgeBootstrapViewer() knowledgeadmin.ViewerRef {
	return knowledgeadmin.ViewerRef{
		ViewerID:        "knowledge-bootstrap",
		PermissionScope: []string{knowledgeadmin.PermissionKnowledgeManage},
	}
}

func isDefaultSystemWorkflowEntry(entry agentcore.AgentDirectoryEntry) bool {
	return entry.AgentKind == agentcore.AgentKindAI &&
		entry.OrgIdentity.OrganizationRef.ID == agentcore.OrganizationID(defaultCoordinatorOrganizationID) &&
		entry.OrgIdentity.EmploymentType == agentcore.EmploymentTypeSystem
}
