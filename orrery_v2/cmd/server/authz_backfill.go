package main

import (
	"context"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	authorizationapp "orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/developmentproject"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/projectadmin"
	"orrery_v2/internal/app/scmconnection"
	"orrery_v2/internal/app/todo"
	"orrery_v2/internal/mission/projection"
	"orrery_v2/internal/platform"
	"orrery_v2/internal/platform/config"
)

type authorizationBackfillAgentDirectory interface {
	QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
}

type authorizationBackfillOperations interface {
	GetOrgGraph(ctx context.Context, viewer operations.ViewerRef, query operations.OrgGraphQuery) (operations.OrgGraphDTO, error)
	ListRoleAssignments(ctx context.Context, viewer operations.ViewerRef, query operations.RoleAssignmentQuery) (operations.RoleAssignmentPageDTO, error)
}

type authorizationBackfillProjectRepository interface {
	ListProjectCatalog(ctx context.Context, afterID string, limit int) ([]projectadmin.Project, bool, error)
}

type authorizationBackfillDevelopmentProjectRepository interface {
	ListCatalog(ctx context.Context, afterKey string, limit int) ([]developmentproject.Project, bool, error)
}

type authorizationBackfillSCMConnectionRepository interface {
	ListCatalog(ctx context.Context, afterKey string, limit int) ([]scmconnection.Connection, bool, error)
}

type authorizationBackfillTodoRepository interface {
	ListTodoCatalog(ctx context.Context, afterKey string, limit int) ([]todo.TodoItem, bool, error)
}

type authorizationBackfillMissionRepository interface {
	ListMissionCatalog(ctx context.Context, afterID string, limit int) ([]projection.ReadModel, bool, error)
}

type authorizationBackfillDiscussionRepository interface {
	ListConversations(context.Context) ([]discussionsession.ConversationRecordView, error)
}

type authorizationRelationBackfillOptions struct {
	Mode     string
	DryRun   bool
	Diff     bool
	Prune    bool
	Disabled bool
}

func runAuthorizationRelationBackfillFromPlatform(
	ctx context.Context,
	cfg config.Config,
	projector *authorizationapp.RelationProjector,
	missions authorizationBackfillMissionRepository,
	discussions authorizationBackfillDiscussionRepository,
	projects authorizationBackfillProjectRepository,
	developmentProjects authorizationBackfillDevelopmentProjectRepository,
	scmConnections authorizationBackfillSCMConnectionRepository,
	todos authorizationBackfillTodoRepository,
	ops authorizationBackfillOperations,
	agents authorizationBackfillAgentDirectory,
) (authorizationapp.RelationBackfillResult, error) {
	opts, err := authorizationRelationBackfillOptionsFromPlatform(cfg)
	if err != nil {
		return authorizationapp.RelationBackfillResult{}, err
	}
	return runAuthorizationRelationBackfill(ctx, projector, missions, discussions, projects, developmentProjects, scmConnections, todos, ops, agents, opts)
}

func runAuthorizationRelationBackfillFromRuntime(
	ctx context.Context,
	rt *platform.Runtime,
	recorder *authorizationapp.LoggingShadowRecorder,
	projector *authorizationapp.RelationProjector,
	missions authorizationBackfillMissionRepository,
	discussions authorizationBackfillDiscussionRepository,
	projects authorizationBackfillProjectRepository,
	developmentProjects authorizationBackfillDevelopmentProjectRepository,
	scmConnections authorizationBackfillSCMConnectionRepository,
	todos authorizationBackfillTodoRepository,
	ops authorizationBackfillOperations,
	agents authorizationBackfillAgentDirectory,
) error {
	result, err := runAuthorizationRelationBackfillFromPlatform(ctx, rt.Config, projector, missions, discussions, projects, developmentProjects, scmConnections, todos, ops, agents)
	if err != nil {
		return err
	}
	if err := registerAuthorizationRelationBackfillReadiness(rt, result); err != nil {
		return err
	}
	return registerAuthorizationEnforceTransitionReadiness(rt, recorder, result)
}

