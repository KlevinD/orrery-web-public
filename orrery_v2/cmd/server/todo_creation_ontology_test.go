package main

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/ontology"
	todoapp "orrery_v2/internal/app/todo"
	"orrery_v2/internal/platform/graphprotocol"
)

func todoCreationFixture(tenantID string) todoapp.TodoItem {
	at := time.Date(2026, 10, 19, 1, 2, 3, 0, time.UTC)
	return todoapp.TodoItem{
		TenantID: tenantID, TodoID: "task-shared", Title: "Initial task", Status: todoapp.TodoStatusInbox,
		CreatedBy: "creator-" + tenantID, CreatedByKind: todoapp.ViewerKindUser,
		CreatedAt: at, UpdatedAt: at, Version: 1,
	}
}

func TestTodoCreationOntologyReplayPreservesCurrentTask(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "todo-created-replay")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	api := &productionAPI{ontologyProject: runner, ontologyEvents: newProductionOntologyEventLog(core)}
	service := newProductionTodoOntologyService(&todoapp.Service{}, api)
	item := todoCreationFixture("tenant-a")
	viewer := todoapp.ViewerRef{TenantID: item.TenantID, ViewerID: item.CreatedBy, ViewerKind: item.CreatedByKind}
	if err := service.publishTodoChanged(ctx, viewer, item, string(item.Status), "todo.create", item.CreatedAt); err != nil {
		t.Fatal(err)
	}
	if err := service.publishTodoCreated(ctx, item, "todo.create"); err != nil {
		t.Fatal(err)
	}
	events, err := api.ontologyEvents.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var creation graphprotocol.DomainEvent
	for _, event := range events {
		if event.Type == "todo.created" {
			creation = event
		}
	}
	if creation.ID == "" || creation.StreamID != item.TodoID || !creation.OccurredAt.Equal(item.CreatedAt) || creation.PublishedBy.ID != item.CreatedBy {
		t.Fatalf("creation identity = %+v", creation)
	}
	current := item
	current.Title, current.Status, current.AssigneeID = "Current task", todoapp.TodoStatusPlanned, "new-assignee"
	current.Version, current.UpdatedAt = 2, item.CreatedAt.Add(time.Hour)
	if err := service.publishTodoChanged(ctx, viewer, current, string(current.Status), "todo.update", current.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	graph := contextgraph.NewPlatformStore(core)
	before := catalogCoverageSnapshot(t, ctx, graph, item.TenantID)
	assertContextGraphFact(t, before.Facts, "todo", "todo:"+item.TodoID, "created_at", item.CreatedAt.Format(time.RFC3339Nano))
	assertContextGraphFact(t, before.Facts, "todo", "todo:"+item.TodoID, "creation_actor_id", item.CreatedBy)
	assertContextGraphFact(t, before.Facts, "todo", "todo:"+item.TodoID, "title", current.Title)
	assertContextGraphFact(t, before.Facts, "todo", "todo:"+item.TodoID, "status", string(current.Status))
	assertContextGraphFact(t, before.Facts, "todo", "todo:"+item.TodoID, "assignee_id", current.AssigneeID)
	for i := 0; i < 3; i++ {
		if err := api.applyOntologyEvent(ctx, creation); err != nil {
			t.Fatal(err)
		}
	}
	after := catalogCoverageSnapshot(t, ctx, graph, item.TenantID)
	if !reflect.DeepEqual(semanticCatalogCoverageSnapshot(before), semanticCatalogCoverageSnapshot(after)) {
		t.Fatalf("late todo.created replay changed current task or origin identity: before=%+v after=%+v", before, after)
	}
	events, err = api.ontologyEvents.List(ctx)
	if err != nil || len(events) != 3 {
		t.Fatalf("repeated publication accumulated events: %d err=%v", len(events), err)
	}
}

func TestTodoCreationOntologyFailureRemainsReplayable(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "todo-created-failure")
	log := newProductionOntologyEventLog(core)
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Exec(ctx, `CREATE TRIGGER fail_todo_creation_fact BEFORE INSERT ON context_graph_facts BEGIN SELECT RAISE(ABORT, 'injected creation fact failure'); END`); err != nil {
		t.Fatal(err)
	}
	api := &productionAPI{
		ontologyEvents:  log,
		ontologyProject: runner,
	}
	service := newProductionTodoOntologyService(&todoapp.Service{}, api)
	item := todoCreationFixture("tenant-a")
	if err := service.publishTodoCreated(ctx, item, "todo.create"); err == nil || !strings.Contains(err.Error(), "injected creation fact failure") {
		t.Fatalf("expected graph storage failure, got %v", err)
	}
	events, err := log.List(ctx)
	if err != nil || len(events) != 1 || events[0].Type != "todo.created" {
		t.Fatalf("durable creation event=%+v err=%v", events, err)
	}
	graph := contextgraph.NewPlatformStore(core)
	before := catalogCoverageSnapshot(t, ctx, graph, item.TenantID)
	if len(before.Facts) != 0 || len(before.Evidence) != 0 {
		t.Fatalf("failed projection mutated graph: %+v", before)
	}
	if _, err := core.Exec(ctx, `DROP TRIGGER fail_todo_creation_fact`); err != nil {
		t.Fatal(err)
	}
	runner, err = newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := runner.ApplyEvent(ctx, events[0]); err != nil {
		t.Fatalf("recover logged creation: %v", err)
	}
	after := catalogCoverageSnapshot(t, ctx, graph, item.TenantID)
	assertContextGraphFact(t, after.Facts, "todo", "todo:"+item.TodoID, "creation_source", "todo.create")
	assertContextGraphEvidence(t, after.Evidence, events[0].ID)
}

