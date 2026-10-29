package operations

import (
	"context"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/rebac"
	toolres "orrery_v2/internal/resource/tool"
)

type AgentDirectoryPort interface {
	QueryDirectory(ctx context.Context, q agentcore.AgentDirectoryQuery) (agentcore.AgentDirectoryPage, error)
}

type AgentProfileLookupPort interface {
	GetProfile(ctx context.Context, id agentcore.AgentProfileID) (agentcore.AgentProfile, error)
}

type AgentProfileBatchLookupPort interface {
	GetProfiles(ctx context.Context, ids []agentcore.AgentProfileID) (map[agentcore.AgentProfileID]agentcore.AgentProfile, error)
}

type WorkforceAccountProjectionStateReaderPort interface {
	AccountProjectionState(ctx context.Context, personID string) (WorkforceAccountProjectionDTO, bool, error)
}

type ToolRegistryPort interface {
	List(ctx context.Context) ([]toolres.ToolDefinition, error)
}

type OrganizationCatalogPort interface {
	ListOrganizations(ctx context.Context, query OrgGraphQuery) (OrgGraphDTO, error)
}

type QueryService struct {
	agents        AgentDirectoryPort
	tools         ToolRegistryPort
	audit         AuditStorePort
	orgs          OrganizationCatalogPort
	roles         RoleStorePort
	rebac         ReBACStorePort
	missions      MissionProjectionRepository
	authorization AuthorizationCheckerPort
	grants        []TemporaryPermissionGrantDTO
	evals         EvaluationProjectionStore
	accountStates WorkforceAccountProjectionStateReaderPort
}

func NewQueryService(agents AgentDirectoryPort, tools ToolRegistryPort) *QueryService {
	return &QueryService{agents: agents, tools: tools}
}

func (s *QueryService) SetWorkforceAccountProjectionStateReader(reader WorkforceAccountProjectionStateReaderPort) {
	if s != nil {
		s.accountStates = reader
	}
}

type ReBACStorePort interface {
	CheckRelation(ctx context.Context, check rebac.RelationCheck) (bool, rebac.RelationTuple, error)
}

type ViewerRef struct {
	ViewerID           string
	ViewerKind         string
	DisplayName        string
	AgentProfileID     string
	PermissionScope    []string
	AuthorizationScope []string
	RequestID          string
	IdempotencyKey     string
}

const (
	PermissionOperationsRead    = "operations.read"
	PermissionOperationsManage  = "operations.manage"
	PermissionOrgRead           = "org.read"
	PermissionOrgManage         = "org.manage"
	PermissionWorkforceRead     = "workforce.read"
	PermissionWorkforceManage   = "workforce.manage"
	PermissionRolesRead         = "roles.read"
	PermissionRolesManage       = "roles.manage"
	PermissionPermissionsRead   = "permissions.read"
	PermissionPermissionsManage = "permissions.manage"
	PermissionAuditRead         = "audit.read"
	PermissionAuditManage       = "audit.manage"
	PermissionAgentRun          = "agent.run"
	PermissionAgentTraceRead    = "agent.trace.read"
	PermissionAgentTraceManage  = "agent.trace.manage"
	PermissionToolCall          = "tool.call"
)

func (s *QueryService) SetPermissionGrants(grants []TemporaryPermissionGrantDTO) {
	if s == nil {
		return
	}
	s.grants = append([]TemporaryPermissionGrantDTO(nil), grants...)
}

func (s *QueryService) ListWorkforceMembers(ctx context.Context, viewer ViewerRef, query WorkforceQuery) (WorkforcePageDTO, error) {
	if s == nil || s.agents == nil {
		return WorkforcePageDTO{}, ErrServiceDependency
	}
	if err := requireOperationsPermission(viewer, PermissionWorkforceRead); err != nil {
		return WorkforcePageDTO{}, err
	}
	if err := s.authorizeWorkforceRead(ctx, viewer, query); err != nil {
		return WorkforcePageDTO{}, err
	}
	page, err := s.agents.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
		Organization:  agentcore.OrganizationID(query.OrganizationID),
		Department:    agentcore.DepartmentID(query.DepartmentID),
		EnabledOnly:   query.EnabledOnly,
		IncludeAI:     true,
		IncludeHumans: true,
		Placement:     agentcore.TalentPlacementFilterInternal,
		Page:          agentcore.Page{Limit: query.Limit, Offset: query.Offset},
	})
	if err != nil {
		return WorkforcePageDTO{}, err
	}
	members := make([]WorkforceMemberDTO, 0, len(page.Entries))
	profiles, profilesBatched := s.batchMemberProfiles(ctx, page.Entries, query.IncludeDetails)
	permissionRoleIDs, err := s.workforcePermissionRoleIDs(ctx)
	if err != nil {
		return WorkforcePageDTO{}, err
	}
	for _, entry := range page.Entries {
		if entry.AgentProfileID.IsZero() {
			continue
		}
		member := s.mapMemberWithProfiles(ctx, entry, query.IncludeDetails, profiles, profilesBatched)
		splitWorkforceRoleIDs(&member, permissionRoleIDs)
		s.applyWorkforceAccountProjectionState(ctx, &member)
		members = append(members, member)
	}
	if s.orgs != nil {
		if err := s.applyOrganizationCatalogNames(ctx, query.OrganizationID, members); err != nil {
			return WorkforcePageDTO{}, err
		}
	}
	return WorkforcePageDTO{
		Members: members,
		Page:    PageDTO{Limit: page.Limit, Offset: page.Offset, Total: page.Total, Returned: len(members), HasMore: page.Offset+len(members) < page.Total},
	}, nil
}

