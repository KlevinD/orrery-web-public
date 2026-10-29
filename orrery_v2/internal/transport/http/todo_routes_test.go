package httptransport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/todo"
)

var todoRouteTestTime = time.Date(2026, 6, 15, 10, 30, 0, 0, time.UTC)

func TestTodoRoutesCreateListUpdateClaimCompleteReopenAndDelete(t *testing.T) {
	item := todoRouteItem("todo-1")
	service := &fakeTodoService{
		item: item,
		page: todo.TodoPage{
			Todos:  []todo.TodoItem{item},
			Total:  1,
			Limit:  10,
			Offset: 1,
		},
	}
	handler := newTestRouter(RouterOptions{Todo: service})

	create := authedRequest(http.MethodPost, "/api/todos", `{"title":"Ship homepage","description":"Finish todo list","tags":["frontend","ai"]}`)
	create.Header.Set("X-User-Display-Name", "Operator%20One")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.createReq.Title != "Ship homepage" || service.createReq.Description != "Finish todo list" || !sameStrings(service.createReq.Tags, []string{"frontend", "ai"}) {
		t.Fatalf("create request = %+v", service.createReq)
	}
	if service.createViewer.ViewerID != "agent-1" || service.createViewer.ViewerKind != todo.ViewerKindAgent || service.createViewer.DisplayName != "Operator One" {
		t.Fatalf("create viewer = %+v", service.createViewer)
	}
	var created todo.TodoItem
	if err := json.NewDecoder(rec.Body).Decode(&created); err != nil {
		t.Fatalf("decode created todo: %v", err)
	}
	if created.TodoID != item.TodoID {
		t.Fatalf("created todo = %+v", created)
	}

	suggest := authedRequest(http.MethodPost, "/api/todo-drafts/suggest", `{"kind":"todo","content":"Use persisted commands for board polish","current_tags":["frontend"]}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, suggest)
	if rec.Code != http.StatusOK {
		t.Fatalf("suggest status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.suggestReq.Kind != "todo" || service.suggestReq.Content != "Use persisted commands for board polish" || !sameStrings(service.suggestReq.CurrentTags, []string{"frontend"}) {
		t.Fatalf("suggest request = %+v", service.suggestReq)
	}
	var suggestion todo.DraftSuggestion
	if err := json.NewDecoder(rec.Body).Decode(&suggestion); err != nil {
		t.Fatalf("decode suggestion: %v", err)
	}
	if suggestion.Title != "Use persisted commands" || !sameStrings(suggestion.Tags, []string{"frontend", "polish"}) {
		t.Fatalf("suggestion = %+v", suggestion)
	}

	list := authedRequest(http.MethodGet, "/api/todos?status=inbox&assignee_id=agent-1&q=ship&limit=10&offset=1", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, list)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.listQuery.Status != todo.TodoStatusInbox || service.listQuery.AssigneeID != "agent-1" || service.listQuery.Q != "ship" || service.listQuery.Limit != 10 || service.listQuery.Offset != 1 {
		t.Fatalf("list query = %+v", service.listQuery)
	}
	var page todo.TodoPage
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode todo page: %v", err)
	}
	if page.Total != 1 || len(page.Todos) != 1 || page.Todos[0].TodoID != item.TodoID {
		t.Fatalf("todo page = %+v", page)
	}

	get := authedRequest(http.MethodGet, "/api/todos/todo-1", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, get)
	if rec.Code != http.StatusOK {
		t.Fatalf("get todo status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.getTodoID != "todo-1" {
		t.Fatalf("get todo id = %q", service.getTodoID)
	}
	var got todo.TodoItem
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode todo detail: %v", err)
	}
	if got.TodoID != item.TodoID {
		t.Fatalf("todo detail = %+v", got)
	}

	update := authedRequest(http.MethodPatch, "/api/todos/todo-1", `{"title":"Ship real homepage","description":"Done through API","tags":["release"]}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, update)
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.updateReq.TodoID != "todo-1" || service.updateReq.Title == nil || *service.updateReq.Title != "Ship real homepage" || service.updateReq.Description == nil || *service.updateReq.Description != "Done through API" {
		t.Fatalf("update request = %+v", service.updateReq)
	}
	if service.updateReq.Tags == nil || !sameStrings(*service.updateReq.Tags, []string{"release"}) {
		t.Fatalf("update tags request = %+v", service.updateReq)
	}

	claim := authedRequest(http.MethodPost, "/api/todos/todo-1/claim", `{"assignee_id":"human-1","assignee_name":"Human One"}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, claim)
	if rec.Code != http.StatusOK {
		t.Fatalf("claim status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.claimReq.TodoID != "todo-1" || service.claimReq.AssigneeID != "human-1" || service.claimReq.AssigneeName != "Human One" {
		t.Fatalf("claim request = %+v", service.claimReq)
	}

	move := authedRequest(http.MethodPost, "/api/todos/todo-1/move", `{"status":"in_progress"}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, move)
	if rec.Code != http.StatusOK {
		t.Fatalf("move status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.moveReq.TodoID != "todo-1" || service.moveReq.Status != todo.TodoStatusInProgress {
		t.Fatalf("move request = %+v", service.moveReq)
	}

	complete := authedRequest(http.MethodPost, "/api/todos/todo-1/complete", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, complete)
	if rec.Code != http.StatusOK {
		t.Fatalf("complete status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.completeReq.TodoID != "todo-1" {
		t.Fatalf("complete request = %+v", service.completeReq)
	}

	cancel := authedRequest(http.MethodPost, "/api/todos/todo-1/cancel", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, cancel)
	if rec.Code != http.StatusOK {
		t.Fatalf("cancel status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.cancelReq.TodoID != "todo-1" {
		t.Fatalf("cancel request = %+v", service.cancelReq)
	}

	reopen := authedRequest(http.MethodPost, "/api/todos/todo-1/reopen", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, reopen)
	if rec.Code != http.StatusOK {
		t.Fatalf("reopen status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.reopenReq.TodoID != "todo-1" {
		t.Fatalf("reopen request = %+v", service.reopenReq)
	}

	del := authedRequest(http.MethodDelete, "/api/todos/todo-1", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, del)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.deleteReq.TodoID != "todo-1" {
		t.Fatalf("delete request = %+v", service.deleteReq)
	}
}

func TestTodoCommentRoutesCreateAndListIssueComments(t *testing.T) {
	item := todoRouteItem("todo-1")
	comment := todo.IssueComment{
		CommentID:  "comment-1",
		IssueKind:  todo.IssueKindTodo,
		IssueID:    item.TodoID,
		Body:       "Looks good",
		AuthorID:   "agent-1",
		AuthorName: "Agent One",
		CreatedAt:  todoRouteTestTime,
		UpdatedAt:  todoRouteTestTime,
	}
	service := &fakeTodoService{
		item: item,
		commentPage: todo.IssueCommentPage{
			Comments: []todo.IssueComment{comment},
			Total:    1,
			Limit:    20,
			Offset:   0,
		},
		comment: comment,
	}
	handler := newTestRouter(RouterOptions{Todo: service})

	create := authedRequest(http.MethodPost, "/api/todos/todo-1/comments", `{"body":"Looks good"}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create comment status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.createCommentReq.IssueKind != todo.IssueKindTodo || service.createCommentReq.IssueID != "todo-1" || service.createCommentReq.Body != "Looks good" {
		t.Fatalf("create comment req = %+v", service.createCommentReq)
	}

	list := authedRequest(http.MethodGet, "/api/todos/todo-1/comments?limit=20&offset=0", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, list)
	if rec.Code != http.StatusOK {
		t.Fatalf("list comment status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.listCommentsQuery.IssueKind != todo.IssueKindTodo || service.listCommentsQuery.IssueID != "todo-1" || service.listCommentsQuery.Limit != 20 {
		t.Fatalf("list comments query = %+v", service.listCommentsQuery)
	}
	var page todo.IssueCommentPage
	if err := json.NewDecoder(rec.Body).Decode(&page); err != nil {
		t.Fatalf("decode comments: %v", err)
	}
	if page.Total != 1 || page.Comments[0].CommentID != "comment-1" {
		t.Fatalf("comment page = %+v", page)
	}
}

func TestTodoExecutionPlanAndCanonicalDiscussionRoutes(t *testing.T) {
	item := todoRouteItem("todo-1")
	item.DiscussionBinding = &todo.TodoDiscussionBinding{TodoID: item.TodoID, ConversationID: "discussion-todo-1"}
	plans := todo.TodoExecutionPlanPage{ExecutionPlans: []todo.TodoExecutionPlanView{{
		Plan:       todo.TodoExecutionPlan{PlanID: "plan-1", TodoID: item.TodoID, ProposalRevision: 1},
		PlanDigest: "digest-1", LifecycleStatus: todo.TodoAutomationStatusPendingApproval,
	}}}
	service := &fakeTodoService{item: item, executionPlans: plans}
	discussions := &fakeDiscussionService{conversation: discussionsession.ConversationDTO{
		ConversationID: "discussion-todo-1", PurposeKind: "todo", PurposeID: item.TodoID,
		Messages: []discussionsession.MessageDTO{}, Members: []discussionsession.ConversationMemberDTO{},
		Runs: []discussionsession.RunDTO{}, LinkedMissions: []discussionsession.LinkedMissionDTO{}, Attachments: []discussionsession.AttachmentDTO{},
	}}
	handler := newTestRouter(RouterOptions{Todo: service, Discussions: discussions})

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, authedRequest(http.MethodGet, "/api/todos/todo-1/execution-plans", ""))
	if recorder.Code != http.StatusOK {
		t.Fatalf("execution plans status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var page todo.TodoExecutionPlanPage
	if err := json.NewDecoder(recorder.Body).Decode(&page); err != nil || len(page.ExecutionPlans) != 1 || page.ExecutionPlans[0].Plan.PlanID != "plan-1" {
		t.Fatalf("execution plans=%+v err=%v", page, err)
	}

	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, authedRequest(http.MethodGet, "/api/todos/todo-1/discussion", ""))
	if recorder.Code != http.StatusOK || !discussions.getPageCalled || discussions.getConversationID != "discussion-todo-1" {
		t.Fatalf("discussion status=%d id=%q body=%s", recorder.Code, discussions.getConversationID, recorder.Body.String())
	}
	var discussionBody map[string]any
	if err := json.NewDecoder(recorder.Body).Decode(&discussionBody); err != nil || discussionBody["discussion_id"] != "discussion-todo-1" {
		t.Fatalf("discussion body=%+v err=%v", discussionBody, err)
	}
}

func TestIdeaRoutesCreateListUpdateArchiveRestoreConvertAndDelete(t *testing.T) {
	item := ideaRouteItem("idea-1")
	todoItem := todoRouteItem("todo-1")
	service := &fakeTodoService{
		idea: item,
		ideaPage: todo.IdeaPage{
			Ideas:  []todo.IdeaItem{item},
			Total:  1,
			Limit:  10,
			Offset: 2,
		},
		conversion: todo.IdeaConversionResult{Idea: item, Todo: todoItem},
	}
	handler := newTestRouter(RouterOptions{Todo: service})

	create := authedRequest(http.MethodPost, "/api/ideas", `{"title":"Capture idea","content":"Useful thought","tags":["planning","ai"]}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, create)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create idea status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.createIdeaReq.Title != "Capture idea" || service.createIdeaReq.Content != "Useful thought" || !sameStrings(service.createIdeaReq.Tags, []string{"planning", "ai"}) {
		t.Fatalf("create idea request = %+v", service.createIdeaReq)
	}

	list := authedRequest(http.MethodGet, "/api/ideas?status=submitted&q=idea&limit=10&offset=2", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, list)
	if rec.Code != http.StatusOK {
		t.Fatalf("list idea status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.listIdeasQuery.Status != todo.IdeaStatusSubmitted || service.listIdeasQuery.Q != "idea" || service.listIdeasQuery.Limit != 10 || service.listIdeasQuery.Offset != 2 {
		t.Fatalf("list idea query = %+v", service.listIdeasQuery)
	}

	get := authedRequest(http.MethodGet, "/api/ideas/idea-1", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, get)
	if rec.Code != http.StatusOK {
		t.Fatalf("get idea status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.getIdeaID != "idea-1" {
		t.Fatalf("get idea id = %q", service.getIdeaID)
	}
	var got todo.IdeaItem
	if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
		t.Fatalf("decode idea detail: %v", err)
	}
	if got.IdeaID != item.IdeaID {
		t.Fatalf("idea detail = %+v", got)
	}

	update := authedRequest(http.MethodPatch, "/api/ideas/idea-1", `{"title":"Refined idea","content":"More detail","impact":"high","tags":["experiment"]}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, update)
	if rec.Code != http.StatusOK {
		t.Fatalf("update idea status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.updateIdeaReq.IdeaID != "idea-1" || service.updateIdeaReq.Title == nil || *service.updateIdeaReq.Title != "Refined idea" || service.updateIdeaReq.Impact == nil || *service.updateIdeaReq.Impact != todo.IdeaImpactHigh {
		t.Fatalf("update idea request = %+v", service.updateIdeaReq)
	}
	if service.updateIdeaReq.Tags == nil || !sameStrings(*service.updateIdeaReq.Tags, []string{"experiment"}) {
		t.Fatalf("update idea tags request = %+v", service.updateIdeaReq)
	}

	transition := authedRequest(http.MethodPost, "/api/ideas/idea-1/transition", `{"status":"accepted","decision_reason":"Approved"}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, transition)
	if rec.Code != http.StatusOK || service.transitionIdeaReq.IdeaID != "idea-1" || service.transitionIdeaReq.Status != todo.IdeaStatusAccepted || service.transitionIdeaReq.DecisionReason != "Approved" {
		t.Fatalf("transition idea status = %d req=%+v body=%s", rec.Code, service.transitionIdeaReq, rec.Body.String())
	}

	vote := authedRequest(http.MethodPost, "/api/ideas/idea-1/vote", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, vote)
	if rec.Code != http.StatusOK || service.voteIdeaReq.IdeaID != "idea-1" {
		t.Fatalf("vote idea status = %d req=%+v body=%s", rec.Code, service.voteIdeaReq, rec.Body.String())
	}

	archive := authedRequest(http.MethodPost, "/api/ideas/idea-1/archive", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, archive)
	if rec.Code != http.StatusOK || service.archiveIdeaReq.IdeaID != "idea-1" {
		t.Fatalf("archive idea status = %d req=%+v body=%s", rec.Code, service.archiveIdeaReq, rec.Body.String())
	}

	restore := authedRequest(http.MethodPost, "/api/ideas/idea-1/restore", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, restore)
	if rec.Code != http.StatusOK || service.restoreIdeaReq.IdeaID != "idea-1" {
		t.Fatalf("restore idea status = %d req=%+v body=%s", rec.Code, service.restoreIdeaReq, rec.Body.String())
	}

	convert := authedRequest(http.MethodPost, "/api/ideas/idea-1/convert-to-todo", `{"todo_title":"Todo from idea"}`)
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, convert)
	if rec.Code != http.StatusOK || service.convertIdeaReq.IdeaID != "idea-1" || service.convertIdeaReq.TodoTitle != "Todo from idea" {
		t.Fatalf("convert idea status = %d req=%+v body=%s", rec.Code, service.convertIdeaReq, rec.Body.String())
	}

	del := authedRequest(http.MethodDelete, "/api/ideas/idea-1", "")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, del)
	if rec.Code != http.StatusNoContent || service.deleteIdeaReq.IdeaID != "idea-1" {
		t.Fatalf("delete idea status = %d req=%+v body=%s", rec.Code, service.deleteIdeaReq, rec.Body.String())
	}
}

func TestTodoRoutesMapServiceErrors(t *testing.T) {
	t.Run("invalid request", func(t *testing.T) {
		service := &fakeTodoService{err: todo.ErrInvalidRequest}
		handler := newTestRouter(RouterOptions{Todo: service})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, authedRequest(http.MethodPost, "/api/todos", `{"title":""}`))
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		var body ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode error: %v", err)
		}
		if body.Code != ErrorInvalidRequest {
			t.Fatalf("error response = %+v", body)
		}
	})

	t.Run("not found", func(t *testing.T) {
		service := &fakeTodoService{err: todo.ErrNotFound}
		handler := newTestRouter(RouterOptions{Todo: service})
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, authedRequest(http.MethodPatch, "/api/todos/missing", `{"title":"Missing"}`))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
		}
		var body ErrorResponse
		if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
			t.Fatalf("decode error: %v", err)
		}
		if body.Code != ErrorNotFound {
			t.Fatalf("error response = %+v", body)
		}
	})
}

