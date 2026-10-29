package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/memoryadmin"
	"orrery_v2/internal/app/ontology"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/migration"
)

func TestProductionMemoryLifecycleUpdatesOntologyContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-memory-lifecycle-ontology"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	core, ok := databases.Get(db.RoleCore)
	if !ok || core == nil {
		t.Fatalf("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(core)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}

	now := time.Date(2026, 6, 19, 11, 0, 0, 0, time.UTC)
	memoryStore := memstore.NewPlatformStore(core)
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:        "candidate-lifecycle-production",
		MissionID: "mission-1",
		Status:    memcore.CandidateAccepted,
		CreatedBy: agentcore.AgentID("reviewer-1"),
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save candidate: %v", err)
	}
	record := memcore.MemoryRecord{
		ID:                 "memory-record-lifecycle-production",
		ProfileID:          agentcore.MemoryProfileID("memory-profile-1"),
		CandidateID:        "candidate-lifecycle-production",
		Scope:              memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		Layer:              memcore.MemoryLayerFact,
		Topic:              "release",
		SourceRefs:         []string{"summary:release"},
		SourceEvidenceRefs: []string{"evidence:release"},
		Content:            "Release lifecycle fact for Zhang.",
		Summary:            "Release lifecycle fact for Zhang.",
		Facts:              []string{"Release requires review by Zhang."},
		Entities:           map[string]string{"person": "Zhang"},
		LifecycleStatus:    memcore.MemoryLifecycleActive,
		Visibility:         memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeMission, ScopeID: "mission-1"},
		Sensitivity:        memcore.SensitivityNormal,
		Confidence:         0.9,
		ExpiresAt:          now.Add(-time.Hour),
		CreatedBy:          agentcore.AgentID("reviewer-1"),
		CreatedAt:          now.Add(-24 * time.Hour),
		UpdatedAt:          now,
	}
	if err := memoryStore.SaveRecord(ctx, record); err != nil {
		t.Fatalf("save record: %v", err)
	}
	sink, err := newProductionOntologyMemoryRecordMetadataSink(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology memory sink: %v", err)
	}
	currentTime := now
	memoryAdmin := memoryadmin.NewService(memoryStore, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return currentTime }).
		WithMemoryRecordMetadataSink(sink)
	viewer := memoryadmin.ViewerRef{
		ViewerID:        "reviewer-1",
		PermissionScope: []string{memoryadmin.PermissionMemoryRead, memoryadmin.PermissionMemoryReview, memoryadmin.PermissionMemoryAuditRead},
		MissionIDs:      []string{"mission-1"},
	}

	currentTime = now.Add(1 * time.Hour)
	if _, err := memoryAdmin.TouchRecords(ctx, viewer, memoryadmin.TouchMemoryRecordsRequest{MemoryIDs: []string{record.ID}, Reason: "prompt context"}); err != nil {
		t.Fatalf("TouchRecords: %v", err)
	}
	currentTime = now.Add(2 * time.Hour)
	if _, err := memoryAdmin.ArchiveRecord(ctx, viewer, record.ID, memoryadmin.MemoryLifecycleRequest{Reason: "superseded"}); err != nil {
		t.Fatalf("ArchiveRecord: %v", err)
	}
	currentTime = now.Add(3 * time.Hour)
	if _, err := memoryAdmin.DeleteRecord(ctx, viewer, record.ID, memoryadmin.MemoryLifecycleRequest{Reason: "removed"}); err != nil {
		t.Fatalf("DeleteRecord: %v", err)
	}
	currentTime = now.Add(4 * time.Hour)
	if _, err := memoryAdmin.AnonymizeRecord(ctx, viewer, record.ID, memoryadmin.MemoryLifecycleRequest{Reason: "privacy"}); err != nil {
		t.Fatalf("AnonymizeRecord: %v", err)
	}

	objects, _, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "mission:mission-1")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	if len(objects) != 1 || objects[0].ObjectType != "memory_record" || objects[0].ID != record.ID || objects[0].Label != "Release lifecycle fact for [redacted]." {
		t.Fatalf("objects = %+v", objects)
	}
	assertContextGraphFact(t, facts, "memory_record", record.ID, "lifecycle_status", string(memcore.MemoryLifecycleAnonymized))
	assertContextGraphFact(t, facts, "memory_record", record.ID, "reference_count", 1)
	assertContextGraphFact(t, facts, "memory_record", record.ID, "last_referenced_at", now.Add(1*time.Hour).UTC().Format(time.RFC3339Nano))
	wantEvidence := map[string]bool{
		"memory-record-lifecycle-updated:" + record.ID + ":" + string(memcore.MemoryLifecycleActive):     false,
		"memory-record-lifecycle-updated:" + record.ID + ":" + string(memcore.MemoryLifecycleArchived):   false,
		"memory-record-lifecycle-updated:" + record.ID + ":" + string(memcore.MemoryLifecycleDeleted):    false,
		"memory-record-lifecycle-updated:" + record.ID + ":" + string(memcore.MemoryLifecycleAnonymized): false,
	}
	for _, item := range evidence {
		if _, ok := wantEvidence[item.ID]; ok {
			wantEvidence[item.ID] = true
		}
	}
	for id, seen := range wantEvidence {
		if !seen {
			t.Fatalf("evidence %s missing in %+v", id, evidence)
		}
	}
	events, err := newProductionOntologyEventLog(core).List(ctx)
	if err != nil {
		t.Fatalf("list ontology events: %v", err)
	}
	assertOntologyEventType(t, events, "memory.record.lifecycle.updated")
}

