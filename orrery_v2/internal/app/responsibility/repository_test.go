package responsibility

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
	"orrery_v2/internal/platform/testkit"
	"orrery_v2/internal/platform/uow"
)

func TestRepositoryAssignmentAndActivationAreIdempotent(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	input := MissionTaskAssignmentInput{
		TenantID: "tenant-1", MissionID: "mission-1", TaskID: "task-1", AgentProfileID: "agent-1",
		SourceKind: "scheduler_agent_task", SourceID: "schedule-1", SourceStepID: "run-1",
		Priority: 50, AcceptBy: env.Clock.Now().Add(time.Minute), ActivationCause: ActivationCauseAssigned,
	}
	var firstAssignment, secondAssignment AssignmentRecord
	var firstActivation, secondActivation ActivationRecord
	for index := 0; index < 2; index++ {
		err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
			assignment, activation, err := repository.AssignMissionTaskTx(ctx, tx, input, env.Clock.Now())
			if index == 0 {
				firstAssignment, firstActivation = assignment, activation
			} else {
				secondAssignment, secondActivation = assignment, activation
			}
			return err
		})
		if err != nil {
			t.Fatalf("assign mission task %d: %v", index, err)
		}
	}
	if firstAssignment.ID != secondAssignment.ID || firstAssignment.Version != 1 || secondAssignment.Version != 1 {
		t.Fatalf("assignments are not idempotent: first=%+v second=%+v", firstAssignment, secondAssignment)
	}
	if secondAssignment.SourceKind != input.SourceKind || secondAssignment.SourceID != input.SourceID || secondAssignment.SourceStepID != input.SourceStepID {
		t.Fatalf("assignment source = %+v", secondAssignment)
	}
	if firstActivation.ID != secondActivation.ID || firstActivation.DedupeKey != secondActivation.DedupeKey {
		t.Fatalf("activations are not idempotent: first=%+v second=%+v", firstActivation, secondActivation)
	}
}

func TestRepositoryTerminatesMissionAssignmentsBeforeTaskDeletion(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	now := env.Clock.Now()
	var targetAssignment AssignmentRecord
	var targetActivation ActivationRecord
	var otherActivation ActivationRecord
	if err := env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		var err error
		targetAssignment, targetActivation, err = repository.AssignMissionTaskTx(ctx, tx, MissionTaskAssignmentInput{
			TenantID: "tenant-1", MissionID: "mission-delete", TaskID: "task-delete", AgentProfileID: "agent-1",
			Priority: 50, AcceptBy: now.Add(time.Hour), ActivationCause: ActivationCauseAssigned,
		}, now)
		if err != nil {
			return err
		}
		_, otherActivation, err = repository.AssignMissionTaskTx(ctx, tx, MissionTaskAssignmentInput{
			TenantID: "tenant-1", MissionID: "mission-keep", TaskID: "task-keep", AgentProfileID: "agent-1",
			Priority: 50, AcceptBy: now.Add(time.Hour), ActivationCause: ActivationCauseAssigned,
		}, now)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	terminated, err := repository.TerminateMissionAssignments(ctx, "tenant-1", "mission-delete", "owner-1", "Mission deleted", now.Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if terminated != 1 {
		t.Fatalf("terminated assignments = %d, want 1", terminated)
	}
	assignment, err := repository.GetAssignment(ctx, targetAssignment.ID)
	if err != nil {
		t.Fatal(err)
	}
	if assignment.State != AssignmentStateTerminated || assignment.TerminatedBy != "owner-1" || assignment.TerminationNote != "Mission deleted" {
		t.Fatalf("terminated assignment = %+v", assignment)
	}
	if activation := getActivationForTest(t, env, targetActivation.ID); activation.Status != ActivationStatusSuperseded || activation.ClaimToken != "" {
		t.Fatalf("target activation = %+v", activation)
	}
	if activation := getActivationForTest(t, env, otherActivation.ID); activation.Status != ActivationStatusPending {
		t.Fatalf("unrelated activation = %+v", activation)
	}
	terminated, err = repository.TerminateMissionAssignments(ctx, "tenant-1", "mission-delete", "owner-1", "Mission deleted", now.Add(2*time.Minute))
	if err != nil || terminated != 0 {
		t.Fatalf("idempotent termination count=%d err=%v", terminated, err)
	}
}

func TestRepositoryBackfillsAssignmentSourceWithoutChangingVersionAndRejectsConflict(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	input := MissionTaskAssignmentInput{
		TenantID: "tenant-1", MissionID: "mission-1", TaskID: "task-source", AgentProfileID: "agent-1",
		Priority: 50, AcceptBy: env.Clock.Now().Add(time.Minute), ActivationCause: ActivationCauseAssigned,
	}
	assign := func(value MissionTaskAssignmentInput) (AssignmentRecord, error) {
		var assignment AssignmentRecord
		err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
			var err error
			assignment, _, err = repository.AssignMissionTaskTx(ctx, tx, value, env.Clock.Now())
			return err
		})
		return assignment, err
	}
	first, err := assign(input)
	if err != nil {
		t.Fatal(err)
	}
	input.SourceKind, input.SourceID, input.SourceStepID = "scheduler_agent_task", "schedule-1", "run-1"
	backfilled, err := assign(input)
	if err != nil {
		t.Fatal(err)
	}
	if backfilled.Version != first.Version || backfilled.SourceKind != input.SourceKind || backfilled.SourceID != input.SourceID || backfilled.SourceStepID != input.SourceStepID {
		t.Fatalf("backfilled assignment = %+v first=%+v", backfilled, first)
	}
	audits, err := repository.ListAudits(context.Background(), backfilled.TenantID, backfilled.ID, 10)
	if err != nil {
		t.Fatal(err)
	}
	foundSourceAudit := false
	for _, audit := range audits {
		foundSourceAudit = foundSourceAudit || audit.Action == AuditAssignmentSourceLinked
	}
	if !foundSourceAudit {
		t.Fatalf("source backfill audit missing: %+v", audits)
	}
	input.SourceStepID = "run-other"
	if _, err := assign(input); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting source error = %v, want ErrConflict", err)
	}
}

func TestRepositoryListsReadyActivationsByAssignmentPriority(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	for _, input := range []MissionTaskAssignmentInput{
		{TenantID: "tenant-1", MissionID: "mission-1", TaskID: "task-low", AgentProfileID: "agent-1", Priority: 10, AcceptBy: env.Clock.Now().Add(time.Minute), ActivationCause: ActivationCauseAssigned},
		{TenantID: "tenant-1", MissionID: "mission-1", TaskID: "task-high", AgentProfileID: "agent-2", Priority: 90, AcceptBy: env.Clock.Now().Add(time.Minute), ActivationCause: ActivationCauseAssigned},
	} {
		if err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
			_, _, err := repository.AssignMissionTaskTx(ctx, tx, input, env.Clock.Now())
			return err
		}); err != nil {
			t.Fatalf("assign %s: %v", input.TaskID, err)
		}
	}
	ready, err := repository.ListReadyActivations(context.Background(), env.Clock.Now(), 10)
	if err != nil {
		t.Fatalf("list ready: %v", err)
	}
	if len(ready) != 2 || ready[0].ResponsibilityID != AssignmentID("tenant-1", WorkKindMissionTask, "task-high") {
		t.Fatalf("ready activations = %+v, want high priority first", ready)
	}
}

