package main

import (
	"context"
	"errors"
	"sort"
	"strings"

	developmentproject "orrery_v2/internal/app/developmentproject"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/missioncontrol"
	todoapp "orrery_v2/internal/app/todo"
	missioncore "orrery_v2/internal/mission/core"
)

type productionDevelopmentExecutionMonitor struct {
	api   *productionAPI
	todos *todoapp.Repository
}

func (m productionDevelopmentExecutionMonitor) ListProjectExecutions(ctx context.Context, viewer developmentproject.ViewerRef, project developmentproject.Project, canMonitor bool, query developmentproject.ListExecutionsQuery) (developmentproject.ExecutionPage, error) {
	if m.api == nil || m.todos == nil {
		return developmentproject.ExecutionPage{}, developmentproject.ErrDependency
	}
	items, err := m.todos.ListProjectExecutionTodoSnapshots(ctx, viewer.TenantID, project.DevelopmentProjectID)
	if err != nil {
		return developmentproject.ExecutionPage{}, err
	}
	missionViewer := missionViewerFromDevelopmentViewer(viewer)
	out := make([]developmentproject.ExecutionSummary, 0, len(items))
	for _, item := range items {
		summary, err := m.executionSummary(ctx, missionViewer, viewer, item, canMonitor)
		if err != nil {
			return developmentproject.ExecutionPage{}, err
		}
		if !projectExecutionMatches(summary, query) {
			continue
		}
		out = append(out, summary)
	}
	sort.SliceStable(out, func(i, j int) bool {
		left, right := projectExecutionRank(out[i]), projectExecutionRank(out[j])
		if left != right {
			return left < right
		}
		if !out[i].LastActivityAt.Equal(out[j].LastActivityAt) {
			return out[i].LastActivityAt.After(out[j].LastActivityAt)
		}
		return out[i].TodoID < out[j].TodoID
	})
	offset, limit := executionPageBounds(query.Offset, query.Limit, len(out))
	pageItems := append([]developmentproject.ExecutionSummary(nil), out[offset:offset+limit]...)
	return developmentproject.ExecutionPage{
		Items: pageItems,
		Page:  developmentproject.PageDTO{Limit: limit, Offset: offset, Total: len(out), Returned: len(pageItems), HasMore: offset+limit < len(out)},
	}, nil
}

func (m productionDevelopmentExecutionMonitor) executionSummary(ctx context.Context, missionViewer missioncontrol.ViewerRef, projectViewer developmentproject.ViewerRef, item todoapp.ProjectExecutionTodoSnapshot, canMonitor bool) (developmentproject.ExecutionSummary, error) {
	summary := developmentproject.ExecutionSummary{
		TodoID: item.TodoID, TodoTitle: item.Title, TodoStatus: string(item.Status), AutomationMode: string(item.AutomationMode),
		AssigneeID: item.AssigneeID, AssigneeName: item.AssigneeName, CurrentAgentIDs: []string{}, LastActivityAt: item.UpdatedAt,
	}
	summary.AutomationStatus = string(item.AutomationStatus)
	if strings.TrimSpace(item.MissionID) == "" {
		return summary, nil
	}
	progress, err := m.todos.GetTodoExecutionProgressProjection(ctx, projectViewer.TenantID, item.TodoID, item.ExecutionAttempt)
	if err != nil {
		if errors.Is(err, todoapp.ErrNotFound) {
			return summary, nil
		}
		return developmentproject.ExecutionSummary{}, err
	}
	if progress.MissionID != item.MissionID {
		return developmentproject.ExecutionSummary{}, developmentproject.ErrDependency
	}
	summary.MissionID = progress.MissionID
	summary.ExecutionAttempt = progress.ExecutionAttempt
	summary.MissionStatus = progress.MissionStatus
	summary.CurrentPhase = progress.CurrentPhase
	summary.DeliveryRate = progress.DeliveryRate
	summary.TotalTaskCount = progress.TotalTaskCount
	summary.ActiveTaskCount = progress.ActiveTaskCount
	summary.DoneTaskCount = progress.DoneTaskCount
	summary.BlockedTaskCount = progress.BlockedTaskCount
	summary.PendingApprovalCount = progress.PendingApprovalCount
	summary.CurrentAgentIDs = append([]string(nil), progress.CurrentAgentIDs...)
	summary.LastActivityAt = progress.LastActivityAt
	summary.MissionProjectionVersion = progress.MissionProjectionVersion

	if canMonitor || developmentViewerParticipatesInTodo(projectViewer, item) {
		if _, err := m.api.GetMissionControlView(ctx, missionViewer, missioncore.MissionID(item.MissionID)); err == nil {
			summary.CanOpenControl = true
		} else if !errors.Is(err, missioncontrol.ErrProjectionNotFound) {
			return developmentproject.ExecutionSummary{}, err
		}
	}
	conversationID := strings.TrimSpace(item.ConversationID)
	if conversationID != "" && m.api.discussions != nil {
		if _, err := m.api.discussions.GetConversation(ctx, discussionSessionViewer(missionViewer), conversationID); err == nil {
			summary.CanOpenDiscussion = true
			summary.ConversationID = conversationID
		} else if !errors.Is(err, discussionsession.ErrForbidden) && !errors.Is(err, discussionsession.ErrNotFound) {
			return developmentproject.ExecutionSummary{}, err
		}
	}
	return summary, nil
}

