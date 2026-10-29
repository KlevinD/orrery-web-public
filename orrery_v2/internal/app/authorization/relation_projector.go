package authorization

import (
	"context"
	"errors"
	"strings"
	"time"

	"orrery_v2/internal/app/identity"
)

type RelationSink interface {
	WriteRelations(ctx context.Context, tuples []RelationTuple) error
	DeleteRelations(ctx context.Context, tuples []RelationTuple) error
}

type RelationProjector struct {
	sink       RelationSink
	bestEffort bool
	now        func() time.Time
}

func NewRelationProjector(sink RelationSink) *RelationProjector {
	return &RelationProjector{sink: sink, now: time.Now}
}

func (p *RelationProjector) WithBestEffort() *RelationProjector {
	if p == nil {
		return nil
	}
	cp := *p
	cp.bestEffort = true
	return &cp
}

func (p *RelationProjector) WriteProjectCreated(ctx context.Context, owner SubjectRef, projectID string, organizationID string, tenantID string) error {
	tuples := []RelationTuple{
		p.tuple(tenantID, owner, "owner", identity.ObjectRef{Type: "project", ID: projectID, TenantID: tenantID}),
	}
	if organizationID := strings.TrimSpace(organizationID); organizationID != "" {
		tuples = append(tuples, p.tuple(tenantID, SubjectRef{Kind: "organization", ID: organizationID}, "org", identity.ObjectRef{Type: "project", ID: projectID, TenantID: tenantID}))
	}
	return p.write(ctx, tuples)
}

func (p *RelationProjector) DeleteProjectCreated(ctx context.Context, owner SubjectRef, projectID string, organizationID string, tenantID string) error {
	tuples := []RelationTuple{
		p.tuple(tenantID, owner, "owner", identity.ObjectRef{Type: "project", ID: projectID, TenantID: tenantID}),
	}
	if organizationID := strings.TrimSpace(organizationID); organizationID != "" {
		tuples = append(tuples, p.tuple(tenantID, SubjectRef{Kind: "organization", ID: organizationID}, "org", identity.ObjectRef{Type: "project", ID: projectID, TenantID: tenantID}))
	}
	return p.delete(ctx, tuples)
}

func (p *RelationProjector) WriteDevelopmentProjectCreated(ctx context.Context, owner SubjectRef, projectID string, organizationID string, tenantID string) error {
	object := identity.ObjectRef{Type: "development_project", ID: projectID, TenantID: tenantID}
	tuples := []RelationTuple{p.tuple(tenantID, owner, "owner", object)}
	if organizationID = strings.TrimSpace(organizationID); organizationID != "" {
		tuples = append(tuples, p.tuple(tenantID, SubjectRef{Kind: "organization", ID: organizationID}, "org", object))
	}
	return p.write(ctx, tuples)
}

func (p *RelationProjector) DeleteDevelopmentProjectCreated(ctx context.Context, owner SubjectRef, projectID string, organizationID string, tenantID string) error {
	object := identity.ObjectRef{Type: "development_project", ID: projectID, TenantID: tenantID}
	tuples := []RelationTuple{p.tuple(tenantID, owner, "owner", object)}
	if organizationID = strings.TrimSpace(organizationID); organizationID != "" {
		tuples = append(tuples, p.tuple(tenantID, SubjectRef{Kind: "organization", ID: organizationID}, "org", object))
	}
	return p.delete(ctx, tuples)
}

func (p *RelationProjector) WriteDevelopmentProjectMember(ctx context.Context, member SubjectRef, relation string, projectID string, tenantID string) error {
	return p.write(ctx, []RelationTuple{p.tuple(tenantID, member, relation, identity.ObjectRef{Type: "development_project", ID: projectID, TenantID: tenantID})})
}

func (p *RelationProjector) DeleteDevelopmentProjectMember(ctx context.Context, member SubjectRef, relation string, projectID string, tenantID string) error {
	return p.delete(ctx, []RelationTuple{p.tuple(tenantID, member, relation, identity.ObjectRef{Type: "development_project", ID: projectID, TenantID: tenantID})})
}

func (p *RelationProjector) WriteSCMConnectionCreated(ctx context.Context, owner SubjectRef, connectionID string, organizationID string, tenantID string) error {
	object := identity.ObjectRef{Type: "scm_connection", ID: connectionID, TenantID: tenantID}
	tuples := []RelationTuple{
		p.tuple(tenantID, owner, "owner", object),
		p.tuple(tenantID, owner, "manager", object),
		p.tuple(tenantID, owner, "user", object),
	}
	if organizationID = strings.TrimSpace(organizationID); organizationID != "" {
		tuples = append(tuples, p.tuple(tenantID, SubjectRef{Kind: "organization", ID: organizationID}, "org", object))
	}
	return p.write(ctx, tuples)
}

func (p *RelationProjector) DeleteSCMConnectionCreated(ctx context.Context, owner SubjectRef, connectionID string, organizationID string, tenantID string) error {
	object := identity.ObjectRef{Type: "scm_connection", ID: connectionID, TenantID: tenantID}
	tuples := []RelationTuple{
		p.tuple(tenantID, owner, "owner", object),
		p.tuple(tenantID, owner, "manager", object),
		p.tuple(tenantID, owner, "user", object),
	}
	if organizationID = strings.TrimSpace(organizationID); organizationID != "" {
		tuples = append(tuples, p.tuple(tenantID, SubjectRef{Kind: "organization", ID: organizationID}, "org", object))
	}
	return p.delete(ctx, tuples)
}

func (p *RelationProjector) WriteSCMConnectionUser(ctx context.Context, user SubjectRef, connectionID string, tenantID string) error {
	return p.write(ctx, []RelationTuple{p.tuple(tenantID, user, "user", identity.ObjectRef{Type: "scm_connection", ID: connectionID, TenantID: tenantID})})
}

