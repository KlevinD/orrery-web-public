package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimedaemon "orrery_v2/internal/agent/runtime/daemon"
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/responsibility"
	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/projection"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/clock"
	"orrery_v2/internal/platform/execution"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestProductionResponsibilityAssignmentDispatchesRealMissionRunWithProfileRuntime(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()

	const tenantID = "tenant-responsibility"
	const missionID = "mission-responsibility"
	const taskID = "task-responsibility"
	const agentID = "responsible-agent"
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	seedProductionChatProfile(t, ctx, profiles, agentID, "任执行", "Worker")
	profile, err := profiles.GetProfile(ctx, agentID)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	profile.RuntimeSpec = agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindOpenClaw}
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("save runtime profile: %v", err)
	}

	tasks := missiontask.NewPlatformRepository()
	record := productionResponsibilityTask(taskID, missionID, agentID, missiontask.TaskStatusAssigned)
	saveProductionResponsibilityTask(t, env, tasks, record)
	missionRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	at := env.Clock.Now().UTC()
	model := projection.ReadModel{
		Mission: projection.MissionView{MissionID: missionID, Title: "Responsibility", FinalGoal: "Dispatch automatically", Status: projection.MissionStatusActive, CreatedAt: at},
		Team: projection.TeamView{Members: []projection.TeamMemberView{{
			AgentID: agentID, AgentProfileID: agentID, AgentInstanceID: agentcore.AgentInstanceID(agentID + ":" + missionID), AgentKind: "ai", DisplayName: "任执行", MissionRole: "worker", Status: projection.TeamMemberJoined, JoinedAt: at,
		}}},
		TaskBoard:  projection.TaskBoardView{Tasks: []projection.TaskCardView{{TaskID: taskID, Assignee: agentID, Goal: record.Contract.Goal, WorkerPersonalGoalSummary: record.Contract.WorkerPersonalGoal, Status: projection.TaskStatusAssigned, UpdatedAt: at}}},
		Checkpoint: projection.Checkpoint{MissionID: missionID, Version: 1, UpdatedAt: at}, UpdatedAt: at,
	}
	if err := missionRepo.SaveForTenant(ctx, tenantID, model); err != nil {
		t.Fatalf("save mission projection: %v", err)
	}

	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	service := responsibility.NewService(responsibility.ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	assignmentPort := productionTaskResponsibilityAssignment{service: service}
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return assignmentPort.RecordTaskAssignment(ctx, tx, missioncontrol.TaskResponsibilityAssignmentSignal{
			TenantID: tenantID, MissionID: missionID, TaskID: taskID, AgentProfileID: agentID, Priority: 75,
			SourceKind: "scheduler_agent_task", SourceID: "schedule-1", SourceStepID: "run-1",
		})
	}); err != nil {
		t.Fatalf("record task assignment: %v", err)
	}
	ready, err := repository.ListReadyActivations(ctx, env.Clock.Now(), 10)
	if err != nil || len(ready) != 1 {
		t.Fatalf("durable activations = %+v err=%v", ready, err)
	}
	assignment, err := repository.GetAssignmentForWork(ctx, tenantID, responsibility.WorkKindMissionTask, taskID)
	if err != nil || assignment.SourceKind != "scheduler_agent_task" || assignment.SourceID != "schedule-1" || assignment.SourceStepID != "run-1" {
		t.Fatalf("assignment source = %+v err=%v", assignment, err)
	}

	authorizer := authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}})
	commands := missioncontrol.NewCommandService(nil)
	commands.SetAuthorizationService(authorizer)
	commands.SetRuntimeCommandPorts(productionAgentRunQueue{queue: env.ExecutionQueue}, missionRepo, missionevent.NewStoreSink(env.EventStore), env.IDGenerator, env.Clock)
	commands.SetRuntimeEventReader(missionevent.NewStoreReader(env.EventStore))
	coordinator := responsibility.NewCoordinator(responsibility.CoordinatorOptions{
		Repository: repository, Service: service, Profiles: profiles,
		Dispatcher: productionResponsibilityRuntimeDispatcher{commands: commands}, Clock: env.Clock, IDGenerator: env.IDGenerator,
		WorkerID: "production-responsibility-test", ClaimLease: time.Minute, MaxParallel: 1,
	})
	stats, err := coordinator.Reconcile(ctx, 10)
	if err != nil {
		t.Fatalf("reconcile responsibility: %v", err)
	}
	if stats.Dispatched != 1 {
		t.Fatalf("reconcile stats = %+v", stats)
	}
	runID := responsibility.RunIDForActivation(ready[0].ID)
	job, ok, err := env.ExecutionQueue.Get(ctx, runID)
	if err != nil || !ok {
		t.Fatalf("queued responsibility run found=%v err=%v", ok, err)
	}
	run, err := runtimedaemon.DecodeAgentRunPayload(job.Payload)
	if err != nil {
		t.Fatalf("decode queued Agent run: %v", err)
	}
	if run.RunID.String() != runID || run.RuntimeID != string(agentcore.RuntimeKindOpenClaw) || run.AgentProfileID != agentID || run.TaskID != taskID || run.Request.TenantID != tenantID || run.Attempt != 1 {
		t.Fatalf("queued Agent run = %+v", run)
	}
	attempts, err := repository.ListAttempts(ctx, responsibility.AssignmentID(tenantID, responsibility.WorkKindMissionTask, taskID))
	if err != nil || len(attempts) != 1 || attempts[0].RuntimeKind != string(agentcore.RuntimeKindOpenClaw) || attempts[0].AgentRunID != runID {
		t.Fatalf("responsibility attempts = %+v err=%v", attempts, err)
	}
}

