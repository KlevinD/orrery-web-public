package main

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/responsibility"
	schedulerapp "orrery_v2/internal/app/scheduler"
	"orrery_v2/internal/platform/testkit"
)

func TestProductionResponsibilityBatchScheduleCreatesOneAndArchivesLegacyTasks(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	store := &fakePeopleBatchScheduler{tasks: map[string]schedulerapp.ScheduledTask{}}
	for index, agentID := range []string{"agent-1", "agent-2"} {
		portfolio := testProductionPeoplePortfolio()
		portfolio.ID += string(rune('0' + index + 1))
		portfolio.BindingID += string(rune('0' + index + 1))
		portfolio.AgentProfileID = agentID
		portfolio.SubjectID += string(rune('0' + index + 1))
		portfolio.SourceDigest += string(rune('0' + index + 1))
		portfolio.PulseScheduledTaskID = "legacy-" + agentID
		if _, _, err := repository.SavePeopleResponsibilityPortfolio(ctx, portfolio, env.Clock.Now().UTC()); err != nil {
			t.Fatal(err)
		}
		store.tasks[portfolio.PulseScheduledTaskID] = testLegacyResponsibilityTask(portfolio)
	}
	manager := productionResponsibilityBatchSchedule{
		scheduler: store, tasks: store, portfolios: repository, orreryTenantID: "tenant-1", peopleTenantID: "people-tenant",
		cron: "0 9 * * *", timezone: "Asia/Shanghai",
	}
	task, err := manager.EnsureAndMigrate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if store.createCalls != 1 || store.cancelCalls != 2 || !task.Enabled || task.Status != schedulerapp.TaskStatusEnabled ||
		task.Target.SourceScopeKind != schedulerapp.ResponsibilityEvaluationPeoplePortfoliosScope || task.Target.SourceScopeID != "people-tenant" ||
		task.Target.AgentID != "" || task.Schedule.Cron != "0 9 * * *" || task.Schedule.Timezone != "Asia/Shanghai" {
		t.Fatalf("task=%+v create=%d cancel=%d", task, store.createCalls, store.cancelCalls)
	}
	for _, legacyID := range []string{"legacy-agent-1", "legacy-agent-2"} {
		legacy := store.tasks[legacyID]
		if legacy.Enabled || legacy.Status != schedulerapp.TaskStatusArchived {
			t.Fatalf("legacy task %s = %+v", legacyID, legacy)
		}
	}
	reused, err := manager.EnsureAndMigrate(ctx)
	if err != nil || reused.ScheduledTaskID != task.ScheduledTaskID || store.createCalls != 1 || store.cancelCalls != 2 {
		t.Fatalf("reused=%+v err=%v create=%d cancel=%d", reused, err, store.createCalls, store.cancelCalls)
	}
}

func TestProductionResponsibilityBatchScheduleFailsClosedForUnrecognizedPointer(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	portfolio := testProductionPeoplePortfolio()
	portfolio.PulseScheduledTaskID = "unrelated-task"
	if _, _, err := repository.SavePeopleResponsibilityPortfolio(ctx, portfolio, env.Clock.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	store := &fakePeopleBatchScheduler{tasks: map[string]schedulerapp.ScheduledTask{
		"unrelated-task": {
			TenantID: "tenant-1", ScheduledTaskID: "unrelated-task", Title: "Unrelated work",
			Target:  schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: "agent-1", TaskGoal: "Other work", Prompt: "Do other work"},
			Enabled: true, Status: schedulerapp.TaskStatusEnabled,
		},
	}}
	manager := productionResponsibilityBatchSchedule{
		scheduler: store, tasks: store, portfolios: repository, orreryTenantID: "tenant-1", peopleTenantID: "people-tenant",
		cron: "0 9 * * *", timezone: "Asia/Shanghai",
	}
	if _, err := manager.EnsureAndMigrate(ctx); !errors.Is(err, schedulerapp.ErrInvalidRequest) {
		t.Fatalf("error = %v, want ErrInvalidRequest", err)
	}
	if store.cancelCalls != 0 || !store.tasks["unrelated-task"].Enabled {
		t.Fatalf("unrelated task changed: %+v", store.tasks["unrelated-task"])
	}
}

