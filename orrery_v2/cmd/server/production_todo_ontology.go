package main

import (
	"context"
	"io"
	"strings"
	"time"

	todoapp "orrery_v2/internal/app/todo"
	"orrery_v2/internal/platform/graphprotocol"
)

type productionTodoOntologyService struct {
	inner *todoapp.Service
	api   *productionAPI
}

func newProductionTodoOntologyService(inner *todoapp.Service, api *productionAPI) *productionTodoOntologyService {
	return &productionTodoOntologyService{inner: inner, api: api}
}

func (s *productionTodoOntologyService) CreateTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.CreateTodoRequest) (todoapp.TodoItem, error) {
	if err := s.requireOntologyPublisher(); err != nil {
		return todoapp.TodoItem{}, err
	}
	item, err := s.inner.CreateTodo(ctx, viewer, req)
	if err != nil {
		return todoapp.TodoItem{}, err
	}
	if err := s.publishTodoChanged(ctx, viewer, item, string(item.Status), "todo.create", item.UpdatedAt); err != nil {
		return todoapp.TodoItem{}, err
	}
	if err := s.inner.DeliverCreationEvent(ctx, item.TenantID, item.TodoID, s.publishTodoCreationEvent); err != nil {
		return todoapp.TodoItem{}, err
	}
	return item, nil
}

func (s *productionTodoOntologyService) GetTodo(ctx context.Context, viewer todoapp.ViewerRef, todoID string) (todoapp.TodoItem, error) {
	return s.inner.GetTodo(ctx, viewer, todoID)
}

func (s *productionTodoOntologyService) UploadTodoAttachment(ctx context.Context, viewer todoapp.ViewerRef, todoID, name string, reader io.Reader) (todoapp.TodoAttachment, error) {
	if err := s.requireOntologyPublisher(); err != nil {
		return todoapp.TodoAttachment{}, err
	}
	attachment, err := s.inner.UploadTodoAttachment(ctx, viewer, todoID, name, reader)
	if err != nil {
		return todoapp.TodoAttachment{}, err
	}
	item, err := s.inner.GetTodo(ctx, viewer, todoID)
	if err != nil {
		return todoapp.TodoAttachment{}, err
	}
	if err := s.publishTodoChanged(ctx, viewer, item, string(item.Status), "todo.attachment.upload", item.UpdatedAt); err != nil {
		return todoapp.TodoAttachment{}, err
	}
	return attachment, nil
}

func (s *productionTodoOntologyService) OpenTodoAttachment(ctx context.Context, viewer todoapp.ViewerRef, todoID, attachmentID string) (todoapp.TodoAttachment, io.ReadCloser, error) {
	return s.inner.OpenTodoAttachment(ctx, viewer, todoID, attachmentID)
}

func (s *productionTodoOntologyService) DeleteTodoAttachment(ctx context.Context, viewer todoapp.ViewerRef, todoID, attachmentID string) error {
	if err := s.requireOntologyPublisher(); err != nil {
		return err
	}
	if err := s.inner.DeleteTodoAttachment(ctx, viewer, todoID, attachmentID); err != nil {
		return err
	}
	item, err := s.inner.GetTodo(ctx, viewer, todoID)
	if err != nil {
		return err
	}
	return s.publishTodoChanged(ctx, viewer, item, string(item.Status), "todo.attachment.delete", item.UpdatedAt)
}

func (s *productionTodoOntologyService) UpdateTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.UpdateTodoRequest) (todoapp.TodoItem, error) {
	if err := s.requireOntologyPublisher(); err != nil {
		return todoapp.TodoItem{}, err
	}
	item, err := s.inner.UpdateTodo(ctx, viewer, req)
	if err != nil {
		return todoapp.TodoItem{}, err
	}
	if err := s.publishTodoChanged(ctx, viewer, item, string(item.Status), "todo.update", item.UpdatedAt); err != nil {
		return todoapp.TodoItem{}, err
	}
	return item, nil
}