func (s *QueryService) applyWorkforceAccountProjectionState(ctx context.Context, member *WorkforceMemberDTO) {
	if s == nil || s.accountStates == nil || member == nil || member.AgentKind != string(agentcore.AgentKindHuman) {
		return
	}
	state, found, err := s.accountStates.AccountProjectionState(ctx, member.AgentProfileID)
	if err != nil {
		member.AccountProjection.Status = "unknown"
		return
	}
	if found {
		state.InitialPassword = ""
		member.AccountProjection = state
	}
}

func (s *QueryService) ListTalentMarketMembers(ctx context.Context, viewer ViewerRef, query WorkforceQuery) (WorkforcePageDTO, error) {
	if s == nil || s.agents == nil {
		return WorkforcePageDTO{}, ErrServiceDependency
	}
	if err := requireOperationsPermission(viewer, PermissionWorkforceRead); err != nil {
		return WorkforcePageDTO{}, err
	}
	if err := s.authorizePlatformOperation(ctx, viewer, PermissionWorkforceRead); err != nil {
		return WorkforcePageDTO{}, err
	}
	page, err := s.agents.QueryDirectory(ctx, agentcore.AgentDirectoryQuery{
		EnabledOnly:   query.EnabledOnly,
		IncludeAI:     true,
		IncludeHumans: true,
		Placement:     agentcore.TalentPlacementFilterMarket,
		Page:          agentcore.Page{Limit: query.Limit, Offset: query.Offset},
	})
	if err != nil {
		return WorkforcePageDTO{}, err
	}
	members := make([]WorkforceMemberDTO, 0, len(page.Entries))
	profiles, profilesBatched := s.batchMemberProfiles(ctx, page.Entries, query.IncludeDetails)
	permissionRoleIDs, err := s.workforcePermissionRoleIDs(ctx)
	if err != nil {
		return WorkforcePageDTO{}, err
	}
	for _, entry := range page.Entries {
		if entry.AgentProfileID.IsZero() {
			continue
		}
		member := s.mapMemberWithProfiles(ctx, entry, query.IncludeDetails, profiles, profilesBatched)
		splitWorkforceRoleIDs(&member, permissionRoleIDs)
		members = append(members, member)
	}
	return WorkforcePageDTO{
		Members: members,
		Page:    PageDTO{Limit: page.Limit, Offset: page.Offset, Total: page.Total, Returned: len(members), HasMore: page.Offset+len(members) < page.Total},
	}, nil
}

func (s *QueryService) applyOrganizationCatalogNames(ctx context.Context, organizationID string, members []WorkforceMemberDTO) error {
	catalog, err := s.orgs.ListOrganizations(ctx, OrgGraphQuery{OrganizationID: organizationID})
	if err != nil {
		return err
	}
	departments := map[string]DepartmentDTO{}
	for _, department := range catalog.Departments {
		departments[department.DepartmentID] = department
	}
	positions := map[string]PositionDTO{}
	for _, position := range catalog.Positions {
		positions[position.PositionID] = position
	}
	for i := range members {
		if department := departments[members[i].DepartmentID]; department.Name != "" {
			members[i].DepartmentDisplayName = department.Name
		}
		if position := positions[members[i].PositionID]; position.Name != "" {
			members[i].PositionDisplayName = position.Name
		}
	}
	return nil
}

