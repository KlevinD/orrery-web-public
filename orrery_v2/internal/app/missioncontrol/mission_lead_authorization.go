package missioncontrol

import (
	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/mission/projection"
	missionteam "orrery_v2/internal/mission/team"
)

func missionControlOwnersFromTeam(members []projection.TeamMemberView) []authorization.SubjectRef {
	owners := make([]authorization.SubjectRef, 0, 2)
	for _, member := range members {
		role := missionteam.MissionRole(member.MissionRole)
		if role != missionteam.MissionRoleLead && role != missionteam.MissionRoleRealHumanContributor {
			continue
		}
		subject := missionSubjectFromTeamMember(member)
		if subject.Kind == "" || subject.ID == "" {
			continue
		}
		owners = append(owners, subject)
	}
	return owners
}
