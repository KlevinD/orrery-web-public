package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestProductionResponsibilityAutoplanMissionMaterializerStopsAtFailedCommand(t *testing.T) {
	plan, step := testResponsibilityAutoplanRecord(t)
	missionFailure := errors.New("mission command failed")
	taskFailure := errors.New("task command failed")
	assignmentFailure := errors.New("assignment command failed")

	testCases := []struct {
		name            string
		commands        *responsibilityAutoplanFailureCommands
		wantMissionCall int
		wantTaskCall    int
		wantAssignCall  int
		wantErrorText   string
	}{
		{
			name:            "mission failure prevents task and assignment",
			commands:        &responsibilityAutoplanFailureCommands{missionErr: missionFailure},
			wantMissionCall: 1,
			wantErrorText:   missionFailure.Error(),
		},
		{
			name:            "empty mission identifier prevents task and assignment",
			commands:        &responsibilityAutoplanFailureCommands{},
			wantMissionCall: 1,
			wantErrorText:   "Mission ID is empty",
		},
		{
			name: "task failure prevents assignment",
			commands: &responsibilityAutoplanFailureCommands{
				mission: missioncontrol.MissionDTO{MissionID: "mission-1"}, taskErr: taskFailure,
			},
			wantMissionCall: 1,
			wantTaskCall:    1,
			wantErrorText:   taskFailure.Error(),
		},
		{
			name: "empty task identifier prevents assignment",
			commands: &responsibilityAutoplanFailureCommands{
				mission: missioncontrol.MissionDTO{MissionID: "mission-1"},
			},
			wantMissionCall: 1,
			wantTaskCall:    1,
			wantErrorText:   "task ID is empty",
		},
		{
			name: "assignment failure is returned after task creation",
			commands: &responsibilityAutoplanFailureCommands{
				mission: missioncontrol.MissionDTO{MissionID: "mission-1"}, task: missioncontrol.TaskCardDTO{TaskID: "task-1"}, assignErr: assignmentFailure,
			},
			wantMissionCall: 1,
			wantTaskCall:    1,
			wantAssignCall:  1,
			wantErrorText:   assignmentFailure.Error(),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			materializer := newProductionResponsibilityAutoplanMissionMaterializer(testCase.commands, func() time.Time { return time.Unix(0, 0).UTC() })
			_, err := materializer.MaterializeAutoplanStep(context.Background(), plan, step.StepID)
			if err == nil || !strings.Contains(err.Error(), testCase.wantErrorText) {
				t.Fatalf("materialize error = %v, want text %q", err, testCase.wantErrorText)
			}
			if testCase.commands.missionCalls != testCase.wantMissionCall || testCase.commands.taskCalls != testCase.wantTaskCall || testCase.commands.assignCalls != testCase.wantAssignCall {
				t.Fatalf("command calls Mission=%d task=%d assign=%d, want %d/%d/%d", testCase.commands.missionCalls, testCase.commands.taskCalls, testCase.commands.assignCalls, testCase.wantMissionCall, testCase.wantTaskCall, testCase.wantAssignCall)
			}
		})
	}
}

type responsibilityAutoplanFailureCommands struct {
	mission    missioncontrol.MissionDTO
	task       missioncontrol.TaskCardDTO
	missionErr error
	taskErr    error
	assignErr  error

	missionCalls int
	taskCalls    int
	assignCalls  int
}

func (f *responsibilityAutoplanFailureCommands) CreateMission(_ context.Context, _ missioncontrol.ViewerRef, _ missioncontrol.CreateMissionCommand) (missioncontrol.MissionDTO, error) {
	f.missionCalls++
	if f.missionErr != nil {
		return missioncontrol.MissionDTO{}, f.missionErr
	}
	return f.mission, nil
}

func (f *responsibilityAutoplanFailureCommands) CreateTask(_ context.Context, _ missioncontrol.ViewerRef, _ missioncontrol.CreateTaskCommand) (missioncontrol.TaskCardDTO, error) {
	f.taskCalls++
	if f.taskErr != nil {
		return missioncontrol.TaskCardDTO{}, f.taskErr
	}
	return f.task, nil
}

func (f *responsibilityAutoplanFailureCommands) AssignTask(_ context.Context, _ missioncontrol.ViewerRef, _ missioncontrol.AssignTaskCommand) (missioncontrol.CommandReceiptDTO, error) {
	f.assignCalls++
	if f.assignErr != nil {
		return missioncontrol.CommandReceiptDTO{}, f.assignErr
	}
	return missioncontrol.CommandReceiptDTO{CommandID: "assignment", Status: "accepted"}, nil
}
