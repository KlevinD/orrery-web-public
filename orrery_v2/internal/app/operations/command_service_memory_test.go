package operations_test

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
)

func TestCommandServiceCreatesPersonalMemoryProfileForWorkforceMember(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	memory := &fakeMemoryProfileStore{}
	service := operations.NewCommandService(store)
	service.SetMemoryProfileStore(memory)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "operator", DepartmentID: "operations", Name: "Operator"},
	})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-ops",
		RealName:       "Ops Agent",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		RuntimeKind:    "codex",
		Enabled:        true,
		Persona:        operations.WorkforcePersonaDTO{ResponsibilityBoundary: "Operate production workflows"},
	}); err != nil {
		t.Fatalf("UpsertWorkforceMember: %v", err)
	}

	memoryProfile, ok := memory.profiles["memory-profile-ops"]
	if !ok {
		t.Fatalf("personal memory profile was not created: %+v", memory.profiles)
	}
	if memoryProfile.OwnerRef.AgentID != "profile-ops" ||
		memoryProfile.DefaultScope.Kind != "agent" ||
		memoryProfile.DefaultScope.ID != "profile-ops" ||
		!memoryProfile.Enabled {
		t.Fatalf("personal memory profile should be owner-readable: %+v", memoryProfile)
	}
	profile := store.profiles["profile-ops"]
	if len(profile.CollectiveMemoryScopeRefs) != 2 {
		t.Fatalf("collective memory refs = %+v", profile.CollectiveMemoryScopeRefs)
	}
	if _, ok := memory.profiles["memory-organization-orrery"]; !ok {
		t.Fatalf("organization memory profile was not created: %+v", memory.profiles)
	}
	if _, ok := memory.profiles["memory-department-operations"]; !ok {
		t.Fatalf("department memory profile was not created: %+v", memory.profiles)
	}
}

func TestCommandServiceSyncsPersonalMemoryWhenMemberEnabledChanges(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	memory := &fakeMemoryProfileStore{}
	service := operations.NewCommandService(store)
	service.SetMemoryProfileStore(memory)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "operator", DepartmentID: "operations", Name: "Operator"},
	})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-memory-sync",
		DisplayName:    "Memory Sync Agent",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		Persona:        operations.WorkforcePersonaDTO{ResponsibilityBoundary: "Operate scheduled workflows"},
		Enabled:        false,
	}); err != nil {
		t.Fatalf("create disabled workforce member: %v", err)
	}
	if memory.profiles["memory-profile-memory-sync"].Enabled {
		t.Fatal("personal memory should start disabled with the member")
	}

	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-memory-sync",
		Enabled:        true,
		UpdateMask:     []string{"enabled"},
	}); err != nil {
		t.Fatalf("enable workforce member: %v", err)
	}
	if !memory.profiles["memory-profile-memory-sync"].Enabled {
		t.Fatal("explicitly enabling the member did not enable personal memory")
	}

	profile := memory.profiles["memory-profile-memory-sync"]
	profile.Enabled = false
	memory.profiles[profile.ID] = profile
	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-memory-sync",
		Persona: operations.WorkforcePersonaDTO{
			ResponsibilityBoundary: "Operate scheduled workflows and retain evidence",
		},
		UpdateMask: []string{"persona.responsibility_boundary"},
	}); err != nil {
		t.Fatalf("update responsibility boundary: %v", err)
	}
	if memory.profiles["memory-profile-memory-sync"].Enabled {
		t.Fatal("unrelated member edit overrode a manually disabled personal memory profile")
	}
}

func TestCommandServiceCreatesPersonalMemoryProfileForTalentMarketImport(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	memory := &fakeMemoryProfileStore{}
	service := operations.NewCommandService(store)
	service.SetMemoryProfileStore(memory)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage}}

	if _, err := service.CommitTalentMarketImport(ctx, viewer, operations.TalentMarketImportCommitRequest{
		Candidates: []operations.TalentMarketImportCandidateDTO{{
			AgentProfileID: "profile-researcher",
			RealName:       "研思",
			DisplayName:    "研思",
			Persona: operations.WorkforcePersonaDTO{
				ResponsibilityBoundary: "负责文献研究、论证和引用校验，并提交证据。",
			},
			PersonalMemoryRef: operations.WorkforceMemoryProfileRefDTO{
				MemoryProfileID: "memory-researcher",
				MemoryKind:      "personal",
			},
			Enabled: true,
		}},
	}); err != nil {
		t.Fatalf("CommitTalentMarketImport: %v", err)
	}

	memoryProfile, ok := memory.profiles["memory-researcher"]
	if !ok {
		t.Fatalf("personal memory profile was not created: %+v", memory.profiles)
	}
	if memoryProfile.OwnerRef.AgentID != "profile-researcher" ||
		memoryProfile.DefaultScope.Kind != "agent" ||
		memoryProfile.DefaultScope.ID != "profile-researcher" ||
		!memoryProfile.Enabled {
		t.Fatalf("personal memory profile should be owner-readable: %+v", memoryProfile)
	}
}
