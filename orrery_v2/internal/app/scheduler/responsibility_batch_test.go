package scheduler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/testkit"
)

func TestServiceCreatesSystemResponsibilityEvaluationBatchWithoutExecutorArguments(t *testing.T) {
	service := newServiceForTest()
	systemViewer := schedulerTestViewer()
	systemViewer.ViewerKind = ViewerKindSystem
	request := CreateTaskRequest{
		Title:    "Evaluate active People responsibility portfolios",
		Schedule: Schedule{Type: ScheduleTypeCron, Cron: "0 * * * *", Timezone: "UTC"},
		Target: Target{
			Mode:            TargetModeResponsibilityEvaluation,
			SourceScopeKind: ResponsibilityEvaluationPeoplePortfoliosScope,
			SourceScopeID:   "people-tenant-1",
		},
		IdempotencyKey: "responsibility-evaluation-batch",
	}

	if _, err := service.CreateTask(context.Background(), schedulerTestViewer(), request); !errors.Is(err, ErrInvalidTarget) {
		t.Fatalf("human create error = %v, want ErrInvalidTarget", err)
	}
	created, err := service.CreateTask(context.Background(), systemViewer, request)
	if err != nil {
		t.Fatal(err)
	}
	if created.CreatedByKind != ViewerKindSystem || created.Target.SourceScopeKind != ResponsibilityEvaluationPeoplePortfoliosScope ||
		created.Target.SourceScopeID != "people-tenant-1" || created.Target.AgentID != "" {
		t.Fatalf("created batch task = %+v", created)
	}

	invalidTargets := []struct {
		name   string
		mutate func(*Target)
	}{
		{name: "Agent", mutate: func(target *Target) { target.AgentID = "agent-1" }},
		{name: "approver", mutate: func(target *Target) { target.ApprovalAssigneeID = "approver-1" }},
		{name: "empty scope", mutate: func(target *Target) { target.SourceScopeID = "" }},
		{name: "lead Agent", mutate: func(target *Target) { target.LeadAgentID = "lead-1" }},
		{name: "discussion", mutate: func(target *Target) { target.ConversationID = "discussion-1" }},
		{name: "initial members", mutate: func(target *Target) { target.InitialMemberIDs = []string{"member-1"} }},
		{name: "member additions", mutate: func(target *Target) { value := true; target.AllowMemberAdditions = &value }},
		{name: "message", mutate: func(target *Target) { target.Message = "evaluate" }},
		{name: "task goal", mutate: func(target *Target) { target.TaskGoal = "evaluate" }},
		{name: "prompt", mutate: func(target *Target) { target.Prompt = "evaluate" }},
		{name: "tool", mutate: func(target *Target) { target.ToolID = "tool-1" }},
		{name: "tool arguments", mutate: func(target *Target) { target.ToolArguments = map[string]any{"scope": "all"} }},
		{name: "memory topic", mutate: func(target *Target) { target.MemoryTopic = "responsibilities" }},
		{name: "target scope kind", mutate: func(target *Target) { target.TargetScopeKind = "organization" }},
		{name: "target scope id", mutate: func(target *Target) { target.TargetScopeID = "organization-1" }},
		{name: "memory limit", mutate: func(target *Target) { target.MemoryRecordLimit = 1 }},
		{name: "result contract", mutate: func(target *Target) {
			target.ResultContract.Artifacts = []ArtifactRequirement{{ID: "report", Kind: "report", PathPattern: "C:/tmp/report.md"}}
		}},
	}
	for index, test := range invalidTargets {
		t.Run(test.name, func(t *testing.T) {
			invalid := request
			invalid.IdempotencyKey = fmt.Sprintf("responsibility-evaluation-batch-invalid-%d", index)
			test.mutate(&invalid.Target)
			if _, err := service.CreateTask(context.Background(), systemViewer, invalid); !errors.Is(err, ErrInvalidTarget) {
				t.Fatalf("create error = %v, want ErrInvalidTarget for target %+v", err, invalid.Target)
			}
		})
	}
}

