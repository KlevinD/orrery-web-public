package todo

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Service) CreateTodo(ctx context.Context, viewer ViewerRef, req CreateTodoRequest) (TodoItem, error) {
	viewer, err := normalizeViewer(viewer)
	if err != nil {
		return TodoItem{}, err
	}
	if s == nil || s.clock == nil || s.ids == nil || s.repo == nil {
		return TodoItem{}, ErrServiceDependency
	}
	if s.access != nil && !hasPermission(viewer, "todo.write") {
		return TodoItem{}, ErrForbidden
	}
	if s.access != nil && s.relations == nil {
		return TodoItem{}, ErrServiceDependency
	}
	if err := ctx.Err(); err != nil {
		return TodoItem{}, err
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return TodoItem{}, ErrInvalidRequest
	}
	status := normalizeTodoStatus(req.Status)
	if status == "" {
		status = TodoStatusInbox
	}
	if !isTodoStatus(status) || !isInitialTodoStatus(status) {
		return TodoItem{}, ErrInvalidRequest
	}
	priority := req.Priority
	if priority == "" {
		priority = TodoPriorityMedium
	}
	workKind := normalizeTodoWorkKind(req.WorkKind)
	if workKind == "" {
		workKind = TodoWorkKindGeneral
	}
	automationMode := normalizeTodoAutomationMode(req.AutomationMode)
	if automationMode == "" {
		automationMode = TodoAutomationModeManual
	}
	if !isTodoPriority(priority) || !isAssigneeKind(req.AssigneeKind) || !isTodoWorkKind(workKind) || !isTodoAutomationMode(automationMode) {
		return TodoItem{}, ErrInvalidRequest
	}
	developmentProjectID := strings.TrimSpace(req.DevelopmentProjectID)
	if workKind == TodoWorkKindGeneral && developmentProjectID != "" {
		return TodoItem{}, ErrInvalidRequest
	}
	if developmentProjectID != "" {
		if err := s.ensureTodoDevelopmentProjectBindingAllowed(ctx, viewer, developmentProjectID); err != nil {
			return TodoItem{}, err
		}
	}
	id, err := s.ids.NewIDContext()
	if err != nil {
		return TodoItem{}, err
	}
	now := s.clock.Now().UTC()
	item := TodoItem{
		TenantID:             viewer.TenantID,
		OrganizationID:       firstNonEmptyText(req.OrganizationID, viewer.OrganizationID),
		DepartmentID:         firstNonEmptyText(req.DepartmentID, viewer.DepartmentID),
		ProjectID:            strings.TrimSpace(req.ProjectID),
		WorkKind:             workKind,
		AutomationMode:       automationMode,
		DevelopmentProjectID: developmentProjectID,
		SourceIdeaID:         strings.TrimSpace(req.SourceIdeaID),
		TodoID:               id,
		IssueNumber:          issueNumberFromID("task", id),
		Title:                title,
		Description:          strings.TrimSpace(req.Description),
		Status:               status,
		Priority:             priority,
		ProposerName:         viewerDisplayName(viewer),
		Tags:                 normalizeTags(req.Tags),
		AssigneeID:           strings.TrimSpace(req.AssigneeID),
		AssigneeName:         strings.TrimSpace(req.AssigneeName),
		AssigneeKind:         normalizedAssigneeKind(req.AssigneeKind, req.AssigneeID),
		ReviewerID:           strings.TrimSpace(req.ReviewerID),
		ReviewerName:         strings.TrimSpace(req.ReviewerName),
		AcceptanceCriteria:   strings.TrimSpace(req.AcceptanceCriteria),
		CreatedBy:            viewer.ViewerID,
		CreatedByKind:        viewer.ViewerKind,
		CreatedBySubjectID:   viewer.AuthorizationSubjectID,
		CreatedAt:            now,
		UpdatedAt:            now,
		DueAt:                utcTime(req.DueAt),
		Version:              1,
	}
	if err := s.prepareTodoSource(ctx, &item, req.SourceConversationID); err != nil {
		return TodoItem{}, err
	}
	if s.access == nil {
		if err := s.repo.CreateTodo(ctx, item); err != nil {
			return TodoItem{}, err
		}
		s.notifyChanged()
		return s.deliverTodoSource(ctx, item), nil
	}
	relations := todoRelations(item)
	if err := s.writeTodoRelations(ctx, item); err != nil {
		if cleanupErr := s.relations.DeleteTodoRelations(context.WithoutCancel(ctx), item.TodoID, item.TenantID, relations); cleanupErr != nil {
			return TodoItem{}, errors.Join(err, cleanupErr)
		}
		return TodoItem{}, err
	}
	if err := s.repo.CreateTodo(ctx, item); err != nil {
		if cleanupErr := s.relations.DeleteTodoRelations(context.WithoutCancel(ctx), item.TodoID, item.TenantID, relations); cleanupErr != nil {
			return TodoItem{}, errors.Join(err, cleanupErr)
		}
		return TodoItem{}, err
	}
	s.notifyChanged()
	return s.deliverTodoSource(ctx, item), nil
}