func TestProductionResponsibilityCancellationTreatsMissingRuntimeAsStopped(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()

	const tenantID = "tenant-cancel-missing"
	const missionID = "mission-cancel-missing"
	missionRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	at := env.Clock.Now().UTC()
	if err := missionRepo.SaveForTenant(ctx, tenantID, projection.ReadModel{
		Mission:    projection.MissionView{MissionID: missionID, Title: "Cancellation", FinalGoal: "Stop stale responsibility runs", Status: projection.MissionStatusActive, CreatedAt: at},
		Checkpoint: projection.Checkpoint{MissionID: missionID, Version: 1, UpdatedAt: at},
		UpdatedAt:  at,
	}); err != nil {
		t.Fatalf("save mission projection: %v", err)
	}

	authorizer := authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}})
	commands := missioncontrol.NewCommandService(nil)
	commands.SetAuthorizationService(authorizer)
	commands.SetRuntimeCommandPorts(productionAgentRunQueue{queue: env.ExecutionQueue}, missionRepo, missionevent.NewStoreSink(env.EventStore), env.IDGenerator, env.Clock)
	dispatcher := productionResponsibilityRuntimeDispatcher{commands: commands}
	if err := dispatcher.CancelResponsibility(ctx, responsibility.RuntimeCancelRequest{
		TenantID:  tenantID,
		MissionID: missionID,
		RunID:     "responsibility-run-missing",
		Reason:    "assignment already terminated",
	}); err != nil {
		t.Fatalf("cancel missing responsibility runtime: %v", err)
	}
}

func TestProductionResponsibilityRuntimeOutcomeUsesEventBeforeQueueFallback(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 20, 0, 0, time.UTC)
	source := productionResponsibilityRuntimeOutcomeSource{
		events: staticProductionResponsibilityEventReader{events: []missionevent.Envelope{{
			TenantID: "tenant-a", MissionID: "mission-a", EventType: missionevent.EventAgentRunBlocked, OccurredAt: now,
			Payload: missionevent.RuntimeFailurePayload{TaskID: "task-a", RunID: "run-a", Message: "business blocker"},
		}}},
		jobs: staticProductionResponsibilityJobReader{err: errors.New("terminal event must not query queue")},
	}

	fact, found, err := source.FindTerminalRuntimeOutcome(context.Background(), "tenant-a", "mission-a", "task-a", "run-a")
	if err != nil || !found || fact.Outcome != responsibility.AttemptOutcomeBlocked || fact.Error != "business blocker" {
		t.Fatalf("event outcome=%+v found=%v err=%v", fact, found, err)
	}
}

func TestProductionResponsibilityRuntimeOutcomeFallsBackToTerminalQueueFailures(t *testing.T) {
	tests := []struct {
		name      string
		status    execution.Status
		lastError string
		failure   string
		found     bool
		outcome   responsibility.AttemptOutcome
		message   string
	}{
		{name: "failed", status: execution.StatusFailed, lastError: "worker failed", failure: "fallback failure", found: true, outcome: responsibility.AttemptOutcomeFailed, message: "worker failed"},
		{name: "dead letter", status: execution.StatusDeadLetter, failure: "attempts exhausted", found: true, outcome: responsibility.AttemptOutcomeFailed, message: "attempts exhausted"},
		{name: "cancelled", status: execution.StatusCancelled, lastError: "old retry failure", failure: "operator cancelled", found: true, outcome: responsibility.AttemptOutcomeCancelled, message: "operator cancelled"},
		{name: "completed remains ambiguous", status: execution.StatusCompleted},
		{name: "running is not terminal", status: execution.StatusRunning},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			job := productionResponsibilityRuntimeJob(t, "tenant-a", "mission-a", "task-a", "run-a", test.status)
			job.LastError = test.lastError
			job.FailureReason = test.failure
			source := productionResponsibilityRuntimeOutcomeSource{
				events: staticProductionResponsibilityEventReader{},
				jobs:   staticProductionResponsibilityJobReader{jobs: map[string]execution.JobStatus{"run-a": job}},
			}

			fact, found, err := source.FindTerminalRuntimeOutcome(context.Background(), "tenant-a", "mission-a", "task-a", "run-a")
			if err != nil || found != test.found {
				t.Fatalf("queue outcome=%+v found=%v err=%v", fact, found, err)
			}
			if found && (fact.TenantID != "tenant-a" || fact.TaskID != "task-a" || fact.RunID != "run-a" || fact.Outcome != test.outcome || fact.Error != test.message) {
				t.Fatalf("queue outcome = %+v", fact)
			}
		})
	}
}

func TestProductionResponsibilityRuntimeOutcomePropagatesQueueErrorsWithoutTerminalEvent(t *testing.T) {
	want := errors.New("queue lookup failed")
	source := productionResponsibilityRuntimeOutcomeSource{
		events: staticProductionResponsibilityEventReader{},
		jobs:   staticProductionResponsibilityJobReader{err: want},
	}
	_, _, err := source.FindTerminalRuntimeOutcome(context.Background(), "tenant-a", "mission-a", "task-a", "run-a")
	if !errors.Is(err, want) {
		t.Fatalf("queue error = %v", err)
	}
}

func TestProductionResponsibilityRuntimeHealthUsesLiveHeartbeatDuringSilentRun(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 20, 0, 0, time.UTC)
	startedAt := now.Add(-2 * time.Minute)
	lastSeenAt := now.Add(-10 * time.Second)
	leaseExpiresAt := now.Add(50 * time.Second)
	job := productionResponsibilityRuntimeJob(t, "tenant-a", "mission-a", "task-a", "run-a", execution.StatusRunning)
	job.StartedAt = startedAt
	job.UpdatedAt = now.Add(time.Minute)
	job.LeaseUntil = now.Add(45 * time.Second)
	source := productionResponsibilityRuntimeHealthSource{
		events: staticProductionResponsibilityEventReader{events: []missionevent.Envelope{{
			TenantID: "tenant-a", MissionID: "mission-a", EventType: missionevent.EventAgentRunStarted, OccurredAt: startedAt,
			Payload: missionevent.RuntimeLifecyclePayload{
				TaskID: "task-a", RunID: "run-a", Usage: map[string]agentcore.TokenUsage{"model-a": {InputTokens: 11, OutputTokens: 7}},
			},
		}}},
		jobs: staticProductionResponsibilityJobReader{jobs: map[string]execution.JobStatus{"run-a": job}},
		liveness: staticProductionResponsibilityLiveness{snapshots: map[string]execution.LivenessSnapshot{
			"agent-run:run-a": {RuntimeID: "agent-run:run-a", LastSeenAt: lastSeenAt, ExpiresAt: leaseExpiresAt, Alive: true},
		}},
	}

	fact, found, err := source.FindRuntimeHealth(context.Background(), "tenant-a", "mission-a", "task-a", "run-a")
	if err != nil || !found {
		t.Fatalf("FindRuntimeHealth found=%v err=%v", found, err)
	}
	if !fact.LastHeartbeatAt.Equal(lastSeenAt) || !fact.LeaseExpiresAt.Equal(leaseExpiresAt) || fact.UsageTokens != 18 {
		t.Fatalf("runtime health = %+v", fact)
	}
}

