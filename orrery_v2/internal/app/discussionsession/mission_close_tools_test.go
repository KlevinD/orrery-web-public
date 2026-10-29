package discussionsession

import (
	"context"
	"strings"
	"testing"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
)

func TestMissionToolApplierDefersConversationCloseoutWhileMissionWorkIsPending(t *testing.T) {
	missions := &closeReadyMissionCommands{
		capturingMissionCommands: capturingMissionCommands{},
		readiness:                MissionCloseReadiness{Status: MissionClosePending},
	}
	applier := NewMissionToolApplier(missions)
	calls := []ToolCallResult{{
		Name: "discussion_close",
		Arguments: map[string]string{
			"mission_id":      "mission-1",
			"mission_task_id": "task-worker",
			"note":            "The worker has been started.",
		},
	}}

	if _, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", calls); err != nil {
		t.Fatal(err)
	}
	if missions.submitCalled {
		t.Fatalf("pending closeout submitted a result: %+v", missions.submitCmd)
	}
	if calls[0].Result["status"] != MissionClosePending || calls[0].Result["mission_id"] != "mission-1" || calls[0].Result["task_id"] != "task-worker" {
		t.Fatalf("pending closeout result = %+v", calls[0].Result)
	}
}

func TestMissionClosePendingContinuationTellsCoordinatorToStopRetrying(t *testing.T) {
	content := discussionToolResultContinuationContent([]ToolCallDTO{{
		Name:   "discussion_close",
		Status: "completed",
		Result: map[string]string{"status": MissionClosePending},
	}}, nil)

	if !strings.Contains(content, "discussion_close 返回 pending") || !strings.Contains(content, "停止重试关闭") {
		t.Fatalf("pending close continuation guidance = %q", content)
	}
}

func TestMissionToolApplierCompletesConversationCloseoutWithoutResubmittingApprovedWork(t *testing.T) {
	missions := &closeReadyMissionCommands{
		capturingMissionCommands: capturingMissionCommands{},
		readiness:                MissionCloseReadiness{Status: MissionCloseCompleted},
	}
	applier := NewMissionToolApplier(missions)
	calls := []ToolCallResult{{
		Name: "discussion_close",
		Arguments: map[string]string{
			"mission_id":      "mission-1",
			"mission_task_id": "task-worker",
		},
	}}

	if _, err := applier.Apply(context.Background(), missionToolTestViewer(), "conv-1", calls); err != nil {
		t.Fatal(err)
	}
	if missions.submitCalled {
		t.Fatalf("approved closeout resubmitted a result: %+v", missions.submitCmd)
	}
	if calls[0].Result["status"] != MissionCloseCompleted {
		t.Fatalf("completed closeout result = %+v", calls[0].Result)
	}
}

func TestMissionToolApplierSubmitsReadyConversationCloseout(t *testing.T) {
	missions := &closeReadyMissionCommands{
		capturingMissionCommands: capturingMissionCommands{},
		readiness:                MissionCloseReadiness{Status: MissionCloseReady},
	}
	applier := NewMissionToolApplier(missions)
	calls := []ToolCallResult{{
		Name: "discussion_close",
		Arguments: map[string]string{
			"mission_id":      "mission-1",
			"mission_task_id": "task-worker",
			"note":            "Verified delivery.",
		},
	}}

	if _, err := applier.Apply(context.Background(), missioncontrol.ViewerRef{ViewerID: "agent-1", ViewerKind: missioncontrol.ViewerKindAgent}, "conv-1", calls); err != nil {
		t.Fatal(err)
	}
	if !missions.submitCalled || missions.submitCmd.TaskID != "task-worker" {
		t.Fatalf("ready closeout submission = called:%v command:%+v", missions.submitCalled, missions.submitCmd)
	}
	if calls[0].Result["status"] != MissionCloseCompleted {
		t.Fatalf("ready closeout result = %+v", calls[0].Result)
	}
}

type closeReadyMissionCommands struct {
	capturingMissionCommands
	readiness MissionCloseReadiness
	err       error
}

func (f *closeReadyMissionCommands) CheckMissionCloseReadiness(context.Context, missioncontrol.ViewerRef, string, string) (MissionCloseReadiness, error) {
	return f.readiness, f.err
}
