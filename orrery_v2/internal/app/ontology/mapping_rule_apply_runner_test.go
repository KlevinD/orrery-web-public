package ontology

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/graphprotocol"
	"orrery_v2/internal/platform/migration"
)

func TestMappingRuleProjectionRunnerApplyStateEventWritesGraphOnce(t *testing.T) {
	ctx := context.Background()
	registry, err := db.OpenRegistry(ctx, db.RegistryConfig{Databases: map[string]db.Config{
		db.RoleCore: {Driver: db.DriverMemory, Name: t.Name()},
	}})
	if err != nil {
		t.Fatalf("open database registry: %v", err)
	}
	defer registry.Close()
	coordinator := migration.NewCoordinator(registry)
	if err := contextgraph.RegisterMigrations(coordinator); err != nil {
		t.Fatalf("register context graph migrations: %v", err)
	}
	if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
		t.Fatalf("migrate context graph: %v", err)
	}
	core, err := registry.Store(db.RoleCore)
	if err != nil {
		t.Fatalf("core store: %v", err)
	}
	if err := migration.NewRunner([]migration.Migration{ontologyStateProjectionMigration()}).Up(ctx, core); err != nil {
		t.Fatalf("migrate state projection: %v", err)
	}

	definitions := NewMemoryRegistry()
	if err := definitions.SaveObjectType(ctx, ObjectType{
		ID: "task", DisplayName: "Task", CanonicalKeyRule: "task:{id}",
	}); err != nil {
		t.Fatalf("save task object type: %v", err)
	}
	gate := graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(definitions))
	projections := graphprotocol.NewProjectionRegistry(gate)
	var projectionCalls atomic.Int32
	if err := projections.Register(ctx, graphprotocol.StaticProjection{
		SpecValue: graphprotocol.ProjectionSpec{
			Name: "task.state", Version: "v1", SourceType: "scheduler.task.updated", ObjectTypes: []string{"task"},
		},
		ProjectFunc: func(_ context.Context, event graphprotocol.DomainEvent) (graphprotocol.ProjectionResult, error) {
			projectionCalls.Add(1)
			return graphprotocol.ProjectionResult{
				Cursor: graphprotocol.ProjectionCursor{SourceID: event.ID, SourceVersion: event.Version},
				Assertions: []graphprotocol.Assertion{graphprotocol.ObjectAssertion{
					Object:       graphprotocol.ObjectRef{TenantID: event.TenantID, Type: "task", ID: event.StreamID},
					CanonicalKey: "task:" + event.StreamID,
					Label:        "Task 1",
					Provenance: graphprotocol.ProvenanceRef{
						SourceEventID: event.ID, SourceEventType: event.Type, SourceSystem: "test",
						SourceStreamID: event.StreamID, SourceVersion: event.Version,
						ObservedAt: event.OccurredAt, Actor: event.PublishedBy, SourceAccessRef: event.SourceAccessRef,
					},
				}},
			}, nil
		},
	}); err != nil {
		t.Fatalf("register projection: %v", err)
	}
	runner := NewMappingRuleProjectionRunner(projections, gate, contextgraph.NewPlatformStore(core))
	stateStore := NewPlatformStateProjectionStore(core)
	event := graphprotocol.DomainEvent{
		ID: "task-state:1", TenantID: "tenant-1", Type: "scheduler.task.updated",
		StreamID: "task-1", Version: 1, OccurredAt: time.Date(2026, 8, 22, 1, 0, 0, 0, time.UTC),
		Payload:         map[string]any{"id": "task-1", "enabled": true},
		PublishedBy:     graphprotocol.ActorRef{Kind: "system", ID: "scheduler"},
		SourceAccessRef: graphprotocol.AccessRef{Kind: "tenant", ID: "tenant-1", TenantID: "tenant-1"},
	}

	for index := range 100 {
		report, err := runner.ApplyStateEvent(ctx, stateStore, StateProjectionEvent{
			EntityID: "task-1", SnapshotAt: time.Date(2026, 8, 22, 2, 0, 0, 0, time.UTC), Event: event,
		})
		if err != nil {
			t.Fatalf("ApplyStateEvent(%d): %v", index, err)
		}
		want := StateProjectionNoop
		if index == 0 {
			want = StateProjectionChanged
		}
		if report.Outcome != want {
			t.Fatalf("ApplyStateEvent(%d) outcome = %s, want %s", index, report.Outcome, want)
		}
	}
	if projectionCalls.Load() != 1 {
		t.Fatalf("projection calls = %d, want 1", projectionCalls.Load())
	}
	var objects int
	if err := core.QueryRow(ctx, `SELECT COUNT(*) FROM context_graph_objects`).Scan(&objects); err != nil || objects != 1 {
		t.Fatalf("context graph objects = %d, err=%v", objects, err)
	}
}

