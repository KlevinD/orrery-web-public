package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"

	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/logging"
)

const responsibilityBackgroundName = "responsibility.runtime"
const defaultResponsibilityReconcileScheduledTaskID = "agent-responsibility-periodic-reconcile"

func configureProductionResponsibilityRuntime(ctx context.Context, rt *platform.Runtime, profiles responsibility.AgentProfilePort,
	repository *responsibility.Repository, tasks *schedulerapp.Repository, scheduler *schedulerapp.Service, port *productionSchedulerResponsibilityPort) error {
	if err := configureProductionPeopleResponsibilityReconciler(ctx, rt, profiles, repository, tasks, scheduler, port); err != nil {
		return err
	}
	if err := registerProductionResponsibilityBackground(ctx, rt, port); err != nil {
		return err
	}
	return retireResponsibilityRuntimeSchedules(ctx, tasks, scheduler)
}

type responsibilityBackgroundPort interface {
	ReconcileResponsibilities(context.Context, schedulerapp.ResponsibilityReconcileRequest) (schedulerapp.ResponsibilityReconcileResult, error)
	DiscoverResponsibilityEvaluations(context.Context, schedulerapp.ResponsibilityEvaluationBatchRequest) ([]schedulerapp.ResponsibilityEvaluationBatchTarget, error)
	EvaluateResponsibilityBatch(context.Context, schedulerapp.ResponsibilityEvaluationBatchRequest, []schedulerapp.ResponsibilityEvaluationBatchTarget) ([]schedulerapp.ResponsibilityEvaluationBatchDecision, error)
}

type productionResponsibilityBackground struct {
	runtime      *platform.Runtime
	port         responsibilityBackgroundPort
	mu           sync.Mutex
	wakes        *responsibility.Repository
	nextRecovery time.Time
	nextAttempt  time.Time
	failures     int
	cancel       context.CancelFunc
	done         chan struct{}
	poke         chan struct{}
}

// The platform owns the worker lifecycle. Neither
// entry creates scheduled-task occurrences or sends a public result message.
func registerProductionResponsibilityBackground(ctx context.Context, rt *platform.Runtime, port responsibilityBackgroundPort) error {
	if rt == nil || rt.Clock == nil || rt.Lock == nil || rt.DBRouter == nil || port == nil {
		return responsibility.ErrInvalidContract
	}
	workerCtx, cancel := context.WithCancel(ctx)
	worker := &productionResponsibilityBackground{runtime: rt, port: port,
		wakes: responsibility.NewRepository(rt.DBRouter, rt.UnitOfWork), cancel: cancel, done: make(chan struct{}), poke: make(chan struct{}, 1)}
	if err := rt.RegisterSweeper(responsibilityBackgroundName, worker); err != nil {
		cancel()
		return err
	}
	go func() {
		defer close(worker.done)
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			_ = worker.cycle(workerCtx)
			select {
			case <-workerCtx.Done():
				return
			case <-ticker.C:
			case <-worker.poke:
			}
		}
	}()
	return nil
}

func (w *productionResponsibilityBackground) Close() error {
	w.cancel()
	select {
	case <-w.done:
		return nil
	case <-time.After(5 * time.Second):
		return fmt.Errorf("responsibility runtime shutdown timed out")
	}
}

type responsibilityRuntimeBusy struct{}

func (responsibilityRuntimeBusy) Error() string {
	return "responsibility runtime cycle is already active"
}
func (responsibilityRuntimeBusy) RetryDelay() time.Duration { return time.Second }

func (w *productionResponsibilityBackground) Sweep(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case w.poke <- struct{}{}:
	default:
	}
	return nil
}

func (w *productionResponsibilityBackground) cycle(ctx context.Context) error {
	err := w.run(ctx)
	var busy responsibilityRuntimeBusy
	if errors.As(err, &busy) {
		return nil
	}
	return err
}

