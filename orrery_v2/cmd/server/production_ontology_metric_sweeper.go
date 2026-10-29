package main

import (
	"fmt"
	"time"

	"orrery_v2/internal/app/ontology"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/db"
)

const productionOntologyMetricRecordingSweeperLookback = 15 * time.Minute
const productionOntologyMetricRecordingSweeperLimit = 100

type productionOntologyMetricRecordingSweeperConfig struct {
	Lookback time.Duration
	Limit    int
}

func registerProductionOntologyMetricRecordingSweeper(rt *platform.Runtime, core *db.Store) error {
	if rt == nil || core == nil {
		return fmt.Errorf("ontology metric recording sweeper requires runtime and core database")
	}
	cfg := productionOntologyMetricRecordingSweeperConfigFromRuntime(rt)
	return rt.RegisterSweeper("ontology.model_run_evaluation_metric_recording.collector", ontology.NewModelRunEvaluationTrendMetricRecordingSweeper(ontology.ModelRunEvaluationTrendMetricRecordingSweeperOptions{
		Store:    ontology.NewPlatformModelRunEvaluationTrendMetricRecordingStore(core),
		Sink:     rt.Metrics,
		Clock:    rt.Clock,
		Lookback: cfg.Lookback,
		Query: ontology.ModelRunEvaluationTrendMetricRecordingQuery{
			Limit: cfg.Limit,
		},
	}))
}

func productionOntologyMetricRecordingSweeperConfigFromRuntime(rt *platform.Runtime) productionOntologyMetricRecordingSweeperConfig {
	cfg := productionOntologyMetricRecordingSweeperConfig{
		Lookback: productionOntologyMetricRecordingSweeperLookback,
		Limit:    productionOntologyMetricRecordingSweeperLimit,
	}
	if rt == nil {
		return cfg
	}
	if lookback, ok := rt.Config.Duration("ontology.metric_recording_sweeper.lookback"); ok && lookback > 0 {
		cfg.Lookback = lookback
	}
	if limit, ok := rt.Config.Int("ontology.metric_recording_sweeper.limit"); ok && limit > 0 {
		cfg.Limit = limit
	}
	return cfg
}