func (s *productionTodoOntologyService) DeleteTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.DeleteTodoRequest) error {
	if err := s.requireOntologyPublisher(); err != nil {
		return err
	}
	item, err := s.inner.GetTodo(ctx, viewer, req.TodoID)
	if err != nil {
		return err
	}
	if err := s.inner.DeleteTodo(ctx, viewer, req); err != nil {
		return err
	}
	return s.publishTodoChanged(ctx, viewer, item, "deleted", "todo.delete", time.Now().UTC())
}

func (s *productionTodoOntologyService) ClaimTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.ClaimTodoRequest) (todoapp.TodoItem, error) {
	if err := s.requireOntologyPublisher(); err != nil {
		return todoapp.TodoItem{}, err
	}
	item, err := s.inner.ClaimTodo(ctx, viewer, req)
	if err != nil {
		return todoapp.TodoItem{}, err
	}
	if err := s.publishTodoChanged(ctx, viewer, item, string(item.Status), "todo.claim", item.UpdatedAt); err != nil {
		return todoapp.TodoItem{}, err
	}
	return item, nil
}

func (s *productionTodoOntologyService) MoveTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.MoveTodoRequest) (todoapp.TodoItem, error) {
	if err := s.requireOntologyPublisher(); err != nil {
		return todoapp.TodoItem{}, err
	}
	item, err := s.inner.MoveTodo(ctx, viewer, req)
	if err != nil {
		return todoapp.TodoItem{}, err
	}
	if err := s.publishTodoChanged(ctx, viewer, item, string(item.Status), "todo.move", item.UpdatedAt); err != nil {
		return todoapp.TodoItem{}, err
	}
	return item, nil
}

func (s *productionTodoOntologyService) CompleteTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.CompleteTodoRequest) (todoapp.TodoItem, error) {
	if err := s.requireOntologyPublisher(); err != nil {
		return todoapp.TodoItem{}, err
	}
	item, err := s.inner.CompleteTodo(ctx, viewer, req)
	if err != nil {
		return todoapp.TodoItem{}, err
	}
	if err := s.publishTodoChanged(ctx, viewer, item, string(item.Status), "todo.complete", item.UpdatedAt); err != nil {
		return todoapp.TodoItem{}, err
	}
	return item, nil
}

func (s *productionTodoOntologyService) CancelTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.CancelTodoRequest) (todoapp.TodoItem, error) {
	if err := s.requireOntologyPublisher(); err != nil {
		return todoapp.TodoItem{}, err
	}
	item, err := s.inner.CancelTodo(ctx, viewer, req)
	if err != nil {
		return todoapp.TodoItem{}, err
	}
	if err := s.publishTodoChanged(ctx, viewer, item, string(item.Status), "todo.cancel", item.UpdatedAt); err != nil {
		return todoapp.TodoItem{}, err
	}
	return item, nil
}

func (s *productionTodoOntologyService) ReopenTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.ReopenTodoRequest) (todoapp.TodoItem, error) {
	if err := s.requireOntologyPublisher(); err != nil {
		return todoapp.TodoItem{}, err
	}
	item, err := s.inner.ReopenTodo(ctx, viewer, req)
	if err != nil {
		return todoapp.TodoItem{}, err
	}
	if err := s.publishTodoChanged(ctx, viewer, item, string(item.Status), "todo.reopen", item.UpdatedAt); err != nil {
		return todoapp.TodoItem{}, err
	}
	return item, nil
}

func (s *productionTodoOntologyService) ListTodos(ctx context.Context, viewer todoapp.ViewerRef, query todoapp.ListTodosQuery) (todoapp.TodoPage, error) {
	return s.inner.ListTodos(ctx, viewer, query)
}

func (s *productionTodoOntologyService) GetTodoExecutionProgress(ctx context.Context, viewer todoapp.ViewerRef, todoID string) (todoapp.TodoExecutionSummary, error) {
	return s.inner.GetTodoExecutionProgress(ctx, viewer, todoID)
}

