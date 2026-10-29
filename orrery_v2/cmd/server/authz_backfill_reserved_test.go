package main

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	authorizationapp "orrery_v2/internal/app/authorization"
)

func TestAuthorizationBackfillRemovesReservedNonWorkforceSubjects(t *testing.T) {
	legacy := authorizationapp.SubjectRef{Kind: "user", ID: agentcore.LegacySuperadminAgentProfileID}
	local := authorizationapp.SubjectRef{Kind: "agent", ID: agentcore.LegacyLocalUserAgentProfileID}
	valid := authorizationapp.SubjectRef{Kind: "user", ID: "human-ding"}
	system := authorizationapp.SubjectRef{Kind: "system", ID: agentcore.SystemSuperadminPrincipalID}
	snapshot := authorizationapp.RelationBackfillSnapshot{
		Projects: []authorizationapp.ProjectRelationSnapshot{{Owner: legacy}},
		DevelopmentProjects: []authorizationapp.DevelopmentProjectRelationSnapshot{{
			Owner: legacy, Members: []authorizationapp.DevelopmentProjectMemberRelationSnapshot{{Subject: local}, {Subject: valid}},
		}},
		Todos: []authorizationapp.TodoRelationSnapshot{{
			Proposer: legacy, Assignee: local, Reviewer: legacy, Collaborators: []authorizationapp.SubjectRef{legacy, valid},
		}},
		Missions: []authorizationapp.MissionRelationSnapshot{{
			Owner: legacy, Owners: []authorizationapp.SubjectRef{legacy, valid}, Participants: []authorizationapp.SubjectRef{local, valid}, Automation: []authorizationapp.SubjectRef{system},
		}},
		Discussions:      []authorizationapp.DiscussionRelationSnapshot{{Owner: legacy, Members: []authorizationapp.SubjectRef{legacy, valid}}},
		Organizations:    []authorizationapp.OrganizationRelationSnapshot{{Admin: legacy}},
		Departments:      []authorizationapp.DepartmentRelationSnapshot{{Managers: []authorizationapp.SubjectRef{legacy, valid}}},
		WorkforceMembers: []authorizationapp.WorkforceMemberRelationSnapshot{{Subject: local}, {Subject: valid}},
		RoleAssignments:  []authorizationapp.RoleAssignmentRelationSnapshot{{Assignee: legacy}, {Assignee: valid}},
		ResourceBindings: []authorizationapp.ResourceBindingRelationSnapshot{
			{Subject: authorizationapp.SubjectRef{Kind: "agent", ID: agentcore.LegacyLocalAdminAgentProfileID}},
			{Subject: authorizationapp.SubjectRef{Kind: "agent", ID: "agent-valid"}},
		},
	}

	removeReservedNonWorkforceAuthorizationSubjects(&snapshot)

	if snapshot.Projects[0].Owner.ID != "" || snapshot.DevelopmentProjects[0].Owner.ID != "" || snapshot.Todos[0].Proposer.ID != "" || snapshot.Missions[0].Owner.ID != "" || snapshot.Discussions[0].Owner.ID != "" || snapshot.Organizations[0].Admin.ID != "" {
		t.Fatalf("reserved owners survived: %+v", snapshot)
	}
	if len(snapshot.DevelopmentProjects[0].Members) != 1 || len(snapshot.Todos[0].Collaborators) != 1 || len(snapshot.Missions[0].Owners) != 1 || len(snapshot.Missions[0].Participants) != 1 || len(snapshot.Discussions[0].Members) != 1 || len(snapshot.Departments[0].Managers) != 1 || len(snapshot.WorkforceMembers) != 1 || len(snapshot.RoleAssignments) != 1 || len(snapshot.ResourceBindings) != 1 {
		t.Fatalf("reserved relation subjects survived: %+v", snapshot)
	}
	if len(snapshot.Missions[0].Automation) != 1 || snapshot.Missions[0].Automation[0] != system {
		t.Fatalf("system automation principal was removed: %+v", snapshot.Missions[0].Automation)
	}
}
