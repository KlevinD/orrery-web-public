package todo

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"orrery_v2/internal/platform/testkit"
)

func TestRepositorySavesLoadsListsAndDeletesTodos(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	first := todoRepositoryTestItem("todo-1", "First")
	second := todoRepositoryTestItem("todo-2", "Second")

	if err := repo.SaveTodo(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveTodo(ctx, second); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.GetTodo(ctx, first.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := repo.QueryTodos(ctx, ViewerRef{TenantID: defaultTenantID, ViewerID: "reader"}, ListTodosQuery{IncludeCompleted: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteTodo(ctx, first.TodoID); err != nil {
		t.Fatal(err)
	}
	_, err = repo.GetTodo(ctx, first.TodoID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted get error = %v, want ErrNotFound", err)
	}

	if loaded.TodoID != first.TodoID || loaded.Title != first.Title {
		t.Fatalf("loaded todo = %+v", loaded)
	}
	if len(listed.Todos) != 2 {
		t.Fatalf("listed todos = %+v", listed)
	}
}

func TestRepositoryAuthorizationFilterPreservesCaseAndAvoidsBindVariableLimits(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	for _, item := range []TodoItem{
		todoRepositoryTestItem("Todo-A", "Uppercase ID"),
		todoRepositoryTestItem("todo-a", "Lowercase ID"),
		todoRepositoryTestItem("todo-private", "Private"),
	} {
		if err := repo.SaveTodo(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	authorizedIDs := make([]string, 0, 1204)
	for index := 0; index < 1200; index++ {
		authorizedIDs = append(authorizedIDs, fmt.Sprintf("missing-%04d", index))
	}
	authorizedIDs = append(authorizedIDs, "Todo-A", "todo-a", "Todo-A", " ")

	page, err := repo.QueryTodos(ctx, ViewerRef{TenantID: defaultTenantID, ViewerID: "reader"}, ListTodosQuery{
		IncludeCompleted:      true,
		Limit:                 10,
		authorizationFiltered: true,
		authorizedIDs:         authorizedIDs,
	})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Todos) != 2 {
		t.Fatalf("page = %+v", page)
	}
	got := map[string]bool{}
	for _, item := range page.Todos {
		got[item.TodoID] = true
	}
	if !got["Todo-A"] || !got["todo-a"] || got["todo-private"] {
		t.Fatalf("todo ids = %+v", got)
	}
}

func TestRepositorySavesLoadsListsAndDeletesIdeas(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	first := ideaRepositoryTestItem("idea-1", "First")
	second := ideaRepositoryTestItem("idea-2", "Second")

	if err := repo.SaveIdea(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveIdea(ctx, second); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.GetIdea(ctx, first.IdeaID)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := repo.QueryIdeas(ctx, ViewerRef{TenantID: defaultTenantID, ViewerID: "reader"}, ListIdeasQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.DeleteIdea(ctx, first.IdeaID); err != nil {
		t.Fatal(err)
	}
	_, err = repo.GetIdea(ctx, first.IdeaID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted get error = %v, want ErrNotFound", err)
	}

	if loaded.IdeaID != first.IdeaID || loaded.Title != first.Title {
		t.Fatalf("loaded idea = %+v", loaded)
	}
	if len(listed.Ideas) != 2 {
		t.Fatalf("listed ideas = %+v", listed)
	}
}

func TestRepositorySavesAndListsIssueCommentsByIssue(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	first := issueCommentRepositoryTestItem("comment-1", IssueKindTodo, "todo-1", "First reply")
	second := issueCommentRepositoryTestItem("comment-2", IssueKindTodo, "todo-1", "Second reply")
	other := issueCommentRepositoryTestItem("comment-3", IssueKindIdea, "idea-1", "Idea reply")

	if err := repo.SaveIssueComment(ctx, first); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveIssueComment(ctx, second); err != nil {
		t.Fatal(err)
	}
	if err := repo.SaveIssueComment(ctx, other); err != nil {
		t.Fatal(err)
	}

	listed, err := repo.ListIssueComments(ctx, IssueKindTodo, "todo-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(listed) != 2 || listed[0].CommentID != "comment-1" || listed[1].CommentID != "comment-2" {
		t.Fatalf("listed comments = %+v", listed)
	}
	if listed[0].Body != "First reply" || listed[0].AuthorName != "产品经理" {
		t.Fatalf("first comment = %+v", listed[0])
	}
}

func TestRepositoryReportsNotFoundForMissingTodo(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)

	_, err := repo.GetTodo(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing error = %v, want ErrNotFound", err)
	}
}

func todoRepositoryTestItem(id string, title string) TodoItem {
	return TodoItem{
		TodoID:    id,
		Title:     title,
		Status:    TodoStatusInbox,
		Priority:  TodoPriorityMedium,
		CreatedBy: "creator",
		CreatedAt: todoTestTime,
		UpdatedAt: todoTestTime,
	}
}

func ideaRepositoryTestItem(id string, title string) IdeaItem {
	return IdeaItem{
		IdeaID:     id,
		Title:      title,
		Content:    "Content for " + title,
		Status:     IdeaStatusSubmitted,
		Visibility: IdeaVisibilityOrganization,
		Impact:     IdeaImpactMedium,
		Effort:     IdeaEffortMedium,
		CreatedBy:  "creator",
		CreatedAt:  todoTestTime,
		UpdatedAt:  todoTestTime,
	}
}

func issueCommentRepositoryTestItem(id string, kind IssueKind, issueID string, body string) IssueComment {
	return IssueComment{
		CommentID:  id,
		IssueKind:  kind,
		IssueID:    issueID,
		Body:       body,
		AuthorID:   "user-1",
		AuthorName: "产品经理",
		CreatedAt:  todoTestTime,
		UpdatedAt:  todoTestTime,
	}
}