func (s *Service) GetTodo(ctx context.Context, viewer ViewerRef, todoID string) (TodoItem, error) {
	viewer, err := normalizeViewer(viewer)
	if err != nil {
		return TodoItem{}, err
	}
	if s == nil || s.repo == nil {
		return TodoItem{}, ErrServiceDependency
	}
	item, err := s.repo.GetTodoForTenant(ctx, viewer.TenantID, strings.TrimSpace(todoID))
	if err != nil {
		return TodoItem{}, err
	}
	if err := s.authorizeTodo(ctx, viewer, item, "todo.view"); err != nil {
		return TodoItem{}, err
	}
	if item.Source != nil && s.sourceDiscussion != nil {
		source := *item.Source
		_, accessErr := s.sourceDiscussion.GetConversation(ctx, sourceViewer(TodoItem{TenantID: viewer.TenantID, CreatedBy: viewer.ViewerID, CreatedBySubjectID: viewer.AuthorizationSubjectID, CreatedByKind: viewer.ViewerKind}), source.ConversationID)
		source.CanOpen = accessErr == nil
		item.Source = &source
	}
	return s.attachTodoExecutionProgress(ctx, viewer, item)
}

func (s *Service) UpdateTodo(ctx context.Context, viewer ViewerRef, req UpdateTodoRequest) (TodoItem, error) {
	viewer, item, err := s.todoForMutation(ctx, viewer, req.TodoID)
	if err != nil {
		return TodoItem{}, err
	}
	previous := item
	expected := expectedVersion(req.Version, item.Version)
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			return TodoItem{}, ErrInvalidRequest
		}
		item.Title = title
	}
	if req.Description != nil {
		item.Description = strings.TrimSpace(*req.Description)
	}
	if req.Priority != nil {
		if !isTodoPriority(*req.Priority) {
			return TodoItem{}, ErrInvalidRequest
		}
		item.Priority = *req.Priority
	}
	if req.OrganizationID != nil {
		item.OrganizationID = strings.TrimSpace(*req.OrganizationID)
	}
	if req.DepartmentID != nil {
		item.DepartmentID = strings.TrimSpace(*req.DepartmentID)
	}
	if req.ProjectID != nil {
		item.ProjectID = strings.TrimSpace(*req.ProjectID)
	}
	if req.WorkKind != nil {
		value := normalizeTodoWorkKind(*req.WorkKind)
		if !isTodoWorkKind(value) {
			return TodoItem{}, ErrInvalidRequest
		}
		item.WorkKind = value
	}
	if req.AutomationMode != nil {
		value := normalizeTodoAutomationMode(*req.AutomationMode)
		if !isTodoAutomationMode(value) {
			return TodoItem{}, ErrInvalidRequest
		}
		item.AutomationMode = value
	}
	if req.DevelopmentProjectID != nil {
		item.DevelopmentProjectID = strings.TrimSpace(*req.DevelopmentProjectID)
	}
	if item.WorkKind == TodoWorkKindGeneral {
		item.DevelopmentProjectID = ""
	}
	if req.AssigneeID != nil {
		item.AssigneeID = strings.TrimSpace(*req.AssigneeID)
	}
	if req.AssigneeName != nil {
		item.AssigneeName = strings.TrimSpace(*req.AssigneeName)
	}
	if req.AssigneeKind != nil {
		if !isAssigneeKind(*req.AssigneeKind) {
			return TodoItem{}, ErrInvalidRequest
		}
		item.AssigneeKind = *req.AssigneeKind
	}
	if item.AssigneeID == "" {
		item.AssigneeName = ""
		item.AssigneeKind = ""
	} else if item.AssigneeKind == "" {
		item.AssigneeKind = AssigneeKindHuman
	}
	if req.ReviewerID != nil {
		item.ReviewerID = strings.TrimSpace(*req.ReviewerID)
	}
	if req.ReviewerName != nil {
		item.ReviewerName = strings.TrimSpace(*req.ReviewerName)
	}
	if req.ClearDueAt {
		item.DueAt = time.Time{}
	} else if req.DueAt != nil {
		item.DueAt = utcTime(*req.DueAt)
	}
	if req.AcceptanceCriteria != nil {
		item.AcceptanceCriteria = strings.TrimSpace(*req.AcceptanceCriteria)
	}
	if req.Tags != nil {
		item.Tags = normalizeTags(*req.Tags)
	}
	if err := validateTodoLifecycleFields(item); err != nil {
		return TodoItem{}, err
	}
	if item.DevelopmentProjectID != "" && item.DevelopmentProjectID != previous.DevelopmentProjectID {
		if err := s.ensureTodoDevelopmentProjectBindingAllowed(ctx, viewer, item.DevelopmentProjectID); err != nil {
			return TodoItem{}, err
		}
	}
	item.UpdatedAt = s.clock.Now().UTC()
	item.Version++
	if err := s.persistTodoMutation(ctx, viewer, previous, item, expected, true); err != nil {
		return TodoItem{}, err
	}
	return item, nil
}

