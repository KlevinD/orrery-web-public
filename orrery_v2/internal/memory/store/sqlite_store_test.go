package store

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memcore "orrery_v2/internal/memory/core"
	"orrery_v2/internal/platform/db"
)

func TestPlatformStorePersistsProfilesAndRecords(t *testing.T) {
	ctx := context.Background()
	dbstore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "memory-store-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = dbstore.Close() })
	store := NewPlatformStore(dbstore)
	profile := memcore.MemoryProfile{
		ID:               agentcore.MemoryProfileID("profile-1"),
		OwnerRef:         memcore.OwnerRef{AgentID: agentcore.AgentID("agent-a")},
		MemoryKind:       agentcore.MemoryKindPersonal,
		VisibilityPolicy: memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
		Enabled:          true,
	}
	record := memcore.MemoryRecord{
		ID:          "record-1",
		ProfileID:   profile.ID,
		CandidateID: "candidate-1",
		Summary:     "Remember to keep tests focused.",
		Facts:       []string{"Tests are required."},
		Visibility:  memcore.Visibility{Mode: memcore.VisibilityOwnerOnly},
	}

	if err := store.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save profile: %v", err)
	}
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{ID: "candidate-1", Status: memcore.CandidateAccepted}); err != nil {
		t.Fatalf("save candidate: %v", err)
	}
	if err := store.SaveRecord(ctx, record); err != nil {
		t.Fatalf("save record: %v", err)
	}
	reopened := NewPlatformStore(dbstore)
	gotProfile, err := reopened.GetProfile(ctx, profile.ID)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if gotProfile.OwnerRef.AgentID != "agent-a" {
		t.Fatalf("profile not persisted: %+v", gotProfile)
	}
	records, err := reopened.FindRecords(ctx, RecordQuery{ProfileIDs: []agentcore.MemoryProfileID{profile.ID}})
	if err != nil {
		t.Fatalf("find records: %v", err)
	}
	if len(records) != 1 || records[0].Facts[0] != "Tests are required." {
		t.Fatalf("records = %+v", records)
	}
	gotRecord, err := reopened.GetRecord(ctx, record.ID)
	if err != nil || gotRecord.ID != record.ID || gotRecord.Summary != record.Summary {
		t.Fatalf("get record = %+v, err=%v", gotRecord, err)
	}
	if _, err := reopened.GetRecord(ctx, "missing"); !errors.Is(err, memcore.ErrNotFound) {
		t.Fatalf("missing record error = %v", err)
	}
	profiles, err := reopened.ListProfiles(ctx)
	if err != nil {
		t.Fatalf("list profiles: %v", err)
	}
	if len(profiles) != 1 || profiles[0].ID != profile.ID || profiles[0].MemoryKind != agentcore.MemoryKindPersonal {
		t.Fatalf("profiles = %+v", profiles)
	}
}

func TestPlatformStoreReassignProfileMergesRecordsIntoCanonicalProfile(t *testing.T) {
	ctx := context.Background()
	dbstore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "memory-profile-reassign-test"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbstore.Close() })
	store := NewPlatformStore(dbstore)
	for _, profile := range []memcore.MemoryProfile{
		{ID: "memory-legacy", OwnerRef: memcore.OwnerRef{AgentID: "A000001"}, Enabled: true},
		{ID: "memory-A000001", OwnerRef: memcore.OwnerRef{AgentID: "A000001"}, Enabled: true},
	} {
		if err := store.SaveProfile(ctx, profile); err != nil {
			t.Fatal(err)
		}
	}
	for _, record := range []memcore.MemoryRecord{
		{ID: "legacy-record", ProfileID: "memory-legacy", CandidateID: "legacy-candidate"},
		{ID: "canonical-record", ProfileID: "memory-A000001", CandidateID: "canonical-candidate"},
	} {
		if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{ID: record.CandidateID, Status: memcore.CandidateAccepted}); err != nil {
			t.Fatal(err)
		}
		if err := store.SaveRecord(ctx, record); err != nil {
			t.Fatal(err)
		}
	}

	report, err := store.ReassignProfile(ctx, "memory-legacy", "memory-A000001")
	if err != nil {
		t.Fatal(err)
	}
	if !report.ProfileMoved || !report.TargetExisted || report.RecordsMoved != 1 {
		t.Fatalf("report = %+v", report)
	}
	if _, err := store.GetProfile(ctx, "memory-legacy"); !errors.Is(err, memcore.ErrNotFound) {
		t.Fatalf("legacy profile error = %v", err)
	}
	records, err := store.FindRecords(ctx, RecordQuery{ProfileIDs: []agentcore.MemoryProfileID{"memory-A000001"}, IncludeArchived: true})
	if err != nil || len(records) != 2 {
		t.Fatalf("canonical records = %+v err=%v", records, err)
	}
}

