package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
)

func TestDisableRetiredEmptyMemoryProfilesPreservesLiveAndNonemptyProfiles(t *testing.T) {
	ctx := context.Background()
	profiles := retiredMemoryProfileDirectoryFake{deleted: map[agentcore.AgentProfileID]bool{
		"retired-empty":    true,
		"retired-nonempty": true,
	}}
	memory := &retiredMemoryProfileStoreFake{
		profiles: []memcore.MemoryProfile{
			{ID: "memory-retired-empty", OwnerRef: memcore.OwnerRef{AgentID: "retired-empty"}, Enabled: true},
			{ID: "memory-retired-nonempty", OwnerRef: memcore.OwnerRef{AgentID: "retired-nonempty"}, Enabled: true},
			{ID: "memory-live", OwnerRef: memcore.OwnerRef{AgentID: "live-agent"}, Enabled: true},
			{ID: "memory-A000001", OwnerRef: memcore.OwnerRef{AgentID: "A000001"}, Enabled: true},
		},
		records: map[agentcore.MemoryProfileID][]memcore.MemoryRecord{
			"memory-retired-nonempty": {{ID: "memory-1", ProfileID: "memory-retired-nonempty"}},
		},
	}

	report, err := disableRetiredEmptyMemoryProfiles(ctx, profiles, memory)
	if err != nil {
		t.Fatal(err)
	}
	if report.Scanned != 3 || report.Disabled != 1 {
		t.Fatalf("report = %+v", report)
	}
	if memory.saved["memory-retired-empty"].Enabled {
		t.Fatal("retired empty profile remained enabled")
	}
	if _, changed := memory.saved["memory-retired-nonempty"]; changed {
		t.Fatal("nonempty retired profile was modified")
	}
	if _, changed := memory.saved["memory-live"]; changed {
		t.Fatal("live profile was modified")
	}
}

type retiredMemoryProfileDirectoryFake struct {
	deleted map[agentcore.AgentProfileID]bool
}

func (f retiredMemoryProfileDirectoryFake) ListDeletedProfileIDs(context.Context) ([]agentcore.AgentProfileID, error) {
	ids := make([]agentcore.AgentProfileID, 0, len(f.deleted))
	for id, deleted := range f.deleted {
		if deleted {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

type retiredMemoryProfileStoreFake struct {
	profiles []memcore.MemoryProfile
	records  map[agentcore.MemoryProfileID][]memcore.MemoryRecord
	saved    map[agentcore.MemoryProfileID]memcore.MemoryProfile
}

func (f *retiredMemoryProfileStoreFake) ListProfiles(context.Context) ([]memcore.MemoryProfile, error) {
	return append([]memcore.MemoryProfile(nil), f.profiles...), nil
}

func (f *retiredMemoryProfileStoreFake) FindRecords(_ context.Context, query memstore.RecordQuery) ([]memcore.MemoryRecord, error) {
	if len(query.ProfileIDs) == 0 {
		return nil, nil
	}
	return append([]memcore.MemoryRecord(nil), f.records[query.ProfileIDs[0]]...), nil
}

func (f *retiredMemoryProfileStoreFake) SaveProfile(_ context.Context, profile memcore.MemoryProfile) error {
	if f.saved == nil {
		f.saved = make(map[agentcore.MemoryProfileID]memcore.MemoryProfile)
	}
	f.saved[profile.ID] = profile
	return nil
}