func missionViewerFromDevelopmentViewer(viewer developmentproject.ViewerRef) missioncontrol.ViewerRef {
	return missioncontrol.ViewerRef{
		ViewerID: viewer.ViewerID, ViewerKind: missioncontrol.ViewerKind(viewer.ViewerKind), TenantID: viewer.TenantID,
		AuthorizationSubjectID: viewer.AuthorizationSubjectID, PermissionScope: append([]string(nil), viewer.PermissionScope...),
	}
}

func developmentViewerParticipatesInTodo(viewer developmentproject.ViewerRef, item todoapp.ProjectExecutionTodoSnapshot) bool {
	ids := []string{strings.TrimSpace(viewer.ViewerID), strings.TrimSpace(viewer.AuthorizationSubjectID)}
	participants := []string{strings.TrimSpace(item.CreatedBy), strings.TrimSpace(item.CreatedBySubjectID), strings.TrimSpace(item.AssigneeID), strings.TrimSpace(item.ReviewerID)}
	for _, id := range ids {
		for _, participant := range participants {
			if id != "" && id == participant {
				return true
			}
		}
	}
	return false
}

func projectExecutionMatches(item developmentproject.ExecutionSummary, query developmentproject.ListExecutionsQuery) bool {
	if value := strings.TrimSpace(query.TodoID); value != "" && item.TodoID != value {
		return false
	}
	if value := strings.TrimSpace(query.TodoStatus); value != "" && item.TodoStatus != value {
		return false
	}
	if value := strings.TrimSpace(query.MissionStatus); value != "" && item.MissionStatus != value {
		return false
	}
	if value := strings.TrimSpace(query.AssigneeID); value != "" && item.AssigneeID != value {
		return false
	}
	if query.Blocked != nil && (item.BlockedTaskCount > 0) != *query.Blocked {
		return false
	}
	if query.PendingApproval != nil && (item.PendingApprovalCount > 0) != *query.PendingApproval {
		return false
	}
	return true
}

func projectExecutionRank(item developmentproject.ExecutionSummary) int {
	if item.BlockedTaskCount > 0 {
		return 0
	}
	if item.PendingApprovalCount > 0 {
		return 1
	}
	if item.ActiveTaskCount > 0 {
		return 2
	}
	return 3
}

func executionPageBounds(offset, limit, total int) (int, int) {
	if offset < 0 {
		offset = 0
	}
	if offset > total {
		offset = total
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset+limit > total {
		limit = total - offset
	}
	return offset, limit
}
