package scheduler

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	discussionsession "orrery_v2/internal/app/discussionsession"
	memoryadmin "orrery_v2/internal/app/memoryadmin"
	memorydistill "orrery_v2/internal/app/memorydistill"
	todoapp "orrery_v2/internal/app/todo"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/testkit"
)

func TestDispatcherDoesNotOverwriteWorkerResultThatFinishedDuringDispatch(t *testing.T) {
	for _, status := range []RunStatus{RunStatusCompleted, RunStatusFailed} {
		t.Run(string(status), func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			repo := NewRepository(env.DBRouter)
			finished := ScheduledRun{ScheduledRunID: "fast-worker", ScheduledTaskID: "scheduled-task", TargetMode: TargetModeAgentTask,
				Status: status, RuntimeRunID: "runtime-fast", MissionID: "mission", TaskID: "task", AgentID: "agent", ResponsibilityID: "responsibility",
				Result: RunResult{Summary: "Agent result already committed"}, TraceRef: "trace://fast", CompletedAt: env.Clock.Now(), FailureReason: "original blocker"}
			if err := repo.SaveRun(ctx, finished); err != nil {
				t.Fatal(err)
			}
			snapshot := finished
			snapshot.Status, snapshot.Result, snapshot.TraceRef, snapshot.FailureReason = RunStatusRunning, RunResult{}, "", ""
			snapshot.CompletedAt = time.Time{}
			dispatcher := NewDispatcher(DispatcherOptions{Clock: env.Clock, Repository: repo})
			stored, err := dispatcher.persistRunSnapshot(ctx, snapshot)
			if err != nil || stored.Status != status || stored.Result.Summary != finished.Result.Summary || stored.TraceRef != finished.TraceRef || stored.CompletedAt != finished.CompletedAt || stored.FailureReason != finished.FailureReason {
				t.Fatalf("dispatch overwrote the completed worker: %+v error=%v", stored, err)
			}
			snapshot.MissionID = "another-mission"
			if _, err := dispatcher.persistRunSnapshot(ctx, snapshot); !errors.Is(err, ErrRevisionConflict) {
				t.Fatalf("conflicting dispatch identity accepted: %v", err)
			}
		})
	}
}

func TestDispatcherDiscussionWorkflowSendsSystemMessageAndRecordsConversation(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-discussion", schedulerTestTime.Add(-time.Minute))
	task.Target = Target{
		Mode:           TargetModeDiscussionWorkflow,
		ConversationID: "conversation-existing",
		Message:        "Prepare the daily competitor digest.",
	}
	run := schedulerTestRun("run-discussion", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	fakeDiscussion := &fakeDiscussionPort{conversation: discussionsession.ConversationDTO{ConversationID: "conversation-result"}}
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock:      clock.NewFake(schedulerTestTime.Add(5 * time.Minute)),
		Repository: repo,
		Discussion: fakeDiscussion,
	})

	completed, err := dispatcher.DispatchRun(ctx, task, run)
	if err != nil {
		t.Fatal(err)
	}
	runs, err := repo.ListRunsForTask(ctx, task.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}

	if fakeDiscussion.viewer.ViewerID != "human-operator" || fakeDiscussion.viewer.AgentProfileID != "human-operator" || fakeDiscussion.viewer.ViewerKind != discussionsession.ViewerKindHuman {
		t.Fatalf("discussion viewer = %+v, want creator viewer", fakeDiscussion.viewer)
	}
	if fakeDiscussion.cmd.ConversationID != "conversation-existing" {
		t.Fatalf("conversation id = %q", fakeDiscussion.cmd.ConversationID)
	}
	if fakeDiscussion.cmd.IdempotencyKey != "scheduled-run:run-discussion" {
		t.Fatalf("idempotency key = %q", fakeDiscussion.cmd.IdempotencyKey)
	}
	if !strings.Contains(fakeDiscussion.cmd.Content, "Prepare the daily competitor digest.") {
		t.Fatalf("content = %q, want scheduled message", fakeDiscussion.cmd.Content)
	}
	if completed.Status != RunStatusCompleted || completed.ConversationID != "conversation-result" {
		t.Fatalf("completed run = %+v", completed)
	}
	if len(runs) != 1 || runs[0].Status != RunStatusCompleted || !runs[0].CompletedAt.Equal(schedulerTestTime.Add(5*time.Minute)) {
		t.Fatalf("persisted runs = %+v", runs)
	}
}

func TestDispatcherDoesNotRedispatchRunningOrTerminalRun(t *testing.T) {
	statuses := []RunStatus{RunStatusRunning, RunStatusCompleted, RunStatusFailed, RunStatusSkipped}
	for _, status := range statuses {
		t.Run(string(status), func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			repo := NewRepository(env.DBRouter)
			ctx := context.Background()
			task := schedulerTestTask("sched-no-redispatch-"+string(status), schedulerTestTime.Add(-time.Minute))
			task.Target = Target{Mode: TargetModeTodoAutomationScan}
			run := schedulerTestRun("run-no-redispatch-"+string(status), task.ScheduledTaskID, status, schedulerTestTime)
			run.TargetMode = TargetModeTodoAutomationScan
			if status == RunStatusCompleted || status == RunStatusFailed || status == RunStatusSkipped {
				run.CompletedAt = schedulerTestTime.Add(time.Minute)
			}
			if err := repo.SaveRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			port := &fakeTodoAutomationPort{}
			dispatcher := NewDispatcher(DispatcherOptions{
				Clock:          clock.NewFake(schedulerTestTime.Add(5 * time.Minute)),
				Repository:     repo,
				TodoAutomation: port,
			})

			got, err := dispatcher.DispatchRun(ctx, task, schedulerTestRun(run.ScheduledRunID, task.ScheduledTaskID, RunStatusQueued, schedulerTestTime))
			if err != nil {
				t.Fatal(err)
			}
			if port.calls != 0 {
				t.Fatalf("todo automation calls = %d, want 0", port.calls)
			}
			if got.Status != status || !got.CompletedAt.Equal(run.CompletedAt) {
				t.Fatalf("returned run = %+v, want durable %s snapshot", got, status)
			}
		})
	}
}

func TestDispatcherRedispatchesRunningRunForNewQueueAttempt(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-retry", schedulerTestTime.Add(-time.Minute))
	task.Target = Target{Mode: TargetModeResponsibilityReconcile}
	task.CreatedBy = "system:responsibility-coordinator-bootstrap"
	task.CreatedByKind = ViewerKindSystem
	run := schedulerTestRun("run-retry", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}

	temporaryErr := errors.New("responsibility coordinator unavailable")
	port := &fakeResponsibilityReconcilePort{err: temporaryErr}
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock: clock.NewFake(schedulerTestTime.Add(time.Minute)), Repository: repo, Responsibilities: port,
	})

	failed, err := dispatcher.DispatchRun(ctx, task, run)
	if !errors.Is(err, temporaryErr) {
		t.Fatalf("first dispatch error = %v, want %v", err, temporaryErr)
	}
	if port.calls != 1 || failed.Status != RunStatusFailed || failed.Attempt != 1 {
		t.Fatalf("first dispatch calls=%d run=%+v", port.calls, failed)
	}
	if _, err := repo.MutateRun(ctx, run.ScheduledRunID, func(current *ScheduledRun) error {
		current.Status = RunStatusRunning
		current.CompletedAt = time.Time{}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	retry, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := dispatcher.DispatchRun(ctx, task, retry)
	if err != nil {
		t.Fatal(err)
	}
	if port.calls != 1 || unchanged.Status != RunStatusRunning {
		t.Fatalf("same attempt calls=%d run=%+v", port.calls, unchanged)
	}

	port.err = nil
	port.result = ResponsibilityReconcileResult{Candidates: 2, Claimed: 1, Dispatched: 1}
	retry.Attempt = 2
	completed, err := dispatcher.DispatchRun(ctx, task, retry)
	if err != nil {
		t.Fatal(err)
	}
	if port.calls != 2 || completed.Status != RunStatusCompleted || completed.Attempt != 2 || completed.FailureReason != "" {
		t.Fatalf("retry calls=%d run=%+v", port.calls, completed)
	}
	if completed.ResponsibilityReconcile == nil || *completed.ResponsibilityReconcile != port.result {
		t.Fatalf("retry result = %+v", completed.ResponsibilityReconcile)
	}
}

func TestDispatcherNewDiscussionWorkflowPassesInitialMembersAndMemberAddPolicy(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-new-discussion", schedulerTestTime.Add(-time.Minute))
	allowMemberAdditions := false
	task.Target = Target{
		Mode:                 TargetModeDiscussionWorkflow,
		LeadAgentID:          "lead-agent",
		InitialMemberIDs:     []string{"staffing-agent", "lead-agent", "coordinator-agent"},
		AllowMemberAdditions: &allowMemberAdditions,
		Message:              "Prepare the daily competitor digest.",
	}
	run := schedulerTestRun("run-new-discussion", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	fakeDiscussion := &fakeDiscussionPort{conversation: discussionsession.ConversationDTO{ConversationID: "conversation-created"}}
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock:      clock.NewFake(schedulerTestTime.Add(5 * time.Minute)),
		Repository: repo,
		Discussion: fakeDiscussion,
	})

	completed, err := dispatcher.DispatchRun(ctx, task, run)
	if err != nil {
		t.Fatal(err)
	}

	if fakeDiscussion.cmd.ConversationID != "" {
		t.Fatalf("new discussion conversation id = %q, want empty", fakeDiscussion.cmd.ConversationID)
	}
	if strings.Join(fakeDiscussion.cmd.InitialMemberIDs, ",") != "lead-agent,staffing-agent,coordinator-agent" {
		t.Fatalf("initial members = %+v", fakeDiscussion.cmd.InitialMemberIDs)
	}
	if fakeDiscussion.cmd.AllowMemberAdditions == nil || *fakeDiscussion.cmd.AllowMemberAdditions {
		t.Fatalf("allow member additions = %+v, want false", fakeDiscussion.cmd.AllowMemberAdditions)
	}
	if len(fakeDiscussion.cmd.Mentions) != 1 || fakeDiscussion.cmd.Mentions[0].TargetKind != "agent" || fakeDiscussion.cmd.Mentions[0].TargetID != "lead-agent" {
		t.Fatalf("lead agent mentions = %+v", fakeDiscussion.cmd.Mentions)
	}
	if completed.ConversationID != "conversation-created" {
		t.Fatalf("completed run conversation = %q", completed.ConversationID)
	}
}

