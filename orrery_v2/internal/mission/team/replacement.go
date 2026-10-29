package team

import missioncore "orrery_v2/internal/mission/core"

type ReplacementID string

type Replacement struct {
	ID              ReplacementID
	MissionID       missioncore.MissionID
	OldAssignmentID MissionAssignmentID
	NewAssignmentID MissionAssignmentID
	Reason          string
}

type ReplaceCommand struct {
	IDempotencyKey    string
	MissionID         missioncore.MissionID
	OldAssignmentID   MissionAssignmentID
	NewProfileID      string
	Role              MissionRole
	Responsibility    string
	AuthorityScope    string
	ReplacedBy        ActorRef
	ReplacementReason string
}
