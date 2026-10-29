package todo

import (
	"context"
	"testing"

	"orrery_v2/internal/platform/testkit"
)

func TestIdeaConversionPersistsResponsibleCreator(t *testing.T) {
	for _, kind := range []ViewerKind{ViewerKindUser, ViewerKindAgent} {
		t.Run(string(kind), func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			repo := NewRepository(env.DBRouter)
			service := newServiceForTest(repo)
			viewer := testViewer("tenant-a", "converter")
			viewer.ViewerKind = kind
			viewer.AuthorizationSubjectID = "responsible-subject"
			idea := mustCreateIdea(t, service, ctx, viewer, "Approved conversion")
			accepted, err := service.TransitionIdea(ctx, viewer, TransitionIdeaRequest{IdeaID: idea.IdeaID, Status: IdeaStatusAccepted, DecisionReason: "Approved by owner"})
			if err != nil {
				t.Fatal(err)
			}
			result, err := service.ConvertIdeaToTodo(ctx, viewer, ConvertIdeaToTodoRequest{IdeaID: idea.IdeaID, Version: &accepted.Version})
			if err != nil {
				t.Fatal(err)
			}
			stored, err := repo.GetTodoForTenant(ctx, viewer.TenantID, result.Todo.TodoID)
			if err != nil {
				t.Fatal(err)
			}
			for _, item := range []TodoItem{result.Todo, stored} {
				if item.CreatedBy != viewer.ViewerID || item.CreatedByKind != kind || item.CreatedBySubjectID != viewer.AuthorizationSubjectID {
					t.Fatalf("conversion lost responsible identity: %+v", item)
				}
			}
			calls := 0
			if err := service.DeliverCreationEvent(ctx, viewer.TenantID, stored.TodoID, func(_ context.Context, event CreationEvent) error {
				calls++
				if event.Source != "todo.idea_conversion" || event.CreatedBy != viewer.ViewerID || event.CreatedByKind != kind || event.CreatedBySubjectID != viewer.AuthorizationSubjectID || !event.CreatedAt.Equal(stored.CreatedAt) {
					t.Fatalf("conversion origin changed: %+v", event)
				}
				return nil
			}); err != nil || calls != 1 {
				t.Fatalf("conversion creation delivery: calls=%d err=%v", calls, err)
			}
		})
	}
}