func TestRepositoryListsReadyActivationsWithInvalidUnrelatedRecord(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-isolated", "agent-1")
	ctx := context.Background()
	if _, err := env.DB.Exec(ctx, `INSERT INTO platform_records (table_name, record_key, payload_json) VALUES (?, ?, ?)`, "aaa_unrelated", "broken", "{"); err != nil {
		t.Fatalf("insert invalid unrelated record: %v", err)
	}

	ready, err := repository.ListReadyActivations(ctx, env.Clock.Now(), 10)
	if err != nil {
		t.Fatalf("list ready: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != activation.ID {
		t.Fatalf("ready activations = %+v, want %s", ready, activation.ID)
	}
}

func TestRepositoryClaimsActivationOnceAndEnforcesAgentCapacity(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	first := assignTestResponsibility(t, env, repository, "task-1", "agent-1")
	second := assignTestResponsibility(t, env, repository, "task-2", "agent-1")

	var wg sync.WaitGroup
	results := make(chan bool, 2)
	for _, token := range []string{"claim-a", "claim-b"} {
		wg.Add(1)
		go func(token string) {
			defer wg.Done()
			_, claimed, err := repository.ClaimActivation(context.Background(), first.ID, "worker", token, env.Clock.Now(), time.Minute, 1)
			if err != nil {
				t.Errorf("claim activation: %v", err)
			}
			results <- claimed
		}(token)
	}
	wg.Wait()
	close(results)
	claimedCount := 0
	for claimed := range results {
		if claimed {
			claimedCount++
		}
	}
	if claimedCount != 1 {
		t.Fatalf("claimed count = %d, want 1", claimedCount)
	}
	claimed, err := repository.ListReadyActivations(context.Background(), env.Clock.Now(), 10)
	if err != nil {
		t.Fatalf("list ready: %v", err)
	}
	if len(claimed) != 1 || claimed[0].ID != second.ID {
		t.Fatalf("ready activations = %+v, want only second", claimed)
	}
	firstRecord := getActivationForTest(t, env, first.ID)
	if _, err := repository.MarkDispatched(context.Background(), first.ID, firstRecord.ClaimToken, RunIDForActivation(first.ID), "codex", env.Clock.Now()); err != nil {
		t.Fatalf("mark dispatched: %v", err)
	}
	if _, ok, err := repository.ClaimActivation(context.Background(), second.ID, "worker", "claim-c", env.Clock.Now(), time.Minute, 1); err != nil || ok {
		t.Fatalf("capacity claim ok=%v err=%v, want blocked", ok, err)
	}
}

func TestRepositorySerializesCapacityAcrossDifferentActivations(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	first := assignTestResponsibility(t, env, repository, "task-capacity-1", "agent-1")
	second := assignTestResponsibility(t, env, repository, "task-capacity-2", "agent-1")

	start := make(chan struct{})
	results := make(chan bool, 2)
	var wg sync.WaitGroup
	for index, activation := range []ActivationRecord{first, second} {
		wg.Add(1)
		go func(index int, activation ActivationRecord) {
			defer wg.Done()
			<-start
			_, claimed, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", fmt.Sprintf("capacity-claim-%d", index), env.Clock.Now(), time.Minute, 1)
			if err != nil {
				t.Errorf("claim activation %s: %v", activation.ID, err)
			}
			results <- claimed
		}(index, activation)
	}
	close(start)
	wg.Wait()
	close(results)
	claimed := 0
	for result := range results {
		if result {
			claimed++
		}
	}
	if claimed != 1 {
		t.Fatalf("claimed activations = %d, want one Agent-capacity reservation", claimed)
	}
}

func TestRepositoryCapacityRemainsBoundToAttemptAgentAfterReassignment(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	oldActivation := assignTestResponsibility(t, env, repository, "task-reassigned-capacity", "agent-old")
	claimed, ok, err := repository.ClaimActivation(ctx, oldActivation.ID, "worker", "old-agent-claim", env.Clock.Now(), time.Minute, 1)
	if err != nil || !ok {
		t.Fatalf("claim old Agent activation ok=%v err=%v", ok, err)
	}
	if _, err := repository.MarkDispatched(ctx, claimed.ID, "old-agent-claim", RunIDForActivation(claimed.ID), "codex", env.Clock.Now()); err != nil {
		t.Fatalf("dispatch old Agent attempt: %v", err)
	}

	current, err := repository.GetAssignmentForWork(ctx, "tenant-1", WorkKindMissionTask, "task-reassigned-capacity")
	if err != nil {
		t.Fatal(err)
	}
	var reassigned ActivationRecord
	err = env.UnitOfWork.Do(ctx, func(ctx context.Context, tx uow.Tx) error {
		_, next, err := repository.AssignMissionTaskTx(ctx, tx, MissionTaskAssignmentInput{
			TenantID: "tenant-1", MissionID: "mission-1", TaskID: "task-reassigned-capacity", AgentProfileID: "agent-new",
			Priority: 50, AcceptBy: env.Clock.Now().Add(time.Minute), ActivationCause: ActivationCauseReassigned,
			ExpectedAssignmentVersion: current.Version,
		}, env.Clock.Now())
		reassigned = next
		return err
	})
	if err != nil {
		t.Fatalf("reassign responsibility: %v", err)
	}
	oldAgentNext := assignTestResponsibility(t, env, repository, "task-old-agent-next", "agent-old")

	if _, claimedNew, err := repository.ClaimActivation(ctx, reassigned.ID, "worker", "new-agent-claim", env.Clock.Now(), time.Minute, 1); err != nil || !claimedNew {
		t.Fatalf("new Agent capacity claim ok=%v err=%v, want available", claimedNew, err)
	}
	if _, claimedOld, err := repository.ClaimActivation(ctx, oldAgentNext.ID, "worker", "old-agent-next-claim", env.Clock.Now(), time.Minute, 1); err != nil || claimedOld {
		t.Fatalf("old Agent capacity claim ok=%v err=%v, want live old attempt to reserve capacity", claimedOld, err)
	}
}

func TestRepositoryRecoversExpiredClaimAfterRestart(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-restart", "agent-1")
	if _, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker-old", "claim-old", env.Clock.Now(), time.Minute, 1); err != nil || !ok {
		t.Fatalf("initial claim ok=%v err=%v", ok, err)
	}
	env.Clock.Advance(time.Minute + time.Second)
	restarted := NewRepository(env.DBRouter, env.UnitOfWork)
	ready, err := restarted.ListReadyActivations(context.Background(), env.Clock.Now(), 10)
	if err != nil {
		t.Fatalf("list after restart: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != activation.ID {
		t.Fatalf("ready after restart = %+v", ready)
	}
	claimed, ok, err := restarted.ClaimActivation(context.Background(), activation.ID, "worker-new", "claim-new", env.Clock.Now(), time.Minute, 1)
	if err != nil || !ok || claimed.ClaimToken != "claim-new" {
		t.Fatalf("recovered claim = %+v ok=%v err=%v", claimed, ok, err)
	}
}

func TestCoordinatorUsesProfileRuntimeAndWaitsForDependencies(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("dependency", "mission-1", "agent-2", missiontask.TaskStatusInReview))
	task := testTaskRecord("task-1", "mission-1", "agent-1", missiontask.TaskStatusAssigned)
	task.Contract.Dependencies = []missiontask.TaskDependencyRef{{TaskID: "dependency"}}
	saveTaskRecord(t, env, tasks, task)
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-1", "agent-1")
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	dispatcher := &capturingResponsibilityDispatcher{}
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindOpenClaw}},
		}},
		Dispatcher: dispatcher, Clock: env.Clock, IDGenerator: env.IDGenerator,
	})
	stats, err := coordinator.Reconcile(context.Background(), 10)
	if err != nil {
		t.Fatalf("reconcile blocked: %v", err)
	}
	if stats.DependencyBlocked != 1 || len(dispatcher.requests) != 0 {
		t.Fatalf("blocked stats=%+v requests=%+v", stats, dispatcher.requests)
	}
	dependency := testTaskRecord("dependency", "mission-1", "agent-2", missiontask.TaskStatusApproved)
	saveTaskRecord(t, env, tasks, dependency)
	stats, err = coordinator.Reconcile(context.Background(), 10)
	if err != nil {
		t.Fatalf("reconcile ready: %v", err)
	}
	if stats.Dispatched != 1 || len(dispatcher.requests) != 1 {
		t.Fatalf("ready stats=%+v requests=%+v", stats, dispatcher.requests)
	}
	request := dispatcher.requests[0]
	if request.RuntimeKind != string(agentcore.RuntimeKindOpenClaw) || request.RunID != RunIDForActivation(activation.ID) || request.AgentProfileID != "agent-1" || request.Attempt != 1 {
		t.Fatalf("dispatch request = %+v", request)
	}
}

