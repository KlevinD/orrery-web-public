package team

import (
	"fmt"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

type MissionTeamMemberID string
type MemberStatus string
type TeamVisibility string

const (
	MemberStatusInvited     MemberStatus = "invited"
	MemberStatusActive      MemberStatus = "active"
	MemberStatusReleased    MemberStatus = "released"
	MemberStatusReplaced    MemberStatus = "replaced"
	MemberStatusUnavailable MemberStatus = "unavailable"

	TeamVisibilityMission TeamVisibility = "mission"
	TeamVisibilityPrivate TeamVisibility = "private"
)

type AssignmentRef struct {
	ID     MissionAssignmentID
	Role   MissionRole
	Status AssignmentStatus
}

type MissionTeamMember struct {
	ID              MissionTeamMemberID
	MissionID       missioncore.MissionID
	AgentInstanceID agentcore.AgentInstanceID
	AgentProfileID  agentcore.AgentProfileID
	AgentKind       agentcore.AgentKind
	JoinedAt        time.Time
	JoinedBy        ActorRef
	Status          MemberStatus
	Visibility      TeamVisibility
	AssignmentRefs  []AssignmentRef
	ReleaseRef      ReleaseID
	ReplacementRef  ReplacementID
}

func ValidateMissionTeamMember(member MissionTeamMember) error {
	if member.ID == "" {
		return fmt.Errorf("%w: member_id is required", ErrInvalidArgument)
	}
	if member.MissionID.IsZero() {
		return fmt.Errorf("%w: mission_id is required", ErrInvalidArgument)
	}
	if member.AgentProfileID.IsZero() {
		return fmt.Errorf("%w: agent_profile_id is required", ErrInvalidArgument)
	}
	if member.AgentInstanceID.IsZero() {
		return fmt.Errorf("%w: agent_instance_id is required", ErrInvalidArgument)
	}
	if member.Status == "" {
		return fmt.Errorf("%w: member status is required", ErrInvalidArgument)
	}
	return nil
}
