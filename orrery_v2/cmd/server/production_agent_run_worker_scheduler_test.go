package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionAgentRunWorkerPersistsDeliveredFeishuSummary(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := workerTestProfile(t, "scheduled-summary-agent")
	profile.WorkspaceRoot = t.TempDir()
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	publicSummary := strings.Join([]string{
		"结论：技能安全扫描已完成，未发现需要立即处置的高风险问题；现有提示均与依赖说明有关，不代表已经执行安装。",
		"范围与数量：检查了 2 个技能，共定位 6 条依赖安装提示；严重问题 0 条，警告 0 条，普通提示 6 条。",
		"具体结果：",
		"1. ai-tech-collector 命中 1 条文档示例，只说明如何安装搜索依赖，没有发现运行时自动执行或修改环境的证据。",
		"2. aws-wechat-article-publish 命中 5 条运行时依赖提示，涉及 PyYAML 与 Pillow，但未发现泄露凭据或破坏性修改。",
		"限制与下一步：本次只判断静态引用，尚未验证依赖是否已预装，也未执行任何被扫描技能；负责人应按常规流程复核运行环境，无需紧急处置。",
	}, "\n")
	runtime := &capturingWorkerRuntime{response: agentcore.RuntimeResponse{
		Status: agentcore.RuntimeStatusCompleted,
		Messages: []agentcore.RuntimeMessage{{
			Type: agentcore.RuntimeMessageText, Status: agentcore.RuntimeMessageStatusFinal,
			Content: "```orrery-public-summary\n" + publicSummary + "\n```\n```orrery-agent-result\n{\"version\":1,\"outcome\":\"completed\",\"summary\":\"技能安全扫描已完成。\",\"deliverables\":[],\"evidence\":[]}\n```",
		}},
	}}
	run := workerTestRun(profile, "scheduled-summary-runtime-run", "scheduled-summary-mission", "scheduled-summary-task")
	run.Request.TenantID = "system"
	run.Request.RuntimeOptions.ProviderSpecific = map[string]string{
		"run_source":        schedulerAgentTaskRunSource,
		"scheduled_task_id": "scheduled-summary-task",
		"scheduled_run_id":  "scheduled-summary-run",
	}
	payload, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	scheduler := &capturingSchedulerRunLifecycle{}
	deliveryOutbox := &capturingScheduledDeliveryOutbox{}
	worker := productionAgentRunWorker{
		assembler: &runtimeCapturingWorkerAssembler{runtime: runtime}, profiles: profiles,
		lifecycle: &capturingAgentRunLifecycle{}, scheduler: scheduler,
		scheduledDeliveryOutbox: deliveryOutbox,
		schedulerTasks: scheduledTaskContractSourceFunc(func(context.Context, string) (schedulerapp.ScheduledTask, error) {
			return schedulerapp.ScheduledTask{
				ScheduledTaskID: "scheduled-summary-task", Title: "Skill security scan", CreatedBy: "system:orrery-native-migration",
				Delivery: schedulerapp.DeliveryPolicy{Mode: "announce", Channel: "feishu", AccountID: "orrery", RecipientID: "ou_owner", Format: "text"},
			}, nil
		}),
	}

	if err := worker.HandleExecution(ctx, platform.ExecutionDelivery{Topic: runtimedaemon.AgentRunTopic, Payload: payload, MaxAttempts: 1}); err != nil {
		t.Fatalf("HandleExecution: %v", err)
	}
	if deliveryOutbox.calls != 1 || deliveryOutbox.runResult.ScheduledRunID != "scheduled-summary-run" {
		t.Fatalf("scheduled delivery = %+v, calls=%d", deliveryOutbox.runResult, deliveryOutbox.calls)
	}
	if scheduler.completedCalls != 1 || scheduler.completedResult.Summary != publicSummary {
		t.Fatalf("persisted scheduled result = %+v, calls=%d", scheduler.completedResult, scheduler.completedCalls)
	}
}

