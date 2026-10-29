package main

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	authorizationapp "orrery_v2/internal/app/authorization"
)

func removeReservedNonWorkforceAuthorizationSubjects(snapshot *authorizationapp.RelationBackfillSnapshot) {
	if snapshot == nil {
		return
	}
	clearReserved := func(subject *authorizationapp.SubjectRef) {
		if subject != nil && reservedAuthorizationSubject(*subject) {
			*subject = authorizationapp.SubjectRef{}
		}
	}
	filter := func(subjects []authorizationapp.SubjectRef) []authorizationapp.SubjectRef {
		out := subjects[:0]
		for _, subject := range subjects {
			if !reservedAuthorizationSubject(subject) {
				out = append(out, subject)
			}
		}
		return out
	}
	for i := range snapshot.Projects {
		clearReserved(&snapshot.Projects[i].Owner)
	}
	for i := range snapshot.DevelopmentProjects {
		clearReserved(&snapshot.DevelopmentProjects[i].Owner)
		members := snapshot.DevelopmentProjects[i].Members[:0]
		for _, member := range snapshot.DevelopmentProjects[i].Members {
			if !reservedAuthorizationSubject(member.Subject) {
				members = append(members, member)
			}
		}
		snapshot.DevelopmentProjects[i].Members = members
	}
	for i := range snapshot.SCMConnections {
		clearReserved(&snapshot.SCMConnections[i].Owner)
	}
	for i := range snapshot.Todos {
		clearReserved(&snapshot.Todos[i].Proposer)
		clearReserved(&snapshot.Todos[i].Assignee)
		clearReserved(&snapshot.Todos[i].Reviewer)
		snapshot.Todos[i].Collaborators = filter(snapshot.Todos[i].Collaborators)
	}
	for i := range snapshot.Missions {
		clearReserved(&snapshot.Missions[i].Owner)
		snapshot.Missions[i].Owners = filter(snapshot.Missions[i].Owners)
		snapshot.Missions[i].Participants = filter(snapshot.Missions[i].Participants)
		snapshot.Missions[i].Automation = filter(snapshot.Missions[i].Automation)
	}
	for i := range snapshot.Discussions {
		clearReserved(&snapshot.Discussions[i].Owner)
		snapshot.Discussions[i].Members = filter(snapshot.Discussions[i].Members)
	}
	for i := range snapshot.Organizations {
		clearReserved(&snapshot.Organizations[i].Admin)
	}
	for i := range snapshot.Departments {
		snapshot.Departments[i].Managers = filter(snapshot.Departments[i].Managers)
	}
	workforce := snapshot.WorkforceMembers[:0]
	for _, member := range snapshot.WorkforceMembers {
		if !reservedAuthorizationSubject(member.Subject) {
			workforce = append(workforce, member)
		}
	}
	snapshot.WorkforceMembers = workforce
	assignments := snapshot.RoleAssignments[:0]
	for _, assignment := range snapshot.RoleAssignments {
		if !reservedAuthorizationSubject(assignment.Assignee) {
			assignments = append(assignments, assignment)
		}
	}
	snapshot.RoleAssignments = assignments
	bindings := snapshot.ResourceBindings[:0]
	for _, binding := range snapshot.ResourceBindings {
		if !reservedAuthorizationSubject(binding.Subject) {
			bindings = append(bindings, binding)
		}
	}
	snapshot.ResourceBindings = bindings
}

func reservedAuthorizationSubject(subject authorizationapp.SubjectRef) bool {
	kind := strings.ToLower(strings.TrimSpace(subject.Kind))
	return (kind == "user" || kind == "agent") && agentcore.IsReservedNonWorkforceProfileID(subject.ID)
}
