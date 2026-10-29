package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform"
)

func TestLivePeopleResponsibilityCreatesOneExpandableEvaluationSchedule(t *testing.T) {
	if !strings.EqualFold(strings.TrimSpace(os.Getenv("ORRERY_PEOPLE_RESPONSIBILITY_LIVE_E2E")), "true") {
		t.Skip("set ORRERY_PEOPLE_RESPONSIBILITY_LIVE_E2E=true to run the live People responsibility e2e")
	}
	configPath := strings.TrimSpace(os.Getenv("ORRERY_PEOPLE_RESPONSIBILITY_CONFIG"))
	if configPath == "" {
		t.Fatal("ORRERY_PEOPLE_RESPONSIBILITY_CONFIG is required")
	}
	cfg, err := loadProductionConfig(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Integrations.People.Enabled || len(cfg.Integrations.People.AgentBindings) == 0 {
		t.Fatal("live People responsibility integration and at least one explicit Agent binding are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	rt, err := openProductionRuntime(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rt.Close() }()
	if err := rt.Initialize(ctx); err != nil {
		t.Fatal(err)
	}
	_, cleanupHandler, err := newProductionHandlerFromRuntime(ctx, rt)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanupHandler()

	scheduledTasks := schedulerapp.NewRepository(rt.DBRouter)
	task, err := scheduledTasks.GetTask(ctx, defaultResponsibilityReconcileScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	service := schedulerapp.NewService(schedulerapp.ServiceOptions{
		Clock: rt.Clock, IDGenerator: rt.IDGenerator, Repository: scheduledTasks,
	})
	viewer := schedulerapp.ViewerRef{
		TenantID: "system", ViewerID: "system:people-responsibility-live-e2e",
		ViewerKind: schedulerapp.ViewerKindSystem, ProfileID: defaultLeadAgentID,
	}
	run, err := service.PrepareManualRun(ctx, viewer, schedulerapp.RunNowRequest{
		ScheduledTaskID: task.ScheduledTaskID,
		Force:           true,
		IdempotencyKey:  fmt.Sprintf("people-responsibility-live-e2e:%d", time.Now().UTC().UnixNano()),
	})
	if err != nil {
		t.Fatal(err)
	}
	dispatcher := productionSchedulerDispatcher{queue: rt.ExecutionQueue, repo: scheduledTasks, clock: rt.Clock}
	if _, err := dispatcher.DispatchRun(ctx, task, run); err != nil {
		t.Fatal(err)
	}
	runnerCtx, stopRunner := context.WithCancel(ctx)
	defer stopRunner()
	if err := rt.StartExecutionRunner(runnerCtx, platform.ExecutionRunnerRuntimeConfig{
		WorkerID: "people-responsibility-live-e2e", PollInterval: 10 * time.Millisecond,
	}); err != nil {
		t.Fatal(err)
	}

	completed := waitForLivePeopleResponsibilityRun(t, ctx, scheduledTasks, run.ScheduledRunID)
	stopRunner()
	if completed.Status != schedulerapp.RunStatusCompleted || completed.ResponsibilityReconcile == nil {
		t.Fatalf("run = %+v", completed)
	}
	result := completed.ResponsibilityReconcile
	if !result.PeopleSourceAttempted && !result.PeopleSourceThrottled {
		t.Fatalf("People source state was not recorded: %+v", result)
	}
	if result.PeopleFailed != 0 {
		t.Fatalf("People reconciliation failed: %+v", result)
	}

	responsibilities := responsibility.NewRepository(rt.DBRouter, rt.UnitOfWork)
	allTasks, err := scheduledTasks.ListTasks(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for subjectID, agentProfileID := range cfg.Integrations.People.AgentBindings {
		bindingID := responsibility.PeopleAgentBindingID(
			cfg.Integrations.People.OrreryTenantID,
			cfg.Integrations.People.PeopleTenantID,
			subjectID,
		)
		portfolio, found, err := responsibilities.GetPeopleResponsibilityPortfolioByBindingID(ctx, bindingID)
		if err != nil || !found {
			t.Fatalf("portfolio for %s found=%v err=%v", subjectID, found, err)
		}
		if portfolio.AgentProfileID != agentProfileID || portfolio.SourceRevision == "" ||
			len(portfolio.Responsibilities) == 0 {
			t.Fatalf("portfolio for %s = %+v", subjectID, portfolio)
		}
		audits, err := responsibilities.ListAudits(ctx, cfg.Integrations.People.OrreryTenantID, bindingID, 20)
		if err != nil || len(audits) == 0 {
			t.Fatalf("audits for %s = %+v err=%v", subjectID, audits, err)
		}
	}
	batchTasks := 0
	for _, task := range allTasks {
		if task.Target.Mode != schedulerapp.TargetModeResponsibilityEvaluation || !task.Enabled || task.Status != schedulerapp.TaskStatusEnabled {
			continue
		}
		if task.Target.SourceScopeKind != schedulerapp.ResponsibilityEvaluationPeoplePortfoliosScope ||
			task.Target.SourceScopeID != cfg.Integrations.People.PeopleTenantID || task.Target.AgentID != "" ||
			task.MissionID != "" || task.Target.TaskGoal != "" || task.Target.Prompt != "" {
			t.Fatalf("unexpected enabled responsibility evaluation task: %+v", task)
		}
		batchTasks++
	}
	if batchTasks != 1 {
		t.Fatalf("enabled responsibility evaluation batch tasks = %d, want 1", batchTasks)
	}
}

func waitForLivePeopleResponsibilityRun(
	t *testing.T,
	ctx context.Context,
	repository *schedulerapp.Repository,
	runID string,
) schedulerapp.ScheduledRun {
	t.Helper()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		run, err := repository.GetRun(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		switch run.Status {
		case schedulerapp.RunStatusCompleted, schedulerapp.RunStatusFailed, schedulerapp.RunStatusSkipped:
			return run
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for live People responsibility run: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}
