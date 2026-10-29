package team

import "testing"

func TestMissionRoleRejectsDeprecatedPeopleTitles(t *testing.T) {
	for _, role := range []MissionRole{"PM", "HR"} {
		if role.IsValid() {
			t.Fatalf("%s should not be a valid mission role; use neutral roles such as %s or %s", role, MissionRoleLead, MissionRoleStaffing)
		}
	}
}