func (s *Service) ensureTodoDevelopmentProjectBindingAllowed(ctx context.Context, viewer ViewerRef, projectID string) error {
	if s == nil || s.developmentExecution == nil {
		return ErrServiceDependency
	}
	return s.developmentExecution.EnsureTodoBindingAllowed(ctx, TodoDevelopmentExecutionAuthorization{
		TenantID: viewer.TenantID, DevelopmentProjectID: strings.TrimSpace(projectID),
		RequesterID: viewer.ViewerID, RequesterKind: viewer.ViewerKind, RequesterSubjectID: viewer.AuthorizationSubjectID,
		RequesterScopes: append([]string(nil), viewer.PermissionScope...),
	})
}

func (s *Service) ClaimTodo(ctx context.Context, viewer ViewerRef, req ClaimTodoRequest) (TodoItem, error) {
	viewer, item, err := s.todoForMutation(ctx, viewer, req.TodoID)
	if err != nil {
		return TodoItem{}, err
	}
	previous := item
	expected := expectedVersion(req.Version, item.Version)
	assigneeID := strings.TrimSpace(req.AssigneeID)
	if assigneeID == "" {
		assigneeID = viewer.ViewerID
	}
	assigneeName := strings.TrimSpace(req.AssigneeName)
	if assigneeName == "" && assigneeID == viewer.ViewerID {
		assigneeName = viewerDisplayName(viewer)
	}
	kind := normalizedAssigneeKind(req.AssigneeKind, assigneeID)
	if !isAssigneeKind(kind) {
		return TodoItem{}, ErrInvalidRequest
	}
	item.AssigneeID = assigneeID
	item.AssigneeName = assigneeName
	item.AssigneeKind = kind
	item.UpdatedAt = s.clock.Now().UTC()
	item.Version++
	if err := s.persistTodoMutation(ctx, viewer, previous, item, expected, true); err != nil {
		return TodoItem{}, err
	}
	return item, nil
}

