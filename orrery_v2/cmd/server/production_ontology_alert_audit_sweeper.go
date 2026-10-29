package main

import (
	"fmt"
	"time"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

const productionOntologyPolicyAlertAuditSweeperLookback = 15 * time.Minute
const productionOntologyPolicyAlertAuditSweeperTrendLimit = 100
const productionOntologyPolicyAlertAuditSweeperProfileLimit = 100
const productionOntologyPolicyAlertAuditSweeperMinFailures = 1

type productionOntologyPolicyAlertAuditSweeperConfig struct {
	Lookback     time.Duration
	TrendLimit   int
	ProfileLimit int
	MinFailures  int
}

func registerProductionOntologyPolicyAlertAuditSweeper(rt *platform.Runtime, core *db.Store) error {
	if rt == nil || core == nil {
		return fmt.Errorf("ontology policy alert audit sweeper requires runtime and core database")
	}
	cfg := productionOntologyPolicyAlertAuditSweeperConfigFromRuntime(rt)
	return rt.RegisterSweeper("ontology.model_run_evaluation_policy_alert_audit.recorder", ontology.NewModelRunEvaluationTrendPolicyAlertAuditSweeper(ontology.ModelRunEvaluationTrendPolicyAlertAuditSweeperOptions{
		Ledger:    ontology.NewPlatformModelRunLedger(core),
		Artifacts: ontology.NewPlatformModelRunEvaluationArtifactStore(core),
		Policies:  ontology.NewPlatformModelRunEvaluationPolicyProfileStore(core),
		Store:     ontology.NewPlatformModelRunEvaluationTrendAlertAuditStore(core),
		Clock:     rt.Clock,
		Lookback:  cfg.Lookback,
		Query: ontology.ModelRunEvaluationTrendQuery{
			Limit: cfg.TrendLimit,
		},
		ProfileQuery: ontology.ModelRunEvaluationPolicyProfileQuery{
			Limit: cfg.ProfileLimit,
		},
		MinFailures: cfg.MinFailures,
	}))
}

func productionOntologyPolicyAlertAuditSweeperConfigFromRuntime(rt *platform.Runtime) productionOntologyPolicyAlertAuditSweeperConfig {
	cfg := productionOntologyPolicyAlertAuditSweeperConfig{
		Lookback:     productionOntologyPolicyAlertAuditSweeperLookback,
		TrendLimit:   productionOntologyPolicyAlertAuditSweeperTrendLimit,
		ProfileLimit: productionOntologyPolicyAlertAuditSweeperProfileLimit,
		MinFailures:  productionOntologyPolicyAlertAuditSweeperMinFailures,
	}
	if rt == nil {
		return cfg
	}
	if lookback, ok := rt.Config.Duration("ontology.policy_alert_audit_sweeper.lookback"); ok && lookback > 0 {
		cfg.Lookback = lookback
	}
	if limit, ok := rt.Config.Int("ontology.policy_alert_audit_sweeper.trend_limit"); ok && limit > 0 {
		cfg.TrendLimit = limit
	}
	if limit, ok := rt.Config.Int("ontology.policy_alert_audit_sweeper.profile_limit"); ok && limit > 0 {
		cfg.ProfileLimit = limit
	}
	if minFailures, ok := rt.Config.Int("ontology.policy_alert_audit_sweeper.min_failures"); ok && minFailures > 0 {
		cfg.MinFailures = minFailures
	}
	return cfg
}
