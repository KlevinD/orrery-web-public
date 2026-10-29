package discussion

import (
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/mission/team"
)

type DiscussionMemberKind string
type VisibilityScope string

const (
	DiscussionMemberAgent DiscussionMemberKind = "agent"
	DiscussionMemberHuman DiscussionMemberKind = "human"

	VisibilityMission VisibilityScope = "mission"
	VisibilityPrivate VisibilityScope = "private"
	VisibilityQAOnly  VisibilityScope = "qa-only"
)

type DiscussionMember struct {
	DiscussionGroupID    DiscussionGroupID
	AgentProfileID       agentcore.AgentProfileID
	AgentInstanceID      agentcore.AgentInstanceID
	UserID               string
	MissionAssignmentRef team.MissionAssignmentID
	MissionRole          team.MissionRole
	MemberKind           DiscussionMemberKind
	VisibilityScope      VisibilityScope
	CanPost              bool
	CanMention           bool
	JoinedAt             time.Time
	LeftAt               time.Time
}

func NewDiscussionMemberFromAssignment(groupID DiscussionGroupID, assignment team.MissionAssignment) DiscussionMember {
	return DiscussionMember{
		DiscussionGroupID:    groupID,
		AgentProfileID:       assignment.AgentProfileID,
		AgentInstanceID:      assignment.AgentInstanceID,
		MissionAssignmentRef: assignment.ID,
		MissionRole:          assignment.Role,
		MemberKind:           DiscussionMemberAgent,
		VisibilityScope:      VisibilityMission,
		CanPost:              true,
		CanMention:           true,
		JoinedAt:             assignment.AssignedAt,
	}
}

func NewHumanDiscussionMemberFromAssignment(groupID DiscussionGroupID, userID string, assignment team.MissionAssignment) DiscussionMember {
	return DiscussionMember{
		DiscussionGroupID:    groupID,
		AgentProfileID:       assignment.AgentProfileID,
		UserID:               userID,
		MissionAssignmentRef: assignment.ID,
		MissionRole:          assignment.Role,
		MemberKind:           DiscussionMemberHuman,
		VisibilityScope:      VisibilityMission,
		CanPost:              true,
		CanMention:           true,
		JoinedAt:             assignment.AssignedAt,
	}
}

func (m DiscussionMember) Active() bool {
	return (m.AgentProfileID != "" || m.UserID != "") && m.LeftAt.IsZero()
}
