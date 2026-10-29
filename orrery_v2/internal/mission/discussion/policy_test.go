package discussion

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
	"orrery_v2/internal/mission/team"
)

func TestMessagePolicyAllowsOnlyActivePostingMembers(t *testing.T) {
	group := DiscussionGroup{ID: "discussion-1", MissionID: "mission-1", Status: DiscussionGroupActive}
	member := DiscussionMember{
		DiscussionGroupID: group.ID,
		AgentProfileID:    "profile-1",
		AgentInstanceID:   "profile-1",
		MemberKind:        DiscussionMemberAgent,
		CanPost:           true,
		CanMention:        true,
		JoinedAt:          time.Now().UTC(),
	}
	policy := NewMessagePolicy()

	if err := policy.CanPost(group, []DiscussionMember{member}, ViewerRef{Kind: ViewerAgent, Ref: "profile-1"}); err != nil {
		t.Fatalf("stable agent profile should identify the same active posting member: %v", err)
	}
	if err := policy.CanPost(group, []DiscussionMember{member}, ViewerRef{Kind: ViewerAgent, Ref: "profile-1:runtime"}); err == nil {
		t.Fatalf("runtime instance id should not identify an agent member")
	}
	if err := policy.CanPost(group, []DiscussionMember{member}, ViewerRef{Kind: ViewerAgent, Ref: "agent-2"}); err == nil {
		t.Fatalf("non-member post should be rejected")
	}
	member.CanPost = false
	if err := policy.CanPost(group, []DiscussionMember{member}, ViewerRef{Kind: ViewerAgent, Ref: "profile-1"}); err == nil {
		t.Fatalf("member without can_post should be rejected")
	}
}

func TestMessagePolicyAllowsActiveUserMembersToPost(t *testing.T) {
	group := DiscussionGroup{ID: "discussion-1", MissionID: "mission-1", Status: DiscussionGroupActive}
	member := DiscussionMember{
		DiscussionGroupID: group.ID,
		UserID:            "planner-1",
		MemberKind:        DiscussionMemberHuman,
		CanPost:           true,
		CanMention:        true,
		JoinedAt:          time.Now().UTC(),
	}
	policy := NewMessagePolicy()

	if err := policy.CanPost(group, []DiscussionMember{member}, ViewerRef{Kind: ViewerUser, Ref: "planner-1"}); err != nil {
		t.Fatalf("active user member should post: %v", err)
	}
	if err := policy.CanPost(group, []DiscussionMember{member}, ViewerRef{Kind: ViewerUser, Ref: "other-user"}); err == nil {
		t.Fatalf("non-member user post should be rejected")
	}
}

func TestMessagePolicyAllowsNamedSystemPrincipalWithoutMembership(t *testing.T) {
	group := DiscussionGroup{ID: "discussion-1", MissionID: "mission-1", Status: DiscussionGroupActive}
	policy := NewMessagePolicy()
	viewer := ViewerRef{Kind: ViewerSystem, Ref: "system-superadmin"}

	if err := policy.CanPost(group, nil, viewer); err != nil {
		t.Fatalf("named system principal should post without workforce membership: %v", err)
	}
	if err := policy.CanMention(nil, viewer); err != nil {
		t.Fatalf("named system principal should mention without workforce membership: %v", err)
	}
	if err := policy.CanPost(group, nil, ViewerRef{Kind: ViewerSystem}); err == nil {
		t.Fatal("anonymous system principal should be rejected")
	}
	group.Status = DiscussionGroupArchived
	if err := policy.CanPost(group, nil, viewer); err == nil {
		t.Fatal("system principal should not post to an inactive discussion")
	}
}

func TestDiscussionMemberCanBeProjectedFromMissionTeamAssignment(t *testing.T) {
	assignment := team.MissionAssignment{
		ID:              "assignment-1",
		MissionID:       missioncore.MissionID("mission-1"),
		MemberID:        "member-1",
		AgentProfileID:  agentcore.AgentProfileID("profile-1"),
		AgentInstanceID: agentcore.AgentInstanceID("profile-1"),
		Role:            team.MissionRoleWorker,
		Status:          team.AssignmentStatusActive,
		AssignedAt:      time.Now().UTC(),
	}

	member := NewDiscussionMemberFromAssignment("discussion-1", assignment)

	if member.AgentProfileID != "profile-1" || member.AgentInstanceID != "profile-1" || member.MissionAssignmentRef != "assignment-1" || member.VisibilityScope != VisibilityMission {
		t.Fatalf("unexpected member projection: %#v", member)
	}
	if !member.CanPost || !member.CanMention {
		t.Fatalf("mission assignment members should be able to post and mention by default")
	}
}