func TestTodoRoutesRequireService(t *testing.T) {
	handler := newTestRouter(RouterOptions{})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/todos", ""))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Code != ErrorResourceUnavailable || !body.Retryable {
		t.Fatalf("error response = %+v", body)
	}
}

func TestTodoRoutesRejectInvalidJSONBody(t *testing.T) {
	handler := newTestRouter(RouterOptions{Todo: &fakeTodoService{item: todoRouteItem("todo-1")}})
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodPost, "/api/todos", `{"title":`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Code != ErrorInvalidRequest {
		t.Fatalf("error response = %+v", body)
	}
}

func todoRouteItem(id string) todo.TodoItem {
	return todo.TodoItem{
		TodoID:       id,
		Title:        "Ship homepage",
		Description:  "Finish todo list",
		Status:       todo.TodoStatusInbox,
		Priority:     todo.TodoPriorityMedium,
		AssigneeID:   "agent-1",
		AssigneeName: "Agent One",
		CreatedBy:    "agent-1",
		CreatedAt:    todoRouteTestTime,
		UpdatedAt:    todoRouteTestTime,
	}
}

func ideaRouteItem(id string) todo.IdeaItem {
	return todo.IdeaItem{
		IdeaID:     id,
		Title:      "Capture idea",
		Content:    "Useful thought",
		Status:     todo.IdeaStatusSubmitted,
		Visibility: todo.IdeaVisibilityOrganization,
		Impact:     todo.IdeaImpactMedium,
		Effort:     todo.IdeaEffortMedium,
		CreatedBy:  "agent-1",
		CreatedAt:  todoRouteTestTime,
		UpdatedAt:  todoRouteTestTime,
	}
}

