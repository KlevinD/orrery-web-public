package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/testkit"
)

type backgroundResponsibilityPort struct {
	reconcile   func(context.Context) error
	cycles      int
	evaluations int
	request     schedulerapp.ResponsibilityEvaluationBatchRequest
}

func (p *backgroundResponsibilityPort) ReconcileResponsibilities(ctx context.Context, _ schedulerapp.ResponsibilityReconcileRequest) (schedulerapp.ResponsibilityReconcileResult, error) {
	p.cycles++
	if p.reconcile != nil {
		return schedulerapp.ResponsibilityReconcileResult{}, p.reconcile(ctx)
	}
	return schedulerapp.ResponsibilityReconcileResult{}, nil
}
func (p *backgroundResponsibilityPort) DiscoverResponsibilityEvaluations(_ context.Context, req schedulerapp.ResponsibilityEvaluationBatchRequest) ([]schedulerapp.ResponsibilityEvaluationBatchTarget, error) {
	p.request = req
	return nil, nil
}
func (p *backgroundResponsibilityPort) EvaluateResponsibilityBatch(_ context.Context, _ schedulerapp.ResponsibilityEvaluationBatchRequest, _ []schedulerapp.ResponsibilityEvaluationBatchTarget) ([]schedulerapp.ResponsibilityEvaluationBatchDecision, error) {
	p.evaluations++
	return nil, nil
}
func responsibilityBackgroundFixture(t *testing.T, port responsibilityBackgroundPort) (*productionResponsibilityBackground, *testkit.Env) {
	t.Helper()
	env := testkit.New(t)
	t.Cleanup(env.Close)
	rt := &platform.Runtime{Lock: env.Lock, Clock: env.Clock, Config: env.Config, Logger: env.Logger}
	rt.Config.Integrations.People.Enabled = true
	rt.Config.Integrations.People.OrreryTenantID = "tenant-1"
	rt.Config.Integrations.People.PeopleTenantID = "people-tenant"
	return &productionResponsibilityBackground{runtime: rt, port: port, wakes: responsibility.NewRepository(env.DBRouter, env.UnitOfWork)}, env
}