func (p *RelationProjector) DeleteSCMConnectionUser(ctx context.Context, user SubjectRef, connectionID string, tenantID string) error {
	return p.delete(ctx, []RelationTuple{p.tuple(tenantID, user, "user", identity.ObjectRef{Type: "scm_connection", ID: connectionID, TenantID: tenantID})})
}

func (p *RelationProjector) WriteTodoRelations(ctx context.Context, todoID string, tenantID string, relations map[string][]SubjectRef) error {
	tuples := make([]RelationTuple, 0)
	object := identity.ObjectRef{Type: "todo", ID: todoID, TenantID: tenantID}
	for relation, subjects := range relations {
		for _, subject := range subjects {
			tuples = append(tuples, p.tuple(tenantID, subject, relation, object))
		}
	}
	return p.write(ctx, tuples)
}

func (p *RelationProjector) DeleteTodoRelations(ctx context.Context, todoID string, tenantID string, relations map[string][]SubjectRef) error {
	tuples := make([]RelationTuple, 0)
	object := identity.ObjectRef{Type: "todo", ID: todoID, TenantID: tenantID}
	for relation, subjects := range relations {
		for _, subject := range subjects {
			tuples = append(tuples, p.tuple(tenantID, subject, relation, object))
		}
	}
	return p.delete(ctx, tuples)
}

func (p *RelationProjector) WriteMissionCreated(ctx context.Context, owner SubjectRef, missionID string, tenantID string, participants []SubjectRef) error {
	tuples := []RelationTuple{
		p.tuple(tenantID, owner, "owner", identity.ObjectRef{Type: "mission", ID: missionID, TenantID: tenantID}),
	}
	for _, participant := range participants {
		tuples = append(tuples, p.tuple(tenantID, participant, "participant", identity.ObjectRef{Type: "mission", ID: missionID, TenantID: tenantID}))
	}
	return p.write(ctx, tuples)
}

func (p *RelationProjector) WriteMissionParticipants(ctx context.Context, missionID string, tenantID string, participants []SubjectRef) error {
	tuples := make([]RelationTuple, 0, len(participants))
	for _, participant := range participants {
		tuples = append(tuples, p.tuple(tenantID, participant, "participant", identity.ObjectRef{Type: "mission", ID: missionID, TenantID: tenantID}))
	}
	return p.write(ctx, tuples)
}

func (p *RelationProjector) WriteMissionParticipant(ctx context.Context, participant SubjectRef, missionID string, tenantID string) error {
	return p.write(ctx, []RelationTuple{
		p.tuple(tenantID, participant, "participant", identity.ObjectRef{Type: "mission", ID: missionID, TenantID: tenantID}),
	})
}

func (p *RelationProjector) WriteMissionOwner(ctx context.Context, owner SubjectRef, missionID string, tenantID string) error {
	return p.write(ctx, []RelationTuple{
		p.tuple(tenantID, owner, "owner", identity.ObjectRef{Type: "mission", ID: missionID, TenantID: tenantID}),
	})
}

func (p *RelationProjector) DeleteMissionOwner(ctx context.Context, owner SubjectRef, missionID string, tenantID string) error {
	return p.delete(ctx, []RelationTuple{
		p.tuple(tenantID, owner, "owner", identity.ObjectRef{Type: "mission", ID: missionID, TenantID: tenantID}),
	})
}

func (p *RelationProjector) WriteMissionAutomation(ctx context.Context, automation SubjectRef, missionID string, tenantID string) error {
	return p.write(ctx, []RelationTuple{
		p.tuple(tenantID, automation, "automation", identity.ObjectRef{Type: "mission", ID: missionID, TenantID: tenantID}),
	})
}

func (p *RelationProjector) WriteMissionOrigins(ctx context.Context, missionID, tenantID, todoID, developmentProjectID string) error {
	object := identity.ObjectRef{Type: "mission", ID: missionID, TenantID: tenantID}
	tup := make([]RelationTuple, 0, 2)
	if todoID = strings.TrimSpace(todoID); todoID != "" {
		tup = append(tup, p.tuple(tenantID, SubjectRef{Kind: "todo", ID: todoID}, "origin_todo", object))
	}
	if developmentProjectID = strings.TrimSpace(developmentProjectID); developmentProjectID != "" {
		tup = append(tup, p.tuple(tenantID, SubjectRef{Kind: "development_project", ID: developmentProjectID}, "origin_development_project", object))
	}
	return p.write(ctx, tup)
}

func (p *RelationProjector) DeleteMissionOrigins(ctx context.Context, missionID, tenantID, todoID, developmentProjectID string) error {
	object := identity.ObjectRef{Type: "mission", ID: missionID, TenantID: tenantID}
	tup := make([]RelationTuple, 0, 2)
	if todoID = strings.TrimSpace(todoID); todoID != "" {
		tup = append(tup, p.tuple(tenantID, SubjectRef{Kind: "todo", ID: todoID}, "origin_todo", object))
	}
	if developmentProjectID = strings.TrimSpace(developmentProjectID); developmentProjectID != "" {
		tup = append(tup, p.tuple(tenantID, SubjectRef{Kind: "development_project", ID: developmentProjectID}, "origin_development_project", object))
	}
	return p.delete(ctx, tup)
}

func (p *RelationProjector) WriteDiscussionCreated(ctx context.Context, owner SubjectRef, discussionID string, missionID string, tenantID string, members []SubjectRef) error {
	object := identity.ObjectRef{Type: "discussion", ID: discussionID, TenantID: tenantID}
	tuples := []RelationTuple{p.tuple(tenantID, SubjectRef{Kind: "mission", ID: missionID}, "mission", object)}
	if ownerKind := normalizeKind(owner.Kind); (ownerKind == "user" || ownerKind == "agent") && strings.TrimSpace(owner.ID) != "" {
		tuples = append(tuples, p.tuple(tenantID, owner, "owner", object))
	}
	for _, member := range members {
		tuples = append(tuples, p.tuple(tenantID, member, "member", object))
	}
	return p.write(ctx, tuples)
}

