package operations

import (
	"context"
	"errors"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/authorization"
)

type authorizationRelationWriter interface {
	WriteOrganizationCreated(ctx context.Context, admin authorization.SubjectRef, organizationID string, tenantID string) error
	WriteOrganizationAdmin(ctx context.Context, admin authorization.SubjectRef, organizationID string, tenantID string) error
	DeleteOrganizationAdmin(ctx context.Context, admin authorization.SubjectRef, organizationID string, tenantID string) error
	WriteDepartmentCreated(ctx context.Context, departmentID string, organizationID string, tenantID string) error
	DeleteDepartmentCreated(ctx context.Context, departmentID string, organizationID string, tenantID string) error
	WriteDepartmentManager(ctx context.Context, manager authorization.SubjectRef, departmentID string, tenantID string) error
	DeleteDepartmentManager(ctx context.Context, manager authorization.SubjectRef, departmentID string, tenantID string) error
	WriteWorkforceMemberPlacement(ctx context.Context, member authorization.SubjectRef, organizationID string, departmentID string, tenantID string, roleIDs []string) error
	DeleteWorkforceMemberPlacement(ctx context.Context, member authorization.SubjectRef, organizationID string, departmentID string, tenantID string, roleIDs []string) error
	WriteRoleAssignment(ctx context.Context, assignment authorization.RoleAssignmentRelationSnapshot) error
	DeleteRoleAssignment(ctx context.Context, assignment authorization.RoleAssignmentRelationSnapshot) error
}

func (s *CommandService) SetAuthorizationRelations(writer authorizationRelationWriter) {
	if s != nil {
		s.authorizationRelations = writer
	}
}

func (s *CommandService) writeOrganizationCreatedRelation(ctx context.Context, viewer ViewerRef, organization OrganizationDTO) error {
	if s == nil || s.authorizationRelations == nil {
		return nil
	}
	return s.authorizationRelations.WriteOrganizationCreated(ctx, authorizationSubjectFromViewer(viewer), organization.OrganizationID, organization.OrganizationID)
}

func (s *CommandService) writeDepartmentCreatedRelation(ctx context.Context, department DepartmentDTO) error {
	if s == nil || s.authorizationRelations == nil {
		return nil
	}
	return s.authorizationRelations.WriteDepartmentCreated(ctx, department.DepartmentID, department.OrganizationID, department.OrganizationID)
}

func (s *CommandService) deleteDepartmentCreatedRelation(ctx context.Context, department DepartmentDTO) error {
	if s == nil || s.authorizationRelations == nil {
		return nil
	}
	return s.authorizationRelations.DeleteDepartmentCreated(ctx, department.DepartmentID, department.OrganizationID, department.OrganizationID)
}

func (s *CommandService) syncOrganizationOwnerRelation(ctx context.Context, node OrganizationNodeDTO, previousOwner string, organizationID string) error {
	if s == nil || s.authorizationRelations == nil {
		return nil
	}
	tenantID := strings.TrimSpace(organizationID)
	if tenantID == "" && node.NodeType == organizationNodeTypeCompany {
		tenantID = node.ID
	}
	previous := authorizationSubjectFromOwnerProfile(previousOwner)
	if previous.ID != "" {
		if node.NodeType == organizationNodeTypeCompany {
			if err := s.authorizationRelations.DeleteOrganizationAdmin(ctx, previous, node.ID, tenantID); err != nil {
				return err
			}
		} else if err := s.authorizationRelations.DeleteDepartmentManager(ctx, previous, node.ID, tenantID); err != nil {
			return err
		}
	}
	next := authorizationSubjectFromOwnerProfile(node.OwnerProfileID)
	if next.ID == "" {
		return nil
	}
	var err error
	if node.NodeType == organizationNodeTypeCompany {
		err = s.authorizationRelations.WriteOrganizationAdmin(ctx, next, node.ID, tenantID)
	} else {
		err = s.authorizationRelations.WriteDepartmentManager(ctx, next, node.ID, tenantID)
	}
	if err == nil || previous.ID == "" {
		return err
	}
	if node.NodeType == organizationNodeTypeCompany {
		return errors.Join(err, s.authorizationRelations.WriteOrganizationAdmin(ctx, previous, node.ID, tenantID))
	}
	return errors.Join(err, s.authorizationRelations.WriteDepartmentManager(ctx, previous, node.ID, tenantID))
}

func (s *CommandService) writeWorkforceMemberPlacementRelation(ctx context.Context, profile agentcore.AgentProfile) error {
	if s == nil || s.authorizationRelations == nil {
		return nil
	}
	subject := authorizationSubjectFromProfile(profile)
	organizationID := strings.TrimSpace(string(profile.OrgIdentity.OrganizationRef.ID))
	departmentID := strings.TrimSpace(string(profile.OrgIdentity.DepartmentRef.ID))
	if subject.ID == "" || organizationID == "" || departmentID == "" {
		return nil
	}
	return s.authorizationRelations.WriteWorkforceMemberPlacement(ctx, subject, organizationID, departmentID, organizationID, roleIDsFromProfile(profile))
}

