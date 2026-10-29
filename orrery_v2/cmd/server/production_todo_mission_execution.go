package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/developmentproject"
	"orrery_v2/internal/app/discussionsession"
	missioncontrol "orrery_v2/internal/app/missioncontrol"
	todoapp "orrery_v2/internal/app/todo"
	missioncore "orrery_v2/internal/mission/core"
)

type productionTodoDevelopmentExecutionGuard struct {
	projects *developmentproject.Service
}

func (g productionTodoDevelopmentExecutionGuard) EnsureTodoBindingAllowed(ctx context.Context, req todoapp.TodoDevelopmentExecutionAuthorization) error {
	if g.projects == nil {
		return todoapp.ErrServiceDependency
	}
	err := g.projects.EnsureTodoBindingAllowed(ctx, todoDevelopmentProjectViewer(req), req.DevelopmentProjectID)
	return mapTodoDevelopmentProjectGuardError(err, "development project binding is not authorized")
}

func (g productionTodoDevelopmentExecutionGuard) EnsureTodoPlanApprovalAllowed(ctx context.Context, req todoapp.TodoDevelopmentExecutionAuthorization) error {
	if g.projects == nil {
		return todoapp.ErrServiceDependency
	}
	err := g.projects.EnsureTodoPlanApprovalAllowed(ctx, todoDevelopmentProjectViewer(req), req.DevelopmentProjectID)
	return mapTodoDevelopmentProjectGuardError(err, "development project plan approval is not authorized")
}

func (g productionTodoDevelopmentExecutionGuard) EnsureTodoExecutionAllowed(ctx context.Context, req todoapp.TodoDevelopmentExecutionAuthorization) error {
	if g.projects == nil {
		return todoapp.ErrServiceDependency
	}
	err := g.projects.EnsureTodoExecutionAllowed(ctx, todoDevelopmentProjectViewer(req), req.DevelopmentProjectID, req.AssignedAgentProfileID)
	return mapTodoDevelopmentProjectGuardError(err, "development project execution is not authorized")
}

func (g productionTodoDevelopmentExecutionGuard) ResolveTodoExecutionPlanContext(ctx context.Context, req todoapp.TodoDevelopmentExecutionPlanContextRequest) (todoapp.TodoDevelopmentExecutionPlanContext, error) {
	if g.projects == nil {
		return todoapp.TodoDevelopmentExecutionPlanContext{}, todoapp.ErrServiceDependency
	}
	snapshot, err := g.projects.ResolveExecutionPlanSnapshot(ctx, req.TenantID, req.DevelopmentProjectID)
	if err != nil {
		return todoapp.TodoDevelopmentExecutionPlanContext{}, mapTodoDevelopmentProjectGuardError(err, "development project execution plan context is unavailable")
	}
	result := todoapp.TodoDevelopmentExecutionPlanContext{
		DevelopmentProjectID: snapshot.DevelopmentProjectID, ResourceSnapshotDigest: snapshot.SnapshotDigest,
		AllowedCommandPrefixes: append([]string(nil), snapshot.ExecutionPolicy.AllowedCommandPrefixes...),
		AllowedFileScopes:      append([]string(nil), snapshot.ExecutionPolicy.AllowedFileScopes...),
		AllowedNetworkMode:     snapshot.ExecutionPolicy.NetworkMode,
		AllowedNetworkHosts:    append([]string(nil), snapshot.ExecutionPolicy.AllowedHosts...),
		Repositories:           make([]todoapp.TodoDevelopmentPlanRepository, 0, len(snapshot.Repositories)),
	}
	for _, secretRef := range snapshot.ExecutionPolicy.AllowedSecretRefs {
		digest := sha256.Sum256([]byte(strings.TrimSpace(secretRef)))
		result.AllowedSecretRefDigests = append(result.AllowedSecretRefDigests, "sha256:"+hex.EncodeToString(digest[:]))
	}
	for _, repository := range snapshot.Repositories {
		result.Repositories = append(result.Repositories, todoapp.TodoDevelopmentPlanRepository{
			BindingID: repository.BindingID, DisplayName: repository.DisplayName, Role: repository.Role, AccessMode: repository.AccessMode,
			DefaultBranch: repository.DefaultBranch, SourceSubdirectory: repository.SourceSubdirectory,
			BuildCommands: append([]string(nil), repository.BuildCommands...), TestCommands: append([]string(nil), repository.TestCommands...),
			BaseCommit: repository.BaseCommit,
		})
	}
	return result, nil
}