type fakeTodoService struct {
	item todo.TodoItem
	page todo.TodoPage
	err  error

	createViewer      todo.ViewerRef
	getTodoID         string
	updateViewer      todo.ViewerRef
	deleteViewer      todo.ViewerRef
	claimViewer       todo.ViewerRef
	completeViewer    todo.ViewerRef
	moveViewer        todo.ViewerRef
	cancelViewer      todo.ViewerRef
	reopenViewer      todo.ViewerRef
	createIdeaViewer  todo.ViewerRef
	summaryViewer     todo.ViewerRef
	getIdeaID         string
	updateIdeaViewer  todo.ViewerRef
	deleteIdeaViewer  todo.ViewerRef
	archiveIdeaViewer todo.ViewerRef
	restoreIdeaViewer todo.ViewerRef
	convertIdeaViewer todo.ViewerRef

	createReq         todo.CreateTodoRequest
	updateReq         todo.UpdateTodoRequest
	deleteReq         todo.DeleteTodoRequest
	claimReq          todo.ClaimTodoRequest
	moveReq           todo.MoveTodoRequest
	completeReq       todo.CompleteTodoRequest
	cancelReq         todo.CancelTodoRequest
	reopenReq         todo.ReopenTodoRequest
	listQuery         todo.ListTodosQuery
	idea              todo.IdeaItem
	ideaPage          todo.IdeaPage
	conversion        todo.IdeaConversionResult
	createIdeaReq     todo.CreateIdeaRequest
	updateIdeaReq     todo.UpdateIdeaRequest
	deleteIdeaReq     todo.DeleteIdeaRequest
	archiveIdeaReq    todo.ArchiveIdeaRequest
	restoreIdeaReq    todo.RestoreIdeaRequest
	convertIdeaReq    todo.ConvertIdeaToTodoRequest
	transitionIdeaReq todo.TransitionIdeaRequest
	voteIdeaReq       todo.VoteIdeaRequest
	listIdeasQuery    todo.ListIdeasQuery
	suggestReq        todo.SuggestDraftRequest
	suggestion        todo.DraftSuggestion
	comment           todo.IssueComment
	commentPage       todo.IssueCommentPage
	executionPlans    todo.TodoExecutionPlanPage

	createCommentReq  todo.CreateIssueCommentRequest
	listCommentsQuery todo.ListIssueCommentsQuery
}

