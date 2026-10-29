package operations

import "testing"

func TestBuildOrganizationNodesAddsCodesCountsParentsAndOptionalOwner(t *testing.T) {
	graph := OrgGraphDTO{
		Organizations: []OrganizationDTO{{
			OrganizationID:      "org-1",
			Name:                "Orrery",
			Code:                "01",
			Status:              "active",
			OwnerProfileID:      "",
			OwnerEmployeeNo:     "",
			OwnerName:           "",
			DefaultDepartmentID: "dept-root",
		}},
		Departments: []DepartmentDTO{
			{DepartmentID: "dept-root", OrganizationID: "org-1", Name: "总裁办", Code: "0101", Status: "active", MemberCount: 1},
			{DepartmentID: "dept-ai", OrganizationID: "org-1", Name: "AI 平台", ParentDepartmentID: "dept-root", Code: "010101", Status: "active", MemberCount: 2},
		},
		Members: []WorkforceMemberDTO{
			{AgentProfileID: "p1", AgentID: "p1", OrganizationID: "org-1", DepartmentID: "dept-root", Enabled: true},
			{AgentProfileID: "p2", AgentID: "p2", OrganizationID: "org-1", DepartmentID: "dept-ai", Enabled: true},
			{AgentProfileID: "p3", AgentID: "p3", OrganizationID: "org-1", DepartmentID: "dept-ai", Enabled: true},
		},
	}

	nodes, summary := buildOrganizationNodes(graph)

	if summary.OrganizationCount != 3 || summary.TotalMemberCount != 3 {
		t.Fatalf("summary = %+v", summary)
	}
	if len(nodes) != 1 {
		t.Fatalf("roots = %d, want 1", len(nodes))
	}
	root := nodes[0]
	if root.DisplayCode != "01" || root.Level != 1 || root.DirectMemberCount != 0 || root.TotalMemberCount != 3 {
		t.Fatalf("root = %+v", root)
	}
	if root.OwnerEmployeeNo != "" || root.OwnerName != "" {
		t.Fatalf("owner fields should be optional and empty: %+v", root)
	}
	if len(root.Children) != 1 {
		t.Fatalf("root children = %+v", root.Children)
	}
	dept := root.Children[0]
	if dept.DisplayCode != "01.01" || dept.ParentDisplayCode != "01" || dept.ParentName != "Orrery" {
		t.Fatalf("dept = %+v", dept)
	}
	if dept.DirectMemberCount != 1 || dept.TotalMemberCount != 3 {
		t.Fatalf("dept counts = direct %d total %d", dept.DirectMemberCount, dept.TotalMemberCount)
	}
	if len(dept.Children) != 1 {
		t.Fatalf("dept children = %+v", dept.Children)
	}
	child := dept.Children[0]
	if child.DisplayCode != "01.01.01" || child.ParentDisplayCode != "01.01" || child.DirectMemberCount != 2 || child.TotalMemberCount != 2 {
		t.Fatalf("child = %+v", child)
	}
}

func TestAttachRoleAssignmentsToOrganizationNodesSummarizesDirectAndInheritedRoles(t *testing.T) {
	nodes := []OrganizationNodeDTO{{
		ID:          "org-1",
		NodeType:    organizationNodeTypeCompany,
		Code:        "01",
		DisplayCode: "01",
		Name:        "Orrery",
		Status:      "active",
		Children: []OrganizationNodeDTO{{
			ID:          "dept-ai",
			NodeType:    organizationNodeTypeDepartment,
			Code:        "0101",
			DisplayCode: "01.01",
			Name:        "AI",
			Status:      "active",
			ParentID:    "org-1",
		}},
	}}

	attachRoleAssignmentsToOrganizationNodes(nodes, []RoleAssignmentDTO{
		{
			AssignmentID:       "owner",
			Actor:              ActorRefDTO{ActorType: "human", ActorID: "profile-owner"},
			RoleID:             "org.owner",
			OrganizationNodeID: "org-1",
			OrganizationCode:   "01",
			ScopeMode:          RoleScopeSelfAndDescendants,
			Source:             RoleSourceSystem,
		},
		{
			AssignmentID:       "manager",
			Actor:              ActorRefDTO{ActorType: "human", ActorID: "profile-manager"},
			RoleID:             "org.manager",
			OrganizationNodeID: "dept-ai",
			OrganizationCode:   "01.01",
			ScopeMode:          RoleScopeSelf,
			Source:             RoleSourceDirect,
		},
	})

	root := nodes[0]
	if len(root.RoleAssignments) != 1 || root.PermissionSummary.DirectRoleCount != 1 || root.PermissionSummary.EffectiveRoleCount != 1 {
		t.Fatalf("root role summary = %+v assignments=%+v", root.PermissionSummary, root.RoleAssignments)
	}
	child := root.Children[0]
	if len(child.RoleAssignments) != 2 {
		t.Fatalf("child assignments = %+v", child.RoleAssignments)
	}
	if child.PermissionSummary.DirectRoleCount != 1 || child.PermissionSummary.InheritedRoleCount != 1 || child.PermissionSummary.EffectiveRoleCount != 2 {
		t.Fatalf("child role summary = %+v", child.PermissionSummary)
	}
	if child.RoleAssignments[0].Source != RoleSourceInherited && child.RoleAssignments[1].Source != RoleSourceInherited {
		t.Fatalf("expected one inherited assignment on child: %+v", child.RoleAssignments)
	}
}

func TestBuildOrganizationNodesBackfillsDeterministicCodesForLegacyCatalog(t *testing.T) {
	graph := OrgGraphDTO{
		Organizations: []OrganizationDTO{
			{OrganizationID: "org-z", Name: "Zeta"},
			{OrganizationID: "org-a", Name: "Alpha"},
		},
		Departments: []DepartmentDTO{
			{DepartmentID: "dept-z-child", OrganizationID: "org-a", Name: "Child", ParentDepartmentID: "dept-a"},
			{DepartmentID: "dept-b", OrganizationID: "org-a", Name: "Beta"},
			{DepartmentID: "dept-a", OrganizationID: "org-a", Name: "Alpha"},
			{DepartmentID: "dept-z", OrganizationID: "org-z", Name: "Zed"},
		},
	}

	nodes, summary := buildOrganizationNodes(graph)

	if summary.RootCount != 2 || summary.OrganizationCount != 6 {
		t.Fatalf("summary = %+v", summary)
	}
	alpha := nodes[0]
	if alpha.ID != "org-a" || alpha.Code != "01" || alpha.DisplayCode != "01" {
		t.Fatalf("alpha root = %+v", alpha)
	}
	if got := alpha.Children[0]; got.ID != "dept-a" || got.Code != "0101" || got.DisplayCode != "01.01" || got.Level != 2 {
		t.Fatalf("alpha first child = %+v", got)
	}
	if got := alpha.Children[0].Children[0]; got.ID != "dept-z-child" || got.Code != "010101" || got.ParentCode != "0101" {
		t.Fatalf("alpha grandchild = %+v", got)
	}
	zeta := nodes[1]
	if zeta.ID != "org-z" || zeta.Code != "02" || zeta.Children[0].Code != "0201" {
		t.Fatalf("zeta tree = %+v", zeta)
	}
}
