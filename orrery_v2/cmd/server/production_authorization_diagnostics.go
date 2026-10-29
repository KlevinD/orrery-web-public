package main

import (
	"context"

	authorizationapp "orrery_v2/internal/app/authorization"
)

func (api productionAPI) GetAuthorizationRelationBackfillDiagnostics(ctx context.Context) (authorizationapp.RelationBackfillResult, error) {
	return api.authorizationRelationBackfillDiagnostics(ctx)
}

func (api productionAPI) GetAuthorizationEnforceTransitionDiagnostics(ctx context.Context) (authorizationapp.EnforceTransitionDiagnostics, error) {
	backfill, err := api.authorizationRelationBackfillDiagnostics(ctx)
	if err != nil {
		return authorizationapp.EnforceTransitionDiagnostics{}, err
	}
	var shadow authorizationapp.ShadowDecisionStats
	if api.authorizationShadow != nil {
		shadow = api.authorizationShadow.Snapshot()
	}
	return authorizationapp.NewEnforceTransitionDiagnostics(shadow, backfill), nil
}

func (api productionAPI) authorizationRelationBackfillDiagnostics(ctx context.Context) (authorizationapp.RelationBackfillResult, error) {
	var missions authorizationBackfillMissionRepository
	if repo, ok := api.missionRepo.(authorizationBackfillMissionRepository); ok {
		missions = repo
	}
	var projects authorizationBackfillProjectRepository
	if api.projectRepository != nil {
		projects = api.projectRepository
	}
	var developmentProjects authorizationBackfillDevelopmentProjectRepository
	if api.developmentProjectRepository != nil {
		developmentProjects = api.developmentProjectRepository
	}
	var scmConnections authorizationBackfillSCMConnectionRepository
	if api.scmConnectionRepository != nil {
		scmConnections = api.scmConnectionRepository
	}
	var todos authorizationBackfillTodoRepository
	if api.todoRepository != nil {
		todos = api.todoRepository
	}
	var agents authorizationBackfillAgentDirectory
	if api.humanProfiles.profiles != nil {
		agents = api.humanProfiles.profiles
	}
	var ops authorizationBackfillOperations
	if api.authorizationBackfill != nil {
		ops = api.authorizationBackfill
	}
	return runAuthorizationRelationBackfill(ctx, api.authorizationRelations, missions, api.discussionRepository, projects, developmentProjects, scmConnections, todos, ops, agents, authorizationRelationBackfillOptions{
		Mode:   authorizationRelationBackfillModeDryRun,
		DryRun: true,
		Diff:   true,
	})
}
