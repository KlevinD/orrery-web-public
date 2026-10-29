package memorydistill

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	memaudit "orrery_v2/internal/memory/audit"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
)

func TestServiceDistillExperienceRequiresPermissionAndCreatesCandidate(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	audit := &memtestkit.MemoryAuditRecorder{}
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	seedAcceptedFact(t, ctx, store, "memory-qa-1", "candidate-qa-1", now)
	seedAcceptedFact(t, ctx, store, "memory-qa-2", "candidate-qa-2", now)
	service := NewService(store, audit, func() time.Time { return now })

	_, err := service.DistillExperience(ctx, ViewerRef{ViewerID: "reader-1", PermissionScope: []string{"memory.read"}, MissionIDs: []string{"mission-1"}, ProjectIDs: []string{"project-1"}}, DistillExperienceRequest{
		TargetScope: MemoryScopeDTO{Kind: "project", ID: "project-1"},
		Topic:       "qa",
		SourceScope: MemoryScopeDTO{Kind: "mission", ID: "mission-1"},
	})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("DistillExperience err = %v, want permission denied", err)
	}

	result, err := service.DistillExperience(ctx, ViewerRef{ViewerID: "distiller-1", PermissionScope: []string{PermissionMemoryDistill}, MissionIDs: []string{"mission-1"}, ProjectIDs: []string{"project-1"}}, DistillExperienceRequest{
		TargetScope: MemoryScopeDTO{Kind: "project", ID: "project-1"},
		Topic:       "qa",
		SourceScope: MemoryScopeDTO{Kind: "mission", ID: "mission-1"},
	})
	if err != nil {
		t.Fatalf("DistillExperience: %v", err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].ProposedLayer != "experience" {
		t.Fatalf("result = %+v", result)
	}
	if result.Candidates[0].Confidence != 0.7 || len(result.Candidates[0].Warnings) == 0 {
		t.Fatalf("candidate confidence/warnings = %+v", result.Candidates[0])
	}
	if result.SourceMemoryIDs[0] != "memory-qa-1" || result.SourceMemoryIDs[1] != "memory-qa-2" {
		t.Fatalf("source ids = %+v", result.SourceMemoryIDs)
	}
	saved, err := store.GetCandidate(ctx, result.Candidates[0].CandidateID)
	if err != nil {
		t.Fatalf("GetCandidate: %v", err)
	}
	if saved.ProposedLayer != memcore.MemoryLayerExperience || len(saved.LineageRefs) != 2 {
		t.Fatalf("saved candidate = %+v", saved)
	}
	records, err := store.FindRecords(ctx, memstore.RecordQuery{
		ScopeKinds: []memcore.ScopeKind{memcore.ScopeProject},
		ScopeIDs:   []string{"project-1"},
		Layers:     []memcore.MemoryLayer{memcore.MemoryLayerExperience},
	})
	if err != nil {
		t.Fatalf("FindRecords experience: %v", err)
	}
	if len(records) != 1 || records[0].CandidateID != saved.ID || records[0].LifecycleStatus != memcore.MemoryLifecycleActive {
		t.Fatalf("experience records = %+v", records)
	}
	events := audit.Snapshot()
	if len(events) != 3 || events[0].Type != memaudit.EventMemoryDistillationStarted || events[1].Type != memaudit.EventMemoryRecordCreated || events[2].Type != memaudit.EventMemoryDistillationCompleted {
		t.Fatalf("events = %+v", events)
	}
}