func TestDispatcherResponsibilityEvaluationBatchIsolatedFailureAndReplay(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-responsibility-batch", schedulerTestTime.Add(-time.Minute))
	task.TenantID = "tenant-system"
	task.CreatedByKind = ViewerKindSystem
	task.Target = Target{
		Mode:            TargetModeResponsibilityEvaluation,
		SourceScopeKind: ResponsibilityEvaluationPeoplePortfoliosScope,
		SourceScopeID:   "people-tenant-1",
	}
	run := schedulerTestRun("run-responsibility-batch", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	run.TenantID = task.TenantID
	if err := repository.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	targets := []ResponsibilityEvaluationBatchTarget{
		batchEvaluationTarget("binding-3", "agent-3"),
		batchEvaluationTarget("binding-1", "agent-1"),
		batchEvaluationTarget("binding-2", "agent-2"),
	}
	port := &responsibilityEvaluationBatchTestPort{
		targets:  targets,
		failures: map[string]error{"binding-3": errors.New("portfolio evaluation failed")},
	}
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock:                    clock.NewFake(schedulerTestTime.Add(time.Minute)),
		Repository:               repository,
		ResponsibilityEvaluation: port,
	})

	failedRun, err := dispatcher.DispatchRun(ctx, task, run)
	if err == nil {
		t.Fatal("first batch dispatch error = nil, want failed item error")
	}
	wantFirstSummary := ResponsibilityEvaluationBatchResult{
		ScopeID: "people-tenant-1", DiscoveredCount: 3, EvaluatedCount: 2,
		NoActionCount: 1, LinkedExistingMissionCount: 1, FailedCount: 1,
		CandidateCount: 5, ActionableCount: 1, WaitingCount: 1, ReviewCount: 1,
		PausedCount: 1, InvalidCount: 1,
	}
	assertResponsibilityBatchParentRun(t, failedRun, RunStatusFailed, wantFirstSummary)
	if port.discoverCalls != 1 || len(port.evaluateCalls) != 1 || len(port.evaluateCalls[0]) != 3 {
		t.Fatalf("batch calls: discover=%d evaluate=%+v", port.discoverCalls, port.evaluateCalls)
	}

	firstPage, err := repository.ListResponsibilityEvaluationItemsPage(ctx, run.ScheduledRunID, "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(firstPage.Items) != 3 {
		t.Fatalf("persisted item count = %d, want 3: %+v", len(firstPage.Items), firstPage.Items)
	}
	statuses := map[string]string{}
	for _, item := range firstPage.Items {
		statuses[item.PortfolioBindingID] = item.Status
	}
	if statuses["binding-1"] != ResponsibilityEvaluationItemCompleted || statuses["binding-2"] != ResponsibilityEvaluationItemCompleted ||
		statuses["binding-3"] != ResponsibilityEvaluationItemFailed {
		t.Fatalf("persisted item statuses = %+v", statuses)
	}
	persisted, err := repository.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	assertResponsibilityBatchParentRun(t, persisted, RunStatusFailed, wantFirstSummary)

	port.failures = nil
	port.evaluateCalls = nil
	port.targets = append(port.targets, batchEvaluationTarget("binding-4", "agent-4"))
	replay := failedRun
	replay.Attempt = 2
	replay.StartedAt = schedulerTestTime.Add(2 * time.Minute)
	replayed, err := dispatcher.DispatchRun(ctx, task, replay)
	if err != nil {
		t.Fatal(err)
	}
	wantReplaySummary := ResponsibilityEvaluationBatchResult{
		ScopeID: "people-tenant-1", DiscoveredCount: 3, EvaluatedCount: 3,
		NoActionCount: 2, LinkedExistingMissionCount: 1, SkippedCount: 2,
		CandidateCount: 6, ActionableCount: 1, WaitingCount: 1, ReviewCount: 1,
		EscalatedCount: 1, PausedCount: 1, InvalidCount: 1,
	}
	assertResponsibilityBatchParentRun(t, replayed, RunStatusCompleted, wantReplaySummary)
	if port.discoverCalls != 1 || len(port.evaluateCalls) != 1 || len(port.evaluateCalls[0]) != 1 || port.evaluateCalls[0][0].PortfolioBindingID != "binding-3" {
		t.Fatalf("replay evaluated targets = %+v, want only binding-3", port.evaluateCalls)
	}
	for _, target := range targets {
		itemID := ResponsibilityEvaluationItemID(run.ScheduledRunID, target.PortfolioBindingID, target.PortfolioVersionID)
		item, getErr := repository.GetResponsibilityEvaluationItem(ctx, itemID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		wantAttempt := 1
		if target.PortfolioBindingID == "binding-3" {
			wantAttempt = 2
		}
		if item.Status != ResponsibilityEvaluationItemCompleted || item.Attempt != wantAttempt {
			t.Fatalf("replayed item %s = %+v, want completed attempt %d", target.PortfolioBindingID, item, wantAttempt)
		}
	}
}