func TestProductionResponsibilityRuntimeHealthFallsBackToValidatedQueueLease(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 20, 0, 0, time.UTC)
	startedAt := now.Add(-2 * time.Minute)
	leaseExpiresAt := now.Add(time.Minute)
	job := productionResponsibilityRuntimeJob(t, "tenant-a", "mission-a", "task-a", "run-a", execution.StatusRunning)
	job.StartedAt = startedAt
	job.UpdatedAt = leaseExpiresAt
	job.LeaseUntil = leaseExpiresAt
	source := productionResponsibilityRuntimeHealthSource{
		events:   staticProductionResponsibilityEventReader{},
		jobs:     staticProductionResponsibilityJobReader{jobs: map[string]execution.JobStatus{"run-a": job}},
		liveness: staticProductionResponsibilityLiveness{},
	}

	fact, found, err := source.FindRuntimeHealth(context.Background(), "tenant-a", "mission-a", "task-a", "run-a")
	if err != nil || !found {
		t.Fatalf("FindRuntimeHealth found=%v err=%v", found, err)
	}
	if !fact.LastHeartbeatAt.Equal(startedAt) || !fact.LeaseExpiresAt.Equal(leaseExpiresAt) {
		t.Fatalf("queue-backed runtime health = %+v", fact)
	}
}

func TestProductionResponsibilityRuntimeHealthUsesAutomaticQueueRenewalAfterLivenessExpires(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 20, 0, 0, time.UTC)
	renewedAt := now.Add(-10 * time.Second)
	job := productionResponsibilityRuntimeJob(t, "tenant-a", "mission-a", "task-a", "run-a", execution.StatusRunning)
	job.StartedAt = now.Add(-10 * time.Minute)
	job.UpdatedAt = renewedAt
	job.LeaseUntil = renewedAt.Add(5 * time.Second)
	source := productionResponsibilityRuntimeHealthSource{
		events:   staticProductionResponsibilityEventReader{},
		jobs:     staticProductionResponsibilityJobReader{jobs: map[string]execution.JobStatus{"run-a": job}},
		liveness: staticProductionResponsibilityLiveness{},
		clock:    clock.NewFake(now),
	}

	fact, found, err := source.FindRuntimeHealth(context.Background(), "tenant-a", "mission-a", "task-a", "run-a")
	if err != nil || !found {
		t.Fatalf("FindRuntimeHealth found=%v err=%v", found, err)
	}
	if !fact.LastHeartbeatAt.Equal(renewedAt) || !fact.LeaseExpiresAt.Equal(job.LeaseUntil) {
		t.Fatalf("renewed queue health = %+v", fact)
	}
}

func TestProductionResponsibilityRuntimeHealthTracksQueuedRetryWindow(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 20, 0, 0, time.UTC)
	updatedAt := now.Add(-10 * time.Second)
	nextAttemptAt := now.Add(5 * time.Minute)
	job := productionResponsibilityRuntimeJob(t, "tenant-a", "mission-a", "task-a", "run-a", execution.StatusQueued)
	job.CreatedAt = now.Add(-10 * time.Minute)
	job.UpdatedAt = updatedAt
	job.NextAttemptAt = nextAttemptAt
	source := productionResponsibilityRuntimeHealthSource{
		events:   staticProductionResponsibilityEventReader{},
		jobs:     staticProductionResponsibilityJobReader{jobs: map[string]execution.JobStatus{"run-a": job}},
		liveness: staticProductionResponsibilityLiveness{},
	}

	fact, found, err := source.FindRuntimeHealth(context.Background(), "tenant-a", "mission-a", "task-a", "run-a")
	if err != nil || !found || fact.Terminal {
		t.Fatalf("queued retry health=%+v found=%v err=%v", fact, found, err)
	}
	if !fact.LastHeartbeatAt.Equal(updatedAt) || !fact.TimeoutBaseAt.Equal(nextAttemptAt) || !fact.LeaseExpiresAt.IsZero() {
		t.Fatalf("queued retry health = %+v", fact)
	}
}