func TestServiceDistillExperienceSkipsSensitiveFactsWithoutAuditRead(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	seedAcceptedFact(t, ctx, store, "memory-sensitive-1", "candidate-sensitive-1", now)
	seedAcceptedFact(t, ctx, store, "memory-sensitive-2", "candidate-sensitive-2", now)
	for _, id := range []string{"memory-sensitive-1", "memory-sensitive-2"} {
		records, err := store.FindRecords(ctx, memstore.RecordQuery{LifecycleStatuses: []memcore.MemoryLifecycleStatus{memcore.MemoryLifecycleActive}, IncludeArchived: true})
		if err != nil {
			t.Fatalf("FindRecords: %v", err)
		}
		for _, record := range records {
			if record.ID == id {
				record.Sensitivity = memcore.SensitivityLegal
				if err := store.SaveRecord(ctx, record); err != nil {
					t.Fatalf("SaveRecord sensitive: %v", err)
				}
			}
		}
	}
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now })

	_, err := service.DistillExperience(ctx, ViewerRef{ViewerID: "distiller-1", PermissionScope: []string{PermissionMemoryDistill}, MissionIDs: []string{"mission-1"}, ProjectIDs: []string{"project-1"}}, DistillExperienceRequest{
		TargetScope: MemoryScopeDTO{Kind: "project", ID: "project-1"},
		Topic:       "qa",
		SourceScope: MemoryScopeDTO{Kind: "mission", ID: "mission-1"},
	})
	if !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("DistillExperience err = %v, want no candidates", err)
	}

	_, err = service.DistillExperience(ctx, ViewerRef{ViewerID: "auditor-1", PermissionScope: []string{PermissionMemoryDistill, permissionMemoryAuditRead}, MissionIDs: []string{"mission-1"}, ProjectIDs: []string{"project-1"}}, DistillExperienceRequest{
		TargetScope: MemoryScopeDTO{Kind: "project", ID: "project-1"},
		Topic:       "qa",
		SourceScope: MemoryScopeDTO{Kind: "mission", ID: "mission-1"},
	})
	if !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("DistillExperience sensitive audit sources err = %v, want no candidates", err)
	}
}

func TestServiceDistillExperienceAuditReaderCanAggregateAcrossMissionScopes(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 8, 17, 12, 0, 0, 0, time.UTC)
	seedAcceptedFact(t, ctx, store, "memory-agent-result-1", "candidate-agent-result-1", now)
	seedAcceptedFact(t, ctx, store, "memory-agent-result-2", "candidate-agent-result-2", now)
	seedAcceptedFact(t, ctx, store, "memory-agent-result-sensitive", "candidate-agent-result-sensitive", now)
	for _, id := range []string{"memory-agent-result-1", "memory-agent-result-2", "memory-agent-result-sensitive"} {
		records, err := store.FindRecords(ctx, memstore.RecordQuery{LifecycleStatuses: []memcore.MemoryLifecycleStatus{memcore.MemoryLifecycleActive}, IncludeArchived: true})
		if err != nil {
			t.Fatalf("FindRecords: %v", err)
		}
		for _, record := range records {
			if record.ID == id {
				record.Topic = "agent_result"
				record.Tags = []string{"agent_result"}
				if id == "memory-agent-result-sensitive" {
					record.Sensitivity = memcore.SensitivitySecret
				}
				if err := store.SaveRecord(ctx, record); err != nil {
					t.Fatalf("SaveRecord agent result: %v", err)
				}
			}
		}
	}
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now })
	request := DistillExperienceRequest{
		TargetScope: MemoryScopeDTO{Kind: "organization", ID: "org-1"},
		Topic:       "agent_result",
	}

	_, err := service.DistillExperience(ctx, ViewerRef{
		ViewerID:        "distiller-1",
		PermissionScope: []string{PermissionMemoryDistill},
		OrganizationIDs: []string{"org-1"},
	}, request)
	if !errors.Is(err, ErrNoCandidates) {
		t.Fatalf("DistillExperience without audit scope err = %v, want no candidates", err)
	}

	result, err := service.DistillExperience(ctx, ViewerRef{
		ViewerID:        "distiller-1",
		PermissionScope: []string{PermissionMemoryDistill, permissionMemoryAuditRead},
		OrganizationIDs: []string{"org-1"},
	}, request)
	if err != nil {
		t.Fatalf("DistillExperience with audit scope: %v", err)
	}
	if len(result.Candidates) != 1 || len(result.SourceMemoryIDs) != 2 {
		t.Fatalf("result = %+v", result)
	}
	if containsString(result.SourceMemoryIDs, "memory-agent-result-sensitive") {
		t.Fatalf("sensitive source was distilled: %+v", result.SourceMemoryIDs)
	}
}

