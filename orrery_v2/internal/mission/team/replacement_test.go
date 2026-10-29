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

func TestServiceReplaceLinksOldAndNewAssignments(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	oldProfile := serviceProfile("profile-old", agentcore.AgentKindAI)
	newProfile := serviceProfile("profile-new", agentcore.AgentKindAI)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), oldProfile)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), newProfile)
	oldAssignment, err := service.Appoint(ctx, AppointmentCommand{IDempotencyKey: "appoint-old", MissionID: "mission-001", ProfileID: oldProfile.ID, Role: MissionRoleWorker, AppointedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}})
	if err != nil {
		t.Fatalf("appoint old: %v", err)
	}

	newAssignment, err := service.Replace(ctx, ReplaceCommand{IDempotencyKey: "replace-001", MissionID: "mission-001", OldAssignmentID: oldAssignment.ID, NewProfileID: newProfile.ID.String(), Role: MissionRoleWorker, ReplacedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}, ReplacementReason: "availability changed"})
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if newAssignment.ID == oldAssignment.ID || newAssignment.AgentProfileID != newProfile.ID {
		t.Fatalf("new assignment = %#v", newAssignment)
	}
	events, err := eventstore.New(env.DB).Load(ctx, "mission-001")
	if err != nil {
		t.Fatalf("load events: %v", err)
	}
	if events[len(events)-1].EventType != string(missionevent.EventAgentReplaced) {
		t.Fatalf("last event = %#v", events[len(events)-1])
	}
}

func TestServiceReplaceIsIdempotentForSameKey(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	service := newServiceFixture(t, env)
	oldProfile := serviceProfile("profile-old", agentcore.AgentKindAI)
	newProfile := serviceProfile("profile-new", agentcore.AgentKindAI)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), oldProfile)
	seedProfile(t, ctx, service.profileRepo.(*agentcore.PlatformProfileRepository), newProfile)
	oldAssignment, err := service.Appoint(ctx, AppointmentCommand{IDempotencyKey: "appoint-old", MissionID: "mission-001", ProfileID: oldProfile.ID, Role: MissionRoleWorker, AppointedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}})
	if err != nil {
		t.Fatalf("appoint old: %v", err)
	}
	cmd := ReplaceCommand{IDempotencyKey: "replace-001", MissionID: "mission-001", OldAssignmentID: oldAssignment.ID, NewProfileID: newProfile.ID.String(), Role: MissionRoleWorker, ReplacedBy: ActorRef{ID: "staffing-agent-001", Kind: ActorKindAgent}, ReplacementReason: "availability changed"}
	first, err := service.Replace(ctx, cmd)
	if err != nil {
		t.Fatalf("first replace: %v", err)
	}
	second, err := service.Replace(ctx, cmd)
	if err != nil {
		t.Fatalf("second replace same key: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("idempotent replacement returned %s, want %s", second.ID, first.ID)
	}
	cmd.IDempotencyKey = "replace-002"
	_, err = service.Replace(ctx, cmd)
	if !errors.Is(err, ErrPolicyDenied) {
		t.Fatalf("different replace key err = %v, want policy denied", err)
	}
}