func TestPlatformStoreRequiresAcceptedCandidateForRecords(t *testing.T) {
	ctx := context.Background()
	dbstore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "memory-store-candidate-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = dbstore.Close() })
	store := NewPlatformStore(dbstore)

	if err := store.SaveRecord(ctx, memcore.MemoryRecord{ID: "record-direct", CandidateID: "", ProfileID: "profile-1"}); err == nil {
		t.Fatalf("expected missing candidate error")
	}
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{ID: "candidate-proposed", Status: memcore.CandidateProposed}); err != nil {
		t.Fatalf("save candidate: %v", err)
	}
	if err := store.SaveRecord(ctx, memcore.MemoryRecord{ID: "record-proposed", CandidateID: "candidate-proposed", ProfileID: "profile-1"}); err == nil {
		t.Fatalf("expected proposed candidate error")
	}
}

func TestPlatformStoreRejectsOversizedCandidateAndRecordIdentifiers(t *testing.T) {
	ctx := context.Background()
	dbstore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "memory-store-id-limit-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = dbstore.Close() })
	store := NewPlatformStore(dbstore)
	oversized := strings.Repeat("x", memcore.MaxIdentifierBytes+1)
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{ID: oversized, Status: memcore.CandidateAccepted}); !errors.Is(err, memcore.ErrInvalid) {
		t.Fatalf("oversized candidate error = %v", err)
	}
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{ID: "candidate-valid", Status: memcore.CandidateAccepted}); err != nil {
		t.Fatalf("save valid candidate: %v", err)
	}
	if err := store.SaveRecord(ctx, memcore.MemoryRecord{ID: oversized, CandidateID: "candidate-valid", ProfileID: "profile-1"}); !errors.Is(err, memcore.ErrInvalid) {
		t.Fatalf("oversized record error = %v", err)
	}
}

func TestPlatformStoreFindRecordsFiltersByPensieveMetadata(t *testing.T) {
	ctx := context.Background()
	dbstore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "memory-store-pensieve-filter-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = dbstore.Close() })
	store := NewPlatformStore(dbstore)
	profile := memcore.MemoryProfile{
		ID:               agentcore.MemoryProfileID("profile-filter"),
		OwnerRef:         memcore.OwnerRef{OrganizationID: "org-1"},
		MemoryKind:       agentcore.MemoryKindOrganization,
		VisibilityPolicy: memcore.VisibilityPolicy{Mode: memcore.VisibilityOrganization},
		Enabled:          true,
	}
	if err := store.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	candidate := memcore.MemoryCandidate{ID: "candidate-world", Status: memcore.CandidateAccepted}
	if err := store.SaveCandidate(ctx, candidate); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	records := []memcore.MemoryRecord{
		{
			ID:              "memory-fact",
			ProfileID:       profile.ID,
			CandidateID:     candidate.ID,
			Scope:           memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
			Layer:           memcore.MemoryLayerFact,
			Topic:           "codebase",
			Summary:         "fact",
			LifecycleStatus: memcore.MemoryLifecycleActive,
			CreatedAt:       now,
		},
		{
			ID:              "memory-world",
			ProfileID:       profile.ID,
			CandidateID:     candidate.ID,
			Scope:           memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
			Layer:           memcore.MemoryLayerWorld,
			Topic:           "codebase",
			Summary:         "world",
			LineageRefs:     []string{"memory-fact"},
			LifecycleStatus: memcore.MemoryLifecycleActive,
			CreatedAt:       now,
		},
		{
			ID:              "memory-archived",
			ProfileID:       profile.ID,
			CandidateID:     candidate.ID,
			Scope:           memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
			Layer:           memcore.MemoryLayerWorld,
			Topic:           "risk",
			Summary:         "archived",
			LineageRefs:     []string{"memory-fact"},
			LifecycleStatus: memcore.MemoryLifecycleArchived,
			CreatedAt:       now,
		},
	}
	for _, record := range records {
		if err := store.SaveRecord(ctx, record); err != nil {
			t.Fatalf("SaveRecord(%s): %v", record.ID, err)
		}
	}

	got, err := store.FindRecords(ctx, RecordQuery{
		ProfileIDs:        []agentcore.MemoryProfileID{profile.ID},
		ScopeKinds:        []memcore.ScopeKind{memcore.ScopeOrganization},
		ScopeIDs:          []string{"org-1"},
		Layers:            []memcore.MemoryLayer{memcore.MemoryLayerWorld},
		Topics:            []string{"codebase"},
		LifecycleStatuses: []memcore.MemoryLifecycleStatus{memcore.MemoryLifecycleActive},
	})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(got) != 1 || got[0].ID != "memory-world" {
		t.Fatalf("FindRecords = %+v, want memory-world only", got)
	}
}