func runAuthorizationRelationBackfill(
	ctx context.Context,
	projector *authorizationapp.RelationProjector,
	missions authorizationBackfillMissionRepository,
	discussions authorizationBackfillDiscussionRepository,
	projects authorizationBackfillProjectRepository,
	developmentProjects authorizationBackfillDevelopmentProjectRepository,
	scmConnections authorizationBackfillSCMConnectionRepository,
	todos authorizationBackfillTodoRepository,
	ops authorizationBackfillOperations,
	agents authorizationBackfillAgentDirectory,
	opts authorizationRelationBackfillOptions,
) (authorizationapp.RelationBackfillResult, error) {
	if projector == nil || opts.Disabled {
		return authorizationRelationBackfillResultWithOptions(authorizationapp.RelationBackfillResult{DryRun: opts.DryRun}, opts), nil
	}
	snapshot, err := buildAuthorizationRelationBackfillSnapshot(ctx, missions, discussions, projects, developmentProjects, scmConnections, todos, ops, agents)
	if err != nil {
		return authorizationapp.RelationBackfillResult{}, err
	}
	result, err := projector.BackfillWithOptions(ctx, snapshot, authorizationapp.RelationBackfillOptions{DryRun: opts.DryRun, Diff: opts.Diff, Prune: opts.Prune})
	if err != nil {
		return authorizationapp.RelationBackfillResult{}, err
	}
	return authorizationRelationBackfillResultWithOptions(result, opts), nil
}

func authorizationRelationBackfillResultWithOptions(result authorizationapp.RelationBackfillResult, opts authorizationRelationBackfillOptions) authorizationapp.RelationBackfillResult {
	result.Mode = opts.Mode
	result.Disabled = opts.Disabled
	return result
}