func (p *RelationProjector) WriteDiscussionMember(ctx context.Context, member SubjectRef, discussionID string, tenantID string) error {
	return p.write(ctx, []RelationTuple{
		p.tuple(tenantID, member, "member", identity.ObjectRef{Type: "discussion", ID: discussionID, TenantID: tenantID}),
	})
}

func (p *RelationProjector) DeleteDiscussionMember(ctx context.Context, member SubjectRef, discussionID string, tenantID string) error {
	return p.delete(ctx, []RelationTuple{
		p.tuple(tenantID, member, "member", identity.ObjectRef{Type: "discussion", ID: discussionID, TenantID: tenantID}),
	})
}

func (p *RelationProjector) DeleteMissionRelations(ctx context.Context, owner SubjectRef, missionID string, tenantID string, participants []SubjectRef) error {
	tuples := []RelationTuple{
		p.tuple(tenantID, owner, "owner", identity.ObjectRef{Type: "mission", ID: missionID, TenantID: tenantID}),
	}
	for _, participant := range participants {
		tuples = append(tuples, p.tuple(tenantID, participant, "participant", identity.ObjectRef{Type: "mission", ID: missionID, TenantID: tenantID}))
	}
	return p.delete(ctx, tuples)
}

func (p *RelationProjector) DeleteMissionParticipants(ctx context.Context, missionID string, tenantID string, participants []SubjectRef) error {
	tuples := make([]RelationTuple, 0, len(participants))
	for _, participant := range participants {
		tuples = append(tuples, p.tuple(tenantID, participant, "participant", identity.ObjectRef{Type: "mission", ID: missionID, TenantID: tenantID}))
	}
	return p.delete(ctx, tuples)
}

func (p *RelationProjector) DeleteMissionAutomation(ctx context.Context, automation SubjectRef, missionID string, tenantID string) error {
	return p.delete(ctx, []RelationTuple{
		p.tuple(tenantID, automation, "automation", identity.ObjectRef{Type: "mission", ID: missionID, TenantID: tenantID}),
	})
}

func (p *RelationProjector) DeleteDiscussionRelations(ctx context.Context, owner SubjectRef, discussionID string, missionID string, tenantID string, members []SubjectRef) error {
	object := identity.ObjectRef{Type: "discussion", ID: discussionID, TenantID: tenantID}
	tuples := []RelationTuple{p.tuple(tenantID, SubjectRef{Kind: "mission", ID: missionID}, "mission", object)}
	if ownerKind := normalizeKind(owner.Kind); (ownerKind == "user" || ownerKind == "agent") && strings.TrimSpace(owner.ID) != "" {
		tuples = append(tuples, p.tuple(tenantID, owner, "owner", object))
	}
	for _, member := range members {
		tuples = append(tuples, p.tuple(tenantID, member, "member", object))
	}
	return p.delete(ctx, tuples)
}

func (p *RelationProjector) WriteOrganizationCreated(ctx context.Context, admin SubjectRef, organizationID string, tenantID string) error {
	return p.write(ctx, []RelationTuple{
		p.tuple(tenantID, admin, "admin", identity.ObjectRef{Type: "organization", ID: organizationID, TenantID: tenantID}),
	})
}

func (p *RelationProjector) WriteOrganizationAdmin(ctx context.Context, admin SubjectRef, organizationID string, tenantID string) error {
	return p.write(ctx, []RelationTuple{
		p.tuple(tenantID, admin, "admin", identity.ObjectRef{Type: "organization", ID: organizationID, TenantID: tenantID}),
	})
}

func (p *RelationProjector) DeleteOrganizationAdmin(ctx context.Context, admin SubjectRef, organizationID string, tenantID string) error {
	return p.delete(ctx, []RelationTuple{
		p.tuple(tenantID, admin, "admin", identity.ObjectRef{Type: "organization", ID: organizationID, TenantID: tenantID}),
	})
}

func (p *RelationProjector) WriteDepartmentCreated(ctx context.Context, departmentID string, organizationID string, tenantID string) error {
	return p.write(ctx, []RelationTuple{
		p.tuple(tenantID, SubjectRef{Kind: "organization", ID: organizationID}, "org", identity.ObjectRef{Type: "department", ID: departmentID, TenantID: tenantID}),
	})
}

func (p *RelationProjector) DeleteDepartmentCreated(ctx context.Context, departmentID string, organizationID string, tenantID string) error {
	return p.delete(ctx, []RelationTuple{
		p.tuple(tenantID, SubjectRef{Kind: "organization", ID: organizationID}, "org", identity.ObjectRef{Type: "department", ID: departmentID, TenantID: tenantID}),
	})
}

func (p *RelationProjector) WriteDepartmentManager(ctx context.Context, manager SubjectRef, departmentID string, tenantID string) error {
	return p.write(ctx, []RelationTuple{
		p.tuple(tenantID, manager, "manager", identity.ObjectRef{Type: "department", ID: departmentID, TenantID: tenantID}),
	})
}

func (p *RelationProjector) DeleteDepartmentManager(ctx context.Context, manager SubjectRef, departmentID string, tenantID string) error {
	return p.delete(ctx, []RelationTuple{
		p.tuple(tenantID, manager, "manager", identity.ObjectRef{Type: "department", ID: departmentID, TenantID: tenantID}),
	})
}

func (p *RelationProjector) WriteWorkforceMemberPlacement(ctx context.Context, member SubjectRef, organizationID string, departmentID string, tenantID string, roleIDs []string) error {
	return p.write(ctx, p.workforceMemberPlacementTuples(member, organizationID, departmentID, tenantID, roleIDs))
}

func (p *RelationProjector) DeleteWorkforceMemberPlacement(ctx context.Context, member SubjectRef, organizationID string, departmentID string, tenantID string, roleIDs []string) error {
	return p.delete(ctx, p.workforceMemberPlacementTuples(member, organizationID, departmentID, tenantID, roleIDs))
}

