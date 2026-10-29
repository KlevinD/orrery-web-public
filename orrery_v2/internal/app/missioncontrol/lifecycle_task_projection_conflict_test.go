package missioncontrol_test

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	projection "orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform/testkit"
)

func TestLifecycleServiceTaskCommandsRecoverProjectionConflicts(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	baseRepo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	repo := &conflictOnceProjectionRepository{base: baseRepo}
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentProfileID: "agent-1", AgentInstanceID: "agent-1", AgentKind: agentcore.AgentKindAI, Enabled: true},
		{AgentProfileID: "agent-2", AgentInstanceID: "agent-2", AgentKind: agentcore.AgentKindAI, Enabled: true},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, AgentDirectory: agents})
	viewer := missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}

	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-conflict-recovery", Title: "Launch", FinalGoal: "Ship v2"})
	if err != nil {
		t.Fatal(err)
	}
	repo.arm("concurrent-create-update")
	created, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey: "task-conflict-recovery", MissionID: missioncore.MissionID(mission.MissionID),
		AssigneeID: "agent-1", Goal: "Implement route", WorkerPersonalGoal: "Deliver working route",
	})
	if err != nil {
		t.Fatalf("CreateTask did not recover projection conflict: %v", err)
	}
	repo.arm("concurrent-assign-update")
	if _, err := service.AssignTask(ctx, viewer, missioncontrol.AssignTaskCommand{
		IdempotencyKey: "assign-conflict-recovery", MissionID: missioncore.MissionID(mission.MissionID),
		TaskID: missioncore.TaskID(created.TaskID), AssigneeID: "agent-2", Reason: "rebalance",
	}); err != nil {
		t.Fatalf("AssignTask did not recover projection conflict: %v", err)
	}

	model, err := baseRepo.GetMissionControl(ctx, missioncore.MissionID(mission.MissionID))
	if err != nil {
		t.Fatal(err)
	}
	if len(model.TaskBoard.Tasks) != 1 || model.TaskBoard.Tasks[0].Assignee != "agent-2" || model.TaskBoard.Tasks[0].Status != projection.TaskStatusAssigned {
		t.Fatalf("reconciled task projection = %+v", model.TaskBoard.Tasks)
	}
	for _, marker := range []string{"concurrent-create-update", "concurrent-assign-update"} {
		if !containsString(model.ProgressGraph.CurrentFocus, marker) {
			t.Fatalf("concurrent projection update %q was lost: %+v", marker, model.ProgressGraph.CurrentFocus)
		}
	}
}

type conflictOnceProjectionRepository struct {
	base   *missioncontrol.DBProjectionRepository
	marker string
}

func (r *conflictOnceProjectionRepository) arm(marker string) {
	r.marker = marker
}

func (r *conflictOnceProjectionRepository) GetMissionControl(ctx context.Context, missionID missioncore.MissionID) (projection.ReadModel, error) {
	return r.base.GetMissionControl(ctx, missionID)
}

func (r *conflictOnceProjectionRepository) Save(ctx context.Context, model projection.ReadModel) error {
	if r.marker != "" {
		marker := r.marker
		r.marker = ""
		current, err := r.base.GetMissionControl(ctx, model.Mission.MissionID)
		if err != nil {
			return err
		}
		current.ProgressGraph.CurrentFocus = append(current.ProgressGraph.CurrentFocus, marker)
		current.Checkpoint.Version++
		if err := r.base.Save(ctx, current); err != nil {
			return err
		}
	}
	return r.base.Save(ctx, model)
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