func buildAuthorizationRelationBackfillSnapshot(
	ctx context.Context,
	missions authorizationBackfillMissionRepository,
	discussions authorizationBackfillDiscussionRepository,
	projects authorizationBackfillProjectRepository,
	developmentProjects authorizationBackfillDevelopmentProjectRepository,
	scmConnections authorizationBackfillSCMConnectionRepository,
	todos authorizationBackfillTodoRepository,
	ops authorizationBackfillOperations,
	agents authorizationBackfillAgentDirectory,
) (authorizationapp.RelationBackfillSnapshot, error) {
	var snapshot authorizationapp.RelationBackfillSnapshot
	profileIDByUsername := map[string]string{}
	if projects != nil {
		const pageSize = 200
		after := ""
		for {
			items, hasMore, err := projects.ListProjectCatalog(ctx, after, pageSize)
			if err != nil {
				return authorizationapp.RelationBackfillSnapshot{}, err
			}
			for _, project := range items {
				after = project.TenantID + "/" + project.ProjectID
				if strings.TrimSpace(project.ProjectID) == "" || strings.TrimSpace(project.CreatedBy) == "" {
					continue
				}
				snapshot.Projects = append(snapshot.Projects, authorizationapp.ProjectRelationSnapshot{
					ProjectID:      project.ProjectID,
					OrganizationID: project.OrganizationID,
					TenantID:       project.TenantID,
					Owner:          authorizationapp.SubjectRef{Kind: "user", ID: project.CreatedBy},
				})
			}
			if !hasMore {
				break
			}
			if len(items) == 0 {
				return authorizationapp.RelationBackfillSnapshot{}, fmt.Errorf("project authorization backfill catalog returned an empty continuation page")
			}
		}
	}
	if developmentProjects != nil {
		const pageSize = 200
		after := ""
		for {
			items, hasMore, err := developmentProjects.ListCatalog(ctx, after, pageSize)
			if err != nil {
				return authorizationapp.RelationBackfillSnapshot{}, err
			}
			for _, project := range items {
				after = project.TenantID + "/" + project.DevelopmentProjectID
				if strings.TrimSpace(project.DevelopmentProjectID) == "" || strings.TrimSpace(project.CreatedBy) == "" {
					continue
				}
				snapshotProject := authorizationapp.DevelopmentProjectRelationSnapshot{
					ProjectID: project.DevelopmentProjectID, OrganizationID: project.OrganizationID, TenantID: project.TenantID,
				}
				hasOwner := false
				for _, member := range project.Members {
					if strings.TrimSpace(member.SubjectKind) == "" || strings.TrimSpace(member.SubjectID) == "" || strings.TrimSpace(member.Role) == "" {
						continue
					}
					snapshotProject.Members = append(snapshotProject.Members, authorizationapp.DevelopmentProjectMemberRelationSnapshot{
						Subject: authorizationapp.SubjectRef{Kind: member.SubjectKind, ID: member.SubjectID}, Relation: member.Role,
					})
					if member.Role == developmentproject.MemberRoleOwner {
						hasOwner = true
					}
				}
				if !hasOwner {
					snapshotProject.Owner = authorizationapp.SubjectRef{Kind: "user", ID: project.CreatedBy}
				}
				for _, binding := range project.RepositoryBindings {
					if connectionID := strings.TrimSpace(binding.ConnectionID); connectionID != "" {
						snapshotProject.ConnectionIDs = append(snapshotProject.ConnectionIDs, connectionID)
					}
				}
				snapshot.DevelopmentProjects = append(snapshot.DevelopmentProjects, snapshotProject)
			}
			if !hasMore {
				break
			}
			if len(items) == 0 {
				return authorizationapp.RelationBackfillSnapshot{}, fmt.Errorf("development project authorization backfill catalog returned an empty continuation page")
			}
		}
	}
	if scmConnections != nil {
		const pageSize = 200
		after := ""
		for {
			items, hasMore, err := scmConnections.ListCatalog(ctx, after, pageSize)
			if err != nil {
				return authorizationapp.RelationBackfillSnapshot{}, err
			}
			for _, connection := range items {
				after = connection.TenantID + "/" + connection.ConnectionID
				if strings.TrimSpace(connection.ConnectionID) == "" || strings.TrimSpace(connection.OwnerID) == "" {
					continue
				}
				snapshot.SCMConnections = append(snapshot.SCMConnections, authorizationapp.SCMConnectionRelationSnapshot{
					ConnectionID: connection.ConnectionID, OrganizationID: connection.OrganizationID, TenantID: connection.TenantID,
					Owner: authorizationapp.SubjectRef{Kind: "user", ID: connection.OwnerID},
				})
			}
			if !hasMore {
				break
			}
			if len(items) == 0 {
				return authorizationapp.RelationBackfillSnapshot{}, fmt.Errorf("SCM connection authorization backfill catalog returned an empty continuation page")
			}
		}
	}
	if todos != nil {
		const pageSize = 200
		after := ""
		for {
			items, hasMore, err := todos.ListTodoCatalog(ctx, after, pageSize)
			if err != nil {
				return authorizationapp.RelationBackfillSnapshot{}, err
			}
			for _, item := range items {
				after = strings.TrimSpace(item.TenantID) + "::" + strings.TrimSpace(item.TodoID)
				if strings.TrimSpace(item.TodoID) == "" {
					continue
				}
				proposerID := strings.TrimSpace(item.CreatedBySubjectID)
				if proposerID == "" {
					proposerID = strings.TrimSpace(item.CreatedBy)
				}
				snapshot.Todos = append(snapshot.Todos, authorizationapp.TodoRelationSnapshot{
					TodoID: item.TodoID, TenantID: item.TenantID,
					DevelopmentProjectID: item.DevelopmentProjectID,
					Proposer:             relationSubjectFromTodoActor(item.CreatedByKind, proposerID),
					Assignee:             relationSubjectFromTodoAssignee(item),
					Reviewer:             authorizationapp.SubjectRef{Kind: "user", ID: strings.TrimSpace(item.ReviewerID)},
				})
			}
			if !hasMore {
				break
			}
			if len(items) == 0 {
				return authorizationapp.RelationBackfillSnapshot{}, fmt.Errorf("Todo authorization backfill catalog returned an empty continuation page")
			}
		}
	}
	if missions != nil {
		const pageSize = 200
		after := ""
		for {
			models, hasMore, err := missions.ListMissionCatalog(ctx, after, pageSize)
			if err != nil {
				return authorizationapp.RelationBackfillSnapshot{}, err
			}
			for _, model := range models {
				tenantID := missionBackfillTenantID(model)
				after = tenantID + "/" + model.Mission.MissionID.String()
				if model.Mission.MissionID.IsZero() {
					continue
				}
				missionSnapshot := authorizationapp.MissionRelationSnapshot{
					MissionID: model.Mission.MissionID.String(), TenantID: tenantID,
					Owner: relationSubjectFromMissionOwner(model), Owners: relationControlOwnersFromTeamMembers(model.Team.Members),
					Participants: relationSubjectsFromTeamMembers(model.Team.Members),
					OriginTodoID: model.Mission.OriginTodoID, OriginDevelopmentProjectID: model.Mission.OriginDevelopmentProjectID,
				}
				missionSnapshot.Automation = append(missionSnapshot.Automation, authorizationapp.SubjectRef{Kind: "system", ID: qaPlanSystemViewerID})
				if missionSnapshot.Owner.Kind == "system" && missionSnapshot.Owner.ID != "" {
					missionSnapshot.Automation = appendUniqueAuthorizationSubject(missionSnapshot.Automation, missionSnapshot.Owner)
				}
				snapshot.Missions = append(snapshot.Missions, missionSnapshot)
				if discussionID := strings.TrimSpace(model.Discussion.DiscussionGroupID); discussionID != "" && strings.TrimSpace(model.Mission.OriginTodoID) == "" {
					discussionOwner := missionSnapshot.Owner
					if discussionOwner.Kind == "system" {
						discussionOwner = authorizationapp.SubjectRef{}
					}
					snapshot.Discussions = append(snapshot.Discussions, authorizationapp.DiscussionRelationSnapshot{
						DiscussionID: discussionID,
						MissionID:    model.Mission.MissionID.String(),
						TenantID:     tenantID,
						Owner:        discussionOwner,
						Members:      relationSubjectsFromTeamMembers(model.Team.Members),
					})
				}
			}
			if !hasMore {
				break
			}
			if len(models) == 0 {
				return authorizationapp.RelationBackfillSnapshot{}, fmt.Errorf("mission authorization backfill catalog returned an empty continuation page")
			}
		}
	}
	if ops != nil {
		viewer := operations.ViewerRef{ViewerID: "authorization_backfill", ViewerKind: "system", PermissionScope: []string{
			operations.PermissionOperationsRead,
			operations.PermissionOrgRead,
			operations.PermissionWorkforceRead,
			operations.PermissionRolesRead,
		}}
		graph, err := ops.GetOrgGraph(ctx, viewer, operations.OrgGraphQuery{IncludeMembers: true})
		if err != nil {
			return authorizationapp.RelationBackfillSnapshot{}, err
		}
		for _, organization := range graph.Organizations {
			if strings.TrimSpace(organization.OrganizationID) == "" || strings.TrimSpace(organization.OwnerProfileID) == "" {
				continue
			}
			snapshot.Organizations = append(snapshot.Organizations, authorizationapp.OrganizationRelationSnapshot{
				OrganizationID: organization.OrganizationID,
				TenantID:       organization.OrganizationID,
				Admin:          authorizationapp.SubjectRef{Kind: "user", ID: organization.OwnerProfileID},
			})
		}
		for _, department := range graph.Departments {
			if strings.TrimSpace(department.DepartmentID) == "" || strings.TrimSpace(department.OrganizationID) == "" {
				continue
			}
			departmentSnapshot := authorizationapp.DepartmentRelationSnapshot{
				DepartmentID:   department.DepartmentID,
				OrganizationID: department.OrganizationID,
				TenantID:       department.OrganizationID,
			}
			if strings.TrimSpace(department.OwnerProfileID) != "" {
				departmentSnapshot.Managers = append(departmentSnapshot.Managers, authorizationapp.SubjectRef{Kind: "user", ID: department.OwnerProfileID})
			}
			snapshot.Departments = append(snapshot.Departments, departmentSnapshot)
		}
		for _, member := range graph.Members {
			if username := strings.ToLower(strings.TrimSpace(member.UserAccount.Username)); username != "" && strings.TrimSpace(member.AgentProfileID) != "" {
				profileIDByUsername[username] = strings.TrimSpace(member.AgentProfileID)
			}
			if strings.TrimSpace(member.AgentProfileID) == "" || strings.TrimSpace(member.OrganizationID) == "" || strings.TrimSpace(member.DepartmentID) == "" {
				continue
			}
			snapshot.WorkforceMembers = append(snapshot.WorkforceMembers, authorizationapp.WorkforceMemberRelationSnapshot{
				Subject:        relationSubjectFromWorkforceMember(member),
				OrganizationID: member.OrganizationID,
				DepartmentID:   member.DepartmentID,
				TenantID:       member.OrganizationID,
				RoleIDs:        append([]string(nil), member.RoleIDs...),
			})
		}
		assignments, err := ops.ListRoleAssignments(ctx, viewer, operations.RoleAssignmentQuery{})
		if err != nil {
			return authorizationapp.RelationBackfillSnapshot{}, err
		}
		organizationByNode := organizationIDsByNode(graph)
		for _, assignment := range assignments.Items {
			organizationID := organizationByNode[assignment.OrganizationNodeID]
			if assignment.ScopeMode == operations.RoleScopePlatform {
				organizationID = "global"
			}
			assignee := relationSubjectFromRoleActor(assignment.Actor)
			if organizationID == "" || assignee.ID == "" || strings.TrimSpace(assignment.RoleID) == "" {
				continue
			}
			snapshot.RoleAssignments = append(snapshot.RoleAssignments, authorizationapp.RoleAssignmentRelationSnapshot{
				AssignmentID:       assignment.AssignmentID,
				Assignee:           assignee,
				RoleID:             assignment.RoleID,
				OrganizationID:     organizationID,
				OrganizationNodeID: assignment.OrganizationNodeID,
				ScopeMode:          assignment.ScopeMode,
				TenantID:           organizationID,
				StartsAt:           assignment.StartsAt,
				ExpiresAt:          assignment.ExpiresAt,
			})
		}
	}
	if err := appendDiscussionAuthorizationBackfillSnapshot(ctx, &snapshot, discussions); err != nil {
		return authorizationapp.RelationBackfillSnapshot{}, err
	}
	canonicalizeHumanRelationSubjects(&snapshot, profileIDByUsername)
	if agents != nil {
		page, err := agents.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{IncludeAI: true, IncludeHumans: true})
		if err != nil {
			return authorizationapp.RelationBackfillSnapshot{}, err
		}
		for _, entry := range page.Entries {
			subject := relationSubjectFromAgentDirectoryEntry(entry)
			if subject.ID == "" {
				continue
			}
			for _, ref := range entry.ToolRefs {
				resourceID := strings.TrimSpace(string(ref.ToolID))
				if resourceID == "" {
					continue
				}
				snapshot.ResourceBindings = append(snapshot.ResourceBindings, authorizationapp.ResourceBindingRelationSnapshot{Subject: subject, ResourceID: resourceID})
			}
			for _, ref := range entry.SkillRefs {
				resourceID := strings.TrimSpace(string(ref.SkillID))
				if resourceID == "" {
					continue
				}
				snapshot.ResourceBindings = append(snapshot.ResourceBindings, authorizationapp.ResourceBindingRelationSnapshot{Subject: subject, ResourceID: resourceID})
			}
		}
	}
	removeReservedNonWorkforceAuthorizationSubjects(&snapshot)
	return snapshot, nil
}

