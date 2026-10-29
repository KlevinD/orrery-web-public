package todo

import (
	"context"
	"strings"
	"time"
)

func (s *Service) CreateIdea(ctx context.Context, viewer ViewerRef, req CreateIdeaRequest) (IdeaItem, error) {
	viewer, err := normalizeViewer(viewer)
	if err != nil {
		return IdeaItem{}, err
	}
	if s == nil || s.clock == nil || s.ideaIDs == nil || s.repo == nil {
		return IdeaItem{}, ErrServiceDependency
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		return IdeaItem{}, ErrInvalidRequest
	}
	status := normalizeIdeaStatus(req.Status)
	if status == "" {
		status = IdeaStatusSubmitted
	}
	if status != IdeaStatusDraft && status != IdeaStatusSubmitted {
		return IdeaItem{}, ErrInvalidRequest
	}
	visibility := req.Visibility
	if visibility == "" {
		visibility = IdeaVisibilityOrganization
	}
	impact := req.Impact
	if impact == "" {
		impact = IdeaImpactMedium
	}
	effort := req.Effort
	if effort == "" {
		effort = IdeaEffortMedium
	}
	if !isIdeaVisibility(visibility) || !isIdeaImpact(impact) || !isIdeaEffort(effort) {
		return IdeaItem{}, ErrInvalidRequest
	}
	id, err := s.ideaIDs.NewIDContext()
	if err != nil {
		return IdeaItem{}, err
	}
	now := s.clock.Now().UTC()
	item := IdeaItem{
		TenantID:       viewer.TenantID,
		OrganizationID: firstNonEmptyText(req.OrganizationID, viewer.OrganizationID),
		DepartmentID:   firstNonEmptyText(req.DepartmentID, viewer.DepartmentID),
		IdeaID:         id,
		IssueNumber:    issueNumberFromID("idea", id),
		Title:          title,
		Content:        strings.TrimSpace(req.Content),
		Status:         status,
		Visibility:     visibility,
		Impact:         impact,
		Effort:         effort,
		ProposerName:   viewerDisplayName(viewer),
		Tags:           normalizeTags(req.Tags),
		OwnerID:        firstNonEmptyText(req.OwnerID, viewer.ViewerID),
		OwnerName:      firstNonEmptyText(req.OwnerName, viewerDisplayName(viewer)),
		CreatedBy:      viewer.ViewerID,
		CreatedAt:      now,
		UpdatedAt:      now,
		Version:        1,
	}
	if status == IdeaStatusSubmitted {
		item.SubmittedAt = now
	}
	if err := s.repo.CreateIdea(ctx, item); err != nil {
		return IdeaItem{}, err
	}
	return ideaForViewer(item, viewer), nil
}

func (s *Service) GetIdea(ctx context.Context, viewer ViewerRef, ideaID string) (IdeaItem, error) {
	viewer, err := normalizeViewer(viewer)
	if err != nil {
		return IdeaItem{}, err
	}
	if s == nil || s.repo == nil {
		return IdeaItem{}, ErrServiceDependency
	}
	item, err := s.repo.GetIdeaForTenant(ctx, viewer.TenantID, strings.TrimSpace(ideaID))
	if err != nil {
		return IdeaItem{}, err
	}
	if !canReadIdea(viewer, item) {
		return IdeaItem{}, ErrForbidden
	}
	return ideaForViewer(item, viewer), nil
}

func (s *Service) UpdateIdea(ctx context.Context, viewer ViewerRef, req UpdateIdeaRequest) (IdeaItem, error) {
	viewer, item, err := s.ideaForMutation(ctx, viewer, req.IdeaID)
	if err != nil {
		return IdeaItem{}, err
	}
	expected := expectedVersion(req.Version, item.Version)
	if req.Title != nil {
		title := strings.TrimSpace(*req.Title)
		if title == "" {
			return IdeaItem{}, ErrInvalidRequest
		}
		item.Title = title
	}
	if req.Content != nil {
		item.Content = strings.TrimSpace(*req.Content)
	}
	if req.Visibility != nil {
		if !isIdeaVisibility(*req.Visibility) {
			return IdeaItem{}, ErrInvalidRequest
		}
		item.Visibility = *req.Visibility
	}
	if req.OrganizationID != nil {
		item.OrganizationID = strings.TrimSpace(*req.OrganizationID)
	}
	if req.DepartmentID != nil {
		item.DepartmentID = strings.TrimSpace(*req.DepartmentID)
	}
	if req.OwnerID != nil {
		item.OwnerID = strings.TrimSpace(*req.OwnerID)
	}
	if req.OwnerName != nil {
		item.OwnerName = strings.TrimSpace(*req.OwnerName)
	}
	if req.ReviewerID != nil {
		item.ReviewerID = strings.TrimSpace(*req.ReviewerID)
	}
	if req.ReviewerName != nil {
		item.ReviewerName = strings.TrimSpace(*req.ReviewerName)
	}
	if req.Impact != nil {
		if !isIdeaImpact(*req.Impact) {
			return IdeaItem{}, ErrInvalidRequest
		}
		item.Impact = *req.Impact
	}
	if req.Effort != nil {
		if !isIdeaEffort(*req.Effort) {
			return IdeaItem{}, ErrInvalidRequest
		}
		item.Effort = *req.Effort
	}
	if req.DecisionReason != nil {
		item.DecisionReason = strings.TrimSpace(*req.DecisionReason)
	}
	if req.Tags != nil {
		item.Tags = normalizeTags(*req.Tags)
	}
	item.UpdatedAt = s.clock.Now().UTC()
	item.Version++
	if err := s.repo.UpdateIdea(ctx, item, expected); err != nil {
		return IdeaItem{}, err
	}
	return ideaForViewer(item, viewer), nil
}