func TestDispatcherAgentTaskMaterializesLinkedResponsibilityWithCreatorPermissions(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-agent", schedulerTestTime.Add(-time.Minute))
	task.MissionID = "mission-scheduled"
	task.Target = Target{
		Mode:               TargetModeAgentTask,
		AgentID:            "agent-summarizer",
		ApprovalAssigneeID: "human-ding",
		TaskGoal:           "Summarize this week's customer feedback.",
		Prompt:             "Use the feedback brief and return a ranked action list.",
		ResultContract: AgentResultContract{Artifacts: []ArtifactRequirement{{
			ID: "weekly-report", Kind: "report", PathPattern: "/srv/orrery/reports/weekly-*.md", MinBytes: 100,
		}}},
	}
	task.Policy.MaxRetries = 2
	task.Execution = ExecutionPolicy{TimeoutSeconds: 1800, LLMProfileID: "llm-minimax", SourceModel: "minimax/MiniMax-M3"}
	task.Delivery = DeliveryPolicy{Mode: "announce", Channel: "feishu", RecipientID: "oc_group", AccountID: "main", BestEffort: true}
	run := schedulerTestRun("run-agent", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	run.TargetMode = TargetModeAgentTask
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	fakeAgentRuns := &fakeAgentTaskPort{receipt: AgentTaskReceipt{CommandID: "responsibility-1", AgentID: "agent-summarizer", MissionID: "mission-1", TaskID: "task-1", ResponsibilityID: "responsibility-1"}}
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock:              clock.NewFake(schedulerTestTime.Add(5 * time.Minute)),
		Repository:         repo,
		AgentTasks:         fakeAgentRuns,
		CreatorPermissions: schedulerTestCreatorResolver(),
	})

	running, err := dispatcher.DispatchRun(ctx, task, run)
	if err != nil {
		t.Fatal(err)
	}

	if fakeAgentRuns.calls != 1 {
		t.Fatalf("agent task calls = %d, want 1", fakeAgentRuns.calls)
	}
	if fakeAgentRuns.req.RunID != "run-agent" || fakeAgentRuns.req.ScheduledTaskID != "sched-agent" {
		t.Fatalf("agent task ids = %+v", fakeAgentRuns.req)
	}
	if fakeAgentRuns.req.MissionID != task.MissionID {
		t.Fatalf("agent task Mission = %q, want %q", fakeAgentRuns.req.MissionID, task.MissionID)
	}
	if fakeAgentRuns.req.TenantID != task.TenantID {
		t.Fatalf("agent task tenant = %q, want %q", fakeAgentRuns.req.TenantID, task.TenantID)
	}
	if fakeAgentRuns.req.Title != task.Title {
		t.Fatalf("agent task title = %q, want %q", fakeAgentRuns.req.Title, task.Title)
	}
	if fakeAgentRuns.req.AgentID != "agent-summarizer" || fakeAgentRuns.req.TaskGoal != "Summarize this week's customer feedback." {
		t.Fatalf("agent task target = %+v", fakeAgentRuns.req)
	}
	if fakeAgentRuns.req.ApprovalAssigneeID != "human-ding" {
		t.Fatalf("approval assignee = %q, want human-ding", fakeAgentRuns.req.ApprovalAssigneeID)
	}
	if fakeAgentRuns.req.Prompt != "Use the feedback brief and return a ranked action list." {
		t.Fatalf("prompt = %q", fakeAgentRuns.req.Prompt)
	}
	if len(fakeAgentRuns.req.ResultContract.Artifacts) != 1 || fakeAgentRuns.req.ResultContract.Artifacts[0].ID != "weekly-report" {
		t.Fatalf("result contract = %+v", fakeAgentRuns.req.ResultContract)
	}
	if fakeAgentRuns.req.PrincipalID != "operator" || fakeAgentRuns.req.CreatorProfileID != "human-operator" || fakeAgentRuns.req.CreatorKind != ViewerKindHuman || fakeAgentRuns.req.CreatorName != "Operator" {
		t.Fatalf("creator identity = %+v", fakeAgentRuns.req)
	}
	if !containsTestString(fakeAgentRuns.req.Permissions, memorydistill.PermissionMemoryDistill) {
		t.Fatalf("agent task permissions = %+v", fakeAgentRuns.req.Permissions)
	}
	if fakeAgentRuns.req.MaxAttempts != 3 {
		t.Fatalf("agent task max attempts = %d, want 3", fakeAgentRuns.req.MaxAttempts)
	}
	if fakeAgentRuns.req.Execution.TimeoutSeconds != 1800 || fakeAgentRuns.req.Execution.LLMProfileID != "llm-minimax" {
		t.Fatalf("agent task execution policy = %+v", fakeAgentRuns.req.Execution)
	}
	if fakeAgentRuns.req.Delivery.RecipientID != "oc_group" || fakeAgentRuns.req.Delivery.AccountID != "main" {
		t.Fatalf("agent task delivery policy = %+v", fakeAgentRuns.req.Delivery)
	}
	if running.Status != RunStatusRunning || running.CommandID != "responsibility-1" || running.AgentID != "agent-summarizer" || running.MissionID != "mission-1" || running.TaskID != "task-1" || running.ResponsibilityID != "responsibility-1" || !running.CompletedAt.IsZero() {
		t.Fatalf("running run = %+v", running)
	}
}

