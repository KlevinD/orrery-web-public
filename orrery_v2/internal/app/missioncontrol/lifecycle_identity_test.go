package missioncontrol_test

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	missionteam "orrery_v2/internal/mission/team"
	"orrery_v2/internal/platform/testkit"
)

func TestLifecycleServiceDoesNotResolveAssignmentIdentityFromAnotherProfileInstance(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{{
		AgentProfileID: "other-agent", AgentInstanceID: "lead-agent", DisplayName: "Wrong Agent", RoleName: "Wrong Role", AgentKind: agentcore.AgentKindAI, Enabled: true,
	}}}
	teamBootstrap := &fakeTeamBootstrap{assignments: []missionteam.MissionAssignment{{
		ID: "assignment-lead", AgentProfileID: "lead-agent", AgentInstanceID: "lead-agent", Role: missionteam.MissionRoleLead, AssignedAt: env.Clock.Now().UTC(),
	}}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{
		Clock:          env.Clock,
		IDGenerator:    env.IDGenerator,
		AgentDirectory: agents,
		TeamBootstrap:  teamBootstrap,
		SystemRoles: []missioncontrol.SystemRoleBootstrap{{
			ProfileID: "lead-agent", Role: missionteam.MissionRoleLead, IdempotencySuffix: "lead",
		}},
	})

	mission, err := service.CreateMission(ctx, missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}, missioncontrol.CreateMissionCommand{
		IdempotencyKey: "mission-key",
		Title:          "Launch",
		FinalGoal:      "Ship v2",
	})
	if err != nil {
		t.Fatalf("CreateMission returned error: %v", err)
	}
	view, err := newAuthorizedQueryService(repo).GetMissionControlView(ctx, missioncontrol.ViewerRef{ViewerID: "planner-1", ViewerKind: missioncontrol.ViewerKindUser}, missioncore.MissionID(mission.MissionID))
	if err != nil {
		t.Fatalf("GetMissionControlView returned error: %v", err)
	}
	if len(view.Team.Members) != 1 {
		t.Fatalf("team members = %+v, want one member", view.Team.Members)
	}
	member := view.Team.Members[0]
	if member.AgentID != "lead-agent" || member.DisplayName == "Wrong Agent" || member.RoleName == "Wrong Role" {
		t.Fatalf("team member identity = %+v, want assignment profile without another profile directory identity", member)
	}
}
