package main

import (
	"context"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	mempageindex "orrery_v2/internal/memory/pageindex"
	memstore "orrery_v2/internal/memory/store"
)

type canonicalMemoryProfileDirectory interface {
	QueryDirectory(context.Context, agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
	GetProfile(context.Context, agentcore.AgentProfileID) (agentcore.AgentProfile, error)
	SaveProfile(context.Context, agentcore.AgentProfile) error
}

type canonicalMemoryProfileStore interface {
	ReassignProfile(context.Context, agentcore.MemoryProfileID, agentcore.MemoryProfileID) (memstore.ProfileReassignmentReport, error)
}

type canonicalMemoryPageIndexState interface {
	ConsolidateProfile(context.Context, agentcore.MemoryProfileID, agentcore.MemoryProfileID) (mempageindex.ProfileConsolidationReport, error)
}

type canonicalMemoryProfileMigrationReport struct {
	ProfilesMoved              int
	RecordsMoved               int
	PageIndexBindingsMoved     int
	PageIndexBindingsRecreated int
}

func migrateCanonicalAgentMemoryProfiles(ctx context.Context, profiles canonicalMemoryProfileDirectory, memory canonicalMemoryProfileStore, pageIndex canonicalMemoryPageIndexState) (canonicalMemoryProfileMigrationReport, error) {
	report := canonicalMemoryProfileMigrationReport{}
	if profiles == nil || memory == nil {
		return report, nil
	}
	const pageLimit = 200
	for offset := 0; ; offset += pageLimit {
		page, err := profiles.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
			Placement:                 agentcore.TalentPlacementFilterAll,
			IncludeAI:                 true,
			IncludeHumans:             true,
			IncludePersonalAssistants: true,
			Page:                      agentcore.Page{Limit: pageLimit, Offset: offset},
		})
		if err != nil {
			return report, fmt.Errorf("list profiles for memory identity migration: %w", err)
		}
		for _, entry := range page.Entries {
			if !isEmployeeNumberProfileID(entry.AgentProfileID.String()) {
				continue
			}
			profile, err := profiles.GetProfile(ctx, entry.AgentProfileID)
			if err != nil {
				return report, fmt.Errorf("load profile %s for memory identity migration: %w", entry.AgentProfileID, err)
			}
			currentID := profile.PersonalMemoryProfileRef.MemoryProfileID
			if currentID == "" {
				continue
			}
			targetID := memcore.ScopeMemoryProfileID(memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: profile.ID.String()})
			if currentID == targetID {
				continue
			}
			if pageIndex != nil {
				stateReport, err := pageIndex.ConsolidateProfile(ctx, currentID, targetID)
				if err != nil {
					return report, fmt.Errorf("consolidate PageIndex memory profile %s into %s: %w", currentID, targetID, err)
				}
				report.PageIndexBindingsMoved += stateReport.DocumentBindingsMoved
				report.PageIndexBindingsRecreated += stateReport.DocumentBindingsRemoved
			}
			memoryReport, err := memory.ReassignProfile(ctx, currentID, targetID)
			if err != nil {
				return report, fmt.Errorf("migrate memory profile %s into %s: %w", currentID, targetID, err)
			}
			profile.PersonalMemoryProfileRef.MemoryProfileID = targetID
			profile.PersonalMemoryProfileRef.OwnerAgentID = agentcore.AgentID(profile.ID)
			if err := profiles.SaveProfile(ctx, profile); err != nil {
				return report, fmt.Errorf("save canonical memory reference for %s: %w", profile.ID, err)
			}
			report.ProfilesMoved++
			report.RecordsMoved += memoryReport.RecordsMoved
		}
		if len(page.Entries) < pageLimit || offset+len(page.Entries) >= page.Total {
			return report, nil
		}
	}
}

func isEmployeeNumberProfileID(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != 7 || (value[0] != 'A' && value[0] != 'H') {
		return false
	}
	for _, ch := range value[1:] {
		if ch < '0' || ch > '9' {
			return false
		}
	}
	return true
}