func TestProductionResponsibilityBatchScheduleSecondEnsurePreservesScheduleState(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	store := &fakePeopleBatchScheduler{tasks: map[string]schedulerapp.ScheduledTask{}}
	manager := productionResponsibilityBatchSchedule{
		scheduler: store, tasks: store, portfolios: responsibility.NewRepository(env.DBRouter, env.UnitOfWork),
		orreryTenantID: "tenant-1", peopleTenantID: "people-tenant", cron: "0 9 * * *", timezone: "Asia/Shanghai",
	}

	created, err := manager.EnsureAndMigrate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	nextRunAt := time.Date(2026, 9, 1, 1, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 8, 31, 3, 42, 11, 0, time.UTC)
	created.NextRunAt = nextRunAt
	created.UpdatedAt = updatedAt
	store.tasks[created.ScheduledTaskID] = created
	updateCalls := store.updateCalls

	reused, err := manager.EnsureAndMigrate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if store.createCalls != 1 || store.updateCalls != updateCalls {
		t.Fatalf("create=%d update=%d, want create=1 update=%d", store.createCalls, store.updateCalls, updateCalls)
	}
	if !reused.NextRunAt.Equal(nextRunAt) || !reused.UpdatedAt.Equal(updatedAt) {
		t.Fatalf("reused schedule state next=%s updated=%s, want next=%s updated=%s", reused.NextRunAt, reused.UpdatedAt, nextRunAt, updatedAt)
	}
}

func TestProductionResponsibilityBatchScheduleSecondEnsureIsNoOpWithRealSchedulerService(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	tasks := schedulerapp.NewRepository(env.DBRouter)
	service := schedulerapp.NewService(schedulerapp.ServiceOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator, Repository: tasks,
	})
	manager := productionResponsibilityBatchSchedule{
		scheduler: service, tasks: tasks, portfolios: responsibility.NewRepository(env.DBRouter, env.UnitOfWork),
		orreryTenantID: "tenant-1", peopleTenantID: "people-tenant", cron: "0 9 * * *", timezone: "Asia/Shanghai",
	}

	created, err := manager.EnsureAndMigrate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !productionResponsibilityBatchTaskMatches(created, manager.createRequest(true)) {
		t.Fatalf("real scheduler task is not canonical: task target=%#v execution=%#v delivery=%#v; request target=%#v execution=%#v delivery=%#v",
			created.Target, created.Execution, created.Delivery, manager.createRequest(true).Target, manager.createRequest(true).Execution, manager.createRequest(true).Delivery)
	}
	env.Clock.Advance(time.Hour)
	reused, err := manager.EnsureAndMigrate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reused.NextRunAt.Equal(created.NextRunAt) || !reused.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("second ensure changed schedule state: created next=%s updated=%s, reused next=%s updated=%s",
			created.NextRunAt, created.UpdatedAt, reused.NextRunAt, reused.UpdatedAt)
	}
}

func TestProductionResponsibilityBatchScheduleDoesNotReviveArchivedBatch(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	store := &fakePeopleBatchScheduler{tasks: map[string]schedulerapp.ScheduledTask{}}
	manager := productionResponsibilityBatchSchedule{
		scheduler: store, tasks: store, portfolios: responsibility.NewRepository(env.DBRouter, env.UnitOfWork),
		orreryTenantID: "tenant-1", peopleTenantID: "people-tenant", cron: "0 9 * * *", timezone: "Asia/Shanghai",
	}

	task, err := manager.EnsureAndMigrate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	task.Enabled = false
	task.Status = schedulerapp.TaskStatusArchived
	store.tasks[task.ScheduledTaskID] = task
	updateCalls := store.updateCalls

	reused, err := manager.EnsureAndMigrate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if reused.Enabled || reused.Status != schedulerapp.TaskStatusArchived || store.updateCalls != updateCalls {
		t.Fatalf("reused=%+v update=%d, want archived and update=%d", reused, store.updateCalls, updateCalls)
	}
}

