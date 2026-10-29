package main

import (
	"context"
	"errors"
	"testing"

	"orrery_v2/internal/app/missioncontrol"
)

type schedulerMissionCommandsFake struct {
	mission     missioncontrol.MissionDTO
	createCalls int
	syncCalls   int
}

func (f *schedulerMissionCommandsFake) CreateMission(context.Context, missioncontrol.ViewerRef, missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error) {
	f.createCalls++
	return f.mission, nil
}

func (f *schedulerMissionCommandsFake) SyncSchedulerMission(context.Context, missioncontrol.ViewerRef, missioncontrol.SyncSchedulerMissionCommand) (missioncontrol.MissionDTO, error) {
	f.syncCalls++
	return f.mission, nil
}

func (f *schedulerMissionCommandsFake) CreateTask(context.Context, missioncontrol.ViewerRef, missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error) {
	return missioncontrol.TaskCardDTO{}, nil
}

func (f *schedulerMissionCommandsFake) AssignTask(context.Context, missioncontrol.ViewerRef, missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error) {
	return missioncontrol.CommandReceiptDTO{}, nil
}

func TestProductionSchedulerMissionCommandsEnsuresCanonicalConversationAfterCreateAndSync(t *testing.T) {
	next := &schedulerMissionCommandsFake{mission: missioncontrol.MissionDTO{MissionID: "mission-scheduled-1"}}
	var ensured []string
	commands := productionSchedulerMissionCommands{
		next: next,
		ensureConversation: func(_ context.Context, _ missioncontrol.ViewerRef, mission missioncontrol.MissionDTO) error {
			ensured = append(ensured, mission.MissionID)
			return nil
		},
	}
	viewer := missioncontrol.ViewerRef{ViewerID: "scheduler-agent-task", ViewerKind: missioncontrol.ViewerKindSystem, TenantID: "tenant-a"}

	if _, err := commands.CreateMission(context.Background(), viewer, missioncontrol.CreateMissionCommand{}); err != nil {
		t.Fatalf("create Mission: %v", err)
	}
	if _, err := commands.SyncSchedulerMission(context.Background(), viewer, missioncontrol.SyncSchedulerMissionCommand{}); err != nil {
		t.Fatalf("sync Mission: %v", err)
	}
	if next.createCalls != 1 || next.syncCalls != 1 {
		t.Fatalf("delegate calls create=%d sync=%d", next.createCalls, next.syncCalls)
	}
	if len(ensured) != 2 || ensured[0] != "mission-scheduled-1" || ensured[1] != "mission-scheduled-1" {
		t.Fatalf("ensured Missions = %v", ensured)
	}
}

func TestProductionSchedulerMissionCommandsReturnsConversationError(t *testing.T) {
	wantErr := errors.New("ensure canonical conversation")
	commands := productionSchedulerMissionCommands{
		next: &schedulerMissionCommandsFake{mission: missioncontrol.MissionDTO{MissionID: "mission-scheduled-1"}},
		ensureConversation: func(context.Context, missioncontrol.ViewerRef, missioncontrol.MissionDTO) error {
			return wantErr
		},
	}

	_, err := commands.SyncSchedulerMission(context.Background(), missioncontrol.ViewerRef{}, missioncontrol.SyncSchedulerMissionCommand{})
	if !errors.Is(err, wantErr) {
		t.Fatalf("sync error = %v, want %v", err, wantErr)
	}
}
