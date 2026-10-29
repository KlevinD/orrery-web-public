package todo

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/db"
	"orrery_v2/internal/platform/testkit"
)

func TestTodoCreationOutboxSurvivesDatabaseReopenAndTaskDeletion(t *testing.T) {
	ctx := context.Background()
	urls := map[string]string{db.RoleCore: "sqlite://" + filepath.ToSlash(filepath.Join(t.TempDir(), "core.db"))}
	open := func() *db.Registry {
		r, err := db.OpenRegistryFromURLs(ctx, urls)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := open()
	repo := NewRepository(db.NewRouter(r))
	s := newServiceForTest(repo)
	viewer := testViewer("tenant-a", "original-agent")
	viewer.ViewerKind, viewer.AuthorizationSubjectID = ViewerKindAgent, "responsible-user"
	item := mustCreateTask(t, s, ctx, viewer, CreateTodoRequest{Title: "Before interruption"})
	changed := item
	changed.Title, changed.Status, changed.CreatedBy = "Later task", TodoStatusPlanned, "later-editor"
	if err := repo.UpdateTodo(ctx, changed, item.Version); err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteTodoForTenant(ctx, item.TenantID, item.TodoID, item.Version); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r = open()
	defer r.Close()
	s = newServiceForTest(NewRepository(db.NewRouter(r)))
	var got []CreationEvent
	publish := func(_ context.Context, event CreationEvent) error { got = append(got, event); return nil }
	for i := 0; i < 3; i++ {
		if err := s.ReconcileCreationEvents(ctx, 10, publish); err != nil {
			t.Fatal(err)
		}
	}
	want := CreationEvent{TenantID: item.TenantID, TodoID: item.TodoID, CreatedBy: viewer.ViewerID,
		CreatedByKind: viewer.ViewerKind, CreatedBySubjectID: viewer.AuthorizationSubjectID,
		CreatedAt: item.CreatedAt, Source: "todo.create"}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Fatalf("recovered origin = %+v, want %+v", got, want)
	}
}

func TestTodoCreationOutboxInsertFailureRollsBackBusiness(t *testing.T) {
	for _, conversion := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "conversion"}[conversion], func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			repo := NewRepository(env.DBRouter)
			s := newServiceForTest(repo)
			viewer := testViewer("tenant-a", "creator")
			var accepted IdeaItem
			if conversion {
				idea := mustCreateIdea(t, s, ctx, viewer, "Convert atomically")
				var err error
				accepted, err = s.TransitionIdea(ctx, viewer, TransitionIdeaRequest{IdeaID: idea.IdeaID, Status: IdeaStatusAccepted, DecisionReason: "Approved"})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := env.DB.Exec(ctx, `CREATE TRIGGER reject_creation_outbox BEFORE INSERT ON platform_records WHEN NEW.table_name = 'todo_creation_outbox' BEGIN SELECT RAISE(ABORT, 'injected creation intent failure'); END`); err != nil {
				t.Fatal(err)
			}
			var err error
			if conversion {
				_, err = s.ConvertIdeaToTodo(ctx, viewer, ConvertIdeaToTodoRequest{IdeaID: accepted.IdeaID, Version: &accepted.Version})
			} else {
				_, err = s.CreateTodo(ctx, viewer, CreateTodoRequest{Title: "Atomic creation"})
			}
			if err == nil || !strings.Contains(err.Error(), "injected creation intent failure") {
				t.Fatalf("creation error = %v", err)
			}
			page, err := s.ListTodos(ctx, viewer, ListTodosQuery{})
			if err != nil || page.Total != 0 {
				t.Fatalf("partial task commit: %+v %v", page, err)
			}
			if conversion {
				stored, err := repo.GetIdeaForTenant(ctx, viewer.TenantID, accepted.IdeaID)
				if err != nil || stored.Status != IdeaStatusAccepted || stored.Version != accepted.Version || stored.ConvertedTodoID != "" {
					t.Fatalf("partial conversion: %+v %v", stored, err)
				}
			}
		})
	}
}

func TestTodoCreationOutboxRetryLeaseBoundAndTenantIsolation(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	s := newServiceForTest(repo)
	for _, tenant := range []string{"tenant-a", "tenant-b"} {
		item := TodoItem{TenantID: tenant, TodoID: "shared", CreatedBy: tenant + "-creator", CreatedAt: todoTestTime}
		if err := repo.CreateTodo(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	var got []CreationEvent
	failure := errors.New("publisher unavailable before event append")
	publish := func(_ context.Context, event CreationEvent) error {
		got = append(got, event)
		if event.TenantID == "tenant-a" {
			return failure
		}
		return nil
	}
	if err := s.DeliverCreationEvent(ctx, "tenant-c", "shared", publish); err != nil || len(got) != 0 {
		t.Fatalf("cross-tenant delivery = %+v %v", got, err)
	}
	if err := s.ReconcileCreationEvents(ctx, 1, publish); !errors.Is(err, failure) || len(got) != 1 {
		t.Fatalf("bounded failure = %+v %v", got, err)
	}
	if err := s.ReconcileCreationEvents(ctx, 1, publish); err != nil || len(got) != 2 || got[1].TenantID != "tenant-b" {
		t.Fatalf("failed row starved other tenant: %+v %v", got, err)
	}
	if err := s.ReconcileCreationEvents(ctx, 1, publish); err != nil || len(got) != 2 {
		t.Fatalf("lease ignored: %+v %v", got, err)
	}
	s.clock.(*clock.Fake).Advance(time.Minute)
	if err := s.ReconcileCreationEvents(ctx, 1, func(_ context.Context, event CreationEvent) error { got = append(got, event); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[2] != got[0] || got[1].CreatedBy != "tenant-b-creator" {
		t.Fatalf("retry changed origin: %+v", got)
	}
	for _, limit := range []int{0, 101} {
		if err := s.ReconcileCreationEvents(ctx, limit, publish); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("unbounded scan %d: %v", limit, err)
		}
	}
}

func TestTodoCreationOutboxConcurrentDeliveryClaimsOnce(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	s := newServiceForTest(NewRepository(env.DBRouter))
	item := mustCreateTask(t, s, ctx, testViewer("tenant-a", "creator"), CreateTodoRequest{Title: "Concurrent delivery"})
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	var calls atomic.Int32
	publish := func(context.Context, CreationEvent) error { calls.Add(1); close(entered); <-release; return nil }
	go func() { done <- s.DeliverCreationEvent(ctx, item.TenantID, item.TodoID, publish) }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("publisher did not start")
	}
	err := s.DeliverCreationEvent(ctx, item.TenantID, item.TodoID, func(context.Context, CreationEvent) error { calls.Add(1); return nil })
	close(release)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("concurrent calls = %d", calls.Load())
	}
}

func TestTodoCreationOutboxDoesNotBackfillLegacyTasks(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	item := TodoItem{TenantID: "tenant-a", TodoID: "legacy", CreatedBy: "old-creator"}
	record, err := todoRecord(item)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.DBRouter.PutRecord(ctx, todoTable, scopedRecordKey(item.TenantID, item.TodoID), record); err != nil {
		t.Fatal(err)
	}
	s := newServiceForTest(NewRepository(env.DBRouter))
	if err := s.ReconcileCreationEvents(ctx, 10, func(context.Context, CreationEvent) error { t.Fatal("replayed historical task"); return nil }); err != nil {
		t.Fatal(err)
	}
}