func TestDispatcherAgentTaskUsesOccurrenceConfigurationSnapshotAfterTaskEdit(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-agent-snapshot", schedulerTestTime.Add(-time.Minute))
	task.TenantID = "tenant-original"
	task.MissionID = "mission-original"
	task.AgentTaskID = "agent-task-original"
	task.Title = "Original task title"
	task.Description = "Original task description"
	task.Target = Target{
		Mode: TargetModeAgentTask, AgentID: "agent-snapshot", ApprovalAssigneeID: "human-original",
		TaskGoal: "Produce the report", Prompt: "Use the original source material",
		ResultContract: AgentResultContract{Artifacts: []ArtifactRequirement{{
			ID: "original-report", Kind: "report", PathPattern: "/workspace/original.md", MinBytes: 10,
		}}},
	}
	task.Policy.MaxRetries = 2
	task.Policy.RetryDelay = "15s"
	task.Execution = ExecutionPolicy{
		TimeoutSeconds: 900, LLMProfileID: "llm-original", SourceModel: "model-original",
		FallbackModels: []string{"fallback-original"}, Thinking: "high", LightContext: true, AllowedToolIDs: []string{"tool-original"},
	}
	task.Delivery = DeliveryPolicy{
		Mode: "announce", Channel: "feishu", RecipientID: "oc_original", AccountID: "main", ContentArtifactID: "original-report", Format: "markdown",
	}
	configurationSnapshot, err := cloneScheduledTaskConfiguration(task)
	if err != nil {
		t.Fatal(err)
	}
	run := schedulerTestRun("run-agent-snapshot", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	run.TargetMode = TargetModeAgentTask
	run.ConfigurationSnapshotVersion = 1
	run.ConfigurationSnapshot = &configurationSnapshot
	run.TenantID = task.TenantID
	run.ScheduledTaskTitle = task.Title
	run.ResultContract = cloneAgentResultContract(task.Target.ResultContract)
	run.Delivery = task.Delivery
	run.AgentID = task.Target.AgentID

	task.Title = "Edited task title"
	task.Description = "Edited task description"
	task.MissionID = "mission-edited"
	task.AgentTaskID = "agent-task-edited"
	task.Target = Target{Mode: TargetModeDiscussionWorkflow, Message: "Dispatch the edited workflow"}
	task.Policy.MaxRetries = 9
	task.Policy.RetryDelay = "9m"
	task.Execution = ExecutionPolicy{TimeoutSeconds: 60, LLMProfileID: "llm-edited", SourceModel: "model-edited", AllowedToolIDs: []string{"tool-edited"}}
	task.CreatedBy = "edited-owner"
	task.CreatedByKind = ViewerKindAgent
	task.CreatedByName = "Edited owner"
	task.CreatorProfileID = "edited-profile"
	task.Delivery = DeliveryPolicy{
		Mode: "announce", Channel: "feishu", RecipientID: "oc_updated", AccountID: "secondary", ContentArtifactID: "updated-report", Format: "text",
	}
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	fakeAgentRuns := &fakeAgentTaskPort{receipt: AgentTaskReceipt{
		CommandID: "responsibility-snapshot", AgentID: "agent-snapshot", MissionID: "mission-snapshot", TaskID: "task-snapshot", ResponsibilityID: "responsibility-snapshot",
	}}
	creatorResolver := schedulerTestCreatorResolver()
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock: clock.NewFake(schedulerTestTime.Add(time.Minute)), Repository: repo, AgentTasks: fakeAgentRuns,
		CreatorPermissions: creatorResolver,
	})

	if _, err := dispatcher.DispatchRun(ctx, task, run); err != nil {
		t.Fatal(err)
	}
	if fakeAgentRuns.req.TenantID != "tenant-original" || fakeAgentRuns.req.Title != "Original task title" ||
		fakeAgentRuns.req.Description != "Original task description" || fakeAgentRuns.req.MissionID != "mission-original" || fakeAgentRuns.req.AgentTaskID != "agent-task-original" {
		t.Fatalf("dispatched occurrence identity = tenant %q title %q", fakeAgentRuns.req.TenantID, fakeAgentRuns.req.Title)
	}
	if fakeAgentRuns.req.AgentID != "agent-snapshot" || fakeAgentRuns.req.ApprovalAssigneeID != "human-original" ||
		fakeAgentRuns.req.TaskGoal != "Produce the report" || fakeAgentRuns.req.Prompt != "Use the original source material" {
		t.Fatalf("dispatched occurrence target = %+v", fakeAgentRuns.req)
	}
	if fakeAgentRuns.req.PrincipalID != "operator" || fakeAgentRuns.req.CreatorProfileID != "human-operator" || fakeAgentRuns.req.CreatorKind != ViewerKindHuman ||
		fakeAgentRuns.req.CreatorName != "Operator" || creatorResolver.ref.ViewerID != "operator" || creatorResolver.ref.ProfileID != "human-operator" {
		t.Fatalf("dispatched occurrence creator = request %+v resolver %+v", fakeAgentRuns.req, creatorResolver.ref)
	}
	if fakeAgentRuns.req.MaxAttempts != 3 || fakeAgentRuns.req.RetryDelay != "15s" || fakeAgentRuns.req.Execution.TimeoutSeconds != 900 ||
		fakeAgentRuns.req.Execution.LLMProfileID != "llm-original" || len(fakeAgentRuns.req.Execution.AllowedToolIDs) != 1 || fakeAgentRuns.req.Execution.AllowedToolIDs[0] != "tool-original" {
		t.Fatalf("dispatched occurrence policy = %+v", fakeAgentRuns.req)
	}
	if len(fakeAgentRuns.req.ResultContract.Artifacts) != 1 || fakeAgentRuns.req.ResultContract.Artifacts[0].ID != "original-report" ||
		fakeAgentRuns.req.ResultContract.Artifacts[0].PathPattern != "/workspace/original.md" {
		t.Fatalf("dispatched result contract = %+v", fakeAgentRuns.req.ResultContract)
	}
	if fakeAgentRuns.req.Delivery.RecipientID != "oc_original" || fakeAgentRuns.req.Delivery.AccountID != "main" ||
		fakeAgentRuns.req.Delivery.ContentArtifactID != "original-report" || fakeAgentRuns.req.Delivery.Format != "markdown" {
		t.Fatalf("dispatched delivery policy = %+v", fakeAgentRuns.req.Delivery)
	}
}

func TestDispatcherRejectsInvalidOccurrenceConfigurationSnapshotBeforeClaim(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*ScheduledTask, *ScheduledRun)
		wantErr  error
		wantMode TargetMode
	}{
		{name: "missing full snapshot", mutate: func(_ *ScheduledTask, run *ScheduledRun) { run.ConfigurationSnapshot = nil }, wantErr: ErrInvalidRequest, wantMode: TargetModeAgentTask},
		{name: "missing tenant", mutate: func(_ *ScheduledTask, run *ScheduledRun) { run.TenantID = "" }, wantErr: ErrInvalidRequest, wantMode: TargetModeAgentTask},
		{name: "missing title", mutate: func(_ *ScheduledTask, run *ScheduledRun) { run.ScheduledTaskTitle = "" }, wantErr: ErrInvalidRequest, wantMode: TargetModeAgentTask},
		{name: "missing target mode", mutate: func(_ *ScheduledTask, run *ScheduledRun) { run.TargetMode = "" }, wantErr: ErrRevisionConflict},
		{name: "tenant changed", mutate: func(task *ScheduledTask, _ *ScheduledRun) { task.TenantID = "tenant-edited" }, wantErr: ErrRevisionConflict, wantMode: TargetModeAgentTask},
		{name: "snapshot task changed", mutate: func(_ *ScheduledTask, run *ScheduledRun) {
			run.ConfigurationSnapshot.ScheduledTaskID = "other-task"
		}, wantErr: ErrRevisionConflict, wantMode: TargetModeAgentTask},
		{name: "snapshot Agent changed", mutate: func(_ *ScheduledTask, run *ScheduledRun) {
			run.ConfigurationSnapshot.Target.AgentID = "other-agent"
		}, wantErr: ErrRevisionConflict, wantMode: TargetModeAgentTask},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			repo := NewRepository(env.DBRouter)
			ctx := context.Background()
			task := schedulerTestTask("sched-agent-invalid-snapshot-"+strings.ReplaceAll(test.name, " ", "-"), schedulerTestTime.Add(-time.Minute))
			task.TenantID = "tenant-original"
			task.Target = Target{Mode: TargetModeAgentTask, AgentID: "agent-snapshot", TaskGoal: "Produce the report"}
			run := schedulerTestRun("run-agent-invalid-snapshot-"+strings.ReplaceAll(test.name, " ", "-"), task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
			run.ConfigurationSnapshotVersion = 1
			configurationSnapshot, err := cloneScheduledTaskConfiguration(task)
			if err != nil {
				t.Fatal(err)
			}
			run.ConfigurationSnapshot = &configurationSnapshot
			run.TenantID = task.TenantID
			run.ScheduledTaskTitle = task.Title
			run.TargetMode = TargetModeAgentTask
			run.AgentID = task.Target.AgentID
			test.mutate(&task, &run)
			if err := repo.SaveRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			agentTasks := &fakeAgentTaskPort{}
			dispatcher := NewDispatcher(DispatcherOptions{
				Clock: clock.NewFake(schedulerTestTime.Add(time.Minute)), Repository: repo, AgentTasks: agentTasks,
				CreatorPermissions: schedulerTestCreatorResolver(),
			})

			if _, err := dispatcher.DispatchRun(ctx, task, run); !errors.Is(err, test.wantErr) {
				t.Fatalf("DispatchRun error = %v, want %v", err, test.wantErr)
			}
			if agentTasks.calls != 0 {
				t.Fatalf("agent task calls = %d, want 0", agentTasks.calls)
			}
			persisted, err := repo.GetRun(ctx, run.ScheduledRunID)
			if err != nil {
				t.Fatal(err)
			}
			if persisted.Status != RunStatusQueued || persisted.TargetMode != test.wantMode {
				t.Fatalf("persisted run after rejected snapshot = %+v", persisted)
			}
		})
	}
}

func TestDispatcherRejectsAgentReceiptThatConflictsWithOccurrenceSnapshot(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-agent-receipt-conflict", schedulerTestTime.Add(-time.Minute))
	task.TenantID = "tenant-receipt-conflict"
	task.Target = Target{Mode: TargetModeAgentTask, AgentID: "agent-original", TaskGoal: "Produce the report"}
	configurationSnapshot, err := cloneScheduledTaskConfiguration(task)
	if err != nil {
		t.Fatal(err)
	}
	run := schedulerTestRun("run-agent-receipt-conflict", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	run.ConfigurationSnapshotVersion = 1
	run.ConfigurationSnapshot = &configurationSnapshot
	run.TenantID = task.TenantID
	run.ScheduledTaskTitle = task.Title
	run.TargetMode = task.Target.Mode
	run.AgentID = task.Target.AgentID
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	agentTasks := &fakeAgentTaskPort{receipt: AgentTaskReceipt{
		CommandID: "responsibility-conflict", AgentID: "agent-unexpected", MissionID: "mission-conflict", TaskID: "task-conflict", ResponsibilityID: "responsibility-conflict",
	}}
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock: clock.NewFake(schedulerTestTime.Add(time.Minute)), Repository: repo, AgentTasks: agentTasks,
		CreatorPermissions: schedulerTestCreatorResolver(),
	})

	failed, err := dispatcher.DispatchRun(ctx, task, run)
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("DispatchRun error = %v, want ErrRevisionConflict", err)
	}
	if agentTasks.calls != 1 {
		t.Fatalf("agent task calls = %d, want 1", agentTasks.calls)
	}
	if failed.Status != RunStatusFailed || failed.AgentID != "agent-original" || failed.AgentID == agentTasks.receipt.AgentID {
		t.Fatalf("failed run accepted conflicting Agent receipt: %+v", failed)
	}
	persisted, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != RunStatusFailed || persisted.AgentID != "agent-original" || persisted.CommandID != "" || persisted.MissionID != "" {
		t.Fatalf("persisted run accepted conflicting Agent receipt: %+v", persisted)
	}
}

