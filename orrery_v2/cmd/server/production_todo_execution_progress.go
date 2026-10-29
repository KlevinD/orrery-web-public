package main

import (
	"context"
	"errors"
	"strings"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	todoapp "orrery_v2/internal/app/todo"
	missioncore "orrery_v2/internal/mission/core"
)

type productionTodoExecutionProgress struct {
	api        *productionAPI
	repository *todoapp.Repository
}

func (p productionTodoExecutionProgress) GetTodoExecutionProgress(ctx context.Context, viewer todoapp.ViewerRef, attempt todoapp.TodoExecutionAttempt) (todoapp.TodoExecutionProgress, bool, error) {
	if p.api == nil || p.repository == nil || strings.TrimSpace(attempt.MissionID) == "" {
		return todoapp.TodoExecutionProgress{}, false, nil
	}
	projected, err := p.repository.GetTodoExecutionProgressProjection(ctx, viewer.TenantID, attempt.TodoID, attempt.ExecutionAttempt)
	if err != nil {
		if errors.Is(err, todoapp.ErrNotFound) {
			return todoapp.TodoExecutionProgress{}, false, nil
		}
		return todoapp.TodoExecutionProgress{}, false, err
	}
	if projected.MissionID != strings.TrimSpace(attempt.MissionID) {
		return todoapp.TodoExecutionProgress{}, false, todoapp.ErrServiceDependency
	}
	view, err := p.api.GetMissionControlView(ctx, missionViewerFromTodoViewer(viewer), missioncore.MissionID(attempt.MissionID))
	if err != nil {
		if errors.Is(err, missioncontrol.ErrProjectionNotFound) {
			return todoapp.TodoExecutionProgress{}, false, nil
		}
		return todoapp.TodoExecutionProgress{}, false, err
	}
	progress := todoapp.TodoExecutionProgress{TodoExecutionProgressProjection: projected, CanOpenControl: true}
	if !view.Discussion.Restricted && strings.TrimSpace(view.Discussion.ConversationID) != "" {
		progress.CanOpenDiscussion = true
		progress.ConversationID = view.Discussion.ConversationID
	}
	return progress, true, nil
}

func missionViewerFromTodoViewer(viewer todoapp.ViewerRef) missioncontrol.ViewerRef {
	kind := missioncontrol.ViewerKindUser
	switch viewer.ViewerKind {
	case todoapp.ViewerKindAgent:
		kind = missioncontrol.ViewerKindAgent
	case todoapp.ViewerKindSystem:
		kind = missioncontrol.ViewerKindSystem
	}
	return missioncontrol.ViewerRef{
		ViewerID: viewer.ViewerID, ViewerKind: kind, TenantID: viewer.TenantID,
		AuthorizationSubjectID: viewer.AuthorizationSubjectID,
		PermissionScope:        append([]string(nil), viewer.PermissionScope...),
	}
}
