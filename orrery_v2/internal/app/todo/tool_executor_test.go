package todo

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/idgen"
	"orrery_v2/internal/platform/testkit"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestToolExecutorCreatesListsMutatesAndDeletesTodos(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := NewService(ServiceOptions{
		Clock:       clock.NewFake(todoTestTime),
		IDGenerator: idgen.NewDeterministic("todo"),
		Repository:  repo,
	})
	executor := NewToolExecutor(ToolExecutorOptions{Service: service})
	ctx := context.Background()
	viewer := ViewerRef{ViewerID: "agent-ops", ViewerKind: ViewerKindAgent, DisplayName: "Agent Ops"}

	created, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID: string(toolstd.TodoCreate),
		Viewer: viewer,
		Arguments: map[string]any{
			"title":       " Ship homepage ",
			"description": " Real persisted todo ",
			"tags":        []string{" frontend ", " AI "},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	todoID := toolStringOutput(created.Output, "todo_id")
	if todoID != "todo-000001" || toolStringOutput(created.Output, "status") != string(TodoStatusInbox) {
		t.Fatalf("created output = %+v", created.Output)
	}
	if toolStringOutput(created.Output, "issue_number") != issueNumberFromID(todoID) {
		t.Fatalf("created output issue number = %+v", created.Output)
	}
	if toolStringOutput(created.Output, "proposer_name") != "Agent Ops" || !sameStrings(toolStringSliceOutput(created.Output, "tags"), []string{"frontend", "AI"}) {
		t.Fatalf("created output proposer/tags = %+v", created.Output)
	}

	claimed, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID: string(toolstd.TodoClaim),
		Viewer: viewer,
		Arguments: map[string]any{
			"todo_id":       todoID,
			"assignee_id":   "human-1",
			"assignee_name": "Human One",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolStringOutput(claimed.Output, "assignee_id") != "human-1" {
		t.Fatalf("claimed output = %+v", claimed.Output)
	}

	moved, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID: string(toolstd.TodoMove),
		Viewer: viewer,
		Arguments: map[string]any{
			"todo_id": todoID,
			"status":  string(TodoStatusInProgress),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolStringOutput(moved.Output, "status") != string(TodoStatusInProgress) {
		t.Fatalf("moved output = %+v", moved.Output)
	}

	completed, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID:    string(toolstd.TodoComplete),
		Viewer:    viewer,
		Arguments: map[string]any{"todo_id": todoID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolStringOutput(completed.Output, "status") != string(TodoStatusDone) {
		t.Fatalf("completed output = %+v", completed.Output)
	}

	reopenedDone, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID:    string(toolstd.TodoReopen),
		Viewer:    viewer,
		Arguments: map[string]any{"todo_id": todoID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolStringOutput(reopenedDone.Output, "status") != string(TodoStatusPlanned) {
		t.Fatalf("reopened done output = %+v", reopenedDone.Output)
	}

	canceled, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID:    string(toolstd.TodoCancel),
		Viewer:    viewer,
		Arguments: map[string]any{"todo_id": todoID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolStringOutput(canceled.Output, "status") != string(TodoStatusCanceled) {
		t.Fatalf("canceled output = %+v", canceled.Output)
	}

	reopened, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID:    string(toolstd.TodoReopen),
		Viewer:    viewer,
		Arguments: map[string]any{"todo_id": todoID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolStringOutput(reopened.Output, "status") != string(TodoStatusInbox) {
		t.Fatalf("reopened canceled output = %+v", reopened.Output)
	}

	updated, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID: string(toolstd.TodoUpdate),
		Viewer: viewer,
		Arguments: map[string]any{
			"todo_id":     todoID,
			"title":       "Ship full homepage",
			"description": "Updated through MCP",
			"tags":        []string{"release"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolStringOutput(updated.Output, "title") != "Ship full homepage" {
		t.Fatalf("updated output = %+v", updated.Output)
	}
	if toolStringOutput(updated.Output, "proposer_name") != "Agent Ops" || !sameStrings(toolStringSliceOutput(updated.Output, "tags"), []string{"release"}) {
		t.Fatalf("updated output proposer/tags = %+v", updated.Output)
	}

	listed, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID: string(toolstd.TodoList),
		Viewer: viewer,
		Arguments: map[string]any{
			"status":      string(TodoStatusInbox),
			"assignee_id": "human-1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolIntOutput(listed.Output, "total") != 1 {
		t.Fatalf("listed output = %+v", listed.Output)
	}

	deleted, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID:    string(toolstd.TodoDelete),
		Viewer:    viewer,
		Arguments: map[string]any{"todo_id": todoID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolStringOutput(deleted.Output, "todo_id") != todoID || deleted.Output["deleted"] != true {
		t.Fatalf("deleted output = %+v", deleted.Output)
	}
	if _, err := repo.GetTodo(ctx, todoID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get deleted error = %v, want ErrNotFound", err)
	}
}

func TestToolExecutorManagesIdeas(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	service := NewService(ServiceOptions{
		Clock:           clock.NewFake(todoTestTime),
		IDGenerator:     idgen.NewDeterministic("todo"),
		IdeaIDGenerator: idgen.NewDeterministic("idea"),
		Repository:      repo,
	})
	executor := NewToolExecutor(ToolExecutorOptions{Service: service})
	ctx := context.Background()
	viewer := ViewerRef{ViewerID: "agent-ops", ViewerKind: ViewerKindAgent, DisplayName: "Agent Ops"}

	created, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID: string(toolstd.IdeaCreate),
		Viewer: viewer,
		Arguments: map[string]any{
			"title":   " Capture planning insight ",
			"content": " Useful later ",
			"tags":    []string{" planning ", " inbox "},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ideaID := toolStringOutput(created.Output, "idea_id")
	if ideaID != "idea-000001" || toolStringOutput(created.Output, "status") != string(IdeaStatusSubmitted) {
		t.Fatalf("created idea output = %+v", created.Output)
	}
	if toolStringOutput(created.Output, "issue_number") != issueNumberFromID(ideaID) {
		t.Fatalf("created idea issue number output = %+v", created.Output)
	}
	if toolStringOutput(created.Output, "proposer_name") != "Agent Ops" || !sameStrings(toolStringSliceOutput(created.Output, "tags"), []string{"planning", "inbox"}) {
		t.Fatalf("created idea proposer/tags output = %+v", created.Output)
	}

	updated, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID: string(toolstd.IdeaUpdate),
		Viewer: viewer,
		Arguments: map[string]any{
			"idea_id": ideaID,
			"title":   "Planning insight",
			"tags":    []string{"experiment"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolStringOutput(updated.Output, "status") != string(IdeaStatusSubmitted) {
		t.Fatalf("updated idea output = %+v", updated.Output)
	}
	if toolStringOutput(updated.Output, "proposer_name") != "Agent Ops" || !sameStrings(toolStringSliceOutput(updated.Output, "tags"), []string{"experiment"}) {
		t.Fatalf("updated idea proposer/tags output = %+v", updated.Output)
	}

	inReview, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID: string(toolstd.IdeaTransition),
		Viewer: viewer,
		Arguments: map[string]any{
			"idea_id": ideaID,
			"status":  string(IdeaStatusUnderReview),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolStringOutput(inReview.Output, "status") != string(IdeaStatusUnderReview) {
		t.Fatalf("idea transition output = %+v", inReview.Output)
	}

	voted, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID:    string(toolstd.IdeaVote),
		Viewer:    viewer,
		Arguments: map[string]any{"idea_id": ideaID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolIntOutput(voted.Output, "vote_count") != 1 || voted.Output["viewer_has_voted"] != true {
		t.Fatalf("idea vote output = %+v", voted.Output)
	}

	archived, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID:    string(toolstd.IdeaArchive),
		Viewer:    viewer,
		Arguments: map[string]any{"idea_id": ideaID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolStringOutput(archived.Output, "status") != string(IdeaStatusArchived) {
		t.Fatalf("archived idea output = %+v", archived.Output)
	}

	restored, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID:    string(toolstd.IdeaRestore),
		Viewer:    viewer,
		Arguments: map[string]any{"idea_id": ideaID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolStringOutput(restored.Output, "status") != string(IdeaStatusSubmitted) {
		t.Fatalf("restored idea output = %+v", restored.Output)
	}

	if _, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID:    string(toolstd.IdeaTransition),
		Viewer:    viewer,
		Arguments: map[string]any{"idea_id": ideaID, "status": string(IdeaStatusConverted), "decision_reason": "Bypass conversion"},
	}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("direct converted transition error = %v, want ErrInvalidTransition", err)
	}
	if _, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID:    string(toolstd.IdeaConvertToTodo),
		Viewer:    viewer,
		Arguments: map[string]any{"idea_id": ideaID, "decision_reason": "Bypass governance"},
	}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("unapproved conversion error = %v, want ErrInvalidTransition", err)
	}

	accepted, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID: string(toolstd.IdeaTransition),
		Viewer: viewer,
		Arguments: map[string]any{
			"idea_id":         ideaID,
			"status":          string(IdeaStatusAccepted),
			"decision_reason": "Approved for execution",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolStringOutput(accepted.Output, "status") != string(IdeaStatusAccepted) {
		t.Fatalf("accepted idea output = %+v", accepted.Output)
	}

	converted, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID:    string(toolstd.IdeaConvertToTodo),
		Viewer:    viewer,
		Arguments: map[string]any{"idea_id": ideaID, "decision_reason": "Approved for execution"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if converted.Output["todo"] == nil || converted.Output["idea"] == nil {
		t.Fatalf("converted idea output = %+v", converted.Output)
	}
	convertedTodo := toolMapOutput(converted.Output, "todo")
	convertedIdea := toolMapOutput(converted.Output, "idea")
	convertedTodoID := toolStringOutput(convertedTodo, "todo_id")
	if convertedTodoID == ideaID || toolStringOutput(convertedIdea, "converted_todo_id") != convertedTodoID {
		t.Fatalf("converted idea identity output = %+v", converted.Output)
	}
	if _, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID:    string(toolstd.IdeaRestore),
		Viewer:    viewer,
		Arguments: map[string]any{"idea_id": ideaID},
	}); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("restore converted idea error = %v, want ErrInvalidTransition", err)
	}

	listed, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID:    string(toolstd.TodoList),
		Viewer:    viewer,
		Arguments: map[string]any{"status": string(TodoStatusPlanned)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolIntOutput(listed.Output, "total") != 1 {
		t.Fatalf("listed converted todos output = %+v", listed.Output)
	}

	if _, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID:    string(toolstd.TodoCancel),
		Viewer:    viewer,
		Arguments: map[string]any{"todo_id": convertedTodoID},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID:    string(toolstd.TodoReopen),
		Viewer:    viewer,
		Arguments: map[string]any{"todo_id": convertedTodoID},
	}); err != nil {
		t.Fatal(err)
	}
	deleted, err := executor.Execute(ctx, ToolExecutionRequest{
		ToolID:    string(toolstd.TodoDelete),
		Viewer:    viewer,
		Arguments: map[string]any{"todo_id": convertedTodoID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if toolStringOutput(deleted.Output, "todo_id") != convertedTodoID || deleted.Output["deleted"] != true {
		t.Fatalf("deleted converted todo output = %+v", deleted.Output)
	}
}

func TestToolExecutorRejectsUnsupportedTodoTool(t *testing.T) {
	executor := NewToolExecutor(ToolExecutorOptions{Service: &Service{}})
	_, err := executor.Execute(context.Background(), ToolExecutionRequest{
		ToolID:    "todo_unknown",
		Viewer:    ViewerRef{ViewerID: "agent-ops"},
		Arguments: map[string]any{},
	})
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
}

func toolStringOutput(output map[string]any, key string) string {
	value, _ := output[key].(string)
	return value
}

func toolIntOutput(output map[string]any, key string) int {
	switch value := output[key].(type) {
	case int:
		return value
	case float64:
		return int(value)
	default:
		return 0
	}
}

func toolStringSliceOutput(output map[string]any, key string) []string {
	raw, _ := output[key].([]string)
	if raw != nil {
		return raw
	}
	values, _ := output[key].([]any)
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, _ := value.(string)
		out = append(out, text)
	}
	return out
}

func toolMapOutput(output map[string]any, key string) map[string]any {
	raw, _ := output[key].(map[string]any)
	return raw
}