func TestProductionMemoryDistillSourceFactUpdatesOntologyContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-memory-distill-ontology"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	core, ok := databases.Get(db.RoleCore)
	if !ok || core == nil {
		t.Fatalf("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(core)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}

	now := time.Date(2026, 6, 20, 10, 0, 0, 0, time.UTC)
	memoryStore := memstore.NewPlatformStore(core)
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:        "candidate-source-fact-production",
		MissionID: "project-1",
		Status:    memcore.CandidateAccepted,
		CreatedBy: agentcore.AgentID("reviewer-1"),
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save source candidate: %v", err)
	}
	sourceFact := memcore.MemoryRecord{
		ID:                 "memory-source-fact-production",
		ProfileID:          agentcore.MemoryProfileID("memory-profile-1"),
		CandidateID:        "candidate-source-fact-production",
		Scope:              memcore.MemoryScope{Kind: memcore.ScopeProject, ID: "project-1"},
		Layer:              memcore.MemoryLayerFact,
		Topic:              "qa",
		SourceRefs:         []string{"qa_result:qa-1"},
		SourceEvidenceRefs: []string{"evidence:qa-1"},
		Summary:            "QA review needs earlier regression evidence.",
		Facts:              []string{"QA review needs earlier regression evidence."},
		LifecycleStatus:    memcore.MemoryLifecycleActive,
		Visibility:         memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeProject, ScopeID: "project-1"},
		Sensitivity:        memcore.SensitivityNormal,
		Confidence:         0.88,
		CreatedBy:          agentcore.AgentID("reviewer-1"),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := memoryStore.SaveRecord(ctx, sourceFact); err != nil {
		t.Fatalf("save source fact: %v", err)
	}
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-experience-production",
		MissionID:     "project-1",
		SourceType:    memcore.SourceSummary,
		SourceRef:     "memory_distill:qa",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeProject, ID: "project-1"},
		ProposedKind:  agentcore.MemoryKindProject,
		ProposedLayer: memcore.MemoryLayerExperience,
		Topic:         "qa",
		Summary:       "QA should attach regression evidence before review.",
		Lessons:       []string{"Attach regression evidence before QA review."},
		LineageRefs:   []string{"memory:" + sourceFact.ID},
		Confidence:    0.81,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("distiller-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("save experience candidate: %v", err)
	}
	sink, err := newProductionOntologyMemoryRecordMetadataSink(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology memory sink: %v", err)
	}
	memoryAdmin := memoryadmin.NewService(memoryStore, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithMemoryRecordMetadataSink(sink)

	experience, err := memoryAdmin.AcceptCandidate(ctx, memoryadmin.ViewerRef{
		ViewerID:        "reviewer-1",
		PermissionScope: []string{memoryadmin.PermissionMemoryReview},
		ProjectIDs:      []string{"project-1"},
	}, "candidate-experience-production", memoryadmin.AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-experience-production",
	})
	if err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "project:project-1")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "memory_record", sourceFact.ID)
	assertContextGraphObject(t, objects, "memory_record", experience.MemoryID)
	assertContextGraphLink(t, links, "memory_distilled_from_memory", experience.MemoryID, sourceFact.ID)
	assertContextGraphFact(t, facts, "memory_record", sourceFact.ID, "lifecycle_status", string(memcore.MemoryLifecycleDistilled))
	assertContextGraphFact(t, facts, "memory_record", sourceFact.ID, "reference_count", 0)
	assertContextGraphFact(t, facts, "memory_record", sourceFact.ID, "last_referenced_at", "")
	assertContextGraphEvidence(t, evidence, "memory-lineage-linked:"+experience.MemoryID+":"+sourceFact.ID)
	assertContextGraphEvidence(t, evidence, "memory-record-lifecycle-updated:"+sourceFact.ID+":"+string(memcore.MemoryLifecycleDistilled))
}

