package main

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/memoryadmin"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontology"
	memcore "orrery_v2/internal/memory/core"
	memstore "orrery_v2/internal/memory/store"
	memtestkit "orrery_v2/internal/memory/testkit"
	missioncore "orrery_v2/internal/mission/core"
	missionprojection "orrery_v2/internal/mission/projection"
	missionstorage "orrery_v2/internal/mission/storage"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/migration"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionMemoryScopeWritesOntologyContextGraphLinks(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-memory-scope-ontology"},
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

	now := time.Date(2026, 6, 20, 11, 0, 0, 0, time.UTC)
	ontologyRunner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology runner: %v", err)
	}
	if _, err := ontologyRunner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "mission-created:mission-scope-1",
		TenantID: "mission:mission-scope-1",
		Type:     "mission.created",
		StreamID: "mission-scope-1",
		Version:  1,
		Payload: map[string]any{
			"id":     "mission-scope-1",
			"title":  "Scope mission",
			"status": "active",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "mission"},
		OccurredAt:  now,
	}); err != nil {
		t.Fatalf("apply mission created event: %v", err)
	}
	if _, err := ontologyRunner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "mission-task-created:task-scope-1",
		TenantID: "mission:mission-scope-1",
		Type:     "mission.task.created",
		StreamID: "task-scope-1",
		Version:  1,
		Payload: map[string]any{
			"mission_id": "mission-scope-1",
			"task_id":    "task-scope-1",
			"title":      "Scope task",
			"status":     "created",
			"event_id":   "mission-task-created:task-scope-1",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "mission"},
		OccurredAt:  now,
	}); err != nil {
		t.Fatalf("apply mission task created event: %v", err)
	}

	memoryStore := memstore.NewPlatformStore(core)
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-scope-production",
		MissionID:     "mission-scope-1",
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-scope-1",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-scope-1"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "scope",
		Summary:       "Scope facts should be traversable from mission and task.",
		EvidenceRefs:  []string{"evidence:scope-1"},
		LineageRefs:   []string{"task_contract:task-scope-1"},
		Confidence:    0.9,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("save candidate: %v", err)
	}
	sink, err := newProductionOntologyMemoryRecordMetadataSink(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology memory sink: %v", err)
	}
	memoryAdmin := memoryadmin.NewService(memoryStore, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithMemoryRecordMetadataSink(sink)

	record, err := memoryAdmin.AcceptCandidate(ctx, memoryadmin.ViewerRef{
		ViewerID:        "reviewer-1",
		PermissionScope: []string{memoryadmin.PermissionMemoryReview},
		MissionIDs:      []string{"mission-scope-1"},
	}, "candidate-scope-production", memoryadmin.AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-scope-production",
	})
	if err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	objects, links, _, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "mission:mission-scope-1")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "memory_record", record.MemoryID)
	assertContextGraphObject(t, objects, "mission", "mission-scope-1")
	assertContextGraphObject(t, objects, "task", "task-scope-1")
	assertContextGraphLink(t, links, "memory_about_mission", record.MemoryID, "mission-scope-1")
	assertContextGraphLink(t, links, "memory_about_task", record.MemoryID, "task-scope-1")
	assertContextGraphEvidence(t, evidence, "memory-scope-linked:"+record.MemoryID)
}

