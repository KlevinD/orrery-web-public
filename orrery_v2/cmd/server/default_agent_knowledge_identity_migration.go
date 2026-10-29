package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	knowledgecore "orrery_v2/internal/knowledge/core"
)

type canonicalKnowledgeProfileDirectory interface {
	QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
	GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}

type canonicalKnowledgeStore interface {
	GetKnowledgeBase(context.Context, knowledgecore.KnowledgeBaseID) (knowledgecore.KnowledgeBase, error)
	SaveKnowledgeBase(context.Context, knowledgecore.KnowledgeBase) error
}

type canonicalKnowledgeNameMigrationReport struct {
	Renamed int
}

func migrateCanonicalAgentKnowledgeNames(ctx context.Context, profiles canonicalKnowledgeProfileDirectory, knowledge canonicalKnowledgeStore, now func() time.Time) (canonicalKnowledgeNameMigrationReport, error) {
	report := canonicalKnowledgeNameMigrationReport{}
	if profiles == nil || knowledge == nil {
		return report, nil
	}
	if now == nil {
		now = time.Now
	}
	for offset := 0; ; offset += 200 {
		page, err := profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
			EnabledOnly:               true,
			IncludeAI:                 true,
			IncludePersonalAssistants: true,
			Placement:                 agentcore.TalentPlacementFilterAll,
			Page:                      agentcore.Page{Offset: offset, Limit: 200},
		})
		if err != nil {
			return report, fmt.Errorf("list profiles for knowledge name migration: %w", err)
		}
		for _, entry := range page.Entries {
			if !isEmployeeNumberProfileID(entry.AgentProfileID.String()) || !strings.HasPrefix(entry.AgentProfileID.String(), "A") {
				continue
			}
			profile, err := profiles.GetProfile(ctx, entry.AgentProfileID)
			if err != nil {
				return report, fmt.Errorf("load profile %s for knowledge name migration: %w", entry.AgentProfileID, err)
			}
			baseID := knowledgecore.KnowledgeBaseID(strings.TrimSpace(profile.PersonalKnowledgeBaseID))
			if baseID == "" {
				continue
			}
			base, err := knowledge.GetKnowledgeBase(ctx, baseID)
			if errors.Is(err, knowledgecore.ErrNotFound) {
				continue
			}
			if err != nil {
				return report, fmt.Errorf("load personal knowledge %s: %w", baseID, err)
			}
			if base.OwnerType != knowledgecore.OwnerAgent || strings.TrimSpace(base.OwnerID) != profile.ID.String() || !legacyPersonalKnowledgeName(base.Name) {
				continue
			}
			displayName := strings.TrimSpace(profile.DisplayName)
			if displayName == "" {
				displayName = strings.TrimSpace(profile.RealName)
			}
			if displayName == "" {
				continue
			}
			desired := displayName + " 个人知识库"
			if base.Name == desired {
				continue
			}
			base.Name = desired
			base.UpdatedAt = now().UTC()
			if err := knowledge.SaveKnowledgeBase(ctx, base); err != nil {
				return report, fmt.Errorf("rename personal knowledge %s: %w", base.ID, err)
			}
			report.Renamed++
		}
		if len(page.Entries) == 0 || offset+len(page.Entries) >= page.Total {
			return report, nil
		}
	}
}

func legacyPersonalKnowledgeName(name string) bool {
	name = strings.TrimSpace(name)
	return strings.HasSuffix(strings.ToLower(name), " default knowledge") ||
		name == "个人助理 个人知识库" ||
		name == "钢镚 个人知识库"
}
