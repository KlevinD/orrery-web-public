package missioncontrol_test

import (
	"context"
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	missiontask "orrery_v2/internal/mission/task"
)

func TestCommandServiceCreatesMissionTaskAndAssignsTaskThroughPorts(t *testing.T) {
	at := time.Date(2026, 6, 1, 19, 0, 0, 0, time.UTC)
	port := &fakeMissionTaskPort{
		mission: missioncontrol.MissionDTO{MissionID: "mission-1", Title: "Stage 10", FinalGoal: "Transport", Status: "active", CreatedAt: at},
		task:    missioncontrol.TaskCardDTO{TaskID: "task-1", Goal: "Build route", Status: "assigned", UpdatedAt: at},
		receipt: missioncontrol.CommandReceiptDTO{CommandID: "assign-1", MissionID: "mission-1", TaskID: "task-1", Status: "accepted", AcceptedAt: at},
	}
	service := newAuthorizedCommandServiceWithMissionPorts(nil, nil, nil, nil, port)

	mission, err := service.CreateMission(context.Background(), viewer(), missioncontrol.CreateMissionCommand{
		IdempotencyKey: "mission-key",
		Title:          "Stage 10",
		FinalGoal:      "Transport",
	})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}
	if mission.MissionID != "mission-1" || port.createMission.IdempotencyKey != "mission-key" {
		t.Fatalf("mission/command = %+v / %+v", mission, port.createMission)
	}

	task, err := service.CreateTask(context.Background(), viewer(), missioncontrol.CreateTaskCommand{
		IdempotencyKey: "task-key",
		MissionID:      "mission-1",
		TaskID:         "task-1",
		AssigneeID:     "agent-1",
		Goal:           "Build route",
	})
	if err != nil {
		t.Fatalf("CreateTask returned error: %v", err)
	}
	if task.TaskID != "task-1" || port.createTask.AssigneeID != "agent-1" {
		t.Fatalf("task/command = %+v / %+v", task, port.createTask)
	}

	receipt, err := service.AssignTask(context.Background(), viewer(), missioncontrol.AssignTaskCommand{
		IdempotencyKey: "assign-key",
		MissionID:      "mission-1",
		TaskID:         "task-1",
		AssigneeID:     "agent-2",
	})
	if err != nil {
		t.Fatalf("AssignTask returned error: %v", err)
	}
	if receipt.CommandID != "assign-1" || port.assignTask.AssigneeID != "agent-2" {
		t.Fatalf("receipt/command = %+v / %+v", receipt, port.assignTask)
	}

	updated, err := service.UpdateMissionBrief(context.Background(), viewer(), missioncontrol.UpdateMissionBriefCommand{
		IdempotencyKey: "mission-update-key",
		MissionID:      "mission-1",
		Title:          "Stage 10 adjusted",
		FinalGoal:      "Transport with clarified scope",
	})
	if err != nil {
		t.Fatalf("UpdateMissionBrief returned error: %v", err)
	}
	if updated.MissionID != "mission-1" || port.updateMission.IdempotencyKey != "mission-update-key" || port.updateMission.Title != "Stage 10 adjusted" {
		t.Fatalf("updated/command = %+v / %+v", updated, port.updateMission)
	}
}

func TestCommandServiceDefaultsInitialHumanToAuthorizationSubject(t *testing.T) {
	port := &fakeMissionTaskPort{mission: missioncontrol.MissionDTO{MissionID: "mission-1"}}
	service := newAuthorizedCommandServiceWithMissionPorts(nil, nil, nil, nil, port)
	viewer := missioncontrol.ViewerRef{
		ViewerID:               "orrery-pa-e2e-1786420622",
		ViewerKind:             missioncontrol.ViewerKindUser,
		AuthorizationSubjectID: "human-runtime-user",
	}

	if _, err := service.CreateMission(context.Background(), viewer, missioncontrol.CreateMissionCommand{IdempotencyKey: "mission-key"}); err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}
	if port.createMission.InitialHumanAgentID != "human-runtime-user" {
		t.Fatalf("initial human = %q, want canonical authorization subject", port.createMission.InitialHumanAgentID)
	}
}

func TestMissionQueryFacadeListsMissionsThroughCatalogPort(t *testing.T) {
	catalog := &fakeMissionCatalog{missions: []missioncontrol.MissionDTO{{MissionID: "mission-1", Title: "Stage 10"}}}
	service := newAuthorizedQueryServiceWithCatalog(nil, catalog)

	page, err := service.ListMissions(context.Background(), viewer(), missioncontrol.ListMissionsQuery{Limit: 20})
	if err != nil {
		t.Fatalf("ListMissions returned error: %v", err)
	}
	if len(page.Missions) != 1 || page.Missions[0].MissionID != "mission-1" || catalog.query.Limit != 20 {
		t.Fatalf("page/query = %+v / %+v", page, catalog.query)
	}
}

type fakeMissionTaskPort struct {
	mission       missioncontrol.MissionDTO
	task          missioncontrol.TaskCardDTO
	receipt       missioncontrol.CommandReceiptDTO
	assignViewer  missioncontrol.ViewerRef
	createMission missioncontrol.CreateMissionCommand
	updateMission missioncontrol.UpdateMissionBriefCommand
	createTask    missioncontrol.CreateTaskCommand
	assignTask    missioncontrol.AssignTaskCommand
}

func (f *fakeMissionTaskPort) CreateMission(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error) {
	f.createMission = cmd
	return f.mission, nil
}

func (f *fakeMissionTaskPort) UpdateMissionBrief(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.UpdateMissionBriefCommand) (missioncontrol.MissionDTO, error) {
	f.updateMission = cmd
	if f.mission.MissionID == "" {
		return f.mission, nil
	}
	f.mission.Title = cmd.Title
	f.mission.FinalGoal = cmd.FinalGoal
	return f.mission, nil
}

func (f *fakeMissionTaskPort) CreateTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error) {
	f.createTask = cmd
	return f.task, nil
}

func (f *fakeMissionTaskPort) AssignTask(ctx context.Context, viewer missioncontrol.ViewerRef, cmd missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.assignViewer = viewer
	f.assignTask = cmd
	return f.receipt, nil
}

type fakeMissionCatalog struct {
	missions []missioncontrol.MissionDTO
	query    missioncontrol.ListMissionsQuery
}

func (f *fakeMissionCatalog) ListMissions(ctx context.Context, viewer missioncontrol.ViewerRef, query missioncontrol.ListMissionsQuery) (missioncontrol.MissionListDTO, error) {
	f.query = query
	return missioncontrol.MissionListDTO{Missions: f.missions, Page: missioncontrol.PageDTO{Limit: query.Limit, Total: len(f.missions), Returned: len(f.missions)}}, nil
}

func minimalTaskContract() missiontask.TaskContract {
	return missiontask.TaskContract{
		ID:        "task-1",
		MissionID: "mission-1",
		Assignee:  missiontask.TaskAssigneeRef{AgentProfileRef: &agentcore.AgentProfileRef{ID: "agent-1"}},
		Goal:      "Build route",
	}
}

var _ = minimalTaskContract
var _ = missioncore.MissionID("")