func (s *Service) TransitionIdea(ctx context.Context, viewer ViewerRef, req TransitionIdeaRequest) (IdeaItem, error) {
	viewer, item, err := s.ideaForMutation(ctx, viewer, req.IdeaID)
	if err != nil {
		return IdeaItem{}, err
	}
	status := normalizeIdeaStatus(req.Status)
	if status == IdeaStatusConverted || !isIdeaStatus(status) || !canTransitionIdea(item.Status, status) {
		return IdeaItem{}, ErrInvalidTransition
	}
	reason := strings.TrimSpace(req.DecisionReason)
	if ideaTransitionRequiresDecision(status) && reason == "" {
		return IdeaItem{}, ErrInvalidRequest
	}
	expected := expectedVersion(req.Version, item.Version)
	if strings.TrimSpace(req.ReviewerID) != "" {
		item.ReviewerID = strings.TrimSpace(req.ReviewerID)
		item.ReviewerName = strings.TrimSpace(req.ReviewerName)
	}
	now := s.clock.Now().UTC()
	applyIdeaTransition(&item, viewer, status, reason, now)
	item.Version++
	if err := s.repo.UpdateIdea(ctx, item, expected); err != nil {
		return IdeaItem{}, err
	}
	return ideaForViewer(item, viewer), nil
}

func (s *Service) VoteIdea(ctx context.Context, viewer ViewerRef, req VoteIdeaRequest) (IdeaItem, error) {
	viewer, item, err := s.ideaForMutation(ctx, viewer, req.IdeaID)
	if err != nil {
		return IdeaItem{}, err
	}
	if item.Status == IdeaStatusDraft || item.Status == IdeaStatusRejected || item.Status == IdeaStatusArchived || item.Status == IdeaStatusConverted {
		return IdeaItem{}, ErrInvalidTransition
	}
	voters := make([]string, 0, len(item.VoterIDs)+1)
	found := false
	for _, id := range item.VoterIDs {
		if id == viewer.ViewerID {
			found = true
			continue
		}
		voters = append(voters, id)
	}
	if !found {
		voters = append(voters, viewer.ViewerID)
	}
	item.VoterIDs = normalizeTags(voters)
	item.VoteCount = len(item.VoterIDs)
	item.ViewerHasVoted = !found
	item.UpdatedAt = s.clock.Now().UTC()
	expected := item.Version
	item.Version++
	if err := s.repo.UpdateIdea(ctx, item, expected); err != nil {
		return IdeaItem{}, err
	}
	return item, nil
}

func (s *Service) ArchiveIdea(ctx context.Context, viewer ViewerRef, req ArchiveIdeaRequest) (IdeaItem, error) {
	return s.TransitionIdea(ctx, viewer, TransitionIdeaRequest{IdeaID: req.IdeaID, Status: IdeaStatusArchived, Version: req.Version})
}

func (s *Service) RestoreIdea(ctx context.Context, viewer ViewerRef, req RestoreIdeaRequest) (IdeaItem, error) {
	return s.TransitionIdea(ctx, viewer, TransitionIdeaRequest{IdeaID: req.IdeaID, Status: IdeaStatusSubmitted, Version: req.Version})
}