func (s *productionTodoOntologyService) GetTodoExecutionPlans(ctx context.Context, viewer todoapp.ViewerRef, todoID string) (todoapp.TodoExecutionPlanPage, error) {
	return s.inner.GetTodoExecutionPlans(ctx, viewer, todoID)
}

func (s *productionTodoOntologyService) CreateIdea(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.CreateIdeaRequest) (todoapp.IdeaItem, error) {
	if err := s.requireOntologyPublisher(); err != nil {
		return todoapp.IdeaItem{}, err
	}
	item, err := s.inner.CreateIdea(ctx, viewer, req)
	if err != nil {
		return todoapp.IdeaItem{}, err
	}
	if err := s.publishIdeaChanged(ctx, viewer, item, string(item.Status), "idea.create", item.UpdatedAt); err != nil {
		return todoapp.IdeaItem{}, err
	}
	return item, nil
}

func (s *productionTodoOntologyService) GetIdea(ctx context.Context, viewer todoapp.ViewerRef, ideaID string) (todoapp.IdeaItem, error) {
	return s.inner.GetIdea(ctx, viewer, ideaID)
}

func (s *productionTodoOntologyService) UpdateIdea(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.UpdateIdeaRequest) (todoapp.IdeaItem, error) {
	if err := s.requireOntologyPublisher(); err != nil {
		return todoapp.IdeaItem{}, err
	}
	item, err := s.inner.UpdateIdea(ctx, viewer, req)
	if err != nil {
		return todoapp.IdeaItem{}, err
	}
	if err := s.publishIdeaChanged(ctx, viewer, item, string(item.Status), "idea.update", item.UpdatedAt); err != nil {
		return todoapp.IdeaItem{}, err
	}
	return item, nil
}

func (s *productionTodoOntologyService) TransitionIdea(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.TransitionIdeaRequest) (todoapp.IdeaItem, error) {
	if err := s.requireOntologyPublisher(); err != nil {
		return todoapp.IdeaItem{}, err
	}
	item, err := s.inner.TransitionIdea(ctx, viewer, req)
	if err != nil {
		return todoapp.IdeaItem{}, err
	}
	if err := s.publishIdeaChanged(ctx, viewer, item, string(item.Status), "idea.transition", item.UpdatedAt); err != nil {
		return todoapp.IdeaItem{}, err
	}
	return item, nil
}

func (s *productionTodoOntologyService) VoteIdea(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.VoteIdeaRequest) (todoapp.IdeaItem, error) {
	if err := s.requireOntologyPublisher(); err != nil {
		return todoapp.IdeaItem{}, err
	}
	item, err := s.inner.VoteIdea(ctx, viewer, req)
	if err != nil {
		return todoapp.IdeaItem{}, err
	}
	if err := s.publishIdeaChanged(ctx, viewer, item, string(item.Status), "idea.vote", item.UpdatedAt); err != nil {
		return todoapp.IdeaItem{}, err
	}
	return item, nil
}

func (s *productionTodoOntologyService) DeleteIdea(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.DeleteIdeaRequest) error {
	if err := s.requireOntologyPublisher(); err != nil {
		return err
	}
	item, err := s.inner.GetIdea(ctx, viewer, req.IdeaID)
	if err != nil {
		return err
	}
	if err := s.inner.DeleteIdea(ctx, viewer, req); err != nil {
		return err
	}
	return s.publishIdeaChanged(ctx, viewer, item, "deleted", "idea.delete", time.Now().UTC())
}

func (s *productionTodoOntologyService) ArchiveIdea(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.ArchiveIdeaRequest) (todoapp.IdeaItem, error) {
	if err := s.requireOntologyPublisher(); err != nil {
		return todoapp.IdeaItem{}, err
	}
	item, err := s.inner.ArchiveIdea(ctx, viewer, req)
	if err != nil {
		return todoapp.IdeaItem{}, err
	}
	if err := s.publishIdeaChanged(ctx, viewer, item, string(item.Status), "idea.archive", item.UpdatedAt); err != nil {
		return todoapp.IdeaItem{}, err
	}
	return item, nil
}

