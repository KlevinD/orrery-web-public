package todo

import (
	"context"
	"errors"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	missionprojection "orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/testkit"
)

func TestTodoExecutionProgressProjectionIsDurableMonotonicAndPreservesSubmissionRevision(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	item, state := seedTodoMissionExecution(t, repo, "owner-a")
	binding, acquired, err := repo.BeginTodoMissionExecution(ctx, item, state, "owner-a", "claim-progress", todoTestTime)
	if err != nil || !acquired {
		t.Fatalf("begin execution binding=%+v acquired=%v err=%v", binding, acquired, err)
	}
	binding.MissionID = "mission-progress"
	binding.DevelopmentProjectID = "development-project-a"
	binding.MissionTaskIDs = []string{"task-1", "task-2", "task-3"}
	binding.Status = TodoMissionBindingStatusMaterialized
	binding.UpdatedAt = todoTestTime.Add(time.Minute)
	if err := repo.SaveTodoMissionBinding(ctx, binding); err != nil {
		t.Fatalf("save materialized binding: %v", err)
	}

	model := todoProgressTestMission(item.TenantID, item.TodoID, binding.MissionID, 3, todoTestTime.Add(2*time.Minute))
	projectedAt := todoTestTime.Add(3 * time.Minute)
	progress, applied, err := repo.ProjectTodoExecutionProgress(ctx, model, projectedAt)
	if err != nil || !applied {
		t.Fatalf("project Mission progress=%+v applied=%v err=%v", progress, applied, err)
	}
	if progress.MissionProjectionVersion != 3 || progress.TotalTaskCount != 3 || progress.ActiveTaskCount != 1 || progress.DoneTaskCount != 1 || progress.BlockedTaskCount != 1 || progress.PendingApprovalCount != 2 || progress.DeliveryRate != 1.0/3.0 {
		t.Fatalf("projected Mission counters = %+v", progress)
	}
	if len(progress.CurrentAgentIDs) != 2 || progress.CurrentAgentIDs[0] != "agent-active" || progress.CurrentAgentIDs[1] != "agent-blocked" {
		t.Fatalf("current Agent IDs = %+v", progress.CurrentAgentIDs)
	}

	duplicate, applied, err := repo.ProjectTodoExecutionProgress(ctx, model, projectedAt.Add(time.Minute))
	if err != nil || applied || duplicate.ProjectedAt != progress.ProjectedAt {
		t.Fatalf("duplicate projection=%+v applied=%v err=%v", duplicate, applied, err)
	}
	conflicting := model
	conflicting.Mission.Status = missionprojection.MissionStatusCancelled
	if _, _, err := repo.ProjectTodoExecutionProgress(ctx, conflicting, projectedAt.Add(time.Minute)); !errors.Is(err, ErrConflict) {
		t.Fatalf("same-version conflict error = %v, want ErrConflict", err)
	}
	older := model
	older.Checkpoint.Version = 2
	older.Mission.Status = missionprojection.MissionStatusCancelled
	stale, applied, err := repo.ProjectTodoExecutionProgress(ctx, older, projectedAt.Add(2*time.Minute))
	if err != nil || applied || stale.MissionProjectionVersion != 3 || stale.MissionStatus != string(missionprojection.MissionStatusActive) {
		t.Fatalf("stale projection=%+v applied=%v err=%v", stale, applied, err)
	}

	withSubmission, applied, err := repo.ProjectTodoCodeSubmissionProgress(ctx, item.TenantID, item.TodoID, binding.ExecutionAttempt, TodoCodeSubmissionProgress{
		SubmissionRevision: 4, CodeSubmissionStatus: "pr_open", PullRequestStatus: "checks_running", MergeStatus: "pending",
		ProjectedAt: projectedAt.Add(3 * time.Minute),
	})
	if err != nil || !applied || withSubmission.SubmissionRevision != 4 {
		t.Fatalf("submission projection=%+v applied=%v err=%v", withSubmission, applied, err)
	}
	newerMission := model
	newerMission.Checkpoint.Version = 4
	newerMission.UpdatedAt = projectedAt.Add(4 * time.Minute)
	merged, applied, err := repo.ProjectTodoExecutionProgress(ctx, newerMission, projectedAt.Add(5*time.Minute))
	if err != nil || !applied || merged.SubmissionRevision != 4 || merged.CodeSubmissionStatus != "pr_open" || merged.PullRequestStatus != "checks_running" || merged.MergeStatus != "pending" {
		t.Fatalf("Mission update lost newer submission state: projection=%+v applied=%v err=%v", merged, applied, err)
	}

	reopened := NewRepository(env.DBRouter)
	stored, err := reopened.GetTodoExecutionProgressProjection(ctx, item.TenantID, item.TodoID, binding.ExecutionAttempt)
	if err != nil || stored.MissionProjectionVersion != 4 || stored.SubmissionRevision != 4 {
		t.Fatalf("reopened durable projection=%+v err=%v", stored, err)
	}
}