func (p *RelationProjector) WriteRoleAssignment(ctx context.Context, assignment RoleAssignmentRelationSnapshot) error {
	return p.write(ctx, p.roleAssignmentTuples(assignment, false))
}

func (p *RelationProjector) DeleteRoleAssignment(ctx context.Context, assignment RoleAssignmentRelationSnapshot) error {
	return p.delete(ctx, p.roleAssignmentTuples(assignment, true))
}

func (p *RelationProjector) WriteResourceBinding(ctx context.Context, agentID string, resourceID string, tenantID string) error {
	return p.WriteResourceBindingForSubject(ctx, SubjectRef{Kind: "agent", ID: agentID}, resourceID, tenantID)
}

// deferredHumanResourceBindingProfileID preserves the identity whose migration
// is explicitly postponed pending a separate review.
const deferredHumanResourceBindingProfileID = "profile-b1bc89fa27b7429887a4b20633d3a80e"

// ResolveResourceBindingSubject maps a directory entry to its authorization subject.
func ResolveResourceBindingSubject(profileID string, instanceID string, human bool) SubjectRef {
	id := strings.TrimSpace(profileID)
	if id == "" {
		id = strings.TrimSpace(instanceID)
	}
	kind := "agent"
	if human && !strings.EqualFold(id, deferredHumanResourceBindingProfileID) {
		kind = "user"
	}
	return SubjectRef{Kind: kind, ID: id}
}

func (p *RelationProjector) WriteResourceBindingForSubject(ctx context.Context, subject SubjectRef, resourceID string, tenantID string) error {
	return p.write(ctx, []RelationTuple{
		p.tuple(tenantID, subject, "operator", identity.ObjectRef{Type: "resource", ID: resourceID, TenantID: tenantID}),
	})
}

func (p *RelationProjector) DeleteResourceBinding(ctx context.Context, agentID string, resourceID string, tenantID string) error {
	return p.DeleteResourceBindingForSubject(ctx, SubjectRef{Kind: "agent", ID: agentID}, resourceID, tenantID)
}

func (p *RelationProjector) DeleteResourceBindingForSubject(ctx context.Context, subject SubjectRef, resourceID string, tenantID string) error {
	return p.delete(ctx, []RelationTuple{
		p.tuple(tenantID, subject, "operator", identity.ObjectRef{Type: "resource", ID: resourceID, TenantID: tenantID}),
	})
}

func (p *RelationProjector) Backfill(ctx context.Context, snapshot RelationBackfillSnapshot) (RelationBackfillResult, error) {
	return p.BackfillWithOptions(ctx, snapshot, RelationBackfillOptions{})
}

func (p *RelationProjector) BackfillWithOptions(ctx context.Context, snapshot RelationBackfillSnapshot, opts RelationBackfillOptions) (RelationBackfillResult, error) {
	if err := ctx.Err(); err != nil {
		return RelationBackfillResult{}, err
	}
	plan := p.PlanBackfill(snapshot)
	result := plan.Result()
	result.DryRun = opts.DryRun
	if opts.Diff {
		diff, err := p.DiffBackfillPlan(ctx, plan)
		if err != nil {
			return RelationBackfillResult{}, err
		}
		result.Diff = diff
	}
	if opts.DryRun {
		return result, nil
	}
	tuplesToWrite := plan.Tuples
	if opts.Diff {
		tuplesToWrite = result.Diff.MissingTuples
	}
	if err := p.write(ctx, tuplesToWrite); err != nil {
		return RelationBackfillResult{}, err
	}
	if opts.Prune && len(result.Diff.ExtraTuples) > 0 {
		if err := p.delete(ctx, result.Diff.ExtraTuples); err != nil {
			return RelationBackfillResult{}, err
		}
		result.Pruned = len(result.Diff.ExtraTuples)
	}
	if opts.Diff {
		diff, err := p.DiffBackfillPlan(ctx, plan)
		if err != nil {
			return RelationBackfillResult{}, err
		}
		result.Diff = diff
	}
	return result, nil
}

