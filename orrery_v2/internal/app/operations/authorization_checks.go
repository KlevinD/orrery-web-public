package operations

import (
	"context"
	"fmt"
	"strings"

	"orrery_v2/internal/app/identity"
)

const (
	authorizationActionOrganizationRead   = "organization.read"
	authorizationActionOrganizationManage = "organization.manage"
	authorizationActionDepartmentRead     = "department.read"
	authorizationActionDepartmentManage   = "department.manage"
	authorizationActionWorkforceRead      = "workforce.member.read"
	authorizationActionWorkforceManage    = "workforce.member.manage"
	authorizationActionRoleRead           = "role.read"
	authorizationActionRoleManage         = "role.manage"
	authorizationActionRoleAssignmentRead = "role.assignment.read"

	authorizationObjectOrganization = "organization"
	authorizationObjectDepartment   = "department"
	authorizationObjectRole         = "role"
	authorizationObjectPlatform     = "platform"
)

type AuthorizationCheckerPort interface {
	Can(ctx context.Context, principal identity.Principal, action string, object identity.ObjectRef) error
}

func (s *QueryService) SetAuthorizationChecker(checker AuthorizationCheckerPort) {
	if s != nil {
		s.authorization = checker
	}
}

func (s *CommandService) SetAuthorizationChecker(checker AuthorizationCheckerPort) {
	if s != nil {
		s.authorizationChecker = checker
	}
}

func (s *QueryService) authorizeOperationObject(ctx context.Context, viewer ViewerRef, action string, objectType string, objectID string, tenantID string) error {
	if s == nil {
		return nil
	}
	return authorizeOperationObject(ctx, s.authorization, viewer, action, objectType, objectID, tenantID)
}

func (s *CommandService) authorizeOperationObject(ctx context.Context, viewer ViewerRef, action string, objectType string, objectID string, tenantID string) error {
	if s == nil {
		return nil
	}
	return authorizeOperationObject(ctx, s.authorizationChecker, viewer, action, objectType, objectID, tenantID)
}

func (s *QueryService) authorizePlatformOperation(ctx context.Context, viewer ViewerRef, permission string) error {
	return s.authorizeOperationObject(ctx, viewer, permission, authorizationObjectPlatform, "global", "global")
}

func (s *CommandService) authorizePlatformOperation(ctx context.Context, viewer ViewerRef, permission string) error {
	return s.authorizeOperationObject(ctx, viewer, permission, authorizationObjectPlatform, "global", "global")
}

func authorizeOperationObject(ctx context.Context, checker AuthorizationCheckerPort, viewer ViewerRef, action string, objectType string, objectID string, tenantID string) error {
	if checker == nil {
		return nil
	}
	action = strings.TrimSpace(action)
	objectType = strings.TrimSpace(objectType)
	objectID = strings.TrimSpace(objectID)
	tenantID = strings.TrimSpace(firstNonEmpty(tenantID, objectID))
	if action == "" || objectType == "" || objectID == "" {
		return fmt.Errorf("%w: authorization action, object type and object id are required", ErrInvalidRequest)
	}
	principal := authorizationPrincipalFromViewer(viewer, tenantID)
	return checker.Can(ctx, principal, action, identity.ObjectRef{Type: objectType, ID: objectID, TenantID: tenantID})
}

func (s *QueryService) authorizeWorkforceRead(ctx context.Context, viewer ViewerRef, query WorkforceQuery) error {
	if s == nil || s.authorization == nil {
		return nil
	}
	organizationID := strings.TrimSpace(query.OrganizationID)
	departmentID := strings.TrimSpace(query.DepartmentID)
	if departmentID != "" {
		if organizationID == "" {
			resolved, err := s.organizationIDForDepartment(ctx, departmentID)
			if err != nil {
				return err
			}
			organizationID = resolved
		}
		return s.authorizeOperationObject(ctx, viewer, authorizationActionWorkforceRead, authorizationObjectDepartment, departmentID, organizationID)
	}
	if organizationID != "" {
		return s.authorizeOperationObject(ctx, viewer, authorizationActionWorkforceRead, authorizationObjectOrganization, organizationID, organizationID)
	}
	return s.authorizePlatformOperation(ctx, viewer, PermissionWorkforceRead)
}

func (s *QueryService) organizationIDForDepartment(ctx context.Context, departmentID string) (string, error) {
	if s == nil || s.orgs == nil {
		return "", ErrServiceDependency
	}
	graph, err := s.orgs.ListOrganizations(ctx, OrgGraphQuery{})
	if err != nil {
		return "", err
	}
	for _, department := range graph.Departments {
		if strings.TrimSpace(department.DepartmentID) == strings.TrimSpace(departmentID) {
			return strings.TrimSpace(department.OrganizationID), nil
		}
	}
	return "", ErrNotFound
}

