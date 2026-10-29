package main

import (
	"context"
	"strings"

	"orrery_v2/internal/app/actioncontract"
	"orrery_v2/internal/app/contextgraph"
	"orrery_v2/internal/app/datapolicy"
	"orrery_v2/internal/app/hil"
	"orrery_v2/internal/app/knowledgeinstances"
	"orrery_v2/internal/app/knowledgeontology"
	"orrery_v2/internal/app/memoryontology"
	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/app/ontologyreasoning"
	"orrery_v2/internal/app/ontologyscope"
	"orrery_v2/internal/app/peopleingest"
	"orrery_v2/internal/app/problemhub"
	"orrery_v2/internal/app/rebac"
	"orrery_v2/internal/app/reliability"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform"
	platformmetrics "orrery_v2/internal/platform/metrics"
)

func OntologyModule() platform.Module {
	return platform.ModuleFunc(func(ctx context.Context, registrar *platform.ModuleRegistrar) error {
		if err := ontology.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := peopleingest.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := actioncontract.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := reliability.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := problemhub.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := hil.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := contextgraph.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := knowledgeinstances.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := knowledgeontology.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := memoryontology.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := schedulerapp.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := rebac.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := datapolicy.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := ontologyreasoning.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		if err := ontologyscope.RegisterMigrations(registrar.Migrations()); err != nil {
			return err
		}
		core, err := registrar.Databases().Store(platform.DatabaseRoleCore)
		if err != nil {
			return err
		}
		if err := registrar.RegisterStatusMetrics(platformmetrics.StatusGroup{
			Subsystem: "ontology",
			Name:      "model_run_evaluation_alert_audits_total",
			Help:      "Ontology model run evaluation alert audit events by status.",
			Providers: map[string]platformmetrics.StatusCountsProvider{
				"core": ontology.NewPlatformModelRunEvaluationTrendAlertAuditStore(core),
			},
		}); err != nil {
			return err
		}
		if err := registrar.RegisterInitializer("ontology.defaults", func(ctx context.Context, init platform.InitializerContext) error {
			return ontology.InstallDefaultOntologyDefinitions(ctx, ontology.NewPlatformRegistry(core))
		}); err != nil {
			return err
		}
		return registrar.RegisterInitializer("ontology.action_defaults", func(ctx context.Context, init platform.InitializerContext) error {
			if err := actioncontract.InstallDefaultActionTypes(ctx, actioncontract.NewService(actioncontract.NewPlatformRepository(core))); err != nil {
				return err
			}
			return installDefaultActionObjectBindings(ctx, ontology.NewPlatformRegistry(core), actioncontract.DefaultActionTypes())
		})
	})
}

func installDefaultActionObjectBindings(ctx context.Context, registry ontology.Registry, actionTypes []actioncontract.ActionType) error {
	if registry == nil {
		return ontology.ErrInvalidDefinition
	}
	for _, actionType := range actionTypes {
		actionID := string(actionType.ID)
		for _, objectTypeID := range actionType.ObjectTypeRefs {
			objectType, err := registry.GetObjectType(ctx, objectTypeID)
			if err != nil {
				return err
			}
			if containsNormalizedID(objectType.AllowedActions, actionID) {
				continue
			}
			objectType.AllowedActions = append(objectType.AllowedActions, actionID)
			if err := registry.SaveObjectType(ctx, objectType); err != nil {
				return err
			}
		}
	}
	return nil
}

func containsNormalizedID(values []string, want string) bool {
	want = strings.TrimSpace(strings.ToLower(want))
	for _, value := range values {
		if strings.TrimSpace(strings.ToLower(value)) == want {
			return true
		}
	}
	return false
}
