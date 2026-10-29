package team

import (
	"context"
	"errors"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/platform/eventstore"
	"orrery_v2/internal/platform/testkit"
)

func TestServiceReleaseMarksAssignmentAndMemberReleased(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	profile := serviceProfile("profile-ada", agentcore.AgentKindAI)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)
	assignment, err := service.Appoint(ctx, AppointmentCommand{IDempotencyKey: "appoint-001", MissionID: "mission-001", ProfileID: profile.ID, Role: MissionRoleWorker, AppointedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}})
	if err != nil {
		t.Fatalf("appoint: %v", err)
	}

	if err := service.Release(ctx, ReleaseCommand{IDempotencyKey: "release-001", MissionID: "mission-001", AssignmentID: assignment.ID, ReleasedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}, Reason: "done"}); err != nil {
		t.Fatalf("Release: %v", err)
	}
	team, err := service.ListTeam(ctx, "mission-001")
	if err != nil {
		t.Fatalf("ListTeam: %v", err)
	}
	if team.Members[0].Status != MemberStatusReleased {
		t.Fatalf("team = %#v", team)
	}
	events, err := eventstore.New(env.DB).Load(ctx, "mission-001")
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	assertEventTypes(t, events, []missionevent.EventType{
		missionevent.EventStaffingDecisionRecorded,
		missionevent.EventAgentJoined,
		missionevent.EventMissionAssignmentCreated,
		missionevent.EventAgentReleased,
		missionevent.EventMissionAssignmentReleased,
	})
}

func TestServiceReleaseIsIdempotentForSameKey(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	profile := serviceProfile("profile-ada", agentcore.AgentKindAI)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), profile)
	assignment, err := service.Appoint(ctx, AppointmentCommand{IDempotencyKey: "appoint-001", MissionID: "mission-001", ProfileID: profile.ID, Role: MissionRoleWorker, AppointedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}})
	if err != nil {
		t.Fatalf("appoint: %v", err)
	}
	cmd := ReleaseCommand{IDempotencyKey: "release-001", MissionID: "mission-001", AssignmentID: assignment.ID, ReleasedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}, Reason: "done"}
	if err := service.Release(ctx, cmd); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if err := service.Release(ctx, cmd); err != nil {
		t.Fatalf("second release same key: %v", err)
	}
	cmd.IDempotencyKey = "release-002"
	if err := service.Release(ctx, cmd); !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("different release key err = %v, want policy denied", err)
	}
}