func TestCompleteCommittedScheduledResultPersistsBeforeSchedulingDelivery(t *testing.T) {
	run := runtimedaemon.AgentRun{
		RunID: "runtime-delivery-once", AgentID: "agent-delivery-once", MissionID: "mission-delivery-once", TaskID: "task-delivery-once",
		Request: agentcore.RuntimeRequest{TenantID: "system", RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
			"run_source": schedulerAgentTaskRunSource, "scheduled_task_id": "scheduled-delivery-once", "scheduled_run_id": "scheduled-run-delivery-once",
			"delivery_mode": "announce", "delivery_channel": "feishu", "delivery_account_id": "orrery", "delivery_recipient_id": "ou_owner",
		}}},
	}
	result := agentcore.AgentResult{
		ID: "result-delivery-once", RunID: run.RunID, AgentID: run.AgentID, MissionID: "mission-delivery-once", TaskID: "task-delivery-once",
		Summary: agentcore.Summary{Text: "durable result"},
	}
	scheduleErr := errors.New("delivery outbox unavailable")
	scheduler := &capturingSchedulerRunLifecycle{}
	deliveryOutbox := &capturingScheduledDeliveryOutbox{err: scheduleErr}
	worker := productionAgentRunWorker{
		scheduler: scheduler, scheduledDeliveryOutbox: deliveryOutbox,
	}
	if err := worker.completeCommittedScheduledResult(context.Background(), run, result, agentcore.RuntimeResponse{}, "worker-1"); !errors.Is(err, scheduleErr) {
		t.Fatalf("first completion error = %v, want delivery schedule failure", err)
	}
	if scheduler.completedCalls != 1 {
		t.Fatalf("scheduler completion calls=%d, want result persisted before the failed delivery enqueue", scheduler.completedCalls)
	}
	deliveryOutbox.err = nil
	if err := worker.completeCommittedScheduledResult(context.Background(), run, result, agentcore.RuntimeResponse{}, "worker-2"); err != nil {
		t.Fatalf("retry completion: %v", err)
	}
	if deliveryOutbox.calls != 2 || scheduler.completedCalls != 2 {
		t.Fatalf("delivery schedules=%d completions=%d, want idempotent retry attempts 2/2", deliveryOutbox.calls, scheduler.completedCalls)
	}
	if deliveryOutbox.runResult.RuntimeRunID != run.RunID.String() {
		t.Fatalf("scheduled delivery runtime run ID = %q, want %q", deliveryOutbox.runResult.RuntimeRunID, run.RunID)
	}
}

func TestCompleteCommittedScheduledResultRecordsResponsibilityUsage(t *testing.T) {
	run := runtimedaemon.AgentRun{
		RunID: "runtime-usage", AgentID: "agent-usage", MissionID: "mission-usage", TaskID: "task-usage",
		Request: agentcore.RuntimeRequest{TenantID: "system", RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
			"run_source": schedulerAgentTaskRunSource, "scheduled_task_id": "scheduled-usage", "scheduled_run_id": "scheduled-run-usage",
		}}},
	}
	result := agentcore.AgentResult{
		ID: "result-usage", RunID: run.RunID, AgentID: run.AgentID, MissionID: "mission-usage", TaskID: "task-usage",
		Summary: agentcore.Summary{Text: "completed with usage"}, InternalTraceRef: "trace-usage",
	}
	response := agentcore.RuntimeResponse{Usage: map[string]agentcore.TokenUsage{
		"model-a": {InputTokens: 11, OutputTokens: 7, CacheReadTokens: 5, CacheWriteTokens: 2},
	}}
	responsibility := &capturingResponsibilityRunLifecycle{}
	worker := productionAgentRunWorker{scheduler: &capturingSchedulerRunLifecycle{}, responsibility: responsibility}

	if err := worker.completeCommittedScheduledResult(context.Background(), run, result, response, "worker-usage"); err != nil {
		t.Fatalf("complete scheduled result: %v", err)
	}
	if responsibility.completedCalls != 1 || responsibility.completedUsage != 25 || responsibility.completedTrace != "trace-usage" {
		t.Fatalf("responsibility completion = %+v", responsibility)
	}
}