func TestProductionResponsibilityRuntimeHealthMergesEventSignalsMonotonically(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 20, 0, 0, time.UTC)
	leaseExpiresAt := now.Add(time.Minute)
	job := productionResponsibilityRuntimeJob(t, "tenant-a", "mission-a", "task-a", "run-a", execution.StatusRunning)
	job.StartedAt = now.Add(-2 * time.Minute)
	source := productionResponsibilityRuntimeHealthSource{
		events: staticProductionResponsibilityEventReader{events: []missionevent.Envelope{
			{
				TenantID: "tenant-a", MissionID: "mission-a", EventType: missionevent.EventAgentRunStarted, OccurredAt: now.Add(-20 * time.Second),
				Payload: missionevent.RuntimeLifecyclePayload{
					TaskID: "task-a", RunID: "run-a", LeaseExpiresAt: leaseExpiresAt,
					Usage: map[string]agentcore.TokenUsage{"model-a": {InputTokens: 11, OutputTokens: 7}},
				},
			},
			{
				TenantID: "tenant-a", MissionID: "mission-a", EventType: missionevent.EventAgentRunStarted, OccurredAt: now.Add(-10 * time.Second),
				Payload: missionevent.RuntimeLifecyclePayload{
					TaskID: "task-a", RunID: "run-a",
					Usage: map[string]agentcore.TokenUsage{"model-a": {InputTokens: 3, OutputTokens: 2}},
				},
			},
		}},
		jobs:     staticProductionResponsibilityJobReader{jobs: map[string]execution.JobStatus{"run-a": job}},
		liveness: staticProductionResponsibilityLiveness{},
	}

	fact, found, err := source.FindRuntimeHealth(context.Background(), "tenant-a", "mission-a", "task-a", "run-a")
	if err != nil || !found {
		t.Fatalf("merged event health=%+v found=%v err=%v", fact, found, err)
	}
	if !fact.LastHeartbeatAt.Equal(now.Add(-10*time.Second)) || !fact.LeaseExpiresAt.Equal(leaseExpiresAt) || fact.UsageTokens != 18 {
		t.Fatalf("merged event health = %+v", fact)
	}
}

func TestProductionResponsibilityRuntimeHealthMarksTerminalWithoutInferringOutcome(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 20, 0, 0, time.UTC)
	startedAt := now.Add(-2 * time.Minute)
	job := productionResponsibilityRuntimeJob(t, "tenant-a", "mission-a", "task-a", "run-a", execution.StatusCompleted)
	job.StartedAt = startedAt
	job.CompletedAt = now
	source := productionResponsibilityRuntimeHealthSource{
		events: staticProductionResponsibilityEventReader{events: []missionevent.Envelope{{
			TenantID: "tenant-a", MissionID: "mission-a", EventType: missionevent.EventAgentRunStarted, OccurredAt: startedAt,
			Payload: missionevent.RuntimeLifecyclePayload{
				TaskID: "task-a", RunID: "run-a", Usage: map[string]agentcore.TokenUsage{"model-a": {InputTokens: 11, OutputTokens: 7}},
			},
		}}},
		jobs:     staticProductionResponsibilityJobReader{jobs: map[string]execution.JobStatus{"run-a": job}},
		liveness: staticProductionResponsibilityLiveness{err: errors.New("terminal queue must not query liveness")},
	}

	fact, found, err := source.FindRuntimeHealth(context.Background(), "tenant-a", "mission-a", "task-a", "run-a")
	if err != nil || !found || !fact.Terminal {
		t.Fatalf("terminal queue health=%+v found=%v err=%v", fact, found, err)
	}
	if !fact.LastHeartbeatAt.Equal(now) || !fact.LeaseExpiresAt.IsZero() || fact.UsageTokens != 18 {
		t.Fatalf("terminal queue accepted stale liveness = %+v", fact)
	}
}

func TestProductionResponsibilityRuntimeHealthTerminalEventSkipsInfrastructureDependencies(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 20, 0, 0, time.UTC)
	startedAt := now.Add(-2 * time.Minute)
	source := productionResponsibilityRuntimeHealthSource{
		events: staticProductionResponsibilityEventReader{events: []missionevent.Envelope{
			{
				TenantID: "tenant-a", MissionID: "mission-a", EventType: missionevent.EventAgentRunStarted, OccurredAt: startedAt,
				Payload: missionevent.RuntimeLifecyclePayload{
					TaskID: "task-a", RunID: "run-a", LastHeartbeatAt: startedAt, LeaseExpiresAt: now.Add(time.Minute),
				},
			},
			{
				TenantID: "tenant-a", MissionID: "mission-a", EventType: missionevent.EventAgentRunCompleted, OccurredAt: now,
				Payload: missionevent.RuntimeLifecyclePayload{
					TaskID: "task-a", RunID: "run-a", LastHeartbeatAt: startedAt,
					Usage: map[string]agentcore.TokenUsage{"model-a": {InputTokens: 15, OutputTokens: 10}},
				},
			},
		}},
		jobs:     staticProductionResponsibilityJobReader{err: errors.New("terminal event must not query queue")},
		liveness: staticProductionResponsibilityLiveness{err: errors.New("terminal event must not query liveness")},
	}

	fact, found, err := source.FindRuntimeHealth(context.Background(), "tenant-a", "mission-a", "task-a", "run-a")
	if err != nil || !found || !fact.Terminal {
		t.Fatalf("terminal event health=%+v found=%v err=%v", fact, found, err)
	}
	if !fact.LastHeartbeatAt.Equal(now) || !fact.LeaseExpiresAt.IsZero() || fact.UsageTokens != 25 {
		t.Fatalf("terminal event health = %+v", fact)
	}
}

func TestProductionResponsibilityRuntimeHealthPropagatesDependencyErrors(t *testing.T) {
	want := errors.New("runtime health dependency failed")
	job := productionResponsibilityRuntimeJob(t, "tenant-a", "mission-a", "task-a", "run-a", execution.StatusRunning)
	job.StartedAt = time.Now().UTC()
	tests := []struct {
		name   string
		source productionResponsibilityRuntimeHealthSource
	}{
		{name: "events", source: productionResponsibilityRuntimeHealthSource{
			events: staticProductionResponsibilityEventReader{err: want}, jobs: staticProductionResponsibilityJobReader{}, liveness: staticProductionResponsibilityLiveness{},
		}},
		{name: "queue", source: productionResponsibilityRuntimeHealthSource{
			events: staticProductionResponsibilityEventReader{}, jobs: staticProductionResponsibilityJobReader{err: want}, liveness: staticProductionResponsibilityLiveness{},
		}},
		{name: "liveness", source: productionResponsibilityRuntimeHealthSource{
			events: staticProductionResponsibilityEventReader{}, jobs: staticProductionResponsibilityJobReader{jobs: map[string]execution.JobStatus{"run-a": job}}, liveness: staticProductionResponsibilityLiveness{err: want},
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := test.source.FindRuntimeHealth(context.Background(), "tenant-a", "mission-a", "task-a", "run-a")
			if !errors.Is(err, want) {
				t.Fatalf("dependency error = %v", err)
			}
		})
	}
}