func (s *Service) MoveTodo(ctx context.Context, viewer ViewerRef, req MoveTodoRequest) (TodoItem, error) {
	viewer, item, err := s.todoForMutation(ctx, viewer, req.TodoID)
	if err != nil {
		return TodoItem{}, err
	}
	previous := item
	status := normalizeTodoStatus(req.Status)
	// Human feedback is a single completion action, including newly received or blocked work.
	humanCompletion := humanManualTodo(item) && !isTerminalTodoStatus(item.Status) && status == TodoStatusDone &&
		(viewer.ViewerKind == ViewerKindUser || viewer.ViewerKind == ViewerKindHuman) && (strings.TrimSpace(req.Result) != "" || len(req.ResultAttachmentIDs) > 0)
	if !isTodoStatus(status) || (!canTransitionTodo(item.Status, status) && !humanCompletion) {
		return TodoItem{}, ErrInvalidTransition
	}
	if (item.Source != nil || item.Feedback != nil) && status == item.Status {
		return TodoItem{}, ErrInvalidTransition
	}
	blockedReason := strings.TrimSpace(req.BlockedReason)
	if status == TodoStatusBlocked && blockedReason == "" {
		return TodoItem{}, ErrInvalidRequest
	}
	expected := expectedVersion(req.Version, item.Version)
	now := s.clock.Now().UTC()
	item.Status = status
	if status == TodoStatusBlocked {
		item.BlockedReason = blockedReason
	} else {
		item.BlockedReason = ""
	}
	if isTerminalTodoStatus(status) {
		item.CompletedAt = now
	} else {
		item.CompletedAt = time.Time{}
	}
	item.UpdatedAt = now
	item.Version++
	if err := s.prepareFeedback(viewer, &item, req); err != nil {
		return TodoItem{}, err
	}
	if err := s.prepareSourceTransition(viewer, &item, status, req.Result); err != nil {
		return TodoItem{}, err
	}
	if err := s.persistTodoMutation(ctx, viewer, previous, item, expected, false); err != nil {
		return TodoItem{}, err
	}
	return s.deliverTodoSource(ctx, item), nil
}

func (s *Service) persistTodoMutation(ctx context.Context, viewer ViewerRef, previous, next TodoItem, expectedVersion int64, updateRelations bool) error {
	if next.Source != nil && (next.AssigneeKind != AssigneeKindHuman || next.AssigneeID == "" || next.AutomationMode != TodoAutomationModeManual || next.WorkKind != TodoWorkKindGeneral) {
		return ErrInvalidRequest
	}
	if s == nil || s.repo == nil || s.clock == nil {
		return ErrServiceDependency
	}
	if current := previous.Execution; current != nil && current.Current != nil && current.Current.MissionID != "" && !todoMissionBindingTerminal(current.Current.Status) && s.missionPause == nil {
		return ErrServiceDependency
	}
	var result todoMutationResult
	persist := func() error {
		var err error
		result, err = s.repo.UpdateTodoAndInvalidateAutomation(ctx, next, expectedVersion, next.UpdatedAt)
		return err
	}
	var err error
	if updateRelations {
		err = s.updateTodoRelations(ctx, previous, next, persist)
	} else {
		err = persist()
	}
	if err != nil {
		return err
	}
	if result.Automation != nil {
		s.recordTodoAutomationAudit(ctx, *result.Automation, viewer.ViewerID, "todo.automation.invalidated_by_todo_change", "succeeded")
		s.recordTodoAutomationAudit(ctx, *result.Automation, viewer.ViewerID, "todo.execution_plan.stale", "succeeded")
	}
	s.notifyChanged()
	if result.PauseRequest.MissionID == "" {
		return nil
	}
	if err := s.pauseTodoMission(ctx, result.PauseRequest); err != nil {
		if result.Automation != nil {
			s.recordTodoAutomationAudit(ctx, *result.Automation, viewer.ViewerID, "todo.automation.mission_pause_requested", "failed")
		}
		return nil
	}
	if result.Automation != nil {
		s.recordTodoAutomationAudit(ctx, *result.Automation, viewer.ViewerID, "todo.automation.mission_paused", "succeeded")
	}
	return nil
}

func (s *Service) pauseTodoMission(ctx context.Context, request TodoMissionPauseRequest) error {
	if s == nil || s.missionPause == nil || s.repo == nil {
		return ErrServiceDependency
	}
	if err := s.missionPause.PauseMissionForTodo(ctx, request); err != nil {
		return err
	}
	return s.repo.CompleteTodoMissionPause(context.WithoutCancel(ctx), request, s.clock.Now().UTC())
}