func todoDevelopmentProjectViewer(req todoapp.TodoDevelopmentExecutionAuthorization) developmentproject.ViewerRef {
	viewerKind := strings.TrimSpace(string(req.RequesterKind))
	if viewerKind == "human" {
		viewerKind = "user"
	}
	return developmentproject.ViewerRef{
		ViewerID: req.RequesterID, ViewerKind: viewerKind, TenantID: req.TenantID,
		AuthorizationSubjectID: req.RequesterSubjectID, PermissionScope: append([]string(nil), req.RequesterScopes...),
	}
}

func mapTodoDevelopmentProjectGuardError(err error, deniedMessage string) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, developmentproject.ErrNotFound):
		return fmt.Errorf("%w: %s", todoapp.ErrForbidden, deniedMessage)
	case errors.Is(err, developmentproject.ErrArchived), errors.Is(err, developmentproject.ErrRepositoryPolicy):
		return fmt.Errorf("%w: %v", todoapp.ErrInvalidRequest, err)
	case errors.Is(err, developmentproject.ErrDependency):
		return fmt.Errorf("%w: %v", todoapp.ErrServiceDependency, err)
	default:
		return err
	}
}

type productionTodoMissionExecutor struct {
	api         *productionAPI
	discussions *discussionsession.Service
}

func (e productionTodoMissionExecutor) PauseMissionForTodo(ctx context.Context, req todoapp.TodoMissionPauseRequest) error {
	if e.api == nil || e.api.missionCommand == nil {
		return todoapp.ErrServiceDependency
	}
	if strings.TrimSpace(req.TenantID) == "" || strings.TrimSpace(req.TodoID) == "" || strings.TrimSpace(req.BindingID) == "" || strings.TrimSpace(req.MissionID) == "" || strings.TrimSpace(req.Reason) == "" || strings.TrimSpace(req.IdempotencyKey) == "" || req.ExecutionAttempt <= 0 {
		return todoapp.ErrInvalidRequest
	}
	return e.api.missionCommand.PauseTodoMission(ctx, qaPlanSystemViewer(req.TenantID), missioncontrol.PauseTodoMissionCommand{
		IdempotencyKey: req.IdempotencyKey,
		MissionID:      missioncore.MissionID(req.MissionID),
		TodoID:         req.TodoID,
		Reason:         req.Reason,
	})
}