func TestCoordinatorSupersedesHumanActivationWithoutRuntime(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-human", "mission-1", "human-1", missiontask.TaskStatusAssigned))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-human", "human-1")
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	dispatcher := &capturingResponsibilityDispatcher{}
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"human-1": {ID: "human-1", AgentKind: agentcore.AgentKindHuman, Enabled: true},
		}},
		Dispatcher: dispatcher, Clock: env.Clock, IDGenerator: env.IDGenerator,
	})
	if _, err := coordinator.Reconcile(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if got := getActivationForTest(t, env, activation.ID); got.Status != ActivationStatusSuperseded {
		t.Fatalf("activation status = %q, want superseded", got.Status)
	}
	if len(dispatcher.requests) != 0 {
		t.Fatalf("human activation dispatched: %+v", dispatcher.requests)
	}
}

func TestCoordinatorTerminatesResponsibilityWhenMissionTaskWasDeleted(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-deleted", "agent-1")
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	dispatcher := &capturingResponsibilityDispatcher{}
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: dispatcher, Clock: env.Clock, IDGenerator: env.IDGenerator,
	})

	if _, err := coordinator.Reconcile(context.Background(), 10); err != nil {
		t.Fatalf("reconcile deleted task: %v", err)
	}
	assignment, err := repository.GetAssignment(context.Background(), activation.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	if assignment.State != AssignmentStateTerminated || assignment.TerminationNote != "mission task no longer exists" {
		t.Fatalf("assignment after deleted task = %+v", assignment)
	}
	if got := getActivationForTest(t, env, activation.ID); got.Status != ActivationStatusSuperseded {
		t.Fatalf("activation status = %q, want superseded", got.Status)
	}
	if len(dispatcher.requests) != 0 {
		t.Fatalf("deleted task was dispatched: %+v", dispatcher.requests)
	}
	if _, err := coordinator.Reconcile(context.Background(), 10); err != nil {
		t.Fatalf("second reconcile deleted task: %v", err)
	}
}

func TestCoordinatorTerminatesDeletedTaskFromReadyActivation(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-deleted-activation", "agent-1")
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: &capturingResponsibilityDispatcher{}, Clock: env.Clock, IDGenerator: env.IDGenerator,
	})

	if _, err := coordinator.reconcileActivation(context.Background(), activation); err != nil {
		t.Fatalf("reconcile deleted task activation: %v", err)
	}
	assignment, err := repository.GetAssignment(context.Background(), activation.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	if assignment.State != AssignmentStateTerminated || assignment.TerminationNote != "mission task no longer exists" {
		t.Fatalf("assignment after deleted task activation = %+v", assignment)
	}
	if got := getActivationForTest(t, env, activation.ID); got.Status != ActivationStatusSuperseded {
		t.Fatalf("activation status = %q, want superseded", got.Status)
	}
}

func TestCoordinatorTerminatesResponsibilityWhenTaskDisappearsDuringReconcile(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	storedTasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, storedTasks, testTaskRecord("task-disappears", "mission-1", "agent-1", missiontask.TaskStatusAssigned))
	tasks := &disappearingTaskRepository{TaskRepositoryPort: storedTasks, disappearAfter: 1}
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-disappears", "agent-1")
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: &capturingResponsibilityDispatcher{}, Clock: env.Clock, IDGenerator: env.IDGenerator,
	})

	if _, err := coordinator.Reconcile(context.Background(), 10); err != nil {
		t.Fatalf("reconcile disappearing task: %v", err)
	}
	assignment, err := repository.GetAssignment(context.Background(), activation.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	if tasks.calls != 2 || assignment.State != AssignmentStateTerminated || assignment.TerminationNote != "mission task no longer exists" {
		t.Fatalf("disappearing task calls=%d assignment=%+v", tasks.calls, assignment)
	}
	if got := getActivationForTest(t, env, activation.ID); got.Status != ActivationStatusSuperseded {
		t.Fatalf("activation status = %q, want superseded", got.Status)
	}
}

func TestCoordinatorContinuesBatchAfterDeletedMissionTask(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-active", "mission-1", "agent-1", missiontask.TaskStatusAssigned))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	deleted := assignTestResponsibility(t, env, repository, "task-deleted-in-batch", "agent-1")
	active := assignTestResponsibility(t, env, repository, "task-active", "agent-1")
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	dispatcher := &capturingResponsibilityDispatcher{}
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: dispatcher, Clock: env.Clock, IDGenerator: env.IDGenerator,
	})

	stats, err := coordinator.Reconcile(context.Background(), 10)
	if err != nil {
		t.Fatalf("reconcile mixed task batch: %v", err)
	}
	deletedAssignment, err := repository.GetAssignment(context.Background(), deleted.ResponsibilityID)
	if err != nil {
		t.Fatal(err)
	}
	if deletedAssignment.State != AssignmentStateTerminated || getActivationForTest(t, env, deleted.ID).Status != ActivationStatusSuperseded {
		t.Fatalf("deleted assignment=%+v activation=%+v", deletedAssignment, getActivationForTest(t, env, deleted.ID))
	}
	if stats.Failed != 0 || stats.Dispatched != 1 || len(dispatcher.requests) != 1 || dispatcher.requests[0].RunID != RunIDForActivation(active.ID) {
		t.Fatalf("mixed batch stats=%+v requests=%+v", stats, dispatcher.requests)
	}
}

