package missioncontrol_test

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
)

func TestMissionControlServicesFailClosedWithoutAuthorizationDependencies(t *testing.T) {
	ctx := context.Background()
	viewer := missioncontrol.ViewerRef{ViewerID: "viewer-1", ViewerKind: missioncontrol.ViewerKindUser}
	repo := missioncontrol.NewMemoryProjectionRepository()

	t.Run("command", func(t *testing.T) {
		service := missioncontrol.NewCommandService(&fakeDeliveryPort{})
		_, err := service.SubmitAgentResult(ctx, viewer, missioncontrol.SubmitResultCommand{
			IdempotencyKey: "submit-1",
			MissionID:      "mission-1",
			TaskID:         "task-1",
			Result:         agentcore.AgentResult{ID: "result-1"},
		})
		if !errors.Is(err, missioncontrol.ErrServiceDependency) {
			t.Fatalf("SubmitAgentResult error = %v, want ErrServiceDependency", err)
		}
	})

	t.Run("query", func(t *testing.T) {
		service := missioncontrol.NewQueryService(repo)
		_, err := service.GetMissionControlView(ctx, viewer, "mission-1")
		if !errors.Is(err, missioncontrol.ErrServiceDependency) {
			t.Fatalf("GetMissionControlView error = %v, want ErrServiceDependency", err)
		}
	})

	t.Run("approval", func(t *testing.T) {
		service := missioncontrol.NewApprovalService(repo, nil)
		_, err := service.ListApprovals(ctx, viewer, missioncontrol.ApprovalListQuery{MissionID: "mission-1"})
		if !errors.Is(err, missioncontrol.ErrServiceDependency) {
			t.Fatalf("ListApprovals error = %v, want ErrServiceDependency", err)
		}
	})

	t.Run("lifecycle", func(t *testing.T) {
		service := missioncontrol.NewLifecycleService(nil, nil, repo, missioncontrol.LifecycleOptions{})
		_, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{
			IdempotencyKey: "mission-1",
			FinalGoal:      "fail closed",
		})
		if !errors.Is(err, missioncontrol.ErrServiceDependency) {
			t.Fatalf("CreateMission error = %v, want ErrServiceDependency", err)
		}
	})
}
