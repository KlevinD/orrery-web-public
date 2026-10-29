package main

import (
	"orrery_v2/internal/app/authorization"
	developmentproject "orrery_v2/internal/app/developmentproject"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/operations"
	todoapp "orrery_v2/internal/app/todo"
	"orrery_v2/internal/platform"
	"path/filepath"
)

func newProductionTodoService(
	rt *platform.Runtime,
	repository *todoapp.Repository,
	settings productionSettingsStore,
	discussions *discussionsession.Service,
	audit *operations.AuditStore,
	api *productionAPI,
	projects *developmentproject.Service,
	decisionPrincipal todoapp.TodoDecisionPrincipalResolver,
	authorizationService *authorization.Service,
	authorizationRelations *authorization.RelationProjector,
) *todoapp.Service {
	projects.SetExecutionMonitor(productionDevelopmentExecutionMonitor{api: api, todos: repository})
	missionExecutor := productionTodoMissionExecutor{api: api, discussions: discussions}
	service := todoapp.NewService(todoapp.ServiceOptions{
		AttachmentStore:        discussionsession.NewLocalAttachmentStore(filepath.Join(rt.Config.BaseDir, "data", "todo-attachments")),
		Clock:                  rt.Clock,
		IDGenerator:            rt.IDGenerator,
		Repository:             repository,
		DraftSuggester:         newProductionTodoDraftSuggester(settings, platformSecretResolver{provider: rt.Secrets}),
		AutomationDiscussion:   discussions,
		SourceDiscussion:       discussions,
		AutomationAudit:        audit,
		MissionExecution:       missionExecutor,
		MissionPause:           missionExecutor,
		DevelopmentExecution:   productionTodoDevelopmentExecutionGuard{projects: projects},
		ExecutionProgress:      productionTodoExecutionProgress{api: api, repository: repository},
		WorkspaceExecution:     productionTodoWorkspaceExecution{projects: projects, discussions: discussions, api: api},
		DecisionPrincipal:      decisionPrincipal,
		AutomationAgentIDs:     []string{defaultLeadAgentID, defaultCoordinatorAgentID},
		ExecutionSupportIDs:    []string{defaultQAPlanAgentID, defaultQACheckAgentID},
		AllowOwnerSelfApproval: rt.Config.Development.AllowOwnerSelfApproval,
		HumanReviewEnabled:     settings.HumanReviewEnabled,
	})
	service.SetAuthorization(authorizationService, authorizationRelations)
	return service
}