func TestMappingRuleProjectionRunnerAppliesEventToContextGraph(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := RegisterMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry, ""); err != nil {
		t.Fatalf("RegisterMappingRuleProjections: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:         "event-1",
		TenantID:   "tenant-1",
		Type:       "mission.task.created",
		Version:    3,
		OccurredAt: time.Unix(30, 0).UTC(),
		Payload: map[string]any{
			"event_id":   "event-1",
			"mission_id": "mission-1",
			"task_id":    "task-1",
			"title":      "Draft ontology runner",
			"status":     "created",
		},
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "mission"},
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.AppliedProjectionCount != 1 {
		t.Fatalf("report = %+v", report)
	}
	objects, links, facts, evidence, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	materialized := materializedContextGraphObjects(objects)
	if len(materialized) != 1 || materialized[0].ID != "task-1" || materialized[0].ObjectType != "task" {
		t.Fatalf("objects = %+v", objects)
	}
	if len(links) != 1 || links[0].LinkType != "mission_has_task" || links[0].FromObjectID != "mission-1" || links[0].ToObjectID != "task-1" {
		t.Fatalf("links = %+v", links)
	}
	if len(facts) != 2 || facts[0].Subject.ID != "task-1" || facts[0].Predicate == "" {
		t.Fatalf("facts = %+v", facts)
	}
	if len(evidence) != 1 || evidence[0].ID != "event-1" {
		t.Fatalf("evidence = %+v", evidence)
	}
}

func TestMappingRuleProjectionRunnerRejectsNonConformantProjection(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := InstallDefaultOntologyDefinitions(ctx, ontologyRegistry); err != nil {
		t.Fatalf("InstallDefaultOntologyDefinitions: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	projectionRegistry.Register(ctx, graphprotocol.StaticProjection{
		SpecValue: graphprotocol.ProjectionSpec{
			Name:        "bad.task.graph",
			Version:     "v1",
			SourceType:  "mission.task.created",
			ObjectTypes: []string{"task"},
		},
		ProjectFunc: func(context.Context, graphprotocol.DomainEvent) (graphprotocol.ProjectionResult, error) {
			return graphprotocol.ProjectionResult{
				Cursor: graphprotocol.ProjectionCursor{SourceID: "event-1", SourceVersion: 1},
				Assertions: []graphprotocol.Assertion{
					graphprotocol.ObjectAssertion{
						Object:     graphprotocol.ObjectRef{TenantID: "tenant-1", Type: "task", ID: "task-1"},
						Provenance: runnerTestProvenance(),
					},
				},
			}, nil
		},
	})
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:          "event-1",
		TenantID:    "tenant-1",
		Type:        "mission.task.created",
		Version:     1,
		OccurredAt:  time.Unix(30, 0).UTC(),
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "mission"},
	})
	if err == nil {
		t.Fatalf("ApplyEvent should reject nonconformant projection")
	}
	if report.OK || report.AppliedProjectionCount != 0 {
		t.Fatalf("report = %+v", report)
	}
	objects, _, _, _, err := graphStore.Snapshot(ctx, "tenant-1")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 0 {
		t.Fatalf("nonconformant projection should not be applied: %+v", objects)
	}
}