func (f *fakeTodoService) CreateTodo(_ context.Context, viewer todo.ViewerRef, req todo.CreateTodoRequest) (todo.TodoItem, error) {
	f.createViewer = viewer
	f.createReq = req
	if f.err != nil {
		return todo.TodoItem{}, f.err
	}
	return f.item, nil
}

func (f *fakeTodoService) GetTodo(_ context.Context, _ todo.ViewerRef, todoID string) (todo.TodoItem, error) {
	f.getTodoID = todoID
	if f.err != nil {
		return todo.TodoItem{}, f.err
	}
	return f.item, nil
}

func (f *fakeTodoService) GetTodoExecutionProgress(_ context.Context, _ todo.ViewerRef, _ string) (todo.TodoExecutionSummary, error) {
	if f.err != nil {
		return todo.TodoExecutionSummary{}, f.err
	}
	return todo.TodoExecutionSummary{}, nil
}

func (f *fakeTodoService) GetTodoExecutionPlans(_ context.Context, _ todo.ViewerRef, _ string) (todo.TodoExecutionPlanPage, error) {
	if f.err != nil {
		return todo.TodoExecutionPlanPage{}, f.err
	}
	return f.executionPlans, nil
}

func (f *fakeTodoService) UpdateTodo(_ context.Context, viewer todo.ViewerRef, req todo.UpdateTodoRequest) (todo.TodoItem, error) {
	f.updateViewer = viewer
	f.updateReq = req
	if f.err != nil {
		return todo.TodoItem{}, f.err
	}
	return f.item, nil
}