func (s *productionTodoOntologyService) RestoreIdea(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.RestoreIdeaRequest) (todoapp.IdeaItem, error) {
	if err := s.requireOntologyPublisher(); err != nil {
		return todoapp.IdeaItem{}, err
	}
	item, err := s.inner.RestoreIdea(ctx, viewer, req)
	if err != nil {
		return todoapp.IdeaItem{}, err
	}
	if err := s.publishIdeaChanged(ctx, viewer, item, string(item.Status), "idea.restore", item.UpdatedAt); err != nil {
		return todoapp.IdeaItem{}, err
	}
	return item, nil
}

func (s *productionTodoOntologyService) ConvertIdeaToTodo(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.ConvertIdeaToTodoRequest) (todoapp.IdeaConversionResult, error) {
	if err := s.requireOntologyPublisher(); err != nil {
		return todoapp.IdeaConversionResult{}, err
	}
	result, err := s.inner.ConvertIdeaToTodo(ctx, viewer, req)
	if err != nil {
		return todoapp.IdeaConversionResult{}, err
	}
	if err := s.publishIdeaChanged(ctx, viewer, result.Idea, string(result.Idea.Status), "idea.convert_to_todo", result.Idea.UpdatedAt); err != nil {
		return todoapp.IdeaConversionResult{}, err
	}
	if err := s.publishTodoChanged(ctx, viewer, result.Todo, string(result.Todo.Status), "todo.idea_conversion", result.Todo.UpdatedAt); err != nil {
		return todoapp.IdeaConversionResult{}, err
	}
	if err := s.inner.DeliverCreationEvent(ctx, result.Todo.TenantID, result.Todo.TodoID, s.publishTodoCreationEvent); err != nil {
		return todoapp.IdeaConversionResult{}, err
	}
	if err := s.publishIdeaConvertedToTodo(ctx, viewer, result); err != nil {
		return todoapp.IdeaConversionResult{}, err
	}
	return result, nil
}

func (s *productionTodoOntologyService) ListIdeas(ctx context.Context, viewer todoapp.ViewerRef, query todoapp.ListIdeasQuery) (todoapp.IdeaPage, error) {
	return s.inner.ListIdeas(ctx, viewer, query)
}

func (s *productionTodoOntologyService) GetWorkHubSummary(ctx context.Context, viewer todoapp.ViewerRef) (todoapp.WorkHubSummary, error) {
	return s.inner.GetWorkHubSummary(ctx, viewer)
}

func (s *productionTodoOntologyService) SuggestDraft(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.SuggestDraftRequest) (todoapp.DraftSuggestion, error) {
	return s.inner.SuggestDraft(ctx, viewer, req)
}

func (s *productionTodoOntologyService) CreateIssueComment(ctx context.Context, viewer todoapp.ViewerRef, req todoapp.CreateIssueCommentRequest) (todoapp.IssueComment, error) {
	if err := s.requireOntologyPublisher(); err != nil {
		return todoapp.IssueComment{}, err
	}
	comment, err := s.inner.CreateIssueComment(ctx, viewer, req)
	if err != nil {
		return todoapp.IssueComment{}, err
	}
	if err := s.publishIssueCommentLinked(ctx, viewer, comment, "comment.create"); err != nil {
		return todoapp.IssueComment{}, err
	}
	return comment, nil
}

func (s *productionTodoOntologyService) ListIssueComments(ctx context.Context, viewer todoapp.ViewerRef, query todoapp.ListIssueCommentsQuery) (todoapp.IssueCommentPage, error) {
	return s.inner.ListIssueComments(ctx, viewer, query)
}