func (s *Service) CompleteTodo(ctx context.Context, viewer ViewerRef, req CompleteTodoRequest) (TodoItem, error) {
	return s.MoveTodo(ctx, viewer, MoveTodoRequest{TodoID: req.TodoID, Status: TodoStatusDone, Version: req.Version, Result: req.Result, ResultAttachmentIDs: req.ResultAttachmentIDs})
}

func (s *Service) CancelTodo(ctx context.Context, viewer ViewerRef, req CancelTodoRequest) (TodoItem, error) {
	return s.MoveTodo(ctx, viewer, MoveTodoRequest{TodoID: req.TodoID, Status: TodoStatusCanceled, Version: req.Version})
}

func (s *Service) ReopenTodo(ctx context.Context, viewer ViewerRef, req ReopenTodoRequest) (TodoItem, error) {
	item, err := s.GetTodo(ctx, viewer, req.TodoID)
	if err != nil {
		return TodoItem{}, err
	}
	var status TodoStatus
	switch item.Status {
	case TodoStatusDone:
		status = TodoStatusPlanned
	case TodoStatusCanceled:
		status = TodoStatusInbox
	default:
		return TodoItem{}, ErrInvalidTransition
	}
	return s.MoveTodo(ctx, viewer, MoveTodoRequest{TodoID: req.TodoID, Status: status, Version: req.Version})
}

func (s *Service) DeleteTodo(ctx context.Context, viewer ViewerRef, req DeleteTodoRequest) error {
	viewer, item, err := s.todoForMutation(ctx, viewer, req.TodoID)
	if err != nil {
		return err
	}
	if item.Status != TodoStatusInbox || (item.CreatedBy != viewer.ViewerID && !hasPermission(viewer, "todo.manage")) {
		return ErrForbidden
	}
	if item.Source != nil || item.DiscussionBinding != nil || (item.Execution != nil && (item.Execution.Current != nil || len(item.Execution.Attempts) > 0)) {
		return fmt.Errorf("%w: Todo has discussion or execution history; cancel or archive it instead", ErrConflict)
	}
	if s.access == nil {
		return s.repo.DeleteTodoForTenant(ctx, viewer.TenantID, item.TodoID, expectedVersion(req.Version, item.Version))
	}
	if s.relations == nil {
		return ErrServiceDependency
	}
	relations := todoRelations(item)
	if err := s.relations.DeleteTodoRelations(ctx, item.TodoID, item.TenantID, relations); err != nil {
		return err
	}
	if err := s.repo.DeleteTodoForTenant(ctx, viewer.TenantID, item.TodoID, expectedVersion(req.Version, item.Version)); err != nil {
		if restoreErr := s.relations.WriteTodoRelations(context.WithoutCancel(ctx), item.TodoID, item.TenantID, relations); restoreErr != nil {
			return errors.Join(err, restoreErr)
		}
		return err
	}
	return nil
}

func (s *Service) ListTodos(ctx context.Context, viewer ViewerRef, query ListTodosQuery) (TodoPage, error) {
	viewer, err := normalizeViewer(viewer)
	if err != nil {
		return TodoPage{}, err
	}
	if s == nil || s.repo == nil {
		return TodoPage{}, ErrServiceDependency
	}
	query.Status = normalizeTodoStatus(query.Status)
	if query.Status != "" && !isTodoStatus(query.Status) {
		return TodoPage{}, ErrInvalidRequest
	}
	if query.Priority != "" && !isTodoPriority(query.Priority) {
		return TodoPage{}, ErrInvalidRequest
	}
	if !validListSearch(query.Q) {
		return TodoPage{}, ErrInvalidRequest
	}
	if s.access != nil {
		if err := s.authorizeTodoListQuery(ctx, viewer, &query); err != nil {
			return TodoPage{}, err
		}
	}
	page, err := s.repo.QueryTodos(ctx, viewer, query)
	if err != nil {
		return TodoPage{}, err
	}
	for index := range page.Todos {
		page.Todos[index], err = s.attachTodoExecutionProgress(ctx, viewer, page.Todos[index])
		if err != nil {
			return TodoPage{}, err
		}
	}
	return page, nil
}