func TestProductionAgentRunWorkerKeepsScheduledRunActiveWhileResponsibilityRetriesTimeout(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := workerTestProfile(t, "scheduled-timeout-agent")
	saveAvailableAgentProfile(t, ctx, profiles, profile)
	run := workerTestRun(profile, "scheduled-runtime-run", "scheduled-mission", "scheduled-mission-task")
	run.Request.RuntimeOptions.WorkingDirectory = t.TempDir()
	run.Request.RuntimeOptions.ProviderSpecific = map[string]string{
		"run_source":        schedulerAgentTaskRunSource,
		"scheduled_task_id": "scheduled-task-timeout",
		"scheduled_run_id":  "scheduled-run-timeout",
	}
	payload, err := json.Marshal(run)
	if err != nil {
		t.Fatal(err)
	}
	scheduler := &capturingSchedulerRunLifecycle{}
	responsibility := &capturingResponsibilityRunLifecycle{}
	worker := productionAgentRunWorker{
		assembler: &runtimeCapturingWorkerAssembler{runtime: &capturingWorkerRuntime{err: agentcore.RuntimePipelineError{
			Status: agentcore.RuntimeStatusTimeout, Message: "runtime deadline exceeded",
		}}},
		profiles: profiles, lifecycle: &capturingAgentRunLifecycle{}, scheduler: scheduler, responsibility: responsibility,
		schedulerTasks: scheduledTaskContractSourceFunc(func(context.Context, string) (schedulerapp.ScheduledTask, error) {
			return schedulerapp.ScheduledTask{ScheduledTaskID: "scheduled-task-timeout"}, nil
		}),
	}

	if err := worker.HandleExecution(ctx, platform.ExecutionDelivery{Topic: runtimedaemon.AgentRunTopic, Payload: payload, MaxAttempts: 1}); err != nil {
		t.Fatalf("HandleExecution: %v", err)
	}
	if scheduler.failedCalls != 1 || scheduler.failureTerminal {
		t.Fatalf("scheduler failure = calls=%d terminal=%v, want one non-terminal update", scheduler.failedCalls, scheduler.failureTerminal)
	}
	if responsibility.failedCalls != 1 {
		t.Fatalf("responsibility failures = %d, want 1", responsibility.failedCalls)
	}

	scheduler = &capturingSchedulerRunLifecycle{}
	responsibility = &capturingResponsibilityRunLifecycle{err: errors.New("responsibility unavailable")}
	worker.scheduler = scheduler
	worker.responsibility = responsibility
	if err := worker.HandleExecution(ctx, platform.ExecutionDelivery{Topic: runtimedaemon.AgentRunTopic, Payload: payload, MaxAttempts: 1}); err != nil {
		t.Fatalf("HandleExecution with responsibility failure: %v", err)
	}
	if scheduler.failedCalls != 1 || !scheduler.failureTerminal {
		t.Fatalf("scheduler fallback failure = calls=%d terminal=%v, want one terminal update", scheduler.failedCalls, scheduler.failureTerminal)
	}
}

type capturingSchedulerRunLifecycle struct {
	failedCalls     int
	failureTerminal bool
	completedCalls  int
	completedResult schedulerapp.RunResult
	deliveryCalls   int
	deliveryStatus  string
	deliveryErr     error
	blockedCalls    int
	blockedResult   schedulerapp.RunResult
	blockedReason   string
}

func (*capturingSchedulerRunLifecycle) Started(context.Context, string, int) error { return nil }

func (c *capturingSchedulerRunLifecycle) AttemptFailed(_ context.Context, _ string, _ int, terminal bool, _ error) error {
	c.failedCalls++
	c.failureTerminal = terminal
	return nil
}

func (c *capturingSchedulerRunLifecycle) CompleteAgentRun(_ context.Context, _ schedulerapp.ScheduledRun, result schedulerapp.RunResult, _ string, _ time.Time) error {
	c.completedCalls++
	c.completedResult = result
	return nil
}

