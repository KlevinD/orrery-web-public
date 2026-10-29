package httptransport

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/todo"
)

func (r *Router) handleTodos(w http.ResponseWriter, req *http.Request, parts []string) {
	if r.todo == nil {
		writeTodoError(w, req, todo.ErrServiceDependency)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 2 {
		r.handleListTodos(w, req)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 2 {
		r.handleCreateTodo(w, req)
		return
	}
	if len(parts) < 3 {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	todoID := strings.TrimSpace(parts[2])
	if len(parts) >= 4 && parts[3] == "attachments" {
		r.handleTodoAttachments(w, req, parts)
		return
	}
	if todoID == "" {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 3 {
		r.handleGetTodo(w, req, todoID)
		return
	}
	if req.Method == http.MethodPatch && len(parts) == 3 {
		r.handleUpdateTodo(w, req, todoID)
		return
	}
	if req.Method == http.MethodDelete && len(parts) == 3 {
		r.handleDeleteTodo(w, req, todoID)
		return
	}
	if len(parts) == 4 && parts[3] == "comments" {
		r.handleIssueComments(w, req, todo.IssueKindTodo, todoID)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 4 && parts[3] == "discussion" {
		r.handleGetTodoDiscussion(w, req, todoID)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 4 && parts[3] == "execution-plans" {
		r.handleGetTodoExecutionPlans(w, req, todoID)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 4 && (parts[3] == "missions" || parts[3] == "progress") {
		r.handleGetTodoExecution(w, req, todoID, parts[3] == "progress")
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "execution-attempts" {
		r.handleStartTodoAutomation(w, req, todoID)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "claim" {
		r.handleClaimTodo(w, req, todoID)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "move" {
		r.handleMoveTodo(w, req, todoID)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "complete" {
		r.handleCompleteTodo(w, req, todoID)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "cancel" {
		r.handleCancelTodo(w, req, todoID)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "reopen" {
		r.handleReopenTodo(w, req, todoID)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 5 && parts[3] == "automation" && parts[4] == "start" {
		r.handleStartTodoAutomation(w, req, todoID)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 5 && parts[3] == "automation" && parts[4] == "check" {
		r.handleCheckTodoAutomation(w, req, todoID)
		return
	}
	writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "not_found", Message: "route not found", CorrelationID: correlationID(req)})
}

func (r *Router) handleGetTodoDiscussion(w http.ResponseWriter, req *http.Request, todoID string) {
	if r.discussions == nil {
		writeDiscussionError(w, req, discussionsession.ErrServiceDependency)
		return
	}
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	item, err := r.todo.GetTodo(req.Context(), viewer, todoID)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	if item.DiscussionBinding == nil || strings.TrimSpace(item.DiscussionBinding.ConversationID) == "" {
		writeTodoError(w, req, todo.ErrNotFound)
		return
	}
	discussionViewer, ok := discussionViewerFromRequest(w, req)
	if !ok {
		return
	}
	conversation, err := r.discussions.GetConversationPage(req.Context(), discussionViewer, item.DiscussionBinding.ConversationID, discussionsession.ListMessagesQuery{
		Limit: positiveQueryInt(req, "limit", 20), Offset: discussionNonNegativeQueryInt(req, "offset", 0),
	})
	if err != nil {
		writeDiscussionError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, toDiscussionDTOForViewer(conversation, discussionViewer))
}

func (r *Router) handleGetTodoExecutionPlans(w http.ResponseWriter, req *http.Request, todoID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	plans, err := r.todo.GetTodoExecutionPlans(req.Context(), viewer, todoID)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, plans)
}

func (r *Router) handleGetTodoExecution(w http.ResponseWriter, req *http.Request, todoID string, currentOnly bool) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	summary, err := r.todo.GetTodoExecutionProgress(req.Context(), viewer, todoID)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	if currentOnly {
		if summary.CurrentProgress == nil {
			writeTodoError(w, req, todo.ErrNotFound)
			return
		}
		writeJSON(w, http.StatusOK, summary.CurrentProgress)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (r *Router) handleStartTodoAutomation(w http.ResponseWriter, req *http.Request, todoID string) {
	if r.todoAutomation == nil {
		writeTodoError(w, req, todo.ErrServiceDependency)
		return
	}
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	if idempotencyKey(req) == "" {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	var body struct {
		PlanID      string `json:"plan_id"`
		PlanDigest  string `json:"plan_digest"`
		TodoVersion int64  `json:"todo_version"`
	}
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	state, err := r.todoAutomation.StartTodoAutomationExecution(req.Context(), viewer, todo.StartTodoAutomationExecutionRequest{
		TodoID: todoID, PlanID: body.PlanID, PlanDigest: body.PlanDigest, TodoVersion: body.TodoVersion, IdempotencyKey: idempotencyKey(req),
	})
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusAccepted, state)
}

func (r *Router) handleCheckTodoAutomation(w http.ResponseWriter, req *http.Request, todoID string) {
	if r.todoAutomation == nil {
		writeTodoError(w, req, todo.ErrServiceDependency)
		return
	}
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	key := idempotencyKey(req)
	if key == "" {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	result, err := r.todoAutomation.CheckTodoAutomation(req.Context(), viewer, todo.CheckTodoAutomationRequest{
		TodoID: todoID, IdempotencyKey: key,
	})
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) handleIdeas(w http.ResponseWriter, req *http.Request, parts []string) {
	if r.todo == nil {
		writeTodoError(w, req, todo.ErrServiceDependency)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 2 {
		r.handleListIdeas(w, req)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 2 {
		r.handleCreateIdea(w, req)
		return
	}
	if len(parts) < 3 {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	ideaID := strings.TrimSpace(parts[2])
	if ideaID == "" {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 3 {
		r.handleGetIdea(w, req, ideaID)
		return
	}
	if req.Method == http.MethodPatch && len(parts) == 3 {
		r.handleUpdateIdea(w, req, ideaID)
		return
	}
	if req.Method == http.MethodDelete && len(parts) == 3 {
		r.handleDeleteIdea(w, req, ideaID)
		return
	}
	if len(parts) == 4 && parts[3] == "comments" {
		r.handleIssueComments(w, req, todo.IssueKindIdea, ideaID)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "archive" {
		r.handleArchiveIdea(w, req, ideaID)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "restore" {
		r.handleRestoreIdea(w, req, ideaID)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "convert-to-todo" {
		r.handleConvertIdeaToTodo(w, req, ideaID)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "transition" {
		r.handleTransitionIdea(w, req, ideaID)
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 && parts[3] == "vote" {
		r.handleVoteIdea(w, req, ideaID)
		return
	}
	writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "not_found", Message: "route not found", CorrelationID: correlationID(req)})
}

func (r *Router) handleSuggestTodoDraft(w http.ResponseWriter, req *http.Request) {
	if r.todo == nil {
		writeTodoError(w, req, todo.ErrServiceDependency)
		return
	}
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	var body todo.SuggestDraftRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	suggestion, err := r.todo.SuggestDraft(req.Context(), viewer, body)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, suggestion)
}

func (r *Router) handleListTodos(w http.ResponseWriter, req *http.Request) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	page, err := r.todo.ListTodos(req.Context(), viewer, todo.ListTodosQuery{
		Status:                 todo.TodoStatus(strings.TrimSpace(req.URL.Query().Get("status"))),
		Priority:               todo.TodoPriority(strings.TrimSpace(req.URL.Query().Get("priority"))),
		OrganizationID:         strings.TrimSpace(req.URL.Query().Get("organization_id")),
		DepartmentID:           strings.TrimSpace(req.URL.Query().Get("department_id")),
		ProjectID:              strings.TrimSpace(req.URL.Query().Get("project_id")),
		DevelopmentProjectID:   strings.TrimSpace(req.URL.Query().Get("development_project_id")),
		SourceIdeaID:           strings.TrimSpace(req.URL.Query().Get("source_idea_id")),
		AssigneeID:             strings.TrimSpace(req.URL.Query().Get("assignee_id")),
		CreatedBy:              strings.TrimSpace(req.URL.Query().Get("created_by")),
		Q:                      strings.TrimSpace(req.URL.Query().Get("q")),
		DueBefore:              todoQueryTime(req, "due_before"),
		Mine:                   todoQueryBool(req, "mine"),
		Unassigned:             todoQueryBool(req, "unassigned"),
		IncludeCompleted:       todoQueryBool(req, "include_completed"),
		IncludePendingDelivery: todoQueryBool(req, "include_pending_delivery"),
		Limit:                  positiveQueryInt(req, "limit", 50),
		Offset:                 todoNonNegativeQueryInt(req, "offset", 0),
	})
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleCreateTodo(w http.ResponseWriter, req *http.Request) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	var body todo.CreateTodoRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	item, err := r.todo.CreateTodo(req.Context(), viewer, body)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (r *Router) handleGetTodo(w http.ResponseWriter, req *http.Request, todoID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	item, err := r.todo.GetTodo(req.Context(), viewer, todoID)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (r *Router) handleUpdateTodo(w http.ResponseWriter, req *http.Request, todoID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	var body todo.UpdateTodoRequest
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	body.TodoID = todoID
	item, err := r.todo.UpdateTodo(req.Context(), viewer, body)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (r *Router) handleDeleteTodo(w http.ResponseWriter, req *http.Request, todoID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	body, err := decodeOptionalTodoRequest[todo.DeleteTodoRequest](req.Body)
	if err != nil {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	body.TodoID = todoID
	if err := r.todo.DeleteTodo(req.Context(), viewer, body); err != nil {
		writeTodoError(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) handleClaimTodo(w http.ResponseWriter, req *http.Request, todoID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	body, err := decodeOptionalTodoRequest[todo.ClaimTodoRequest](req.Body)
	if err != nil {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	body.TodoID = todoID
	item, err := r.todo.ClaimTodo(req.Context(), viewer, body)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (r *Router) handleMoveTodo(w http.ResponseWriter, req *http.Request, todoID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	var body todo.MoveTodoRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	body.TodoID = todoID
	item, err := r.todo.MoveTodo(req.Context(), viewer, body)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (r *Router) handleCompleteTodo(w http.ResponseWriter, req *http.Request, todoID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	body, err := decodeOptionalTodoRequest[todo.CompleteTodoRequest](req.Body)
	if err != nil {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	body.TodoID = todoID
	item, err := r.todo.CompleteTodo(req.Context(), viewer, body)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (r *Router) handleCancelTodo(w http.ResponseWriter, req *http.Request, todoID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	body, err := decodeOptionalTodoRequest[todo.CancelTodoRequest](req.Body)
	if err != nil {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	body.TodoID = todoID
	item, err := r.todo.CancelTodo(req.Context(), viewer, body)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (r *Router) handleReopenTodo(w http.ResponseWriter, req *http.Request, todoID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	body, err := decodeOptionalTodoRequest[todo.ReopenTodoRequest](req.Body)
	if err != nil {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	body.TodoID = todoID
	item, err := r.todo.ReopenTodo(req.Context(), viewer, body)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (r *Router) handleListIdeas(w http.ResponseWriter, req *http.Request) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	page, err := r.todo.ListIdeas(req.Context(), viewer, todo.ListIdeasQuery{
		Status:         todo.IdeaStatus(strings.TrimSpace(req.URL.Query().Get("status"))),
		Visibility:     todo.IdeaVisibility(strings.TrimSpace(req.URL.Query().Get("visibility"))),
		OrganizationID: strings.TrimSpace(req.URL.Query().Get("organization_id")),
		DepartmentID:   strings.TrimSpace(req.URL.Query().Get("department_id")),
		OwnerID:        strings.TrimSpace(req.URL.Query().Get("owner_id")),
		ReviewerID:     strings.TrimSpace(req.URL.Query().Get("reviewer_id")),
		Q:              strings.TrimSpace(req.URL.Query().Get("q")),
		Limit:          positiveQueryInt(req, "limit", 50),
		Offset:         todoNonNegativeQueryInt(req, "offset", 0),
	})
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleCreateIdea(w http.ResponseWriter, req *http.Request) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	var body todo.CreateIdeaRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	item, err := r.todo.CreateIdea(req.Context(), viewer, body)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (r *Router) handleGetIdea(w http.ResponseWriter, req *http.Request, ideaID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	item, err := r.todo.GetIdea(req.Context(), viewer, ideaID)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (r *Router) handleUpdateIdea(w http.ResponseWriter, req *http.Request, ideaID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	var body todo.UpdateIdeaRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	body.IdeaID = ideaID
	item, err := r.todo.UpdateIdea(req.Context(), viewer, body)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (r *Router) handleDeleteIdea(w http.ResponseWriter, req *http.Request, ideaID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	body, err := decodeOptionalTodoRequest[todo.DeleteIdeaRequest](req.Body)
	if err != nil {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	body.IdeaID = ideaID
	if err := r.todo.DeleteIdea(req.Context(), viewer, body); err != nil {
		writeTodoError(w, req, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (r *Router) handleArchiveIdea(w http.ResponseWriter, req *http.Request, ideaID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	item, err := r.todo.ArchiveIdea(req.Context(), viewer, todo.ArchiveIdeaRequest{IdeaID: ideaID})
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (r *Router) handleRestoreIdea(w http.ResponseWriter, req *http.Request, ideaID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	item, err := r.todo.RestoreIdea(req.Context(), viewer, todo.RestoreIdeaRequest{IdeaID: ideaID})
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (r *Router) handleConvertIdeaToTodo(w http.ResponseWriter, req *http.Request, ideaID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	body, err := decodeOptionalTodoRequest[todo.ConvertIdeaToTodoRequest](req.Body)
	if err != nil {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	body.IdeaID = ideaID
	result, err := r.todo.ConvertIdeaToTodo(req.Context(), viewer, body)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) handleTransitionIdea(w http.ResponseWriter, req *http.Request, ideaID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	var body todo.TransitionIdeaRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	body.IdeaID = ideaID
	item, err := r.todo.TransitionIdea(req.Context(), viewer, body)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (r *Router) handleVoteIdea(w http.ResponseWriter, req *http.Request, ideaID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	item, err := r.todo.VoteIdea(req.Context(), viewer, todo.VoteIdeaRequest{IdeaID: ideaID})
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (r *Router) handleWorkHubSummary(w http.ResponseWriter, req *http.Request) {
	if r.todo == nil {
		writeTodoError(w, req, todo.ErrServiceDependency)
		return
	}
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	summary, err := r.todo.GetWorkHubSummary(req.Context(), viewer)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, summary)
}

func (r *Router) handleIssueComments(w http.ResponseWriter, req *http.Request, kind todo.IssueKind, issueID string) {
	switch req.Method {
	case http.MethodGet:
		r.handleListIssueComments(w, req, kind, issueID)
	case http.MethodPost:
		r.handleCreateIssueComment(w, req, kind, issueID)
	default:
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "not_found", Message: "route not found", CorrelationID: correlationID(req)})
	}
}

func (r *Router) handleListIssueComments(w http.ResponseWriter, req *http.Request, kind todo.IssueKind, issueID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	page, err := r.todo.ListIssueComments(req.Context(), viewer, todo.ListIssueCommentsQuery{
		IssueKind: kind,
		IssueID:   issueID,
		Limit:     positiveQueryInt(req, "limit", 20),
		Offset:    todoNonNegativeQueryInt(req, "offset", 0),
	})
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleCreateIssueComment(w http.ResponseWriter, req *http.Request, kind todo.IssueKind, issueID string) {
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	var body todo.CreateIssueCommentRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return
	}
	body.IssueKind = kind
	body.IssueID = issueID
	comment, err := r.todo.CreateIssueComment(req.Context(), viewer, body)
	if err != nil {
		writeTodoError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, comment)
}

func decodeOptionalTodoRequest[T any](reader io.Reader) (T, error) {
	var body T
	if reader == nil {
		return body, nil
	}
	raw, err := io.ReadAll(reader)
	if err != nil {
		return body, err
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return body, nil
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return body, err
	}
	return body, nil
}

func todoViewerFromRequest(w http.ResponseWriter, req *http.Request) (todo.ViewerRef, bool) {
	base, err := ViewerFromRequest(req)
	if err != nil {
		writeTodoError(w, req, todo.ErrInvalidRequest)
		return todo.ViewerRef{}, false
	}
	return todo.ViewerRef{
		TenantID:               strings.TrimSpace(firstNonEmpty(req.Header.Get("X-Tenant-ID"), req.Header.Get("X-Tenant"), "system")),
		OrganizationID:         strings.TrimSpace(req.Header.Get("X-Organization-ID")),
		DepartmentID:           strings.TrimSpace(req.Header.Get("X-Department-ID")),
		TimeZone:               strings.TrimSpace(firstNonEmpty(req.URL.Query().Get("timezone"), req.Header.Get("X-Time-Zone"), "UTC")),
		ViewerID:               base.ViewerID,
		ViewerKind:             todo.ViewerKind(base.ViewerKind),
		AuthorizationSubjectID: base.AuthorizationSubjectID,
		DisplayName:            displayNameFromRequest(req),
		PermissionScope:        append([]string(nil), base.PermissionScope...),
	}, true
}

func writeTodoError(w http.ResponseWriter, req *http.Request, err error) {
	status, response := MapError(err, correlationID(req))
	writeJSON(w, status, response)
}

func todoNonNegativeQueryInt(req *http.Request, name string, fallback int) int {
	raw := strings.TrimSpace(req.URL.Query().Get(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return fallback
	}
	return value
}

func todoQueryBool(req *http.Request, name string) bool {
	value, err := strconv.ParseBool(strings.TrimSpace(req.URL.Query().Get(name)))
	return err == nil && value
}

func todoQueryTime(req *http.Request, name string) time.Time {
	raw := strings.TrimSpace(req.URL.Query().Get(name))
	if raw == "" {
		return time.Time{}
	}
	value, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}
	}
	return value.UTC()
}
