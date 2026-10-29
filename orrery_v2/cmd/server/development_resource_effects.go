package main

import (
	"context"
	"errors"
	"fmt"

	"orrery_v2/internal/platform"
)

const developmentResourceEffectReconcileLimit = 100

func registerDevelopmentResourceEffectReconciler(rt *platform.Runtime, resources productionDevelopmentResources) error {
	if rt == nil || resources.projects == nil || resources.connections == nil {
		return fmt.Errorf("development resource effect reconciler requires runtime and services")
	}
	return rt.RegisterSweeper("development_resources.pending_effects", platform.SweeperFunc(func(ctx context.Context) error {
		return errors.Join(
			resources.projects.ReconcilePendingEffects(ctx, developmentResourceEffectReconcileLimit),
			resources.connections.ReconcilePendingEffects(ctx, developmentResourceEffectReconcileLimit),
		)
	}))
}