func appendDiscussionAuthorizationBackfillSnapshot(ctx context.Context, snapshot *authorizationapp.RelationBackfillSnapshot, discussions authorizationBackfillDiscussionRepository) error {
	if snapshot == nil || discussions == nil {
		return nil
	}
	conversations, err := discussions.ListConversations(ctx)
	if err != nil {
		return err
	}
	for _, conversation := range conversations {
		discussion := authorizationapp.DiscussionRelationSnapshot{
			DiscussionID: strings.TrimSpace(conversation.ConversationID),
			TenantID:     firstNonBlank(strings.TrimSpace(conversation.TenantID), "global"),
		}
		if discussion.DiscussionID == "" {
			continue
		}
		creatorID := strings.TrimSpace(conversation.CreatedBy)
		for _, member := range conversation.Members {
			subject := discussionAuthorizationBackfillSubject(member.MemberKind, member.MemberID)
			if subject.Kind == "" || subject.ID == "" {
				continue
			}
			discussion.Members = appendUniqueAuthorizationSubject(discussion.Members, subject)
			if subject.ID == creatorID {
				discussion.Owner = subject
			}
		}
		for _, link := range conversation.LinkedMissions {
			if missionID := strings.TrimSpace(link.MissionID); missionID != "" {
				discussion.MissionID = missionID
				break
			}
		}
		mergeDiscussionAuthorizationBackfillSnapshot(&snapshot.Discussions, discussion)
	}
	return nil
}