func TestProductionMissionTaskCreationWritesOntologyContextGraphScopeTargets(t *testing.T) {
	ctx := context.Background()
	rt := testkit.NewInitializedTestRuntime(t, platform.ModuleFunc(missionstorage.RegisterMissionStorage), OntologyModule())
	core, err := rt.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core database: %v", err)
	}
	missionRepo := missioncontrol.NewDBProjectionRepository(rt.DBRouter)
	authz := newProductionMissionAuthorizationTestFixture()
	lifecycle := missioncontrol.NewLifecycleService(rt.UnitOfWork, rt.DBRouter, missionRepo, missioncontrol.LifecycleOptions{
		Clock:                  rt.Clock,
		IDGenerator:            rt.IDGenerator,
		AuthorizationService:   authz.service,
		AuthorizationRelations: authz.relations,
	})
	missionCommand := missioncontrol.NewCommandServiceWithMissionPorts(nil, nil, nil, nil, lifecycle)
	authz.wireCommand(missionCommand)
	missionQuery := missioncontrol.NewQueryServiceWithCatalog(missionRepo, lifecycle)
	authz.wireQuery(missionQuery)
	ontologyRunner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology runner: %v", err)
	}
	api := productionAPI{
		missionQuery:    missionQuery,
		missionCommand:  missionCommand,
		missionRepo:     missionRepo,
		unitOfWork:      rt.UnitOfWork,
		taskRepo:        missiontask.NewPlatformRepository(),
		ontologyProject: ontologyRunner,
		ontologyEvents:  newProductionOntologyEventLog(core),
	}

	viewer := authorizedMissionTestViewer("planner-1", missioncontrol.ViewerKindUser)
	mission, err := api.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{
		IdempotencyKey: "mission-scope-target",
		Title:          "Ontology mission",
		FinalGoal:      "Create authority-owned scope targets.",
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}
	task, err := api.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey:     "task-scope-target",
		MissionID:          missioncore.MissionID(mission.MissionID),
		AssigneeID:         "",
		Goal:               "Write mission task to ontology",
		WorkerPersonalGoal: "Keep memory scope links attached to existing targets",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	objects, links, facts, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, missionScopeTenantID("global", mission.MissionID))
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "mission", mission.MissionID)
	assertContextGraphObject(t, objects, "task", task.TaskID)
	assertContextGraphLink(t, links, "mission_has_task", mission.MissionID, task.TaskID)
	assertContextGraphFact(t, facts, "mission", mission.MissionID, "title", mission.Title)
	assertContextGraphFact(t, facts, "mission", mission.MissionID, "status", mission.Status)
	assertContextGraphFact(t, facts, "task", task.TaskID, "title", task.Title)
	assertContextGraphFact(t, facts, "task", task.TaskID, "status", string(missionprojection.TaskStatusCreated))
	assertContextGraphEvidence(t, evidence, "mission-created:"+mission.MissionID)
	assertContextGraphEvidence(t, evidence, "mission-task-created:"+task.TaskID)
	events, err := api.ontologyEvents.List(ctx)
	if err != nil {
		t.Fatalf("list ontology events: %v", err)
	}
	assertOntologyEvent(t, events, "mission-created:"+mission.MissionID, "mission.created")
	assertOntologyEvent(t, events, "mission-task-created:"+task.TaskID, "mission.task.created")
}

