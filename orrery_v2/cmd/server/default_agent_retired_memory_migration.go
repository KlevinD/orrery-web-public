package main

import (
	"context"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
)

type retiredMemoryProfileDirectory interface {
	ListDeletedProfileIDs(context.Context) ([]agentcore.AgentProfileID, error)
}

type retiredMemoryProfileStore interface {
	ListProfiles(context.Context) ([]memcore.MemoryProfile, error)
	FindRecords(context.Context, memstore.RecordQuery) ([]memcore.MemoryRecord, error)
	SaveProfile(context.Context, memcore.MemoryProfile) error
}

type retiredMemoryProfileMigrationReport struct {
	Scanned  int
	Disabled int
}

func disableRetiredEmptyMemoryProfiles(ctx context.Context, profiles retiredMemoryProfileDirectory, memory retiredMemoryProfileStore) (retiredMemoryProfileMigrationReport, error) {
	report := retiredMemoryProfileMigrationReport{}
	if profiles == nil || memory == nil {
		return report, nil
	}
	items, err := memory.ListProfiles(ctx)
	if err != nil {
		return report, fmt.Errorf("list memory profiles for retired owner migration: %w", err)
	}
	deletedIDs, err := profiles.ListDeletedProfileIDs(ctx)
	if err != nil {
		return report, fmt.Errorf("list retired memory owners: %w", err)
	}
	deleted := make(map[agentcore.AgentProfileID]struct{}, len(deletedIDs))
	for _, id := range deletedIDs {
		deleted[id] = struct{}{}
	}
	for _, profile := range items {
		ownerID := strings.TrimSpace(profile.OwnerRef.AgentID.String())
		if !profile.Enabled || ownerID == "" || isEmployeeNumberProfileID(ownerID) {
			continue
		}
		report.Scanned++
		if _, retired := deleted[agentcore.AgentProfileID(ownerID)]; !retired {
			continue
		}
		records, err := memory.FindRecords(ctx, memstore.RecordQuery{
			ProfileIDs:      []agentcore.MemoryProfileID{profile.ID},
			IncludeArchived: true,
			Limit:           1,
		})
		if err != nil {
			return report, fmt.Errorf("check retired memory profile %s: %w", profile.ID, err)
		}
		if len(records) > 0 {
			continue
		}
		profile.Enabled = false
		if err := memory.SaveProfile(ctx, profile); err != nil {
			return report, fmt.Errorf("disable retired memory profile %s: %w", profile.ID, err)
		}
		report.Disabled++
	}
	return report, nil
}