func TestTodoCreationOntologyRejectsMissingOrigin(t *testing.T) {
	for _, field := range []string{"tenant", "id", "actor", "actor_kind", "time", "source"} {
		t.Run(field, func(t *testing.T) {
			ctx := context.Background()
			core := newOntologyEventLogTestCore(t, "todo-created-invalid")
			runner, err := newProductionOntologyProjectionRunner(ctx, core)
			if err != nil {
				t.Fatal(err)
			}
			api := &productionAPI{ontologyEvents: newProductionOntologyEventLog(core), ontologyProject: runner}
			service := newProductionTodoOntologyService(&todoapp.Service{}, api)
			item, source := todoCreationFixture("tenant-a"), "todo.create"
			switch field {
			case "tenant":
				item.TenantID = ""
			case "id":
				item.TodoID = ""
			case "actor":
				item.CreatedBy = ""
			case "actor_kind":
				item.CreatedByKind = ""
			case "time":
				item.CreatedAt = time.Time{}
			case "source":
				source = ""
			}
			if err := service.publishTodoCreated(ctx, item, source); err == nil {
				t.Fatal("invalid origin accepted")
			}
			events, err := api.ontologyEvents.List(ctx)
			if err != nil || len(events) != 0 {
				t.Fatalf("invalid event persisted: %+v err=%v", events, err)
			}
		})
	}
}

func TestTodoCreationOntologyTenantIsolation(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "todo-created-tenants")
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	api := &productionAPI{ontologyEvents: newProductionOntologyEventLog(core), ontologyProject: runner}
	service := newProductionTodoOntologyService(&todoapp.Service{}, api)
	for _, tenantID := range []string{"tenant-a", "tenant-b"} {
		item := todoCreationFixture(tenantID)
		if err := service.publishTodoCreated(ctx, item, "todo.create"); err != nil {
			t.Fatal(err)
		}
		snapshot := catalogCoverageSnapshot(t, ctx, contextgraph.NewPlatformStore(core), tenantID)
		assertContextGraphFact(t, snapshot.Facts, "todo", "todo:"+item.TodoID, "creation_actor_id", item.CreatedBy)
		if len(snapshot.Facts) != 3 || len(snapshot.Evidence) != 1 {
			t.Fatalf("unexpected tenant snapshot: %+v", snapshot)
		}
		for _, fact := range snapshot.Facts {
			if fact.TenantID != tenantID || fact.AccessState != contextgraph.AccessStateResolved || fact.SourceAccessRef.Kind != "todo" || fact.SourceAccessRef.ID != item.TodoID || fact.SourceAccessRef.TenantID != tenantID {
				t.Fatalf("tenant source ACL=%+v", fact)
			}
		}
		for _, evidence := range snapshot.Evidence {
			if evidence.TenantID != tenantID || evidence.SourceAccessRef.TenantID != tenantID || evidence.SourceAccessRef.ID != item.TodoID {
				t.Fatalf("evidence ACL=%+v", evidence)
			}
		}
	}
	events, err := api.ontologyEvents.List(ctx)
	if err != nil || len(events) != 2 || events[0].ID == events[1].ID {
		t.Fatalf("cross-tenant event collision=%+v err=%v", events, err)
	}
}

func TestTodoCreationOntologyUpgradesPersistentDefinition(t *testing.T) {
	ctx := context.Background()
	core := newOntologyEventLogTestCore(t, "todo-created-upgrade")
	if _, err := newProductionOntologyProjectionRunner(ctx, core); err != nil {
		t.Fatal(err)
	}
	registry := ontology.NewPlatformRegistry(core)
	legacy, err := registry.GetObjectType(ctx, "todo")
	if err != nil {
		t.Fatal(err)
	}
	var oldFields []ontology.PropertyDefinition
	for _, field := range legacy.Properties {
		switch field.PropertyID {
		case "created_at", "creation_actor_id", "creation_source":
		default:
			oldFields = append(oldFields, field)
		}
	}
	legacy.Properties = oldFields
	legacy.AllowedActions = []string{"operator.review"}
	before, err := registry.SaveObjectTypeWithExpectedVersion(ctx, legacy, legacy.Version)
	if err != nil {
		t.Fatal(err)
	}
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatal(err)
	}
	runner, err := newProductionOntologyProjectionRunner(ctx, core)
	if err != nil {
		t.Fatal(err)
	}
	after, err := registry.GetObjectType(ctx, "todo")
	if err != nil || len(after.Properties) != len(before.Properties)+3 || !reflect.DeepEqual(after.AllowedActions, before.AllowedActions) {
		t.Fatalf("persistent Todo upgrade=%+v before=%+v err=%v", after, before, err)
	}
	api := &productionAPI{ontologyEvents: newProductionOntologyEventLog(core), ontologyProject: runner}
	service := newProductionTodoOntologyService(&todoapp.Service{}, api)
	item := todoCreationFixture("tenant-upgrade")
	if err := service.publishTodoCreated(ctx, item, "todo.create"); err != nil {
		t.Fatal(err)
	}
	snapshot := catalogCoverageSnapshot(t, ctx, contextgraph.NewPlatformStore(core), item.TenantID)
	assertContextGraphFact(t, snapshot.Facts, "todo", "todo:"+item.TodoID, "creation_actor_id", item.CreatedBy)
	if err := ontology.InstallDefaultOntologyDefinitions(ctx, registry); err != nil {
		t.Fatal(err)
	}
	if _, err := newProductionOntologyProjectionRunner(ctx, core); err != nil {
		t.Fatal(err)
	}
	repeated, err := registry.GetObjectType(ctx, "todo")
	if err != nil || !reflect.DeepEqual(after, repeated) {
		t.Fatalf("repeated persistent startup changed Todo schema: before=%+v after=%+v err=%v", after, repeated, err)
	}
}
