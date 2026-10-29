package todo

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	toolstd "orrery_v2/internal/resource/tool/std"
)

type ToolExecutorOptions struct {
	Service ToolCommandService
}

type ToolExecutor struct {
	service ToolCommandService
}

type ToolCommandService interface {
	CreateTodo(context.Context, ViewerRef, CreateTodoRequest) (TodoItem, error)
	UpdateTodo(context.Context, ViewerRef, UpdateTodoRequest) (TodoItem, error)
	DeleteTodo(context.Context, ViewerRef, DeleteTodoRequest) error
	ClaimTodo(context.Context, ViewerRef, ClaimTodoRequest) (TodoItem, error)
	MoveTodo(context.Context, ViewerRef, MoveTodoRequest) (TodoItem, error)
	CompleteTodo(context.Context, ViewerRef, CompleteTodoRequest) (TodoItem, error)
	CancelTodo(context.Context, ViewerRef, CancelTodoRequest) (TodoItem, error)
	ReopenTodo(context.Context, ViewerRef, ReopenTodoRequest) (TodoItem, error)
	ListTodos(context.Context, ViewerRef, ListTodosQuery) (TodoPage, error)
	CreateIdea(context.Context, ViewerRef, CreateIdeaRequest) (IdeaItem, error)
	UpdateIdea(context.Context, ViewerRef, UpdateIdeaRequest) (IdeaItem, error)
	DeleteIdea(context.Context, ViewerRef, DeleteIdeaRequest) error
	ArchiveIdea(context.Context, ViewerRef, ArchiveIdeaRequest) (IdeaItem, error)
	RestoreIdea(context.Context, ViewerRef, RestoreIdeaRequest) (IdeaItem, error)
	TransitionIdea(context.Context, ViewerRef, TransitionIdeaRequest) (IdeaItem, error)
	VoteIdea(context.Context, ViewerRef, VoteIdeaRequest) (IdeaItem, error)
	ConvertIdeaToTodo(context.Context, ViewerRef, ConvertIdeaToTodoRequest) (IdeaConversionResult, error)
	ListIdeas(context.Context, ViewerRef, ListIdeasQuery) (IdeaPage, error)
}

type ToolExecutionRequest struct {
	ToolID    string
	Viewer    ViewerRef
	Arguments map[string]any
}

type ToolExecutionResult struct {
	Output map[string]any
}

func NewToolExecutor(opts ToolExecutorOptions) *ToolExecutor {
	return &ToolExecutor{service: opts.Service}
}