func TestPlatformStoreFindRecordsMatchesTopicOrTag(t *testing.T) {
	ctx := context.Background()
	dbstore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "memory-store-topic-tag-filter-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = dbstore.Close() })
	store := NewPlatformStore(dbstore)
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{ID: "candidate-topic-tag", Status: memcore.CandidateAccepted}); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	records := []memcore.MemoryRecord{
		{
			ID:              "memory-topic",
			ProfileID:       "profile-1",
			CandidateID:     "candidate-topic-tag",
			Scope:           memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
			Layer:           memcore.MemoryLayerFact,
			Topic:           "qa",
			Summary:         "topic match",
			LifecycleStatus: memcore.MemoryLifecycleActive,
		},
		{
			ID:              "memory-tag",
			ProfileID:       "profile-1",
			CandidateID:     "candidate-topic-tag",
			Scope:           memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
			Layer:           memcore.MemoryLayerFact,
			Tags:            []string{"qa", "evidence"},
			Summary:         "tag match",
			LifecycleStatus: memcore.MemoryLifecycleActive,
		},
		{
			ID:              "memory-other",
			ProfileID:       "profile-1",
			CandidateID:     "candidate-topic-tag",
			Scope:           memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
			Layer:           memcore.MemoryLayerFact,
			Topic:           "planning",
			Tags:            []string{"release"},
			Summary:         "other",
			LifecycleStatus: memcore.MemoryLifecycleActive,
		},
	}
	for _, record := range records {
		if err := store.SaveRecord(ctx, record); err != nil {
			t.Fatalf("SaveRecord(%s): %v", record.ID, err)
		}
	}

	got, err := store.FindRecords(ctx, RecordQuery{Topics: []string{"qa"}, Tags: []string{"qa"}})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(got) != 2 || got[0].ID != "memory-tag" || got[1].ID != "memory-topic" {
		t.Fatalf("FindRecords = %+v, want memory-tag and memory-topic", got)
	}
}

func TestPlatformStoreFindRecordsTreatsLegacyRecordAsFactActive(t *testing.T) {
	ctx := context.Background()
	dbstore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "memory-store-legacy-pensieve-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = dbstore.Close() })
	store := NewPlatformStore(dbstore)
	profile := memcore.MemoryProfile{
		ID:               agentcore.MemoryProfileID("profile-legacy"),
		OwnerRef:         memcore.OwnerRef{AgentID: agentcore.AgentID("agent-1")},
		MemoryKind:       agentcore.MemoryKindPersonal,
		VisibilityPolicy: memcore.VisibilityPolicy{Mode: memcore.VisibilityOwnerOnly},
		Enabled:          true,
	}
	if err := store.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	candidate := memcore.MemoryCandidate{ID: "candidate-legacy", Status: memcore.CandidateAccepted}
	if err := store.SaveCandidate(ctx, candidate); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	record := memcore.MemoryRecord{
		ID:          "memory-legacy",
		ProfileID:   profile.ID,
		CandidateID: candidate.ID,
		Scope:       memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-1"},
		Summary:     "legacy",
	}
	if err := store.SaveRecord(ctx, record); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}

	got, err := store.FindRecords(ctx, RecordQuery{
		ProfileIDs:        []agentcore.MemoryProfileID{profile.ID},
		Layers:            []memcore.MemoryLayer{memcore.MemoryLayerFact},
		LifecycleStatuses: []memcore.MemoryLifecycleStatus{memcore.MemoryLifecycleActive},
	})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(got) != 1 || got[0].ID != "memory-legacy" || got[0].Layer != memcore.MemoryLayerFact {
		t.Fatalf("legacy query = %+v, want normalized fact active record", got)
	}
}