func (f *fakeTodoService) DeleteTodo(_ context.Context, viewer todo.ViewerRef, req todo.DeleteTodoRequest) error {
	f.deleteViewer = viewer
	f.deleteReq = req
	if f.err != nil {
		return f.err
	}
	return nil
}

func (f *fakeTodoService) ClaimTodo(_ context.Context, viewer todo.ViewerRef, req todo.ClaimTodoRequest) (todo.TodoItem, error) {
	f.claimViewer = viewer
	f.claimReq = req
	if f.err != nil {
		return todo.TodoItem{}, f.err
	}
	return f.item, nil
}

func (f *fakeTodoService) MoveTodo(_ context.Context, viewer todo.ViewerRef, req todo.MoveTodoRequest) (todo.TodoItem, error) {
	f.moveViewer = viewer
	f.moveReq = req
	if f.err != nil {
		return todo.TodoItem{}, f.err
	}
	return f.item, nil
}

func (f *fakeTodoService) CompleteTodo(_ context.Context, viewer todo.ViewerRef, req todo.CompleteTodoRequest) (todo.TodoItem, error) {
	f.completeViewer = viewer
	f.completeReq = req
	if f.err != nil {
		return todo.TodoItem{}, f.err
	}
	return f.item, nil
}

func (f *fakeTodoService) CancelTodo(_ context.Context, viewer todo.ViewerRef, req todo.CancelTodoRequest) (todo.TodoItem, error) {
	f.cancelViewer = viewer
	f.cancelReq = req
	if f.err != nil {
		return todo.TodoItem{}, f.err
	}
	return f.item, nil
}

