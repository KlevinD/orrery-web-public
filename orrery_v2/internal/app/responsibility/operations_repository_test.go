package responsibility

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/logging"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestPolicySaveUsesCASAndPayloadBoundIdempotency(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	policy := DefaultPolicy("tenant-1", "agent-1", WorkKindMissionTask)

	first, err := repository.SavePolicy(context.Background(), policy, "manager-1", "policy-save-1", 1, env.Clock.Now())
	if err != nil || first.Version != 2 {
		t.Fatalf("first policy save = %+v err=%v", first, err)
	}
	replayed, err := repository.SavePolicy(context.Background(), policy, "manager-1", "policy-save-1", 1, env.Clock.Now())
	if err != nil || replayed.Version != first.Version || replayed.MaxParallel != first.MaxParallel {
		t.Fatalf("policy replay = %+v err=%v", replayed, err)
	}
	changed := policy
	changed.MaxParallel++
	if _, err := repository.SavePolicy(context.Background(), changed, "manager-1", "policy-save-1", 1, env.Clock.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("same idempotency key with changed payload error = %v, want conflict", err)
	}

	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	for index := 0; index < 2; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			<-start
			next := first
			next.MaxParallel = 4 + index
			_, saveErr := repository.SavePolicy(context.Background(), next, "manager-1", "policy-cas-"+string(rune('a'+index)), first.Version, env.Clock.Now())
			results <- saveErr
		}(index)
	}
	close(start)
	wait.Wait()
	close(results)
	succeeded, conflicted := 0, 0
	for result := range results {
		switch {
		case result == nil:
			succeeded++
		case errors.Is(result, ErrConflict):
			conflicted++
		default:
			t.Fatalf("concurrent policy save error = %v", result)
		}
	}
	if succeeded != 1 || conflicted != 1 {
		t.Fatalf("concurrent policy saves succeeded=%d conflicted=%d", succeeded, conflicted)
	}
}

func TestResolveMissingPolicyUsesDefaultWithoutRollbackLog(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	var logs bytes.Buffer
	env.UnitOfWork.WithLogger(logging.NewJSONLogger(&logs, logging.LevelDebug))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	assignment := AssignmentRecord{ResponsibilityAssignment: ResponsibilityAssignment{
		TenantID: "tenant-1", AccountableAgent: "agent-1", WorkKind: WorkKindMissionTask,
	}}

	policy, err := repository.ResolvePolicy(context.Background(), assignment)
	if err != nil {
		t.Fatal(err)
	}
	want := DefaultPolicy(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind)
	if policy.ID != want.ID || policy.Version != want.Version {
		t.Fatalf("resolved policy = %+v, want default %+v", policy, want)
	}
	if _, err := repository.GetPolicy(context.Background(), assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetPolicy error = %v, want ErrNotFound", err)
	}
	if strings.Contains(logs.String(), `"error_code":"transaction_rollback"`) {
		t.Fatalf("missing policy emitted rollback error: %s", logs.String())
	}
}

func TestBlockerResolutionIsCASIdempotentAndWaitsForEveryBlocker(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-blockers", "agent-1")
	claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim-blockers", env.Clock.Now(), time.Minute, 2)
	if err != nil || !ok {
		t.Fatalf("claim activation ok=%v err=%v", ok, err)
	}
	if _, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim-blockers", RunIDForActivation(claimed.ID), "codex", env.Clock.Now()); err != nil {
		t.Fatalf("mark dispatched: %v", err)
	}
	assignment, err := repository.GetAssignmentForWork(context.Background(), "tenant-1", WorkKindMissionTask, "task-blockers")
	if err != nil {
		t.Fatal(err)
	}
	first, err := repository.OpenBlocker(context.Background(), OpenBlockerInput{TenantID: assignment.TenantID, ResponsibilityID: assignment.ID,
		Kind: BlockerKindInput, Needed: "customer input", RequestedFrom: "user:owner", RecheckAt: env.Clock.Now().Add(time.Hour), DedupeKey: "input"}, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.OpenBlocker(context.Background(), OpenBlockerInput{TenantID: assignment.TenantID, ResponsibilityID: assignment.ID,
		Kind: BlockerKindApproval, Needed: "approval", RequestedFrom: "user:reviewer", RecheckAt: env.Clock.Now().Add(time.Hour), DedupeKey: "approval"}, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}

	resolvedFirst, firstActivation, err := repository.ResolveBlocker(context.Background(), assignment.TenantID, first.ID, "manager-1", "input received", "resolve-first", first.Version, ActivationCauseBlockerResolved, env.Clock.Now())
	if err != nil || resolvedFirst.Status != BlockerStatusResolved || firstActivation.ID != "" {
		t.Fatalf("first resolution blocker=%+v activation=%+v err=%v", resolvedFirst, firstActivation, err)
	}
	replayed, replayActivation, err := repository.ResolveBlocker(context.Background(), assignment.TenantID, first.ID, "manager-1", "input received", "resolve-first", first.Version, ActivationCauseBlockerResolved, env.Clock.Now())
	if err != nil || replayed.Version != resolvedFirst.Version || replayActivation.ID != "" {
		t.Fatalf("replayed resolution blocker=%+v activation=%+v err=%v", replayed, replayActivation, err)
	}
	if _, _, err := repository.ResolveBlocker(context.Background(), assignment.TenantID, first.ID, "manager-1", "different", "resolve-first", first.Version, ActivationCauseBlockerResolved, env.Clock.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed resolution replay error = %v, want conflict", err)
	}
	if _, _, err := repository.ResolveBlocker(context.Background(), assignment.TenantID, second.ID, "manager-1", "approved", "resolve-stale", second.Version+1, ActivationCauseBlockerResolved, env.Clock.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale blocker version error = %v, want conflict", err)
	}
	resolvedSecond, resumed, err := repository.ResolveBlocker(context.Background(), assignment.TenantID, second.ID, "manager-1", "approved", "resolve-second", second.Version, ActivationCauseBlockerResolved, env.Clock.Now())
	if err != nil || resolvedSecond.Status != BlockerStatusResolved || resumed.ID == "" {
		t.Fatalf("last resolution blocker=%+v activation=%+v err=%v", resolvedSecond, resumed, err)
	}
	current, err := repository.GetAssignment(context.Background(), assignment.ID)
	if err != nil || current.State != AssignmentStateActive {
		t.Fatalf("assignment after all blockers = %+v err=%v", current, err)
	}
}