func TestProductionResponsibilityRuntimeHealthRequiresTenantBoundIdentity(t *testing.T) {
	now := time.Date(2026, 9, 3, 0, 20, 0, 0, time.UTC)
	t.Run("bare liveness is not identity", func(t *testing.T) {
		source := productionResponsibilityRuntimeHealthSource{
			events: staticProductionResponsibilityEventReader{}, jobs: staticProductionResponsibilityJobReader{},
			liveness: staticProductionResponsibilityLiveness{snapshots: map[string]execution.LivenessSnapshot{
				"agent-run:run-a": {RuntimeID: "agent-run:run-a", LastSeenAt: now, ExpiresAt: now.Add(time.Minute), Alive: true},
			}},
		}
		_, found, err := source.FindRuntimeHealth(context.Background(), "tenant-a", "mission-a", "task-a", "run-a")
		if err != nil || found {
			t.Fatalf("bare liveness found=%v err=%v", found, err)
		}
	})

	t.Run("queue payload mismatch fails closed", func(t *testing.T) {
		job := productionResponsibilityRuntimeJob(t, "tenant-b", "mission-a", "task-a", "run-a", execution.StatusRunning)
		job.StartedAt, job.LeaseUntil = now.Add(-time.Minute), now.Add(time.Minute)
		source := productionResponsibilityRuntimeHealthSource{
			events:   staticProductionResponsibilityEventReader{},
			jobs:     staticProductionResponsibilityJobReader{jobs: map[string]execution.JobStatus{"run-a": job}},
			liveness: staticProductionResponsibilityLiveness{},
		}
		_, _, err := source.FindRuntimeHealth(context.Background(), "tenant-a", "mission-a", "task-a", "run-a")
		if !errors.Is(err, responsibility.ErrConflict) {
			t.Fatalf("identity mismatch error = %v", err)
		}
	})
}

func TestProductionResponsibilityDispatchClassifiesInvalidMissionRequestAsRejected(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()

	const tenantID = "tenant-paused"
	const missionID = "mission-paused"
	const taskID = "task-paused"
	const agentID = "agent-paused"
	missionRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	at := env.Clock.Now().UTC()
	if err := missionRepo.SaveForTenant(ctx, tenantID, projection.ReadModel{
		Mission: projection.MissionView{MissionID: missionID, Title: "Paused", FinalGoal: "Remain paused", Status: projection.MissionStatusPaused, CreatedAt: at},
		Team: projection.TeamView{Members: []projection.TeamMemberView{{
			AgentID: agentID, AgentProfileID: agentID, AgentInstanceID: agentcore.AgentInstanceID(agentID + ":" + missionID), AgentKind: "ai", MissionRole: "worker", Status: projection.TeamMemberJoined, JoinedAt: at,
		}}},
		TaskBoard:  projection.TaskBoardView{Tasks: []projection.TaskCardView{{TaskID: taskID, Assignee: agentID, Goal: "Wait", Status: projection.TaskStatusAssigned, UpdatedAt: at}}},
		Checkpoint: projection.Checkpoint{MissionID: missionID, Version: 1, UpdatedAt: at}, UpdatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	authorizer := authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}})
	commands := missioncontrol.NewCommandService(nil)
	commands.SetAuthorizationService(authorizer)
	commands.SetRuntimeCommandPorts(productionAgentRunQueue{queue: env.ExecutionQueue}, missionRepo, missionevent.NewStoreSink(env.EventStore), env.IDGenerator, env.Clock)
	dispatcher := productionResponsibilityRuntimeDispatcher{commands: commands}
	_, err := dispatcher.DispatchResponsibility(ctx, responsibility.RuntimeDispatchRequest{
		TenantID: tenantID, MissionID: missionID, TaskID: taskID, AgentProfileID: agentID,
		RuntimeKind: string(agentcore.RuntimeKindOpenClaw), RunID: "run-paused", Attempt: 1, IdempotencyKey: "dispatch-paused",
		Context: agentcore.ResponsibilityContext{
			ContextID: "context-paused", ResponsibilityID: "responsibility-paused", ResponsibilityVersion: 1,
			PolicyID: "policy-paused", PolicyVersion: 1, AgentProfileID: agentID, MissionID: missionID, TaskID: taskID,
		},
	})
	if !errors.Is(err, responsibility.ErrDispatchRejected) {
		t.Fatalf("dispatch error = %v, want ErrDispatchRejected", err)
	}
}

