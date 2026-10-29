package main

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/app/developmentproject"
	"orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	todoapp "orrery_v2/internal/app/todo"
	missioncore "orrery_v2/internal/mission/core"
)

type productionTodoWorkspaceExecution struct {
	projects    *developmentproject.Service
	discussions *discussionsession.Service
	api         *productionAPI
}

func (p productionTodoWorkspaceExecution) ProvisionTodoExecution(ctx context.Context, req todoapp.TodoWorkspaceExecutionRequest) (todoapp.TodoWorkspaceExecutionReceipt, error) {
	if p.projects == nil || p.discussions == nil || p.api == nil || p.api.missionCommand == nil {
		return todoapp.TodoWorkspaceExecutionReceipt{}, todoapp.ErrServiceDependency
	}
	taskIDs, agentIDs := make([]string, 0, len(req.MissionTasks)), make([]string, 0, len(req.MissionTasks))
	for _, task := range req.MissionTasks {
		if strings.TrimSpace(task.TaskID) == "" || strings.TrimSpace(task.AgentID) == "" || strings.TrimSpace(task.MissionID) != strings.TrimSpace(req.MissionID) {
			return todoapp.TodoWorkspaceExecutionReceipt{}, todoapp.ErrInvalidRequest
		}
		taskIDs, agentIDs = append(taskIDs, strings.TrimSpace(task.TaskID)), append(agentIDs, strings.TrimSpace(task.AgentID))
	}
	result, err := p.projects.ProvisionTodoWorkspace(ctx, developmentproject.ProvisionTodoWorkspaceCommand{
		TenantID: req.TenantID, DevelopmentProjectID: req.DevelopmentProjectID, TodoID: req.TodoID, TodoTitle: req.TodoTitle,
		PlanID: req.PlanID, PlanDigest: req.PlanDigest, ApprovalID: req.ApprovalID, MissionID: req.MissionID,
		ResourceSnapshotDigest: req.ResourceSnapshotDigest, RepositoryBindingID: req.RepositoryBindingID, BaseCommit: req.BaseCommit,
		AllowedFileScopes: append([]string(nil), req.AllowedFileScopes...), PlannedCommands: append([]string(nil), req.PlannedCommands...),
		NetworkMode: req.NetworkMode, AllowedHosts: append([]string(nil), req.AllowedHosts...), RequiredSecretRefs: append([]string(nil), req.RequiredSecretRefs...),
		MissionTaskIDs: taskIDs, AgentIDs: agentIDs, LeaseOwnerRunID: req.LeaseOwnerRunID,
	})
	if err != nil {
		return todoapp.TodoWorkspaceExecutionReceipt{}, err
	}
	if _, err := p.discussions.BindTodoWorkspace(ctx, discussionsession.ViewerRef{
		ViewerID: todoMissionAutomationViewerID, ViewerKind: discussionsession.ViewerKindSystem, TenantID: req.TenantID,
	}, req.ConversationID, req.TodoID, result.Workspace.WorkspaceID); err != nil {
		return todoapp.TodoWorkspaceExecutionReceipt{}, err
	}
	viewer := qaPlanSystemViewer(req.TenantID)
	for index, task := range req.MissionTasks {
		if _, err := p.api.missionCommand.AssignTask(ctx, viewer, missioncontrol.AssignTaskCommand{
			RequestID:      fmt.Sprintf("todo-workspace:%s:%s:assign", req.TodoID, task.TaskID),
			IdempotencyKey: fmt.Sprintf("todo-workspace:%s:%s:assign", req.TodoID, task.TaskID),
			MissionID:      missioncore.MissionID(req.MissionID), TaskID: missioncore.TaskID(task.TaskID),
			AssigneeID: task.AgentID, Reason: "approved Todo workspace is ready",
		}); err != nil {
			return todoapp.TodoWorkspaceExecutionReceipt{}, err
		}
		_ = index
	}
	runIDs := make([]string, len(result.Runs))
	for index := range result.Runs {
		runIDs[index] = result.Runs[index].ExecutionRunID
	}
	return todoapp.TodoWorkspaceExecutionReceipt{WorkspaceID: result.Workspace.WorkspaceID, ExecutionRunIDs: runIDs}, nil
}

type productionManagedWorkspaceResolver struct{ projects *developmentproject.Service }

func (r productionManagedWorkspaceResolver) ResolveManagedWorkspace(ctx context.Context, tenantID, workspaceID string) (discussionsession.ManagedWorkspaceContext, error) {
	if r.projects == nil {
		return discussionsession.ManagedWorkspaceContext{}, discussionsession.ErrServiceDependency
	}
	resolved, err := r.projects.ResolveManagedWorkspace(ctx, tenantID, workspaceID, "")
	if err != nil {
		return discussionsession.ManagedWorkspaceContext{}, err
	}
	return discussionsession.ManagedWorkspaceContext{RootDir: resolved.RootDir, WorkDir: resolved.WorkDir, NetworkMode: resolved.NetworkMode}, nil
}