func TestCompleteCommittedScheduledResultPersistsBlockedReportAsFailure(t *testing.T) {
	run := runtimedaemon.AgentRun{
		RunID: "runtime-blocked-result", AgentID: "agent-blocked-result", MissionID: "mission-blocked-result", TaskID: "task-blocked-result",
		Request: agentcore.RuntimeRequest{TenantID: "minvia", RuntimeOptions: agentcore.RuntimeOptions{ProviderSpecific: map[string]string{
			"run_source": schedulerAgentTaskRunSource, "scheduled_task_id": "scheduled-blocked-result", "scheduled_run_id": "scheduled-run-blocked-result",
		}}},
	}
	result := agentcore.AgentResult{
		ID: "result-blocked", RunID: run.RunID, AgentID: run.AgentID, MissionID: "mission-blocked-result", TaskID: "task-blocked-result",
		Summary: agentcore.Summary{Text: "Ontology association is still pending."},
		Blocker: &agentcore.Blocker{Summary: "Ontology association is still pending."},
	}
	scheduler := &capturingSchedulerRunLifecycle{}
	responsibility := &capturingResponsibilityRunLifecycle{}
	worker := productionAgentRunWorker{scheduler: scheduler, responsibility: responsibility}

	response := agentcore.RuntimeResponse{Usage: map[string]agentcore.TokenUsage{"model-a": {InputTokens: 20, OutputTokens: 10}}}
	if err := worker.completeCommittedScheduledResult(context.Background(), run, result, response, "worker-blocked"); err != nil {
		t.Fatalf("complete blocked result: %v", err)
	}
	if scheduler.completedCalls != 0 || scheduler.blockedCalls != 1 || scheduler.blockedResult.Summary != result.Summary.Text || scheduler.blockedReason != result.Blocker.Summary {
		t.Fatalf("scheduler blocked result = %+v", scheduler)
	}
	if responsibility.failedCalls != 1 {
		t.Fatalf("responsibility failed calls = %d, want 1", responsibility.failedCalls)
	}
	if responsibility.failedUsage != 30 || responsibility.failedKind != runtimedaemon.RuntimeLifecycleBlocked {
		t.Fatalf("responsibility failure = %+v", responsibility)
	}
}

func (c *capturingSchedulerRunLifecycle) FailAgentRun(_ context.Context, _ schedulerapp.ScheduledRun, result schedulerapp.RunResult, _ string, _ time.Time, cause error) error {
	c.blockedCalls++
	c.blockedResult = result
	if cause != nil {
		c.blockedReason = cause.Error()
	}
	return nil
}

func (c *capturingSchedulerRunLifecycle) RecordDelivery(_ context.Context, _ string, status, _, _ string) error {
	c.deliveryCalls++
	if c.deliveryErr != nil {
		return c.deliveryErr
	}
	c.deliveryStatus = status
	return nil
}

type capturingScheduledDeliveryOutbox struct {
	calls     int
	runResult schedulerapp.ScheduledRun
	err       error
}

func (o *capturingScheduledDeliveryOutbox) Schedule(_ context.Context, _ runtimedaemon.AgentRun, run schedulerapp.ScheduledRun) error {
	o.calls++
	o.runResult = run
	return o.err
}

type capturingResponsibilityRunLifecycle struct {
	completedCalls int
	completedUsage int64
	completedTrace string
	failedCalls    int
	failedUsage    int64
	failedKind     runtimedaemon.RuntimeLifecycleKind
	err            error
}

func (*capturingResponsibilityRunLifecycle) Completed(context.Context, string, string, string) error {
	return nil
}

func (c *capturingResponsibilityRunLifecycle) Failed(context.Context, string, string, string, runtimedaemon.RuntimeLifecycleKind, error) error {
	c.failedCalls++
	return c.err
}

func (c *capturingResponsibilityRunLifecycle) CompletedWithTraceAndUsage(_ context.Context, _, _, _, traceRef string, usageTokens int64) error {
	c.completedCalls++
	c.completedTrace = traceRef
	c.completedUsage = usageTokens
	return c.err
}

func (c *capturingResponsibilityRunLifecycle) FailedWithTraceAndUsage(_ context.Context, _, _, _ string, kind runtimedaemon.RuntimeLifecycleKind, _ error, _ string, usageTokens int64) error {
	c.failedCalls++
	c.failedKind = kind
	c.failedUsage = usageTokens
	return c.err
}
