package team

import (
	"time"

	missioncore "orrery_v2/internal/mission/core"
)

type ReleaseID string

type Release struct {
	ID           ReleaseID
	MissionID    missioncore.MissionID
	AssignmentID MissionAssignmentID
	ReleasedBy   ActorRef
	ReleasedAt   time.Time
	Reason       string
}

type ReleaseCommand struct {
	IDempotencyKey string
	MissionID      missioncore.MissionID
	AssignmentID   MissionAssignmentID
	ReleasedBy     ActorRef
	Reason         string
}