func TestPlatformStoreUpdateRecordLifecyclePersistsDistilledStatus(t *testing.T) {
	ctx := context.Background()
	dbstore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "memory-store-lifecycle-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = dbstore.Close() })
	store := NewPlatformStore(dbstore)
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{ID: "candidate-fact", Status: memcore.CandidateAccepted}); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	record := memcore.MemoryRecord{
		ID:              "memory-fact",
		ProfileID:       "profile-1",
		CandidateID:     "candidate-fact",
		Layer:           memcore.MemoryLayerFact,
		Scope:           memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		Summary:         "Repeated QA rework.",
		Facts:           []string{"Repeated QA rework."},
		SourceRefs:      []string{"qa_result:qa-1"},
		LifecycleStatus: memcore.MemoryLifecycleActive,
	}
	if err := store.SaveRecord(ctx, record); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}

	if err := store.UpdateRecordLifecycle(ctx, "memory-fact", memcore.MemoryLifecycleDistilled); err != nil {
		t.Fatalf("UpdateRecordLifecycle: %v", err)
	}

	records, err := store.FindRecords(ctx, RecordQuery{LifecycleStatuses: []memcore.MemoryLifecycleStatus{memcore.MemoryLifecycleDistilled}, IncludeArchived: true})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(records) != 1 || records[0].ID != "memory-fact" || records[0].LifecycleStatus != memcore.MemoryLifecycleDistilled {
		t.Fatalf("records = %+v", records)
	}
}

func TestPlatformStoreFindRecordsExcludesDeletedFromMainRetrieval(t *testing.T) {
	ctx := context.Background()
	dbstore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "memory-store-deleted-filter-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = dbstore.Close() })
	store := NewPlatformStore(dbstore)
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{ID: "candidate-deleted", Status: memcore.CandidateAccepted}); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	for _, record := range []memcore.MemoryRecord{
		{ID: "memory-active", ProfileID: "profile-1", CandidateID: "candidate-deleted", Summary: "active", LifecycleStatus: memcore.MemoryLifecycleActive},
		{ID: "memory-deleted", ProfileID: "profile-1", CandidateID: "candidate-deleted", Summary: "deleted", LifecycleStatus: memcore.MemoryLifecycleDeleted},
	} {
		if err := store.SaveRecord(ctx, record); err != nil {
			t.Fatalf("SaveRecord(%s): %v", record.ID, err)
		}
	}

	got, err := store.FindRecords(ctx, RecordQuery{})
	if err != nil {
		t.Fatalf("FindRecords: %v", err)
	}
	if len(got) != 1 || got[0].ID != "memory-active" {
		t.Fatalf("FindRecords = %+v, want active only", got)
	}
	all, err := store.FindRecords(ctx, RecordQuery{IncludeArchived: true})
	if err != nil {
		t.Fatalf("FindRecords include archived: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("FindRecords include archived = %+v, want active and deleted", all)
	}
}

func TestPlatformStoreListCandidatesFiltersByPensieveMetadata(t *testing.T) {
	ctx := context.Background()
	dbstore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "memory-store-candidate-filter-test"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = dbstore.Close() })
	store := NewPlatformStore(dbstore)
	reviewRequired := true
	candidates := []memcore.MemoryCandidate{
		{
			ID:             "candidate-world",
			MissionID:      "mission-1",
			ProposedScope:  memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
			ProposedLayer:  memcore.MemoryLayerWorld,
			Topic:          "codebase",
			ReviewRequired: true,
			Status:         memcore.CandidateProposed,
		},
		{
			ID:             "candidate-fact",
			MissionID:      "mission-1",
			ProposedScope:  memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
			ProposedLayer:  memcore.MemoryLayerFact,
			Topic:          "codebase",
			ReviewRequired: false,
			Status:         memcore.CandidateProposed,
		},
	}
	for _, candidate := range candidates {
		if err := store.SaveCandidate(ctx, candidate); err != nil {
			t.Fatalf("SaveCandidate(%s): %v", candidate.ID, err)
		}
	}

	got, err := store.ListCandidates(ctx, CandidateQuery{
		MissionID:      "mission-1",
		ScopeKind:      memcore.ScopeOrganization,
		ScopeID:        "org-1",
		Layer:          memcore.MemoryLayerWorld,
		Topic:          "codebase",
		ReviewRequired: &reviewRequired,
		Status:         memcore.CandidateProposed,
	})
	if err != nil {
		t.Fatalf("ListCandidates: %v", err)
	}
	if len(got) != 1 || got[0].ID != "candidate-world" || got[0].ProposedLayer != memcore.MemoryLayerWorld {
		t.Fatalf("ListCandidates = %+v, want candidate-world only", got)
	}
}

