package todo

import (
	"context"
	"testing"

	"orrery_v2/internal/platform/testkit"
)

func TestTodoNotificationWakeFollowsDurableMutationOnly(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	service := newServiceForTest(NewRepository(env.DBRouter))
	viewer := testViewer("tenant-a", "human-a")
	ctx := context.Background()
	wakes := 0
	service.SetNotificationWake(func() {
		wakes++
		page, err := service.repo.QueryTodos(ctx, viewer, ListTodosQuery{})
		if err != nil || page.Total != 1 {
			t.Fatalf("notification before commit: %+v %v", page, err)
		}
	})
	if _, err := service.CreateTodo(ctx, viewer, CreateTodoRequest{}); err == nil || wakes != 0 {
		t.Fatal("invalid request woke notifications")
	}
	item, err := service.CreateTodo(ctx, viewer, CreateTodoRequest{Title: "Human action", AssigneeID: viewer.ViewerID, AssigneeKind: AssigneeKindHuman})
	if err != nil {
		t.Fatal(err)
	}
	if wakes != 1 {
		t.Fatalf("wakes=%d", wakes)
	}
	title := "Revised human action"
	item, err = service.UpdateTodo(ctx, viewer, UpdateTodoRequest{TodoID: item.TodoID, Title: &title, Version: &item.Version})
	if err != nil {
		t.Fatal(err)
	}
	if wakes != 2 {
		t.Fatalf("wakes=%d", wakes)
	}
	if _, err := service.GetTodo(ctx, viewer, item.TodoID); err != nil {
		t.Fatal(err)
	}
	if wakes != 2 {
		t.Fatal("read woke notifications")
	}
	service.SetNotificationWake(nil)
}
