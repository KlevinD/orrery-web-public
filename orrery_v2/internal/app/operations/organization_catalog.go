package operations

import (
	"context"
	"sort"
	"strings"

	"orrery_v2/internal/platform/db"
)

const (
	organizationCatalogTable = "core.operations_organizations"
	departmentCatalogTable   = "core.operations_departments"
	positionCatalogTable     = "core.operations_positions"
)

type OrganizationCatalogStore struct {
	router *db.Router
}

func NewOrganizationCatalogStore(router *db.Router) *OrganizationCatalogStore {
	return &OrganizationCatalogStore{router: router}
}

func (s *OrganizationCatalogStore) SaveOrganization(ctx context.Context, organization OrganizationDTO) error {
	if s == nil || s.router == nil {
		return ErrServiceDependency
	}
	return s.router.PutRecord(ctx, organizationCatalogTable, organization.OrganizationID, db.Record{
		"organization_id":       organization.OrganizationID,
		"name":                  organization.Name,
		"default_department_id": organization.DefaultDepartmentID,
		"default_position_id":   organization.DefaultPositionID,
		"code":                  organization.Code,
		"status":                normalizeOrganizationNodeStatus(organization.Status),
		"owner_profile_id":      organization.OwnerProfileID,
		"owner_employee_no":     organization.OwnerEmployeeNo,
		"owner_name":            organization.OwnerName,
		"knowledge_base_id":     organization.KnowledgeBaseID,
		"node_type":             firstNonEmpty(organization.NodeType, organizationNodeTypeCompany),
	})
}

func (s *OrganizationCatalogStore) SaveDepartment(ctx context.Context, department DepartmentDTO) error {
	if s == nil || s.router == nil {
		return ErrServiceDependency
	}
	return s.router.PutRecord(ctx, departmentCatalogTable, department.DepartmentID, db.Record{
		"department_id":        department.DepartmentID,
		"organization_id":      department.OrganizationID,
		"name":                 department.Name,
		"parent_department_id": department.ParentDepartmentID,
		"default_position_id":  department.DefaultPositionID,
		"code":                 department.Code,
		"status":               normalizeOrganizationNodeStatus(department.Status),
		"owner_profile_id":     department.OwnerProfileID,
		"owner_employee_no":    department.OwnerEmployeeNo,
		"owner_name":           department.OwnerName,
		"node_type":            firstNonEmpty(department.NodeType, organizationNodeTypeDepartment),
	})
}

func (s *OrganizationCatalogStore) SavePosition(ctx context.Context, position PositionDTO) error {
	if s == nil || s.router == nil {
		return ErrServiceDependency
	}
	return s.router.PutRecord(ctx, positionCatalogTable, position.PositionID, db.Record{
		"position_id":       position.PositionID,
		"department_id":     position.DepartmentID,
		"name":              position.Name,
		"knowledge_base_id": position.KnowledgeBaseID,
	})
}

func (s *OrganizationCatalogStore) GetOrganization(ctx context.Context, organizationID string) (OrganizationDTO, bool, error) {
	if s == nil || s.router == nil {
		return OrganizationDTO{}, false, ErrServiceDependency
	}
	organizationID = strings.TrimSpace(organizationID)
	if organizationID == "" {
		return OrganizationDTO{}, false, nil
	}
	record, found, err := s.router.GetRecord(ctx, organizationCatalogTable, organizationID)
	if err != nil || !found {
		return OrganizationDTO{}, found, err
	}
	organization := organizationFromRecord(record)
	if organization.OrganizationID != organizationID {
		return OrganizationDTO{}, false, ErrServiceDependency
	}
	return organization, true, nil
}

func (s *OrganizationCatalogStore) ListOrganizationCatalog(ctx context.Context, afterID string, limit int) ([]OrganizationDTO, bool, error) {
	if s == nil || s.router == nil {
		return nil, false, ErrServiceDependency
	}
	page, err := s.router.ListRecords(ctx, organizationCatalogTable, afterID, limit)
	if err != nil {
		return nil, false, err
	}
	organizations := make([]OrganizationDTO, 0, len(page.Items))
	for _, item := range page.Items {
		organization := organizationFromRecord(item.Record)
		if organization.OrganizationID == "" || organization.OrganizationID != item.Key {
			return nil, false, ErrServiceDependency
		}
		organizations = append(organizations, organization)
	}
	return organizations, page.HasMore, nil
}

func (s *OrganizationCatalogStore) DeleteOrganization(ctx context.Context, organizationID string) error {
	if s == nil || s.router == nil {
		return ErrServiceDependency
	}
	return s.router.DeleteRecord(ctx, organizationCatalogTable, organizationID)
}