func (p *RelationProjector) PlanBackfill(snapshot RelationBackfillSnapshot) RelationBackfillPlan {
	tuples := make([]RelationTuple, 0)
	for _, project := range snapshot.Projects {
		tuples = append(tuples, p.tuple(project.TenantID, project.Owner, "owner", identity.ObjectRef{Type: "project", ID: project.ProjectID, TenantID: project.TenantID}))
		if organizationID := strings.TrimSpace(project.OrganizationID); organizationID != "" {
			tuples = append(tuples, p.tuple(project.TenantID, SubjectRef{Kind: "organization", ID: organizationID}, "org", identity.ObjectRef{Type: "project", ID: project.ProjectID, TenantID: project.TenantID}))
		}
	}
	for _, project := range snapshot.DevelopmentProjects {
		object := identity.ObjectRef{Type: "development_project", ID: project.ProjectID, TenantID: project.TenantID}
		if project.Owner.Kind != "" && project.Owner.ID != "" {
			tuples = append(tuples, p.tuple(project.TenantID, project.Owner, "owner", object))
		}
		if organizationID := strings.TrimSpace(project.OrganizationID); organizationID != "" {
			tuples = append(tuples, p.tuple(project.TenantID, SubjectRef{Kind: "organization", ID: organizationID}, "org", object))
		}
		for _, member := range project.Members {
			tuples = append(tuples, p.tuple(project.TenantID, member.Subject, member.Relation, object))
		}
		for _, connectionID := range project.ConnectionIDs {
			tuples = append(tuples, p.tuple(project.TenantID, SubjectRef{Kind: "development_project", ID: project.ProjectID}, "user", identity.ObjectRef{Type: "scm_connection", ID: connectionID, TenantID: project.TenantID}))
		}
	}
	for _, connection := range snapshot.SCMConnections {
		object := identity.ObjectRef{Type: "scm_connection", ID: connection.ConnectionID, TenantID: connection.TenantID}
		tuples = append(tuples,
			p.tuple(connection.TenantID, connection.Owner, "owner", object),
			p.tuple(connection.TenantID, connection.Owner, "manager", object),
			p.tuple(connection.TenantID, connection.Owner, "user", object),
		)
		if organizationID := strings.TrimSpace(connection.OrganizationID); organizationID != "" {
			tuples = append(tuples, p.tuple(connection.TenantID, SubjectRef{Kind: "organization", ID: organizationID}, "org", object))
		}
	}
	for _, todo := range snapshot.Todos {
		object := identity.ObjectRef{Type: "todo", ID: todo.TodoID, TenantID: todo.TenantID}
		if todo.Proposer.Kind != "" && todo.Proposer.ID != "" {
			tuples = append(tuples, p.tuple(todo.TenantID, todo.Proposer, "proposer", object))
		}
		if todo.Assignee.Kind != "" && todo.Assignee.ID != "" {
			tuples = append(tuples, p.tuple(todo.TenantID, todo.Assignee, "assignee", object))
		}
		if todo.Reviewer.Kind != "" && todo.Reviewer.ID != "" {
			tuples = append(tuples, p.tuple(todo.TenantID, todo.Reviewer, "reviewer", object))
		}
		for _, collaborator := range todo.Collaborators {
			tuples = append(tuples, p.tuple(todo.TenantID, collaborator, "collaborator", object))
		}
		if projectID := strings.TrimSpace(todo.DevelopmentProjectID); projectID != "" {
			tuples = append(tuples, p.tuple(todo.TenantID, SubjectRef{Kind: "development_project", ID: projectID}, "development_project", object))
		}
	}
	for _, mission := range snapshot.Missions {
		object := identity.ObjectRef{Type: "mission", ID: mission.MissionID, TenantID: mission.TenantID}
		if normalizeKind(mission.Owner.Kind) == "system" {
			tuples = append(tuples, p.tuple(mission.TenantID, mission.Owner, "automation", object))
		} else if strings.TrimSpace(mission.OriginTodoID) == "" && strings.TrimSpace(mission.Owner.Kind) != "" && strings.TrimSpace(mission.Owner.ID) != "" {
			tuples = append(tuples, p.tuple(mission.TenantID, mission.Owner, "owner", object))
		}
		for _, owner := range mission.Owners {
			tuples = append(tuples, p.tuple(mission.TenantID, owner, "owner", object))
		}
		for _, participant := range mission.Participants {
			tuples = append(tuples, p.tuple(mission.TenantID, participant, "participant", object))
		}
		for _, automation := range mission.Automation {
			tuples = append(tuples, p.tuple(mission.TenantID, automation, "automation", object))
		}
		if mission.OriginTodoID != "" {
			tuples = append(tuples, p.tuple(mission.TenantID, SubjectRef{Kind: "todo", ID: mission.OriginTodoID}, "origin_todo", object))
		}
		if mission.OriginDevelopmentProjectID != "" {
			tuples = append(tuples, p.tuple(mission.TenantID, SubjectRef{Kind: "development_project", ID: mission.OriginDevelopmentProjectID}, "origin_development_project", object))
		}
	}
	for _, discussion := range snapshot.Discussions {
		object := identity.ObjectRef{Type: "discussion", ID: discussion.DiscussionID, TenantID: discussion.TenantID}
		tuples = append(tuples, p.tuple(discussion.TenantID, SubjectRef{Kind: "mission", ID: discussion.MissionID}, "mission", object))
		if ownerKind := normalizeKind(discussion.Owner.Kind); (ownerKind == "user" || ownerKind == "agent") && strings.TrimSpace(discussion.Owner.ID) != "" {
			tuples = append(tuples, p.tuple(discussion.TenantID, discussion.Owner, "owner", object))
		}
		for _, member := range discussion.Members {
			tuples = append(tuples, p.tuple(discussion.TenantID, member, "member", identity.ObjectRef{Type: "discussion", ID: discussion.DiscussionID, TenantID: discussion.TenantID}))
		}
	}
	for _, organization := range snapshot.Organizations {
		tuples = append(tuples, p.tuple(organization.TenantID, organization.Admin, "admin", identity.ObjectRef{Type: "organization", ID: organization.OrganizationID, TenantID: organization.TenantID}))
	}
	for _, department := range snapshot.Departments {
		tuples = append(tuples, p.tuple(department.TenantID, SubjectRef{Kind: "organization", ID: department.OrganizationID}, "org", identity.ObjectRef{Type: "department", ID: department.DepartmentID, TenantID: department.TenantID}))
		for _, manager := range department.Managers {
			tuples = append(tuples, p.tuple(department.TenantID, manager, "manager", identity.ObjectRef{Type: "department", ID: department.DepartmentID, TenantID: department.TenantID}))
		}
	}
	for _, member := range snapshot.WorkforceMembers {
		tuples = append(tuples, p.workforceMemberPlacementTuples(member.Subject, member.OrganizationID, member.DepartmentID, member.TenantID, member.RoleIDs)...)
	}
	for _, assignment := range snapshot.RoleAssignments {
		tuples = append(tuples, p.roleAssignmentTuples(assignment, false)...)
	}
	for _, binding := range snapshot.ResourceBindings {
		tuples = append(tuples, p.tuple(binding.TenantID, binding.Subject, "operator", identity.ObjectRef{Type: "resource", ID: binding.ResourceID, TenantID: binding.TenantID}))
	}
	tuples = dedupeRelationTuples(tuples)
	return RelationBackfillPlan{
		Projects:            len(snapshot.Projects),
		DevelopmentProjects: len(snapshot.DevelopmentProjects),
		SCMConnections:      len(snapshot.SCMConnections),
		Todos:               len(snapshot.Todos),
		Missions:            len(snapshot.Missions),
		Discussions:         len(snapshot.Discussions),
		Organizations:       len(snapshot.Organizations),
		Departments:         len(snapshot.Departments),
		WorkforceMembers:    len(snapshot.WorkforceMembers),
		RoleAssignments:     len(snapshot.RoleAssignments),
		ResourceBindings:    len(snapshot.ResourceBindings),
		Tuples:              tuples,
	}
}