func TestProductionResponsibilityDispatchClassifiesExistingActiveRunAsRejected(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()

	const tenantID = "tenant-active-run"
	const missionID = "mission-active-run"
	const taskID = "task-active-run"
	const agentID = "agent-active-run"
	missionRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	at := env.Clock.Now().UTC()
	if err := missionRepo.SaveForTenant(ctx, tenantID, projection.ReadModel{
		Mission: projection.MissionView{MissionID: missionID, Title: "Active", FinalGoal: "Avoid duplicate dispatch", Status: projection.MissionStatusActive, CreatedAt: at},
		Team: projection.TeamView{Members: []projection.TeamMemberView{{
			AgentID: agentID, AgentProfileID: agentID, AgentInstanceID: agentcore.AgentInstanceID(agentID + ":" + missionID), AgentKind: "ai", MissionRole: "worker", Status: projection.TeamMemberJoined, JoinedAt: at,
		}}},
		TaskBoard: projection.TaskBoardView{Tasks: []projection.TaskCardView{{
			TaskID: taskID, Assignee: agentID, Goal: "Run once", Status: projection.TaskStatusAssigned, UpdatedAt: at,
		}}},
		RuntimeSessions: []projection.RuntimeSessionView{{
			RunID: "run-existing", TaskID: taskID, AgentID: agentID, RuntimeKind: string(agentcore.RuntimeKindOpenClaw),
			Status: projection.RuntimeSessionRunning, QueuedAt: at.Add(-time.Minute), StartedAt: at.Add(-time.Minute), UpdatedAt: at,
		}},
		Checkpoint: projection.Checkpoint{MissionID: missionID, Version: 1, UpdatedAt: at}, UpdatedAt: at,
	}); err != nil {
		t.Fatal(err)
	}
	authorizer := authorization.NewService(fixedProductionAuthorizationAdapter{decision: authorization.Decision{Allowed: true, Source: authorization.DecisionSourceOpenFGA}})
	commands := missioncontrol.NewCommandService(nil)
	commands.SetAuthorizationService(authorizer)
	commands.SetRuntimeCommandPorts(productionAgentRunQueue{queue: env.ExecutionQueue}, missionRepo, missionevent.NewStoreSink(env.EventStore), env.IDGenerator, env.Clock)
	dispatcher := productionResponsibilityRuntimeDispatcher{commands: commands}
	_, err := dispatcher.DispatchResponsibility(ctx, responsibility.RuntimeDispatchRequest{
		TenantID: tenantID, MissionID: missionID, TaskID: taskID, AgentProfileID: agentID,
		RuntimeKind: string(agentcore.RuntimeKindOpenClaw), RunID: "run-duplicate", Attempt: 1, IdempotencyKey: "dispatch-duplicate",
		Context: agentcore.ResponsibilityContext{
			ContextID: "context-active", ResponsibilityID: "responsibility-active", ResponsibilityVersion: 1,
			PolicyID: "policy-active", PolicyVersion: 1, AgentProfileID: agentID, MissionID: missionID, TaskID: taskID,
		},
	})
	if !errors.Is(err, responsibility.ErrDispatchRejected) {
		t.Fatalf("dispatch error = %v, want rejected active-run classification", err)
	}
}

func TestProductionResponsibilityAssignmentImportsHistoricalWorkWithoutDispatch(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	const tenantID = "tenant-history"
	const missionID = "mission-history"
	const taskID = "task-history"
	const agentID = "agent-history"
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	service := responsibility.NewService(responsibility.ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Clock: env.Clock})
	assignmentPort := productionTaskResponsibilityAssignment{service: service}
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return assignmentPort.RecordHistoricalTaskAssignment(ctx, tx, missioncontrol.HistoricalTaskResponsibilityAssignmentSignal{
			TenantID: tenantID, MissionID: missionID, TaskID: taskID, AgentProfileID: agentID,
			SourceKind: "scheduler_agent_task", SourceID: "schedule-history", SourceStepID: "run-history",
			State: string(responsibility.AssignmentStateFulfilled), ActorID: "scheduler-history-backfill",
			Reason: "stored completed scheduled run", OccurredAt: env.Clock.Now(),
		})
	}); err != nil {
		t.Fatalf("record historical assignment: %v", err)
	}
	assignmentID := responsibility.AssignmentID(tenantID, responsibility.WorkKindMissionTask, taskID)
	assignment, err := repository.GetAssignment(ctx, assignmentID)
	if err != nil || assignment.State != responsibility.AssignmentStateFulfilled || assignment.SourceKind != "scheduler_agent_task" ||
		assignment.SourceID != "schedule-history" || assignment.SourceStepID != "run-history" {
		t.Fatalf("historical assignment = %+v err=%v", assignment, err)
	}
	ready, err := repository.ListReadyActivations(ctx, env.Clock.Now(), 10)
	if err != nil || len(ready) != 0 {
		t.Fatalf("historical ready activations = %+v err=%v", ready, err)
	}
	attempts, err := repository.ListAttempts(ctx, assignmentID)
	if err != nil || len(attempts) != 0 {
		t.Fatalf("historical attempts = %+v err=%v", attempts, err)
	}
}

func TestProductionResponsibilityLifecycleTracksRuntimeAndQAOutcomes(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	const tenantID = "tenant-lifecycle"
	const missionID = "mission-lifecycle"
	const taskID = "task-lifecycle"
	const agentID = "agent-lifecycle"
	tasks := missiontask.NewPlatformRepository()
	record := productionResponsibilityTask(taskID, missionID, agentID, missiontask.TaskStatusAssigned)
	saveProductionResponsibilityTask(t, env, tasks, record)
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	service := responsibility.NewService(responsibility.ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock, RetryDelays: []time.Duration{0}})
	assignmentPort := productionTaskResponsibilityAssignment{service: service}
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		return assignmentPort.RecordTaskAssignment(ctx, tx, missioncontrol.TaskResponsibilityAssignmentSignal{TenantID: tenantID, MissionID: missionID, TaskID: taskID, AgentProfileID: agentID})
	}); err != nil {
		t.Fatalf("record assignment: %v", err)
	}
	ready, err := repository.ListReadyActivations(ctx, env.Clock.Now(), 10)
	if err != nil || len(ready) != 1 {
		t.Fatalf("ready activations = %+v err=%v", ready, err)
	}
	claimed, ok, err := repository.ClaimActivation(ctx, ready[0].ID, "worker", "claim-lifecycle", env.Clock.Now(), time.Minute, 1)
	if err != nil || !ok {
		t.Fatalf("claim activation ok=%v err=%v", ok, err)
	}
	runID := responsibility.RunIDForActivation(claimed.ID)
	if _, err := repository.MarkDispatched(ctx, claimed.ID, "claim-lifecycle", runID, "managed", env.Clock.Now()); err != nil {
		t.Fatalf("mark dispatched: %v", err)
	}
	record.Status = missiontask.TaskStatusInReview
	saveProductionResponsibilityTask(t, env, tasks, record)
	lifecycle := productionResponsibilityRuntimeLifecycle{service: service}
	if err := lifecycle.CompletedWithTraceAndUsage(ctx, tenantID, taskID, runID, "trace-lifecycle", 25); err != nil {
		t.Fatalf("record runtime completion: %v", err)
	}
	attempt, err := repository.FindAttemptByRun(ctx, tenantID, runID)
	if err != nil || attempt.Outcome != responsibility.AttemptOutcomeCompleted || attempt.UsageTokens != 25 {
		t.Fatalf("completed attempt = %+v err=%v", attempt, err)
	}
	assignmentID := responsibility.AssignmentID(tenantID, responsibility.WorkKindMissionTask, taskID)
	assignment, err := repository.GetAssignment(ctx, assignmentID)
	if err != nil || assignment.State != responsibility.AssignmentStateSubmitted {
		t.Fatalf("assignment after runtime = %+v err=%v", assignment, err)
	}

	record.Status = missiontask.TaskStatusReworkRequested
	saveProductionResponsibilityTask(t, env, tasks, record)
	review := productionTaskResponsibilityReview{service: service}
	if err := review.ObserveTaskReview(ctx, missioncontrol.TaskResponsibilityReviewSignal{TenantID: tenantID, TaskID: taskID, Approved: false}); err != nil {
		t.Fatalf("record QA rejection: %v", err)
	}
	rework, err := repository.ListReadyActivations(ctx, env.Clock.Now(), 10)
	if err != nil || len(rework) != 1 || rework[0].Cause != responsibility.ActivationCauseReviewRejected {
		t.Fatalf("rework activations = %+v err=%v", rework, err)
	}
	record.Status = missiontask.TaskStatusApproved
	saveProductionResponsibilityTask(t, env, tasks, record)
	if err := review.ObserveTaskReview(ctx, missioncontrol.TaskResponsibilityReviewSignal{TenantID: tenantID, TaskID: taskID, Approved: true}); err != nil {
		t.Fatalf("record QA approval: %v", err)
	}
	assignment, err = repository.GetAssignment(ctx, assignmentID)
	if err != nil || assignment.State != responsibility.AssignmentStateFulfilled {
		t.Fatalf("assignment after QA approval = %+v err=%v", assignment, err)
	}
}