func TestProductionResponsibilityBackgroundEventRecoveryAndNoSchedule(t *testing.T) {
	port := &backgroundResponsibilityPort{}
	w, env := responsibilityBackgroundFixture(t, port)
	ctx := context.Background()
	saveResponsibilityEvaluationPortfolio(t, w.wakes, env.Clock.Now())
	if err := w.cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if port.cycles != 1 || port.evaluations != 1 || port.request.EvaluationID == "" || port.request.ScheduledRunID != "" || port.request.ScheduledTaskID != "" {
		t.Fatalf("port=%+v", port)
	}
	wakes, err := w.wakes.PendingRuntimeWakes(ctx)
	if err != nil || len(wakes) != 0 {
		t.Fatalf("ack wakes=%v err=%v", wakes, err)
	}
	if err := w.cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if port.cycles != 1 {
		t.Fatal("idle service repeated evaluation")
	}
	policy := responsibility.DefaultPolicy("tenant-1", "agent-1", responsibility.WorkKindMissionTask)
	if _, err := w.wakes.SavePolicy(ctx, policy, "test", "change", policy.Version, env.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	if err := w.cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if port.cycles != 2 {
		t.Fatal("committed event did not wake runtime")
	}
	env.Clock.Advance(time.Minute)
	if err := w.cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if port.cycles != 2 {
		t.Fatal("idle recovery ran after only one minute")
	}
	env.Clock.Advance(59*time.Minute - time.Second)
	if err := w.cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if port.cycles != 2 {
		t.Fatal("idle recovery ran before one hour")
	}
	env.Clock.Advance(time.Second)
	if err := w.cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if port.cycles != 3 {
		t.Fatal("hourly recovery scan did not run")
	}
	tasks, err := schedulerapp.NewRepository(env.DBRouter).ListTasks(ctx, true)
	if err != nil || len(tasks) != 0 {
		t.Fatalf("background service created schedules: %v err=%v", tasks, err)
	}
}

func TestProductionResponsibilityBackgroundFailureRetainsWakeAndLeaseExcludesOtherInstances(t *testing.T) {
	want := errors.New("temporarily unavailable")
	port := &backgroundResponsibilityPort{reconcile: func(context.Context) error { return want }}
	w, env := responsibilityBackgroundFixture(t, port)
	ctx := context.Background()
	saveResponsibilityEvaluationPortfolio(t, w.wakes, env.Clock.Now())
	lease, ok, err := env.Lock.TryAcquire(ctx, responsibilityBackgroundName, "other-instance", 90*time.Second, env.Clock.Now())
	if err != nil || !ok {
		t.Fatalf("acquire: %v %v", ok, err)
	}
	if err := w.cycle(ctx); err != nil {
		t.Fatal(err)
	}
	if port.cycles != 0 {
		t.Fatal("another instance's lease was ignored")
	}
	if err := lease.Release(ctx); err != nil {
		t.Fatal(err)
	}
	env.Clock.Advance(5 * time.Second)
	if err := w.cycle(ctx); !errors.Is(err, want) {
		t.Fatalf("cycle error=%v", err)
	}
	wakes, err := w.wakes.PendingRuntimeWakes(ctx)
	if err != nil || len(wakes) == 0 {
		t.Fatalf("failure lost event: %v %v", wakes, err)
	}
	port.reconcile = nil
	env.Clock.Advance(5 * time.Second)
	if err := w.cycle(ctx); err != nil {
		t.Fatal(err)
	}
	wakes, err = w.wakes.PendingRuntimeWakes(ctx)
	if err != nil || len(wakes) != 0 {
		t.Fatalf("retry did not acknowledge: %v %v", wakes, err)
	}
}

func TestProductionResponsibilityBackgroundRetiresOnlyOwnedSchedules(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := schedulerapp.NewRepository(env.DBRouter)
	service := schedulerapp.NewService(schedulerapp.ServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, Repository: repo})
	coordinator := defaultResponsibilityReconcileScheduledTask(env.Clock.Now())
	batch := testProductionResponsibilityBatchTask(productionResponsibilityBatchSchedule{orreryTenantID: "tenant-1", peopleTenantID: "people-tenant", cron: "0 9 * * *", timezone: "Asia/Shanghai"}, "batch")
	other := coordinator
	other.ScheduledTaskID = "user-managed"
	other.CreatedBy = "user-1"
	for _, task := range []schedulerapp.ScheduledTask{coordinator, batch, other} {
		if err := repo.SaveTask(ctx, task); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := retireResponsibilityRuntimeSchedules(ctx, repo, service); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{coordinator.ScheduledTaskID, batch.ScheduledTaskID} {
		task, err := repo.GetTask(ctx, id)
		if err != nil || task.Enabled || task.Status != schedulerapp.TaskStatusArchived {
			t.Fatalf("legacy=%+v err=%v", task, err)
		}
	}
	task, err := repo.GetTask(ctx, other.ScheduledTaskID)
	if err != nil || !task.Enabled {
		t.Fatalf("user task changed=%+v err=%v", task, err)
	}
}

func TestProductionResponsibilityBackgroundCancellationAndPanicRetainWake(t *testing.T) {
	started := make(chan struct{})
	port := &backgroundResponsibilityPort{reconcile: func(ctx context.Context) error { close(started); <-ctx.Done(); return ctx.Err() }}
	w, env := responsibilityBackgroundFixture(t, port)
	saveResponsibilityEvaluationPortfolio(t, w.wakes, env.Clock.Now())
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.cycle(ctx) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("cycle did not start")
	}
	other := &productionResponsibilityBackground{runtime: w.runtime, port: &backgroundResponsibilityPort{}, wakes: w.wakes}
	if err := other.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if other.port.(*backgroundResponsibilityPort).cycles != 0 {
		t.Fatal("concurrent worker entered held lease")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error=%v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("canceled cycle did not stop")
	}
	env.Clock.Advance(5 * time.Second)
	port.reconcile = func(context.Context) error { panic("broken integration") }
	if err := w.cycle(context.Background()); err == nil {
		t.Fatal("panic was not contained")
	}
	wakes, err := w.wakes.PendingRuntimeWakes(context.Background())
	if err != nil || len(wakes) == 0 {
		t.Fatalf("wake lost after cancellation/panic: %v %v", wakes, err)
	}
	port.reconcile = nil
	env.Clock.Advance(10 * time.Second)
	if err := w.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestProductionResponsibilityBackgroundUsesRealPortfolioEvaluationWithoutRun(t *testing.T) {
	coordinator := &recordingResponsibilityCoordinator{}
	port := &productionSchedulerResponsibilityPort{coordinator: coordinator}
	w, env := responsibilityBackgroundFixture(t, port)
	port.repository = w.wakes
	saveResponsibilityEvaluationPortfolio(t, w.wakes, env.Clock.Now())
	if err := w.cycle(context.Background()); err != nil {
		t.Fatal(err)
	}
	if coordinator.calls != 1 {
		t.Fatal("existing coordinator was not reused")
	}
	wakes, err := w.wakes.PendingRuntimeWakes(context.Background())
	if err != nil || len(wakes) != 0 {
		t.Fatalf("real evaluation failed: %v %v", wakes, err)
	}
}
