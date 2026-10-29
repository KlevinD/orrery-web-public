package main

import (
	"context"
	"fmt"

	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/platform"
)

type authorizationRelationReconciler struct {
	projector           *authorizationapp.RelationProjector
	missions            authorizationBackfillMissionRepository
	discussions         authorizationBackfillDiscussionRepository
	projects            authorizationBackfillProjectRepository
	developmentProjects authorizationBackfillDevelopmentProjectRepository
	scmConnections      authorizationBackfillSCMConnectionRepository
	todos               authorizationBackfillTodoRepository
	ops                 authorizationBackfillOperations
	agents              authorizationBackfillAgentDirectory
	opts                authorizationRelationBackfillOptions
}

func (r *authorizationRelationReconciler) Sweep(ctx context.Context) error {
	if r == nil || r.projector == nil || r.opts.Disabled || r.opts.DryRun {
		return nil
	}
	opts := r.opts
	// A recurring snapshot cannot prove that an extra tuple is stale: it may have
	// been written after the snapshot was acquired. Destructive pruning is only
	// safe during the startup backfill gate.
	opts.Prune = false
	result, err := runAuthorizationRelationBackfill(ctx, r.projector, r.missions, r.discussions, r.projects, r.developmentProjects, r.scmConnections, r.todos, r.ops, r.agents, opts)
	if err != nil {
		return err
	}
	if result.Diff.Errors != 0 || result.Diff.Missing != 0 {
		return fmt.Errorf("authorization relation reconciliation incomplete: missing=%d extra=%d errors=%d", result.Diff.Missing, result.Diff.Extra, result.Diff.Errors)
	}
	return nil
}

func registerAuthorizationRelationReconciliationSweeper(
	rt *platform.Runtime,
	projector *authorizationapp.RelationProjector,
	missions authorizationBackfillMissionRepository,
	discussions authorizationBackfillDiscussionRepository,
	projects authorizationBackfillProjectRepository,
	developmentProjects authorizationBackfillDevelopmentProjectRepository,
	scmConnections authorizationBackfillSCMConnectionRepository,
	todos authorizationBackfillTodoRepository,
	ops authorizationBackfillOperations,
	agents authorizationBackfillAgentDirectory,
) error {
	if rt == nil || projector == nil {
		return nil
	}
	opts, err := authorizationRelationBackfillOptionsFromPlatform(rt.Config)
	if err != nil {
		return err
	}
	if opts.Disabled || opts.DryRun {
		return nil
	}
	return rt.RegisterSweeper("authorization.relation_reconcile", &authorizationRelationReconciler{
		projector:           projector,
		missions:            missions,
		discussions:         discussions,
		projects:            projects,
		developmentProjects: developmentProjects,
		scmConnections:      scmConnections,
		todos:               todos,
		ops:                 ops,
		agents:              agents,
		opts:                opts,
	})
}