func TestDispatcherAgentTaskResumesRunningRunWithoutMissionLinks(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-agent-resume", schedulerTestTime.Add(-time.Minute))
	task.Target = Target{
		Mode:     TargetModeAgentTask,
		AgentID:  "agent-summarizer",
		TaskGoal: "Summarize this week's customer feedback.",
		Prompt:   "Return a ranked action list.",
	}
	run := schedulerTestRun("run-agent-resume", task.ScheduledTaskID, RunStatusRunning, schedulerTestTime)
	run.TargetMode = TargetModeAgentTask
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	fakeAgentRuns := &fakeAgentTaskPort{receipt: AgentTaskReceipt{
		CommandID: "responsibility-resumed", AgentID: "agent-summarizer", MissionID: "mission-resumed",
		TaskID: "task-resumed", ResponsibilityID: "responsibility-resumed",
	}}
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock:              clock.NewFake(schedulerTestTime.Add(5 * time.Minute)),
		Repository:         repo,
		AgentTasks:         fakeAgentRuns,
		CreatorPermissions: schedulerTestCreatorResolver(),
	})

	resumed, err := dispatcher.DispatchRun(ctx, task, schedulerTestRun(run.ScheduledRunID, task.ScheduledTaskID, RunStatusQueued, schedulerTestTime))
	if err != nil {
		t.Fatal(err)
	}
	if fakeAgentRuns.calls != 1 {
		t.Fatalf("agent task calls = %d, want 1", fakeAgentRuns.calls)
	}
	if resumed.Status != RunStatusRunning || resumed.MissionID != "mission-resumed" || resumed.TaskID != "task-resumed" || resumed.ResponsibilityID != "responsibility-resumed" {
		t.Fatalf("resumed run = %+v", resumed)
	}
}

func TestDispatcherMemoryExperienceDistillationCallsServiceAndRecordsCandidate(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-memory", schedulerTestTime.Add(-time.Minute))
	task.Target = Target{
		Mode:              TargetModeMemoryExperienceDistillation,
		MemoryTopic:       "qa",
		SourceScopeKind:   "mission",
		SourceScopeID:     "mission-1",
		TargetScopeKind:   "project",
		TargetScopeID:     "project-1",
		MemoryRecordLimit: 25,
	}
	run := schedulerTestRun("run-memory", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	fakeMemory := &fakeMemoryDistillationPort{
		response: memorydistill.DistillExperienceResponse{
			Candidates:      []memorydistill.ExperienceCandidateDTO{{CandidateID: "candidate-experience-1"}},
			SourceMemoryIDs: []string{"memory-1", "memory-2"},
		},
	}
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock:              clock.NewFake(schedulerTestTime.Add(5 * time.Minute)),
		Repository:         repo,
		MemoryDistillation: fakeMemory,
		CreatorPermissions: schedulerTestCreatorResolver(),
	})

	completed, err := dispatcher.DispatchRun(ctx, task, run)
	if err != nil {
		t.Fatal(err)
	}

	if fakeMemory.viewer.ViewerID != "operator" || fakeMemory.viewer.ViewerKind != "human" || !containsTestString(fakeMemory.viewer.PermissionScope, memorydistill.PermissionMemoryDistill) {
		t.Fatalf("memory viewer = %+v, want creator with memory.distill", fakeMemory.viewer)
	}
	if !containsTestString(fakeMemory.viewer.MissionIDs, "mission-1") || !containsTestString(fakeMemory.viewer.ProjectIDs, "project-1") {
		t.Fatalf("memory viewer scopes = %+v", fakeMemory.viewer)
	}
	if fakeMemory.req.Topic != "qa" || fakeMemory.req.SourceScope.ID != "mission-1" || fakeMemory.req.TargetScope.ID != "project-1" || fakeMemory.req.Limit != 25 {
		t.Fatalf("memory distill request = %+v", fakeMemory.req)
	}
	if completed.Status != RunStatusCompleted || completed.TargetMode != TargetModeMemoryExperienceDistillation || completed.CommandID != "candidate-experience-1" {
		t.Fatalf("completed run = %+v", completed)
	}
}

func TestDispatcherMemoryExperienceDistillationRequiresCreatorPermissionResolver(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-memory-no-resolver", schedulerTestTime.Add(-time.Minute))
	task.Target = Target{
		Mode:            TargetModeMemoryExperienceDistillation,
		MemoryTopic:     "qa",
		SourceScopeKind: "mission",
		SourceScopeID:   "mission-1",
		TargetScopeKind: "project",
		TargetScopeID:   "project-1",
	}
	run := schedulerTestRun("run-memory-no-resolver", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock:              clock.NewFake(schedulerTestTime.Add(5 * time.Minute)),
		Repository:         repo,
		MemoryDistillation: &fakeMemoryDistillationPort{},
	})

	failed, err := dispatcher.DispatchRun(ctx, task, run)
	if !errors.Is(err, ErrServiceDependency) {
		t.Fatalf("error = %v, want ErrServiceDependency", err)
	}
	if failed.Status != RunStatusFailed {
		t.Fatalf("failed run = %+v", failed)
	}
}

func TestDispatcherMemoryDistillationTreatsNoCandidatesAsSuccessfulNoOp(t *testing.T) {
	tests := []struct {
		name string
		mode TargetMode
	}{
		{name: "experience", mode: TargetModeMemoryExperienceDistillation},
		{name: "world", mode: TargetModeMemoryWorldConsolidation},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			repo := NewRepository(env.DBRouter)
			ctx := context.Background()
			task := schedulerTestTask("sched-no-candidates-"+tt.name, schedulerTestTime.Add(-time.Minute))
			task.Target = Target{
				Mode:            tt.mode,
				MemoryTopic:     "agent_result",
				TargetScopeKind: "organization",
				TargetScopeID:   "org-system-runtime-support",
			}
			run := schedulerTestRun("run-no-candidates-"+tt.name, task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
			if err := repo.SaveRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			dispatcher := NewDispatcher(DispatcherOptions{
				Clock:              clock.NewFake(schedulerTestTime.Add(5 * time.Minute)),
				Repository:         repo,
				MemoryDistillation: &fakeMemoryDistillationPort{err: memorydistill.ErrNoCandidates},
				CreatorPermissions: schedulerTestCreatorResolver(),
			})

			completed, err := dispatcher.DispatchRun(ctx, task, run)
			if err != nil {
				t.Fatalf("dispatch: %v", err)
			}
			if completed.Status != RunStatusCompleted || completed.FailureReason != "" {
				t.Fatalf("completed run = %+v", completed)
			}
			persisted, err := repo.GetRun(ctx, run.ScheduledRunID)
			if err != nil {
				t.Fatal(err)
			}
			if persisted.Status != RunStatusCompleted || persisted.FailureReason != "" {
				t.Fatalf("persisted run = %+v", persisted)
			}
		})
	}
}

func TestDispatcherMemoryDistillationStillFailsForOtherErrors(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-memory-error", schedulerTestTime.Add(-time.Minute))
	task.Target = Target{
		Mode:            TargetModeMemoryExperienceDistillation,
		MemoryTopic:     "agent_result",
		TargetScopeKind: "organization",
		TargetScopeID:   "org-system-runtime-support",
	}
	run := schedulerTestRun("run-memory-error", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	wantErr := errors.New("memory service unavailable")
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock:              clock.NewFake(schedulerTestTime.Add(5 * time.Minute)),
		Repository:         repo,
		MemoryDistillation: &fakeMemoryDistillationPort{err: wantErr},
		CreatorPermissions: schedulerTestCreatorResolver(),
	})

	failed, err := dispatcher.DispatchRun(ctx, task, run)
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
	if failed.Status != RunStatusFailed {
		t.Fatalf("failed run = %+v", failed)
	}
}

func TestDispatcherMemoryWorldConsolidationCallsServiceAndRecordsCandidate(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-world", schedulerTestTime.Add(-time.Minute))
	task.Target = Target{
		Mode:              TargetModeMemoryWorldConsolidation,
		MemoryTopic:       "quality",
		SourceScopeKind:   "project",
		SourceScopeID:     "project-1",
		TargetScopeKind:   "organization",
		TargetScopeID:     "org-1",
		MemoryRecordLimit: 40,
	}
	run := schedulerTestRun("run-world", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	fakeMemory := &fakeMemoryDistillationPort{
		worldResponse: memorydistill.DistillWorldResponse{
			Candidates:      []memorydistill.WorldCandidateDTO{{CandidateID: "candidate-world-1"}},
			SourceMemoryIDs: []string{"memory-exp-1", "memory-exp-2"},
		},
	}
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock:              clock.NewFake(schedulerTestTime.Add(5 * time.Minute)),
		Repository:         repo,
		MemoryDistillation: fakeMemory,
		CreatorPermissions: schedulerTestCreatorResolver(),
	})

	completed, err := dispatcher.DispatchRun(ctx, task, run)
	if err != nil {
		t.Fatal(err)
	}

	if fakeMemory.worldViewer.ViewerID != "operator" || fakeMemory.worldViewer.ViewerKind != "human" || !containsTestString(fakeMemory.worldViewer.PermissionScope, memorydistill.PermissionMemoryDistill) {
		t.Fatalf("world viewer = %+v, want creator with memory.distill", fakeMemory.worldViewer)
	}
	if !containsTestString(fakeMemory.worldViewer.ProjectIDs, "project-1") || !containsTestString(fakeMemory.worldViewer.OrganizationIDs, "org-1") {
		t.Fatalf("world viewer scopes = %+v", fakeMemory.worldViewer)
	}
	if fakeMemory.worldReq.Topic != "quality" || fakeMemory.worldReq.SourceScope.ID != "project-1" || fakeMemory.worldReq.TargetScope.ID != "org-1" || fakeMemory.worldReq.Limit != 40 {
		t.Fatalf("world distill request = %+v", fakeMemory.worldReq)
	}
	if completed.Status != RunStatusCompleted || completed.TargetMode != TargetModeMemoryWorldConsolidation || completed.CommandID != "candidate-world-1" {
		t.Fatalf("completed run = %+v", completed)
	}
}