func TestProductionWorldMemoryLineageMissingSourceDoesNotBlockAcceptedRecord(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-world-memory-lineage-missing-source"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	core, ok := databases.Get(db.RoleCore)
	if !ok || core == nil {
		t.Fatalf("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(core)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}

	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	memoryStore := memstore.NewPlatformStore(core)
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:        "candidate-source-experience-world",
		Status:    memcore.CandidateAccepted,
		CreatedBy: agentcore.AgentID("reviewer-1"),
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save source candidate: %v", err)
	}
	sourceExperience := memcore.MemoryRecord{
		ID:                 "memory-source-experience-world",
		ProfileID:          agentcore.MemoryProfileID("memory-profile-world"),
		CandidateID:        "candidate-source-experience-world",
		Scope:              memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		Layer:              memcore.MemoryLayerExperience,
		Topic:              "quality",
		SourceRefs:         []string{"summary:source-experience"},
		SourceEvidenceRefs: []string{"evidence:source-experience"},
		Summary:            "Accepted source experience is not yet projected.",
		LifecycleStatus:    memcore.MemoryLifecycleActive,
		Visibility:         memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeOrganization, ScopeID: "org-1"},
		Sensitivity:        memcore.SensitivityNormal,
		DeIDLevel:          memcore.DeIDTeamSafe,
		Confidence:         0.86,
		CreatedBy:          agentcore.AgentID("reviewer-1"),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := memoryStore.SaveRecord(ctx, sourceExperience); err != nil {
		t.Fatalf("save source experience: %v", err)
	}
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-world-lineage-production",
		SourceType:    memcore.SourceSummary,
		SourceRef:     "world:organization:org-1:quality",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeOrganization, ID: "org-1"},
		ProposedKind:  agentcore.MemoryKindOrganization,
		ProposedLayer: memcore.MemoryLayerWorld,
		Topic:         "quality",
		Summary:       "Organization quality guidance should cite accepted source experience.",
		Lessons:       []string{"Keep accepted source experience as lineage, not as policy."},
		LineageRefs:   []string{"memory:" + sourceExperience.ID},
		Sensitivity:   memcore.SensitivityNormal,
		DeIDLevel:     memcore.DeIDTeamSafe,
		Confidence:    0.82,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("distiller-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("save world candidate: %v", err)
	}
	sink, err := newProductionOntologyMemoryRecordMetadataSink(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology memory sink: %v", err)
	}
	memoryAdmin := memoryadmin.NewService(memoryStore, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithMemoryRecordMetadataSink(sink)

	world, err := memoryAdmin.AcceptCandidate(ctx, memoryadmin.ViewerRef{
		ViewerID:        "world-manager-1",
		PermissionScope: []string{memoryadmin.PermissionMemoryWorldManage},
		OrganizationIDs: []string{"org-1"},
	}, "candidate-world-lineage-production", memoryadmin.AcceptCandidateRequest{
		ProfileID: "memory-profile-world",
		MemoryID:  "memory-world-lineage-production",
	})
	if err != nil {
		t.Fatalf("AcceptCandidate should not fail when lineage source is missing from ContextGraph: %v", err)
	}

	objects, links, _, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "memory_record", world.MemoryID)
	if len(links) != 0 {
		t.Fatalf("lineage source object is missing, so no lineage link should be written: %+v", links)
	}
	assertContextGraphEvidence(t, evidence, "memory-record-accepted:"+world.MemoryID)

	audit := assertProjectionAudit(t, ctx, contextgraph.NewPlatformStore(core), "org-1", "projection-skipped:memory-lineage-linked:"+world.MemoryID+":"+sourceExperience.ID+":missing-lineage-targets", "memory.lineage.linked", "missing_lineage_target")
	assertProjectionAuditTarget(t, audit, "missing_targets", "memory_record", sourceExperience.ID)

	unresolvedAudit, err := sink.(productionMemoryRecordOntologySink).computeProjectionAuditReplay(ctx, audit)
	if err != nil {
		t.Fatalf("ReplayProjectionAudit unresolved: %v", err)
	}
	if unresolvedAudit.Status != "skipped" {
		t.Fatalf("unresolved replay status = %q, want skipped", unresolvedAudit.Status)
	}
	_, links, _, _, err = contextgraph.NewPlatformStore(core).Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("context graph snapshot after unresolved replay: %v", err)
	}
	if len(links) != 0 {
		t.Fatalf("unresolved replay should not write lineage links: %+v", links)
	}

	if err := contextgraph.NewPlatformStore(core).SaveObject(ctx, contextgraph.Object{
		ID:           sourceExperience.ID,
		TenantID:     "org-1",
		ObjectType:   "memory_record",
		CanonicalKey: "memory_record:" + sourceExperience.ID,
		Label:        sourceExperience.Summary,
		Status:       "active",
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("save recovered source memory object: %v", err)
	}
	replayedAudit, err := sink.(productionMemoryRecordOntologySink).computeProjectionAuditReplay(ctx, audit)
	if err != nil {
		t.Fatalf("ReplayProjectionAudit: %v", err)
	}
	if replayedAudit.Status != "replayed" {
		t.Fatalf("replayed audit status = %q, want replayed", replayedAudit.Status)
	}
	if replayedAudit.ReasonCode != "missing_lineage_target" {
		t.Fatalf("replayed audit reason code = %q, want original missing_lineage_target", replayedAudit.ReasonCode)
	}
	_, links, _, _, err = contextgraph.NewPlatformStore(core).Snapshot(ctx, "org-1")
	if err != nil {
		t.Fatalf("context graph snapshot after replay: %v", err)
	}
	assertContextGraphLink(t, links, "memory_distilled_from_memory", world.MemoryID, sourceExperience.ID)
}