func (s *Service) ConvertIdeaToTodo(ctx context.Context, viewer ViewerRef, req ConvertIdeaToTodoRequest) (IdeaConversionResult, error) {
	viewer, idea, err := s.ideaForMutation(ctx, viewer, req.IdeaID)
	if err != nil {
		return IdeaConversionResult{}, err
	}
	if !isConvertibleIdeaStatus(idea.Status) {
		return IdeaConversionResult{}, ErrInvalidTransition
	}
	decisionReason := firstNonEmptyText(req.DecisionReason, idea.DecisionReason)
	if decisionReason == "" {
		return IdeaConversionResult{}, ErrInvalidRequest
	}
	expected := expectedVersion(req.Version, idea.Version)
	taskID, err := s.ids.NewIDContext()
	if err != nil {
		return IdeaConversionResult{}, err
	}
	now := s.clock.Now().UTC()
	priority := req.Priority
	if priority == "" {
		priority = priorityFromIdea(idea)
	}
	if !isTodoPriority(priority) || !isAssigneeKind(req.AssigneeKind) {
		return IdeaConversionResult{}, ErrInvalidRequest
	}
	title := firstNonEmptyText(req.TodoTitle, idea.Title)
	description := firstNonEmptyText(req.TodoDescription, idea.Content)
	task := TodoItem{
		TenantID:           viewer.TenantID,
		OrganizationID:     firstNonEmptyText(req.OrganizationID, idea.OrganizationID, viewer.OrganizationID),
		DepartmentID:       firstNonEmptyText(req.DepartmentID, idea.DepartmentID, viewer.DepartmentID),
		ProjectID:          strings.TrimSpace(req.ProjectID),
		SourceIdeaID:       idea.IdeaID,
		TodoID:             taskID,
		IssueNumber:        issueNumberFromID("task", taskID),
		Title:              title,
		Description:        description,
		Status:             TodoStatusPlanned,
		Priority:           priority,
		ProposerName:       idea.ProposerName,
		Tags:               append([]string(nil), idea.Tags...),
		AssigneeID:         strings.TrimSpace(req.AssigneeID),
		AssigneeName:       strings.TrimSpace(req.AssigneeName),
		AssigneeKind:       normalizedAssigneeKind(req.AssigneeKind, req.AssigneeID),
		ReviewerID:         strings.TrimSpace(req.ReviewerID),
		ReviewerName:       strings.TrimSpace(req.ReviewerName),
		AcceptanceCriteria: strings.TrimSpace(req.AcceptanceCriteria),
		CreatedBy:          viewer.ViewerID,
		CreatedByKind:      viewer.ViewerKind,
		CreatedBySubjectID: viewer.AuthorizationSubjectID,
		CreatedAt:          now,
		UpdatedAt:          now,
		DueAt:              utcTime(req.DueAt),
		Version:            1,
	}
	applyIdeaTransition(&idea, viewer, IdeaStatusConverted, decisionReason, now)
	idea.ConvertedAt = now
	idea.ConvertedTodoID = task.TodoID
	idea.Version++
	if err := s.repo.ConvertIdea(ctx, idea, expected, task); err != nil {
		return IdeaConversionResult{}, err
	}
	return IdeaConversionResult{Idea: ideaForViewer(idea, viewer), Todo: task}, nil
}

func (s *Service) DeleteIdea(ctx context.Context, viewer ViewerRef, req DeleteIdeaRequest) error {
	viewer, item, err := s.ideaForMutation(ctx, viewer, req.IdeaID)
	if err != nil {
		return err
	}
	if item.Status != IdeaStatusDraft || (item.CreatedBy != viewer.ViewerID && !hasPermission(viewer, "idea.manage")) {
		return ErrForbidden
	}
	return s.repo.DeleteIdeaForTenant(ctx, viewer.TenantID, item.IdeaID, expectedVersion(req.Version, item.Version))
}

func (s *Service) ListIdeas(ctx context.Context, viewer ViewerRef, query ListIdeasQuery) (IdeaPage, error) {
	viewer, err := normalizeViewer(viewer)
	if err != nil {
		return IdeaPage{}, err
	}
	if s == nil || s.repo == nil {
		return IdeaPage{}, ErrServiceDependency
	}
	query.Status = normalizeIdeaStatus(query.Status)
	if query.Status != "" && !isIdeaStatus(query.Status) {
		return IdeaPage{}, ErrInvalidRequest
	}
	if query.Visibility != "" && !isIdeaVisibility(query.Visibility) {
		return IdeaPage{}, ErrInvalidRequest
	}
	if !validListSearch(query.Q) {
		return IdeaPage{}, ErrInvalidRequest
	}
	return s.repo.QueryIdeas(ctx, viewer, query)
}

func (s *Service) ideaForMutation(ctx context.Context, viewer ViewerRef, ideaID string) (ViewerRef, IdeaItem, error) {
	viewer, err := normalizeViewer(viewer)
	if err != nil {
		return ViewerRef{}, IdeaItem{}, err
	}
	if s == nil || s.clock == nil || s.repo == nil {
		return ViewerRef{}, IdeaItem{}, ErrServiceDependency
	}
	item, err := s.repo.GetIdeaForTenant(ctx, viewer.TenantID, strings.TrimSpace(ideaID))
	if err != nil {
		return ViewerRef{}, IdeaItem{}, err
	}
	if !canReadIdea(viewer, item) {
		return ViewerRef{}, IdeaItem{}, ErrForbidden
	}
	return viewer, item, nil
}