func TestDispatcherMemoryLifecycleSweepCallsServiceAndRecordsArchivedCount(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-lifecycle", schedulerTestTime.Add(-time.Minute))
	task.Target = Target{
		Mode:              TargetModeMemoryLifecycleSweep,
		TargetScopeKind:   "mission",
		TargetScopeID:     "mission-1",
		MemoryRecordLimit: 50,
	}
	run := schedulerTestRun("run-lifecycle", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	fakeLifecycle := &fakeMemoryLifecyclePort{
		response: memoryadmin.MemoryRetentionSweepResultDTO{
			ArchivedMemoryIDs: []string{"memory-old-1", "memory-old-2"},
			CheckedCount:      10,
			ArchivedCount:     2,
		},
	}
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock:              clock.NewFake(schedulerTestTime.Add(5 * time.Minute)),
		Repository:         repo,
		MemoryLifecycle:    fakeLifecycle,
		CreatorPermissions: schedulerTestCreatorResolver(),
	})

	completed, err := dispatcher.DispatchRun(ctx, task, run)
	if err != nil {
		t.Fatal(err)
	}

	if fakeLifecycle.viewer.ViewerID != "operator" || fakeLifecycle.viewer.ViewerKind != "human" || !containsTestString(fakeLifecycle.viewer.PermissionScope, memoryadmin.PermissionMemoryReview) {
		t.Fatalf("lifecycle viewer = %+v, want creator with memory.review", fakeLifecycle.viewer)
	}
	if !containsTestString(fakeLifecycle.viewer.MissionIDs, "mission-1") {
		t.Fatalf("lifecycle viewer scopes = %+v", fakeLifecycle.viewer)
	}
	if fakeLifecycle.req.Limit != 50 || !strings.Contains(fakeLifecycle.req.Reason, "run-lifecycle") {
		t.Fatalf("lifecycle request = %+v", fakeLifecycle.req)
	}
	if completed.Status != RunStatusCompleted || completed.TargetMode != TargetModeMemoryLifecycleSweep || completed.CommandID != "archived:2" {
		t.Fatalf("completed run = %+v", completed)
	}
}

func TestDispatcherToolCallUsesCreatorProfileAndRecordsCommand(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-tool", schedulerTestTime.Add(-time.Minute))
	task.Target = Target{
		Mode:          TargetModeToolCall,
		ToolID:        "todo.create",
		ToolArguments: map[string]any{"title": "Ship scheduler tool call"},
	}
	run := schedulerTestRun("run-tool", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	toolGateway := &fakeToolCallPort{result: ToolCallResult{Output: map[string]any{"todo_id": "todo-1"}}}
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock:              clock.NewFake(schedulerTestTime.Add(5 * time.Minute)),
		Repository:         repo,
		ToolCalls:          toolGateway,
		CreatorPermissions: schedulerTestCreatorResolver(),
	})

	completed, err := dispatcher.DispatchRun(ctx, task, run)
	if err != nil {
		t.Fatal(err)
	}

	if toolGateway.req.ToolID != "todo.create" || toolGateway.req.PrincipalID != "operator" || toolGateway.req.ProfileID != "human-operator" {
		t.Fatalf("tool call req = %+v", toolGateway.req)
	}
	if !containsTestString(toolGateway.req.Permissions, memorydistill.PermissionMemoryDistill) {
		t.Fatalf("tool call permissions = %+v", toolGateway.req.Permissions)
	}
	if toolGateway.req.Arguments["title"] != "Ship scheduler tool call" {
		t.Fatalf("tool arguments = %+v", toolGateway.req.Arguments)
	}
	if toolGateway.req.IdempotencyKey != "scheduled-run:run-tool" {
		t.Fatalf("tool call idempotency key = %q", toolGateway.req.IdempotencyKey)
	}
	if completed.Status != RunStatusCompleted || completed.CommandID != "tool:todo-1" {
		t.Fatalf("completed run = %+v", completed)
	}
}

func TestDispatcherSendsEvaluationRequestAsDiscussionWorkflow(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-evaluation", schedulerTestTime.Add(-time.Minute))
	task.Target = Target{
		Mode:           TargetModeDiscussionWorkflow,
		ConversationID: "discussion-1",
		LeadAgentID:    "agent-reviewer",
		Message:        "对 profile-agent-1 开展综合评价：请调用 evaluation_review 工具写入结论与人天估算。",
	}
	run := schedulerTestRun("run-evaluation", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	fakeDiscussion := &fakeDiscussionPort{conversation: discussionsession.ConversationDTO{
		ConversationID: "discussion-1",
		Messages: []discussionsession.MessageDTO{{
			MessageID: "message-review", AuthorID: "agent-reviewer", Content: "结论：讨论组推进稳定，创造价值约 2.5 人天。",
		}},
		Runs: []discussionsession.RunDTO{{
			RunID: "run-review", Usage: discussionsession.ModelUsageMap{"gpt-test": {InputTokens: 900, OutputTokens: 300, TotalTokens: 1200}},
		}},
	}}
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock:      clock.NewFake(schedulerTestTime.Add(5 * time.Minute)),
		Repository: repo,
		Discussion: fakeDiscussion,
	})

	completed, err := dispatcher.DispatchRun(ctx, task, run)
	if err != nil {
		t.Fatal(err)
	}

	if fakeDiscussion.cmd.ConversationID != "discussion-1" {
		t.Fatalf("conversation id = %q", fakeDiscussion.cmd.ConversationID)
	}
	if fakeDiscussion.cmd.DirectAgentID != "" || fakeDiscussion.cmd.DirectTargetID != "" {
		t.Fatalf("dispatcher should not special-case evaluator direct target: %+v", fakeDiscussion.cmd)
	}
	if !strings.Contains(fakeDiscussion.cmd.Content, "evaluation_review") || !strings.Contains(fakeDiscussion.cmd.Content, "人天估算") || !strings.Contains(fakeDiscussion.cmd.Content, "profile-agent-1") {
		t.Fatalf("evaluation request content = %q", fakeDiscussion.cmd.Content)
	}
	if completed.Status != RunStatusCompleted || completed.TargetMode != TargetModeDiscussionWorkflow || completed.ConversationID != "discussion-1" {
		t.Fatalf("completed run = %+v", completed)
	}
}

func TestDispatcherStoresFailedRunWhenTargetCallFails(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-fail", schedulerTestTime.Add(-time.Minute))
	run := schedulerTestRun("run-fail", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	targetErr := errors.New("discussion service offline")
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock:      clock.NewFake(schedulerTestTime.Add(5 * time.Minute)),
		Repository: repo,
		Discussion: &fakeDiscussionPort{err: targetErr},
	})

	failed, err := dispatcher.DispatchRun(ctx, task, run)
	if !errors.Is(err, targetErr) {
		t.Fatalf("error = %v, want target error", err)
	}
	runs, err := repo.ListRunsForTask(ctx, task.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}

	if failed.Status != RunStatusFailed || !strings.Contains(failed.FailureReason, "discussion service offline") {
		t.Fatalf("failed run = %+v", failed)
	}
	if len(runs) != 1 || runs[0].Status != RunStatusFailed || !strings.Contains(runs[0].FailureReason, "discussion service offline") {
		t.Fatalf("persisted runs = %+v", runs)
	}
}

func TestDispatcherRunsTodoAutomationScanAndRecordsCompletedRun(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-todo-automation", schedulerTestTime.Add(-time.Minute))
	task.Target = Target{Mode: TargetModeTodoAutomationScan}
	run := schedulerTestRun("run-todo-automation", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	automation := &fakeTodoAutomationPort{result: todoapp.TodoAutomationScanResult{EligibleCount: 1, PlannedCount: 1}}
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock:          clock.NewFake(schedulerTestTime.Add(5 * time.Minute)),
		Repository:     repo,
		TodoAutomation: automation,
	})

	completed, err := dispatcher.DispatchRun(ctx, task, run)
	if err != nil {
		t.Fatal(err)
	}
	if automation.calls != 1 {
		t.Fatalf("automation calls = %d", automation.calls)
	}
	if completed.Status != RunStatusCompleted || completed.TargetMode != TargetModeTodoAutomationScan || completed.CommandID != "todo-automation:run-todo-automation:eligible=1:planned=1:failed=0" {
		t.Fatalf("completed run = %+v", completed)
	}
	if completed.TodoAutomationScan == nil || completed.TodoAutomationScan.PlannedCount != 1 {
		t.Fatalf("scan result = %+v", completed.TodoAutomationScan)
	}
	persisted, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != RunStatusCompleted || persisted.CommandID != completed.CommandID {
		t.Fatalf("persisted run = %+v", persisted)
	}
}