func (e *ToolExecutor) Execute(ctx context.Context, req ToolExecutionRequest) (ToolExecutionResult, error) {
	if e == nil || e.service == nil {
		return ToolExecutionResult{}, ErrServiceDependency
	}
	switch strings.TrimSpace(req.ToolID) {
	case string(toolstd.TodoCreate):
		var create CreateTodoRequest
		if err := decodeToolArguments(req.Arguments, &create); err != nil {
			return ToolExecutionResult{}, err
		}
		item, err := e.service.CreateTodo(ctx, req.Viewer, create)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: todoOutput(item)}, nil
	case string(toolstd.TodoUpdate):
		var update UpdateTodoRequest
		if err := decodeToolArguments(req.Arguments, &update); err != nil {
			return ToolExecutionResult{}, err
		}
		item, err := e.service.UpdateTodo(ctx, req.Viewer, update)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: todoOutput(item)}, nil
	case string(toolstd.TodoDelete):
		var del DeleteTodoRequest
		if err := decodeToolArguments(req.Arguments, &del); err != nil {
			return ToolExecutionResult{}, err
		}
		if err := e.service.DeleteTodo(ctx, req.Viewer, del); err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: map[string]any{"todo_id": strings.TrimSpace(del.TodoID), "deleted": true}}, nil
	case string(toolstd.TodoClaim):
		var claim ClaimTodoRequest
		if err := decodeToolArguments(req.Arguments, &claim); err != nil {
			return ToolExecutionResult{}, err
		}
		item, err := e.service.ClaimTodo(ctx, req.Viewer, claim)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: todoOutput(item)}, nil
	case string(toolstd.TodoMove):
		var move MoveTodoRequest
		if err := decodeToolArguments(req.Arguments, &move); err != nil {
			return ToolExecutionResult{}, err
		}
		item, err := e.service.MoveTodo(ctx, req.Viewer, move)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: todoOutput(item)}, nil
	case string(toolstd.TodoComplete):
		var complete CompleteTodoRequest
		if err := decodeToolArguments(req.Arguments, &complete); err != nil {
			return ToolExecutionResult{}, err
		}
		item, err := e.service.CompleteTodo(ctx, req.Viewer, complete)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: todoOutput(item)}, nil
	case string(toolstd.TodoCancel):
		var cancel CancelTodoRequest
		if err := decodeToolArguments(req.Arguments, &cancel); err != nil {
			return ToolExecutionResult{}, err
		}
		item, err := e.service.CancelTodo(ctx, req.Viewer, cancel)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: todoOutput(item)}, nil
	case string(toolstd.TodoReopen):
		var reopen ReopenTodoRequest
		if err := decodeToolArguments(req.Arguments, &reopen); err != nil {
			return ToolExecutionResult{}, err
		}
		item, err := e.service.ReopenTodo(ctx, req.Viewer, reopen)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: todoOutput(item)}, nil
	case string(toolstd.TodoList):
		var query ListTodosQuery
		if err := decodeToolArguments(req.Arguments, &query); err != nil {
			return ToolExecutionResult{}, err
		}
		page, err := e.service.ListTodos(ctx, req.Viewer, query)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: todoListOutput(page)}, nil
	case string(toolstd.IdeaCreate):
		var create CreateIdeaRequest
		if err := decodeToolArguments(req.Arguments, &create); err != nil {
			return ToolExecutionResult{}, err
		}
		item, err := e.service.CreateIdea(ctx, req.Viewer, create)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: ideaOutput(item)}, nil
	case string(toolstd.IdeaUpdate):
		var update UpdateIdeaRequest
		if err := decodeToolArguments(req.Arguments, &update); err != nil {
			return ToolExecutionResult{}, err
		}
		item, err := e.service.UpdateIdea(ctx, req.Viewer, update)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: ideaOutput(item)}, nil
	case string(toolstd.IdeaDelete):
		var del DeleteIdeaRequest
		if err := decodeToolArguments(req.Arguments, &del); err != nil {
			return ToolExecutionResult{}, err
		}
		if err := e.service.DeleteIdea(ctx, req.Viewer, del); err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: map[string]any{"idea_id": strings.TrimSpace(del.IdeaID), "deleted": true}}, nil
	case string(toolstd.IdeaArchive):
		var archive ArchiveIdeaRequest
		if err := decodeToolArguments(req.Arguments, &archive); err != nil {
			return ToolExecutionResult{}, err
		}
		item, err := e.service.ArchiveIdea(ctx, req.Viewer, archive)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: ideaOutput(item)}, nil
	case string(toolstd.IdeaRestore):
		var restore RestoreIdeaRequest
		if err := decodeToolArguments(req.Arguments, &restore); err != nil {
			return ToolExecutionResult{}, err
		}
		item, err := e.service.RestoreIdea(ctx, req.Viewer, restore)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: ideaOutput(item)}, nil
	case string(toolstd.IdeaTransition):
		var transition TransitionIdeaRequest
		if err := decodeToolArguments(req.Arguments, &transition); err != nil {
			return ToolExecutionResult{}, err
		}
		item, err := e.service.TransitionIdea(ctx, req.Viewer, transition)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: ideaOutput(item)}, nil
	case string(toolstd.IdeaVote):
		var vote VoteIdeaRequest
		if err := decodeToolArguments(req.Arguments, &vote); err != nil {
			return ToolExecutionResult{}, err
		}
		item, err := e.service.VoteIdea(ctx, req.Viewer, vote)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: ideaOutput(item)}, nil
	case string(toolstd.IdeaConvertToTodo):
		var convert ConvertIdeaToTodoRequest
		if err := decodeToolArguments(req.Arguments, &convert); err != nil {
			return ToolExecutionResult{}, err
		}
		result, err := e.service.ConvertIdeaToTodo(ctx, req.Viewer, convert)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: map[string]any{"idea": ideaOutput(result.Idea), "todo": todoOutput(result.Todo)}}, nil
	case string(toolstd.IdeaList):
		var query ListIdeasQuery
		if err := decodeToolArguments(req.Arguments, &query); err != nil {
			return ToolExecutionResult{}, err
		}
		page, err := e.service.ListIdeas(ctx, req.Viewer, query)
		if err != nil {
			return ToolExecutionResult{}, err
		}
		return ToolExecutionResult{Output: ideaListOutput(page)}, nil
	default:
		return ToolExecutionResult{}, fmt.Errorf("%w: unsupported todo tool %q", ErrInvalidRequest, req.ToolID)
	}
}

func decodeToolArguments(arguments map[string]any, out any) error {
	raw, err := json.Marshal(arguments)
	if err != nil {
		return fmt.Errorf("%w: encode tool arguments", ErrInvalidRequest)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("%w: decode tool arguments", ErrInvalidRequest)
	}
	return nil
}