func TestCoordinatorSupersedesPermanentlyRejectedDispatch(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-rejected", "mission-1", "agent-1", missiontask.TaskStatusAssigned))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-rejected", "agent-1")
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	dispatcher := &capturingResponsibilityDispatcher{err: ErrDispatchRejected}
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: dispatcher, Clock: env.Clock, IDGenerator: env.IDGenerator,
	})
	if _, err := coordinator.Reconcile(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if got := getActivationForTest(t, env, activation.ID); got.Status != ActivationStatusSuperseded {
		t.Fatalf("activation status = %q, want superseded", got.Status)
	}
	if len(dispatcher.requests) != 1 {
		t.Fatalf("dispatch requests = %+v", dispatcher.requests)
	}
}

func TestCoordinatorNumbersResponsibilityRetryAttempts(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-retry-number", "mission-1", "agent-1", missiontask.TaskStatusAssigned))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	assignTestResponsibility(t, env, repository, "task-retry-number", "agent-1")
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock, RetryDelays: []time.Duration{0}})
	dispatcher := &capturingResponsibilityDispatcher{}
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: dispatcher, Clock: env.Clock, IDGenerator: env.IDGenerator,
	})

	if _, err := coordinator.Reconcile(context.Background(), 10); err != nil {
		t.Fatalf("reconcile first attempt: %v", err)
	}
	if len(dispatcher.requests) != 1 || dispatcher.requests[0].Attempt != 1 {
		t.Fatalf("first dispatch requests = %+v", dispatcher.requests)
	}
	if err := service.RecordRuntimeOutcome(context.Background(), RuntimeOutcomeInput{
		TenantID: "tenant-1", TaskID: "task-retry-number", RunID: dispatcher.requests[0].RunID,
		Outcome: AttemptOutcomeFailed, Error: errors.New("provider failed"),
	}); err != nil {
		t.Fatalf("record first failure: %v", err)
	}
	if _, err := coordinator.Reconcile(context.Background(), 10); err != nil {
		t.Fatalf("reconcile second attempt: %v", err)
	}
	if len(dispatcher.requests) != 2 || dispatcher.requests[1].Attempt != 2 {
		t.Fatalf("retry dispatch requests = %+v", dispatcher.requests)
	}
}

func TestCoordinatorRequiresDurableQAPlanBeforeWorkerDispatch(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	task := testTaskRecord("task-qa", "mission-1", "agent-1", missiontask.TaskStatusAssigned)
	task.Contract.QAEnabled = true
	task.Contract.QAPlanRequired = true
	task.Contract.QAPlanCompleted = false
	saveTaskRecord(t, env, tasks, task)
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	assignTestResponsibility(t, env, repository, "task-qa", "agent-1")
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	dispatcher := &capturingResponsibilityDispatcher{}
	profiles := staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
	}}
	withoutPlanner := NewCoordinator(CoordinatorOptions{Repository: repository, Service: service, Profiles: profiles, Dispatcher: dispatcher, Clock: env.Clock, IDGenerator: env.IDGenerator})
	if _, err := withoutPlanner.Reconcile(context.Background(), 10); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("missing QA plan coordinator error = %v, want ErrInvalidContract", err)
	}
	if len(dispatcher.requests) != 0 {
		t.Fatalf("worker dispatched without QA planner: %+v", dispatcher.requests)
	}

	plannerState := QAPlanStatePending
	planner := qaPlanCoordinatorFunc(func(_ context.Context, _ AssignmentRecord, record missiontask.TaskRecord) (QAPlanState, error) {
		if plannerState != QAPlanStateCompleted {
			return plannerState, nil
		}
		record.Contract.QAPlanCompleted = true
		record.Contract.QAPlanRef = "qa-plan-task-qa"
		record.Contract.QAPlanArtifactRef = agentcore.ArtifactRef{ID: "artifact-qa-plan", Kind: agentcore.ArtifactKindLog, URI: "agent-run://qa-plan/output"}
		saveTaskRecord(t, env, tasks, record)
		return QAPlanStateCompleted, nil
	})
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service, Profiles: profiles, Dispatcher: dispatcher, QAPlans: planner,
		Clock: env.Clock, IDGenerator: env.IDGenerator,
	})
	stats, err := coordinator.Reconcile(context.Background(), 10)
	if err != nil || stats.QABlocked != 1 || len(dispatcher.requests) != 0 {
		t.Fatalf("pending QA plan reconcile stats=%+v requests=%+v err=%v", stats, dispatcher.requests, err)
	}
	plannerState = QAPlanStateCompleted
	stats, err = coordinator.Reconcile(context.Background(), 10)
	if err != nil || stats.Dispatched != 1 || len(dispatcher.requests) != 1 {
		t.Fatalf("completed QA plan reconcile stats=%+v requests=%+v err=%v", stats, dispatcher.requests, err)
	}
}

func TestCoordinatorEscalatesParentWhenQAPlanExecutionEscalates(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	task := testTaskRecord("task-qa-escalated", "mission-1", "agent-1", missiontask.TaskStatusAssigned)
	task.Contract.QAEnabled = true
	task.Contract.QAPlanRequired = true
	task.Contract.QAPlanCompleted = false
	saveTaskRecord(t, env, tasks, task)
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	assignTestResponsibility(t, env, repository, "task-qa-escalated", "agent-1")
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: &capturingResponsibilityDispatcher{},
		QAPlans: qaPlanCoordinatorFunc(func(context.Context, AssignmentRecord, missiontask.TaskRecord) (QAPlanState, error) {
			return QAPlanStateEscalated, nil
		}),
		Clock: env.Clock, IDGenerator: env.IDGenerator,
	})
	stats, err := coordinator.Reconcile(context.Background(), 10)
	if err != nil || stats.QAEscalated != 1 {
		t.Fatalf("QA escalation stats=%+v err=%v", stats, err)
	}
	assignment, err := repository.GetAssignmentForWork(context.Background(), "tenant-1", WorkKindMissionTask, "task-qa-escalated")
	if err != nil || assignment.State != AssignmentStateEscalated {
		t.Fatalf("parent assignment after QA escalation = %+v err=%v", assignment, err)
	}
	ready, err := repository.ListReadyActivations(context.Background(), env.Clock.Now(), 10)
	if err != nil || len(ready) != 0 {
		t.Fatalf("QA-escalated activation remained ready: %+v err=%v", ready, err)
	}
}

