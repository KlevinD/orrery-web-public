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
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/migration"
	"orrery_v2/internal/platform/testkit"
)

func TestTodoCreationOutboxProductionRecovery(t *testing.T) {
	for _, stage := range []string{"before_append", "after_append", "before_ack"} {
		t.Run(stage, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			coordinator := migration.NewCoordinator(env.Databases)
			if err := ontology.RegisterMigrations(coordinator); err != nil {
				t.Fatal(err)
			}
			if err := contextgraph.RegisterMigrations(coordinator); err != nil {
				t.Fatal(err)
			}
			if err := coordinator.UpRole(ctx, db.RoleCore); err != nil {
				t.Fatal(err)
			}
			if err := ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(env.DB)); err != nil {
				t.Fatal(err)
			}
			runner, err := newProductionOntologyProjectionRunner(ctx, env.DB)
			if err != nil {
				t.Fatal(err)
			}
			api := &productionAPI{ontologyProject: runner, ontologyEvents: newProductionOntologyEventLog(env.DB)}
			if _, err := api.ontologyEvents.List(ctx); err != nil {
				t.Fatal(err)
			}
			fake := clock.NewFake(time.Date(2026, 10, 19, 1, 0, 0, 0, time.UTC))
			newService := func() *productionTodoOntologyService {
				inner := todoapp.NewService(todoapp.ServiceOptions{Repository: todoapp.NewRepository(env.DBRouter), Clock: fake})
				return newProductionTodoOntologyService(inner, api)
			}
			s := newService()
			viewer := todoapp.ViewerRef{TenantID: "tenant-a", ViewerID: "responsible-agent", ViewerKind: todoapp.ViewerKindAgent, AuthorizationSubjectID: "delegating-user"}
			triggers := map[string]string{
				"before_append": `CREATE TRIGGER fail_creation_delivery BEFORE INSERT ON platform_events BEGIN SELECT RAISE(ABORT, 'injected before append'); END`,
				"after_append":  `CREATE TRIGGER fail_creation_delivery BEFORE INSERT ON context_graph_facts WHEN NEW.predicate = 'created_at' BEGIN SELECT RAISE(ABORT, 'injected after append'); END`,
				"before_ack":    `CREATE TRIGGER fail_creation_delivery BEFORE DELETE ON platform_records WHEN OLD.table_name = 'todo_creation_outbox' BEGIN SELECT RAISE(ABORT, 'injected before ack'); END`,
			}
			if _, err := env.DB.Exec(ctx, triggers[stage]); err != nil {
				t.Fatal(err)
			}
			_, err = s.CreateTodo(ctx, viewer, todoapp.CreateTodoRequest{Title: "Created before failure"})
			if err == nil || !strings.Contains(err.Error(), "injected") {
				t.Fatalf("expected real %s failure, got %v", stage, err)
			}
			page, err := s.inner.ListTodos(ctx, viewer, todoapp.ListTodosQuery{})
			if err != nil || len(page.Todos) != 1 {
				t.Fatalf("durable business task=%+v %v", page, err)
			}
			item := page.Todos[0]
			events, err := api.ontologyEvents.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if stage == "before_append" && len(events) != 0 {
				t.Fatalf("expected interruption before any log: %+v", events)
			}
			if _, err := env.DB.Exec(ctx, `DROP TRIGGER fail_creation_delivery`); err != nil {
				t.Fatal(err)
			}
			current := item
			current.Title, current.Status, current.AssigneeID = "Latest task", todoapp.TodoStatusPlanned, "new-owner"
			current.UpdatedAt, current.Version = fake.Now().Add(time.Minute), item.Version+1
			if err := todoapp.NewRepository(env.DBRouter).UpdateTodo(ctx, current, item.Version); err != nil {
				t.Fatal(err)
			}
			if err := s.publishTodoChanged(ctx, viewer, current, string(current.Status), "todo.update", current.UpdatedAt); err != nil {
				t.Fatal(err)
			}
			fake.Advance(2 * time.Minute)
			runner, err = newProductionOntologyProjectionRunner(ctx, env.DB)
			if err != nil {
				t.Fatal(err)
			}
			api.ontologyProject = runner
			s = newService()
			rt := &platform.Runtime{Config: env.Config, Sweeper: execution.NewSweeperRunner(100)}
			if err := registerProductionTodoCreationSweeper(rt, s); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 3; i++ {
				report := rt.Sweeper.SweepOnce(ctx)
				if report.Total != 1 || report.Failed != 0 || report.Results[0].Name != "todo.creation_events" {
					t.Fatalf("production recovery=%+v", report)
				}
			}
			graph := contextgraph.NewPlatformStore(env.DB)
			snapshot := catalogCoverageSnapshot(t, ctx, graph, viewer.TenantID)
			assertContextGraphFact(t, snapshot.Facts, "todo", "todo:"+item.TodoID, "creation_actor_id", viewer.ViewerID)
			assertContextGraphFact(t, snapshot.Facts, "todo", "todo:"+item.TodoID, "created_at", item.CreatedAt.Format(time.RFC3339Nano))
			assertContextGraphFact(t, snapshot.Facts, "todo", "todo:"+item.TodoID, "title", current.Title)
			assertContextGraphFact(t, snapshot.Facts, "todo", "todo:"+item.TodoID, "status", string(current.Status))
			assertContextGraphFact(t, snapshot.Facts, "todo", "todo:"+item.TodoID, "assignee_id", current.AssigneeID)
			events, err = api.ontologyEvents.List(ctx)
			if err != nil {
				t.Fatal(err)
			}
			creations := 0
			for _, event := range events {
				if event.Type == "todo.created" {
					creations++
					if event.PublishedBy.ID != viewer.ViewerID || event.PublishedBy.Kind != string(viewer.ViewerKind) || event.TenantID != viewer.TenantID || !event.OccurredAt.Equal(item.CreatedAt) || event.SourceAccessRef.TenantID != viewer.TenantID {
						t.Fatalf("origin changed during recovery: %+v", event)
					}
				}
			}
			if creations != 1 {
				t.Fatalf("creation duplicates=%d", creations)
			}
			var pending int
			if err := env.DB.QueryRow(ctx, `SELECT COUNT(*) FROM platform_records WHERE table_name = 'todo_creation_outbox'`).Scan(&pending); err != nil || pending != 0 {
				t.Fatalf("pending receipts=%d %v", pending, err)
			}
			before := semanticCatalogCoverageSnapshot(snapshot)
			if err := s.inner.ReconcileCreationEvents(ctx, 100, s.publishTodoCreationEvent); err != nil {
				t.Fatal(err)
			}
			after := semanticCatalogCoverageSnapshot(catalogCoverageSnapshot(t, ctx, graph, viewer.TenantID))
			if !reflect.DeepEqual(before, after) {
				t.Fatal("acknowledged event was republished")
			}
		})
	}
}
