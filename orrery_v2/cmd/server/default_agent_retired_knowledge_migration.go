package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	knowledgecore "orrery_v2/internal/knowledge/core"
)

type retiredKnowledgeProfileDirectory interface {
	ListDeletedProfileIDs(context.Context) ([]agentcore.AgentProfileID, error)
}

type retiredKnowledgeStore interface {
	ListKnowledgeBases(context.Context, knowledgecore.KnowledgeBaseQuery) (knowledgecore.KnowledgeBasePage, error)
	ListBindings(context.Context, knowledgecore.BindingQuery) ([]knowledgecore.KnowledgeBinding, error)
	ListDocuments(context.Context, knowledgecore.DocumentQuery) (knowledgecore.DocumentPage, error)
	SaveKnowledgeBase(context.Context, knowledgecore.KnowledgeBase) error
}

type retiredKnowledgeMigrationReport struct {
	Scanned  int
	Disabled int
}

func disableRetiredEmptyKnowledgeBases(ctx context.Context, profiles retiredKnowledgeProfileDirectory, knowledge retiredKnowledgeStore, now func() time.Time) (retiredKnowledgeMigrationReport, error) {
	report := retiredKnowledgeMigrationReport{}
	if profiles == nil || knowledge == nil {
		return report, nil
	}
	if now == nil {
		now = time.Now
	}
	deletedIDs, err := profiles.ListDeletedProfileIDs(ctx)
	if err != nil {
		return report, fmt.Errorf("list retired knowledge owners: %w", err)
	}
	deleted := make(map[string]struct{}, len(deletedIDs))
	for _, id := range deletedIDs {
		deleted[id.String()] = struct{}{}
	}
	if len(deleted) == 0 {
		return report, nil
	}
	page, err := knowledge.ListKnowledgeBases(ctx, knowledgecore.KnowledgeBaseQuery{
		OwnerType: knowledgecore.OwnerAgent,
		Status:    knowledgecore.KnowledgeActive,
	})
	if err != nil {
		return report, fmt.Errorf("list knowledge bases for retired owner migration: %w", err)
	}
	for _, base := range page.Items {
		ownerID := strings.TrimSpace(base.OwnerID)
		if base.Status != knowledgecore.KnowledgeActive || base.OwnerType != knowledgecore.OwnerAgent || ownerID == "" {
			continue
		}
		report.Scanned++
		if _, retired := deleted[ownerID]; !retired {
			continue
		}
		documents, err := knowledge.ListDocuments(ctx, knowledgecore.DocumentQuery{KnowledgeBaseID: base.ID, Limit: 1})
		if err != nil {
			return report, fmt.Errorf("check retired knowledge documents %s: %w", base.ID, err)
		}
		if len(documents.Items) > 0 {
			continue
		}
		bindings, err := knowledge.ListBindings(ctx, knowledgecore.BindingQuery{KnowledgeBaseID: base.ID})
		if err != nil {
			return report, fmt.Errorf("check retired knowledge bindings %s: %w", base.ID, err)
		}
		if len(bindings) > 0 {
			continue
		}
		base.Status = knowledgecore.KnowledgeDisabled
		base.UpdatedAt = now().UTC()
		if err := knowledge.SaveKnowledgeBase(ctx, base); err != nil {
			return report, fmt.Errorf("disable retired knowledge base %s: %w", base.ID, err)
		}
		report.Disabled++
	}
	return report, nil
}