func TestGeneralTodoExecutionCompletesFromApprovedMissionAndReopenCreatesNextAttempt(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	item, state := seedTodoMissionExecution(t, repo, "owner-a")
	binding, acquired, err := repo.BeginTodoMissionExecution(ctx, item, state, "owner-a", "claim-terminal", todoTestTime)
	if err != nil || !acquired {
		t.Fatalf("begin execution binding=%+v acquired=%v err=%v", binding, acquired, err)
	}
	binding.MissionID = "mission-terminal"
	binding.MissionTaskIDs = []string{"task-1", "task-2"}
	binding.Status = TodoMissionBindingStatusMaterialized
	binding.UpdatedAt = todoTestTime.Add(time.Minute)
	if err := repo.SaveTodoMissionBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	binding.Status = TodoMissionBindingStatusDispatching
	binding.UpdatedAt = todoTestTime.Add(2 * time.Minute)
	if err := repo.SaveTodoMissionBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}
	binding.Status = TodoMissionBindingStatusExecuting
	binding.UpdatedAt = todoTestTime.Add(3 * time.Minute)
	if err := repo.SaveTodoMissionBinding(ctx, binding); err != nil {
		t.Fatal(err)
	}

	model := todoProgressTestMission(item.TenantID, item.TodoID, binding.MissionID, 9, todoTestTime.Add(4*time.Minute))
	for index := range model.TaskBoard.Tasks {
		model.TaskBoard.Tasks[index].Status = missionprojection.TaskStatusApproved
		model.TaskBoard.Tasks[index].QAStatus = missionprojection.QAStatusApproved
		model.TaskBoard.Tasks[index].BlockerRefs = nil
	}
	model.TaskBoard.Tasks = model.TaskBoard.Tasks[:2]
	model.ApprovalRequests = nil
	model.Blockers = nil
	completedAt := todoTestTime.Add(5 * time.Minute)
	if _, applied, err := repo.ProjectTodoExecutionProgress(ctx, model, completedAt); err != nil || !applied {
		t.Fatalf("project terminal Mission applied=%v err=%v", applied, err)
	}
	terminalBinding, err := repo.GetTodoMissionBinding(ctx, item.TenantID, item.TodoID)
	if err != nil || terminalBinding.Status != TodoMissionBindingStatusExecuted {
		t.Fatalf("terminal binding=%+v err=%v", terminalBinding, err)
	}
	automation, err := repo.GetTodoAutomation(ctx, item.TenantID, item.TodoID)
	if err != nil || automation.Status != TodoAutomationStatusExecuted || !automation.ExecutedAt.Equal(completedAt) {
		t.Fatalf("terminal automation=%+v err=%v", automation, err)
	}
	attempts, err := repo.ListTodoExecutionAttempts(ctx, item.TenantID, item.TodoID)
	if err != nil || len(attempts) != 1 || attempts[0].Status != TodoMissionBindingStatusExecuted || !attempts[0].CompletedAt.Equal(completedAt) {
		t.Fatalf("terminal attempts=%+v err=%v", attempts, err)
	}
	if _, applied, err := repo.ProjectTodoExecutionProgress(ctx, model, completedAt.Add(time.Minute)); err != nil || applied {
		t.Fatalf("duplicate terminal projection applied=%v err=%v", applied, err)
	}

	service := NewService(ServiceOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, Repository: repo})
	viewer := testViewer(item.TenantID, "owner-a")
	planned, err := service.MoveTodo(ctx, viewer, MoveTodoRequest{TodoID: item.TodoID, Status: TodoStatusPlanned, Version: &item.Version})
	if err != nil {
		t.Fatal(err)
	}
	done, err := service.CompleteTodo(ctx, viewer, CompleteTodoRequest{TodoID: item.TodoID, Version: &planned.Version})
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := service.ReopenTodo(ctx, viewer, ReopenTodoRequest{TodoID: item.TodoID, Version: &done.Version})
	if err != nil {
		t.Fatal(err)
	}
	nextState := state
	nextState.ProposalID = "proposal-2"
	nextState.TodoVersion = reopened.Version
	nextState.Status = TodoAutomationStatusExecuting
	nextState.PlanDigest = "digest-2"
	nextState.ApprovedPlanDigest = "digest-2"
	nextState.ExecutedAt = time.Time{}
	nextState.UpdatedAt = completedAt.Add(2 * time.Minute)
	if err := repo.SaveTodoAutomation(ctx, nextState); err != nil {
		t.Fatal(err)
	}
	second, acquired, err := repo.BeginTodoMissionExecution(ctx, reopened, nextState, "owner-a", "claim-terminal-2", completedAt.Add(3*time.Minute))
	if err != nil || !acquired || second.ExecutionAttempt != 2 || second.BindingID == terminalBinding.BindingID {
		t.Fatalf("second attempt binding=%+v acquired=%v err=%v", second, acquired, err)
	}
}