func TestServiceDistillExperienceMatchesAcceptedFactsByTag(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	audit := &memtestkit.MemoryAuditRecorder{}
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	seedAcceptedFact(t, ctx, store, "memory-tag-1", "candidate-tag-1", now)
	seedAcceptedFact(t, ctx, store, "memory-tag-2", "candidate-tag-2", now)
	for _, id := range []string{"memory-tag-1", "memory-tag-2"} {
		records, err := store.FindRecords(ctx, memstore.RecordQuery{LifecycleStatuses: []memcore.MemoryLifecycleStatus{memcore.MemoryLifecycleActive}, IncludeArchived: true})
		if err != nil {
			t.Fatalf("FindRecords: %v", err)
		}
		for _, record := range records {
			if record.ID == id {
				record.Topic = ""
				record.Tags = []string{"qa", "evidence"}
				if err := store.SaveRecord(ctx, record); err != nil {
					t.Fatalf("SaveRecord tag-only: %v", err)
				}
			}
		}
	}
	service := NewService(store, audit, func() time.Time { return now })

	result, err := service.DistillExperience(ctx, ViewerRef{ViewerID: "distiller-1", PermissionScope: []string{PermissionMemoryDistill}, MissionIDs: []string{"mission-1"}, ProjectIDs: []string{"project-1"}}, DistillExperienceRequest{
		TargetScope: MemoryScopeDTO{Kind: "project", ID: "project-1"},
		Topic:       "qa",
		SourceScope: MemoryScopeDTO{Kind: "mission", ID: "mission-1"},
	})
	if err != nil {
		t.Fatalf("DistillExperience tag-only facts: %v", err)
	}
	if len(result.Candidates) != 1 || len(result.SourceMemoryIDs) != 2 {
		t.Fatalf("result = %+v", result)
	}
}

func TestServiceDistillExperienceRejectsUnsupportedTargetScope(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	seedAcceptedFact(t, ctx, store, "memory-qa-1", "candidate-qa-1", now)
	seedAcceptedFact(t, ctx, store, "memory-qa-2", "candidate-qa-2", now)
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now })

	_, err := service.DistillExperience(ctx, ViewerRef{ViewerID: "distiller-1", PermissionScope: []string{PermissionMemoryDistill}, MissionIDs: []string{"mission-1"}}, DistillExperienceRequest{
		TargetScope: MemoryScopeDTO{Kind: "mission", ID: "mission-1"},
		Topic:       "qa",
		SourceScope: MemoryScopeDTO{Kind: "mission", ID: "mission-1"},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("DistillExperience err = %v, want invalid request", err)
	}
}

func TestServiceDistillExperienceRejectsAgentSourceScope(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC) })

	_, err := service.DistillExperience(ctx, ViewerRef{ViewerID: "agent-1", AgentID: "agent-1", PermissionScope: []string{PermissionMemoryDistill}, ProjectIDs: []string{"project-1"}}, DistillExperienceRequest{
		TargetScope: MemoryScopeDTO{Kind: "project", ID: "project-1"},
		Topic:       "qa",
		SourceScope: MemoryScopeDTO{Kind: "agent", ID: "agent-1"},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("DistillExperience err = %v, want invalid request", err)
	}
}