func (e productionTodoMissionExecutor) CreateMissionFromTodo(ctx context.Context, req todoapp.TodoMissionExecutionRequest) (todoapp.TodoMissionExecutionReceipt, error) {
	if e.api == nil || e.api.missionCommand == nil || e.api.authorizationRelations == nil || e.discussions == nil {
		return todoapp.TodoMissionExecutionReceipt{}, todoapp.ErrServiceDependency
	}
	if strings.TrimSpace(req.TenantID) == "" || strings.TrimSpace(req.TodoID) == "" || strings.TrimSpace(req.ConversationID) == "" || strings.TrimSpace(req.PlanDigest) == "" || strings.TrimSpace(req.ApprovedBy) == "" || strings.TrimSpace(req.ApproverAuthorizationSubjectID) == "" || strings.TrimSpace(req.IdempotencyKey) == "" || req.ExecutionAttempt <= 0 {
		return todoapp.TodoMissionExecutionReceipt{}, todoapp.ErrInvalidRequest
	}
	steps := productionTodoMissionPlanSteps(req)
	creationViewer := missioncontrol.ViewerRef{
		ViewerID: todoMissionAutomationViewerID, ViewerKind: missioncontrol.ViewerKindSystem, TenantID: req.TenantID,
		AuthorizationSubjectID: todoMissionAutomationViewerID,
	}
	goal := strings.TrimSpace(req.TodoDescription)
	if goal == "" {
		goal = strings.TrimSpace(req.TodoTitle)
	}
	if acceptance := strings.TrimSpace(req.AcceptanceCriteria); acceptance != "" {
		goal += "\n\nAcceptance criteria:\n" + acceptance
	}
	mission, err := e.api.missionCommand.CreateMission(ctx, creationViewer, missioncontrol.CreateMissionCommand{
		RequestID: req.IdempotencyKey, IdempotencyKey: req.IdempotencyKey,
		Title: "Todo: " + strings.TrimSpace(req.TodoTitle), FinalGoal: goal,
		RequesterID: req.ApprovedBy, InitialHumanAgentID: req.ApproverAuthorizationSubjectID, InitialHumanDisplayName: firstNonBlank(req.ApproverDisplayName, req.ApprovedBy),
		OriginKind: "todo", OriginID: req.TodoID, OriginTodoID: req.TodoID, ExecutionAttempt: req.ExecutionAttempt,
		DiscussionMode: "existing_conversation", OriginDevelopmentProjectID: req.DevelopmentProjectID,
		ExistingConversationID: req.ConversationID,
	})
	if err != nil {
		return todoapp.TodoMissionExecutionReceipt{}, err
	}
	receipt := todoapp.TodoMissionExecutionReceipt{
		MissionID: mission.MissionID, ConversationID: req.ConversationID,
		Tasks: make([]todoapp.TodoMissionTaskExecution, 0, len(steps)), RequiresWorkspace: strings.TrimSpace(req.DevelopmentProjectID) != "",
	}
	if err := e.api.authorizationRelations.WriteMissionAutomation(ctx, authorization.SubjectRef{
		Kind: string(missioncontrol.ViewerKindSystem), ID: qaPlanSystemViewerID,
	}, mission.MissionID, req.TenantID); err != nil {
		return receipt, err
	}
	taskViewer := qaPlanSystemViewer(req.TenantID)
	if err := e.api.publishMissionCreatedToOntology(ctx, creationViewer, mission); err != nil {
		return receipt, err
	}
	if _, err := e.discussions.LinkMission(ctx, discussionsession.ViewerRef{
		ViewerID: req.ApproverAuthorizationSubjectID, AgentProfileID: req.ApproverAuthorizationSubjectID, ViewerKind: discussionsession.ViewerKindUser, DisplayName: req.ApproverDisplayName, TenantID: req.TenantID,
	}, req.ConversationID, discussionsession.LinkedMissionRecord{MissionID: mission.MissionID, Title: mission.Title, Status: mission.Status, CreatedAt: mission.CreatedAt}); err != nil {
		return receipt, err
	}

	taskIDs := make(map[string]missioncore.TaskID, len(steps))
	for _, step := range steps {
		taskIDs[step.StepID] = missioncore.TaskID(todoMissionTaskID(req.TenantID, req.TodoID, req.ExecutionAttempt, step.StepID))
	}
	for index, step := range steps {
		dependencies := make([]missioncore.TaskID, 0, len(step.DependsOn))
		for _, dependency := range step.DependsOn {
			dependencyID, ok := taskIDs[dependency]
			if !ok {
				return receipt, fmt.Errorf("%w: plan step %s references unknown dependency %s", todoapp.ErrInvalidRequest, step.StepID, dependency)
			}
			dependencies = append(dependencies, dependencyID)
		}
		task, err := e.api.CreateTask(ctx, taskViewer, missioncontrol.CreateTaskCommand{
			RequestID: fmt.Sprintf("%s:step:%d", req.IdempotencyKey, index+1), IdempotencyKey: fmt.Sprintf("%s:step:%s", req.IdempotencyKey, step.StepID),
			MissionID: missioncore.MissionID(mission.MissionID), TaskID: taskIDs[step.StepID], AssigneeID: step.AssigneeAgentID,
			Goal: firstNonBlank(step.Description, step.Title), WorkerPersonalGoal: step.Title, Scope: "Todo " + req.TodoID + " approved plan step " + step.StepID,
			Priority: "normal", ExpectedDeliverables: []missioncontrol.ExpectedDeliverableCommand{{ID: "deliverable-" + step.StepID, Title: step.Title}},
			AcceptanceCriteria: step.AcceptanceCriteria, EvidenceRequirements: todoStepEvidenceRequirements(step),
			Dependencies: dependencies, SourceKind: "todo_plan_step", SourceID: req.TodoID, SourceStepID: step.StepID,
		})
		if err != nil {
			return receipt, err
		}
		receipt.Tasks = append(receipt.Tasks, todoapp.TodoMissionTaskExecution{
			MissionID: mission.MissionID, TaskID: task.TaskID, AgentID: step.AssigneeAgentID, Goal: task.Goal,
		})
		if !receipt.RequiresWorkspace && (task.Status == string(missioncontrolTaskStatusCreated) || strings.TrimSpace(task.Status) == "") {
			if _, err := e.api.missionCommand.AssignTask(ctx, taskViewer, missioncontrol.AssignTaskCommand{
				RequestID:      fmt.Sprintf("%s:step:%s:assign", req.IdempotencyKey, step.StepID),
				IdempotencyKey: fmt.Sprintf("%s:step:%s:assign", req.IdempotencyKey, step.StepID),
				MissionID:      missioncore.MissionID(mission.MissionID), TaskID: missioncore.TaskID(task.TaskID),
				AssigneeID: step.AssigneeAgentID, Reason: "approved Todo plan execution",
			}); err != nil {
				return receipt, err
			}
		}
	}
	return receipt, nil
}