func mergeDiscussionAuthorizationBackfillSnapshot(discussions *[]authorizationapp.DiscussionRelationSnapshot, candidate authorizationapp.DiscussionRelationSnapshot) {
	if discussions == nil || strings.TrimSpace(candidate.DiscussionID) == "" {
		return
	}
	candidate.TenantID = firstNonBlank(strings.TrimSpace(candidate.TenantID), "global")
	for index := range *discussions {
		existing := &(*discussions)[index]
		existingTenantID := firstNonBlank(strings.TrimSpace(existing.TenantID), "global")
		if existingTenantID != candidate.TenantID || strings.TrimSpace(existing.DiscussionID) != strings.TrimSpace(candidate.DiscussionID) {
			continue
		}
		existing.TenantID = existingTenantID
		if existing.MissionID == "" {
			existing.MissionID = candidate.MissionID
		}
		if existing.Owner.Kind == "" || existing.Owner.ID == "" {
			existing.Owner = candidate.Owner
		}
		for _, member := range candidate.Members {
			existing.Members = appendUniqueAuthorizationSubject(existing.Members, member)
		}
		return
	}
	*discussions = append(*discussions, candidate)
}

func discussionAuthorizationBackfillSubject(kind, id string) authorizationapp.SubjectRef {
	kind = strings.ToLower(strings.TrimSpace(kind))
	id = strings.TrimSpace(id)
	if id == "" || kind != "user" && kind != "agent" {
		return authorizationapp.SubjectRef{}
	}
	return authorizationapp.SubjectRef{Kind: kind, ID: id}
}