func (s *OrganizationCatalogStore) DeleteDepartment(ctx context.Context, departmentID string) error {
	if s == nil || s.router == nil {
		return ErrServiceDependency
	}
	return s.router.DeleteRecord(ctx, departmentCatalogTable, departmentID)
}

func (s *OrganizationCatalogStore) DeletePosition(ctx context.Context, positionID string) error {
	if s == nil || s.router == nil {
		return ErrServiceDependency
	}
	return s.router.DeleteRecord(ctx, positionCatalogTable, positionID)
}

func (s *OrganizationCatalogStore) ListOrganizations(ctx context.Context, query OrgGraphQuery) (OrgGraphDTO, error) {
	if s == nil || s.router == nil {
		return OrgGraphDTO{}, ErrServiceDependency
	}
	var graph OrgGraphDTO
	organizations, err := s.records(ctx, organizationCatalogTable)
	if err != nil {
		return OrgGraphDTO{}, err
	}
	departments, err := s.records(ctx, departmentCatalogTable)
	if err != nil {
		return OrgGraphDTO{}, err
	}
	positions, err := s.records(ctx, positionCatalogTable)
	if err != nil {
		return OrgGraphDTO{}, err
	}
	departmentsByID := map[string]DepartmentDTO{}
	for _, record := range organizations {
		organization := organizationFromRecord(record)
		if query.OrganizationID != "" && organization.OrganizationID != query.OrganizationID {
			continue
		}
		graph.Organizations = append(graph.Organizations, organization)
	}
	for _, record := range departments {
		department := departmentFromRecord(record)
		if query.OrganizationID != "" && department.OrganizationID != query.OrganizationID {
			continue
		}
		departmentsByID[department.DepartmentID] = department
		graph.Departments = append(graph.Departments, department)
	}
	for _, record := range positions {
		position := positionFromRecord(record)
		department := departmentsByID[position.DepartmentID]
		if query.OrganizationID != "" && department.OrganizationID != query.OrganizationID {
			continue
		}
		graph.Positions = append(graph.Positions, position)
	}
	sort.Slice(graph.Organizations, func(i, j int) bool { return graph.Organizations[i].Name < graph.Organizations[j].Name })
	sort.Slice(graph.Departments, func(i, j int) bool { return graph.Departments[i].Name < graph.Departments[j].Name })
	sort.Slice(graph.Positions, func(i, j int) bool { return graph.Positions[i].Name < graph.Positions[j].Name })
	return graph, nil
}

func (s *OrganizationCatalogStore) records(ctx context.Context, logicalTable string) ([]db.Record, error) {
	if s == nil || s.router == nil {
		return nil, ErrServiceDependency
	}
	return listOperationRecords(ctx, s.router, logicalTable)
}

func organizationFromRecord(record db.Record) OrganizationDTO {
	return OrganizationDTO{
		OrganizationID:      recordString(record, "organization_id"),
		Name:                recordString(record, "name"),
		DefaultDepartmentID: recordString(record, "default_department_id"),
		DefaultPositionID:   recordString(record, "default_position_id"),
		Code:                recordString(record, "code"),
		DisplayCode:         displayOrganizationCode(recordString(record, "code")),
		Status:              normalizeOrganizationNodeStatus(recordString(record, "status")),
		OwnerProfileID:      recordString(record, "owner_profile_id"),
		OwnerEmployeeNo:     recordString(record, "owner_employee_no"),
		OwnerName:           recordString(record, "owner_name"),
		KnowledgeBaseID:     recordString(record, "knowledge_base_id"),
		NodeType:            firstNonEmpty(recordString(record, "node_type"), organizationNodeTypeCompany),
	}
}

func departmentFromRecord(record db.Record) DepartmentDTO {
	return DepartmentDTO{
		DepartmentID:       recordString(record, "department_id"),
		OrganizationID:     recordString(record, "organization_id"),
		Name:               recordString(record, "name"),
		ParentDepartmentID: recordString(record, "parent_department_id"),
		DefaultPositionID:  recordString(record, "default_position_id"),
		Code:               recordString(record, "code"),
		DisplayCode:        displayOrganizationCode(recordString(record, "code")),
		Status:             normalizeOrganizationNodeStatus(recordString(record, "status")),
		OwnerProfileID:     recordString(record, "owner_profile_id"),
		OwnerEmployeeNo:    recordString(record, "owner_employee_no"),
		OwnerName:          recordString(record, "owner_name"),
		NodeType:           firstNonEmpty(recordString(record, "node_type"), organizationNodeTypeDepartment),
	}
}

func positionFromRecord(record db.Record) PositionDTO {
	return PositionDTO{
		PositionID:      recordString(record, "position_id"),
		DepartmentID:    recordString(record, "department_id"),
		Name:            recordString(record, "name"),
		KnowledgeBaseID: recordString(record, "knowledge_base_id"),
	}
}
