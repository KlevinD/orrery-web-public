package main

import (
	"context"
	"encoding/json"
	"io"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/projectadmin"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/db"
	toolres "orrery_v2/internal/resource/tool"
)

type productionScopeBindingBackfillOptions struct {
	Apply     bool
	Limit     int
	After     ontologyscope.BindingBackfillCursor
	ChangedBy string
}

func runProductionScopeBindingBackfill(ctx context.Context, cfg config.Config, opts productionScopeBindingBackfillOptions, out io.Writer) error {
	if out == nil {
		out = io.Discard
	}
	runtime, err := platform.OpenWithModules(ctx, cfg, productionModules()...)
	if err != nil {
		return err
	}
	defer runtime.Close()
	if err := runtime.Initialize(ctx); err != nil {
		return err
	}
	core, err := runtime.Databases.Store(db.RoleCore)
	if err != nil {
		return err
	}
	store, resolver, err := newProductionOntologyBindingResolver(
		core,
		operations.NewOrganizationCatalogStore(runtime.DBRouter),
		projectadmin.NewPlatformRepository(runtime.DBRouter),
		missioncontrol.NewDBProjectionRepository(runtime.DBRouter),
		toolres.NewPlatformRegistry(core),
	)
	if err != nil {
		return err
	}
	report, err := ontologyscope.BackfillBindings(ctx, core, resolver, store, ontologyscope.BindingBackfillOptions{
		Apply:     opts.Apply,
		Limit:     opts.Limit,
		After:     opts.After,
		ChangedBy: opts.ChangedBy,
	})
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(report)
}