func TestMappingRuleProjectionRunnerSkipsTenantScopedProjectionForOtherTenant(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := ontologyRegistry.SaveObjectType(ctx, ObjectType{
		ID:               "project",
		TenantScope:      TenantScopeTenant,
		DisplayName:      "Project",
		CanonicalKeyRule: "project:{id}",
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := projectionRegistry.Register(ctx, graphprotocol.StaticProjection{
		SpecValue: graphprotocol.ProjectionSpec{
			Name:        "tenant-project.created.graph",
			Version:     "v1",
			TenantID:    "tenant-1",
			SourceType:  "project.created",
			ObjectTypes: []string{"project"},
		},
		ProjectFunc: func(_ context.Context, event graphprotocol.DomainEvent) (graphprotocol.ProjectionResult, error) {
			return graphprotocol.ProjectionResult{
				Cursor: graphprotocol.ProjectionCursor{SourceID: event.ID, SourceVersion: event.Version},
				Assertions: []graphprotocol.Assertion{
					graphprotocol.ObjectAssertion{
						Object:       graphprotocol.ObjectRef{TenantID: event.TenantID, Type: "project", ID: "project-1"},
						CanonicalKey: "project:project-1",
						Provenance: graphprotocol.ProvenanceRef{
							SourceEventID:   event.ID,
							SourceEventType: event.Type,
							SourceSystem:    "test",
							Actor:           event.PublishedBy,
							ObservedAt:      event.OccurredAt,
						},
					},
				},
			}, nil
		},
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}
	graphStore := contextgraph.NewMemoryStore()
	runner := NewMappingRuleProjectionRunner(projectionRegistry, graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)), graphStore)

	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID:          "project-created:tenant-2",
		TenantID:    "tenant-2",
		Type:        "project.created",
		Version:     1,
		OccurredAt:  time.Unix(30, 0).UTC(),
		PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "project"},
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if !report.OK || report.MatchedProjectionCount != 0 || report.AppliedProjectionCount != 0 {
		t.Fatalf("report = %+v", report)
	}
	objects, _, _, _, err := graphStore.Snapshot(ctx, "tenant-2")
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(objects) != 0 {
		t.Fatalf("tenant scoped projection should not apply to tenant-2: %+v", objects)
	}
}

func TestSyncMappingRuleProjectionsRemovesDisabledRules(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := ontologyRegistry.SaveObjectType(ctx, ObjectType{
		ID:               "project",
		TenantScope:      TenantScopeTenant,
		DisplayName:      "Project",
		CanonicalKeyRule: "project:{id}",
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	rule := MappingRule{
		ID:          "project.created",
		TenantID:    "tenant-1",
		EventType:   "project.created",
		ObjectTypes: []string{"project"},
		Enabled:     true,
		Rule: map[string]any{
			"objects": []any{
				map[string]any{"type": "project", "id": "$.project_id", "canonical_key": "project:{project_id}"},
			},
		},
	}
	if err := ontologyRegistry.SaveMappingRule(ctx, rule); err != nil {
		t.Fatalf("SaveMappingRule: %v", err)
	}
	projectionRegistry := graphprotocol.NewProjectionRegistry(graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry)))
	if err := SyncMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry); err != nil {
		t.Fatalf("SyncMappingRuleProjections: %v", err)
	}
	if matched := projectionRegistry.ListBySourceType("project.created"); len(matched) != 1 {
		t.Fatalf("matched projections before disable = %+v", matched)
	}
	rule.Enabled = false
	if _, err := ontologyRegistry.SaveMappingRuleWithExpectedVersion(ctx, rule, 1); err != nil {
		t.Fatalf("SaveMappingRuleWithExpectedVersion: %v", err)
	}
	if err := SyncMappingRuleProjections(ctx, ontologyRegistry, projectionRegistry); err != nil {
		t.Fatalf("SyncMappingRuleProjections after disable: %v", err)
	}
	if matched := projectionRegistry.ListBySourceType("project.created"); len(matched) != 0 {
		t.Fatalf("disabled rule projection should be removed, got %+v", matched)
	}
}

type mappingProjectionRevisionStub struct {
	revision int64
	err      error
}

func (s *mappingProjectionRevisionStub) MappingProjectionRevision(context.Context) (int64, error) {
	return s.revision, s.err
}

type mappingProjectionLockRevisionStub struct {
	revision     int64
	lock         *sync.Mutex
	requireLock  atomic.Bool
	readUnlocked atomic.Bool
}

func (s *mappingProjectionLockRevisionStub) MappingProjectionRevision(context.Context) (int64, error) {
	if s.requireLock.Load() && s.lock.TryLock() {
		s.readUnlocked.Store(true)
		s.lock.Unlock()
	}
	return s.revision, nil
}

