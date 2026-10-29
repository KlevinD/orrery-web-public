package discussion

import (
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/mission/team"
)

func authorRole(members []DiscussionMember, viewer ViewerRef) team.MissionRole {
	for _, member := range members {
		if memberMatchesViewer(member, viewer) {
			return member.MissionRole
		}
	}
	return ""
}

func authorRefFromViewer(members []DiscussionMember, viewer ViewerRef) AuthorRef {
	switch viewer.Kind {
	case ViewerAgent:
		if member, ok := findPostingMember(members, viewer); ok {
			return AuthorRef{Kind: AuthorAgent, AgentID: stableAgentIDForMember(member)}
		}
		return AuthorRef{Kind: AuthorAgent}
	case ViewerUser:
		return AuthorRef{Kind: AuthorUser, UserID: viewer.Ref}
	case ViewerSystem:
		return AuthorRef{Kind: AuthorSystem, SystemID: viewer.Ref}
	default:
		return AuthorRef{Kind: AuthorUser, UserID: viewer.Ref}
	}
}

func stableAgentIDForMember(member DiscussionMember) agentcore.AgentID {
	if member.AgentProfileID != "" {
		return agentcore.AgentID(member.AgentProfileID)
	}
	return ""
}