func TestResponsibilityEvaluationItemRepositoryPagingAndServiceTenantIsolation(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-responsibility-items", schedulerTestTime)
	task.TenantID = "tenant-a"
	task.CreatedByKind = ViewerKindSystem
	task.Target = Target{
		Mode: TargetModeResponsibilityEvaluation, SourceScopeKind: ResponsibilityEvaluationPeoplePortfoliosScope, SourceScopeID: "people-a",
	}
	if err := repository.SaveTask(ctx, task); err != nil {
		t.Fatal(err)
	}
	run := schedulerTestRun("run-responsibility-items", task.ScheduledTaskID, RunStatusRunning, schedulerTestTime)
	run.TenantID = task.TenantID
	run.Attempt = 1
	run.TargetMode = TargetModeResponsibilityEvaluation
	run.ResponsibilityEvaluationBatch = &ResponsibilityEvaluationBatchResult{ScopeID: "people-a", DiscoveredCount: 3}
	if err := repository.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	statuses := []string{ResponsibilityEvaluationItemCompleted, ResponsibilityEvaluationItemFailed, ResponsibilityEvaluationItemCompleted}
	for index, status := range statuses {
		item := ResponsibilityEvaluationItem{
			TenantID: task.TenantID, ScheduledTaskID: task.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID,
			PortfolioID: fmt.Sprintf("portfolio-%d", index), PortfolioBindingID: fmt.Sprintf("binding-%d", index),
			PortfolioVersionID: fmt.Sprintf("version-%d", index), AgentProfileID: fmt.Sprintf("agent-%d", index),
			Status: status, Attempt: 1, CreatedAt: schedulerTestTime, UpdatedAt: schedulerTestTime,
		}
		if err := repository.SaveResponsibilityEvaluationItem(ctx, item); err != nil {
			t.Fatal(err)
		}
	}
	run.Status = RunStatusCompleted
	run.CompletedAt = schedulerTestTime.Add(time.Minute)
	if err := repository.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	storedID := ResponsibilityEvaluationItemID(run.ScheduledRunID, "binding-0", "version-0")
	stored, err := repository.GetResponsibilityEvaluationItem(ctx, storedID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.ItemID != storedID || stored.TenantID != task.TenantID || stored.PortfolioBindingID != "binding-0" {
		t.Fatalf("stored item = %+v", stored)
	}
	first, err := repository.ListResponsibilityEvaluationItemsPage(ctx, run.ScheduledRunID, "", "", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || !first.HasMore || first.NextCursor == "" || first.Limit != 2 {
		t.Fatalf("first page = %+v", first)
	}
	second, err := repository.ListResponsibilityEvaluationItemsPage(ctx, run.ScheduledRunID, "", first.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.HasMore || second.NextCursor != "" {
		t.Fatalf("second page = %+v", second)
	}
	failed, err := repository.ListResponsibilityEvaluationItemsPage(ctx, run.ScheduledRunID, ResponsibilityEvaluationItemFailed, "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(failed.Items) != 1 || failed.Items[0].Status != ResponsibilityEvaluationItemFailed || failed.Items[0].PortfolioBindingID != "binding-1" {
		t.Fatalf("failed items = %+v", failed)
	}
	if _, err := repository.ListResponsibilityEvaluationItemsPage(ctx, run.ScheduledRunID, "", "another-run:cursor", 10); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("foreign cursor error = %v, want ErrInvalidRequest", err)
	}
	for _, cursor := range []string{
		run.ScheduledRunID + ":" + strings.Repeat("a", 23),
		run.ScheduledRunID + ":" + strings.Repeat("a", 25),
		run.ScheduledRunID + ":" + strings.Repeat("g", 24),
		run.ScheduledRunID + ":" + strings.Repeat("A", 24),
		run.ScheduledRunID + ":" + strings.Repeat("a", 600),
		run.ScheduledRunID + ":" + string([]byte{0xff, 0xfe}),
	} {
		if _, err := repository.ListResponsibilityEvaluationItemsPage(ctx, run.ScheduledRunID, "", cursor, 10); !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("invalid cursor %q error = %v, want ErrInvalidRequest", cursor, err)
		}
	}

	service := newServiceForTestWithRepository(repository)
	page, err := service.ListResponsibilityEvaluationItems(ctx, ListResponsibilityEvaluationItemsQuery{
		TenantID: task.TenantID, ScheduledTaskID: task.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID,
		Status: ResponsibilityEvaluationItemCompleted, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("service completed items = %+v", page)
	}
	if _, err := service.ListResponsibilityEvaluationItems(ctx, ListResponsibilityEvaluationItemsQuery{
		TenantID: "tenant-b", ScheduledTaskID: task.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID, Limit: 10,
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant list error = %v, want ErrNotFound", err)
	}
}

func TestResponsibilityEvaluationItemRepositoryRejectsStaleAttempt(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter)
	ctx := context.Background()
	run := schedulerTestRun("run-responsibility-attempt-fence", "sched-responsibility-attempt-fence", RunStatusRunning, schedulerTestTime)
	run.TenantID = "tenant-a"
	run.Attempt = 1
	if err := repository.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	item := ResponsibilityEvaluationItem{
		TenantID: run.TenantID, ScheduledTaskID: run.ScheduledTaskID, ScheduledRunID: run.ScheduledRunID,
		PortfolioID: "portfolio-1", PortfolioBindingID: "binding-1", PortfolioVersionID: "version-1", AgentProfileID: "agent-1",
		Status: ResponsibilityEvaluationItemFailed, Attempt: 1, CreatedAt: schedulerTestTime, UpdatedAt: schedulerTestTime,
	}
	invalidIdentity := item
	invalidIdentity.ItemID = "arbitrary-item-id"
	if err := repository.SaveResponsibilityEvaluationItem(ctx, invalidIdentity); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("arbitrary item ID error = %v, want ErrInvalidRequest", err)
	}
	if err := repository.SaveResponsibilityEvaluationItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	run.Attempt = 2
	if err := repository.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	item.Status = ResponsibilityEvaluationItemCompleted
	item.Attempt = 2
	item.UpdatedAt = schedulerTestTime.Add(time.Minute)
	if err := repository.SaveResponsibilityEvaluationItem(ctx, item); err != nil {
		t.Fatal(err)
	}
	stale := item
	stale.Status = ResponsibilityEvaluationItemFailed
	stale.Attempt = 1
	stale.UpdatedAt = schedulerTestTime.Add(2 * time.Minute)
	if err := repository.SaveResponsibilityEvaluationItem(ctx, stale); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale item save error = %v, want ErrRevisionConflict", err)
	}
	stored, err := repository.GetResponsibilityEvaluationItem(ctx, ResponsibilityEvaluationItemID(run.ScheduledRunID, "binding-1", "version-1"))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != ResponsibilityEvaluationItemCompleted || stored.Attempt != 2 {
		t.Fatalf("stored item = %+v, want completed attempt 2", stored)
	}
}

