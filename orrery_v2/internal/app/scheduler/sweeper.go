package scheduler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"orrery_v2/internal/platform/clock"
)

type SweeperOptions struct {
	Service      *Service
	Repository   *Repository
	Dispatcher   RunDispatcher
	Clock        clock.Clock
	BatchSize    int
	Reconciler   RunReconciler
	MisfireGrace time.Duration
}

type RunReconciler interface {
	Reconcile(context.Context) error
}

type Sweeper struct {
	service      *Service
	repo         *Repository
	dispatcher   RunDispatcher
	clock        clock.Clock
	batchSize    int
	reconciler   RunReconciler
	misfireGrace time.Duration
}

func NewSweeper(opts SweeperOptions) *Sweeper {
	sweepClock := opts.Clock
	if sweepClock == nil {
		sweepClock = clock.Real{}
	}
	batchSize := opts.BatchSize
	if batchSize <= 0 {
		batchSize = 100
	}
	return &Sweeper{
		service:      opts.Service,
		repo:         opts.Repository,
		dispatcher:   opts.Dispatcher,
		clock:        sweepClock,
		batchSize:    batchSize,
		reconciler:   opts.Reconciler,
		misfireGrace: opts.MisfireGrace,
	}
}

func (s *Sweeper) Name() string {
	return "scheduler.scheduled_tasks"
}

func (s *Sweeper) Sweep(ctx context.Context) error {
	if s == nil || s.service == nil || s.repo == nil || s.dispatcher == nil || s.clock == nil {
		return ErrServiceDependency
	}
	var sweepErrors []error
	now := s.clock.Now().UTC()
	_, err := s.service.ReconcileDisabledOccurrences(ctx, now)
	if err != nil {
		return fmt.Errorf("reconcile disabled occurrences: %w", err)
	}
	if s.reconciler != nil {
		if err := s.reconciler.Reconcile(ctx); err != nil {
			sweepErrors = append(sweepErrors, fmt.Errorf("reconcile active runs: %w", err))
		}
	}
	due, err := s.repo.ListDueTasks(ctx, now, s.batchSize)
	if err != nil {
		return err
	}
	for _, task := range due {
		if err := s.service.ProcessDueTask(ctx, task.ScheduledTaskID, s.dispatcher, now, s.misfireGrace); err != nil {
			sweepErrors = append(sweepErrors, fmt.Errorf("process task %s: %w", task.ScheduledTaskID, err))
		}
	}
	return errors.Join(sweepErrors...)
}