func (s *QueryService) authorizeRoleAssignmentRead(ctx context.Context, viewer ViewerRef, query RoleAssignmentQuery) error {
	if s == nil || s.authorization == nil {
		return nil
	}
	if strings.TrimSpace(query.OrganizationCode) == "" {
		return s.authorizePlatformOperation(ctx, viewer, PermissionRolesRead)
	}
	node, organizationID, err := s.organizationNodeForCode(ctx, query.OrganizationCode)
	if err != nil {
		return err
	}
	if strings.TrimSpace(query.RoleID) != "" {
		roleObjectID := scopedRoleAuthorizationObjectID(organizationID, query.RoleID)
		return s.authorizeOperationObject(ctx, viewer, authorizationActionRoleRead, authorizationObjectRole, roleObjectID, organizationID)
	}
	if node.NodeType == organizationNodeTypeCompany {
		return s.authorizeOperationObject(ctx, viewer, authorizationActionRoleAssignmentRead, authorizationObjectOrganization, organizationID, organizationID)
	}
	return s.authorizeOperationObject(ctx, viewer, authorizationActionRoleAssignmentRead, authorizationObjectDepartment, node.ID, organizationID)
}

func (s *QueryService) organizationNodeForCode(ctx context.Context, code string) (OrganizationNodeDTO, string, error) {
	if s == nil || s.orgs == nil {
		return OrganizationNodeDTO{}, "", ErrServiceDependency
	}
	code = normalizeOrganizationCodeForQuery(code)
	graph, err := s.orgs.ListOrganizations(ctx, OrgGraphQuery{})
	if err != nil {
		return OrganizationNodeDTO{}, "", err
	}
	nodes, _ := buildOrganizationNodes(graph)
	node, ok := findOrganizationNodeByCode(nodes, code)
	if !ok {
		return OrganizationNodeDTO{}, "", ErrNotFound
	}
	organizationID := organizationIDForNode(graph, node.ID)
	if strings.TrimSpace(organizationID) == "" {
		return OrganizationNodeDTO{}, "", fmt.Errorf("%w: organization node %s is not attached to an organization", ErrInvalidRequest, node.ID)
	}
	return node, organizationID, nil
}

func scopedRoleAuthorizationObjectID(organizationID string, roleID string) string {
	organizationID = strings.TrimSpace(organizationID)
	roleID = strings.TrimSpace(roleID)
	if organizationID == "" {
		return roleID
	}
	return organizationID + "/" + roleID
}

func authorizationPrincipalFromViewer(viewer ViewerRef, tenantID string) identity.Principal {
	viewerID := strings.TrimSpace(viewer.ViewerID)
	viewerKind := authorizationViewerKind(viewer.ViewerKind)
	subjectID := authorizationSubjectIDFromViewer(viewer, viewerKind)
	authorizationScopes := viewer.AuthorizationScope
	if authorizationScopes == nil {
		authorizationScopes = viewer.PermissionScope
	}
	return identity.NormalizePrincipal(identity.Principal{
		SubjectProvider: "operations",
		Subject:         subjectID,
		UserID:          subjectID,
		Username:        viewerID,
		DisplayName:     strings.TrimSpace(viewer.DisplayName),
		AgentProfileID:  strings.TrimSpace(viewer.AgentProfileID),
		ViewerKind:      viewerKind,
		TenantID:        strings.TrimSpace(tenantID),
		CoarseScopes:    append([]string(nil), authorizationScopes...),
	})
}

func authorizationSubjectIDFromViewer(viewer ViewerRef, viewerKind string) string {
	switch viewerKind {
	case identity.ViewerKindAgent, identity.ViewerKindSystem:
		return strings.TrimSpace(viewer.ViewerID)
	default:
		return strings.TrimSpace(firstNonEmpty(viewer.AgentProfileID, viewer.ViewerID))
	}
}

func authorizationViewerKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "human", identity.ViewerKindUser:
		return identity.ViewerKindUser
	case "ai", identity.ViewerKindAgent:
		return identity.ViewerKindAgent
	case identity.ViewerKindSystem:
		return identity.ViewerKindSystem
	default:
		return identity.ViewerKindUser
	}
}

func authorizationOwnerManageAction(viewer ViewerRef) string {
	if viewerHasOperationsPermission(viewer, PermissionOrgManage) {
		return authorizationActionOrganizationManage
	}
	if viewerHasOperationsPermission(viewer, PermissionRolesManage) {
		return authorizationActionRoleManage
	}
	return authorizationActionOrganizationManage
}

func viewerHasOperationsPermission(viewer ViewerRef, permission string) bool {
	for _, granted := range viewer.PermissionScope {
		if operationsPermissionScopeMatches(strings.TrimSpace(granted), permission) {
			return true
		}
	}
	return false
}