func (p *RelationProjector) DiffBackfillPlan(ctx context.Context, plan RelationBackfillPlan) (RelationBackfillDiff, error) {
	if err := ctx.Err(); err != nil {
		return RelationBackfillDiff{}, err
	}
	if p == nil || p.sink == nil {
		return RelationBackfillDiff{}, ErrServiceDependency
	}
	lister, ok := p.sink.(RelationLister)
	if !ok {
		return RelationBackfillDiff{}, ErrUnsupported
	}
	existing, err := lister.ListRelations(ctx)
	if err != nil {
		return RelationBackfillDiff{}, err
	}
	diff := RelationBackfillDiff{Checked: len(plan.Tuples), Existing: len(existing)}
	existingByKey := make(map[string]struct{}, len(existing))
	for _, tuple := range existing {
		existingByKey[relationTupleComparisonKey(tuple)] = struct{}{}
	}
	planned := make(map[string]struct{}, len(plan.Tuples))
	for _, tuple := range plan.Tuples {
		key := relationTupleComparisonKey(tuple)
		planned[key] = struct{}{}
		if _, ok := existingByKey[key]; !ok {
			diff.Missing++
			diff.MissingTuples = append(diff.MissingTuples, tuple)
		}
	}
	for _, tuple := range existing {
		if !managedProjectorTuple(tuple) {
			continue
		}
		if _, ok := planned[relationTupleComparisonKey(tuple)]; ok {
			continue
		}
		diff.Extra++
		diff.ExtraTuples = append(diff.ExtraTuples, tuple)
	}
	return diff, nil
}

func relationTupleComparisonKey(tuple RelationTuple) string {
	tuple = normalizeRelationTuple(tuple)
	return tuple.TenantID + "|" + tuple.Subject.Kind + ":" + tuple.Subject.ID + "|" + tuple.Relation + "|" + tuple.Object.Type + ":" + tuple.Object.ID
}

func managedProjectorTuple(tuple RelationTuple) bool {
	tuple = normalizeRelationTuple(tuple)
	managed := map[string]map[string]struct{}{
		"platform":            {"member": {}, "assigned_role": {}},
		"organization":        {"admin": {}, "member": {}},
		"department":          {"org": {}, "manager": {}, "member": {}},
		"role":                {"org": {}, "scope": {}, "assignee": {}},
		"mission":             {"owner": {}, "participant": {}, "origin_todo": {}, "origin_development_project": {}},
		"discussion":          {"mission": {}, "owner": {}, "member": {}},
		"project":             {"owner": {}, "org": {}},
		"development_project": {"org": {}, "owner": {}, "maintainer": {}, "contributor": {}, "viewer": {}, "executor": {}, "merger": {}},
		"scm_connection":      {"org": {}, "owner": {}, "user": {}, "manager": {}},
		"todo":                {"proposer": {}, "assignee": {}, "reviewer": {}, "collaborator": {}, "development_project": {}},
		"resource":            {"operator": {}},
	}
	relations := managed[tuple.Object.Type]
	_, ok := relations[tuple.Relation]
	return ok
}

func (p *RelationProjector) write(ctx context.Context, tuples []RelationTuple) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil || p.sink == nil || len(tuples) == 0 {
		return nil
	}
	err := p.sink.WriteRelations(ctx, dedupeRelationTuples(tuples))
	if p.bestEffort && (err != nil || errors.Is(err, ErrUnsupported)) {
		return nil
	}
	return err
}

func (p *RelationProjector) delete(ctx context.Context, tuples []RelationTuple) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p == nil || p.sink == nil || len(tuples) == 0 {
		return nil
	}
	err := p.sink.DeleteRelations(ctx, dedupeRelationTuples(tuples))
	if p.bestEffort && (err != nil || errors.Is(err, ErrUnsupported)) {
		return nil
	}
	return err
}

func (p *RelationProjector) tuple(tenantID string, subject SubjectRef, relation string, object identity.ObjectRef) RelationTuple {
	tenantID = relationTenantID(tenantID)
	subject.Kind = normalizeKind(subject.Kind)
	subject.ID = strings.TrimSpace(subject.ID)
	object.Type = normalizeKind(object.Type)
	object.ID = strings.TrimSpace(object.ID)
	object.TenantID = tenantID
	relation = normalizeRelation(relation)
	createdAt := time.Now().UTC()
	if p != nil && p.now != nil {
		createdAt = p.now().UTC()
	}
	return normalizeRelationTuple(RelationTuple{
		ID:        tenantID + ":" + subject.Kind + ":" + subject.ID + ":" + relation + ":" + object.Type + ":" + object.ID,
		TenantID:  tenantID,
		Subject:   subject,
		Relation:  relation,
		Object:    object,
		Source:    SubjectRef{Kind: "system", ID: "authorization_projector"},
		CreatedAt: createdAt,
	})
}

func (p *RelationProjector) workforceMemberPlacementTuples(member SubjectRef, organizationID string, departmentID string, tenantID string, roleIDs []string) []RelationTuple {
	tuples := []RelationTuple{
		p.tuple(tenantID, member, "member", identity.ObjectRef{Type: "platform", ID: "global", TenantID: tenantID}),
		p.tuple(tenantID, member, "member", identity.ObjectRef{Type: "organization", ID: organizationID, TenantID: tenantID}),
		p.tuple(tenantID, member, "member", identity.ObjectRef{Type: "department", ID: departmentID, TenantID: tenantID}),
	}
	for _, roleID := range roleIDs {
		tuples = append(tuples, p.roleAssignmentTuples(RoleAssignmentRelationSnapshot{
			AssignmentID:       "profile-role:" + member.Kind + ":" + member.ID + ":" + roleID + ":" + departmentID,
			Assignee:           member,
			RoleID:             roleID,
			OrganizationID:     organizationID,
			OrganizationNodeID: departmentID,
			ScopeMode:          "self",
			TenantID:           tenantID,
		}, false)...)
	}
	return tuples
}

