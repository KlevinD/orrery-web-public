package missioncontrol_test

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/platform/testkit"
)

func TestLifecycleServiceExcludesHumanTaskAssigneesFromCanonicalAgentMembership(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	repo := missioncontrol.NewDBProjectionRepository(env.DBRouter)
	agents := &fakeLifecycleAgentDirectory{entries: []agentcore.AgentDirectoryEntry{
		{AgentProfileID: "human-1", AgentInstanceID: "human-1", AgentKind: agentcore.AgentKindHuman, Enabled: true},
		{AgentProfileID: "agent-1", AgentInstanceID: "agent-1", AgentKind: agentcore.AgentKindAI, Enabled: true},
	}}
	service := newAuthorizedLifecycleService(env.UnitOfWork, env.DBRouter, repo, missioncontrol.LifecycleOptions{Clock: env.Clock, IDGenerator: env.IDGenerator, AgentDirectory: agents})
	memberships := &capturingCanonicalConversationMembership{}
	service.SetCanonicalConversationMembership(memberships)
	viewer := missioncontrol.ViewerRef{ViewerID: "mission-conversation-linker", ViewerKind: missioncontrol.ViewerKindSystem, TenantID: "tenant-a"}
	mission, err := service.CreateMission(ctx, viewer, missioncontrol.CreateMissionCommand{
		IdempotencyKey: "mission-human-chat", Title: "Mission execution", FinalGoal: "Implement", ExistingConversationID: "conversation-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey: "task-human-chat", MissionID: missioncore.MissionID(mission.MissionID), AssigneeID: "human-1", Goal: "Review step",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateTask(ctx, viewer, missioncontrol.CreateTaskCommand{
		IdempotencyKey: "task-agent-chat", MissionID: missioncore.MissionID(mission.MissionID), AssigneeID: "agent-1", Goal: "Implement step",
	}); err != nil {
		t.Fatal(err)
	}
	if len(memberships.sets) != 2 {
		t.Fatalf("canonical membership reconciliations = %+v", memberships.sets)
	}
	if len(memberships.sets[0].AgentIDs) != 0 {
		t.Fatalf("human task was sent as canonical agent membership: %+v", memberships.sets[0])
	}
	for _, request := range memberships.sets {
		if len(request.HumanIDs) != 1 || request.HumanIDs[0] != "human-1" {
			t.Fatalf("human assignee must be reconciled as a human: %+v", request)
		}
	}
	if got := memberships.sets[1].AgentIDs; len(got) != 1 || got[0] != "agent-1" {
		t.Fatalf("AI task membership retained historical human assignee: %+v", memberships.sets[1])
	}
}