func (s *productionTodoOntologyService) requireOntologyPublisher() error {
	if s == nil || s.inner == nil || s.api == nil || s.api.ontologyProject == nil || s.api.ontologyEvents.store == nil {
		return todoapp.ErrServiceDependency
	}
	return nil
}

func (s *productionTodoOntologyService) publishIdeaChanged(ctx context.Context, viewer todoapp.ViewerRef, item todoapp.IdeaItem, status string, source string, occurredAt time.Time) error {
	if err := s.requireOntologyPublisher(); err != nil {
		return err
	}
	if strings.TrimSpace(item.IdeaID) == "" {
		return todoapp.ErrInvalidRequest
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	if strings.TrimSpace(status) == "" {
		status = string(item.Status)
	}
	if strings.TrimSpace(source) == "" {
		source = "idea.change"
	}
	return s.api.applyOntologyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "planning-idea-captured:" + item.IdeaID + ":" + source + ":rev-" + todoOntologyEventRevision(occurredAt),
		TenantID: firstNonBlank(item.TenantID, viewer.TenantID, "system"),
		Type:     "planning.idea.captured",
		StreamID: item.IdeaID,
		Version:  1,
		Payload: map[string]any{
			"id":              item.IdeaID,
			"issue_number":    item.IssueNumber,
			"title":           item.Title,
			"summary":         item.Content,
			"status":          status,
			"source":          source,
			"captured_by":     item.CreatedBy,
			"proposer_name":   item.ProposerName,
			"updated_by":      strings.TrimSpace(viewer.ViewerID),
			"organization_id": item.OrganizationID,
			"department_id":   item.DepartmentID,
			"owner_id":        item.OwnerID,
			"reviewer_id":     item.ReviewerID,
			"impact":          string(item.Impact),
			"effort":          string(item.Effort),
			"vote_count":      item.VoteCount,
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: string(viewer.ViewerKind), ID: viewer.ViewerID},
		OccurredAt:      occurredAt,
		SourceAccessRef: trustedOntologySourceRef("idea", item.IdeaID, firstNonBlank(item.TenantID, viewer.TenantID, "system")),
	})
}

func (s *productionTodoOntologyService) publishTodoChanged(ctx context.Context, viewer todoapp.ViewerRef, item todoapp.TodoItem, status string, source string, occurredAt time.Time) error {
	if err := s.requireOntologyPublisher(); err != nil {
		return err
	}
	if strings.TrimSpace(item.TodoID) == "" {
		return todoapp.ErrInvalidRequest
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	payload := map[string]any{
		"id":              item.TodoID,
		"issue_number":    item.IssueNumber,
		"title":           item.Title,
		"summary":         item.Description,
		"status":          status,
		"source":          source,
		"proposer_name":   item.ProposerName,
		"created_by":      item.CreatedBy,
		"updated_by":      strings.TrimSpace(viewer.ViewerID),
		"assignee_id":     item.AssigneeID,
		"assignee_name":   item.AssigneeName,
		"assignee_kind":   string(item.AssigneeKind),
		"priority":        string(item.Priority),
		"organization_id": item.OrganizationID,
		"department_id":   item.DepartmentID,
		"project_id":      item.ProjectID,
		"source_idea_id":  item.SourceIdeaID,
		"reviewer_id":     item.ReviewerID,
	}
	return s.api.applyOntologyEvent(ctx, graphprotocol.DomainEvent{
		ID:              "planning-todo-changed:" + item.TodoID + ":" + source + ":rev-" + todoOntologyEventRevision(occurredAt),
		TenantID:        firstNonBlank(item.TenantID, viewer.TenantID, "system"),
		Type:            "planning.todo.changed",
		StreamID:        item.TodoID,
		Version:         1,
		Payload:         payload,
		PublishedBy:     graphprotocol.ActorRef{Kind: string(viewer.ViewerKind), ID: viewer.ViewerID},
		OccurredAt:      occurredAt,
		SourceAccessRef: trustedOntologySourceRef("todo", item.TodoID, firstNonBlank(item.TenantID, viewer.TenantID, "system")),
	})
}

func (s *productionTodoOntologyService) publishIdeaConvertedToTodo(ctx context.Context, viewer todoapp.ViewerRef, result todoapp.IdeaConversionResult) error {
	if err := s.requireOntologyPublisher(); err != nil {
		return err
	}
	if strings.TrimSpace(result.Idea.IdeaID) == "" || strings.TrimSpace(result.Todo.TodoID) == "" {
		return todoapp.ErrInvalidRequest
	}
	return s.api.applyOntologyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "planning-idea-converted-to-todo:" + result.Idea.IdeaID + ":" + result.Todo.TodoID,
		TenantID: firstNonBlank(result.Idea.TenantID, result.Todo.TenantID, viewer.TenantID, "system"),
		Type:     "planning.idea.converted_to_todo",
		StreamID: result.Idea.IdeaID,
		Version:  1,
		Payload: map[string]any{
			"idea_id":     result.Idea.IdeaID,
			"idea_status": string(result.Idea.Status),
			"todo_id":     result.Todo.TodoID,
			"title":       result.Todo.Title,
			"summary":     result.Todo.Description,
			"status":      string(result.Todo.Status),
			"source":      "todo.idea_conversion",
			"created_by":  result.Todo.CreatedBy,
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: string(viewer.ViewerKind), ID: viewer.ViewerID},
		OccurredAt:      time.Now().UTC(),
		SourceAccessRef: trustedOntologySourceRef("idea", result.Idea.IdeaID, firstNonBlank(result.Idea.TenantID, result.Todo.TenantID, viewer.TenantID, "system")),
	})
}