func TestCoordinatorReconcilesRequiredQACheckWithoutRedispatchingWorker(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	record := testTaskRecord("task-qa-check", "mission-1", "agent-1", missiontask.TaskStatusInReview)
	record.Contract.QAEnabled = true
	record.Contract.QACheckRequired = true
	saveTaskRecord(t, env, tasks, record)
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	assignTestResponsibility(t, env, repository, "task-qa-check", "agent-1")
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	dispatcher := &capturingResponsibilityDispatcher{}
	state := QACheckStatePending
	checks := 0
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: dispatcher,
		QAChecks: qaCheckCoordinatorFunc(func(_ context.Context, _ AssignmentRecord, task missiontask.TaskRecord) (QACheckState, error) {
			checks++
			if task.Status != missiontask.TaskStatusInReview || !task.Contract.QACheckRequired {
				t.Fatalf("QA check task = %+v", task)
			}
			if state == QACheckStateCompleted {
				task.Status = missiontask.TaskStatusApproved
				task.Version++
				saveTaskRecord(t, env, tasks, task)
			}
			return state, nil
		}),
		Clock: env.Clock, IDGenerator: env.IDGenerator,
	})

	stats, err := coordinator.Reconcile(context.Background(), 10)
	if err != nil || stats.QABlocked != 1 || checks != 1 {
		t.Fatalf("pending QA check stats=%+v checks=%d err=%v", stats, checks, err)
	}
	if len(dispatcher.requests) != 0 {
		t.Fatalf("pending QA check redispatched worker: %+v", dispatcher.requests)
	}
	assignment, err := repository.GetAssignmentForWork(context.Background(), "tenant-1", WorkKindMissionTask, "task-qa-check")
	if err != nil || assignment.State != AssignmentStateSubmitted {
		t.Fatalf("pending parent assignment=%+v err=%v", assignment, err)
	}

	state = QACheckStateCompleted
	stats, err = coordinator.Reconcile(context.Background(), 10)
	if err != nil || stats.Synchronized == 0 || checks != 2 {
		t.Fatalf("completed QA check stats=%+v checks=%d err=%v", stats, checks, err)
	}
	assignment, err = repository.GetAssignmentForWork(context.Background(), "tenant-1", WorkKindMissionTask, "task-qa-check")
	if err != nil || assignment.State != AssignmentStateFulfilled {
		t.Fatalf("completed parent assignment=%+v err=%v", assignment, err)
	}
	if len(dispatcher.requests) != 0 {
		t.Fatalf("completed QA check redispatched worker: %+v", dispatcher.requests)
	}
}

func TestCoordinatorEscalatesSubmittedParentWhenQACheckEscalates(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	record := testTaskRecord("task-qa-check-escalated", "mission-1", "agent-1", missiontask.TaskStatusInReview)
	record.Contract.QAEnabled = true
	record.Contract.QACheckRequired = true
	saveTaskRecord(t, env, tasks, record)
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	assignTestResponsibility(t, env, repository, "task-qa-check-escalated", "agent-1")
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	dispatcher := &capturingResponsibilityDispatcher{}
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: dispatcher,
		QAChecks: qaCheckCoordinatorFunc(func(context.Context, AssignmentRecord, missiontask.TaskRecord) (QACheckState, error) {
			return QACheckStateEscalated, nil
		}),
		Clock: env.Clock, IDGenerator: env.IDGenerator,
	})
	stats, err := coordinator.Reconcile(context.Background(), 10)
	if err != nil || stats.QAEscalated != 1 {
		t.Fatalf("escalated QA check stats=%+v err=%v", stats, err)
	}
	assignment, err := repository.GetAssignmentForWork(context.Background(), "tenant-1", WorkKindMissionTask, "task-qa-check-escalated")
	if err != nil || assignment.State != AssignmentStateEscalated {
		t.Fatalf("escalated parent assignment=%+v err=%v", assignment, err)
	}
	if len(dispatcher.requests) != 0 {
		t.Fatalf("escalated QA check redispatched worker: %+v", dispatcher.requests)
	}
}

func TestRuntimeCompletionRequiresTaskApprovalAndReviewRejectionReactivates(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	record := testTaskRecord("task-1", "mission-1", "agent-1", missiontask.TaskStatusAssigned)
	saveTaskRecord(t, env, tasks, record)
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-1", "agent-1")
	claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim", env.Clock.Now(), time.Minute, 1)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
	if err != nil {
		t.Fatalf("mark dispatched: %v", err)
	}
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	record.Status = missiontask.TaskStatusInReview
	saveTaskRecord(t, env, tasks, record)
	if err := service.RecordRuntimeOutcome(context.Background(), RuntimeOutcomeInput{TenantID: "tenant-1", TaskID: "task-1", RunID: attempt.AgentRunID, Outcome: AttemptOutcomeCompleted}); err != nil {
		t.Fatalf("record completion: %v", err)
	}
	assignment, err := repository.GetAssignmentForWork(context.Background(), "tenant-1", WorkKindMissionTask, "task-1")
	if err != nil {
		t.Fatalf("get assignment: %v", err)
	}
	if assignment.State != AssignmentStateSubmitted {
		t.Fatalf("state after runtime completion = %s, want submitted", assignment.State)
	}
	record.Status = missiontask.TaskStatusReworkRequested
	saveTaskRecord(t, env, tasks, record)
	if err := service.ObserveTaskReview(context.Background(), TaskReviewSignal{TenantID: "tenant-1", TaskID: "task-1", Approved: false}); err != nil {
		t.Fatalf("observe rejection: %v", err)
	}
	ready, err := repository.ListReadyActivations(context.Background(), env.Clock.Now(), 10)
	if err != nil {
		t.Fatalf("list rework activation: %v", err)
	}
	if len(ready) != 1 || ready[0].Cause != ActivationCauseReviewRejected {
		t.Fatalf("rework activations = %+v", ready)
	}
	record.Status = missiontask.TaskStatusApproved
	saveTaskRecord(t, env, tasks, record)
	if err := service.ObserveTaskReview(context.Background(), TaskReviewSignal{TenantID: "tenant-1", TaskID: "task-1", Approved: true}); err != nil {
		t.Fatalf("observe approval: %v", err)
	}
	assignment, err = repository.GetAssignment(context.Background(), assignment.ID)
	if err != nil || assignment.State != AssignmentStateFulfilled {
		t.Fatalf("fulfilled assignment=%+v err=%v", assignment, err)
	}
}

func TestTaskReviewSignalCannotBypassDurableTaskReviewState(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	record := testTaskRecord("task-review-fact", "mission-1", "agent-1", missiontask.TaskStatusInReview)
	saveTaskRecord(t, env, tasks, record)
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	assignTestResponsibility(t, env, repository, record.Contract.ID.String(), "agent-1")
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})

	if err := service.ObserveTaskReview(context.Background(), TaskReviewSignal{TenantID: "tenant-1", TaskID: record.Contract.ID.String(), Approved: true}); !errors.Is(err, ErrConflict) {
		t.Fatalf("premature approval error = %v, want ErrConflict", err)
	}
	if err := service.ObserveTaskReview(context.Background(), TaskReviewSignal{TenantID: "tenant-1", TaskID: record.Contract.ID.String(), Approved: false}); !errors.Is(err, ErrConflict) {
		t.Fatalf("premature rejection error = %v, want ErrConflict", err)
	}
	assignment, err := repository.GetAssignmentForWork(context.Background(), "tenant-1", WorkKindMissionTask, record.Contract.ID.String())
	if err != nil || assignment.State != AssignmentStateOffered || assignment.Version != 1 {
		t.Fatalf("assignment changed from unmatched review signal: %+v err=%v", assignment, err)
	}
}