func todoOutput(item TodoItem) map[string]any {
	out := map[string]any{
		"todo_id":      item.TodoID,
		"issue_number": item.IssueNumber,
		"title":        item.Title,
		"status":       string(item.Status),
		"created_by":   item.CreatedBy,
		"priority":     string(item.Priority),
		"version":      item.Version,
	}
	if item.Source != nil {
		out["source"] = map[string]any{"conversation_id": item.Source.ConversationID, "delivery_status": item.Source.DeliveryStatus, "result": item.Source.Result, "result_by": item.Source.ResultBy}
	}
	if item.Feedback != nil {
		out["feedback"] = item.Feedback
	}
	if len(item.Attachments) > 0 {
		out["attachments"] = item.Attachments
	}
	if item.Description != "" {
		out["description"] = item.Description
	}
	if item.ProposerName != "" {
		out["proposer_name"] = item.ProposerName
	}
	if len(item.Tags) > 0 {
		out["tags"] = append([]string(nil), item.Tags...)
	}
	if item.AssigneeID != "" {
		out["assignee_id"] = item.AssigneeID
	}
	if item.AssigneeName != "" {
		out["assignee_name"] = item.AssigneeName
	}
	if item.AssigneeKind != "" {
		out["assignee_kind"] = string(item.AssigneeKind)
	}
	if item.TenantID != "" {
		out["tenant_id"] = item.TenantID
	}
	if item.OrganizationID != "" {
		out["organization_id"] = item.OrganizationID
	}
	if item.DepartmentID != "" {
		out["department_id"] = item.DepartmentID
	}
	if item.ProjectID != "" {
		out["project_id"] = item.ProjectID
	}
	if item.SourceIdeaID != "" {
		out["source_idea_id"] = item.SourceIdeaID
	}
	if item.ReviewerID != "" {
		out["reviewer_id"] = item.ReviewerID
		out["reviewer_name"] = item.ReviewerName
	}
	if item.AcceptanceCriteria != "" {
		out["acceptance_criteria"] = item.AcceptanceCriteria
	}
	if item.BlockedReason != "" {
		out["blocked_reason"] = item.BlockedReason
	}
	if !item.DueAt.IsZero() {
		out["due_at"] = item.DueAt.UTC().Format(time.RFC3339)
	}
	if !item.CreatedAt.IsZero() {
		out["created_at"] = item.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !item.UpdatedAt.IsZero() {
		out["updated_at"] = item.UpdatedAt.UTC().Format(time.RFC3339)
	}
	if !item.CompletedAt.IsZero() {
		out["completed_at"] = item.CompletedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func todoListOutput(page TodoPage) map[string]any {
	items := make([]map[string]any, 0, len(page.Todos))
	for _, item := range page.Todos {
		items = append(items, todoOutput(item))
	}
	return map[string]any{
		"todos":  items,
		"total":  page.Total,
		"limit":  page.Limit,
		"offset": page.Offset,
	}
}

func ideaOutput(item IdeaItem) map[string]any {
	out := map[string]any{
		"idea_id":          item.IdeaID,
		"issue_number":     item.IssueNumber,
		"title":            item.Title,
		"status":           string(item.Status),
		"created_by":       item.CreatedBy,
		"visibility":       string(item.Visibility),
		"impact":           string(item.Impact),
		"effort":           string(item.Effort),
		"vote_count":       item.VoteCount,
		"viewer_has_voted": item.ViewerHasVoted,
		"version":          item.Version,
	}
	if item.TenantID != "" {
		out["tenant_id"] = item.TenantID
	}
	if item.Content != "" {
		out["content"] = item.Content
	}
	if item.ProposerName != "" {
		out["proposer_name"] = item.ProposerName
	}
	if len(item.Tags) > 0 {
		out["tags"] = append([]string(nil), item.Tags...)
	}
	if item.ConvertedTodoID != "" {
		out["converted_todo_id"] = item.ConvertedTodoID
	}
	if item.OrganizationID != "" {
		out["organization_id"] = item.OrganizationID
	}
	if item.DepartmentID != "" {
		out["department_id"] = item.DepartmentID
	}
	if item.OwnerID != "" {
		out["owner_id"] = item.OwnerID
		out["owner_name"] = item.OwnerName
	}
	if item.ReviewerID != "" {
		out["reviewer_id"] = item.ReviewerID
		out["reviewer_name"] = item.ReviewerName
	}
	if item.DecisionReason != "" {
		out["decision_reason"] = item.DecisionReason
	}
	if !item.CreatedAt.IsZero() {
		out["created_at"] = item.CreatedAt.UTC().Format(time.RFC3339)
	}
	if !item.UpdatedAt.IsZero() {
		out["updated_at"] = item.UpdatedAt.UTC().Format(time.RFC3339)
	}
	if !item.SubmittedAt.IsZero() {
		out["submitted_at"] = item.SubmittedAt.UTC().Format(time.RFC3339)
	}
	if !item.ReviewedAt.IsZero() {
		out["reviewed_at"] = item.ReviewedAt.UTC().Format(time.RFC3339)
	}
	if !item.ArchivedAt.IsZero() {
		out["archived_at"] = item.ArchivedAt.UTC().Format(time.RFC3339)
	}
	if !item.ConvertedAt.IsZero() {
		out["converted_at"] = item.ConvertedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func ideaListOutput(page IdeaPage) map[string]any {
	items := make([]map[string]any, 0, len(page.Ideas))
	for _, item := range page.Ideas {
		items = append(items, ideaOutput(item))
	}
	return map[string]any{
		"ideas":  items,
		"total":  page.Total,
		"limit":  page.Limit,
		"offset": page.Offset,
	}
}