func (s *productionTodoOntologyService) publishIssueCommentLinked(ctx context.Context, viewer todoapp.ViewerRef, comment todoapp.IssueComment, source string) error {
	if err := s.requireOntologyPublisher(); err != nil {
		return err
	}
	if strings.TrimSpace(comment.CommentID) == "" || strings.TrimSpace(comment.IssueID) == "" {
		return todoapp.ErrInvalidRequest
	}
	var eventType string
	switch comment.IssueKind {
	case todoapp.IssueKindTodo:
		eventType = "planning.todo.comment.linked"
	case todoapp.IssueKindIdea:
		eventType = "planning.idea.comment.linked"
	default:
		return todoapp.ErrInvalidRequest
	}
	occurredAt := comment.UpdatedAt
	if occurredAt.IsZero() {
		occurredAt = comment.CreatedAt
	}
	if occurredAt.IsZero() {
		occurredAt = time.Now().UTC()
	}
	return s.api.applyOntologyEvent(ctx, graphprotocol.DomainEvent{
		ID:       "planning-issue-comment-linked:" + comment.CommentID + ":" + string(comment.IssueKind) + ":" + comment.IssueID + ":" + source + ":rev-" + todoOntologyEventRevision(occurredAt),
		TenantID: firstNonBlank(comment.TenantID, viewer.TenantID, "system"),
		Type:     eventType,
		StreamID: comment.CommentID,
		Version:  1,
		Payload: map[string]any{
			"comment_id":  comment.CommentID,
			"issue_id":    comment.IssueID,
			"body":        comment.Body,
			"source":      source,
			"author_id":   comment.AuthorID,
			"author_name": comment.AuthorName,
		},
		PublishedBy:     graphprotocol.ActorRef{Kind: string(viewer.ViewerKind), ID: viewer.ViewerID},
		OccurredAt:      occurredAt,
		SourceAccessRef: trustedOntologySourceRef("issue_comment", comment.CommentID, firstNonBlank(comment.TenantID, viewer.TenantID, "system")),
	})
}

func todoOntologyEventRevision(t time.Time) string {
	if t.IsZero() {
		t = time.Now().UTC()
	}
	return strings.ReplaceAll(t.UTC().Format(time.RFC3339Nano), ":", "")
}