func TestBlockerReopenPreservesHistoryAndAdvancesVersion(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-reopen-blocker", "agent-1")
	assignment, err := repository.GetAssignment(context.Background(), activation.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := repository.OpenBlocker(context.Background(), OpenBlockerInput{
		TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, Kind: BlockerKindInput,
		Needed: "customer input", RequestedFrom: "user:owner", RecheckAt: env.Clock.Now().Add(time.Hour), DedupeKey: "customer-input",
	}, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	resolved, _, err := repository.ResolveBlocker(context.Background(), assignment.TenantID, opened.ID, "owner", "received", "resolve-reopen", opened.Version, ActivationCauseBlockerResolved, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	env.Clock.Advance(time.Minute)
	reopened, err := repository.OpenBlocker(context.Background(), OpenBlockerInput{
		TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, Kind: BlockerKindInput,
		Needed: "customer input", RequestedFrom: "user:owner", RecheckAt: env.Clock.Now().Add(time.Hour), DedupeKey: "customer-input",
	}, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if reopened.ID != opened.ID || reopened.DedupeKey != opened.DedupeKey || !reopened.CreatedAt.Equal(opened.CreatedAt) || reopened.Version != resolved.Version+1 || reopened.Status != BlockerStatusOpen {
		t.Fatalf("opened=%+v resolved=%+v reopened=%+v", opened, resolved, reopened)
	}
}

func TestConcurrentBlockerResolutionAndEscalationDoNotLoseUpdates(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-blocker-race", "agent-1")
	claimed, ok, err := repository.ClaimActivation(ctx, activation.ID, "worker", "claim-blocker-race", env.Clock.Now(), time.Minute, 2)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if _, err := repository.MarkDispatched(ctx, claimed.ID, "claim-blocker-race", RunIDForActivation(claimed.ID), "codex", env.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	assignment, err := repository.GetAssignment(ctx, activation.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := repository.OpenBlocker(ctx, OpenBlockerInput{
		TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, Kind: BlockerKindApproval,
		Needed: "manager approval", RequestedFrom: "user:manager", RecheckAt: env.Clock.Now(), DedupeKey: "approval-race",
	}, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultPolicy(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind)
	policy.EscalationChain = []EscalationTarget{{SubjectKind: "user", SubjectID: "director", After: time.Minute}}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		_, _, resolveErr := repository.ResolveBlocker(ctx, assignment.TenantID, blocker.ID, "manager", "approved", "resolve-race", blocker.Version, ActivationCauseBlockerResolved, env.Clock.Now())
		results <- resolveErr
	}()
	go func() {
		defer wait.Done()
		<-start
		_, _, escalateErr := repository.EscalateBlocker(ctx, blocker.ID, policy, env.Clock.Now())
		results <- escalateErr
	}()
	close(start)
	wait.Wait()
	close(results)
	for result := range results {
		if result != nil && !errors.Is(result, ErrConflict) {
			t.Fatalf("concurrent blocker mutation error = %v", result)
		}
	}
	stored, err := repository.GetBlocker(ctx, assignment.TenantID, blocker.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, err := repository.GetAssignment(ctx, assignment.ID)
	if err != nil {
		t.Fatal(err)
	}
	switch stored.Status {
	case BlockerStatusResolved:
		if stored.Version != blocker.Version+1 || current.State != AssignmentStateActive {
			t.Fatalf("resolved race blocker=%+v assignment=%+v", stored, current)
		}
	case BlockerStatusOpen:
		if stored.Version != blocker.Version+1 || stored.EscalationLevel != 1 || current.State != AssignmentStateWaiting {
			t.Fatalf("escalated race blocker=%+v assignment=%+v", stored, current)
		}
	default:
		t.Fatalf("unexpected blocker after race = %+v", stored)
	}
}

func TestConcurrentHeartbeatCannotOverwriteTerminalRuntimeOutcome(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-heartbeat-race", "agent-1")
	claimed, ok, err := repository.ClaimActivation(ctx, activation.ID, "worker", "claim-heartbeat-race", env.Clock.Now(), time.Minute, 2)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	runID := RunIDForActivation(claimed.ID)
	if _, err := repository.MarkDispatched(ctx, claimed.ID, "claim-heartbeat-race", runID, "codex", env.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var wait sync.WaitGroup
	wait.Add(2)
	go func() {
		defer wait.Done()
		<-start
		_, heartbeatErr := repository.UpdateAttemptHeartbeat(ctx, "tenant-1", runID, env.Clock.Now().Add(time.Minute), 25, env.Clock.Now().Add(time.Minute))
		results <- heartbeatErr
	}()
	go func() {
		defer wait.Done()
		<-start
		_, _, outcomeErr := repository.PersistRuntimeOutcome(ctx, RuntimeOutcomeRecord{
			TenantID: "tenant-1", TaskID: "task-heartbeat-race", RunID: runID, Outcome: AttemptOutcomeCompleted,
		}, env.Clock.Now().Add(time.Minute))
		results <- outcomeErr
	}()
	close(start)
	wait.Wait()
	close(results)
	for result := range results {
		if result != nil {
			t.Fatal(result)
		}
	}
	attempt, err := repository.FindAttemptByRun(ctx, "tenant-1", runID)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Outcome != AttemptOutcomeCompleted || attempt.CompletedAt.IsZero() {
		t.Fatalf("terminal attempt was overwritten = %+v", attempt)
	}
	updated, err := repository.UpdateAttemptHeartbeat(ctx, "tenant-1", runID, env.Clock.Now().Add(2*time.Minute), 50, env.Clock.Now().Add(2*time.Minute))
	if err != nil || updated.Outcome != AttemptOutcomeCompleted || updated.UsageTokens != attempt.UsageTokens {
		t.Fatalf("post-terminal heartbeat updated attempt=%+v err=%v", updated, err)
	}
}

func TestAcceptTimeoutBlockerEscalatesInsteadOfAutoResolving(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-accept-timeout", "agent-1")
	env.Clock.Advance(2 * time.Minute)
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-accept-timeout", "mission-1", "agent-1", missiontask.TaskStatusAssigned))
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: &capturingResponsibilityDispatcher{}, Clock: env.Clock,
	})
	if _, err := coordinator.Reconcile(ctx, 10); err != nil {
		t.Fatal(err)
	}
	blockers, err := repository.ListBlockers(ctx, "tenant-1", activation.ResponsibilityID, true)
	if err != nil || len(blockers) != 1 || blockers[0].DedupeKey != "accept-timeout" {
		t.Fatalf("accept-timeout blockers=%+v err=%v", blockers, err)
	}
	env.Clock.Advance(16 * time.Minute)
	stats, err := coordinator.Reconcile(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := repository.GetBlocker(ctx, "tenant-1", blockers[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := repository.GetAssignment(ctx, activation.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != BlockerStatusOpen || stored.EscalationLevel != 1 || assignment.State != AssignmentStateEscalated || stats.BlockersEscalated != 1 || stats.BlockersResolved != 0 {
		t.Fatalf("stats=%+v blocker=%+v assignment=%+v", stats, stored, assignment)
	}
}

func TestExternalSignalResolvesMatchingBlockersAtomicallyAndStaysTenantScoped(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	first := assignResponsibilityForTenant(t, env, repository, "tenant-1", "mission-1", "task-signal-1", "agent-1")
	second := assignResponsibilityForTenant(t, env, repository, "tenant-1", "mission-1", "task-signal-2", "agent-1")
	other := assignResponsibilityForTenant(t, env, repository, "tenant-2", "mission-2", "task-signal-other", "agent-2")
	for _, activation := range []ActivationRecord{first, second, other} {
		assignment, err := repository.GetAssignment(context.Background(), activation.ResponsibilityID)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := repository.OpenBlocker(context.Background(), OpenBlockerInput{TenantID: assignment.TenantID, ResponsibilityID: assignment.ID,
			Kind: BlockerKindExternalSystem, Needed: "provider recovery", RequestedFrom: "provider:scm", RecheckAt: env.Clock.Now().Add(time.Hour), DedupeKey: "provider"}, env.Clock.Now()); err != nil {
			t.Fatal(err)
		}
	}
	signal := ExternalSignal{TenantID: "tenant-1", ActorID: "provider-monitor", IdempotencyKey: "signal-1", BlockerKind: BlockerKindExternalSystem,
		RequestedFrom: "provider:scm", SourceRef: "provider-event-1", Summary: "provider recovered"}
	resolved, err := repository.ResolveBlockersForExternalSignal(context.Background(), signal, env.Clock.Now())
	if err != nil || resolved != 2 {
		t.Fatalf("external signal resolved=%d err=%v", resolved, err)
	}
	replayed, err := repository.ResolveBlockersForExternalSignal(context.Background(), signal, env.Clock.Now())
	if err != nil || replayed != 2 {
		t.Fatalf("external signal replay resolved=%d err=%v", replayed, err)
	}
	changed := signal
	changed.Summary = "different payload"
	if _, err := repository.ResolveBlockersForExternalSignal(context.Background(), changed, env.Clock.Now()); !errors.Is(err, ErrConflict) {
		t.Fatalf("external signal changed replay error = %v, want conflict", err)
	}
	otherBlockers, err := repository.ListBlockers(context.Background(), "tenant-2", other.ResponsibilityID, true)
	if err != nil || len(otherBlockers) != 1 {
		t.Fatalf("other tenant blockers = %+v err=%v", otherBlockers, err)
	}
}

func TestExternalSignalCannotResolveApprovalBlocker(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-external-approval", "agent-1")
	assignment, err := repository.GetAssignment(context.Background(), activation.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := repository.OpenBlocker(context.Background(), OpenBlockerInput{
		TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, Kind: BlockerKindApproval,
		Needed: "approve execution", RequestedFrom: "mission_owner:mission-1", RecheckAt: env.Clock.Now().Add(time.Minute), DedupeKey: "approval",
	}, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := repository.ResolveBlockersForExternalSignal(context.Background(), ExternalSignal{
		TenantID: assignment.TenantID, ActorID: "provider-monitor", IdempotencyKey: "approval-signal",
		ResponsibilityID: assignment.ID, BlockerKind: BlockerKindApproval, SourceRef: "provider:event-1", Summary: "provider is healthy",
	}, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if resolved != 0 {
		t.Fatalf("resolved approval blockers = %d, want 0", resolved)
	}
	stored, err := repository.GetBlocker(context.Background(), assignment.TenantID, blocker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != BlockerStatusOpen {
		t.Fatalf("approval blocker status = %s, want open", stored.Status)
	}
}

func TestTerminateAssignmentResolvesOpenBlockersIncludingLegacyTerminalRecords(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-terminate-blockers", "agent-1")
	assignment, err := repository.GetAssignment(ctx, activation.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := repository.OpenBlocker(ctx, OpenBlockerInput{
		TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, Kind: BlockerKindCapacity,
		Needed: "capacity unavailable", RequestedFrom: "agent:agent-1", RecheckAt: env.Clock.Now().Add(time.Minute), DedupeKey: "capacity",
	}, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	terminated, err := repository.TerminateAssignment(ctx, AssignmentControl{
		TenantID: assignment.TenantID, AssignmentID: assignment.ID, ActorID: "manager-1", Reason: "work cancelled",
		ExpectedVersion: assignment.Version, IdempotencyKey: "terminate-with-blocker",
	}, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	assertResolved := func() {
		t.Helper()
		stored, err := repository.GetBlocker(ctx, assignment.TenantID, blocker.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.Status != BlockerStatusResolved || stored.ResolvedBy != "manager-1" || stored.ResolvedAt.IsZero() {
			t.Fatalf("terminated assignment blocker = %+v", stored)
		}
	}
	assertResolved()

	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		record, ok := tx.Get(blockerTable, blocker.ID)
		if !ok {
			return ErrNotFound
		}
		var legacy BlockerRecord
		if err := decodeRecord(record, &legacy); err != nil {
			return err
		}
		legacy.Status = BlockerStatusOpen
		legacy.Resolution = ""
		legacy.ResolvedBy = ""
		legacy.ResolvedAt = time.Time{}
		legacy.Version++
		legacy.UpdatedAt = env.Clock.Now()
		return putRecordTx(ctx, tx, blockerTable, legacy.ID, legacy)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.TerminateAssignment(ctx, AssignmentControl{
		TenantID: assignment.TenantID, AssignmentID: assignment.ID, ActorID: "manager-1", Reason: "repair legacy terminal blocker",
		ExpectedVersion: terminated.Version, IdempotencyKey: "terminate-legacy-blocker",
	}, env.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	assertResolved()
}

func TestControlCASRejectsStaleCommandsBeforeRuntimeCancellation(t *testing.T) {
	for _, action := range []string{"pause", "terminate", "reassign"} {
		t.Run(action, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			tasks := missiontask.NewPlatformRepository()
			saveTaskRecord(t, env, tasks, testTaskRecord("task-control", "mission-1", "agent-1", missiontask.TaskStatusInProgress))
			repository := NewRepository(env.DBRouter, env.UnitOfWork)
			activation := assignTestResponsibility(t, env, repository, "task-control", "agent-1")
			claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim-control", env.Clock.Now(), time.Minute, 2)
			if err != nil || !ok {
				t.Fatalf("claim ok=%v err=%v", ok, err)
			}
			if _, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim-control", RunIDForActivation(claimed.ID), "codex", env.Clock.Now()); err != nil {
				t.Fatal(err)
			}
			assignment, err := repository.GetAssignment(context.Background(), activation.ResponsibilityID)
			if err != nil {
				t.Fatal(err)
			}
			controller := &recordingRuntimeController{}
			reassignment := &recordingTaskReassignment{err: ErrConflict}
			service := NewOperationsService(OperationsServiceOptions{Repository: repository,
				Service:        NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock}),
				Profiles:       staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{"agent-2": {ID: "agent-2", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}}}},
				RuntimeControl: controller, TaskReassignment: reassignment, Clock: env.Clock})
			command := AssignmentControl{TenantID: assignment.TenantID, AssignmentID: assignment.ID, ActorID: "manager-1", Reason: "control",
				ExpectedVersion: assignment.Version + 1, IdempotencyKey: "stale-" + action, AgentProfileID: "agent-2"}
			switch action {
			case "pause":
				_, err = service.Pause(context.Background(), command)
			case "terminate":
				_, err = service.Terminate(context.Background(), command)
			case "reassign":
				_, err = service.Reassign(context.Background(), command)
			}
			if !errors.Is(err, ErrConflict) || controller.calls != 0 {
				t.Fatalf("stale %s err=%v cancellation calls=%d", action, err, controller.calls)
			}
			if action == "reassign" && reassignment.calls != 1 {
				t.Fatalf("stale reassign authoritative calls=%d, want 1", reassignment.calls)
			}
		})
	}
}

func TestOperationsReassignUsesAuthoritativeTaskPortBeforeCancellingOldAttempt(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-reassign", "mission-1", "agent-1", missiontask.TaskStatusInProgress))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-reassign", "agent-1")
	claimed, ok, err := repository.ClaimActivation(ctx, activation.ID, "worker", "claim-reassign", env.Clock.Now(), time.Minute, 2)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	if _, err := repository.MarkDispatched(ctx, claimed.ID, "claim-reassign", RunIDForActivation(claimed.ID), "codex", env.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	assignment, err := repository.GetAssignment(ctx, activation.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	reassignment := &recordingTaskReassignment{onCall: func(request TaskReassignmentRequest) error {
		return env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
			_, _, err := repository.AssignMissionTaskTx(ctx, tx, MissionTaskAssignmentInput{
				TenantID: request.TenantID, MissionID: request.MissionID, TaskID: request.TaskID,
				AgentProfileID: request.AgentProfileID, ActorID: request.ActorID, Reason: request.Reason,
				ExpectedAssignmentVersion: request.ExpectedVersion, ActivationCause: ActivationCauseAssigned,
			}, env.Clock.Now())
			return err
		})
	}}
	controller := &recordingRuntimeController{}
	operations := NewOperationsService(OperationsServiceOptions{
		Repository: repository, Service: NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock}),
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-2": {ID: "agent-2", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		RuntimeControl: controller, TaskReassignment: reassignment, Clock: env.Clock,
	})
	detail, err := operations.Reassign(ctx, AssignmentControl{
		TenantID: assignment.TenantID, AssignmentID: assignment.ID, ActorID: "manager-1", AgentProfileID: "agent-2",
		Reason: "rebalance capacity", ExpectedVersion: assignment.Version, IdempotencyKey: "reassign-agent-2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if reassignment.calls != 1 || controller.calls != 1 || reassignment.request.ActorID != "manager-1" || reassignment.request.Reason != "rebalance capacity" || reassignment.request.ExpectedVersion != assignment.Version {
		t.Fatalf("reassignment=%+v calls=%d cancellation calls=%d", reassignment.request, reassignment.calls, controller.calls)
	}
	if detail.AccountableAgent != "agent-2" || detail.State != AssignmentStateOffered || detail.Version != assignment.Version+1 {
		t.Fatalf("reassigned detail = %+v", detail)
	}
	activations, err := repository.ListActivations(ctx, assignment.TenantID, assignment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(activations) < 2 || activations[0].Cause != ActivationCauseReassigned {
		t.Fatalf("reassignment activations = %+v", activations)
	}
}

func TestCoordinatorRecoversPersistedPauseAfterCancellationFailure(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-pause-recovery", "mission-1", "agent-1", missiontask.TaskStatusInProgress))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-pause-recovery", "agent-1")
	claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim-pause", env.Clock.Now(), time.Minute, 2)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim-pause", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := repository.GetAssignment(context.Background(), activation.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	domainService := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	failing := &recordingRuntimeController{err: errors.New("runtime unavailable")}
	operations := NewOperationsService(OperationsServiceOptions{Repository: repository, Service: domainService, RuntimeControl: failing, Clock: env.Clock})
	_, err = operations.Pause(context.Background(), AssignmentControl{TenantID: assignment.TenantID, AssignmentID: assignment.ID, ActorID: "manager-1",
		Reason: "maintenance", ExpectedVersion: assignment.Version, IdempotencyKey: "pause-recovery"})
	if err == nil {
		t.Fatal("pause should report runtime cancellation failure")
	}
	persisted, err := repository.GetAssignment(context.Background(), assignment.ID)
	if err != nil || !persisted.Paused {
		t.Fatalf("persisted pause = %+v err=%v", persisted, err)
	}

	recoveredController := &recordingRuntimeController{}
	coordinator := NewCoordinator(CoordinatorOptions{Repository: repository, Service: domainService,
		Profiles: staticResponsibilityProfiles{}, Dispatcher: &capturingResponsibilityDispatcher{}, RuntimeControl: recoveredController})
	count, err := coordinator.reconcileControlCancellations(context.Background(), 10)
	if err != nil || count != 1 || recoveredController.calls != 1 {
		t.Fatalf("recovered cancellations=%d calls=%d err=%v", count, recoveredController.calls, err)
	}
	storedAttempt, err := repository.FindAttemptByRun(context.Background(), assignment.TenantID, attempt.AgentRunID)
	if err != nil || storedAttempt.Outcome != AttemptOutcomeCancelled {
		t.Fatalf("stored attempt after recovery = %+v err=%v", storedAttempt, err)
	}
}

func TestDeadlineSupervisionPersistsWarningEscalatesOverdueAndResolvesAtTerminal(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	var assignment AssignmentRecord
	if err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
		var err error
		assignment, _, err = repository.AssignMissionTaskTx(ctx, tx, MissionTaskAssignmentInput{TenantID: "tenant-1", MissionID: "mission-1",
			TaskID: "task-deadline", AgentProfileID: "agent-1", Priority: 50, AcceptBy: env.Clock.Now().Add(time.Minute),
			DueAt: env.Clock.Now().Add(20 * time.Minute), ActivationCause: ActivationCauseAssigned}, env.Clock.Now())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	policy := DefaultPolicy(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind)
	policy.EscalationChain = []EscalationTarget{{SubjectKind: "user", SubjectID: "manager-1", After: time.Minute}}
	coordinator := NewCoordinator(CoordinatorOptions{Repository: repository, Clock: env.Clock})

	opened, err := coordinator.ensureDeadlineSupervision(context.Background(), assignment, policy)
	if err != nil || opened != 1 {
		t.Fatalf("deadline warning opened=%d err=%v", opened, err)
	}
	opened, err = coordinator.ensureDeadlineSupervision(context.Background(), assignment, policy)
	if err != nil || opened != 0 {
		t.Fatalf("deadline warning replay opened=%d err=%v", opened, err)
	}
	env.Clock.Advance(21 * time.Minute)
	assignment, err = repository.GetAssignment(context.Background(), assignment.ID)
	if err != nil {
		t.Fatal(err)
	}
	opened, err = coordinator.ensureDeadlineSupervision(context.Background(), assignment, policy)
	if err != nil || opened != 1 {
		t.Fatalf("overdue alert opened=%d err=%v", opened, err)
	}
	alerts, err := repository.ListSupervisionAlerts(context.Background(), assignment.TenantID, assignment.ID, false)
	if err != nil || len(alerts) != 2 || alerts[0].Kind != SupervisionAlertOverdue || alerts[0].EscalationLevel != 1 || alerts[0].RequestedFrom != "user:manager-1" || alerts[1].Status != SupervisionAlertResolved {
		t.Fatalf("alerts after overdue = %+v err=%v", alerts, err)
	}
	env.Clock.Advance(time.Minute)
	stats, err := coordinator.reconcileSupervisionAlerts(context.Background(), 10)
	if err != nil || stats.AlertsEscalated != 1 {
		t.Fatalf("overdue escalation stats=%+v err=%v", stats, err)
	}
	alerts, err = repository.ListSupervisionAlerts(context.Background(), assignment.TenantID, assignment.ID, true)
	if err != nil || len(alerts) != 1 || alerts[0].RequestedFrom != "mission_owner:mission-1" || alerts[0].NextEscalationAt.IsZero() == false {
		t.Fatalf("escalated alert = %+v err=%v", alerts, err)
	}
	assignment, err = repository.TransitionAssignment(context.Background(), assignment.ID, AssignmentStateTerminated, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	stats, err = coordinator.reconcileSupervisionAlerts(context.Background(), 10)
	if err != nil || stats.AlertsResolved != 1 {
		t.Fatalf("terminal alert resolution stats=%+v err=%v", stats, err)
	}
	alerts, err = repository.ListSupervisionAlerts(context.Background(), assignment.TenantID, assignment.ID, true)
	if err != nil || len(alerts) != 0 {
		t.Fatalf("open alerts after terminal = %+v err=%v", alerts, err)
	}
}

func TestRuntimeSupervisionTimesOutWithoutHeartbeatAndUsesCumulativeTokenBudget(t *testing.T) {
	t.Run("missing heartbeat", func(t *testing.T) {
		env := testkit.New(t)
		defer env.Close()
		tasks := missiontask.NewPlatformRepository()
		saveTaskRecord(t, env, tasks, testTaskRecord("task-heartbeat", "mission-1", "agent-1", missiontask.TaskStatusInProgress))
		repository := NewRepository(env.DBRouter, env.UnitOfWork)
		activation := assignTestResponsibility(t, env, repository, "task-heartbeat", "agent-1")
		claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim-heartbeat", env.Clock.Now(), time.Minute, 2)
		if err != nil || !ok {
			t.Fatalf("claim ok=%v err=%v", ok, err)
		}
		attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim-heartbeat", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
		if err != nil {
			t.Fatal(err)
		}
		env.Clock.Advance(2 * time.Minute)
		service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock, RetryDelays: []time.Duration{time.Minute}})
		controller := &recordingRuntimeController{}
		coordinator := NewCoordinator(CoordinatorOptions{Repository: repository, Service: service, RuntimeHealth: staticRuntimeHealthSource{}, RuntimeControl: controller, Clock: env.Clock})
		assignment, _ := repository.GetAssignment(context.Background(), activation.ResponsibilityID)
		stats, err := coordinator.reconcileRuntimeHealth(context.Background(), assignment, DefaultPolicy(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind))
		if err != nil || stats.TimedOut != 1 || controller.calls != 1 {
			t.Fatalf("missing heartbeat stats=%+v calls=%d err=%v", stats, controller.calls, err)
		}
		stored, err := repository.FindAttemptByRun(context.Background(), assignment.TenantID, attempt.AgentRunID)
		if err != nil || stored.Outcome != AttemptOutcomeTimedOut {
			t.Fatalf("timed out attempt = %+v err=%v", stored, err)
		}
	})

	t.Run("active lease defers a stale heartbeat timeout", func(t *testing.T) {
		env := testkit.New(t)
		defer env.Close()
		tasks := missiontask.NewPlatformRepository()
		saveTaskRecord(t, env, tasks, testTaskRecord("task-active-lease", "mission-1", "agent-1", missiontask.TaskStatusInProgress))
		repository := NewRepository(env.DBRouter, env.UnitOfWork)
		activation := assignTestResponsibility(t, env, repository, "task-active-lease", "agent-1")
		claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim-active-lease", env.Clock.Now(), time.Minute, 2)
		if err != nil || !ok {
			t.Fatalf("claim ok=%v err=%v", ok, err)
		}
		attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim-active-lease", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
		if err != nil {
			t.Fatal(err)
		}
		env.Clock.Advance(2 * time.Minute)
		service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
		controller := &recordingRuntimeController{}
		health := staticRuntimeHealthSource{fact: RuntimeHealthFact{
			TenantID: attempt.TenantID, TaskID: "task-active-lease", RunID: attempt.AgentRunID,
			LastHeartbeatAt: attempt.StartedAt, LeaseExpiresAt: env.Clock.Now().Add(time.Minute),
		}, found: true}
		coordinator := NewCoordinator(CoordinatorOptions{Repository: repository, Service: service, RuntimeHealth: health, RuntimeControl: controller, Clock: env.Clock})
		assignment, _ := repository.GetAssignment(context.Background(), activation.ResponsibilityID)
		stats, err := coordinator.reconcileRuntimeHealth(context.Background(), assignment, DefaultPolicy(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind))
		if err != nil || stats.TimedOut != 0 || stats.Heartbeats != 1 || controller.calls != 0 {
			t.Fatalf("active lease stats=%+v calls=%d err=%v", stats, controller.calls, err)
		}
	})

	for _, test := range []struct {
		name       string
		afterRetry time.Duration
		timedOut   int
		calls      int
	}{
		{name: "queued retry receives grace after becoming claimable", afterRetry: time.Second},
		{name: "queued retry remains valid inside heartbeat timeout", afterRetry: 89 * time.Second},
		{name: "queued retry times out after claim grace", afterRetry: 91 * time.Second, timedOut: 1, calls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			tasks := missiontask.NewPlatformRepository()
			saveTaskRecord(t, env, tasks, testTaskRecord("task-queued-retry", "mission-1", "agent-1", missiontask.TaskStatusInProgress))
			repository := NewRepository(env.DBRouter, env.UnitOfWork)
			activation := assignTestResponsibility(t, env, repository, "task-queued-retry", "agent-1")
			claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim-queued-retry", env.Clock.Now(), time.Minute, 2)
			if err != nil || !ok {
				t.Fatalf("claim ok=%v err=%v", ok, err)
			}
			attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim-queued-retry", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
			if err != nil {
				t.Fatal(err)
			}
			retryAt := env.Clock.Now().Add(10 * time.Minute)
			env.Clock.Advance(10*time.Minute + test.afterRetry)
			service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
			controller := &recordingRuntimeController{}
			health := staticRuntimeHealthSource{fact: RuntimeHealthFact{
				TenantID: attempt.TenantID, TaskID: "task-queued-retry", RunID: attempt.AgentRunID,
				LastHeartbeatAt: attempt.StartedAt, TimeoutBaseAt: retryAt,
			}, found: true}
			coordinator := NewCoordinator(CoordinatorOptions{Repository: repository, Service: service, RuntimeHealth: health, RuntimeControl: controller, Clock: env.Clock})
			assignment, _ := repository.GetAssignment(context.Background(), activation.ResponsibilityID)
			policy := DefaultPolicy(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind)
			policy.HeartbeatTimeout = 90 * time.Second
			stats, err := coordinator.reconcileRuntimeHealth(context.Background(), assignment, policy)
			if err != nil || stats.TimedOut != test.timedOut || controller.calls != test.calls {
				t.Fatalf("queued retry stats=%+v calls=%d err=%v", stats, controller.calls, err)
			}
		})
	}

	t.Run("terminal runtime is not cancelled by heartbeat supervision", func(t *testing.T) {
		env := testkit.New(t)
		defer env.Close()
		tasks := missiontask.NewPlatformRepository()
		saveTaskRecord(t, env, tasks, testTaskRecord("task-terminal-health", "mission-1", "agent-1", missiontask.TaskStatusInProgress))
		repository := NewRepository(env.DBRouter, env.UnitOfWork)
		activation := assignTestResponsibility(t, env, repository, "task-terminal-health", "agent-1")
		claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim-terminal-health", env.Clock.Now(), time.Minute, 2)
		if err != nil || !ok {
			t.Fatalf("claim ok=%v err=%v", ok, err)
		}
		attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim-terminal-health", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
		if err != nil {
			t.Fatal(err)
		}
		env.Clock.Advance(2 * time.Minute)
		service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
		controller := &recordingRuntimeController{}
		health := staticRuntimeHealthSource{fact: RuntimeHealthFact{
			TenantID: attempt.TenantID, TaskID: "task-terminal-health", RunID: attempt.AgentRunID,
			UsageTokens: 25, Terminal: true,
		}, found: true}
		coordinator := NewCoordinator(CoordinatorOptions{Repository: repository, Service: service, RuntimeHealth: health, RuntimeControl: controller, Clock: env.Clock})
		assignment, _ := repository.GetAssignment(context.Background(), activation.ResponsibilityID)
		policy := DefaultPolicy(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind)
		policy.MaxTokenBudget = 10
		stats, err := coordinator.reconcileRuntimeHealth(context.Background(), assignment, policy)
		if err != nil || stats.TimedOut != 0 || stats.BudgetBlocked != 0 || stats.Heartbeats != 1 || controller.calls != 0 {
			t.Fatalf("terminal health stats=%+v calls=%d err=%v", stats, controller.calls, err)
		}
		stored, err := repository.FindAttemptByRun(context.Background(), assignment.TenantID, attempt.AgentRunID)
		if err != nil || stored.UsageTokens != 25 || stored.Outcome != "" {
			t.Fatalf("terminal health attempt=%+v err=%v", stored, err)
		}
	})

	t.Run("attempt completed during heartbeat read is not cancelled", func(t *testing.T) {
		env := testkit.New(t)
		defer env.Close()
		tasks := missiontask.NewPlatformRepository()
		saveTaskRecord(t, env, tasks, testTaskRecord("task-concurrent-terminal", "mission-1", "agent-1", missiontask.TaskStatusInProgress))
		repository := NewRepository(env.DBRouter, env.UnitOfWork)
		activation := assignTestResponsibility(t, env, repository, "task-concurrent-terminal", "agent-1")
		claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim-concurrent-terminal", env.Clock.Now(), time.Minute, 2)
		if err != nil || !ok {
			t.Fatalf("claim ok=%v err=%v", ok, err)
		}
		attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim-concurrent-terminal", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
		if err != nil {
			t.Fatal(err)
		}
		env.Clock.Advance(2 * time.Minute)
		service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
		controller := &recordingRuntimeController{}
		health := runtimeHealthSourceFunc(func(ctx context.Context, _, _, _, _ string) (RuntimeHealthFact, bool, error) {
			_, _, persistErr := repository.PersistRuntimeOutcome(ctx, RuntimeOutcomeRecord{
				TenantID: attempt.TenantID, TaskID: "task-concurrent-terminal", RunID: attempt.AgentRunID, Outcome: AttemptOutcomeCompleted,
			}, env.Clock.Now())
			return RuntimeHealthFact{
				TenantID: attempt.TenantID, TaskID: "task-concurrent-terminal", RunID: attempt.AgentRunID,
				LastHeartbeatAt: attempt.StartedAt, UsageTokens: 25,
			}, true, persistErr
		})
		coordinator := NewCoordinator(CoordinatorOptions{Repository: repository, Service: service, RuntimeHealth: health, RuntimeControl: controller, Clock: env.Clock})
		assignment, _ := repository.GetAssignment(context.Background(), activation.ResponsibilityID)
		policy := DefaultPolicy(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind)
		policy.MaxTokenBudget = 10
		stats, err := coordinator.reconcileRuntimeHealth(context.Background(), assignment, policy)
		if err != nil || stats.TimedOut != 0 || stats.BudgetBlocked != 0 || controller.calls != 0 {
			t.Fatalf("concurrent terminal stats=%+v calls=%d err=%v", stats, controller.calls, err)
		}
		stored, err := repository.FindAttemptByRun(context.Background(), assignment.TenantID, attempt.AgentRunID)
		if err != nil || stored.Outcome != AttemptOutcomeCompleted {
			t.Fatalf("concurrent terminal attempt=%+v err=%v", stored, err)
		}
	})

	t.Run("terminal transition during cancel probe suppresses cancellation", func(t *testing.T) {
		env := testkit.New(t)
		defer env.Close()
		tasks := missiontask.NewPlatformRepository()
		saveTaskRecord(t, env, tasks, testTaskRecord("task-terminal-probe", "mission-1", "agent-1", missiontask.TaskStatusInProgress))
		repository := NewRepository(env.DBRouter, env.UnitOfWork)
		activation := assignTestResponsibility(t, env, repository, "task-terminal-probe", "agent-1")
		claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim-terminal-probe", env.Clock.Now(), time.Minute, 2)
		if err != nil || !ok {
			t.Fatalf("claim ok=%v err=%v", ok, err)
		}
		attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim-terminal-probe", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
		if err != nil {
			t.Fatal(err)
		}
		env.Clock.Advance(2 * time.Minute)
		service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
		controller := &recordingRuntimeController{}
		health := &sequencedRuntimeHealthSource{facts: []RuntimeHealthFact{
			{TenantID: attempt.TenantID, TaskID: "task-terminal-probe", RunID: attempt.AgentRunID, LastHeartbeatAt: attempt.StartedAt, UsageTokens: 25},
			{TenantID: attempt.TenantID, TaskID: "task-terminal-probe", RunID: attempt.AgentRunID, LastHeartbeatAt: env.Clock.Now(), UsageTokens: 100, Terminal: true},
		}}
		coordinator := NewCoordinator(CoordinatorOptions{Repository: repository, Service: service, RuntimeHealth: health, RuntimeControl: controller, Clock: env.Clock})
		assignment, _ := repository.GetAssignment(context.Background(), activation.ResponsibilityID)
		policy := DefaultPolicy(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind)
		policy.MaxTokenBudget = 10
		stats, err := coordinator.reconcileRuntimeHealth(context.Background(), assignment, policy)
		if err != nil || stats.BudgetBlocked != 0 || stats.Heartbeats != 1 || controller.calls != 0 || health.calls != 2 {
			t.Fatalf("terminal probe stats=%+v calls=%d health_calls=%d err=%v", stats, controller.calls, health.calls, err)
		}
		stored, err := repository.FindAttemptByRun(context.Background(), assignment.TenantID, attempt.AgentRunID)
		if err != nil || stored.UsageTokens != 100 || stored.Outcome != "" {
			t.Fatalf("terminal probe attempt=%+v err=%v", stored, err)
		}
	})

	for _, test := range []struct {
		name       string
		firstFound bool
	}{
		{name: "stale heartbeat becomes healthy before cancellation", firstFound: true},
		{name: "missing heartbeat appears before cancellation", firstFound: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			tasks := missiontask.NewPlatformRepository()
			saveTaskRecord(t, env, tasks, testTaskRecord("task-health-refresh", "mission-1", "agent-1", missiontask.TaskStatusInProgress))
			repository := NewRepository(env.DBRouter, env.UnitOfWork)
			activation := assignTestResponsibility(t, env, repository, "task-health-refresh", "agent-1")
			claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim-health-refresh", env.Clock.Now(), time.Minute, 2)
			if err != nil || !ok {
				t.Fatalf("claim ok=%v err=%v", ok, err)
			}
			attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim-health-refresh", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
			if err != nil {
				t.Fatal(err)
			}
			env.Clock.Advance(2 * time.Minute)
			service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
			controller := &recordingRuntimeController{}
			health := &sequencedRuntimeHealthSource{
				facts: []RuntimeHealthFact{
					{TenantID: attempt.TenantID, TaskID: "task-health-refresh", RunID: attempt.AgentRunID, LastHeartbeatAt: attempt.StartedAt},
					{TenantID: attempt.TenantID, TaskID: "task-health-refresh", RunID: attempt.AgentRunID, LastHeartbeatAt: env.Clock.Now(), LeaseExpiresAt: env.Clock.Now().Add(time.Minute)},
				},
				found: []bool{test.firstFound, true},
			}
			coordinator := NewCoordinator(CoordinatorOptions{Repository: repository, Service: service, RuntimeHealth: health, RuntimeControl: controller, Clock: env.Clock})
			assignment, _ := repository.GetAssignment(context.Background(), activation.ResponsibilityID)
			stats, err := coordinator.reconcileRuntimeHealth(context.Background(), assignment, DefaultPolicy(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind))
			if err != nil || stats.TimedOut != 0 || stats.Heartbeats != 1 || controller.calls != 0 || health.calls != 2 {
				t.Fatalf("health refresh stats=%+v calls=%d health_calls=%d err=%v", stats, controller.calls, health.calls, err)
			}
		})
	}

	t.Run("cumulative token budget", func(t *testing.T) {
		env := testkit.New(t)
		defer env.Close()
		tasks := missiontask.NewPlatformRepository()
		saveTaskRecord(t, env, tasks, testTaskRecord("task-budget", "mission-1", "agent-1", missiontask.TaskStatusInProgress))
		repository := NewRepository(env.DBRouter, env.UnitOfWork)
		activation := assignTestResponsibility(t, env, repository, "task-budget", "agent-1")
		claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim-budget", env.Clock.Now(), time.Minute, 2)
		if err != nil || !ok {
			t.Fatalf("claim ok=%v err=%v", ok, err)
		}
		attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim-budget", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
		if err != nil {
			t.Fatal(err)
		}
		previous := AttemptRecord{ResponsibilityAttempt: ResponsibilityAttempt{ID: "attempt-previous", TenantID: attempt.TenantID,
			ResponsibilityID: attempt.ResponsibilityID, AgentRunID: "run-previous", ClaimToken: "claim-previous", Attempt: 1,
			StartedAt: env.Clock.Now().Add(-time.Hour), HeartbeatAt: env.Clock.Now().Add(-59 * time.Minute), Outcome: AttemptOutcomeFailed},
			ActivationID: "activation-previous", RuntimeKind: "codex", AgentProfileID: "agent-1", UsageTokens: 80,
			CompletedAt: env.Clock.Now().Add(-58 * time.Minute), CreatedAt: env.Clock.Now().Add(-time.Hour), UpdatedAt: env.Clock.Now().Add(-58 * time.Minute)}
		if err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error { return putAttemptTx(ctx, tx, previous) }); err != nil {
			t.Fatal(err)
		}
		service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock, RetryDelays: []time.Duration{time.Minute}})
		controller := &recordingRuntimeController{}
		health := staticRuntimeHealthSource{fact: RuntimeHealthFact{TenantID: attempt.TenantID, TaskID: "task-budget", RunID: attempt.AgentRunID,
			LastHeartbeatAt: env.Clock.Now(), LeaseExpiresAt: env.Clock.Now().Add(time.Minute), UsageTokens: 30}, found: true}
		coordinator := NewCoordinator(CoordinatorOptions{Repository: repository, Service: service, RuntimeHealth: health, RuntimeControl: controller, Clock: env.Clock})
		assignment, _ := repository.GetAssignment(context.Background(), activation.ResponsibilityID)
		policy := DefaultPolicy(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind)
		policy.MaxTokenBudget = 100
		stats, err := coordinator.reconcileRuntimeHealth(context.Background(), assignment, policy)
		if err != nil || stats.BudgetBlocked != 1 || controller.calls != 1 {
			t.Fatalf("budget stats=%+v calls=%d err=%v", stats, controller.calls, err)
		}
		blockers, err := repository.ListBlockers(context.Background(), assignment.TenantID, assignment.ID, true)
		if err != nil || len(blockers) != 1 || blockers[0].Kind != BlockerKindBudget {
			t.Fatalf("budget blockers=%+v err=%v", blockers, err)
		}
	})
}

func TestDispatchPolicyEnforcesAvailabilityAndDataAuthority(t *testing.T) {
	profile := agentcore.AgentProfile{ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}}
	assignment := AssignmentRecord{ResponsibilityAssignment: ResponsibilityAssignment{ID: "responsibility-1", TenantID: "tenant-1",
		WorkKind: WorkKindMissionTask, WorkID: "task-1", AccountableAgent: "agent-1", State: AssignmentStateActive, Priority: 1,
		AcceptBy: time.Now().Add(time.Minute), Version: 1}, MissionID: "mission-1"}
	policy := DefaultPolicy(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind)
	coordinator := NewCoordinator(CoordinatorOptions{Directory: staticResponsibilityDirectory{entry: agentcore.AgentDirectoryEntry{AgentProfileID: "agent-1",
		Availability: agentcore.AgentAvailability{Status: agentcore.AvailabilityBusy}, OrgIdentity: agentcore.AgentOrgIdentity{AuthorityScope: "customer.read"}}}})
	kind, reason, err := coordinator.policyBlockReason(context.Background(), assignment, policy, profile, nil)
	if err != nil || kind != BlockerKindCapacity || reason == "" {
		t.Fatalf("busy availability block kind=%q reason=%q err=%v", kind, reason, err)
	}
	coordinator.directory = staticResponsibilityDirectory{entry: agentcore.AgentDirectoryEntry{AgentProfileID: "agent-1",
		Availability: agentcore.AgentAvailability{Status: agentcore.AvailabilityAvailable, Capacity: 2, CurrentLoad: 1}, OrgIdentity: agentcore.AgentOrgIdentity{AuthorityScope: "customer.read"}}}
	policy.DataScopes = []string{"customer.write"}
	kind, reason, err = coordinator.policyBlockReason(context.Background(), assignment, policy, profile, nil)
	if err != nil || kind != BlockerKindPermission || reason == "" {
		t.Fatalf("data authority block kind=%q reason=%q err=%v", kind, reason, err)
	}
	policy.DataScopes = nil
	policy.ToolScopes = []string{"todo.read", "repository.read"}
	profile.ToolRefs = []agentcore.ToolRef{{ToolID: "unrelated.profile.tool"}}
	kind, reason, err = coordinator.policyBlockReason(context.Background(), assignment, policy, profile, []string{"todo.read"})
	if err != nil || kind != "" || reason != "" {
		t.Fatalf("allowed task scope was blocked by profile tools kind=%q reason=%q err=%v", kind, reason, err)
	}
	kind, reason, err = coordinator.policyBlockReason(context.Background(), assignment, policy, profile, []string{"repository.write"})
	if err != nil || kind != BlockerKindPermission || reason == "" {
		t.Fatalf("out-of-policy task scope kind=%q reason=%q err=%v", kind, reason, err)
	}
}

func TestResolvedApprovalGrantIsBoundToAgentPolicyAndExecutionScopes(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	tasks := missiontask.NewPlatformRepository()
	task := testTaskRecord("task-approved-dispatch", "mission-1", "agent-1", missiontask.TaskStatusAssigned)
	task.Contract.ExecutionPermissionScopes = []string{"repository.write"}
	saveTaskRecord(t, env, tasks, task)
	activation := assignResponsibilityForTenant(t, env, repository, "tenant-1", "mission-1", task.Contract.ID.String(), "agent-1")
	assignment, err := repository.GetAssignment(ctx, activation.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	policy := DefaultPolicy(assignment.TenantID, assignment.AccountableAgent, assignment.WorkKind)
	policy.ApprovalPolicy = ApprovalBeforeSideEffect
	policy, err = repository.SavePolicy(ctx, policy, "manager-1", "save-approval-policy", policy.Version, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := repository.OpenBlocker(ctx, OpenBlockerInput{
		TenantID: assignment.TenantID, ResponsibilityID: assignment.ID, Kind: BlockerKindApproval,
		Needed: "human approval is required before runtime execution", RequestedFrom: "user:manager",
		RecheckAt: env.Clock.Now().Add(time.Hour), DedupeKey: "dispatch-policy-approval",
	}, env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	domain := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	operations := NewOperationsService(OperationsServiceOptions{Repository: repository, Service: domain, Clock: env.Clock})
	if _, err := operations.ResolveBlocker(ctx, assignment.TenantID, blocker.ID, "manager-1", "approved for this task scope", "approve-dispatch", blocker.Version); err != nil {
		t.Fatal(err)
	}
	granted, err := repository.ApprovalGranted(ctx, assignment, policy, []string{"repository.write"})
	if err != nil || !granted {
		t.Fatalf("matching approval granted=%v err=%v", granted, err)
	}
	if granted, err = repository.ApprovalGranted(ctx, assignment, policy, []string{"repository.admin"}); err != nil || granted {
		t.Fatalf("changed scope approval granted=%v err=%v", granted, err)
	}
	changedPolicy := policy
	changedPolicy.Version++
	if granted, err = repository.ApprovalGranted(ctx, assignment, changedPolicy, []string{"repository.write"}); err != nil || granted {
		t.Fatalf("changed policy approval granted=%v err=%v", granted, err)
	}
	changedAgent := assignment
	changedAgent.AccountableAgent = "agent-2"
	changedPolicy.AgentProfileID = "agent-2"
	changedPolicy.ID = PolicyID(changedPolicy.TenantID, changedPolicy.AgentProfileID, changedPolicy.WorkKind)
	changedPolicy.Version = policy.Version
	if granted, err = repository.ApprovalGranted(ctx, changedAgent, changedPolicy, []string{"repository.write"}); err != nil || granted {
		t.Fatalf("changed Agent approval granted=%v err=%v", granted, err)
	}

	dispatcher := &capturingResponsibilityDispatcher{}
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: domain,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: dispatcher, Clock: env.Clock,
	})
	result, err := coordinator.reconcileActivation(ctx, activation)
	if err != nil || result.Dispatched != 1 || len(dispatcher.requests) != 1 {
		t.Fatalf("approved dispatch result=%+v requests=%+v err=%v", result, dispatcher.requests, err)
	}
	nextPolicy := policy
	nextPolicy.MaxParallel++
	if _, err := repository.SavePolicy(ctx, nextPolicy, "manager-1", "change-approval-policy", policy.Version, env.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	if _, err := operations.ResolveBlocker(ctx, assignment.TenantID, blocker.ID, "manager-1", "approved for this task scope", "approve-dispatch", blocker.Version); err != nil {
		t.Fatalf("approval replay after policy change: %v", err)
	}
}

func TestCoordinatorCancelsReassignedAttemptUsingAttemptAgent(t *testing.T) {
	controller := &recordingRuntimeController{}
	service := &Service{}
	coordinator := NewCoordinator(CoordinatorOptions{RuntimeControl: controller, Service: service})
	assignment := AssignmentRecord{ResponsibilityAssignment: ResponsibilityAssignment{
		TenantID: "tenant-1", WorkID: "task-1", AccountableAgent: "agent-new",
	}, MissionID: "mission-1"}
	attempt := AttemptRecord{ResponsibilityAttempt: ResponsibilityAttempt{AgentRunID: "run-old"}, AgentProfileID: "agent-old"}
	if err := coordinator.cancelAttemptWithOutcome(context.Background(), assignment, attempt, AttemptOutcomeCancelled, "reassigned"); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("cancellation outcome error=%v, want invalid contract from the empty persistence fixture", err)
	}
	if controller.calls != 1 || controller.request.AgentID != "agent-old" {
		t.Fatalf("cancellation request=%+v calls=%d", controller.request, controller.calls)
	}
}

func TestEvidenceCompletenessUsesDurableLatestSubmission(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	task := testTaskRecord("task-evidence", "mission-1", "agent-1", missiontask.TaskStatusInReview)
	task.Contract.EvidenceRequirements = []missiontask.EvidenceRequirement{{ID: "evidence-required", Description: "test output", Kind: missiontask.EvidenceRequirementKindExternalURL, Required: true}}
	task.Contract.AcceptanceCriteria = []missiontask.AcceptanceCriterion{{ID: "criterion-1", Description: "first"}, {ID: "criterion-2", Description: "second"}}
	saveTaskRecord(t, env, tasks, task)
	service := NewService(ServiceOptions{Repository: NewRepository(env.DBRouter, env.UnitOfWork), UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})

	completeness, err := service.EvidenceCompleteness(context.Background(), task)
	if err != nil || completeness != 0 {
		t.Fatalf("no submission completeness=%v err=%v", completeness, err)
	}
	partial := missiontask.TaskSubmission{ID: "submission-partial", TaskID: task.Contract.ID, AgentID: "agent-1", RunID: "run-partial",
		AgentResultRef: "result-partial", Summary: agentcore.Summary{Text: "partial"}, SubmittedAt: env.Clock.Now(), IdempotencyKey: "partial"}
	if err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
		return tasks.SaveSubmission(ctx, tx, partial, nil, []missiontask.EvidenceSubmission{{ID: "evidence-partial", SubmissionID: partial.ID,
			TaskID: task.Contract.ID, AgentID: "agent-1", SubmittedAt: env.Clock.Now(), Evidence: agentcore.Evidence{ID: "evidence-1",
				EvidenceRequirementID: "evidence-required", Kind: agentcore.EvidenceKindExternalURL, ExternalURL: "https://evidence.example.test/partial"}}})
	}); err != nil {
		t.Fatal(err)
	}
	completeness, err = service.EvidenceCompleteness(context.Background(), task)
	if err != nil || completeness != float64(1)/3 {
		t.Fatalf("partial completeness=%v err=%v", completeness, err)
	}
	env.Clock.Advance(time.Second)
	complete := missiontask.TaskSubmission{ID: "submission-complete", TaskID: task.Contract.ID, AgentID: "agent-1", RunID: "run-complete",
		AgentResultRef: "result-complete", Summary: agentcore.Summary{Text: "complete"}, SubmittedAt: env.Clock.Now(), IdempotencyKey: "complete"}
	evidence := []missiontask.EvidenceSubmission{
		{ID: "evidence-complete-1", SubmissionID: complete.ID, TaskID: task.Contract.ID, AgentID: "agent-1", SubmittedAt: env.Clock.Now(), Evidence: agentcore.Evidence{ID: "evidence-2", EvidenceRequirementID: "evidence-required", AcceptanceCriterionID: "criterion-1", Kind: agentcore.EvidenceKindExternalURL, ExternalURL: "https://evidence.example.test/one"}},
		{ID: "evidence-complete-2", SubmissionID: complete.ID, TaskID: task.Contract.ID, AgentID: "agent-1", SubmittedAt: env.Clock.Now(), Evidence: agentcore.Evidence{ID: "evidence-3", AcceptanceCriterionID: "criterion-2", Kind: agentcore.EvidenceKindExternalURL, ExternalURL: "https://evidence.example.test/two"}},
	}
	if err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
		return tasks.SaveSubmission(ctx, tx, complete, nil, evidence)
	}); err != nil {
		t.Fatal(err)
	}
	completeness, err = service.EvidenceCompleteness(context.Background(), task)
	if err != nil || completeness != 1 {
		t.Fatalf("complete evidence completeness=%v err=%v", completeness, err)
	}
}

func assignResponsibilityForTenant(t *testing.T, env *testkit.Env, repository *Repository, tenantID, missionID, taskID, agentID string) ActivationRecord {
	t.Helper()
	var activation ActivationRecord
	err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
		_, saved, err := repository.AssignMissionTaskTx(ctx, tx, MissionTaskAssignmentInput{TenantID: tenantID, MissionID: missionID, TaskID: taskID,
			AgentProfileID: agentID, Priority: 50, AcceptBy: env.Clock.Now().Add(time.Minute), ActivationCause: ActivationCauseAssigned}, env.Clock.Now())
		activation = saved
		return err
	})
	if err != nil {
		t.Fatalf("assign responsibility %s: %v", taskID, err)
	}
	return activation
}

