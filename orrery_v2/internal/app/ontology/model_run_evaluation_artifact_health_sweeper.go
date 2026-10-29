package ontology

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orrery_v2/internal/platform/clock"
)

const defaultModelRunEvaluationArtifactHealthProbeSweeperName = "ontology.model_run_evaluation_artifact_health.prober"

type ModelRunEvaluationArtifactHealthProbeSweeperOptions struct {
	Name      string
	Store     ModelRunEvaluationArtifactStore
	Clock     clock.Clock
	Query     ModelRunEvaluationArtifactQuery
	Providers map[string]ModelRunEvaluationArtifactProviderProbe
}

type ModelRunEvaluationArtifactHealthProbeSweepReport struct {
	Probed  int `json:"probed"`
	Updated int `json:"updated"`
	Pages   int `json:"pages"`
}

type ModelRunEvaluationArtifactHealthProbeSweeper struct {
	name       string
	store      ModelRunEvaluationArtifactStore
	clock      clock.Clock
	query      ModelRunEvaluationArtifactQuery
	providers  map[string]ModelRunEvaluationArtifactProviderProbe
	lastReport ModelRunEvaluationArtifactHealthProbeSweepReport
}

func NewModelRunEvaluationArtifactHealthProbeSweeper(opts ModelRunEvaluationArtifactHealthProbeSweeperOptions) *ModelRunEvaluationArtifactHealthProbeSweeper {
	name := strings.TrimSpace(opts.Name)
	if name == "" {
		name = defaultModelRunEvaluationArtifactHealthProbeSweeperName
	}
	sweepClock := opts.Clock
	if sweepClock == nil {
		sweepClock = clock.Real{}
	}
	providers := map[string]ModelRunEvaluationArtifactProviderProbe{}
	for name, probe := range opts.Providers {
		key := strings.TrimSpace(strings.ToLower(name))
		if key != "" && probe != nil {
			providers[key] = probe
		}
	}
	return &ModelRunEvaluationArtifactHealthProbeSweeper{
		name:      name,
		store:     opts.Store,
		clock:     sweepClock,
		query:     opts.Query,
		providers: providers,
	}
}

func (s *ModelRunEvaluationArtifactHealthProbeSweeper) Name() string {
	if s == nil || strings.TrimSpace(s.name) == "" {
		return defaultModelRunEvaluationArtifactHealthProbeSweeperName
	}
	return s.name
}

func (s *ModelRunEvaluationArtifactHealthProbeSweeper) Sweep(ctx context.Context) error {
	if s == nil || s.store == nil {
		return fmt.Errorf("%w: model run evaluation artifact health probe sweeper requires store", ErrInvalidDefinition)
	}
	report := ModelRunEvaluationArtifactHealthProbeSweepReport{}
	query := s.query
	for {
		page, err := s.store.ListModelRunEvaluationArtifacts(ctx, query)
		if err != nil {
			return err
		}
		report.Pages++
		for _, artifact := range page.Artifacts {
			if err := ctx.Err(); err != nil {
				return err
			}
			report.Probed++
			probed, err := ProbeModelRunEvaluationArtifactHealthWithProviders(artifact, s.now, s.providers)
			if err != nil {
				return err
			}
			if err := s.store.SaveModelRunEvaluationArtifact(ctx, probed); err != nil {
				return err
			}
			report.Updated++
		}
		if !page.Page.HasMore || len(page.Artifacts) == 0 {
			break
		}
		query.Offset = page.Page.Offset + len(page.Artifacts)
	}
	s.lastReport = report
	return nil
}

func (s *ModelRunEvaluationArtifactHealthProbeSweeper) LastReport() ModelRunEvaluationArtifactHealthProbeSweepReport {
	if s == nil {
		return ModelRunEvaluationArtifactHealthProbeSweepReport{}
	}
	return s.lastReport
}

func (s *ModelRunEvaluationArtifactHealthProbeSweeper) now() time.Time {
	if s == nil || s.clock == nil {
		return time.Now().UTC()
	}
	return s.clock.Now().UTC()
}