func TestServiceDistillWorldRequiresPermissionAndCreatesCandidate(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	audit := &memtestkit.MemoryAuditRecorder{}
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	seedAcceptedExperience(t, ctx, store, "memory-quality-exp-1", "candidate-quality-exp-1", "quality", "Quality review blocks releases when acceptance evidence is missing.", now)
	seedAcceptedExperience(t, ctx, store, "memory-quality-exp-2", "candidate-quality-exp-2", "quality", "Regression evidence before QA improves release confidence.", now)
	service := NewService(store, audit, func() time.Time { return now })

	_, err := service.DistillWorld(ctx, ViewerRef{ViewerID: "reader-1", PermissionScope: []string{"memory.read"}, OrganizationIDs: []string{"org-1"}, ProjectIDs: []string{"project-1"}}, DistillWorldRequest{
		TargetScope: MemoryScopeDTO{Kind: "organization", ID: "org-1"},
		SourceScope: MemoryScopeDTO{Kind: "project", ID: "project-1"},
		Topic:       "quality",
	})
	if !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("DistillWorld err = %v, want permission denied", err)
	}

	result, err := service.DistillWorld(ctx, ViewerRef{ViewerID: "distiller-1", PermissionScope: []string{PermissionMemoryDistill}, OrganizationIDs: []string{"org-1"}, ProjectIDs: []string{"project-1"}}, DistillWorldRequest{
		TargetScope: MemoryScopeDTO{Kind: "organization", ID: "org-1"},
		SourceScope: MemoryScopeDTO{Kind: "project", ID: "project-1"},
		Topic:       "quality",
	})
	if err != nil {
		t.Fatalf("DistillWorld: %v", err)
	}
	if len(result.Candidates) != 1 || result.Candidates[0].ProposedLayer != "world" {
		t.Fatalf("result = %+v", result)
	}
	candidate := result.Candidates[0]
	if candidate.ProposedScope.Kind != "organization" || candidate.ProposedScope.ID != "org-1" || len(candidate.LineageRefs) != 2 {
		t.Fatalf("candidate scope/lineage = %+v", candidate)
	}
	if candidate.Confidence <= 0.7 || candidate.ReviewRequired || candidate.ReviewReason != "" {
		t.Fatalf("candidate confidence/review = %+v", candidate)
	}
	saved, err := store.GetCandidate(ctx, candidate.CandidateID)
	if err != nil {
		t.Fatalf("GetCandidate: %v", err)
	}
	if saved.ProposedLayer != memcore.MemoryLayerWorld || saved.DeIDLevel != memcore.DeIDTeamSafe || saved.Sensitivity != memcore.SensitivityNormal {
		t.Fatalf("saved candidate = %+v", saved)
	}
	records, err := store.FindRecords(ctx, memstore.RecordQuery{
		ScopeKinds: []memcore.ScopeKind{memcore.ScopeOrganization},
		ScopeIDs:   []string{"org-1"},
		Layers:     []memcore.MemoryLayer{memcore.MemoryLayerWorld},
	})
	if err != nil {
		t.Fatalf("FindRecords world: %v", err)
	}
	if len(records) != 1 || records[0].CandidateID != saved.ID || records[0].Visibility.Mode != memcore.VisibilityOrganization {
		t.Fatalf("world records = %+v", records)
	}
	events := audit.Snapshot()
	if len(events) != 3 || events[0].Type != memaudit.EventMemoryWorldConsolidationStarted || events[1].Type != memaudit.EventMemoryRecordCreated || events[2].Type != memaudit.EventMemoryWorldConsolidationCompleted {
		t.Fatalf("events = %+v", events)
	}
}