func TestCoordinatorEscalatesCompletedRuntimeWithoutDurableTaskSubmission(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	record := testTaskRecord("task-missing-submission", "mission-1", "agent-1", missiontask.TaskStatusInProgress)
	saveTaskRecord(t, env, tasks, record)
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, record.Contract.ID.String(), "agent-1")
	claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim-missing-submission", env.Clock.Now(), time.Minute, 1)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, claimed.ClaimToken, RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
	if err != nil {
		t.Fatalf("mark dispatched: %v", err)
	}
	if _, err := repository.CompleteAttempt(context.Background(), attempt.ID, AttemptOutcomeCompleted, nil, env.Clock.Now()); err != nil {
		t.Fatalf("complete attempt: %v", err)
	}
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service, Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: &capturingResponsibilityDispatcher{}, Clock: env.Clock, WorkerID: "worker",
	})
	stats, err := coordinator.Reconcile(context.Background(), 10)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	assignment, err := repository.GetAssignmentForWork(context.Background(), "tenant-1", WorkKindMissionTask, record.Contract.ID.String())
	if err != nil || assignment.State != AssignmentStateEscalated || stats.Synchronized != 1 {
		t.Fatalf("assignment=%+v stats=%+v err=%v", assignment, stats, err)
	}
}

func TestCoordinatorRecoversSubmittedTaskWhenRuntimeCallbackWasLost(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	record := testTaskRecord("task-recovery", "mission-1", "agent-1", missiontask.TaskStatusAssigned)
	saveTaskRecord(t, env, tasks, record)
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-recovery", "agent-1")
	claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim-recovery", env.Clock.Now(), time.Minute, 1)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim-recovery", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
	if err != nil {
		t.Fatalf("mark dispatched: %v", err)
	}
	record.Status = missiontask.TaskStatusInReview
	saveTaskRecord(t, env, tasks, record)
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	dispatcher := &capturingResponsibilityDispatcher{}
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: dispatcher, Clock: env.Clock, IDGenerator: env.IDGenerator,
	})
	stats, err := coordinator.Reconcile(context.Background(), 10)
	if err != nil {
		t.Fatalf("reconcile recovery: %v", err)
	}
	if stats.Synchronized != 1 || len(dispatcher.requests) != 0 {
		t.Fatalf("recovery stats=%+v requests=%+v", stats, dispatcher.requests)
	}
	stored, err := repository.FindAttemptByRun(context.Background(), "tenant-1", attempt.AgentRunID)
	if err != nil || stored.Outcome != AttemptOutcomeCompleted {
		t.Fatalf("recovered attempt=%+v err=%v", stored, err)
	}
	assignment, err := repository.GetAssignmentForWork(context.Background(), "tenant-1", WorkKindMissionTask, "task-recovery")
	if err != nil || assignment.State != AssignmentStateSubmitted {
		t.Fatalf("recovered assignment=%+v err=%v", assignment, err)
	}
}

func TestCoordinatorSupersedesActivationWhenTaskIsAlreadyApproved(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-approved", "mission-1", "agent-1", missiontask.TaskStatusApproved))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	assignTestResponsibility(t, env, repository, "task-approved", "agent-1")
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	dispatcher := &capturingResponsibilityDispatcher{}
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: dispatcher, Clock: env.Clock, IDGenerator: env.IDGenerator,
	})
	if _, err := coordinator.Reconcile(context.Background(), 10); err != nil {
		t.Fatalf("reconcile approved task: %v", err)
	}
	ready, err := repository.ListReadyActivations(context.Background(), env.Clock.Now(), 10)
	if err != nil || len(ready) != 0 {
		t.Fatalf("ready activations after approved reconciliation = %+v err=%v", ready, err)
	}
	if len(dispatcher.requests) != 0 {
		t.Fatalf("approved task was dispatched: %+v", dispatcher.requests)
	}
}

func TestCoordinatorResolvesReworkAfterTaskReentersExecution(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-reworked", "mission-1", "agent-1", missiontask.TaskStatusAccepted))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	assignTestResponsibility(t, env, repository, "task-reworked", "agent-1")
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	rework := &recordingReworkCoordinator{}
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: &capturingResponsibilityDispatcher{}, Rework: rework,
		Clock: env.Clock, IDGenerator: env.IDGenerator,
	})
	if _, err := coordinator.Reconcile(context.Background(), 10); err != nil {
		t.Fatalf("reconcile accepted rework: %v", err)
	}
	if rework.resolveCalls != 1 || rework.prepareCalls != 0 || rework.taskID != "task-reworked" || rework.agentID != "agent-1" {
		t.Fatalf("rework coordinator calls = %+v", rework)
	}
}

func TestRuntimeFailureCreatesOneIdempotentRetryActivation(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-1", "mission-1", "agent-1", missiontask.TaskStatusAssigned))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-1", "agent-1")
	claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim", env.Clock.Now(), time.Minute, 1)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
	if err != nil {
		t.Fatalf("mark dispatched: %v", err)
	}
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock, RetryDelays: []time.Duration{0}})
	input := RuntimeOutcomeInput{TenantID: "tenant-1", TaskID: "task-1", RunID: attempt.AgentRunID, Outcome: AttemptOutcomeFailed, Error: errors.New("provider failed")}
	if err := service.RecordRuntimeOutcome(context.Background(), input); err != nil {
		t.Fatalf("record failure: %v", err)
	}
	if err := service.RecordRuntimeOutcome(context.Background(), input); err != nil {
		t.Fatalf("replay failure: %v", err)
	}
	ready, err := repository.ListReadyActivations(context.Background(), env.Clock.Now(), 10)
	if err != nil {
		t.Fatalf("list retries: %v", err)
	}
	if len(ready) != 1 || ready[0].Cause != ActivationCauseRetryDue {
		t.Fatalf("retry activations = %+v", ready)
	}
}

func TestRuntimeOutcomeBeforeMarkDispatchedIsReplayedAndRetried(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-early-outcome", "mission-1", "agent-1", missiontask.TaskStatusAssigned))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	assignTestResponsibility(t, env, repository, "task-early-outcome", "agent-1")
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock, RetryDelays: []time.Duration{0}})
	dispatcher := &runtimeOutcomeDuringDispatch{service: service, outcome: RuntimeOutcomeInput{
		TenantID: "tenant-1", TaskID: "task-early-outcome", Outcome: AttemptOutcomeFailed, Error: errors.New("failed immediately"),
	}}
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: dispatcher, Clock: env.Clock, IDGenerator: env.IDGenerator,
	})
	if _, err := coordinator.Reconcile(context.Background(), 10); err != nil {
		t.Fatalf("reconcile early runtime outcome: %v", err)
	}
	attempt, err := repository.FindAttemptByRun(context.Background(), "tenant-1", dispatcher.runID)
	if err != nil {
		t.Fatal(err)
	}
	if attempt.Outcome != AttemptOutcomeFailed {
		t.Fatalf("replayed attempt outcome = %q", attempt.Outcome)
	}
	ready, err := repository.ListReadyActivations(context.Background(), env.Clock.Now(), 10)
	if err != nil || len(ready) != 1 || ready[0].Cause != ActivationCauseRetryDue {
		t.Fatalf("early outcome retry activations = %+v err=%v", ready, err)
	}
}