func (s *Service) GetTodoExecutionProgress(ctx context.Context, viewer ViewerRef, todoID string) (TodoExecutionSummary, error) {
	item, err := s.GetTodo(ctx, viewer, todoID)
	if err != nil {
		return TodoExecutionSummary{}, err
	}
	if item.Execution == nil {
		return TodoExecutionSummary{Attempts: []TodoExecutionAttempt{}}, nil
	}
	return *item.Execution, nil
}

func (s *Service) GetTodoExecutionPlans(ctx context.Context, viewer ViewerRef, todoID string) (TodoExecutionPlanPage, error) {
	item, err := s.GetTodo(ctx, viewer, todoID)
	if err != nil {
		return TodoExecutionPlanPage{}, err
	}
	plans, err := s.repo.ListTodoExecutionPlans(ctx, item.TenantID, item.TodoID)
	if err != nil {
		return TodoExecutionPlanPage{}, err
	}
	if len(plans) == 0 && item.Automation != nil && item.Automation.Plan.PlanID != "" && item.Automation.PlanDigest != "" {
		plans = append(plans, TodoExecutionPlanView{
			Plan: item.Automation.Plan, PlanDigest: item.Automation.PlanDigest, LifecycleStatus: item.Automation.Status,
		})
	}
	if item.Automation != nil {
		for index := range plans {
			if plans[index].Plan.PlanID == item.Automation.Plan.PlanID && plans[index].PlanDigest == item.Automation.PlanDigest {
				plans[index].LifecycleStatus = item.Automation.Status
			}
		}
	}
	if plans == nil {
		plans = []TodoExecutionPlanView{}
	}
	return TodoExecutionPlanPage{ExecutionPlans: plans}, nil
}

func (s *Service) attachTodoExecutionProgress(ctx context.Context, viewer ViewerRef, item TodoItem) (TodoItem, error) {
	if item.Execution == nil || s.executionProgress == nil {
		return item, nil
	}
	visible := make([]TodoExecutionAttempt, 0, len(item.Execution.Attempts))
	for _, attempt := range item.Execution.Attempts {
		if strings.TrimSpace(attempt.MissionID) == "" {
			visible = append(visible, attempt)
			continue
		}
		progress, allowed, err := s.executionProgress.GetTodoExecutionProgress(ctx, viewer, attempt)
		if err != nil {
			return TodoItem{}, err
		}
		if !allowed {
			continue
		}
		attempt.Progress = &progress
		visible = append(visible, attempt)
		if item.Execution.Current != nil &&
			item.Execution.Current.ExecutionAttempt == attempt.ExecutionAttempt &&
			item.Execution.Current.MissionID == attempt.MissionID {
			item.Execution.CurrentProgress = &progress
		}
	}
	item.Execution.Attempts = visible
	if item.Execution.Current != nil && strings.TrimSpace(item.Execution.Current.MissionID) != "" && item.Execution.CurrentProgress == nil {
		item.Execution.Current = nil
	}
	return item, nil
}

func (s *Service) authorizeTodoListQuery(ctx context.Context, viewer ViewerRef, query *ListTodosQuery) error {
	if s == nil || s.access == nil {
		return nil
	}
	if query == nil || !hasPermission(viewer, "todo.read") {
		return ErrForbidden
	}
	if isBreakglassTodoViewer(viewer) {
		return nil
	}
	objects, err := s.access.ListObjects(ctx, todoPrincipal(viewer), "reader", "todo", viewer.TenantID)
	if err != nil {
		return err
	}
	query.authorizationFiltered = true
	query.authorizedIDs = make([]string, 0, len(objects))
	for _, object := range objects {
		if object.Type == "todo" && normalizedTenantID(object.TenantID) == viewer.TenantID && validTodoIdentity(object.ID) {
			query.authorizedIDs = append(query.authorizedIDs, object.ID)
		}
	}
	return nil
}

func (s *Service) todoForMutation(ctx context.Context, viewer ViewerRef, todoID string) (ViewerRef, TodoItem, error) {
	viewer, err := normalizeViewer(viewer)
	if err != nil {
		return ViewerRef{}, TodoItem{}, err
	}
	if s == nil || s.clock == nil || s.repo == nil {
		return ViewerRef{}, TodoItem{}, ErrServiceDependency
	}
	item, err := s.repo.GetTodoForTenant(ctx, viewer.TenantID, strings.TrimSpace(todoID))
	if err != nil {
		return ViewerRef{}, TodoItem{}, err
	}
	if err := s.authorizeTodo(ctx, viewer, item, "todo.edit"); err != nil {
		return ViewerRef{}, TodoItem{}, err
	}
	return viewer, item, nil
}

