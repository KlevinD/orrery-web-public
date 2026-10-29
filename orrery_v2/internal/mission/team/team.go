package team

import (
	"fmt"
	"time"

	missioncore "orrery_v2/internal/mission/core"
)

type MissionTeamID string

type MissionTeam struct {
	ID        MissionTeamID
	MissionID missioncore.MissionID
	Members   []MissionTeamMember
	CreatedAt time.Time
	UpdatedAt time.Time
	Version   int
}

func NewMissionTeam(id MissionTeamID, missionID missioncore.MissionID, now time.Time) (MissionTeam, error) {
	if id == "" {
		return MissionTeam{}, fmt.Errorf("%w: team_id is required", ErrInvalidArgument)
	}
	if missionID.IsZero() {
		return MissionTeam{}, fmt.Errorf("%w: mission_id is required", ErrInvalidArgument)
	}
	return MissionTeam{ID: id, MissionID: missionID, CreatedAt: now, UpdatedAt: now, Version: 1}, nil
}

func (t MissionTeam) ActiveAssignmentFor(role MissionRole, profileID string) (MissionAssignmentID, bool) {
	for _, member := range t.Members {
		if string(member.AgentProfileID) != profileID {
			continue
		}
		for _, ref := range member.AssignmentRefs {
			if ref.Role == role && ref.Status == AssignmentStatusActive {
				return ref.ID, true
			}
		}
	}
	return "", false
}