func (s *QueryService) GetOrgGraph(ctx context.Context, viewer ViewerRef, query OrgGraphQuery) (OrgGraphDTO, error) {
	if err := requireOperationsPermission(viewer, PermissionOrgRead); err != nil {
		return OrgGraphDTO{}, err
	}
	if strings.TrimSpace(query.OrganizationID) != "" {
		if err := s.authorizeOperationObject(ctx, viewer, authorizationActionOrganizationRead, authorizationObjectOrganization, query.OrganizationID, query.OrganizationID); err != nil {
			return OrgGraphDTO{}, err
		}
	} else if err := s.authorizePlatformOperation(ctx, viewer, PermissionOrgRead); err != nil {
		return OrgGraphDTO{}, err
	}
	var members []WorkforceMemberDTO
	if query.IncludeMembers {
		workforceViewer := viewer
		workforceViewer.PermissionScope = append(append([]string(nil), viewer.PermissionScope...), PermissionWorkforceRead)
		workforce, err := s.ListWorkforceMembers(ctx, workforceViewer, WorkforceQuery{OrganizationID: query.OrganizationID, EnabledOnly: true})
		if err != nil {
			return OrgGraphDTO{}, err
		}
		members = workforce.Members
	}
	graph := OrgGraphDTO{Members: members}
	if s != nil && s.orgs != nil {
		catalog, err := s.orgs.ListOrganizations(ctx, query)
		if err != nil {
			return OrgGraphDTO{}, err
		}
		graph.Organizations = append(graph.Organizations, catalog.Organizations...)
		graph.Departments = append(graph.Departments, catalog.Departments...)
		graph.Positions = append(graph.Positions, catalog.Positions...)
	}
	organizations := map[string]OrganizationDTO{}
	for _, organization := range graph.Organizations {
		organizations[organization.OrganizationID] = organization
	}
	departments := map[string]DepartmentDTO{}
	departmentCounts := map[string]int{}
	for _, department := range graph.Departments {
		departments[department.DepartmentID] = department
	}
	positions := map[string]PositionDTO{}
	positionCounts := map[string]int{}
	for _, position := range graph.Positions {
		positions[position.PositionID] = position
	}
	for _, member := range members {
		if member.OrganizationID != "" {
			organization := organizations[member.OrganizationID]
			if organization.OrganizationID == "" {
				organization = OrganizationDTO{OrganizationID: member.OrganizationID, Name: member.OrganizationID}
			}
			organizations[member.OrganizationID] = organization
		}
		if member.DepartmentID != "" {
			department := departments[member.DepartmentID]
			if department.DepartmentID == "" {
				department = DepartmentDTO{DepartmentID: member.DepartmentID, OrganizationID: member.OrganizationID, Name: member.DepartmentDisplayName}
			}
			if department.OrganizationID == "" {
				department.OrganizationID = member.OrganizationID
			}
			if department.Name == "" {
				department.Name = member.DepartmentDisplayName
			}
			departments[member.DepartmentID] = department
			departmentCounts[member.DepartmentID]++
		}
		if member.PositionID != "" {
			position := positions[member.PositionID]
			if position.PositionID == "" {
				position = PositionDTO{PositionID: member.PositionID, DepartmentID: member.DepartmentID, Name: member.PositionDisplayName}
			}
			if position.DepartmentID == "" {
				position.DepartmentID = member.DepartmentID
			}
			if position.Name == "" {
				position.Name = member.PositionDisplayName
			}
			positions[member.PositionID] = position
			positionCounts[member.PositionID]++
		}
		if member.ManagerAgentID != "" {
			graph.ReportingEdges = append(graph.ReportingEdges, ReportingEdgeDTO{FromAgentID: member.AgentID, ToManagerAgentID: member.ManagerAgentID, ReportingLineID: member.ReportingLineID})
		}
	}
	graph.Organizations = graph.Organizations[:0]
	for _, organization := range organizations {
		if query.OrganizationID != "" && organization.OrganizationID != query.OrganizationID {
			continue
		}
		graph.Organizations = append(graph.Organizations, organization)
	}
	graph.Departments = graph.Departments[:0]
	for departmentID, department := range departments {
		if query.OrganizationID != "" && department.OrganizationID != query.OrganizationID {
			continue
		}
		department.MemberCount = departmentCounts[departmentID]
		if department.Name == "" {
			department.Name = department.DepartmentID
		}
		graph.Departments = append(graph.Departments, department)
	}
	graph.Positions = graph.Positions[:0]
	for positionID, position := range positions {
		department := departments[position.DepartmentID]
		if query.OrganizationID != "" && department.OrganizationID != query.OrganizationID {
			continue
		}
		position.MemberCount = positionCounts[positionID]
		if position.Name == "" {
			position.Name = position.PositionID
		}
		graph.Positions = append(graph.Positions, position)
	}
	graph.OrganizationNodes, graph.Summary = buildOrganizationNodes(graph)
	if s != nil && s.roles != nil {
		assignments, err := s.roles.ListRoleAssignments(ctx, RoleAssignmentQuery{})
		if err != nil {
			return OrgGraphDTO{}, err
		}
		attachRoleAssignmentsToOrganizationNodes(graph.OrganizationNodes, assignments)
		graph.Summary.RoleAssignmentCount = 0
		for _, node := range flattenOrganizationNodes(graph.OrganizationNodes) {
			graph.Summary.RoleAssignmentCount += len(node.RoleAssignments)
		}
	}
	return graph, nil
}