func (s *CommandService) deleteWorkforceMemberPlacementRelation(ctx context.Context, profile agentcore.AgentProfile) error {
	if s == nil || s.authorizationRelations == nil {
		return nil
	}
	subject := authorizationSubjectFromProfile(profile)
	organizationID := strings.TrimSpace(string(profile.OrgIdentity.OrganizationRef.ID))
	departmentID := strings.TrimSpace(string(profile.OrgIdentity.DepartmentRef.ID))
	if subject.ID == "" || organizationID == "" || departmentID == "" {
		return nil
	}
	return s.authorizationRelations.DeleteWorkforceMemberPlacement(ctx, subject, organizationID, departmentID, organizationID, roleIDsFromProfile(profile))
}

func (s *CommandService) writeRoleAssignmentRelation(ctx context.Context, assignment RoleAssignmentDTO, organizationID string) error {
	if s == nil || s.authorizationRelations == nil {
		return nil
	}
	assignee := authorizationSubjectFromActor(assignment.Actor)
	if assignee.ID == "" || strings.TrimSpace(assignment.RoleID) == "" || strings.TrimSpace(organizationID) == "" {
		return nil
	}
	return s.authorizationRelations.WriteRoleAssignment(ctx, authorizationRoleAssignmentSnapshot(assignment, organizationID, assignee))
}

func (s *CommandService) deleteRoleAssignmentRelation(ctx context.Context, assignment RoleAssignmentDTO, organizationID string) error {
	if s == nil || s.authorizationRelations == nil {
		return nil
	}
	assignee := authorizationSubjectFromActor(assignment.Actor)
	if assignee.ID == "" || strings.TrimSpace(assignment.RoleID) == "" || strings.TrimSpace(organizationID) == "" {
		return nil
	}
	return s.authorizationRelations.DeleteRoleAssignment(ctx, authorizationRoleAssignmentSnapshot(assignment, organizationID, assignee))
}

func authorizationRoleAssignmentSnapshot(assignment RoleAssignmentDTO, organizationID string, assignee authorization.SubjectRef) authorization.RoleAssignmentRelationSnapshot {
	tenantID := strings.TrimSpace(organizationID)
	if roleScopeIsPlatform(assignment.ScopeMode) {
		organizationID = "global"
		tenantID = "global"
	}
	return authorization.RoleAssignmentRelationSnapshot{
		AssignmentID:       assignment.AssignmentID,
		Assignee:           assignee,
		RoleID:             assignment.RoleID,
		OrganizationID:     organizationID,
		OrganizationNodeID: assignment.OrganizationNodeID,
		ScopeMode:          assignment.ScopeMode,
		TenantID:           tenantID,
		StartsAt:           assignment.StartsAt,
		ExpiresAt:          assignment.ExpiresAt,
	}
}

func authorizationSubjectFromViewer(viewer ViewerRef) authorization.SubjectRef {
	viewerKind := strings.TrimSpace(viewer.ViewerKind)
	viewerID := strings.TrimSpace(viewer.ViewerID)
	if strings.EqualFold(viewerKind, "human") || strings.EqualFold(viewerKind, "user") || viewerKind == "" {
		viewerID = firstNonEmpty(viewer.AgentProfileID, viewerID)
	}
	return authorizationSubject(viewerKind, viewerID)
}

func authorizationSubjectFromOwnerProfile(profileID string) authorization.SubjectRef {
	return authorizationSubject("human", profileID)
}

func authorizationSubjectFromActor(actor ActorRefDTO) authorization.SubjectRef {
	return authorizationSubject(actor.ActorType, actor.ActorID)
}

func authorizationSubjectFromProfile(profile agentcore.AgentProfile) authorization.SubjectRef {
	kind := "agent"
	if profile.AgentKind == agentcore.AgentKindHuman {
		kind = "human"
	}
	return authorizationSubject(kind, profile.ID.String())
}

func authorizationSubject(kind string, id string) authorization.SubjectRef {
	id = strings.TrimSpace(id)
	if id == "" {
		return authorization.SubjectRef{}
	}
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "user", "human":
		return authorization.SubjectRef{Kind: "user", ID: id}
	case "agent", "ai":
		return authorization.SubjectRef{Kind: "agent", ID: id}
	default:
		return authorization.SubjectRef{}
	}
}

func roleIDsFromProfile(profile agentcore.AgentProfile) []string {
	out := make([]string, 0, len(profile.OrgIdentity.RoleRefs))
	for _, role := range profile.OrgIdentity.RoleRefs {
		roleID := strings.TrimSpace(string(role.ID))
		if roleID == "" {
			continue
		}
		out = append(out, roleID)
	}
	return out
}