func TestProductionMemoryRetentionSweepUpdatesOntologyContextGraph(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-memory-retention-ontology"},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer databases.Close()
	core, ok := databases.Get(db.RoleCore)
	if !ok || core == nil {
		t.Fatalf("core database missing")
	}
	coordinator := migration.NewCoordinator(databases)
	if err := ontology.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register ontology migrations: %v", err)
	}
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate core db: %v", err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(core)); err != nil {
		t.Fatalf("install default ontology: %v", err)
	}

	now := time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC)
	memoryStore := memstore.NewPlatformStore(core)
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:        "candidate-retention-production",
		MissionID: "mission-1",
		Status:    memcore.CandidateAccepted,
		CreatedBy: agentcore.AgentID("reviewer-1"),
		CreatedAt: now.Add(-48 * time.Hour),
		UpdatedAt: now.Add(-48 * time.Hour),
	}); err != nil {
		t.Fatalf("save candidate: %v", err)
	}
	record := memcore.MemoryRecord{
		ID:                 "memory-record-retention-production",
		ProfileID:          agentcore.MemoryProfileID("memory-profile-1"),
		CandidateID:        "candidate-retention-production",
		Scope:              memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-1"},
		Layer:              memcore.MemoryLayerFact,
		Topic:              "retention",
		SourceRefs:         []string{"summary:retention"},
		SourceEvidenceRefs: []string{"evidence:retention"},
		Summary:            "Expired unused lifecycle fact.",
		LifecycleStatus:    memcore.MemoryLifecycleActive,
		Visibility:         memcore.Visibility{Mode: memcore.VisibilityScopedMembers, ScopeKind: memcore.ScopeMission, ScopeID: "mission-1"},
		Sensitivity:        memcore.SensitivityNormal,
		Confidence:         0.8,
		ExpiresAt:          now.Add(-time.Hour),
		CreatedBy:          agentcore.AgentID("reviewer-1"),
		CreatedAt:          now.Add(-48 * time.Hour),
		UpdatedAt:          now.Add(-48 * time.Hour),
	}
	if err := memoryStore.SaveRecord(ctx, record); err != nil {
		t.Fatalf("save record: %v", err)
	}
	sink, err := newProductionOntologyMemoryRecordMetadataSink(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology memory sink: %v", err)
	}
	memoryAdmin := memoryadmin.NewService(memoryStore, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithMemoryRecordMetadataSink(sink)
	viewer := memoryadmin.ViewerRef{
		ViewerID:        "reviewer-1",
		PermissionScope: []string{memoryadmin.PermissionMemoryReview, memoryadmin.PermissionMemoryAuditRead},
		MissionIDs:      []string{"mission-1"},
	}

	result, err := memoryAdmin.SweepRetention(ctx, viewer, memoryadmin.MemoryRetentionSweepRequest{Reason: "nightly retention"})
	if err != nil {
		t.Fatalf("SweepRetention: %v", err)
	}
	if result.ArchivedCount != 1 || len(result.ArchivedMemoryIDs) != 1 || result.ArchivedMemoryIDs[0] != record.ID {
		t.Fatalf("sweep result = %+v", result)
	}

	objects, _, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "mission:mission-1")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	if len(objects) != 1 || objects[0].ObjectType != "memory_record" || objects[0].ID != record.ID {
		t.Fatalf("objects = %+v", objects)
	}
	assertContextGraphFact(t, facts, "memory_record", record.ID, "lifecycle_status", string(memcore.MemoryLifecycleArchived))
	assertContextGraphFact(t, facts, "memory_record", record.ID, "reference_count", 0)
	assertContextGraphFact(t, facts, "memory_record", record.ID, "last_referenced_at", "")
	wantEvidenceID := "memory-record-lifecycle-updated:" + record.ID + ":" + string(memcore.MemoryLifecycleArchived)
	for _, item := range evidence {
		if item.ID == wantEvidenceID {
			return
		}
	}
	t.Fatalf("evidence %s missing in %+v", wantEvidenceID, evidence)
}