func TestMappingRuleProjectionRunnerRefreshesWhenDurableRevisionChanges(t *testing.T) {
	ctx := context.Background()
	ontologyRegistry := NewMemoryRegistry()
	if err := ontologyRegistry.SaveObjectType(ctx, ObjectType{
		ID: "project", TenantScope: TenantScopeTenant, DisplayName: "Project", CanonicalKeyRule: "project:{id}",
	}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	rule := MappingRule{
		ID: "project.created", TenantID: "tenant-1", EventType: "project.created", ObjectTypes: []string{"project"}, Enabled: true,
		Rule: map[string]any{"objects": []any{map[string]any{"type": "project", "id": "$.project_id", "canonical_key": "project:{project_id}"}}},
	}
	if err := ontologyRegistry.SaveMappingRule(ctx, rule); err != nil {
		t.Fatalf("SaveMappingRule: %v", err)
	}
	gate := graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(ontologyRegistry))
	projectionRegistry := graphprotocol.NewProjectionRegistry(gate)
	revisions := &mappingProjectionRevisionStub{}
	runner := NewMappingRuleProjectionRunner(projectionRegistry, gate, contextgraph.NewMemoryStore())
	if err := runner.EnableRevisionRefresh(ctx, ontologyRegistry, revisions); err != nil {
		t.Fatalf("EnableRevisionRefresh: %v", err)
	}
	if matched := projectionRegistry.ListBySourceType("project.created"); len(matched) != 1 {
		t.Fatalf("initial projections = %+v", matched)
	}

	rule.Enabled = false
	if _, err := ontologyRegistry.SaveMappingRuleWithExpectedVersion(ctx, rule, 1); err != nil {
		t.Fatalf("disable mapping rule: %v", err)
	}
	revisions.revision++
	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID: "project-created:1", TenantID: "tenant-1", Type: "project.created", Version: 1,
		OccurredAt: time.Unix(40, 0).UTC(), PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "project"},
		Payload: map[string]any{"project_id": "project-1"},
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if report.MatchedProjectionCount != 0 || len(projectionRegistry.ListBySourceType("project.created")) != 0 {
		t.Fatalf("stale projection remained active: report=%+v", report)
	}
}

func TestMappingRuleProjectionRunnerReadsRevisionInsideRefreshLock(t *testing.T) {
	ctx := context.Background()
	registry := NewMemoryRegistry()
	if err := registry.SaveObjectType(ctx, ObjectType{ID: "project", TenantScope: TenantScopeTenant, DisplayName: "Project", CanonicalKeyRule: "project:{id}"}); err != nil {
		t.Fatalf("SaveObjectType: %v", err)
	}
	rule := MappingRule{
		ID: "project.created", TenantID: "tenant-1", EventType: "project.created", ObjectTypes: []string{"project"}, Enabled: true,
		Rule: map[string]any{"objects": []any{map[string]any{"type": "project", "id": "$.project_id", "canonical_key": "project:{project_id}"}}},
	}
	if err := registry.SaveMappingRule(ctx, rule); err != nil {
		t.Fatalf("SaveMappingRule: %v", err)
	}
	gate := graphprotocol.NewConformanceGate(graphprotocol.WithOntologyRegistry(registry))
	projections := graphprotocol.NewProjectionRegistry(gate)
	runner := NewMappingRuleProjectionRunner(projections, gate, contextgraph.NewMemoryStore())
	revisions := &mappingProjectionLockRevisionStub{revision: 1, lock: &runner.refreshMu}
	if err := runner.EnableRevisionRefresh(ctx, registry, revisions); err != nil {
		t.Fatalf("EnableRevisionRefresh: %v", err)
	}
	rule.Enabled = false
	if _, err := registry.SaveMappingRuleWithExpectedVersion(ctx, rule, 1); err != nil {
		t.Fatalf("disable mapping rule: %v", err)
	}
	revisions.revision = 2
	revisions.requireLock.Store(true)
	report, err := runner.ApplyEvent(ctx, graphprotocol.DomainEvent{
		ID: "project-created:locked", TenantID: "tenant-1", Type: "project.created", Version: 1,
		OccurredAt: time.Unix(50, 0).UTC(), PublishedBy: graphprotocol.ActorRef{Kind: "service", ID: "project"},
		Payload: map[string]any{"project_id": "project-locked"},
	})
	if err != nil {
		t.Fatalf("ApplyEvent: %v", err)
	}
	if revisions.readUnlocked.Load() || report.MatchedProjectionCount != 0 {
		t.Fatalf("revision read escaped refresh lock=%t report=%+v", revisions.readUnlocked.Load(), report)
	}
}

func runnerTestProvenance() graphprotocol.ProvenanceRef {
	return graphprotocol.ProvenanceRef{
		SourceEventID:   "event-1",
		SourceEventType: "mission.task.created",
		SourceSystem:    "test",
		Actor:           graphprotocol.ActorRef{Kind: "service", ID: "mission"},
		ObservedAt:      time.Unix(30, 0).UTC(),
	}
}