func TestProductionCreatedMissionTaskAllowsMemoryScopeLinks(t *testing.T) {
	ctx := context.Background()
	rt := testkit.NewInitializedTestRuntime(t, platform.ModuleFunc(missionstorage.RegisterMissionStorage), OntologyModule())
	core, err := rt.Databases.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core database: %v", err)
	}
	missionRepo := missioncontrol.NewDBProjectionRepository(rt.DBRouter)
	authz := newProductionMissionAuthorizationTestFixture()
	lifecycle := missioncontrol.NewLifecycleService(rt.UnitOfWork, rt.DBRouter, missionRepo, missioncontrol.LifecycleOptions{
		Clock:                  rt.Clock,
		IDGenerator:            rt.IDGenerator,
		AuthorizationService:   authz.service,
		AuthorizationRelations: authz.relations,
	})
	ontologyRunner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology runner: %v", err)
	}
	missionCommand := missioncontrol.NewCommandServiceWithMissionPorts(nil, nil, nil, nil, lifecycle)
	authz.wireCommand(missionCommand)
	missionQuery := missioncontrol.NewQueryServiceWithCatalog(missionRepo, lifecycle)
	authz.wireQuery(missionQuery)
	api := productionAPI{
		missionQuery:    missionQuery,
		missionCommand:  missionCommand,
		missionRepo:     missionRepo,
		unitOfWork:      rt.UnitOfWork,
		taskRepo:        missiontask.NewPlatformRepository(),
		ontologyProject: ontologyRunner,
	}
	viewer := authorizedMissionTestViewer("planner-1", missioncontrol.ViewerKindUser)
	mission, err := api.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{
		IdempotencyKey: "mission-memory-scope-target",
		Title:          "Memory scope mission",
		FinalGoal:      "Create scope targets before memory acceptance.",
	})
	if err != nil {
		t.Fatalf("CreateMission: %v", err)
	}
	task, err := api.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey:     "task-memory-scope-target",
		MissionID:          missioncore.MissionID(mission.MissionID),
		Goal:               "Attach memory scope to task",
		WorkerPersonalGoal: "Keep memory edges navigable",
	})
	if err != nil {
		t.Fatalf("CreateTask: %v", err)
	}

	now := time.Date(2026, 6, 20, 12, 30, 0, 0, time.UTC)
	memoryStore := memstore.NewPlatformStore(core)
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-production-created-scope",
		MissionID:     mission.MissionID,
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-created-scope",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: mission.MissionID},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "scope",
		Summary:       "Created mission and task should receive memory scope links.",
		EvidenceRefs:  []string{"evidence:created-scope"},
		LineageRefs:   []string{"task_contract:" + task.TaskID},
		Confidence:    0.93,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("save candidate: %v", err)
	}
	sink, err := newProductionOntologyMemoryRecordMetadataSink(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology memory sink: %v", err)
	}
	memoryAdmin := memoryadmin.NewService(memoryStore, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithMemoryRecordMetadataSink(sink)

	record, err := memoryAdmin.AcceptCandidate(ctx, memoryadmin.ViewerRef{
		ViewerID:        "reviewer-1",
		PermissionScope: []string{memoryadmin.PermissionMemoryReview},
		MissionIDs:      []string{mission.MissionID},
	}, "candidate-production-created-scope", memoryadmin.AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-production-created-scope",
	})
	if err != nil {
		t.Fatalf("AcceptCandidate: %v", err)
	}

	objects, links, _, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, missionScopeTenantID("global", mission.MissionID))
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "memory_record", record.MemoryID)
	assertContextGraphLink(t, links, "memory_about_mission", record.MemoryID, mission.MissionID)
	assertContextGraphLink(t, links, "memory_about_task", record.MemoryID, task.TaskID)
	assertContextGraphEvidence(t, evidence, "memory-scope-linked:"+record.MemoryID)
}

