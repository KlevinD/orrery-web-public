package discussion

import (
	"context"
	"errors"
	"sync"
	"testing"

	missioncore "orrery_v2/internal/mission/core"
	missionevent "orrery_v2/internal/mission/event"
	"orrery_v2/internal/mission/storage"
)

func TestDiscussionServiceCreatesStandaloneDiscussionGroup(t *testing.T) {
	ctx := context.Background()
	svc, unit := newTestDiscussionService(t)
	group, err := svc.CreateGroup(ctx, missioncore.MissionID(""), "Discuss product direction")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	if group.ID == "" || !group.MissionID.IsZero() || group.Goal != "Discuss product direction" {
		t.Fatalf("standalone group = %#v", group)
	}
	member := testAgentDiscussionMember(group.ID, "profile-agent-1", "assignment-1")
	if err := svc.AddMember(ctx, group.ID, member); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	msg, err := svc.PostMessage(ctx, PostMessageCommand{
		DiscussionGroupID: group.ID,
		Author:            ViewerRef{Kind: ViewerAgent, Ref: "profile-agent-1"},
		Content:           "No mission yet, just exploring.",
		MessageKind:       MessageKindRequest,
	})

	if err != nil {
		t.Fatalf("PostMessage returned error: %v", err)
	}
	if !msg.MissionID.IsZero() || msg.DiscussionGroupID != group.ID {
		t.Fatalf("standalone message linkage = %#v", msg)
	}
	events := loadDiscussionEvents(t, ctx, unit, string(group.ID))
	if !hasEvent(events, missionevent.EventDiscussionGroupCreated) || !hasEvent(events, missionevent.EventMessagePosted) {
		t.Fatalf("standalone discussion events should be scoped to discussion group, got %#v", eventTypes(events))
	}
}

func TestDiscussionServiceListsGroupMembers(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestDiscussionService(t)
	group, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-1"), "Coordinate stable members")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}
	member := testAgentDiscussionMember(group.ID, "profile-agent-1", "assignment-1")
	if err := svc.AddMember(ctx, group.ID, member); err != nil {
		t.Fatalf("AddMember: %v", err)
	}

	members, err := svc.ListMembers(ctx, group.ID)

	if err != nil {
		t.Fatalf("ListMembers: %v", err)
	}
	if len(members) != 1 || members[0].AgentProfileID != "profile-agent-1" {
		t.Fatalf("members = %+v", members)
	}
}

func TestDiscussionServiceRejectsAgentMemberWithoutProfileIdentity(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestDiscussionService(t)
	group, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-1"), "Coordinate stable members")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}

	err = svc.AddMember(ctx, group.ID, DiscussionMember{
		DiscussionGroupID: group.ID,
		AgentInstanceID:   "agent-1:mission-1",
		MemberKind:        DiscussionMemberAgent,
		VisibilityScope:   VisibilityMission,
		CanPost:           true,
		CanMention:        true,
	})

	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("AddMember error = %v, want ErrInvalidArgument", err)
	}
}

func TestDiscussionServiceRejectsScopedAgentMemberIdentity(t *testing.T) {
	ctx := context.Background()
	svc, _ := newTestDiscussionService(t)
	group, err := svc.CreateGroup(ctx, missioncore.MissionID("mission-1"), "Coordinate stable members")
	if err != nil {
		t.Fatalf("CreateGroup returned error: %v", err)
	}

	err = svc.AddMember(ctx, group.ID, DiscussionMember{
		DiscussionGroupID: group.ID,
		AgentProfileID:    "agent-1",
		AgentInstanceID:   "agent-1:mission-1",
		MemberKind:        DiscussionMemberAgent,
		VisibilityScope:   VisibilityMission,
		CanPost:           true,
		CanMention:        true,
	})

	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("AddMember error = %v, want ErrInvalidArgument for scoped agent identity", err)
	}
}

func TestDiscussionServiceConcurrentCreateReusesOneMissionGroup(t *testing.T) {
	ctx := context.Background()
	svc, unit := newTestDiscussionService(t)
	const workers = 32
	start := make(chan struct{})
	results := make(chan DiscussionGroup, workers)
	errorsFound := make(chan error, workers)
	var wait sync.WaitGroup
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			group, err := svc.CreateGroupForTenant(ctx, "tenant-a", missioncore.MissionID("mission-concurrent"), "Coordinate one canonical discussion")
			if err != nil {
				errorsFound <- err
				return
			}
			results <- group
		}()
	}
	close(start)
	wait.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		if err != nil {
			t.Fatalf("CreateGroupForTenant: %v", err)
		}
	}
	var groupID DiscussionGroupID
	for group := range results {
		if groupID == "" {
			groupID = group.ID
		}
		if group.ID != groupID || group.TenantID != "tenant-a" || group.MissionID != "mission-concurrent" {
			t.Fatalf("concurrent result = %+v, canonical group_id=%s", group, groupID)
		}
	}
	snapshot := unitSnapshot(t, ctx, unit)
	if len(snapshot[storage.DiscussionGroups]) != 1 {
		t.Fatalf("discussion group records = %d, want 1", len(snapshot[storage.DiscussionGroups]))
	}
}