func TestServiceDistillWorldCarriesSupersedesAndContradictions(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	seedAcceptedExperience(t, ctx, store, "memory-quality-exp-1", "candidate-quality-exp-1", "quality", "Quality review must require automated evidence before release.", now)
	seedAcceptedExperience(t, ctx, store, "memory-quality-exp-2", "candidate-quality-exp-2", "quality", "QA must require automated regression evidence for release approval.", now)
	seedAcceptedWorld(t, ctx, store, "memory-world-old", "candidate-world-old", "quality", "Quality review is mostly manual and undocumented.", now.Add(-30*24*time.Hour))
	seedAcceptedWorld(t, ctx, store, "memory-world-conflict", "candidate-world-conflict", "quality", "Quality review must not require automated evidence.", now.Add(-5*24*time.Hour))
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now })

	result, err := service.DistillWorld(ctx, ViewerRef{ViewerID: "distiller-1", PermissionScope: []string{PermissionMemoryDistill}, OrganizationIDs: []string{"org-1"}, ProjectIDs: []string{"project-1"}}, DistillWorldRequest{
		TargetScope: MemoryScopeDTO{Kind: "organization", ID: "org-1"},
		SourceScope: MemoryScopeDTO{Kind: "project", ID: "project-1"},
		Topic:       "quality",
	})
	if err != nil {
		t.Fatalf("DistillWorld: %v", err)
	}
	candidate := result.Candidates[0]
	if len(candidate.SupersedesRefs) != 1 || candidate.SupersedesRefs[0] != "memory:memory-world-old" {
		t.Fatalf("supersedes refs = %+v", candidate.SupersedesRefs)
	}
	if len(candidate.ContradictsRefs) != 1 || candidate.ContradictsRefs[0] != "memory:memory-world-conflict" {
		t.Fatalf("contradicts refs = %+v", candidate.ContradictsRefs)
	}
	saved, err := store.GetCandidate(ctx, candidate.CandidateID)
	if err != nil {
		t.Fatalf("GetCandidate: %v", err)
	}
	if len(saved.SupersedesRefs) != 1 || len(saved.ContradictsRefs) != 1 {
		t.Fatalf("saved relation refs = %+v/%+v", saved.SupersedesRefs, saved.ContradictsRefs)
	}
}

func TestServiceDistillWorldRejectsInvalidScopes(t *testing.T) {
	ctx := context.Background()
	store := memtestkit.NewFakeStore()
	service := NewService(store, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC) })

	_, err := service.DistillWorld(ctx, ViewerRef{ViewerID: "distiller-1", PermissionScope: []string{PermissionMemoryDistill}, ProjectIDs: []string{"project-1"}}, DistillWorldRequest{
		TargetScope: MemoryScopeDTO{Kind: "project", ID: "project-1"},
		Topic:       "quality",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("DistillWorld project target err = %v, want invalid request", err)
	}

	_, err = service.DistillWorld(ctx, ViewerRef{ViewerID: "distiller-1", PermissionScope: []string{PermissionMemoryDistill}, OrganizationIDs: []string{"org-1"}, MissionIDs: []string{"mission-1"}}, DistillWorldRequest{
		TargetScope: MemoryScopeDTO{Kind: "organization", ID: "org-1"},
		SourceScope: MemoryScopeDTO{Kind: "mission", ID: "mission-1"},
		Topic:       "quality",
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("DistillWorld mission source err = %v, want invalid request", err)
	}
}

func seedAcceptedFact(t *testing.T, ctx context.Context, store *memtestkit.FakeStore, memoryID, candidateID string, now time.Time) {
	t.Helper()
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            candidateID,
		SourceType:    memcore.SourceQAResult,
		SourceRef:     candidateID,
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Summary:       "QA rejected missing evidence.",
		Status:        memcore.CandidateAccepted,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveCandidate: %v", err)
	}
	if err := store.SaveRecord(ctx, memcore.MemoryRecord{
		ID:                 memoryID,
		ProfileID:          agentcore.MemoryProfileID("profile-1"),
		CandidateID:        candidateID,
		Layer:              memcore.MemoryLayerFact,
		Scope:              memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		Topic:              "qa",
		Summary:            "QA rejected missing evidence.",
		Facts:              []string{"QA rejected missing evidence."},
		Tags:               []string{"qa", "rejected"},
		SourceRefs:         []string{"qa_result:" + candidateID},
		SourceEvidenceRefs: []string{"evidence:" + candidateID},
		Sensitivity:        memcore.SensitivityNormal,
		LifecycleStatus:    memcore.MemoryLifecycleActive,
		Confidence:         1,
		Visibility:         memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeMission, ScopeID: "mission-1"},
		CreatedBy:          agentcore.AgentID("agent-1"),
		CreatedAt:          now,
		UpdatedAt:          now,
	}); err != nil {
		t.Fatalf("SaveRecord: %v", err)
	}
}