func appendUniqueAuthorizationSubject(subjects []authorizationapp.SubjectRef, subject authorizationapp.SubjectRef) []authorizationapp.SubjectRef {
	for _, existing := range subjects {
		if existing == subject {
			return subjects
		}
	}
	return append(subjects, subject)
}

func missionBackfillTenantID(model projection.ReadModel) string {
	if tenantID := strings.TrimSpace(model.TenantID); tenantID != "" {
		return tenantID
	}
	if tenantID := strings.TrimSpace(model.Mission.TenantID); tenantID != "" {
		return tenantID
	}
	return "global"
}

func relationSubjectFromMissionOwner(model projection.ReadModel) authorizationapp.SubjectRef {
	id := strings.TrimSpace(model.Mission.CreatedByID)
	if id == "" {
		return authorizationapp.SubjectRef{}
	}
	kind := strings.TrimSpace(model.Mission.CreatedByKind)
	if kind == "" {
		kind = "user"
	}
	return authorizationapp.SubjectRef{Kind: kind, ID: id}
}

func relationSubjectsFromTeamMembers(members []projection.TeamMemberView) []authorizationapp.SubjectRef {
	out := make([]authorizationapp.SubjectRef, 0, len(members))
	for _, member := range members {
		id := strings.TrimSpace(member.AgentID.String())
		if member.AgentKind == agentcore.AgentKindHuman {
			id = firstNonBlank(strings.TrimSpace(member.AgentProfileID.String()), id)
		} else if id == "" {
			id = strings.TrimSpace(member.AgentProfileID.String())
		}
		if id == "" {
			continue
		}
		kind := "agent"
		if member.AgentKind == agentcore.AgentKindHuman {
			kind = "user"
		}
		out = append(out, authorizationapp.SubjectRef{Kind: kind, ID: id})
	}
	return out
}

func relationControlOwnersFromTeamMembers(members []projection.TeamMemberView) []authorizationapp.SubjectRef {
	owners := make([]authorizationapp.SubjectRef, 0, 2)
	for _, member := range members {
		if member.MissionRole != "Lead" && member.MissionRole != "RealHumanContributor" {
			continue
		}
		subjects := relationSubjectsFromTeamMembers([]projection.TeamMemberView{member})
		if len(subjects) != 1 {
			continue
		}
		owners = appendUniqueAuthorizationSubject(owners, subjects[0])
	}
	return owners
}