func TestProductionResponsibilityBatchScheduleDisableDisablesBatchAndArchivesLegacy(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	portfolio := testProductionPeoplePortfolio()
	portfolio.PulseScheduledTaskID = "legacy-agent-1"
	if _, _, err := repository.SavePeopleResponsibilityPortfolio(ctx, portfolio, env.Clock.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	store := &fakePeopleBatchScheduler{tasks: map[string]schedulerapp.ScheduledTask{}}
	manager := productionResponsibilityBatchSchedule{
		scheduler: store, tasks: store, portfolios: repository,
		orreryTenantID: "tenant-1", peopleTenantID: "people-tenant", cron: "0 9 * * *", timezone: "Asia/Shanghai",
	}
	batch := testProductionResponsibilityBatchTask(manager, "batch-task")
	oldManager := manager
	oldManager.peopleTenantID = "people-old"
	oldBatch := testProductionResponsibilityBatchTask(oldManager, "old-batch-task")
	store.tasks[batch.ScheduledTaskID] = batch
	store.tasks[oldBatch.ScheduledTaskID] = oldBatch
	store.tasks[portfolio.PulseScheduledTaskID] = testLegacyResponsibilityTask(portfolio)

	if err := manager.DisableAndArchiveLegacy(ctx); err != nil {
		t.Fatal(err)
	}
	disabled := store.tasks[batch.ScheduledTaskID]
	disabledOld := store.tasks[oldBatch.ScheduledTaskID]
	legacy := store.tasks[portfolio.PulseScheduledTaskID]
	if disabled.Enabled || disabled.Status != schedulerapp.TaskStatusDisabled || disabledOld.Enabled || disabledOld.Status != schedulerapp.TaskStatusDisabled ||
		legacy.Enabled || legacy.Status != schedulerapp.TaskStatusArchived {
		t.Fatalf("batch=%+v old batch=%+v legacy=%+v", disabled, disabledOld, legacy)
	}
	if store.updateCalls != 2 || store.cancelCalls != 1 {
		t.Fatalf("update=%d cancel=%d, want 2 updates and 1 archive", store.updateCalls, store.cancelCalls)
	}
}

func TestProductionResponsibilityBatchScheduleIgnoresOtherTenantLegacy(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	current := testProductionPeoplePortfolio()
	current.PulseScheduledTaskID = "current-legacy"
	foreign := testProductionPeoplePortfolio()
	foreign.ID = "foreign-portfolio"
	foreign.TenantID = "tenant-2"
	foreign.BindingID = "foreign-binding"
	foreign.SubjectID = "foreign-subject"
	foreign.AgentProfileID = "foreign-agent"
	foreign.SourceDigest = "foreign-digest"
	foreign.PulseScheduledTaskID = "foreign-legacy"
	for _, portfolio := range []responsibility.PeopleResponsibilityPortfolio{current, foreign} {
		if _, _, err := repository.SavePeopleResponsibilityPortfolio(ctx, portfolio, env.Clock.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	store := &fakePeopleBatchScheduler{tasks: map[string]schedulerapp.ScheduledTask{
		current.PulseScheduledTaskID: testLegacyResponsibilityTask(current),
		foreign.PulseScheduledTaskID: testLegacyResponsibilityTask(foreign),
	}}
	manager := productionResponsibilityBatchSchedule{
		scheduler: store, tasks: store, portfolios: repository,
		orreryTenantID: "tenant-1", peopleTenantID: "people-tenant", cron: "0 9 * * *", timezone: "Asia/Shanghai",
	}

	if _, err := manager.EnsureAndMigrate(ctx); err != nil {
		t.Fatal(err)
	}
	if currentTask := store.tasks[current.PulseScheduledTaskID]; currentTask.Enabled || currentTask.Status != schedulerapp.TaskStatusArchived {
		t.Fatalf("current legacy = %+v, want archived", currentTask)
	}
	if foreignTask := store.tasks[foreign.PulseScheduledTaskID]; !foreignTask.Enabled || foreignTask.Status != schedulerapp.TaskStatusEnabled {
		t.Fatalf("foreign legacy = %+v, want unchanged", foreignTask)
	}
	if store.cancelCalls != 1 {
		t.Fatalf("cancel=%d, want only current tenant legacy canceled", store.cancelCalls)
	}
}

func TestProductionResponsibilityBatchScheduleArchivesPreAdoptionAgentTask(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	portfolio := testProductionPeoplePortfolio()
	portfolio.PulseScheduledTaskID = "pre-adoption-legacy"
	if _, _, err := repository.SavePeopleResponsibilityPortfolio(ctx, portfolio, env.Clock.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	legacy := schedulerapp.ScheduledTask{
		TenantID: portfolio.TenantID, ScheduledTaskID: portfolio.PulseScheduledTaskID,
		Title: "Responsibility pulse: " + portfolio.AgentProfileID,
		Target: schedulerapp.Target{
			Mode: schedulerapp.TargetModeAgentTask, AgentID: portfolio.AgentProfileID,
			TaskGoal: "Evaluate current responsibility evidence", Prompt: "Review the responsibility portfolio.",
		},
		Enabled: true, Status: schedulerapp.TaskStatusEnabled,
		CreatedBy:     "system:people-responsibility:" + portfolio.BindingID + ":" + portfolio.AgentProfileID,
		CreatedByKind: schedulerapp.ViewerKindSystem, CreatorProfileID: portfolio.AgentProfileID,
	}
	store := &fakePeopleBatchScheduler{tasks: map[string]schedulerapp.ScheduledTask{legacy.ScheduledTaskID: legacy}}
	manager := productionResponsibilityBatchSchedule{
		scheduler: store, tasks: store, portfolios: repository,
		orreryTenantID: "tenant-1", peopleTenantID: "people-tenant", cron: "0 9 * * *", timezone: "Asia/Shanghai",
	}

	if _, err := manager.EnsureAndMigrate(ctx); err != nil {
		t.Fatal(err)
	}
	archived := store.tasks[legacy.ScheduledTaskID]
	if archived.Enabled || archived.Status != schedulerapp.TaskStatusArchived || store.cancelCalls != 1 {
		t.Fatalf("legacy=%+v cancel=%d, want archived once", archived, store.cancelCalls)
	}
}

func TestProductionResponsibilityBatchScheduleMigrationDoesNotInvokeLegacyMissionLifecycle(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	portfolios := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	portfolio := testProductionPeoplePortfolio()
	portfolio.PulseScheduledTaskID = "pre-adoption-real-service"
	if _, _, err := portfolios.SavePeopleResponsibilityPortfolio(ctx, portfolio, env.Clock.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	tasks := schedulerapp.NewRepository(env.DBRouter)
	legacy := schedulerapp.ScheduledTask{
		TenantID: portfolio.TenantID, ScheduledTaskID: portfolio.PulseScheduledTaskID, Title: "Responsibility pulse: " + portfolio.AgentProfileID,
		Target:  schedulerapp.Target{Mode: schedulerapp.TargetModeAgentTask, AgentID: portfolio.AgentProfileID, TaskGoal: "Evaluate responsibilities", Prompt: "Evaluate responsibilities"},
		Enabled: true, Status: schedulerapp.TaskStatusEnabled,
		CreatedBy:     "system:people-responsibility:" + portfolio.BindingID + ":" + portfolio.AgentProfileID,
		CreatedByKind: schedulerapp.ViewerKindSystem, CreatorProfileID: portfolio.AgentProfileID,
	}
	if err := tasks.SaveTask(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	lifecycle := &recordingPeopleMigrationMissionLifecycle{err: errors.New("Mission service unavailable")}
	service := schedulerapp.NewService(schedulerapp.ServiceOptions{
		Clock: env.Clock, IDGenerator: env.IDGenerator, Repository: tasks, MissionLifecycle: lifecycle,
	})
	manager := productionResponsibilityBatchSchedule{
		scheduler: service, tasks: tasks, portfolios: portfolios,
		orreryTenantID: portfolio.TenantID, peopleTenantID: portfolio.PeopleTenantID, cron: "0 9 * * *", timezone: "Asia/Shanghai",
	}

	if _, err := manager.EnsureAndMigrate(ctx); err != nil {
		t.Fatal(err)
	}
	if lifecycle.calls != 0 {
		t.Fatalf("Mission lifecycle calls = %d, want 0", lifecycle.calls)
	}
	archived, err := tasks.GetTask(ctx, legacy.ScheduledTaskID)
	if err != nil {
		t.Fatal(err)
	}
	if archived.Enabled || archived.Status != schedulerapp.TaskStatusArchived || archived.MissionID != "" {
		t.Fatalf("archived legacy task = %+v", archived)
	}
}

func TestProductionResponsibilityBatchScheduleTenantSwitchArchivesOldScope(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	oldPortfolio := testProductionPeoplePortfolio()
	oldPortfolio.PeopleTenantID = "people-old"
	oldPortfolio.PulseScheduledTaskID = "old-legacy"
	if _, _, err := repository.SavePeopleResponsibilityPortfolio(ctx, oldPortfolio, env.Clock.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	store := &fakePeopleBatchScheduler{tasks: map[string]schedulerapp.ScheduledTask{}}
	oldManager := productionResponsibilityBatchSchedule{orreryTenantID: "tenant-1", peopleTenantID: "people-old", cron: "0 9 * * *", timezone: "Asia/Shanghai"}
	oldBatch := testProductionResponsibilityBatchTask(oldManager, "old-batch")
	store.tasks[oldBatch.ScheduledTaskID] = oldBatch
	store.tasks[oldPortfolio.PulseScheduledTaskID] = testLegacyResponsibilityTask(oldPortfolio)
	manager := productionResponsibilityBatchSchedule{
		scheduler: store, tasks: store, portfolios: repository,
		orreryTenantID: "tenant-1", peopleTenantID: "people-new", cron: "0 9 * * *", timezone: "Asia/Shanghai",
	}

	created, err := manager.EnsureAndMigrate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !created.Enabled || created.Status != schedulerapp.TaskStatusEnabled || created.Target.SourceScopeID != "people-new" {
		t.Fatalf("new batch = %+v", created)
	}
	for _, taskID := range []string{oldBatch.ScheduledTaskID, oldPortfolio.PulseScheduledTaskID} {
		old := store.tasks[taskID]
		if old.Enabled || old.Status != schedulerapp.TaskStatusArchived {
			t.Fatalf("old schedule %s = %+v, want archived", taskID, old)
		}
	}
}

func TestProductionResponsibilityBatchScheduleDisabledWithoutTenantIDsStopsAllManagedSchedules(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repository := responsibility.NewRepository(env.DBRouter, env.UnitOfWork)
	portfolio := testProductionPeoplePortfolio()
	portfolio.PulseScheduledTaskID = "legacy-task"
	if _, _, err := repository.SavePeopleResponsibilityPortfolio(ctx, portfolio, env.Clock.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	configured := productionResponsibilityBatchSchedule{orreryTenantID: portfolio.TenantID, peopleTenantID: portfolio.PeopleTenantID}
	batch := testProductionResponsibilityBatchTask(configured, "batch-task")
	legacy := testLegacyResponsibilityTask(portfolio)
	unrelated := legacy
	unrelated.ScheduledTaskID = "unrelated-task"
	unrelated.CreatedBy = "system:unrelated"
	store := &fakePeopleBatchScheduler{tasks: map[string]schedulerapp.ScheduledTask{
		batch.ScheduledTaskID: batch, legacy.ScheduledTaskID: legacy, unrelated.ScheduledTaskID: unrelated,
	}}
	manager := productionResponsibilityBatchSchedule{scheduler: store, tasks: store, portfolios: repository}

	if err := manager.DisableAndArchiveLegacy(ctx); err != nil {
		t.Fatal(err)
	}
	if stopped := store.tasks[batch.ScheduledTaskID]; stopped.Enabled || stopped.Status != schedulerapp.TaskStatusDisabled {
		t.Fatalf("batch = %+v, want disabled", stopped)
	}
	if stopped := store.tasks[legacy.ScheduledTaskID]; stopped.Enabled || stopped.Status != schedulerapp.TaskStatusArchived {
		t.Fatalf("legacy = %+v, want archived", stopped)
	}
	if got := store.tasks[unrelated.ScheduledTaskID]; !got.Enabled || got.Status != schedulerapp.TaskStatusEnabled {
		t.Fatalf("unrelated = %+v, want unchanged", got)
	}
}

func TestCanonicalPeopleResponsibilityAgentBindingsUsesEmployeeProfileIDs(t *testing.T) {
	profiles := &productionPeopleResponsibilityProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"H000002": {
			ID: "H000002", AgentKind: agentcore.AgentKindHuman,
			PersonSource: agentcore.PersonSourceFacts{
				EmployeeNumber: "H000002", Source: "people", SourceRecordID: "member-quzong", SourceTenantID: "tenant-preview",
			},
		},
		"gangbeng": {ID: "gangbeng", AgentKind: agentcore.AgentKindAI},
	}}

	bindings, err := canonicalPeopleResponsibilityAgentBindings(context.Background(), profiles, "tenant-preview", map[string]string{
		"member-quzong":        "human-quzheng",
		"member-openclaw-main": "gangbeng",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := bindings["member-quzong"]; got != "H000002" {
		t.Fatalf("human binding = %q, want H000002", got)
	}
	if got := bindings["member-openclaw-main"]; got != "A000001" {
		t.Fatalf("AI binding = %q, want A000001", got)
	}
}

func TestCanonicalPeopleResponsibilityAgentBindingsRejectsMismatchedEmployeeID(t *testing.T) {
	profiles := &productionPeopleResponsibilityProfileDirectory{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"human-quzheng": {
			ID: "human-quzheng", AgentKind: agentcore.AgentKindHuman,
			PersonSource: agentcore.PersonSourceFacts{
				EmployeeNumber: "H000002", Source: "people", SourceRecordID: "member-quzong", SourceTenantID: "tenant-preview",
			},
		},
	}}

	if _, err := canonicalPeopleResponsibilityAgentBindings(context.Background(), profiles, "tenant-preview", map[string]string{
		"member-quzong": "human-quzheng",
	}); err == nil {
		t.Fatal("expected mismatched employee profile id to fail")
	}
}

func TestProductionPeopleResponsibilityRoleSyncGrantsAndRevokesOnlyManagedRoles(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := defaultHumanAgentProfile("H000002", "Qu Zheng")
	profile.PersonSource = agentcore.PersonSourceFacts{
		EmployeeNumber: "H000002", Source: "people", SourceRecordID: "member-quzong", SourceTenantID: "tenant-preview",
	}
	profile.OrgIdentity.RoleRefs = []agentcore.RoleRef{{ID: "member"}, {ID: "basic.user"}}
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatalf("seed profile: %v", err)
	}
	roleSync := productionPeopleResponsibilityRoleSync{profiles: profiles}
	input := responsibility.PeopleResponsibilityRoleSyncInput{
		TenantID: "system", PeopleTenantID: "tenant-preview", SubjectID: "member-quzong", AgentProfileID: "H000002",
		SourceRevision: "7:3", ManagedRoleIDs: []string{"approval.reviewer"}, DesiredRoleIDs: []string{"approval.reviewer"},
	}

	changed, err := roleSync.SyncPeopleResponsibilityRoles(ctx, input)
	if err != nil || !changed {
		t.Fatalf("grant changed=%v err=%v", changed, err)
	}
	stored, err := profiles.GetProfile(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertPeopleRoleIDs(t, stored.OrgIdentity.RoleRefs, []string{"member", "basic.user", "approval.reviewer"})
	if stored.DisplayName != profile.DisplayName || stored.PersonSource.EmployeeNumber != profile.PersonSource.EmployeeNumber {
		t.Fatalf("unrelated profile fields changed: %+v", stored)
	}
	if changed, err := roleSync.SyncPeopleResponsibilityRoles(ctx, input); err != nil || changed {
		t.Fatalf("idempotent grant changed=%v err=%v", changed, err)
	}

	input.DesiredRoleIDs = nil
	changed, err = roleSync.SyncPeopleResponsibilityRoles(ctx, input)
	if err != nil || !changed {
		t.Fatalf("revoke changed=%v err=%v", changed, err)
	}
	stored, err = profiles.GetProfile(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertPeopleRoleIDs(t, stored.OrgIdentity.RoleRefs, []string{"member", "basic.user"})
}

func TestProductionPeopleResponsibilityRoleSyncRejectsWrongProfileSourceAndUnmanagedDesiredRole(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	profile := defaultHumanAgentProfile("H000002", "Qu Zheng")
	profile.PersonSource = agentcore.PersonSourceFacts{
		EmployeeNumber: "H000002", Source: "people", SourceRecordID: "member-quzong", SourceTenantID: "tenant-preview",
	}
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	roleSync := productionPeopleResponsibilityRoleSync{profiles: profiles}
	input := responsibility.PeopleResponsibilityRoleSyncInput{
		TenantID: "system", PeopleTenantID: "tenant-preview", SubjectID: "different-subject", AgentProfileID: "H000002",
		ManagedRoleIDs: []string{"approval.reviewer"}, DesiredRoleIDs: []string{"approval.reviewer"},
	}
	if changed, err := roleSync.SyncPeopleResponsibilityRoles(ctx, input); err == nil || changed {
		t.Fatalf("source mismatch changed=%v err=%v", changed, err)
	}
	input.SubjectID = "member-quzong"
	input.DesiredRoleIDs = []string{"system.admin"}
	if changed, err := roleSync.SyncPeopleResponsibilityRoles(ctx, input); !errors.Is(err, responsibility.ErrInvalidContract) || changed {
		t.Fatalf("unmanaged desired role changed=%v err=%v", changed, err)
	}
	stored, err := profiles.GetProfile(ctx, profile.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertPeopleRoleIDs(t, stored.OrgIdentity.RoleRefs, []string{"realhumancontributor"})
}

func TestProductionPeopleResponsibilityRoleSyncSkipsNonHumanWithoutMappedRole(t *testing.T) {
	profiles := &productionPeopleResponsibilityProfileMutator{profile: agentcore.AgentProfile{ID: "A000001", AgentKind: agentcore.AgentKindAI}}
	roleSync := productionPeopleResponsibilityRoleSync{profiles: profiles}
	input := responsibility.PeopleResponsibilityRoleSyncInput{
		TenantID: "system", PeopleTenantID: "tenant-preview", SubjectID: "member-agent", AgentProfileID: "A000001",
		ManagedRoleIDs: []string{"approval.reviewer"}, DesiredRoleIDs: nil,
	}
	if changed, err := roleSync.SyncPeopleResponsibilityRoles(context.Background(), input); err != nil || changed {
		t.Fatalf("non-human empty mapping changed=%v err=%v", changed, err)
	}
	input.DesiredRoleIDs = []string{"approval.reviewer"}
	if changed, err := roleSync.SyncPeopleResponsibilityRoles(context.Background(), input); err == nil || changed {
		t.Fatalf("non-human mapped role changed=%v err=%v", changed, err)
	}
}

func assertPeopleRoleIDs(t *testing.T, refs []agentcore.RoleRef, expected []string) {
	t.Helper()
	if len(refs) != len(expected) {
		t.Fatalf("role refs = %+v, want %v", refs, expected)
	}
	for index := range refs {
		if string(refs[index].ID) != expected[index] {
			t.Fatalf("role refs = %+v, want %v", refs, expected)
		}
	}
}

type fakePeopleBatchScheduler struct {
	tasks       map[string]schedulerapp.ScheduledTask
	createCalls int
	updateCalls int
	cancelCalls int
}

type recordingPeopleMigrationMissionLifecycle struct {
	calls int
	err   error
}

func (l *recordingPeopleMigrationMissionLifecycle) EnsureScheduledMission(context.Context, schedulerapp.ScheduledTask) (string, error) {
	l.calls++
	return "", l.err
}

type productionPeopleResponsibilityProfileDirectory struct {
	profiles map[agentcore.AgentProfileID]agentcore.AgentProfile
}

type productionPeopleResponsibilityProfileMutator struct {
	profile agentcore.AgentProfile
}

func (m *productionPeopleResponsibilityProfileMutator) MutateProfile(_ context.Context, id agentcore.AgentProfileID, mutation agentcore.ProfileMutation) (agentcore.AgentProfile, bool, error) {
	if m.profile.ID != id {
		return agentcore.AgentProfile{}, false, agentcore.ErrNotFound
	}
	updated, changed, err := mutation(m.profile)
	if err == nil && changed {
		m.profile = updated
	}
	return updated, changed, err
}

func (d *productionPeopleResponsibilityProfileDirectory) GetProfile(_ context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error) {
	profile, ok := d.profiles[id]
	if !ok {
		return agentcore.AgentProfile{}, agentcore.ErrNotFound
	}
	return profile, nil
}

func (d *productionPeopleResponsibilityProfileDirectory) QueryDirectory(_ context.Context, query agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error) {
	entries := make([]agentcore.AgentDirectoryEntry, 0, len(d.profiles))
	for _, profile := range d.profiles {
		if query.AgentKind != "" && profile.AgentKind != query.AgentKind {
			continue
		}
		entries = append(entries, agentcore.AgentDirectoryEntry{AgentProfileID: profile.ID, AgentKind: profile.AgentKind})
	}
	start := query.Page.Offset
	if start > len(entries) {
		start = len(entries)
	}
	end := start + query.Page.Limit
	if end > len(entries) {
		end = len(entries)
	}
	return agentcore.AgentDirectoryPage{Entries: entries[start:end], Total: len(entries), Limit: query.Page.Limit, Offset: query.Page.Offset}, nil
}

func (s *fakePeopleBatchScheduler) CreateTask(_ context.Context, viewer schedulerapp.ViewerRef, request schedulerapp.CreateTaskRequest) (schedulerapp.ScheduledTask, error) {
	s.createCalls++
	id := "pulse-schedule-" + string(rune('0'+s.createCalls))
	enabled := request.Enabled == nil || *request.Enabled
	status := schedulerapp.TaskStatusEnabled
	if !enabled {
		status = schedulerapp.TaskStatusDisabled
	}
	task := schedulerapp.ScheduledTask{
		TenantID: viewer.TenantID, ScheduledTaskID: id, Title: request.Title, Description: request.Description,
		Schedule: request.Schedule, Target: request.Target, Policy: request.Policy, Execution: request.Execution,
		Delivery: request.Delivery, Enabled: enabled, Status: status, CreatedBy: viewer.ViewerID,
		CreatedByKind: viewer.ViewerKind, CreatedByName: viewer.DisplayName, CreatorProfileID: viewer.ProfileID,
		IdempotencyKey: request.IdempotencyKey,
	}
	s.tasks[id] = task
	return task, nil
}

func (s *fakePeopleBatchScheduler) GetTask(_ context.Context, scheduledTaskID string) (schedulerapp.ScheduledTask, error) {
	task, ok := s.tasks[scheduledTaskID]
	if !ok {
		return schedulerapp.ScheduledTask{}, schedulerapp.ErrNotFound
	}
	return task, nil
}

func (s *fakePeopleBatchScheduler) UpdateTask(_ context.Context, _ schedulerapp.ViewerRef, request schedulerapp.UpdateTaskRequest) (schedulerapp.ScheduledTask, error) {
	task, ok := s.tasks[request.ScheduledTaskID]
	if !ok {
		return schedulerapp.ScheduledTask{}, schedulerapp.ErrNotFound
	}
	s.updateCalls++
	if request.Title != nil {
		task.Title = *request.Title
	}
	if request.Description != nil {
		task.Description = *request.Description
	}
	if request.Schedule != nil {
		task.Schedule = *request.Schedule
	}
	if request.Target != nil {
		previousMode := task.Target.Mode
		task.Target = *request.Target
		if previousMode == schedulerapp.TargetModeAgentTask && task.Target.Mode == schedulerapp.TargetModeResponsibilityEvaluation {
			task.MissionID = ""
		}
	}
	if request.Policy != nil {
		task.Policy = *request.Policy
	}
	if request.Execution != nil {
		task.Execution = *request.Execution
	}
	if request.Delivery != nil {
		task.Delivery = *request.Delivery
	}
	if request.Enabled != nil {
		task.Enabled = *request.Enabled
		if task.Enabled {
			task.Status = schedulerapp.TaskStatusEnabled
		} else {
			task.Status = schedulerapp.TaskStatusDisabled
		}
	}
	s.tasks[task.ScheduledTaskID] = task
	return task, nil
}

func (s *fakePeopleBatchScheduler) FindTaskByIdempotency(_ context.Context, tenantID, createdBy, idempotencyKey string) (schedulerapp.ScheduledTask, bool, error) {
	for _, task := range s.tasks {
		if task.TenantID == tenantID && task.CreatedBy == createdBy && task.IdempotencyKey == idempotencyKey {
			return task, true, nil
		}
	}
	return schedulerapp.ScheduledTask{}, false, nil
}

func (s *fakePeopleBatchScheduler) ListTasks(_ context.Context, _ bool) ([]schedulerapp.ScheduledTask, error) {
	tasks := make([]schedulerapp.ScheduledTask, 0, len(s.tasks))
	for _, task := range s.tasks {
		tasks = append(tasks, task)
	}
	return tasks, nil
}

func (s *fakePeopleBatchScheduler) CancelTask(_ context.Context, _ schedulerapp.ViewerRef, request schedulerapp.CancelTaskRequest) (schedulerapp.ScheduledTask, error) {
	task, ok := s.tasks[request.ScheduledTaskID]
	if !ok {
		return schedulerapp.ScheduledTask{}, schedulerapp.ErrNotFound
	}
	s.cancelCalls++
	task.Enabled = false
	task.Status = schedulerapp.TaskStatusArchived
	s.tasks[task.ScheduledTaskID] = task
	return task, nil
}

func (s *fakePeopleBatchScheduler) ArchiveSystemManagedTask(ctx context.Context, viewer schedulerapp.ViewerRef, request schedulerapp.CancelTaskRequest) (schedulerapp.ScheduledTask, error) {
	return s.CancelTask(ctx, viewer, request)
}

func testProductionPeoplePortfolio() responsibility.PeopleResponsibilityPortfolio {
	return responsibility.PeopleResponsibilityPortfolio{
		ID: "portfolio-1", TenantID: "tenant-1", BindingID: "binding-1", PeopleTenantID: "people-tenant",
		SubjectID: "subject-1", AgentProfileID: "agent-1", SourceRevision: "7:3", SourceDigest: "digest-1",
		Responsibilities: []responsibility.PeopleResolvedResponsibility{{
			ID: "responsibility-1", Code: "RESP-1", Title: "Operate service", Revision: 1,
			Description: "Summary: Keep the service healthy.\nEvidence expectations:\n- Verification result",
		}},
	}
}

func testProductionResponsibilityBatchTask(manager productionResponsibilityBatchSchedule, scheduledTaskID string) schedulerapp.ScheduledTask {
	request := manager.createRequest(true)
	viewer := manager.viewer()
	return schedulerapp.ScheduledTask{
		TenantID: viewer.TenantID, ScheduledTaskID: scheduledTaskID, Title: request.Title, Description: request.Description,
		Schedule: request.Schedule, Target: request.Target, Policy: request.Policy, Execution: request.Execution,
		Delivery: request.Delivery, Enabled: true, Status: schedulerapp.TaskStatusEnabled,
		CreatedBy: viewer.ViewerID, CreatedByKind: viewer.ViewerKind, CreatedByName: viewer.DisplayName,
		CreatorProfileID: viewer.ProfileID, IdempotencyKey: request.IdempotencyKey,
	}
}

func testLegacyResponsibilityTask(portfolio responsibility.PeopleResponsibilityPortfolio) schedulerapp.ScheduledTask {
	return schedulerapp.ScheduledTask{
		TenantID: portfolio.TenantID, ScheduledTaskID: portfolio.PulseScheduledTaskID,
		Title: "Responsibility evaluation: " + portfolio.AgentProfileID,
		Target: schedulerapp.Target{
			Mode: schedulerapp.TargetModeResponsibilityEvaluation, AgentID: portfolio.AgentProfileID,
			SourceScopeKind: schedulerapp.ResponsibilityEvaluationPeoplePortfolioScope, SourceScopeID: portfolio.BindingID,
		},
		Enabled: true, Status: schedulerapp.TaskStatusEnabled,
		CreatedBy:     "system:people-responsibility:" + portfolio.BindingID + ":" + portfolio.AgentProfileID,
		CreatedByKind: schedulerapp.ViewerKindSystem, CreatorProfileID: portfolio.AgentProfileID,
		IdempotencyKey: peopleResponsibilityPulseIdempotencyKey(portfolio),
	}
}