func (f *fakeTodoService) ReopenTodo(_ context.Context, viewer todo.ViewerRef, req todo.ReopenTodoRequest) (todo.TodoItem, error) {
	f.reopenViewer = viewer
	f.reopenReq = req
	if f.err != nil {
		return todo.TodoItem{}, f.err
	}
	return f.item, nil
}

func (f *fakeTodoService) ListTodos(_ context.Context, _ todo.ViewerRef, query todo.ListTodosQuery) (todo.TodoPage, error) {
	f.listQuery = query
	if f.err != nil {
		return todo.TodoPage{}, f.err
	}
	return f.page, nil
}

func (f *fakeTodoService) CreateIdea(_ context.Context, viewer todo.ViewerRef, req todo.CreateIdeaRequest) (todo.IdeaItem, error) {
	f.createIdeaViewer = viewer
	f.createIdeaReq = req
	if f.err != nil {
		return todo.IdeaItem{}, f.err
	}
	return f.idea, nil
}

func (f *fakeTodoService) GetIdea(_ context.Context, _ todo.ViewerRef, ideaID string) (todo.IdeaItem, error) {
	f.getIdeaID = ideaID
	if f.err != nil {
		return todo.IdeaItem{}, f.err
	}
	return f.idea, nil
}

func (f *fakeTodoService) UpdateIdea(_ context.Context, viewer todo.ViewerRef, req todo.UpdateIdeaRequest) (todo.IdeaItem, error) {
	f.updateIdeaViewer = viewer
	f.updateIdeaReq = req
	if f.err != nil {
		return todo.IdeaItem{}, f.err
	}
	return f.idea, nil
}