type recordingRuntimeController struct {
	calls   int
	request RuntimeCancelRequest
	err     error
}

type recordingTaskReassignment struct {
	calls   int
	request TaskReassignmentRequest
	err     error
	onCall  func(TaskReassignmentRequest) error
}

func (r *recordingTaskReassignment) ReassignTask(_ context.Context, request TaskReassignmentRequest) error {
	r.calls++
	r.request = request
	if r.onCall != nil {
		return r.onCall(request)
	}
	return r.err
}

func (c *recordingRuntimeController) CancelResponsibility(_ context.Context, request RuntimeCancelRequest) error {
	c.calls++
	c.request = request
	return c.err
}

type staticRuntimeHealthSource struct {
	fact  RuntimeHealthFact
	found bool
}

type runtimeHealthSourceFunc func(context.Context, string, string, string, string) (RuntimeHealthFact, bool, error)

func (f runtimeHealthSourceFunc) FindRuntimeHealth(ctx context.Context, tenantID, missionID, taskID, runID string) (RuntimeHealthFact, bool, error) {
	return f(ctx, tenantID, missionID, taskID, runID)
}

type sequencedRuntimeHealthSource struct {
	facts []RuntimeHealthFact
	found []bool
	calls int
}

func (s *sequencedRuntimeHealthSource) FindRuntimeHealth(context.Context, string, string, string, string) (RuntimeHealthFact, bool, error) {
	if len(s.facts) == 0 {
		return RuntimeHealthFact{}, false, nil
	}
	index := s.calls
	if index >= len(s.facts) {
		index = len(s.facts) - 1
	}
	s.calls++
	found := true
	if index < len(s.found) {
		found = s.found[index]
	}
	return s.facts[index], found, nil
}

func (s staticRuntimeHealthSource) FindRuntimeHealth(_ context.Context, _, _, _, _ string) (RuntimeHealthFact, bool, error) {
	return s.fact, s.found, nil
}

type staticResponsibilityDirectory struct {
	entry agentcore.AgentDirectoryEntry
}

func (s staticResponsibilityDirectory) GetDirectoryEntry(_ context.Context, _ agentcore.AgentProfileID) (agentcore.AgentDirectoryEntry, error) {
	return s.entry, nil
}