func productionTodoMissionPlanSteps(req todoapp.TodoMissionExecutionRequest) []todoapp.TodoExecutionPlanStep {
	if len(req.Plan.Steps) > 0 {
		return append([]todoapp.TodoExecutionPlanStep(nil), req.Plan.Steps...)
	}
	title := "完成 Todo：" + strings.TrimSpace(req.TodoTitle)
	criterion := strings.TrimSpace(req.AcceptanceCriteria)
	if criterion == "" {
		criterion = title + "，且结果已经过可核验证据确认"
	}
	return []todoapp.TodoExecutionPlanStep{{
		StepID: "root", Title: title, Description: firstNonBlank(req.TodoDescription, title), AssigneeAgentID: qaPlanSystemViewerID,
		AcceptanceCriteria: []string{criterion}, EvidenceRequirements: []todoapp.TodoExecutionEvidenceRequirement{{
			EvidenceRequirementID: "evidence-root-1", AcceptanceCriterion: criterion, Kind: "artifact", Required: true,
		}},
	}}
}

const missioncontrolTaskStatusCreated = "created"

const todoMissionAutomationViewerID = "todo-automation-agent"

func todoStepEvidenceRequirements(step todoapp.TodoExecutionPlanStep) []missioncontrol.EvidenceRequirementCommand {
	out := make([]missioncontrol.EvidenceRequirementCommand, 0, len(step.EvidenceRequirements))
	for _, requirement := range step.EvidenceRequirements {
		if description := strings.TrimSpace(requirement.AcceptanceCriterion); description != "" {
			out = append(out, missioncontrol.EvidenceRequirementCommand{
				ID: strings.TrimSpace(requirement.EvidenceRequirementID), Description: description,
				Kind: strings.TrimSpace(requirement.Kind), Required: requirement.Required,
			})
		}
	}
	return out
}

func todoMissionTaskID(tenantID, todoID string, attempt int, stepID string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(tenantID) + "\x00" + strings.TrimSpace(todoID) + "\x00" + fmt.Sprint(attempt) + "\x00" + strings.TrimSpace(stepID)))
	return "todo-task-" + hex.EncodeToString(digest[:12])
}