func (w *productionResponsibilityBackground) run(ctx context.Context) (resultErr error) {
	if w == nil || w.runtime == nil || w.port == nil {
		return responsibility.ErrInvalidContract
	}
	if !w.mu.TryLock() {
		return responsibilityRuntimeBusy{}
	}
	defer w.mu.Unlock()
	defer func() {
		if recovered := recover(); recovered != nil {
			resultErr = fmt.Errorf("responsibility runtime panic: %v", recovered)
		}
		var busy responsibilityRuntimeBusy
		if resultErr != nil && !errors.As(resultErr, &busy) {
			w.failures++
			delay := 5 * time.Second
			for i := 1; i < w.failures && delay < time.Minute; i++ {
				delay *= 2
			}
			if delay > time.Minute {
				delay = time.Minute
			}
			w.nextAttempt = w.runtime.Clock.Now().UTC().Add(delay)
			if w.runtime.Logger != nil {
				w.runtime.Logger.Warn(ctx, "responsibility runtime will retry", logging.String("component", responsibilityBackgroundName), logging.Any("error", resultErr), logging.String("retry_after", delay.String()))
			}
		}
	}()
	now := w.runtime.Clock.Now().UTC()
	if now.Before(w.nextAttempt) {
		return nil
	}
	wakes, err := w.wakes.PendingRuntimeWakes(ctx)
	if err != nil {
		return err
	}
	if len(wakes) == 0 && now.Before(w.nextRecovery) {
		return nil
	}
	cause := "recovery"
	if len(wakes) > 0 {
		cause = "event"
	}
	w.nextAttempt = now.Add(5 * time.Second)
	ctx, cancel := context.WithTimeout(responsibility.RuntimeCycleContext(ctx), time.Minute)
	defer cancel()
	rt := w.runtime
	lease, acquired, err := rt.Lock.TryAcquire(ctx, responsibilityBackgroundName, uuid.NewString(), 90*time.Second, rt.Clock.Now())
	if err != nil {
		return err
	}
	if !acquired {
		return responsibilityRuntimeBusy{}
	}
	defer func() {
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer releaseCancel()
		_ = lease.Release(releaseCtx)
	}()
	started := rt.Clock.Now().UTC()
	stats, reconcileErr := w.port.ReconcileResponsibilities(ctx, schedulerapp.ResponsibilityReconcileRequest{TriggeredAt: started})
	var evaluationErr error
	evaluated := 0
	if cfg := rt.Config.Integrations.People; cfg.Enabled {
		request := schedulerapp.ResponsibilityEvaluationBatchRequest{
			TenantID: cfg.OrreryTenantID, PeopleTenantID: cfg.PeopleTenantID, TriggeredAt: started, EvaluationID: uuid.NewString(),
		}
		targets, err := w.port.DiscoverResponsibilityEvaluations(ctx, request)
		if err != nil {
			evaluationErr = err
		} else {
			decisions, err := w.port.EvaluateResponsibilityBatch(ctx, request, targets)
			evaluationErr = err
			if err == nil && len(decisions) != len(targets) {
				evaluationErr = fmt.Errorf("responsibility runtime evaluation returned an incomplete batch")
			}
			for _, decision := range decisions {
				evaluated++
				evaluationErr = errors.Join(evaluationErr, decision.Err)
				if decision.Decision.Action != nil {
					evaluationErr = errors.Join(evaluationErr, fmt.Errorf("responsibility runtime evaluation cannot execute an unapproved action"))
				}
			}
		}
	}
	err = errors.Join(reconcileErr, evaluationErr)
	if err == nil {
		err = w.wakes.AcknowledgeRuntimeWakes(ctx, wakes)
		if err == nil {
			w.nextRecovery = rt.Clock.Now().UTC().Add(time.Hour)
			w.nextAttempt = time.Time{}
			w.failures = 0
		}
	}
	if rt.Logger != nil {
		fields := []logging.Field{logging.String("component", responsibilityBackgroundName), logging.String("cause", cause),
			logging.Int("evaluated", evaluated), logging.Int("dispatched", stats.Dispatched), logging.Int("failed", stats.Failed)}
		if err != nil {
			rt.Logger.Warn(ctx, "responsibility runtime cycle failed", append(fields, logging.Any("error", err))...)
		} else {
			rt.Logger.Info(ctx, "responsibility runtime cycle completed", fields...)
		}
	}
	return err
}

// Keep historical definitions and runs, but remove the obsolete dispatch
// entries only after the replacement worker has been registered successfully.
func retireResponsibilityRuntimeSchedules(ctx context.Context, repo *schedulerapp.Repository, service *schedulerapp.Service) error {
	tasks, err := repo.ListTasks(ctx, true)
	if err != nil {
		return err
	}
	for _, task := range tasks {
		ownedReconcile := task.ScheduledTaskID == defaultResponsibilityReconcileScheduledTaskID &&
			task.CreatedBy == "system:responsibility-coordinator-bootstrap" && task.CreatedByKind == schedulerapp.ViewerKindSystem &&
			task.Target.Mode == schedulerapp.TargetModeResponsibilityReconcile
		if !ownedReconcile && !productionResponsibilityBatchTaskIdentity(task) && !legacyResponsibilityPulseIdentity(task) {
			continue
		}
		if task.Status == schedulerapp.TaskStatusArchived && !task.Enabled {
			continue
		}
		if _, err := service.ArchiveSystemManagedTask(ctx, managedTaskViewer(task), schedulerapp.CancelTaskRequest{ScheduledTaskID: task.ScheduledTaskID}); err != nil {
			return err
		}
	}
	return nil
}
