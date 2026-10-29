package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/rebac"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

func newProductionHandlerFromRuntime(ctx context.Context, rt *platform.Runtime) (http.Handler, func(), error) {
	return newProductionHandlerFromRuntimeWithOptions(ctx, rt, productionHandlerOptions{})
}

func prepareProductionOntologyRuntime(ctx context.Context, rt *platform.Runtime, disableBackgroundWorkers bool) (*db.Store, *productionOntologySecurityState, error) {
	core, err := prepareProductionRuntime(ctx, rt, disableBackgroundWorkers)
	if err != nil {
		return nil, nil, err
	}
	security, err := newProductionOntologySecurityRuntime(ctx, rt, core)
	if err != nil {
		return nil, nil, err
	}
	return core, security, nil
}

func prepareProductionRuntime(ctx context.Context, rt *platform.Runtime, disableBackgroundWorkers bool) (*db.Store, error) {
	if rt == nil || rt.Databases == nil || rt.DBRouter == nil || rt.UnitOfWork == nil {
		return nil, errors.New("production runtime is not fully configured")
	}
	core, err := rt.Databases.Store(db.RoleCore)
	if err != nil {
		return nil, err
	}
	if !disableBackgroundWorkers {
		if err := migrateLegacyTenantRecordKeys(ctx, rt.DBRouter); err != nil {
			return nil, fmt.Errorf("migrate legacy tenant record keys: %w", err)
		}
	}
	if err := registerExistingPersonalAssistantIdentities(ctx, agentcore.NewPlatformProfileRepository(rt.UnitOfWork)); err != nil {
		return nil, fmt.Errorf("register personal assistant identities: %w", err)
	}
	return core, nil
}

func newProductionOntologySecurityRuntime(ctx context.Context, rt *platform.Runtime, core *db.Store) (*productionOntologySecurityState, error) {
	authorizationMode, _ := rt.Config.String("authz.mode")
	state, err := newProductionOntologySecurityState(core, rebac.NewPlatformStore(core), authorizationMode)
	if err != nil {
		return nil, err
	}
	state.Cursors, err = newProductionOntologyCursorState(
		ctx,
		rt.Secrets,
		state.Invalidations,
		strings.EqualFold(strings.TrimSpace(rt.Config.Environment), "production"),
	)
	if err != nil {
		return nil, err
	}
	return state, nil
}

func runProductionStartup(
	ctx context.Context,
	disabled bool,
	repository *schedulerapp.Repository,
	rt *platform.Runtime,
) error {
	if disabled {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ensureDefaultProductionScheduledTasks(ctx, repository, rt.Clock.Now()); err != nil {
		return err
	}
	return nil
}