func TestProductionQAPlanCoordinatorCreatesRealTaskAndCompletesParentFromSubmittedArtifact(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	const tenantID = "tenant-qa-plan"
	const missionID = "mission-qa-plan"
	const parentTaskID = "task-worker"
	const workerAgentID = "worker-agent"
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	seedProductionChatProfile(t, ctx, profiles, defaultQAPlanAgentID, "质检计划", "QAPlan")
	qaProfile, err := profiles.GetProfile(ctx, defaultQAPlanAgentID)
	if err != nil {
		t.Fatalf("get QA plan profile: %v", err)
	}
	qaProfile.QAEnabled = false
	if err := profiles.SaveProfile(ctx, qaProfile); err != nil {
		t.Fatalf("save QA plan profile: %v", err)
	}

	tasks := missiontask.NewPlatformRepository()
	parent := productionResponsibilityTask(parentTaskID, missionID, workerAgentID, missiontask.TaskStatusAssigned)
	parent.Contract.QAEnabled = true
	parent.Contract.QAPlanRequired = true
	parent.Contract.QAPlanCompleted = false
	parent.Contract.AcceptanceCriteria = []missiontask.AcceptanceCriterion{{ID: "acceptance-1", Description: "The worker outcome is verifiable."}}
	saveProductionResponsibilityTask(t, env, tasks, parent)
	missionRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	at := env.Clock.Now().UTC()
	model := projection.ReadModel{
		Mission:    projection.MissionView{MissionID: missionID, Title: "QA planning", FinalGoal: "Complete worker task with a QA contract", Status: projection.MissionStatusActive, CreatedAt: at},
		TaskBoard:  projection.TaskBoardView{Tasks: []projection.TaskCardView{{TaskID: parentTaskID, Assignee: workerAgentID, Goal: parent.Contract.Goal, Status: projection.TaskStatusAssigned, UpdatedAt: at}}},
		Checkpoint: projection.Checkpoint{MissionID: missionID, Version: 1, UpdatedAt: at}, UpdatedAt: at,
	}
	if err := missionRepo.SaveForTenant(ctx, tenantID, model); err != nil {
		t.Fatalf("save QA mission projection: %v", err)
	}

	responsibilityRepo := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	responsibilityService := responsibility.NewService(responsibility.ServiceOptions{Repository: responsibilityRepo, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	auth := newProductionMissionAuthorizationTestFixture()
	lifecycle := missioncontrol.NewLifecycleService(env.UnitOfWork, env.DBRouter, missionRepo, missioncontrol.LifecycleOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator, TaskRepo: tasks, AgentDirectory: profiles,
		AuthorizationService: auth.service, AuthorizationRelations: auth.relations,
		TaskResponsibility: productionTaskResponsibilityAssignment{service: responsibilityService},
	})
	taskEvents := missionevent.NewTaskEventSink()
	qaService := missiontask.NewQAService(env.UnitOfWork, tasks, taskEvents, missiontask.QAServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})
	planner := productionQAPlanCoordinator{
		unit: env.UnitOfWork, tasks: tasks, lifecycle: lifecycle, qa: qaService,
		responsibilities: responsibilityRepo, relations: auth.relations,
	}
	parentAssignment := responsibility.AssignmentRecord{ResponsibilityAssignment: responsibility.ResponsibilityAssignment{
		ID: "responsibility-parent", TenantID: tenantID, WorkKind: responsibility.WorkKindMissionTask, WorkID: parentTaskID,
		AccountableAgent: workerAgentID, State: responsibility.AssignmentStateOffered, Version: 1,
	}, MissionID: missionID}
	state, err := planner.ReconcileQAPlan(ctx, parentAssignment, parent)
	if err != nil || state != responsibility.QAPlanStatePending {
		t.Fatalf("create QA plan task state=%s err=%v", state, err)
	}
	qaTaskID := missiontask.TaskID(responsibility.QAPlanTaskID(parentTaskID))
	child, err := planner.getTask(ctx, qaTaskID)
	if err != nil || child.Status != missiontask.TaskStatusAssigned || child.Contract.SourceKind != "qa_plan" || child.Contract.SourceID != parentTaskID {
		t.Fatalf("QA plan child task = %+v err=%v", child, err)
	}
	childAssignment, err := responsibilityRepo.GetAssignmentForWork(ctx, tenantID, responsibility.WorkKindMissionTask, qaTaskID.String())
	if err != nil || childAssignment.AccountableAgent != defaultQAPlanAgentID {
		t.Fatalf("QA plan responsibility = %+v err=%v", childAssignment, err)
	}

	delivery := missiontask.NewDeliveryService(env.UnitOfWork, tasks, taskEvents, missiontask.DeliveryServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator})
	qaAgent := agentcore.AgentInstanceRef{ID: defaultQAPlanAgentID}
	if err := delivery.AcceptTask(ctx, qaTaskID, qaAgent); err != nil {
		t.Fatalf("accept QA plan task: %v", err)
	}
	if err := delivery.StartTask(ctx, qaTaskID, qaAgent); err != nil {
		t.Fatalf("start QA plan task: %v", err)
	}
	_, err = delivery.SubmitAgentResult(ctx, qaTaskID, agentcore.AgentResult{
		ID: "result-qa-plan", MissionID: missionID, TaskID: missioncore.TaskID(qaTaskID), AgentID: defaultQAPlanAgentID, RunID: "run-qa-plan",
		Summary: agentcore.Summary{Text: "BDD and TDD contract ready."},
		Deliverables: []agentcore.Deliverable{{
			ID: "deliverable-qa-plan", ExpectedDeliverableID: "qa-plan-artifact", Title: "QA plan",
			ArtifactRef: agentcore.ArtifactRef{ID: "artifact-qa-plan", Kind: agentcore.ArtifactKindLog, URI: "agent-run://run-qa-plan/output"},
		}},
		Evidence: []agentcore.Evidence{
			{ID: "evidence-qa-plan-bdd", EvidenceRequirementID: "evidence-acceptance-1", AcceptanceCriterionID: "acceptance-1", Kind: agentcore.EvidenceKindArtifact, ArtifactRef: agentcore.ArtifactRef{ID: "artifact-qa-plan", Kind: agentcore.ArtifactKindLog, URI: "agent-run://run-qa-plan/output"}},
			{ID: "evidence-qa-plan-handoff", EvidenceRequirementID: "evidence-acceptance-2", AcceptanceCriterionID: "acceptance-2", Kind: agentcore.EvidenceKindArtifact, ArtifactRef: agentcore.ArtifactRef{ID: "artifact-qa-plan", Kind: agentcore.ArtifactKindLog, URI: "agent-run://run-qa-plan/output"}},
			{ID: "evidence-qa-plan-gates", EvidenceRequirementID: "evidence-acceptance-3", AcceptanceCriterionID: "acceptance-3", Kind: agentcore.EvidenceKindArtifact, ArtifactRef: agentcore.ArtifactRef{ID: "artifact-qa-plan", Kind: agentcore.ArtifactKindLog, URI: "agent-run://run-qa-plan/output"}},
		},
	}, missiontask.SubmitOptions{IdempotencyKey: "run-qa-plan"})
	if err != nil {
		t.Fatalf("submit QA plan task result: %v", err)
	}
	state, err = planner.ReconcileQAPlan(ctx, parentAssignment, parent)
	if err != nil || state != responsibility.QAPlanStateCompleted {
		t.Fatalf("complete parent QA plan state=%s err=%v", state, err)
	}
	storedParent, err := planner.getTask(ctx, parent.Contract.ID)
	if err != nil || !storedParent.Contract.QAPlanCompleted || storedParent.Contract.QAPlanArtifactRef.ID != "artifact-qa-plan" || storedParent.Contract.QAPlanRef != responsibility.QAPlanID(parentTaskID) {
		t.Fatalf("completed parent QA contract = %+v err=%v", storedParent.Contract, err)
	}
}