func (p *RelationProjector) roleAssignmentTuples(assignment RoleAssignmentRelationSnapshot, includeInactive bool) []RelationTuple {
	assignment.AssignmentID = strings.TrimSpace(assignment.AssignmentID)
	assignment.RoleID = strings.TrimSpace(assignment.RoleID)
	assignment.OrganizationID = strings.TrimSpace(assignment.OrganizationID)
	assignment.OrganizationNodeID = strings.TrimSpace(assignment.OrganizationNodeID)
	assignment.ScopeMode = strings.ToLower(strings.TrimSpace(assignment.ScopeMode))
	assignment.TenantID = relationTenantID(assignment.TenantID)
	if !includeInactive && !roleAssignmentRelationEffectiveAt(assignment, p.currentTime()) {
		return nil
	}
	if assignment.AssignmentID == "" {
		assignment.AssignmentID = strings.Join([]string{assignment.Assignee.Kind, assignment.Assignee.ID, assignment.RoleID, assignment.OrganizationNodeID, assignment.ScopeMode}, ":")
	}
	roleObjectID := scopedRoleAssignmentObjectID(assignment.OrganizationID, assignment.RoleID, assignment.AssignmentID)
	object := identity.ObjectRef{Type: "role", ID: roleObjectID, TenantID: assignment.TenantID}
	tupleWithValidity := func(subject SubjectRef, relation string) RelationTuple {
		tuple := p.tuple(assignment.TenantID, subject, relation, object)
		if !assignment.StartsAt.IsZero() {
			startsAt := assignment.StartsAt.UTC()
			tuple.ValidFrom = &startsAt
		}
		if !assignment.ExpiresAt.IsZero() {
			expiresAt := assignment.ExpiresAt.UTC()
			tuple.ValidTo = &expiresAt
		}
		return normalizeRelationTuple(tuple)
	}
	targetKind := "department"
	targetID := assignment.OrganizationNodeID
	if assignment.ScopeMode == "platform" {
		targetKind = "platform"
		targetID = "global"
	} else if targetID == "" || targetID == assignment.OrganizationID {
		targetKind = "organization"
		targetID = assignment.OrganizationID
	}
	tuples := []RelationTuple{
		tupleWithValidity(SubjectRef{Kind: targetKind, ID: targetID}, "scope"),
		tupleWithValidity(assignment.Assignee, "assignee"),
	}
	if assignment.ScopeMode == "platform" {
		tuples = append(tuples, p.tuple(
			assignment.TenantID,
			SubjectRef{Kind: "role", ID: roleObjectID},
			"assigned_role",
			identity.ObjectRef{Type: "platform", ID: "global", TenantID: assignment.TenantID},
		))
	}
	if assignment.OrganizationID != "" && assignment.OrganizationID != "global" {
		tuples = append(tuples, tupleWithValidity(SubjectRef{Kind: "organization", ID: assignment.OrganizationID}, "org"))
	}
	return tuples
}

func (p *RelationProjector) currentTime() time.Time {
	if p != nil && p.now != nil {
		return p.now().UTC()
	}
	return time.Now().UTC()
}

func roleAssignmentRelationEffectiveAt(assignment RoleAssignmentRelationSnapshot, now time.Time) bool {
	if !assignment.StartsAt.IsZero() && assignment.StartsAt.After(now) {
		return false
	}
	return assignment.ExpiresAt.IsZero() || assignment.ExpiresAt.After(now)
}

func scopedRoleAssignmentObjectID(organizationID string, roleID string, assignmentID string) string {
	organizationID = strings.TrimSpace(organizationID)
	roleID = strings.TrimSpace(roleID)
	assignmentID = strings.TrimSpace(assignmentID)
	return strings.Join([]string{relationTenantID(organizationID), roleID, assignmentID}, "/")
}

func dedupeRelationTuples(tuples []RelationTuple) []RelationTuple {
	out := make([]RelationTuple, 0, len(tuples))
	seen := map[string]struct{}{}
	for _, tuple := range tuples {
		tuple = normalizeRelationTuple(tuple)
		if tuple.Subject.Kind == "" || tuple.Subject.ID == "" || tuple.Relation == "" || tuple.Object.Type == "" || tuple.Object.ID == "" {
			continue
		}
		if tuple.TenantID == "" {
			tuple.TenantID = relationTenantID(tuple.Object.TenantID)
			tuple.Object.TenantID = tuple.TenantID
		}
		if tuple.ID == "" {
			tuple.ID = tuple.TenantID + ":" + tuple.Subject.Kind + ":" + tuple.Subject.ID + ":" + tuple.Relation + ":" + tuple.Object.Type + ":" + tuple.Object.ID
		}
		if _, ok := seen[tuple.ID]; ok {
			continue
		}
		seen[tuple.ID] = struct{}{}
		out = append(out, tuple)
	}
	return out
}

func relationTenantID(tenantID string) string {
	tenantID = strings.TrimSpace(tenantID)
	if tenantID == "" {
		return "global"
	}
	return tenantID
}

type ProjectRelationSnapshot struct {
	ProjectID      string
	OrganizationID string
	TenantID       string
	Owner          SubjectRef
}

type DevelopmentProjectMemberRelationSnapshot struct {
	Subject  SubjectRef
	Relation string
}

type DevelopmentProjectRelationSnapshot struct {
	ProjectID      string
	OrganizationID string
	TenantID       string
	Owner          SubjectRef
	Members        []DevelopmentProjectMemberRelationSnapshot
	ConnectionIDs  []string
}