func TestDispatcherPreservesTodoAutomationScanResultOnPartialFailure(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-todo-partial-failure", schedulerTestTime.Add(-time.Minute))
	task.Target = Target{Mode: TargetModeTodoAutomationScan}
	run := schedulerTestRun("run-todo-partial-failure", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	scanErr := errors.New("one Todo failed after other candidates were processed")
	result := todoapp.TodoAutomationScanResult{ScannedCount: 4, EligibleCount: 3, ClaimedCount: 3, PlannedCount: 2, FailedCount: 1}
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock: clock.NewFake(schedulerTestTime.Add(5 * time.Minute)), Repository: repo,
		TodoAutomation: &fakeTodoAutomationPort{result: result, err: scanErr},
	})

	failed, err := dispatcher.DispatchRun(ctx, task, run)
	if !errors.Is(err, scanErr) {
		t.Fatalf("DispatchRun error = %v, want %v", err, scanErr)
	}
	if failed.Status != RunStatusFailed || failed.TodoAutomationScan == nil || *failed.TodoAutomationScan != result {
		t.Fatalf("failed run = %+v", failed)
	}
	if failed.CommandID != "todo-automation:run-todo-partial-failure:eligible=3:planned=2:failed=1" {
		t.Fatalf("command id = %q", failed.CommandID)
	}
	persisted, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.TodoAutomationScan == nil || *persisted.TodoAutomationScan != result || persisted.CommandID != failed.CommandID {
		t.Fatalf("persisted failed run = %+v", persisted)
	}
}

func TestDispatcherRunsSystemResponsibilityReconciliationAndPersistsStats(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-responsibility", schedulerTestTime.Add(-time.Minute))
	task.Target = Target{Mode: TargetModeResponsibilityReconcile}
	task.CreatedBy = "system:responsibility-coordinator-bootstrap"
	task.CreatedByKind = ViewerKindSystem
	run := schedulerTestRun("run-responsibility", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	result := ResponsibilityReconcileResult{Candidates: 4, Claimed: 2, Dispatched: 1, RecoveredOutcomes: 1}
	port := &fakeResponsibilityReconcilePort{result: result}
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock: clock.NewFake(schedulerTestTime.Add(time.Minute)), Repository: repo, Responsibilities: port,
	})

	completed, err := dispatcher.DispatchRun(ctx, task, run)
	if err != nil {
		t.Fatal(err)
	}
	if port.calls != 1 || port.request.ScheduledTaskID != task.ScheduledTaskID || port.request.ScheduledRunID != run.ScheduledRunID {
		t.Fatalf("responsibility request calls=%d request=%+v", port.calls, port.request)
	}
	if completed.Status != RunStatusCompleted || completed.TargetMode != TargetModeResponsibilityReconcile || completed.ResponsibilityReconcile == nil || *completed.ResponsibilityReconcile != result {
		t.Fatalf("completed responsibility run = %+v", completed)
	}
	if completed.CommandID != "responsibility:run-responsibility:candidates=4:dispatched=1:failed=0" {
		t.Fatalf("command id = %q", completed.CommandID)
	}
	persisted, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ResponsibilityReconcile == nil || *persisted.ResponsibilityReconcile != result {
		t.Fatalf("persisted responsibility stats = %+v", persisted)
	}
}