func TestRuntimeOutcomeReplayRepairsCrashAfterAttemptCompletion(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-crash-recovery", "mission-1", "agent-1", missiontask.TaskStatusAssigned))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-crash-recovery", "agent-1")
	claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim", env.Clock.Now(), time.Minute, 1)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CompleteAttempt(context.Background(), attempt.ID, AttemptOutcomeFailed, errors.New("provider failed"), env.Clock.Now()); err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock, RetryDelays: []time.Duration{0}})
	if err := service.RecordRuntimeOutcome(context.Background(), RuntimeOutcomeInput{
		TenantID: "tenant-1", TaskID: "task-crash-recovery", RunID: attempt.AgentRunID, Outcome: AttemptOutcomeFailed, Error: errors.New("provider failed"),
	}); err != nil {
		t.Fatalf("replay after completion crash: %v", err)
	}
	ready, err := repository.ListReadyActivations(context.Background(), env.Clock.Now(), 10)
	if err != nil || len(ready) != 1 || ready[0].Cause != ActivationCauseRetryDue {
		t.Fatalf("recovered retry activations = %+v err=%v", ready, err)
	}
}

func TestCoordinatorRecoversFailedRuntimeOutcomeFromDurableMissionFact(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-runtime-fact", "mission-1", "agent-1", missiontask.TaskStatusAssigned))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-runtime-fact", "agent-1")
	claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim", env.Clock.Now(), time.Minute, 1)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceOptions{
		Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock,
		RetryDelays: []time.Duration{time.Hour},
	})
	source := &staticRuntimeOutcomeSource{fact: RuntimeOutcomeFact{
		TenantID: "tenant-1", TaskID: "task-runtime-fact", RunID: attempt.AgentRunID,
		Outcome: AttemptOutcomeFailed, Error: "provider failed",
	}}
	coordinator := NewCoordinator(CoordinatorOptions{
		Repository: repository, Service: service,
		Profiles: staticResponsibilityProfiles{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
			"agent-1": {ID: "agent-1", Enabled: true, RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex}},
		}},
		Dispatcher: &capturingResponsibilityDispatcher{}, RuntimeOutcomes: source,
		Clock: env.Clock, IDGenerator: env.IDGenerator,
	})
	stats, err := coordinator.Reconcile(context.Background(), 10)
	if err != nil || stats.RecoveredOutcomes != 1 {
		t.Fatalf("reconcile stats=%+v err=%v", stats, err)
	}
	stored, err := repository.FindAttemptByRun(context.Background(), "tenant-1", attempt.AgentRunID)
	if err != nil || stored.Outcome != AttemptOutcomeFailed || stored.Error != "provider failed" {
		t.Fatalf("recovered attempt=%+v err=%v", stored, err)
	}
	ready, err := repository.ListReadyActivations(context.Background(), env.Clock.Now().Add(time.Hour), 10)
	if err != nil || len(ready) != 1 || ready[0].Cause != ActivationCauseRetryDue {
		t.Fatalf("recovered retry activations=%+v err=%v", ready, err)
	}
}

func TestOldRuntimeOutcomeCannotCreateRetryAfterNewerActivation(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-old-callback", "mission-1", "agent-1", missiontask.TaskStatusAssigned))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-old-callback", "agent-1")
	claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim", env.Clock.Now(), time.Minute, 1)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock, RetryDelays: []time.Duration{0}})
	input := RuntimeOutcomeInput{TenantID: "tenant-1", TaskID: "task-old-callback", RunID: attempt.AgentRunID, Outcome: AttemptOutcomeFailed}
	if err := service.RecordRuntimeOutcome(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	assignmentBefore, err := repository.GetAssignmentForWork(context.Background(), "tenant-1", WorkKindMissionTask, "task-old-callback")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.RecordRuntimeOutcome(context.Background(), input); err != nil {
		t.Fatal(err)
	}
	assignmentAfter, err := repository.GetAssignmentForWork(context.Background(), "tenant-1", WorkKindMissionTask, "task-old-callback")
	if err != nil {
		t.Fatal(err)
	}
	if assignmentAfter.Version != assignmentBefore.Version {
		t.Fatalf("old callback advanced assignment version: before=%d after=%d", assignmentBefore.Version, assignmentAfter.Version)
	}
	ready, err := repository.ListReadyActivations(context.Background(), env.Clock.Now(), 10)
	if err != nil || len(ready) != 1 {
		t.Fatalf("old callback created duplicate retries: %+v err=%v", ready, err)
	}
}

func TestRuntimeOutcomeRejectsConflictingTerminalResults(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-conflict", "mission-1", "agent-1", missiontask.TaskStatusAssigned))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-conflict", "agent-1")
	claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim", env.Clock.Now(), time.Minute, 1)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock, RetryDelays: []time.Duration{time.Hour}})
	if err := service.RecordRuntimeOutcome(context.Background(), RuntimeOutcomeInput{TenantID: "tenant-1", TaskID: "task-conflict", RunID: attempt.AgentRunID, Outcome: AttemptOutcomeFailed}); err != nil {
		t.Fatal(err)
	}
	err = service.RecordRuntimeOutcome(context.Background(), RuntimeOutcomeInput{TenantID: "tenant-1", TaskID: "task-conflict", RunID: attempt.AgentRunID, Outcome: AttemptOutcomeCompleted})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting runtime outcome error = %v, want ErrConflict", err)
	}
	stored, err := repository.FindAttemptByRun(context.Background(), "tenant-1", attempt.AgentRunID)
	if err != nil || stored.Outcome != AttemptOutcomeFailed {
		t.Fatalf("conflicting callback changed attempt: %+v err=%v", stored, err)
	}
}

func TestRuntimeOutcomeRejectsMismatchedTaskIdentity(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	tasks := missiontask.NewPlatformRepository()
	saveTaskRecord(t, env, tasks, testTaskRecord("task-1", "mission-1", "agent-1", missiontask.TaskStatusAssigned))
	repository := NewRepository(env.DBRouter, env.UnitOfWork)
	activation := assignTestResponsibility(t, env, repository, "task-1", "agent-1")
	claimed, ok, err := repository.ClaimActivation(context.Background(), activation.ID, "worker", "claim", env.Clock.Now(), time.Minute, 1)
	if err != nil || !ok {
		t.Fatalf("claim ok=%v err=%v", ok, err)
	}
	attempt, err := repository.MarkDispatched(context.Background(), claimed.ID, "claim", RunIDForActivation(claimed.ID), "codex", env.Clock.Now())
	if err != nil {
		t.Fatalf("mark dispatched: %v", err)
	}
	service := NewService(ServiceOptions{Repository: repository, UnitOfWork: env.UnitOfWork, Tasks: tasks, Clock: env.Clock})
	err = service.RecordRuntimeOutcome(context.Background(), RuntimeOutcomeInput{TenantID: "tenant-1", TaskID: "task-other", RunID: attempt.AgentRunID, Outcome: AttemptOutcomeCompleted})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("mismatched task outcome error = %v, want ErrConflict", err)
	}
	stored, err := repository.FindAttemptByRun(context.Background(), "tenant-1", attempt.AgentRunID)
	if err != nil {
		t.Fatalf("find attempt: %v", err)
	}
	if stored.Outcome != "" {
		t.Fatalf("mismatched task outcome mutated the attempt: %+v", stored)
	}
}