func TestProductionMemoryScopeMissingTargetDoesNotBlockAcceptedRecord(t *testing.T) {
	ctx := context.Background()
	databases, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: "production-memory-scope-missing-target"},
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

	now := time.Date(2026, 6, 20, 11, 30, 0, 0, time.UTC)
	memoryStore := memstore.NewPlatformStore(core)
	if err := memoryStore.SaveCandidate(ctx, memcore.MemoryCandidate{
		ID:            "candidate-scope-missing-target",
		MissionID:     "mission-missing-scope",
		SourceType:    memcore.SourceEvidence,
		SourceRef:     "evidence-scope-missing",
		ProposedScope: memcore.MemoryScope{Kind: memcore.ScopeMission, ID: "mission-missing-scope"},
		ProposedKind:  agentcore.MemoryKindMission,
		ProposedLayer: memcore.MemoryLayerFact,
		Topic:         "scope",
		Summary:       "Accepted memory should not require preprojected mission object.",
		EvidenceRefs:  []string{"evidence:scope-missing"},
		Confidence:    0.9,
		Status:        memcore.CandidateProposed,
		CreatedBy:     agentcore.AgentID("agent-1"),
		CreatedAt:     now,
		UpdatedAt:     now,
	}); err != nil {
		t.Fatalf("save candidate: %v", err)
	}
	sink, err := newProductionOntologyMemoryRecordMetadataSink(ctx, core)
	if err != nil {
		t.Fatalf("new production ontology memory sink: %v", err)
	}
	memoryAdmin := memoryadmin.NewService(memoryStore, &memtestkit.MemoryAuditRecorder{}, func() time.Time { return now }).
		WithMemoryRecordMetadataSink(sink)

	record, err := memoryAdmin.AcceptCandidate(ctx, memoryadmin.ViewerRef{
		ViewerID:        "reviewer-1",
		PermissionScope: []string{memoryadmin.PermissionMemoryReview},
		MissionIDs:      []string{"mission-missing-scope"},
	}, "candidate-scope-missing-target", memoryadmin.AcceptCandidateRequest{
		ProfileID: "memory-profile-1",
		MemoryID:  "memory-scope-missing-target",
	})
	if err != nil {
		t.Fatalf("AcceptCandidate should not fail when scope target is missing from ContextGraph: %v", err)
	}

	objects, links, _, evidence, err := contextgraph.NewPlatformStore(core).Snapshot(ctx, "mission:mission-missing-scope")
	if err != nil {
		t.Fatalf("context graph snapshot: %v", err)
	}
	assertContextGraphObject(t, objects, "memory_record", record.MemoryID)
	if len(links) != 0 {
		t.Fatalf("scope target is missing, so no scope link should be written: %+v", links)
	}
	assertContextGraphEvidence(t, evidence, "memory-record-accepted:"+record.MemoryID)

	audit := assertProjectionAudit(t, ctx, contextgraph.NewPlatformStore(core), "mission:mission-missing-scope", "projection-skipped:memory-scope-linked:"+record.MemoryID+":missing-scope-targets", "memory.scope.linked", "missing_scope_target")
	assertProjectionAuditTarget(t, audit, "dropped_targets", "mission", "mission-missing-scope")
	events, err := newProductionOntologyEventLog(core).List(ctx)
	if err != nil {
		t.Fatalf("list ontology events: %v", err)
	}
	assertOntologyEvent(t, events, "memory-scope-linked:"+record.MemoryID, "memory.scope.linked")
	assertOntologyEventPayload(t, events, "memory-scope-linked:"+record.MemoryID, "mission_id", "mission-missing-scope")

	unresolvedAudit, err := sink.(productionMemoryRecordOntologySink).computeProjectionAuditReplay(ctx, audit)
	if err != nil {
		t.Fatalf("ReplayProjectionAudit unresolved: %v", err)
	}
	if unresolvedAudit.Status != "skipped" {
		t.Fatalf("unresolved replay status = %q, want skipped", unresolvedAudit.Status)
	}
	_, links, _, _, err = contextgraph.NewPlatformStore(core).Snapshot(ctx, "mission:mission-missing-scope")
	if err != nil {
		t.Fatalf("context graph snapshot after unresolved replay: %v", err)
	}
	if len(links) != 0 {
		t.Fatalf("unresolved replay should not write scope links: %+v", links)
	}

	if err := contextgraph.NewPlatformStore(core).SaveObject(ctx, contextgraph.Object{
		ID:           "mission-missing-scope",
		TenantID:     "mission:mission-missing-scope",
		ObjectType:   "mission",
		CanonicalKey: "mission:mission-missing-scope",
		Label:        "Recovered mission target",
		Status:       "active",
		UpdatedAt:    now,
	}); err != nil {
		t.Fatalf("save recovered mission target: %v", err)
	}
	replayedAudit, err := sink.(productionMemoryRecordOntologySink).computeProjectionAuditReplay(ctx, audit)
	if err != nil {
		t.Fatalf("ReplayProjectionAudit: %v", err)
	}
	if replayedAudit.Status != "replayed" {
		t.Fatalf("replayed audit status = %q, want replayed", replayedAudit.Status)
	}
	if replayedAudit.ReasonCode != "missing_scope_target" {
		t.Fatalf("replayed audit reason code = %q, want original missing_scope_target", replayedAudit.ReasonCode)
	}
	_, links, _, _, err = contextgraph.NewPlatformStore(core).Snapshot(ctx, "mission:mission-missing-scope")
	if err != nil {
		t.Fatalf("context graph snapshot after replay: %v", err)
	}
	assertContextGraphLink(t, links, "memory_about_mission", record.MemoryID, "mission-missing-scope")
}