func canReadIdea(viewer ViewerRef, idea IdeaItem) bool {
	if normalizedTenantID(viewer.TenantID) != normalizedTenantID(idea.TenantID) {
		return false
	}
	if idea.CreatedBy == viewer.ViewerID || idea.OwnerID == viewer.ViewerID || idea.ReviewerID == viewer.ViewerID {
		return true
	}
	switch idea.Visibility {
	case IdeaVisibilityPrivate:
		return false
	case IdeaVisibilityDepartment:
		return viewer.DepartmentID != "" && viewer.DepartmentID == idea.DepartmentID && sameOrganizationScope(viewer, idea)
	case IdeaVisibilityOrganization:
		return idea.OrganizationID == "" || (viewer.OrganizationID != "" && viewer.OrganizationID == idea.OrganizationID)
	default:
		return false
	}
}

func sameOrganizationScope(viewer ViewerRef, idea IdeaItem) bool {
	return idea.OrganizationID == "" || (viewer.OrganizationID != "" && viewer.OrganizationID == idea.OrganizationID)
}

func ideaForViewer(item IdeaItem, viewer ViewerRef) IdeaItem {
	item.ViewerHasVoted = false
	for _, id := range item.VoterIDs {
		if id == viewer.ViewerID {
			item.ViewerHasVoted = true
			break
		}
	}
	item.VoteCount = len(item.VoterIDs)
	item.VoterIDs = nil
	return item
}

func applyIdeaTransition(item *IdeaItem, viewer ViewerRef, status IdeaStatus, reason string, now time.Time) {
	item.Status = status
	if reason = strings.TrimSpace(reason); reason != "" {
		item.DecisionReason = reason
	}
	item.UpdatedAt = now
	switch status {
	case IdeaStatusSubmitted:
		if item.SubmittedAt.IsZero() {
			item.SubmittedAt = now
		}
		item.ArchivedAt = time.Time{}
	case IdeaStatusUnderReview, IdeaStatusAccepted, IdeaStatusExperiment, IdeaStatusRejected, IdeaStatusConverted:
		item.ReviewerID = firstNonEmptyText(item.ReviewerID, viewer.ViewerID)
		item.ReviewerName = firstNonEmptyText(item.ReviewerName, viewerDisplayName(viewer))
		item.ReviewedAt = now
		item.ArchivedAt = time.Time{}
	case IdeaStatusArchived:
		item.ArchivedAt = now
	}
}

func filterIdeasForViewer(items []IdeaItem, viewer ViewerRef, query ListIdeasQuery) []IdeaItem {
	q := strings.ToLower(strings.TrimSpace(query.Q))
	out := make([]IdeaItem, 0, len(items))
	for _, item := range items {
		if !canReadIdea(viewer, item) {
			continue
		}
		if query.Status != "" && item.Status != query.Status {
			continue
		}
		if query.Visibility != "" && item.Visibility != query.Visibility {
			continue
		}
		if strings.TrimSpace(query.OrganizationID) != "" && item.OrganizationID != strings.TrimSpace(query.OrganizationID) {
			continue
		}
		if strings.TrimSpace(query.DepartmentID) != "" && item.DepartmentID != strings.TrimSpace(query.DepartmentID) {
			continue
		}
		if strings.TrimSpace(query.OwnerID) != "" && item.OwnerID != strings.TrimSpace(query.OwnerID) {
			continue
		}
		if strings.TrimSpace(query.ReviewerID) != "" && item.ReviewerID != strings.TrimSpace(query.ReviewerID) {
			continue
		}
		if q != "" && !matchesSearchText(ideaSearchText(item), q) {
			continue
		}
		out = append(out, item)
	}
	return out
}

func ideaSearchText(item IdeaItem) string {
	return strings.ToLower(strings.Join([]string{
		item.IdeaID, item.IssueNumber, item.Title, item.Content, string(item.Status), string(item.Visibility),
		string(item.Impact), string(item.Effort), item.ProposerName, strings.Join(item.Tags, " "),
		item.OwnerID, item.OwnerName, item.ReviewerID, item.ReviewerName, item.DecisionReason,
		item.OrganizationID, item.DepartmentID, item.CreatedBy, item.ConvertedTodoID,
	}, " "))
}

func priorityFromIdea(idea IdeaItem) TodoPriority {
	if idea.Impact == IdeaImpactHigh && idea.Effort == IdeaEffortSmall {
		return TodoPriorityUrgent
	}
	if idea.Impact == IdeaImpactHigh {
		return TodoPriorityHigh
	}
	if idea.Impact == IdeaImpactLow {
		return TodoPriorityLow
	}
	return TodoPriorityMedium
}
