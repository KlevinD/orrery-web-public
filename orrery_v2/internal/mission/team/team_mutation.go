package team

import "time"

func appendMember(team MissionTeam, member MissionTeamMember) MissionTeam {
	if team.ID == "" {
		team.ID = MissionTeamID("team-" + team.MissionID.String())
	}
	if team.MissionID.IsZero() {
		team.MissionID = member.MissionID
	}
	for i := range team.Members {
		if team.Members[i].ID == member.ID {
			team.Members[i] = member
			return team
		}
	}
	team.Members = append(team.Members, member)
	team.Version++
	return team
}

func markMemberReleased(team MissionTeam, memberID MissionTeamMemberID, releaseID ReleaseID, at time.Time) MissionTeam {
	for i := range team.Members {
		if team.Members[i].ID != memberID {
			continue
		}
		team.Members[i].Status = MemberStatusReleased
		team.Members[i].ReleaseRef = releaseID
		for j := range team.Members[i].AssignmentRefs {
			if team.Members[i].AssignmentRefs[j].Status == AssignmentStatusActive {
				team.Members[i].AssignmentRefs[j].Status = AssignmentStatusReleased
			}
		}
		team.UpdatedAt = at
		team.Version++
	}
	return team
}

func markMemberReplaced(team MissionTeam, memberID MissionTeamMemberID, replacementID ReplacementID, at time.Time) MissionTeam {
	for i := range team.Members {
		if team.Members[i].ID != memberID {
			continue
		}
		team.Members[i].Status = MemberStatusReplaced
		team.Members[i].ReplacementRef = replacementID
		for j := range team.Members[i].AssignmentRefs {
			if team.Members[i].AssignmentRefs[j].Status == AssignmentStatusActive {
				team.Members[i].AssignmentRefs[j].Status = AssignmentStatusReplaced
			}
		}
		team.UpdatedAt = at
		team.Version++
	}
	return team
}
