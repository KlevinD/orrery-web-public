package operations

import (
	"sort"
	"strings"
	"time"
)

const (
	organizationNodeTypeCompany    = "company"
	organizationNodeTypeDepartment = "department"
	organizationStatusActive       = "active"
)

func buildOrganizationNodes(graph OrgGraphDTO) ([]OrganizationNodeDTO, OrgGraphSummaryDTO) {
	graph = backfillOrganizationCodes(graph)
	directCounts := organizationNodeDirectCounts(graph.Members)
	nodesByID := map[string]*OrganizationNodeDTO{}
	parentByID := map[string]string{}
	roots := make([]*OrganizationNodeDTO, 0, len(graph.Organizations))

	sort.SliceStable(graph.Organizations, func(i, j int) bool {
		return compareOrganizationDTO(graph.Organizations[i], graph.Organizations[j])
	})
	for _, organization := range graph.Organizations {
		code := organization.Code
		node := OrganizationNodeDTO{
			ID:                organization.OrganizationID,
			NodeType:          firstNonEmpty(organization.NodeType, organizationNodeTypeCompany),
			Code:              code,
			DisplayCode:       firstNonEmpty(organization.DisplayCode, displayOrganizationCode(code)),
			Name:              firstNonEmpty(organization.Name, organization.OrganizationID),
			Level:             organizationCodeLevel(code),
			Status:            normalizeOrganizationNodeStatus(organization.Status),
			OwnerProfileID:    organization.OwnerProfileID,
			OwnerEmployeeNo:   organization.OwnerEmployeeNo,
			OwnerName:         organization.OwnerName,
			KnowledgeBaseID:   organization.KnowledgeBaseID,
			DirectMemberCount: directCounts[organization.OrganizationID],
		}
		nodesByID[node.ID] = &node
		roots = append(roots, &node)
	}

	sort.SliceStable(graph.Departments, func(i, j int) bool {
		return compareDepartmentDTO(graph.Departments[i], graph.Departments[j])
	})
	for _, department := range graph.Departments {
		code := department.Code
		parentID := firstNonEmpty(department.ParentDepartmentID, department.OrganizationID)
		parent := nodesByID[parentID]
		node := OrganizationNodeDTO{
			ID:                department.DepartmentID,
			NodeType:          firstNonEmpty(department.NodeType, organizationNodeTypeDepartment),
			Code:              code,
			DisplayCode:       firstNonEmpty(department.DisplayCode, displayOrganizationCode(code)),
			Name:              firstNonEmpty(department.Name, department.DepartmentID),
			Level:             organizationCodeLevel(code),
			Status:            normalizeOrganizationNodeStatus(department.Status),
			OwnerProfileID:    department.OwnerProfileID,
			OwnerEmployeeNo:   department.OwnerEmployeeNo,
			OwnerName:         department.OwnerName,
			ParentID:          parentID,
			DirectMemberCount: directCounts[department.DepartmentID],
		}
		if node.DirectMemberCount == 0 && department.MemberCount > 0 {
			node.DirectMemberCount = department.MemberCount
		}
		if parent != nil {
			node.ParentCode = parent.Code
			node.ParentDisplayCode = parent.DisplayCode
			node.ParentName = parent.Name
		}
		nodesByID[node.ID] = &node
		parentByID[node.ID] = parentID
	}

	childrenByParentID := map[string][]*OrganizationNodeDTO{}
	for nodeID, parentID := range parentByID {
		node := nodesByID[nodeID]
		parent := nodesByID[parentID]
		if node == nil || parent == nil {
			continue
		}
		childrenByParentID[parent.ID] = append(childrenByParentID[parent.ID], node)
	}

	out := make([]OrganizationNodeDTO, 0, len(roots))
	var summary OrgGraphSummaryDTO
	summary.RootCount = len(roots)
	for _, root := range roots {
		node := buildOrganizationNodeTree(root, childrenByParentID)
		totalMembers := computeOrganizationNodeTotals(&node)
		summary.OrganizationCount += countOrganizationNodes(node)
		summary.DirectMemberCount += node.DirectMemberCount
		summary.TotalMemberCount += totalMembers
		out = append(out, node)
	}
	for _, node := range flattenOrganizationNodes(out) {
		if node.Status == organizationStatusActive {
			summary.ActiveCount++
		} else {
			summary.InactiveCount++
		}
		summary.RoleAssignmentCount += len(node.RoleAssignments)
	}
	sortOrganizationNodeChildren(out)
	return out, summary
}