func (f *fakeTodoService) TransitionIdea(_ context.Context, _ todo.ViewerRef, req todo.TransitionIdeaRequest) (todo.IdeaItem, error) {
	f.transitionIdeaReq = req
	if f.err != nil {
		return todo.IdeaItem{}, f.err
	}
	return f.idea, nil
}

func (f *fakeTodoService) VoteIdea(_ context.Context, _ todo.ViewerRef, req todo.VoteIdeaRequest) (todo.IdeaItem, error) {
	f.voteIdeaReq = req
	if f.err != nil {
		return todo.IdeaItem{}, f.err
	}
	return f.idea, nil
}

func (f *fakeTodoService) DeleteIdea(_ context.Context, viewer todo.ViewerRef, req todo.DeleteIdeaRequest) error {
	f.deleteIdeaViewer = viewer
	f.deleteIdeaReq = req
	if f.err != nil {
		return f.err
	}
	return nil
}

func (f *fakeTodoService) ArchiveIdea(_ context.Context, viewer todo.ViewerRef, req todo.ArchiveIdeaRequest) (todo.IdeaItem, error) {
	f.archiveIdeaViewer = viewer
	f.archiveIdeaReq = req
	if f.err != nil {
		return todo.IdeaItem{}, f.err
	}
	return f.idea, nil
}

func (f *fakeTodoService) RestoreIdea(_ context.Context, viewer todo.ViewerRef, req todo.RestoreIdeaRequest) (todo.IdeaItem, error) {
	f.restoreIdeaViewer = viewer
	f.restoreIdeaReq = req
	if f.err != nil {
		return todo.IdeaItem{}, f.err
	}
	return f.idea, nil
}

func (f *fakeTodoService) ConvertIdeaToTodo(_ context.Context, viewer todo.ViewerRef, req todo.ConvertIdeaToTodoRequest) (todo.IdeaConversionResult, error) {
	f.convertIdeaViewer = viewer
	f.convertIdeaReq = req
	if f.err != nil {
		return todo.IdeaConversionResult{}, f.err
	}
	return f.conversion, nil
}

func (f *fakeTodoService) ListIdeas(_ context.Context, _ todo.ViewerRef, query todo.ListIdeasQuery) (todo.IdeaPage, error) {
	f.listIdeasQuery = query
	if f.err != nil {
		return todo.IdeaPage{}, f.err
	}
	return f.ideaPage, nil
}

func (f *fakeTodoService) GetWorkHubSummary(_ context.Context, viewer todo.ViewerRef) (todo.WorkHubSummary, error) {
	f.summaryViewer = viewer
	if f.err != nil {
		return todo.WorkHubSummary{}, f.err
	}
	return todo.WorkHubSummary{}, nil
}

func (f *fakeTodoService) SuggestDraft(_ context.Context, viewer todo.ViewerRef, req todo.SuggestDraftRequest) (todo.DraftSuggestion, error) {
	f.suggestReq = req
	if f.err != nil {
		return todo.DraftSuggestion{}, f.err
	}
	if f.suggestion.Title != "" {
		return f.suggestion, nil
	}
	return todo.DraftSuggestion{Title: "Use persisted commands", Tags: []string{"frontend", "polish"}}, nil
}

