package team

import (
	"fmt"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	missioncore "orrery_v2/internal/mission/core"
)

type MissionAssignmentID string
type AssignmentStatus string

const (
	AssignmentStatusActive   AssignmentStatus = "active"
	AssignmentStatusReleased AssignmentStatus = "released"
	AssignmentStatusReplaced AssignmentStatus = "replaced"
	AssignmentStatusExpired  AssignmentStatus = "expired"
)

type MissionAssignment struct {
	ID                        MissionAssignmentID
	MissionID                 missioncore.MissionID
	MemberID                  MissionTeamMemberID
	AgentInstanceID           agentcore.AgentInstanceID
	AgentProfileID            agentcore.AgentProfileID
	AgentKind                 agentcore.AgentKind
	Role                      MissionRole
	ResponsibilityBoundary    string
	AuthorityScope            string
	TaskRefs                  []missioncore.TaskID
	AssignedBy                ActorRef
	AssignedAt                time.Time
	ReleasedAt                time.Time
	ReleaseID                 ReleaseID
	ReleaseIdempotencyKey     string
	ReplacedByAssignmentID    MissionAssignmentID
	ReplacementID             ReplacementID
	ReplacementIdempotencyKey string
	ReplacementReason         string
	Status                    AssignmentStatus
	IdempotencyKey            string
}

func ValidateMissionAssignment(a MissionAssignment) error {
	if a.ID == "" {
		return fmt.Errorf("%w: assignment_id is required", ErrInvalidArgument)
	}
	if a.MissionID.IsZero() || a.MemberID == "" || a.AgentInstanceID.IsZero() || a.AgentProfileID.IsZero() {
		return fmt.Errorf("%w: assignment mission/member/agent refs are required", ErrInvalidArgument)
	}
	if !a.Role.IsValid() {
		return fmt.Errorf("%w: mission role is invalid", ErrInvalidArgument)
	}
	if a.Status == "" {
		return fmt.Errorf("%w: assignment status is required", ErrInvalidArgument)
	}
	return nil
}