type responsibilityEvaluationBatchTestPort struct {
	targets       []ResponsibilityEvaluationBatchTarget
	failures      map[string]error
	discoverCalls int
	evaluateCalls [][]ResponsibilityEvaluationBatchTarget
}

func (p *responsibilityEvaluationBatchTestPort) EvaluateResponsibility(context.Context, ResponsibilityEvaluationRequest) (ResponsibilityEvaluationDecision, error) {
	return ResponsibilityEvaluationDecision{}, errors.New("unexpected singular responsibility evaluation")
}

func (p *responsibilityEvaluationBatchTestPort) DiscoverResponsibilityEvaluations(
	_ context.Context,
	_ ResponsibilityEvaluationBatchRequest,
) ([]ResponsibilityEvaluationBatchTarget, error) {
	p.discoverCalls++
	return append([]ResponsibilityEvaluationBatchTarget(nil), p.targets...), nil
}

func (p *responsibilityEvaluationBatchTestPort) EvaluateResponsibilityBatch(
	_ context.Context,
	_ ResponsibilityEvaluationBatchRequest,
	targets []ResponsibilityEvaluationBatchTarget,
) ([]ResponsibilityEvaluationBatchDecision, error) {
	p.evaluateCalls = append(p.evaluateCalls, append([]ResponsibilityEvaluationBatchTarget(nil), targets...))
	decisions := make([]ResponsibilityEvaluationBatchDecision, 0, len(targets))
	for _, target := range targets {
		decisions = append(decisions, ResponsibilityEvaluationBatchDecision{
			Target: target, Decision: batchEvaluationDecision(target), Err: p.failures[target.PortfolioBindingID],
		})
	}
	return decisions, nil
}