func (f *fakeTodoService) CreateIssueComment(_ context.Context, viewer todo.ViewerRef, req todo.CreateIssueCommentRequest) (todo.IssueComment, error) {
	f.createCommentReq = req
	if f.err != nil {
		return todo.IssueComment{}, f.err
	}
	return f.comment, nil
}

func (f *fakeTodoService) ListIssueComments(_ context.Context, _ todo.ViewerRef, query todo.ListIssueCommentsQuery) (todo.IssueCommentPage, error) {
	f.listCommentsQuery = query
	if f.err != nil {
		return todo.IssueCommentPage{}, f.err
	}
	return f.commentPage, nil
}

func TestTodoRoutesRejectMissingViewer(t *testing.T) {
	handler := newTestRouter(RouterOptions{Todo: &fakeTodoService{}})
	req := httptest.NewRequest(http.MethodGet, "/api/todos", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	var body ErrorResponse
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	if body.Code != ErrorUnauthorized {
		t.Fatalf("error response = %+v", body)
	}
}

func TestTodoViewerScopeIgnoresDepartmentQueryAndUsesTrustedProfile(t *testing.T) {
	principal := identity.Principal{
		SubjectProvider: "local",
		Subject:         "operator",
		UserID:          "operator",
		Username:        "operator",
		AgentProfileID:  "profile-operator",
		ViewerKind:      identity.ViewerKindUser,
		CoarseScopes:    []string{"idea.read"},
	}
	req := httptest.NewRequest(http.MethodGet, "/api/ideas/idea-1?department_id=dept-spoof", nil)
	req = req.WithContext(identity.ContextWithPrincipal(req.Context(), principal))
	applyPrincipalViewer(req, principal)
	router := &Router{agentProfiles: todoViewerProfileDirectory{profile: agentcore.AgentProfile{
		ID:        "profile-operator",
		AgentKind: agentcore.AgentKindHuman,
		Enabled:   true,
		Placement: agentcore.TalentPlacement{Status: agentcore.TalentPlacementInternal},
		OrgIdentity: agentcore.AgentOrgIdentity{
			OrganizationRef: agentcore.OrganizationRef{ID: "org-1"},
			DepartmentRef:   agentcore.DepartmentRef{ID: "dept-1"},
		},
	}}}
	router.applyPrincipalOrganizationScope(req, principal)
	viewer, ok := todoViewerFromRequest(httptest.NewRecorder(), req)
	if !ok {
		t.Fatal("trusted profile viewer was rejected")
	}
	if viewer.OrganizationID != "org-1" || viewer.DepartmentID != "dept-1" {
		t.Fatalf("viewer scope = %+v", viewer)
	}
}

func TestTodoRoutesDoNotAcceptBlockedReasonInGenericUpdate(t *testing.T) {
	service := &fakeTodoService{item: todoRouteItem("todo-1")}
	handler := newTestRouter(RouterOptions{Todo: service})
	req := authedRequest(http.MethodPatch, "/api/todos/todo-1", `{"blocked_reason":""}`)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.updateReq.TodoID != "" {
		t.Fatalf("generic update reached service: %+v", service.updateReq)
	}
}

func TestWorkHubSummaryRouteCarriesExplicitTimeZone(t *testing.T) {
	service := &fakeTodoService{}
	handler := newTestRouter(RouterOptions{Todo: service})
	req := authedRequest(http.MethodGet, "/api/work-hub/summary?timezone=Asia%2FShanghai", "")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body = %s", rec.Code, rec.Body.String())
	}
	if service.summaryViewer.TimeZone != "Asia/Shanghai" {
		t.Fatalf("summary viewer = %+v", service.summaryViewer)
	}
}

type todoViewerProfileDirectory struct {
	profile agentcore.AgentProfile
}

func (d todoViewerProfileDirectory) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	if id != d.profile.ID {
		return agentcore.AgentProfile{}, todo.ErrNotFound
	}
	return d.profile, nil
}

func (d todoViewerProfileDirectory) QueryDirectory(_ context.Context, _ agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	return agentcore.AgentDirectoryPage{}, nil
}

var _ TodoService = (*fakeTodoService)(nil)

func sameStrings(got []string, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