func filterTodos(items []TodoItem, query ListTodosQuery) []TodoItem {
	q := strings.ToLower(strings.TrimSpace(query.Q))
	out := make([]TodoItem, 0, len(items))
	for _, item := range items {
		if query.Status != "" && item.Status != query.Status {
			continue
		}
		if !query.IncludeCompleted && query.Status == "" && isTerminalTodoStatus(item.Status) {
			continue
		}
		if query.Priority != "" && item.Priority != query.Priority {
			continue
		}
		if strings.TrimSpace(query.OrganizationID) != "" && item.OrganizationID != strings.TrimSpace(query.OrganizationID) {
			continue
		}
		if strings.TrimSpace(query.DepartmentID) != "" && item.DepartmentID != strings.TrimSpace(query.DepartmentID) {
			continue
		}
		if strings.TrimSpace(query.ProjectID) != "" && item.ProjectID != strings.TrimSpace(query.ProjectID) {
			continue
		}
		if strings.TrimSpace(query.SourceIdeaID) != "" && item.SourceIdeaID != strings.TrimSpace(query.SourceIdeaID) {
			continue
		}
		if strings.TrimSpace(query.AssigneeID) != "" && item.AssigneeID != strings.TrimSpace(query.AssigneeID) {
			continue
		}
		if strings.TrimSpace(query.CreatedBy) != "" && item.CreatedBy != strings.TrimSpace(query.CreatedBy) {
			continue
		}
		if !query.DueBefore.IsZero() && (item.DueAt.IsZero() || !item.DueAt.Before(query.DueBefore)) {
			continue
		}
		if q != "" && !matchesSearchText(todoSearchText(item), q) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func todoSearchText(item TodoItem) string {
	return strings.ToLower(strings.Join([]string{
		item.TodoID, item.IssueNumber, item.Title, item.Description, string(item.Status), string(item.Priority),
		item.ProposerName, strings.Join(item.Tags, " "), item.AssigneeID, item.AssigneeName,
		item.ReviewerID, item.ReviewerName, item.AcceptanceCriteria, item.BlockedReason,
		item.OrganizationID, item.DepartmentID, item.ProjectID, item.SourceIdeaID, item.CreatedBy,
		string(item.WorkKind), string(item.AutomationMode), item.DevelopmentProjectID,
	}, " "))
}

func validateTodoLifecycleFields(item TodoItem) error {
	if !isTodoWorkKind(normalizeTodoWorkKind(item.WorkKind)) || !isTodoAutomationMode(normalizeTodoAutomationMode(item.AutomationMode)) {
		return ErrInvalidRequest
	}
	if item.WorkKind == TodoWorkKindGeneral && strings.TrimSpace(item.DevelopmentProjectID) != "" {
		return ErrInvalidRequest
	}
	blockedReason := strings.TrimSpace(item.BlockedReason)
	if item.Status == TodoStatusBlocked {
		if blockedReason == "" {
			return ErrInvalidRequest
		}
		return nil
	}
	if blockedReason != "" {
		return ErrInvalidRequest
	}
	return nil
}

func normalizedAssigneeKind(kind AssigneeKind, assigneeID string) AssigneeKind {
	if strings.TrimSpace(assigneeID) == "" {
		return ""
	}
	if kind == "" {
		return AssigneeKindHuman
	}
	return kind
}

func utcTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Time{}
	}
	return value.UTC()
}

func firstNonEmptyText(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func paginate[T any](items []T, requestedLimit, requestedOffset int) ([]T, int, int, int) {
	total := len(items)
	offset := requestedOffset
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	limit := requestedLimit
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	end := offset + limit
	if end > total {
		end = total
	}
	return items[offset:end], total, limit, offset
}

func matchesSearchText(text, query string) bool {
	for _, term := range strings.Fields(query) {
		if !strings.Contains(text, term) {
			return false
		}
	}
	return true
}
