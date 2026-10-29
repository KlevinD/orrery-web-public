package operations

import "testing"

func TestSplitWorkforceRoleIDsSeparatesPermissionRolesAndFunctionTags(t *testing.T) {
	member := WorkforceMemberDTO{
		RoleIDs:        []string{"pm", "engineering-delivery", "primary_role", "human_agent"},
		AuthorityScope: "primary_role",
	}

	splitWorkforceRoleIDs(&member, map[string]struct{}{"pm": {}})

	if len(member.RoleIDs) != 1 || member.RoleIDs[0] != "pm" {
		t.Fatalf("permission role ids = %+v", member.RoleIDs)
	}
	if len(member.FunctionalRoleIDs) != 3 || member.FunctionalRoleIDs[0] != "engineering-delivery" || member.FunctionalRoleIDs[1] != "primary_role" || member.FunctionalRoleIDs[2] != "human_agent" {
		t.Fatalf("functional role ids = %+v", member.FunctionalRoleIDs)
	}
	if member.AuthorityScope != "engineering-delivery" {
		t.Fatalf("display authority scope = %q", member.AuthorityScope)
	}
}