func attachRoleAssignmentsToOrganizationNodes(nodes []OrganizationNodeDTO, assignments []RoleAssignmentDTO) {
	for i := range nodes {
		attachRoleAssignmentsToOrganizationNode(&nodes[i], assignments)
	}
}

func attachRoleAssignmentsToOrganizationNode(node *OrganizationNodeDTO, assignments []RoleAssignmentDTO) {
	if node == nil {
		return
	}
	for _, assignment := range effectiveRoleAssignments(assignments, node.Code, nowForRoleSummary()) {
		if normalizeOrganizationCodeForQuery(assignment.OrganizationCode) == node.Code {
			node.PermissionSummary.DirectRoleCount++
		} else {
			node.PermissionSummary.InheritedRoleCount++
		}
		node.RoleAssignments = append(node.RoleAssignments, assignment)
	}
	node.PermissionSummary.EffectiveRoleCount = len(node.RoleAssignments)
	for i := range node.Children {
		attachRoleAssignmentsToOrganizationNode(&node.Children[i], assignments)
	}
}

func nowForRoleSummary() time.Time {
	return time.Now().UTC()
}

func normalizeOrganizationNodeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "active", "inactive", "draft", "archived":
		return strings.ToLower(strings.TrimSpace(status))
	default:
		return organizationStatusActive
	}
}

func organizationNodeDirectCounts(members []WorkforceMemberDTO) map[string]int {
	counts := map[string]int{}
	for _, member := range members {
		if !member.Enabled {
			continue
		}
		if member.DepartmentID != "" {
			counts[member.DepartmentID]++
			continue
		}
		if member.OrganizationID != "" {
			counts[member.OrganizationID]++
		}
	}
	return counts
}

func backfillOrganizationCodes(graph OrgGraphDTO) OrgGraphDTO {
	sort.SliceStable(graph.Organizations, func(i, j int) bool {
		return compareOrganizationDTO(graph.Organizations[i], graph.Organizations[j])
	})
	for i := range graph.Organizations {
		organization := &graph.Organizations[i]
		if !validOrganizationCode(organization.Code) {
			organization.Code = twoDigitSuffix(i + 1)
		}
		organization.DisplayCode = displayOrganizationCode(organization.Code)
		organization.Status = normalizeOrganizationNodeStatus(organization.Status)
		organization.NodeType = firstNonEmpty(organization.NodeType, organizationNodeTypeCompany)
	}

	childrenByParent := map[string][]int{}
	rootByOrganization := map[string]string{}
	for _, organization := range graph.Organizations {
		rootByOrganization[organization.OrganizationID] = organization.Code
	}
	for i := range graph.Departments {
		department := graph.Departments[i]
		parentKey := firstNonEmpty(department.ParentDepartmentID, "org:"+department.OrganizationID)
		childrenByParent[parentKey] = append(childrenByParent[parentKey], i)
	}
	assignDepartmentCodes(graph.Departments, childrenByParent, rootByOrganization)
	for i := range graph.Departments {
		graph.Departments[i].DisplayCode = displayOrganizationCode(graph.Departments[i].Code)
		graph.Departments[i].Status = normalizeOrganizationNodeStatus(graph.Departments[i].Status)
		graph.Departments[i].NodeType = firstNonEmpty(graph.Departments[i].NodeType, organizationNodeTypeDepartment)
	}
	return graph
}