func seedAcceptedExperience(t *testing.T, ctx context.Context, store *memtestkit.FakeStore, memoryID, candidateID, topic, summary string, now time.Time) {
	t.Helper()
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            candidateID,
		SourceType:    memcore.SourceSummary,
		SourceRef:     "experience:" + topic,
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeProject, ID: "project-1"},
		ProposedKind:  agentcore.MemoryKindProject,
		ProposedLayer: memcore.MemoryLayerExperience,
		Summary:       summary,
		Status:        memcore.CandidateAccepted,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveCandidate experience: %v", err)
	}
	if err := store.SaveRecord(ctx, memcore.MemoryRecord{
		ID:                 memoryID,
		ProfileID:          agentcore.MemoryProfileID("profile-experience"),
		CandidateID:        candidateID,
		Layer:              memcore.MemoryLayerExperience,
		Scope:              memcore.MemoryScope{Kind: memcore.ScopeProject, ID: "project-1"},
		Topic:              topic,
		Summary:            summary,
		Narrative:          summary,
		Lessons:            []string{summary},
		Tags:               []string{topic},
		LineageRefs:        []string{"memory:fact-" + memoryID},
		SourceEvidenceRefs: []string{"evidence:" + memoryID},
		Sensitivity:        memcore.SensitivityNormal,
		DeIDLevel:          memcore.DeIDTeamSafe,
		LifecycleStatus:    memcore.MemoryLifecycleActive,
		Confidence:         0.8,
		Visibility:         memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeProject, ScopeID: "project-1"},
		CreatedBy:          agentcore.AgentID("agent-1"),
		CreatedAt:          now,
		UpdatedAt:          now,
	}); err != nil {
		t.Fatalf("SaveRecord experience: %v", err)
	}
}

func seedAcceptedWorld(t *testing.T, ctx context.Context, store *memtestkit.FakeStore, memoryID, candidateID, topic, summary string, now time.Time) {
	t.Helper()
	if err := store.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            candidateID,
		SourceType:    memcore.SourceSummary,
		SourceRef:     "world:organization:org-1:" + topic,
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		ProposedKind:  agentcore.MemoryKindOrganization,
		ProposedLayer: memcore.MemoryLayerWorld,
		Summary:       summary,
		Status:        memcore.CandidateAccepted,
		CreatedBy:     agentcore.AgentID("reviewer-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("SaveCandidate world: %v", err)
	}
	if err := store.SaveRecord(ctx, memcore.MemoryRecord{
		ID:              memoryID,
		ProfileID:       agentcore.MemoryProfileID("profile-world"),
		CandidateID:     candidateID,
		Layer:           memcore.MemoryLayerWorld,
		Scope:           memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		Topic:           topic,
		Summary:         summary,
		Narrative:       summary,
		Lessons:         []string{summary},
		Tags:            []string{topic},
		LineageRefs:     []string{"memory:source-" + memoryID},
		Sensitivity:     memcore.SensitivityNormal,
		DeIDLevel:       memcore.DeIDTeamSafe,
		LifecycleStatus: memcore.MemoryLifecycleActive,
		Confidence:      0.7,
		Visibility:      memcore.Visibility{Mode: memcore.VisibilityOrganization, ScopeKind: memcore.ScopeOrganization, ScopeID: "org-1"},
		CreatedBy:       agentcore.AgentID("reviewer-1"),
		CreatedAt:       now,
		UpdatedAt:       now,
	}); err != nil {
		t.Fatalf("SaveRecord world: %v", err)
	}
}