type SCMConnectionRelationSnapshot struct {
	ConnectionID   string
	OrganizationID string
	TenantID       string
	Owner          SubjectRef
}

type TodoRelationSnapshot struct {
	TodoID               string
	TenantID             string
	Proposer             SubjectRef
	Assignee             SubjectRef
	Reviewer             SubjectRef
	Collaborators        []SubjectRef
	DevelopmentProjectID string
}

type MissionRelationSnapshot struct {
	MissionID                  string
	TenantID                   string
	Owner                      SubjectRef
	Owners                     []SubjectRef
	Participants               []SubjectRef
	Automation                 []SubjectRef
	OriginTodoID               string
	OriginDevelopmentProjectID string
}

type DiscussionRelationSnapshot struct {
	DiscussionID string
	MissionID    string
	TenantID     string
	Owner        SubjectRef
	Members      []SubjectRef
}

type OrganizationRelationSnapshot struct {
	OrganizationID string
	TenantID       string
	Admin          SubjectRef
}

type DepartmentRelationSnapshot struct {
	DepartmentID   string
	OrganizationID string
	TenantID       string
	Managers       []SubjectRef
}

type WorkforceMemberRelationSnapshot struct {
	Subject        SubjectRef
	OrganizationID string
	DepartmentID   string
	TenantID       string
	RoleIDs        []string
}

type RoleAssignmentRelationSnapshot struct {
	AssignmentID       string
	Assignee           SubjectRef
	RoleID             string
	OrganizationID     string
	OrganizationNodeID string
	ScopeMode          string
	TenantID           string
	StartsAt           time.Time
	ExpiresAt          time.Time
}

type ResourceBindingRelationSnapshot struct {
	ResourceID string
	Subject    SubjectRef
	TenantID   string
}

type RelationBackfillSnapshot struct {
	Projects            []ProjectRelationSnapshot
	DevelopmentProjects []DevelopmentProjectRelationSnapshot
	SCMConnections      []SCMConnectionRelationSnapshot
	Todos               []TodoRelationSnapshot
	Missions            []MissionRelationSnapshot
	Discussions         []DiscussionRelationSnapshot
	Organizations       []OrganizationRelationSnapshot
	Departments         []DepartmentRelationSnapshot
	WorkforceMembers    []WorkforceMemberRelationSnapshot
	RoleAssignments     []RoleAssignmentRelationSnapshot
	ResourceBindings    []ResourceBindingRelationSnapshot
}

type RelationBackfillOptions struct {
	DryRun bool
	Diff   bool
	Prune  bool
}

type RelationBackfillPlan struct {
	Projects            int
	DevelopmentProjects int
	SCMConnections      int
	Todos               int
	Missions            int
	Discussions         int
	Organizations       int
	Departments         int
	WorkforceMembers    int
	RoleAssignments     int
	ResourceBindings    int
	Tuples              []RelationTuple
}

func (p RelationBackfillPlan) Result() RelationBackfillResult {
	report := p.Report()
	return RelationBackfillResult{
		Projects:            p.Projects,
		DevelopmentProjects: p.DevelopmentProjects,
		SCMConnections:      p.SCMConnections,
		Todos:               p.Todos,
		Missions:            p.Missions,
		Discussions:         p.Discussions,
		Organizations:       p.Organizations,
		Departments:         p.Departments,
		WorkforceMembers:    p.WorkforceMembers,
		RoleAssignments:     p.RoleAssignments,
		ResourceBindings:    p.ResourceBindings,
		Tuples:              len(p.Tuples),
		Report:              report,
	}
}

func (p RelationBackfillPlan) Report() RelationBackfillReport {
	report := RelationBackfillReport{
		Projects:            p.Projects,
		DevelopmentProjects: p.DevelopmentProjects,
		SCMConnections:      p.SCMConnections,
		Todos:               p.Todos,
		Missions:            p.Missions,
		Discussions:         p.Discussions,
		Organizations:       p.Organizations,
		Departments:         p.Departments,
		WorkforceMembers:    p.WorkforceMembers,
		RoleAssignments:     p.RoleAssignments,
		ResourceBindings:    p.ResourceBindings,
		Tuples:              len(p.Tuples),
		ByObjectType:        map[string]int{},
		ByRelation:          map[string]int{},
		BySubjectKind:       map[string]int{},
		ByTenant:            map[string]int{},
	}
	for _, tuple := range p.Tuples {
		report.ByObjectType[tuple.Object.Type]++
		report.ByRelation[tuple.Relation]++
		report.BySubjectKind[tuple.Subject.Kind]++
		report.ByTenant[relationTenantID(tuple.TenantID)]++
	}
	return report
}

type RelationBackfillReport struct {
	Projects            int
	DevelopmentProjects int
	SCMConnections      int
	Todos               int
	Missions            int
	Discussions         int
	Organizations       int
	Departments         int
	WorkforceMembers    int
	RoleAssignments     int
	ResourceBindings    int
	Tuples              int
	ByObjectType        map[string]int
	ByRelation          map[string]int
	BySubjectKind       map[string]int
	ByTenant            map[string]int
}

type RelationBackfillDiff struct {
	Checked       int
	Existing      int
	Missing       int
	Extra         int
	Errors        int
	MissingTuples []RelationTuple
	ExtraTuples   []RelationTuple
	ErrorMessages []string
}

type RelationBackfillResult struct {
	Mode                string
	Disabled            bool
	Projects            int
	DevelopmentProjects int
	SCMConnections      int
	Todos               int
	Missions            int
	Discussions         int
	Organizations       int
	Departments         int
	WorkforceMembers    int
	RoleAssignments     int
	ResourceBindings    int
	Tuples              int
	DryRun              bool
	Pruned              int
	Report              RelationBackfillReport
	Diff                RelationBackfillDiff
}