func assignTestResponsibility(t *testing.T, env *testkit.Env, repository *Repository, taskID, agentID string) ActivationRecord {
	t.Helper()
	var activation ActivationRecord
	err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
		_, saved, err := repository.AssignMissionTaskTx(ctx, tx, MissionTaskAssignmentInput{
			TenantID: "tenant-1", MissionID: "mission-1", TaskID: taskID, AgentProfileID: agentID,
			Priority: 50, AcceptBy: env.Clock.Now().Add(time.Minute), ActivationCause: ActivationCauseAssigned,
		}, env.Clock.Now())
		activation = saved
		return err
	})
	if err != nil {
		t.Fatalf("assign responsibility: %v", err)
	}
	return activation
}

func getActivationForTest(t *testing.T, env *testkit.Env, activationID string) ActivationRecord {
	t.Helper()
	var out ActivationRecord
	err := env.UnitOfWork.Do(context.Background(), func(_ context.Context, tx uow.Tx) error {
		record, ok := tx.Get(activationTable, activationID)
		if !ok {
			return ErrNotFound
		}
		return decodeRecord(record, &out)
	})
	if err != nil {
		t.Fatalf("get activation: %v", err)
	}
	return out
}

func testTaskRecord(taskID, missionID, agentID string, status missiontask.TaskStatus) missiontask.TaskRecord {
	return missiontask.TaskRecord{
		Contract: missiontask.TaskContract{
			ID: missiontask.TaskID(taskID), MissionID: missioncore.MissionID(missionID),
			Assignee: missiontask.TaskAssigneeRef{AgentProfileRef: &agentcore.AgentProfileRef{ID: agentcore.AgentProfileID(agentID)}},
			Title:    taskID, Goal: taskID, WorkerPersonalGoal: taskID,
			ExpectedDeliverables: []missiontask.ExpectedDeliverable{{ID: missiontask.ExpectedDeliverableID("deliverable-" + taskID), Title: taskID}},
			QAPlanCompleted:      true,
		},
		Status: status, Version: 1, StatusChangedAt: time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC),
	}
}

func saveTaskRecord(t *testing.T, env *testkit.Env, repository missiontask.Repository, record missiontask.TaskRecord) {
	t.Helper()
	if err := env.UnitOfWork.Do(context.Background(), func(ctx context.Context, tx uow.Tx) error {
		return repository.SaveTask(ctx, tx, record)
	}); err != nil {
		t.Fatalf("save task %s: %v", record.Contract.ID, err)
	}
}

type staticResponsibilityProfiles struct {
	profiles         map[agentcore.AgentProfileID]agentcore.AgentProfile
	getProfileCalls  *int
	getProfilesCalls *int
}

type disappearingTaskRepository struct {
	TaskRepositoryPort
	calls          int
	disappearAfter int
}

func (r *disappearingTaskRepository) GetTask(ctx context.Context, tx uow.Tx, id missiontask.TaskID) (missiontask.TaskRecord, error) {
	r.calls++
	if r.calls > r.disappearAfter {
		return missiontask.TaskRecord{}, missiontask.ErrNotFound
	}
	return r.TaskRepositoryPort.GetTask(ctx, tx, id)
}

func (p staticResponsibilityProfiles) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	if p.getProfileCalls != nil {
		(*p.getProfileCalls)++
	}
	profile, ok := p.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, agentcore.ErrNotFound
	}
	return profile, nil
}

func (p staticResponsibilityProfiles) GetProfiles(_ context.Context, ids []agentcore.AgentProfileID) (map[agentcore.AgentProfileID]agentcore.AgentProfile, error) {
	if p.getProfilesCalls != nil {
		(*p.getProfilesCalls)++
	}
	profiles := make(map[agentcore.AgentProfileID]agentcore.AgentProfile, len(ids))
	for _, id := range ids {
		if profile, ok := p.profiles[id]; ok {
			profiles[id] = profile
		}
	}
	return profiles, nil
}

type capturingResponsibilityDispatcher struct {
	requests []RuntimeDispatchRequest
	err      error
}

type runtimeOutcomeDuringDispatch struct {
	service *Service
	outcome RuntimeOutcomeInput
	runID   string
}

type staticRuntimeOutcomeSource struct {
	fact RuntimeOutcomeFact
}

func (s *staticRuntimeOutcomeSource) FindTerminalRuntimeOutcome(_ context.Context, tenantID, missionID, taskID, runID string) (RuntimeOutcomeFact, bool, error) {
	if s == nil || s.fact.TenantID != tenantID || missionID != "mission-1" || s.fact.TaskID != taskID || s.fact.RunID != runID {
		return RuntimeOutcomeFact{}, false, nil
	}
	return s.fact, true, nil
}

type qaPlanCoordinatorFunc func(context.Context, AssignmentRecord, missiontask.TaskRecord) (QAPlanState, error)

type qaCheckCoordinatorFunc func(context.Context, AssignmentRecord, missiontask.TaskRecord) (QACheckState, error)

type recordingReworkCoordinator struct {
	prepareCalls int
	resolveCalls int
	taskID       string
	agentID      string
}

func (r *recordingReworkCoordinator) PrepareRework(_ context.Context, taskID, accountableAgent string) error {
	r.prepareCalls++
	r.taskID = taskID
	r.agentID = accountableAgent
	return nil
}

func (r *recordingReworkCoordinator) ResolveRework(_ context.Context, taskID, accountableAgent string) error {
	r.resolveCalls++
	r.taskID = taskID
	r.agentID = accountableAgent
	return nil
}

func (f qaPlanCoordinatorFunc) ReconcileQAPlan(ctx context.Context, assignment AssignmentRecord, task missiontask.TaskRecord) (QAPlanState, error) {
	return f(ctx, assignment, task)
}

func (f qaCheckCoordinatorFunc) ReconcileQACheck(ctx context.Context, assignment AssignmentRecord, task missiontask.TaskRecord) (QACheckState, error) {
	return f(ctx, assignment, task)
}

func (d *capturingResponsibilityDispatcher) DispatchResponsibility(_ context.Context, request RuntimeDispatchRequest) (string, error) {
	d.requests = append(d.requests, request)
	if d.err != nil {
		return "", d.err
	}
	return request.RunID, nil
}

func (d *runtimeOutcomeDuringDispatch) DispatchResponsibility(ctx context.Context, request RuntimeDispatchRequest) (string, error) {
	d.runID = request.RunID
	input := d.outcome
	input.RunID = request.RunID
	if err := d.service.RecordRuntimeOutcome(ctx, input); err != nil {
		return "", err
	}
	return request.RunID, nil
}
