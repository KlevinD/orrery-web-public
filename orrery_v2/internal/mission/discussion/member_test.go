package discussion

import "testing"

func TestAgentDiscussionMemberActiveRequiresProfileIdentity(t *testing.T) {
	member := DiscussionMember{
		AgentInstanceID: "profile-agent:mission-1",
		MemberKind:      DiscussionMemberAgent,
	}

	if member.Active() {
		t.Fatalf("instance-only agent member should not be active: %+v", member)
	}
}

func TestMemberRefDoesNotCreateInstanceOnlyAgentKey(t *testing.T) {
	member := DiscussionMember{
		AgentInstanceID: "profile-agent:mission-1",
		MemberKind:      DiscussionMemberAgent,
	}

	if got := memberRef(member); got != "unknown" {
		t.Fatalf("memberRef = %q, want unknown for instance-only agent member", got)
	}
}
