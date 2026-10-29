package ontology

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/metrics"
)

const defaultModelRunEvaluationTrendMetricRecordingSweeperName = "ontology.model_run_evaluation_metric_recording.collector"

type ModelRunEvaluationTrendMetricRecordingSweeperOptions struct {
	Name     string
	Store    ModelRunEvaluationTrendMetricRecordingStore
	Sink     *metrics.BoundedSink
	Clock    clock.Clock
	Lookback time.Duration
	Query    ModelRunEvaluationTrendMetricRecordingQuery
}

type ModelRunEvaluationTrendMetricRecordingSweeper struct {
	name       string
	store      ModelRunEvaluationTrendMetricRecordingStore
	sink       *metrics.BoundedSink
	clock      clock.Clock
	lookback   time.Duration
	query      ModelRunEvaluationTrendMetricRecordingQuery
	lastReport ModelRunEvaluationTrendMetricRecordingCollectionReport
}

func NewModelRunEvaluationTrendMetricRecordingSweeper(opts ModelRunEvaluationTrendMetricRecordingSweeperOptions) *ModelRunEvaluationTrendMetricRecordingSweeper {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = defaultModelRunEvaluationTrendMetricRecordingSweeperName
	}
	sweepClock := opts.Clock
	if sweepClock == nil {
		sweepClock = clock.Real{}
	}
	return &ModelRunEvaluationTrendMetricRecordingSweeper{
		name:     name,
		store:    opts.Store,
		sink:     opts.Sink,
		clock:    sweepClock,
		lookback: opts.Lookback,
		query:    opts.Query,
	}
}

func (s *ModelRunEvaluationTrendMetricRecordingSweeper) Name() string {
	if s == nil || strings.TrimSpace(s.name) == "" {
		return defaultModelRunEvaluationTrendMetricRecordingSweeperName
	}
	return s.name
}

func (s *ModelRunEvaluationTrendMetricRecordingSweeper) Sweep(ctx context.Context) error {
	if s == nil || s.store == nil || s.sink == nil {
		return fmt.Errorf("%w: model run evaluation trend metric recording sweeper requires store and sink", ErrInvalidDefinition)
	}
	report, err := CollectAllModelRunEvaluationTrendMetricRecordings(ctx, s.store, s.sink, s.collectionQuery())
	if err != nil {
		return err
	}
	s.lastReport = report
	return nil
}

func (s *ModelRunEvaluationTrendMetricRecordingSweeper) LastReport() ModelRunEvaluationTrendMetricRecordingCollectionReport {
	if s == nil {
		return ModelRunEvaluationTrendMetricRecordingCollectionReport{}
	}
	return s.lastReport
}

func (s *ModelRunEvaluationTrendMetricRecordingSweeper) collectionQuery() ModelRunEvaluationTrendMetricRecordingQuery {
	query := s.query
	if s.lookback <= 0 || s.clock == nil {
		return query
	}
	now := s.clock.Now().UTC()
	query.RecordedFrom = now.Add(-s.lookback)
	query.RecordedTo = now
	return query
}