func batchEvaluationTarget(bindingID, agentID string) ResponsibilityEvaluationBatchTarget {
	return ResponsibilityEvaluationBatchTarget{
		TenantID: "tenant-system", AgentProfileID: agentID, PortfolioID: "portfolio-" + bindingID,
		PortfolioBindingID: bindingID, PortfolioVersionID: "version-" + bindingID,
	}
}

func batchEvaluationDecision(target ResponsibilityEvaluationBatchTarget) ResponsibilityEvaluationDecision {
	result := ResponsibilityEvaluationResult{
		PortfolioID: target.PortfolioID, PortfolioBindingID: target.PortfolioBindingID, PortfolioVersionID: target.PortfolioVersionID,
		SourceRevision: "revision-" + target.PortfolioBindingID, SourceDigest: "digest-" + target.PortfolioBindingID,
		AgentProfileID: target.AgentProfileID, Evaluated: true, DecisionKind: ResponsibilityEvaluationNoAction,
		DecisionSourceRef: target.PortfolioVersionID, Reason: "no_action",
	}
	switch target.PortfolioBindingID {
	case "binding-1":
		result.CandidateCount, result.WaitingCount, result.ReviewCount = 2, 1, 1
	case "binding-2":
		result.Actionable = true
		result.DecisionKind = ResponsibilityEvaluationLinkExistingMission
		result.DecisionSourceRef = "responsibility-assignment-binding-2"
		result.Reason = "existing_mission_task"
		result.CandidateCount, result.ActionableCount, result.PausedCount, result.InvalidCount = 3, 1, 1, 1
		result.SelectedMissionID = "mission-2"
		result.SelectedTaskID = "task-2"
		result.SelectedResponsibilityID = "responsibility-2"
		result.SelectedAssignmentState = "active"
		result.SelectedAssignmentVersion = 1
		return ResponsibilityEvaluationDecision{
			Result: result, MissionID: "mission-2", TaskID: "task-2", ResponsibilityID: "responsibility-2",
		}
	case "binding-3":
		result.CandidateCount, result.EscalatedCount = 1, 1
	}
	return ResponsibilityEvaluationDecision{Result: result}
}

func assertResponsibilityBatchParentRun(t *testing.T, run ScheduledRun, wantStatus RunStatus, want ResponsibilityEvaluationBatchResult) {
	t.Helper()
	if run.Status != wantStatus || run.TargetMode != TargetModeResponsibilityEvaluation || run.ResponsibilityEvaluation != nil ||
		run.ResponsibilityEvaluationBatch == nil || *run.ResponsibilityEvaluationBatch != want {
		t.Fatalf("batch parent run = %+v, want summary %+v", run, want)
	}
	if run.AgentID != "" || run.MissionID != "" || run.TaskID != "" || run.ResponsibilityID != "" {
		t.Fatalf("batch parent retained singular identities: agent=%q mission=%q task=%q responsibility=%q", run.AgentID, run.MissionID, run.TaskID, run.ResponsibilityID)
	}
}