func canonicalizeHumanRelationSubjects(snapshot *authorizationapp.RelationBackfillSnapshot, profileIDByUsername map[string]string) {
	if snapshot == nil || len(profileIDByUsername) == 0 {
		return
	}
	canonicalize := func(subject *authorizationapp.SubjectRef) {
		if subject == nil || subject.Kind != "user" {
			return
		}
		if profileID := profileIDByUsername[strings.ToLower(strings.TrimSpace(subject.ID))]; profileID != "" {
			subject.ID = profileID
		}
	}
	for i := range snapshot.Projects {
		canonicalize(&snapshot.Projects[i].Owner)
	}
	for i := range snapshot.DevelopmentProjects {
		canonicalize(&snapshot.DevelopmentProjects[i].Owner)
		for j := range snapshot.DevelopmentProjects[i].Members {
			canonicalize(&snapshot.DevelopmentProjects[i].Members[j].Subject)
		}
	}
	for i := range snapshot.SCMConnections {
		canonicalize(&snapshot.SCMConnections[i].Owner)
	}
	for i := range snapshot.Todos {
		canonicalize(&snapshot.Todos[i].Proposer)
		canonicalize(&snapshot.Todos[i].Assignee)
		canonicalize(&snapshot.Todos[i].Reviewer)
		for j := range snapshot.Todos[i].Collaborators {
			canonicalize(&snapshot.Todos[i].Collaborators[j])
		}
	}
	for i := range snapshot.Missions {
		canonicalize(&snapshot.Missions[i].Owner)
		for j := range snapshot.Missions[i].Owners {
			canonicalize(&snapshot.Missions[i].Owners[j])
		}
		for j := range snapshot.Missions[i].Participants {
			canonicalize(&snapshot.Missions[i].Participants[j])
		}
	}
	for i := range snapshot.Discussions {
		canonicalize(&snapshot.Discussions[i].Owner)
		for j := range snapshot.Discussions[i].Members {
			canonicalize(&snapshot.Discussions[i].Members[j])
		}
	}
}

func relationSubjectFromTodoActor(kind todo.ViewerKind, id string) authorizationapp.SubjectRef {
	id = strings.TrimSpace(id)
	if id == "" {
		return authorizationapp.SubjectRef{}
	}
	if kind == todo.ViewerKindSystem {
		return authorizationapp.SubjectRef{}
	}
	if kind == todo.ViewerKindAgent {
		return authorizationapp.SubjectRef{Kind: "agent", ID: id}
	}
	return authorizationapp.SubjectRef{Kind: "user", ID: id}
}

func relationSubjectFromTodoAssignee(item todo.TodoItem) authorizationapp.SubjectRef {
	id := strings.TrimSpace(item.AssigneeID)
	if id == "" {
		return authorizationapp.SubjectRef{}
	}
	if item.AssigneeKind == todo.AssigneeKindAgent {
		return authorizationapp.SubjectRef{Kind: "agent", ID: id}
	}
	return authorizationapp.SubjectRef{Kind: "user", ID: id}
}

func relationSubjectFromWorkforceMember(member operations.WorkforceMemberDTO) authorizationapp.SubjectRef {
	id := strings.TrimSpace(member.AgentProfileID)
	if id == "" {
		id = strings.TrimSpace(member.AgentID)
	}
	if id == "" {
		return authorizationapp.SubjectRef{}
	}
	if strings.TrimSpace(member.AgentKind) == string(agentcore.AgentKindHuman) {
		return authorizationapp.SubjectRef{Kind: "user", ID: id}
	}
	return authorizationapp.SubjectRef{Kind: "agent", ID: id}
}

func relationSubjectFromAgentDirectoryEntry(entry agentcore.AgentDirectoryEntry) authorizationapp.SubjectRef {
	return authorizationapp.ResolveResourceBindingSubject(
		entry.AgentProfileID.String(),
		entry.AgentInstanceID.String(),
		entry.AgentKind == agentcore.AgentKindHuman,
	)
}

func relationSubjectFromRoleActor(actor operations.ActorRefDTO) authorizationapp.SubjectRef {
	id := strings.TrimSpace(actor.ActorID)
	if id == "" {
		return authorizationapp.SubjectRef{}
	}
	switch strings.ToLower(strings.TrimSpace(actor.ActorType)) {
	case "human", "user":
		return authorizationapp.SubjectRef{Kind: "user", ID: id}
	case "agent", "ai":
		return authorizationapp.SubjectRef{Kind: "agent", ID: id}
	default:
		return authorizationapp.SubjectRef{}
	}
}

func organizationIDsByNode(graph operations.OrgGraphDTO) map[string]string {
	out := map[string]string{}
	for _, organization := range graph.Organizations {
		if organization.OrganizationID != "" {
			out[organization.OrganizationID] = organization.OrganizationID
		}
	}
	for _, department := range graph.Departments {
		if department.DepartmentID != "" && department.OrganizationID != "" {
			out[department.DepartmentID] = department.OrganizationID
		}
	}
	return out
}