func TestPlatformStoreReassignsOrganizationScopeWithoutRenamingMemory(t *testing.T) {
	ctx := context.Background()
	dbstore, err := db.Open(ctx, db.Config{Driver: db.DriverMemory, Name: "memory-store-organization-migration"})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = dbstore.Close() })
	store := NewPlatformStore(dbstore)
	legacyScope := memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "legacy-org"}
	legacyProfile := memcore.NewScopeMemoryProfile(legacyScope, memcore.OwnerRef{OrganizationID: "legacy-org"})
	if err := store.SaveProfile(ctx, legacyProfile); err != nil {
		t.Fatalf("SaveProfile: %v", err)
	}
	personal := memcore.NewScopeMemoryProfile(memcore.MemoryScope{Kind: memcore.ScopeAgent, ID: "agent-1"}, memcore.OwnerRef{AgentID: "agent-1", OrganizationID: "legacy-org"})
	if err := store.SaveProfile(ctx, personal); err != nil {
		t.Fatalf("SaveProfile personal: %v", err)
	}
	candidate := memcore.MemoryCandidate{ID: "candidate-legacy", ProposedScope: legacyScope, Tags: []string{"tenant_id:legacy-org"}, Status: memcore.CandidateAccepted}
	if err := store.SaveCandidate(ctx, candidate); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	record := memcore.MemoryRecord{
		ID: "memory-historical-legacy-org", ProfileID: legacyProfile.ID, CandidateID: candidate.ID,
		Scope: legacyScope, Visibility: memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeOrganization, ScopeID: "legacy-org"},
		Tags: []string{"agent_diary", "tenant_id:legacy-org"},
	}
	if err := store.SaveRecord(ctx, record); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}

	report, err := store.ReassignOrganizationScope(ctx, "legacy-org", "minvia")
	if err != nil {
		t.Fatalf("ReassignOrganizationScope: %v", err)
	}
	if report.Profiles != 2 || report.Candidates != 1 || report.Records != 1 {
		t.Fatalf("report = %+v", report)
	}
	businessProfileID := memcore.ScopeMemoryProfileID(memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "minvia"})
	gotProfile, err := store.GetProfile(ctx, businessProfileID)
	if err != nil || gotProfile.OwnerRef.OrganizationID != "minvia" || gotProfile.DefaultScope.ID != "minvia" {
		t.Fatalf("business profile = %+v, %v", gotProfile, err)
	}
	gotRecord, err := store.GetRecord(ctx, record.ID)
	if err != nil || gotRecord.ID != record.ID || gotRecord.ProfileID != businessProfileID || gotRecord.Scope.ID != "minvia" || gotRecord.Visibility.ScopeID != "minvia" || !slices.Contains(gotRecord.Tags, "tenant_id:minvia") {
		t.Fatalf("migrated record = %+v, %v", gotRecord, err)
	}
	gotCandidate, err := store.GetCandidate(ctx, candidate.ID)
	if err != nil || gotCandidate.ProposedScope.ID != "minvia" || !slices.Contains(gotCandidate.Tags, "tenant_id:minvia") {
		t.Fatalf("migrated candidate = %+v, %v", gotCandidate, err)
	}
	gotPersonal, err := store.GetProfile(ctx, personal.ID)
	if err != nil || gotPersonal.OwnerRef.OrganizationID != "minvia" {
		t.Fatalf("personal profile = %+v, %v", gotPersonal, err)
	}
	repeated, err := store.ReassignOrganizationScope(ctx, "legacy-org", "minvia")
	if err != nil || repeated != (OrganizationScopeMigrationReport{}) {
		t.Fatalf("repeated migration = %+v, %v", repeated, err)
	}
}
