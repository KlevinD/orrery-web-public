package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/missioncontrol"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	missioncore "orrery_v2/internal/mission/core"
)

type fixedMemoryOntologyProfileCatalog struct {
	profiles []memcore.MemoryProfile
}

func (c fixedMemoryOntologyProfileCatalog) ListProfiles(context.Context) ([]memcore.MemoryProfile, error) {
	return append([]memcore.MemoryProfile(nil), c.profiles...), nil
}

type fixedMemoryOntologyMissionCatalog struct {
	entries []missioncontrol.MissionCatalogEntry
}

func (c fixedMemoryOntologyMissionCatalog) ListMissionCatalogEntries(_ context.Context, after string, _ int) ([]missioncontrol.MissionCatalogEntry, bool, error) {
	if after != "" {
		return nil, false, nil
	}
	return append([]missioncontrol.MissionCatalogEntry(nil), c.entries...), false, nil
}

type fixedProductionMemoryOntologyRecords struct {
	records []memcore.MemoryRecord
}

func (r fixedProductionMemoryOntologyRecords) FindRecords(context.Context, memstore.RecordQuery) ([]memcore.MemoryRecord, error) {
	return append([]memcore.MemoryRecord(nil), r.records...), nil
}

func TestProductionMemoryOntologyScopeResolverUsesProfileOrganization(t *testing.T) {
	now := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	resolver := newProductionMemoryOntologyScopeResolver(fixedMemoryOntologyProfileCatalog{profiles: []memcore.MemoryProfile{{
		ID: "memory-agent-1", OwnerRef: memcore.OwnerRef{AgentID: "A000001", OrganizationID: "minvia"},
	}}}, fixedMemoryOntologyMissionCatalog{}, func() time.Time { return now })
	record := memcore.MemoryRecord{
		ID: "memory-1", ProfileID: "memory-agent-1", Scope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "A000001"},
	}

	tenantIDs, err := resolver.ResolutionTenantIDs(context.Background(), record)
	if err != nil || len(tenantIDs) != 1 || tenantIDs[0] != "minvia" {
		t.Fatalf("tenant ids = %+v, err = %v", tenantIDs, err)
	}
}

func TestProductionMemoryOntologyScopeResolverUsesMissionAndCreatorOrganization(t *testing.T) {
	now := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	missionID := "scheduled-mission-1"
	resolver := newProductionMemoryOntologyScopeResolver(fixedMemoryOntologyProfileCatalog{profiles: []memcore.MemoryProfile{{
		ID: "memory-agent-8", OwnerRef: memcore.OwnerRef{AgentID: "A000008", OrganizationID: "minvia"},
	}}}, fixedMemoryOntologyMissionCatalog{entries: []missioncontrol.MissionCatalogEntry{{
		Seek: "system/" + missionID, TenantID: "system", MissionID: missioncore.MissionID(missionID),
	}}}, func() time.Time { return now })
	record := memcore.MemoryRecord{
		ID: "memory-mission-1", ProfileID: "memory-mission-1", CreatedBy: "A000008",
		Scope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: missionID},
	}

	tenantIDs, err := resolver.ResolutionTenantIDs(context.Background(), record)
	want := []string{"mission:system/" + missionID, "system", "minvia"}
	if err != nil || len(tenantIDs) != len(want) {
		t.Fatalf("tenant ids = %+v, err = %v", tenantIDs, err)
	}
	for index := range want {
		if tenantIDs[index] != want[index] {
			t.Fatalf("tenant ids = %+v, want %+v", tenantIDs, want)
		}
	}
}

func TestProductionMemoryOntologyScopeResolverDoesNotGuessAmbiguousMissionTenant(t *testing.T) {
	missionID := "shared-mission-id"
	resolver := newProductionMemoryOntologyScopeResolver(fixedMemoryOntologyProfileCatalog{profiles: []memcore.MemoryProfile{{
		ID: "memory-agent-8", OwnerRef: memcore.OwnerRef{AgentID: "A000008", OrganizationID: "minvia"},
	}}}, fixedMemoryOntologyMissionCatalog{entries: []missioncontrol.MissionCatalogEntry{
		{Seek: "minvia/" + missionID, TenantID: "minvia", MissionID: missioncore.MissionID(missionID)},
		{Seek: "system/" + missionID, TenantID: "system", MissionID: missioncore.MissionID(missionID)},
	}}, func() time.Time { return time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC) })
	record := memcore.MemoryRecord{
		ID: "memory-ambiguous", CreatedBy: "A000008", Scope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: missionID},
	}

	tenantIDs, err := resolver.ResolutionTenantIDs(context.Background(), record)
	if err != nil || len(tenantIDs) != 1 || tenantIDs[0] != "minvia" {
		t.Fatalf("tenant ids = %+v, err = %v", tenantIDs, err)
	}
}

func TestProductionMemoryOntologyReaderExcludesPersonalAssistantRecords(t *testing.T) {
	assistantID := "A009981"
	if err := agentcore.RegisterPersonalAssistantProfileID("minvia", "owner-memory-scope", assistantID); err != nil {
		t.Fatal(err)
	}
	privateProfileID := discussionsession.PersonalAssistantMemoryProfileID(assistantID)
	reader := newProductionMemoryOntologyReader(fixedProductionMemoryOntologyRecords{records: []memcore.MemoryRecord{
		{ID: "private", ProfileID: agentcore.MemoryProfileID(privateProfileID), Scope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: assistantID}},
		{ID: "business", ProfileID: "memory-agent-1", Scope: memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "A000001"}},
	}})

	records, err := reader.FindRecords(context.Background(), memstore.RecordQuery{})
	if err != nil || len(records) != 1 || records[0].ID != "business" {
		t.Fatalf("records = %+v, err = %v", records, err)
	}
}