func TestDevelopmentTodoDoesNotCompleteFromMissionTaskApprovalAlone(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	ctx := context.Background()
	repo := NewRepository(env.DBRouter)
	item, state := seedTodoMissionExecution(t, repo, "owner-a")
	expected := item.Version
	item.WorkKind = TodoWorkKindDevelopment
	item.DevelopmentProjectID = "development-project-1"
	item.ReviewerID = "owner-a"
	item.ReviewerName = "Owner A"
	item.Version++
	if err := repo.UpdateTodo(ctx, item, expected); err != nil {
		t.Fatal(err)
	}
	state.TodoVersion = item.Version
	if err := repo.SaveTodoAutomation(ctx, state); err != nil {
		t.Fatal(err)
	}
	item, err := repo.GetTodoForTenant(ctx, item.TenantID, item.TodoID)
	if err != nil {
		t.Fatal(err)
	}
	binding, acquired, err := repo.BeginTodoMissionExecution(ctx, item, state, "owner-a", "claim-development", todoTestTime)
	if err != nil || !acquired {
		t.Fatalf("begin development execution acquired=%v err=%v", acquired, err)
	}
	binding.MissionID = "mission-development-terminal-gate"
	binding.MissionTaskIDs = []string{"task-1"}
	for _, status := range []string{TodoMissionBindingStatusMaterialized, TodoMissionBindingStatusDispatching, TodoMissionBindingStatusExecuting} {
		binding.Status = status
		binding.UpdatedAt = binding.UpdatedAt.Add(time.Minute)
		if err := repo.SaveTodoMissionBinding(ctx, binding); err != nil {
			t.Fatal(err)
		}
	}
	model := todoProgressTestMission(item.TenantID, item.TodoID, binding.MissionID, 3, todoTestTime.Add(4*time.Minute))
	model.TaskBoard.Tasks = model.TaskBoard.Tasks[:1]
	model.TaskBoard.Tasks[0].Status = missionprojection.TaskStatusApproved
	model.TaskBoard.Tasks[0].QAStatus = missionprojection.QAStatusApproved
	if _, _, err := repo.ProjectTodoExecutionProgress(ctx, model, todoTestTime.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	current, err := repo.GetTodoMissionBinding(ctx, item.TenantID, item.TodoID)
	if err != nil || current.Status != TodoMissionBindingStatusExecuting {
		t.Fatalf("development binding completed before submission/merge: %+v err=%v", current, err)
	}
}

func todoProgressTestMission(tenantID, todoID, missionID string, version int, at time.Time) missionprojection.ReadModel {
	return missionprojection.ReadModel{
		TenantID: tenantID,
		Mission: missionprojection.MissionView{
			TenantID: tenantID, MissionID: missioncore.MissionID(missionID), Status: missionprojection.MissionStatusActive,
			OriginKind: "todo", OriginTodoID: todoID,
		},
		TaskBoard: missionprojection.TaskBoardView{Tasks: []missionprojection.TaskCardView{
			{TaskID: "task-1", Assignee: agentcore.AgentID("agent-active"), Status: missionprojection.TaskStatusStarted, UpdatedAt: at},
			{TaskID: "task-2", Assignee: agentcore.AgentID("agent-done"), Status: missionprojection.TaskStatusApproved, UpdatedAt: at},
			{TaskID: "task-3", Assignee: agentcore.AgentID("agent-blocked"), Status: missionprojection.TaskStatusBlocked, BlockerRefs: []string{"blocker-1"}, QAStatus: missionprojection.QAStatusRequested, UpdatedAt: at},
		}},
		ApprovalRequests: []missionprojection.ApprovalRequestView{{RequestID: "approval-1", Status: "pending", CreatedAt: at}},
		Blockers:         []missionprojection.BlockerView{{BlockerID: "blocker-1", TaskID: "task-3", RaisedAt: at}},
		UpdatedAt:        at,
		Checkpoint:       missionprojection.Checkpoint{MissionID: missioncore.MissionID(missionID), Version: version, UpdatedAt: at},
	}
}