func TestDispatcherResponsibilityEvaluationCompletesWithoutCreatingMissionWhenNoActionExists(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-responsibility-evaluation", schedulerTestTime.Add(-time.Minute))
	task.Target = Target{
		Mode: TargetModeResponsibilityEvaluation, AgentID: "agent-1",
		SourceScopeKind: ResponsibilityEvaluationPeoplePortfolioScope, SourceScopeID: "binding-1",
	}
	task.CreatedByKind = ViewerKindSystem
	run := schedulerTestRun("run-responsibility-evaluation", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	evaluation := &fakeResponsibilityEvaluationPort{decision: ResponsibilityEvaluationDecision{Result: ResponsibilityEvaluationResult{
		PortfolioID: "portfolio-1", PortfolioBindingID: "binding-1", PortfolioVersionID: "portfolio-version-1",
		SourceRevision: "1:1", SourceDigest: "digest-1", AgentProfileID: "agent-1", Evaluated: true,
		DecisionKind: ResponsibilityEvaluationNoAction, DecisionSourceRef: "portfolio-version-1", Reason: "no_concrete_obligation",
	}}}
	agentTasks := &fakeAgentTaskPort{}
	dispatcher := NewDispatcher(DispatcherOptions{
		Clock: clock.NewFake(schedulerTestTime.Add(time.Minute)), Repository: repo,
		ResponsibilityEvaluation: evaluation, AgentTasks: agentTasks,
	})

	completed, err := dispatcher.DispatchRun(ctx, task, run)
	if err != nil {
		t.Fatal(err)
	}
	if evaluation.calls != 1 || evaluation.request.TenantID != task.TenantID || evaluation.request.AgentProfileID != "agent-1" ||
		evaluation.request.PortfolioBindingID != "binding-1" || agentTasks.calls != 0 {
		t.Fatalf("evaluation calls=%d request=%+v agent_calls=%d", evaluation.calls, evaluation.request, agentTasks.calls)
	}
	if completed.Status != RunStatusCompleted || completed.TargetMode != TargetModeResponsibilityEvaluation ||
		completed.MissionID != "" || completed.TaskID != "" || completed.ResponsibilityID != "" || completed.ResponsibilityEvaluation == nil ||
		completed.ResponsibilityEvaluation.Actionable || completed.ResponsibilityEvaluation.MissionTaskMaterialized {
		t.Fatalf("completed evaluation run = %+v", completed)
	}
	if completed.CommandID != "responsibility-evaluation:run-responsibility-evaluation:no-action" {
		t.Fatalf("command id = %q", completed.CommandID)
	}
	persisted, err := repo.GetRun(ctx, run.ScheduledRunID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.ResponsibilityEvaluation == nil || persisted.ResponsibilityEvaluation.Reason != "no_concrete_obligation" {
		t.Fatalf("persisted evaluation = %+v", persisted)
	}
}

func TestDispatcherResponsibilityEvaluationLinksExistingMissionWithoutCreatingAnother(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-responsibility-link", schedulerTestTime.Add(-time.Minute))
	task.Target = Target{
		Mode: TargetModeResponsibilityEvaluation, AgentID: "agent-1",
		SourceScopeKind: ResponsibilityEvaluationPeoplePortfolioScope, SourceScopeID: "binding-1",
	}
	task.CreatedByKind = ViewerKindSystem
	run := schedulerTestRun("run-responsibility-link", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	evaluation := &fakeResponsibilityEvaluationPort{decision: ResponsibilityEvaluationDecision{
		Result: ResponsibilityEvaluationResult{
			PortfolioID: "portfolio-1", PortfolioBindingID: "binding-1", PortfolioVersionID: "portfolio-version-1",
			SourceRevision: "1:1", SourceDigest: "digest-1", AgentProfileID: "agent-1", Evaluated: true, Actionable: true,
			DecisionKind: ResponsibilityEvaluationLinkExistingMission, DecisionSourceRef: "responsibility_assignment:responsibility-existing:v2",
			Reason: "existing_mission_task", CandidateCount: 1, ActionableCount: 1,
			SelectedMissionID: "mission-existing", SelectedTaskID: "task-existing", SelectedResponsibilityID: "responsibility-existing",
			SelectedAssignmentState: "active", SelectedAssignmentVersion: 2,
		},
		MissionID: "mission-existing", TaskID: "task-existing", ResponsibilityID: "responsibility-existing",
	}}
	agentTasks := &fakeAgentTaskPort{}
	completed, err := NewDispatcher(DispatcherOptions{
		Clock: clock.NewFake(schedulerTestTime.Add(time.Minute)), Repository: repo,
		ResponsibilityEvaluation: evaluation, AgentTasks: agentTasks,
	}).DispatchRun(ctx, task, run)
	if err != nil {
		t.Fatal(err)
	}
	if agentTasks.calls != 0 || completed.MissionID != "mission-existing" || completed.TaskID != "task-existing" ||
		completed.ResponsibilityID != "responsibility-existing" || completed.ResponsibilityEvaluation == nil ||
		!completed.ResponsibilityEvaluation.LinkedExistingMission || completed.ResponsibilityEvaluation.MissionTaskMaterialized {
		t.Fatalf("agent_calls=%d completed=%+v", agentTasks.calls, completed)
	}
}

func TestDispatcherResponsibilityEvaluationMaterializesOnlyExplicitActionContract(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-responsibility-action", schedulerTestTime.Add(-time.Minute))
	task.Target = Target{
		Mode: TargetModeResponsibilityEvaluation, AgentID: "agent-1",
		SourceScopeKind: ResponsibilityEvaluationPeoplePortfolioScope, SourceScopeID: "binding-1",
	}
	task.CreatedByKind = ViewerKindSystem
	run := schedulerTestRun("run-responsibility-action", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	evaluation := &fakeResponsibilityEvaluationPort{decision: ResponsibilityEvaluationDecision{
		Result: ResponsibilityEvaluationResult{
			PortfolioID: "portfolio-1", PortfolioBindingID: "binding-1", PortfolioVersionID: "portfolio-version-1",
			SourceRevision: "1:1", SourceDigest: "digest-1", AgentProfileID: "agent-1", Evaluated: true, Actionable: true,
			DecisionKind: ResponsibilityEvaluationMaterializeAction, DecisionSourceRef: "people:signal:SIG-1", Reason: "structured_signal",
			CandidateCount: 1, ActionableCount: 1,
		},
		Action: &ResponsibilityAction{Title: "Handle concrete signal", TaskGoal: "Resolve signal SIG-1", Prompt: "Resolve only SIG-1 and attach evidence.", SourceRef: "people:signal:SIG-1"},
	}}
	agentTasks := &fakeAgentTaskPort{receipt: AgentTaskReceipt{
		CommandID: "responsibility-new", AgentID: "agent-1", MissionID: "mission-new", TaskID: "task-new", ResponsibilityID: "responsibility-new",
	}}
	completed, err := NewDispatcher(DispatcherOptions{
		Clock: clock.NewFake(schedulerTestTime.Add(time.Minute)), Repository: repo,
		ResponsibilityEvaluation: evaluation, AgentTasks: agentTasks, CreatorPermissions: schedulerTestCreatorResolver(),
	}).DispatchRun(ctx, task, run)
	if err != nil {
		t.Fatal(err)
	}
	if agentTasks.calls != 1 || agentTasks.req.TaskGoal != "Resolve signal SIG-1" || agentTasks.req.Prompt != "Resolve only SIG-1 and attach evidence." {
		t.Fatalf("agent calls=%d request=%+v", agentTasks.calls, agentTasks.req)
	}
	if completed.Status != RunStatusCompleted || completed.MissionID != "mission-new" || completed.TaskID != "task-new" ||
		completed.ResponsibilityEvaluation == nil || !completed.ResponsibilityEvaluation.MissionTaskMaterialized || completed.ResponsibilityEvaluation.LinkedExistingMission {
		t.Fatalf("completed action evaluation = %+v", completed)
	}
}

func TestDispatcherResponsibilityEvaluationRejectsUnboundActionableDecision(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-responsibility-unbound", schedulerTestTime.Add(-time.Minute))
	task.Target = Target{
		Mode: TargetModeResponsibilityEvaluation, AgentID: "agent-1",
		SourceScopeKind: ResponsibilityEvaluationPeoplePortfolioScope, SourceScopeID: "binding-1",
	}
	task.CreatedByKind = ViewerKindSystem
	run := schedulerTestRun("run-responsibility-unbound", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, run); err != nil {
		t.Fatal(err)
	}
	evaluation := &fakeResponsibilityEvaluationPort{decision: ResponsibilityEvaluationDecision{Result: ResponsibilityEvaluationResult{
		PortfolioID: "portfolio-1", PortfolioBindingID: "binding-1", PortfolioVersionID: "portfolio-version-1",
		SourceRevision: "1:1", SourceDigest: "digest-1", AgentProfileID: "agent-1", Evaluated: true, Actionable: true,
		DecisionKind: ResponsibilityEvaluationLinkExistingMission, DecisionSourceRef: "responsibility_assignment:unbound:v1",
		Reason: "unbound", CandidateCount: 1, ActionableCount: 1,
	}}}
	agentTasks := &fakeAgentTaskPort{}
	failed, err := NewDispatcher(DispatcherOptions{
		Clock: clock.NewFake(schedulerTestTime.Add(time.Minute)), Repository: repo,
		ResponsibilityEvaluation: evaluation, AgentTasks: agentTasks,
	}).DispatchRun(ctx, task, run)
	if !errors.Is(err, ErrInvalidTarget) || failed.Status != RunStatusFailed || agentTasks.calls != 0 {
		t.Fatalf("error=%v failed=%+v agent_calls=%d", err, failed, agentTasks.calls)
	}
}

func TestDispatcherResponsibilityReconciliationIsSystemOnlyAndPersistsPartialFailure(t *testing.T) {
	for _, test := range []struct {
		name       string
		creator    ViewerKind
		portErr    error
		wantErr    error
		wantCalls  int
		wantFailed int
	}{
		{name: "human task rejected", creator: ViewerKindHuman, wantErr: ErrInvalidTarget},
		{name: "partial system failure", creator: ViewerKindSystem, portErr: errors.New("one responsibility failed"), wantErr: errors.New("one responsibility failed"), wantCalls: 1, wantFailed: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			repo := NewRepository(env.DBRouter)
			ctx := context.Background()
			task := schedulerTestTask("sched-responsibility-failure", schedulerTestTime)
			task.Target = Target{Mode: TargetModeResponsibilityReconcile}
			task.CreatedByKind = test.creator
			run := schedulerTestRun("run-responsibility-failure", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
			if err := repo.SaveRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			port := &fakeResponsibilityReconcilePort{result: ResponsibilityReconcileResult{Candidates: 3, Failed: test.wantFailed}, err: test.portErr}
			failed, err := NewDispatcher(DispatcherOptions{Clock: clock.NewFake(schedulerTestTime), Repository: repo, Responsibilities: port}).DispatchRun(ctx, task, run)
			if test.portErr != nil {
				if !errors.Is(err, test.portErr) {
					t.Fatalf("error = %v, want %v", err, test.portErr)
				}
			} else if !errors.Is(err, test.wantErr) {
				t.Fatalf("error = %v, want %v", err, test.wantErr)
			}
			if port.calls != test.wantCalls || failed.Status != RunStatusFailed {
				t.Fatalf("calls=%d failed=%+v", port.calls, failed)
			}
			persisted, getErr := repo.GetRun(ctx, run.ScheduledRunID)
			if getErr != nil {
				t.Fatal(getErr)
			}
			if test.portErr != nil && (persisted.ResponsibilityReconcile == nil || persisted.ResponsibilityReconcile.Failed != 1) {
				t.Fatalf("partial stats were not persisted: %+v", persisted)
			}
		})
	}
}

func TestDispatcherOntologyStateRefreshIsSystemOnlyAndPersistsResult(t *testing.T) {
	for _, tc := range []struct {
		name      string
		creator   ViewerKind
		wantCalls int
		wantErr   error
	}{
		{name: "human rejected", creator: ViewerKindHuman, wantErr: ErrInvalidTarget},
		{name: "system dispatched", creator: ViewerKindSystem, wantCalls: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			repo := NewRepository(env.DBRouter)
			ctx := context.Background()
			task := schedulerTestTask("sched-ontology-state-refresh", schedulerTestTime)
			task.Target = Target{Mode: TargetModeOntologyStateRefresh}
			task.CreatedByKind = tc.creator
			run := schedulerTestRun("run-ontology-state-refresh", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
			if err := repo.SaveRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			port := &fakeOntologyStateRefreshPort{result: OntologyStateRefreshResult{
				Fingerprint: "fingerprint-1", SourceCount: 10, ChangedCount: 2,
				SnapshotOnlyCount: 3, NoopCount: 4, StaleCount: 1,
			}}
			completed, err := NewDispatcher(DispatcherOptions{
				Clock: clock.NewFake(schedulerTestTime.Add(time.Minute)), Repository: repo, OntologyStateRefresh: port,
			}).DispatchRun(ctx, task, run)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
				if completed.Status != RunStatusFailed || port.calls != 0 {
					t.Fatalf("completed=%+v calls=%d", completed, port.calls)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			wantSummary := "Ontology state refresh: source=10 changed=2 snapshot_only=3 noop=4 stale=1"
			if port.calls != tc.wantCalls || port.request.ScheduledTaskID != task.ScheduledTaskID || port.request.ScheduledRunID != run.ScheduledRunID ||
				completed.Status != RunStatusCompleted || completed.CommandID != "ontology-state-refresh:fingerprint-1" || completed.Result.Summary != wantSummary {
				t.Fatalf("completed=%+v port=%+v", completed, port)
			}
			persisted, err := repo.GetRun(ctx, run.ScheduledRunID)
			if err != nil {
				t.Fatal(err)
			}
			if persisted.CommandID != completed.CommandID || persisted.Result.Summary != wantSummary {
				t.Fatalf("persisted run = %+v", persisted)
			}
		})
	}
}

func TestDispatcherCleansExpiredTodoAutomationRunsAfterSuccessAndFailure(t *testing.T) {
	for _, test := range []struct {
		name          string
		automationErr error
		wantRunStatus RunStatus
	}{
		{name: "success", wantRunStatus: RunStatusCompleted},
		{name: "failure", automationErr: errors.New("automation unavailable"), wantRunStatus: RunStatusFailed},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			repo := NewRepository(env.DBRouter)
			ctx := context.Background()
			now := schedulerTestTime.Add(100 * 24 * time.Hour)
			expired := retentionTestRun("expired-"+test.name, RunStatusFailed, now.Add(-91*24*time.Hour), nil)
			if err := repo.SaveRun(ctx, expired); err != nil {
				t.Fatal(err)
			}
			task := schedulerTestTask("sched-retention-"+test.name, now.Add(-time.Minute))
			task.Target = Target{Mode: TargetModeTodoAutomationScan}
			run := schedulerTestRun("run-retention-"+test.name, task.ScheduledTaskID, RunStatusQueued, now)
			if err := repo.SaveRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			dispatcher := NewDispatcher(DispatcherOptions{
				Clock: clock.NewFake(now), Repository: repo,
				TodoAutomation: &fakeTodoAutomationPort{err: test.automationErr},
			})
			completed, err := dispatcher.DispatchRun(ctx, task, run)
			if test.automationErr == nil && err != nil {
				t.Fatalf("DispatchRun: %v", err)
			}
			if test.automationErr != nil && !errors.Is(err, test.automationErr) {
				t.Fatalf("DispatchRun error = %v, want %v", err, test.automationErr)
			}
			if completed.Status != test.wantRunStatus {
				t.Fatalf("completed status = %s, want %s", completed.Status, test.wantRunStatus)
			}
			if _, err := repo.GetRun(ctx, expired.ScheduledRunID); !errors.Is(err, ErrNotFound) {
				t.Fatalf("expired run error = %v, want ErrNotFound", err)
			}
		})
	}
}

func TestDispatcherCoalescesRepeatedEmptyTodoAutomationRunsByDay(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-empty-aggregation", schedulerTestTime)
	task.Target = Target{Mode: TargetModeTodoAutomationScan}
	first := schedulerTestRun("run-empty-first", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, first); err != nil {
		t.Fatal(err)
	}
	firstDispatcher := NewDispatcher(DispatcherOptions{
		Clock: clock.NewFake(schedulerTestTime), Repository: repo, TodoAutomation: &fakeTodoAutomationPort{},
	})
	if _, err := firstDispatcher.DispatchRun(ctx, task, first); err != nil {
		t.Fatal(err)
	}
	secondAt := schedulerTestTime.Add(5 * time.Minute)
	second := schedulerTestRun("run-empty-second", task.ScheduledTaskID, RunStatusQueued, secondAt)
	if err := repo.SaveRun(ctx, second); err != nil {
		t.Fatal(err)
	}
	secondDispatcher := NewDispatcher(DispatcherOptions{
		Clock: clock.NewFake(secondAt), Repository: repo, TodoAutomation: &fakeTodoAutomationPort{},
	})
	completed, err := secondDispatcher.DispatchRun(ctx, task, second)
	if err != nil {
		t.Fatal(err)
	}
	if completed.AggregatedIntoRunID != first.ScheduledRunID {
		t.Fatalf("aggregated into = %q, want %q", completed.AggregatedIntoRunID, first.ScheduledRunID)
	}
	runs, err := repo.ListRunsForTask(ctx, task.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].ScheduledRunID != first.ScheduledRunID || runs[0].AggregatedOccurrences != 2 || !runs[0].LastOccurrenceAt.Equal(secondAt) {
		t.Fatalf("aggregated runs = %+v", runs)
	}
}

func TestDispatcherDoesNotCoalesceRecoveryOnlyTodoAutomationRun(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repo := NewRepository(env.DBRouter)
	ctx := context.Background()
	task := schedulerTestTask("sched-recovery-diagnostic", schedulerTestTime)
	task.Target = Target{Mode: TargetModeTodoAutomationScan}
	first := schedulerTestRun("run-recovery-empty", task.ScheduledTaskID, RunStatusQueued, schedulerTestTime)
	if err := repo.SaveRun(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDispatcher(DispatcherOptions{
		Clock: clock.NewFake(schedulerTestTime), Repository: repo, TodoAutomation: &fakeTodoAutomationPort{},
	}).DispatchRun(ctx, task, first); err != nil {
		t.Fatal(err)
	}

	secondAt := schedulerTestTime.Add(5 * time.Minute)
	second := schedulerTestRun("run-recovery-diagnostic", task.ScheduledTaskID, RunStatusQueued, secondAt)
	if err := repo.SaveRun(ctx, second); err != nil {
		t.Fatal(err)
	}
	completed, err := NewDispatcher(DispatcherOptions{
		Clock: clock.NewFake(secondAt), Repository: repo,
		TodoAutomation: &fakeTodoAutomationPort{result: todoapp.TodoAutomationScanResult{RecoveredCount: 1}},
	}).DispatchRun(ctx, task, second)
	if err != nil {
		t.Fatal(err)
	}
	if completed.AggregatedIntoRunID != "" {
		t.Fatalf("recovery run aggregated into %q", completed.AggregatedIntoRunID)
	}
	runs, err := repo.ListRunsForTask(ctx, task.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 || runs[1].ScheduledRunID != second.ScheduledRunID || runs[1].TodoAutomationScan == nil || runs[1].TodoAutomationScan.RecoveredCount != 1 {
		t.Fatalf("diagnostic runs = %+v", runs)
	}
}

type fakeMemoryDistillationPort struct {
	viewer        memorydistill.ViewerRef
	req           memorydistill.DistillExperienceRequest
	response      memorydistill.DistillExperienceResponse
	worldViewer   memorydistill.ViewerRef
	worldReq      memorydistill.DistillWorldRequest
	worldResponse memorydistill.DistillWorldResponse
	err           error
}

func (f *fakeMemoryDistillationPort) DistillExperience(ctx context.Context, viewer memorydistill.ViewerRef, req memorydistill.DistillExperienceRequest) (memorydistill.DistillExperienceResponse, error) {
	f.viewer = viewer
	f.req = req
	if f.err != nil {
		return memorydistill.DistillExperienceResponse{}, f.err
	}
	return f.response, nil
}

func (f *fakeMemoryDistillationPort) DistillWorld(ctx context.Context, viewer memorydistill.ViewerRef, req memorydistill.DistillWorldRequest) (memorydistill.DistillWorldResponse, error) {
	f.worldViewer = viewer
	f.worldReq = req
	if f.err != nil {
		return memorydistill.DistillWorldResponse{}, f.err
	}
	return f.worldResponse, nil
}

type fakeMemoryLifecyclePort struct {
	viewer   memoryadmin.ViewerRef
	req      memoryadmin.MemoryRetentionSweepRequest
	response memoryadmin.MemoryRetentionSweepResultDTO
	err      error
}

func (f *fakeMemoryLifecyclePort) SweepRetention(ctx context.Context, viewer memoryadmin.ViewerRef, req memoryadmin.MemoryRetentionSweepRequest) (memoryadmin.MemoryRetentionSweepResultDTO, error) {
	f.viewer = viewer
	f.req = req
	if f.err != nil {
		return memoryadmin.MemoryRetentionSweepResultDTO{}, f.err
	}
	return f.response, nil
}

type fakeTodoAutomationPort struct {
	calls  int
	result todoapp.TodoAutomationScanResult
	err    error
}

type fakeResponsibilityReconcilePort struct {
	calls   int
	request ResponsibilityReconcileRequest
	result  ResponsibilityReconcileResult
	err     error
}

type fakeResponsibilityEvaluationPort struct {
	calls    int
	request  ResponsibilityEvaluationRequest
	decision ResponsibilityEvaluationDecision
	err      error
}

type fakeOntologyStateRefreshPort struct {
	calls   int
	request OntologyStateRefreshRequest
	result  OntologyStateRefreshResult
	err     error
}

func (f *fakeOntologyStateRefreshPort) RefreshOntologyState(_ context.Context, request OntologyStateRefreshRequest) (OntologyStateRefreshResult, error) {
	f.calls++
	f.request = request
	return f.result, f.err
}

func (f *fakeResponsibilityEvaluationPort) EvaluateResponsibility(_ context.Context, request ResponsibilityEvaluationRequest) (ResponsibilityEvaluationDecision, error) {
	f.calls++
	f.request = request
	return f.decision, f.err
}

func (f *fakeResponsibilityReconcilePort) ReconcileResponsibilities(_ context.Context, request ResponsibilityReconcileRequest) (ResponsibilityReconcileResult, error) {
	f.calls++
	f.request = request
	return f.result, f.err
}

func (f *fakeTodoAutomationPort) RunScheduledAutomationScan(context.Context, todoapp.TodoAutomationScanRequest) (todoapp.TodoAutomationScanResult, error) {
	f.calls++
	return f.result, f.err
}

func containsTestString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func schedulerTestCreatorResolver() *fakeCreatorPermissionResolver {
	return &fakeCreatorPermissionResolver{permissions: CreatorPermissions{
		PermissionScope: []string{
			"mission.read",
			memorydistill.PermissionMemoryDistill,
			memoryadmin.PermissionMemoryRead,
			memoryadmin.PermissionMemoryReview,
			memoryadmin.PermissionMemoryWorldManage,
			memoryadmin.PermissionMemoryAuditRead,
		},
		AgentID:         "operator",
		OrganizationIDs: []string{"org-1"},
		ProjectIDs:      []string{"project-1"},
		MissionIDs:      []string{"mission-1"},
	}}
}

type fakeCreatorPermissionResolver struct {
	ref         CreatorRef
	permissions CreatorPermissions
	err         error
}

func (f *fakeCreatorPermissionResolver) ResolveCreatorPermissions(ctx context.Context, ref CreatorRef) (CreatorPermissions, error) {
	f.ref = ref
	if f.err != nil {
		return CreatorPermissions{}, f.err
	}
	return f.permissions, nil
}

type fakeToolCallPort struct {
	req    ToolCallRequest
	result ToolCallResult
	err    error
}

func (f *fakeToolCallPort) CallTool(ctx context.Context, req ToolCallRequest) (ToolCallResult, error) {
	f.req = req
	if f.err != nil {
		return ToolCallResult{}, f.err
	}
	return f.result, nil
}

type fakeAgentTaskPort struct {
	calls   int
	req     AgentTaskRequest
	receipt AgentTaskReceipt
	err     error
}

func (f *fakeAgentTaskPort) StartAgentTask(ctx context.Context, req AgentTaskRequest) (AgentTaskReceipt, error) {
	f.calls++
	f.req = req
	if f.err != nil {
		return AgentTaskReceipt{}, f.err
	}
	return f.receipt, nil
}

type fakeDiscussionPort struct {
	viewer       discussionsession.ViewerRef
	cmd          discussionsession.SendMessageCommand
	conversation discussionsession.ConversationDTO
	err          error
}

func (f *fakeDiscussionPort) SendMessage(ctx context.Context, viewer discussionsession.ViewerRef, cmd discussionsession.SendMessageCommand) (discussionsession.ConversationDTO, error) {
	f.viewer = viewer
	f.cmd = cmd
	if f.err != nil {
		return discussionsession.ConversationDTO{}, f.err
	}
	return f.conversation, nil
}
