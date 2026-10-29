package discussion

import (
	agentcore "orrery_v2/internal/agent/core"
	missionteam "orrery_v2/internal/mission/team"
)

func testAgentDiscussionMember(groupID DiscussionGroupID, profileID string, assignmentID string) DiscussionMember {
	return testAgentDiscussionMemberWithRole(groupID, profileID, assignmentID, "")
}

func testAgentDiscussionMemberWithRole(groupID DiscussionGroupID, profileID string, assignmentID string, role string) DiscussionMember {
	return DiscussionMember{
		DiscussionGroupID:    groupID,
		AgentProfileID:       agentcore.AgentProfileID(profileID),
		AgentInstanceID:      agentcore.AgentInstanceID(profileID),
		MissionAssignmentRef: missionteam.MissionAssignmentID(assignmentID),
		MissionRole:          missionteam.MissionRole(role),
		MemberKind:           DiscussionMemberAgent,
		VisibilityScope:      VisibilityMission,
		CanPost:              true,
		CanMention:           true,
	}
}