func assignDepartmentCodes(departments []DepartmentDTO, childrenByParent map[string][]int, rootByOrganization map[string]string) {
	var assignChildren func(parentKey string, parentCode string)
	assignChildren = func(parentKey string, parentCode string) {
		childIndexes := childrenByParent[parentKey]
		sort.SliceStable(childIndexes, func(i, j int) bool {
			return compareDepartmentDTO(departments[childIndexes[i]], departments[childIndexes[j]])
		})
		siblingCodes := make([]string, 0, len(childIndexes))
		for _, index := range childIndexes {
			if validOrganizationCode(departments[index].Code) {
				siblingCodes = append(siblingCodes, departments[index].Code)
			}
		}
		for _, index := range childIndexes {
			if !validOrganizationCode(departments[index].Code) {
				next, err := nextOrganizationCode(parentCode, siblingCodes)
				if err == nil {
					departments[index].Code = next
					siblingCodes = append(siblingCodes, next)
				}
			}
		}
		for _, index := range childIndexes {
			assignChildren(departments[index].DepartmentID, departments[index].Code)
		}
	}
	for organizationID, code := range rootByOrganization {
		assignChildren("org:"+organizationID, code)
	}
}

func buildOrganizationNodeTree(node *OrganizationNodeDTO, childrenByParentID map[string][]*OrganizationNodeDTO) OrganizationNodeDTO {
	out := *node
	out.Children = nil
	children := childrenByParentID[node.ID]
	sort.SliceStable(children, func(i, j int) bool {
		return compareOrganizationNodeDTO(*children[i], *children[j])
	})
	for _, child := range children {
		out.Children = append(out.Children, buildOrganizationNodeTree(child, childrenByParentID))
	}
	return out
}

func computeOrganizationNodeTotals(node *OrganizationNodeDTO) int {
	sortOrganizationNodeChildren(node.Children)
	total := node.DirectMemberCount
	for i := range node.Children {
		total += computeOrganizationNodeTotals(&node.Children[i])
	}
	node.TotalMemberCount = total
	node.ChildCount = len(node.Children)
	return total
}

func sortOrganizationNodeChildren(nodes []OrganizationNodeDTO) {
	sort.SliceStable(nodes, func(i, j int) bool {
		if nodes[i].Code != nodes[j].Code {
			return nodes[i].Code < nodes[j].Code
		}
		if nodes[i].Name != nodes[j].Name {
			return nodes[i].Name < nodes[j].Name
		}
		return nodes[i].ID < nodes[j].ID
	})
	for i := range nodes {
		sortOrganizationNodeChildren(nodes[i].Children)
	}
}

func countOrganizationNodes(node OrganizationNodeDTO) int {
	total := 1
	for _, child := range node.Children {
		total += countOrganizationNodes(child)
	}
	return total
}

func flattenOrganizationNodes(nodes []OrganizationNodeDTO) []OrganizationNodeDTO {
	flat := make([]OrganizationNodeDTO, 0)
	for _, node := range nodes {
		flat = append(flat, node)
		flat = append(flat, flattenOrganizationNodes(node.Children)...)
	}
	return flat
}

func compareOrganizationDTO(left, right OrganizationDTO) bool {
	if left.Name != right.Name {
		return left.Name < right.Name
	}
	return left.OrganizationID < right.OrganizationID
}

func compareDepartmentDTO(left, right DepartmentDTO) bool {
	if left.ParentDepartmentID != right.ParentDepartmentID {
		return left.ParentDepartmentID < right.ParentDepartmentID
	}
	if left.Name != right.Name {
		return left.Name < right.Name
	}
	return left.DepartmentID < right.DepartmentID
}

func compareOrganizationNodeDTO(left, right OrganizationNodeDTO) bool {
	if left.Code != right.Code {
		return left.Code < right.Code
	}
	if left.Name != right.Name {
		return left.Name < right.Name
	}
	return left.ID < right.ID
}
