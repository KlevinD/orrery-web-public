package discussion

import "testing"

func TestAuthorRefDoesNotUseInstanceOnlyMemberAsStableAgentIdentity(t *testing.T) {
	member := DiscussionMember{
		AgentInstanceID: "profile-author:mission-1",
		MemberKind:      DiscussionMemberAgent,
		CanPost:         true,
	}

	author := authorRefFromViewer([]DiscussionMember{member}, ViewerRef{Kind: ViewerAgent, Ref: "profile-author:mission-1"})

	if author.Kind != AuthorAgent || author.AgentID != "" {
		t.Fatalf("author = %+v, want agent author without invented stable id", author)
	}
}