func productionResponsibilityTask(taskID, missionID, agentID string, status missiontask.TaskStatus) missiontask.TaskRecord {
	return missiontask.TaskRecord{
		Contract: missiontask.TaskContract{
			ID: missiontask.TaskID(taskID), MissionID: missioncore.MissionID(missionID), Assignee: missiontask.TaskAssigneeRef{AgentProfileRef: &agentcore.AgentProfileRef{ID: agentcore.AgentProfileID(agentID)}},
			Title: taskID, Goal: "Deliver the assigned outcome", WorkerPersonalGoal: "Produce auditable evidence",
			ExpectedDeliverables: []missiontask.ExpectedDeliverable{{ID: "deliverable-1", Title: "Outcome"}}, QAPlanCompleted: true,
		},
		Status: status, Version: 1, StatusChangedAt: time.Date(2026, 7, 25, 0, 0, 0, 0, time.UTC),
	}
}

func saveProductionResponsibilityTask(t *testing.T, env *testkit.Env, repository missiontask.Repository, record missiontask.TaskRecord) {
	t.Helper()
	if err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
		return repository.SaveTask(ctx, tx, record)
	}); err != nil {
		t.Fatalf("save task %s: %v", record.Contract.ID, err)
	}
}

func productionResponsibilityRuntimeJob(t *testing.T, tenantID, missionID, taskID, runID string, status execution.Status) execution.JobStatus {
	t.Helper()
	payload, err := json.Marshal(runtimedaemon.AgentRun{
		RunID: agentcore.AgentRunID(runID), MissionID: missionID, TaskID: taskID,
		Request: agentcore.RuntimeRequest{TenantID: tenantID},
	})
	if err != nil {
		t.Fatalf("marshal responsibility runtime job: %v", err)
	}
	return execution.JobStatus{ID: runID, Topic: runtimedaemon.AgentRunTopic, Payload: payload, Status: status}
}
