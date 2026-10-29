package operations_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/identity"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/app/userauth"
)

func TestCommandServiceCreatesUpdatesAndDisablesWorkforceMembers(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	audit := &fakeOperationsAuditRecorder{}
	service := operations.NewCommandService(store)
	service.SetWorkforceSessionRevoker(&fakeWorkforceSessionRevoker{})
	service.SetAuditRecorder(audit)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
		{DepartmentID: "platform", OrganizationID: "orrery", Name: "Platform"},
	}, []operations.PositionDTO{
		{PositionID: "operator", DepartmentID: "operations", Name: "Operator"},
		{PositionID: "lead", DepartmentID: "platform", Name: "Lead"},
	})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}, RequestID: "corr-workforce"}

	created, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID:    "profile-ops",
		RealName:          "Ops Agent",
		DisplayName:       "Ops",
		AgentKind:         "ai",
		EmploymentType:    "ai_agent",
		OrganizationID:    "orrery",
		DepartmentID:      "operations",
		PositionID:        "operator",
		RoleIDs:           []string{"worker"},
		ManagerAgentID:    "agent-manager",
		ReportingLineID:   "ops-line",
		AuthorityScope:    "deploy:read",
		SupervisionPolicy: "managed",
		RuntimeKind:       "codex",
		Persona:           operations.WorkforcePersonaDTO{ResponsibilityBoundary: "Operate production workflows"},
		PersonalMemoryID:  "memory-agent-ops",
		QAEnabled:         true,
		Enabled:           true,
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember create: %v", err)
	}
	if created.AgentID != "profile-ops" || created.AgentInstanceID != "profile-ops" || created.DepartmentID != "operations" || !created.Enabled {
		t.Fatalf("created member = %+v", created)
	}
	profile := store.profiles["profile-ops"]
	if profile.OrgIdentity.ManagerAgentRef.ID != "agent-manager" || profile.RuntimeSpec.Kind != agentcore.RuntimeKindCodex {
		t.Fatalf("saved profile = %+v", profile)
	}
	if !profile.BaseAgent.InheritsBaseAgent || !profile.BaseAgent.AuditRequired || profile.BaseAgent.LLMAPIKeyRef == "" {
		t.Fatalf("saved profile missing BaseAgent contract: %+v", profile.BaseAgent)
	}
	if !profile.QAEnabled || !created.QAEnabled {
		t.Fatalf("qa_enabled should be saved and returned: profile=%+v dto=%+v", profile, created)
	}
	if created.DisplayName != "Ops" || profile.DisplayName != "Ops" {
		t.Fatalf("saved display name should keep operator-provided value: profile=%+v dto=%+v", profile, created)
	}
	if !containsChinese(profile.RealName) || !containsChinese(profile.Persona.ResponsibilityBoundary) {
		t.Fatalf("saved profile should have Chinese managed identity: %+v", profile)
	}
	if store.availability["profile-ops"].AgentInstanceID != "profile-ops" {
		t.Fatalf("stable agent identity should be persisted through availability: %+v", store.availability)
	}

	updated, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID:    "profile-ops",
		RealName:          "Ops Agent",
		DisplayName:       "Ops Lead",
		AgentKind:         "ai",
		EmploymentType:    "ai_agent",
		OrganizationID:    "orrery",
		DepartmentID:      "platform",
		PositionID:        "lead",
		SupervisionPolicy: "root",
		RuntimeKind:       "codex",
		Persona:           operations.WorkforcePersonaDTO{ResponsibilityBoundary: "Lead production workflows"},
		PersonalMemoryID:  "memory-agent-ops",
		QAEnabled:         false,
		Enabled:           true,
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember update: %v", err)
	}
	if updated.DisplayName != "Ops Lead" || updated.DepartmentID != "platform" || updated.ManagerAgentID != "" {
		t.Fatalf("updated member = %+v", updated)
	}
	if store.profiles["profile-ops"].QAEnabled || updated.QAEnabled {
		t.Fatalf("qa_enabled should update to false: profile=%+v dto=%+v", store.profiles["profile-ops"], updated)
	}

	if err := service.SetWorkforceMemberEnabled(ctx, viewer, operations.SetWorkforceMemberEnabledCommand{AgentProfileID: "profile-ops", Enabled: false}); err != nil {
		t.Fatalf("SetWorkforceMemberEnabled: %v", err)
	}
	if store.profiles["profile-ops"].Enabled {
		t.Fatalf("profile should be disabled: %+v", store.profiles["profile-ops"])
	}
	if _, ok := store.profiles["profile-ops"]; !ok || store.deleted["profile-ops"] {
		t.Fatalf("disable should not delete profile: profiles=%+v deleted=%+v", store.profiles, store.deleted)
	}
	if store.availability["profile-ops"].Status != agentcore.AvailabilityDisabled {
		t.Fatalf("disable should update availability separately: %+v", store.availability["profile-ops"])
	}
	if len(audit.events) != 3 {
		t.Fatalf("audit events = %+v", audit.events)
	}
	if audit.events[0].Action != "workforce.member.upsert" || audit.events[0].CorrelationID != "corr-workforce" || audit.events[0].Metadata["agent_id"] != "profile-ops" || audit.events[0].Metadata["agent_profile_id"] != "profile-ops" || audit.events[0].Metadata["agent_instance_id"] != "profile-ops" {
		t.Fatalf("upsert audit = %+v", audit.events[0])
	}
	if audit.events[2].Action != "workforce.member.disable" || audit.events[2].Metadata["enabled"] != "false" {
		t.Fatalf("disable audit = %+v", audit.events[2])
	}
}

func TestCommandServiceSetWorkforceMemberEnabledMapsMissingProfileToNotFound(t *testing.T) {
	service := operations.NewCommandService(&fakeWorkforceProfileStore{
		profiles:   map[agentcore.AgentProfileID]agentcore.AgentProfile{},
		missingErr: fmt.Errorf("%w: profile-missing", agentcore.ErrNotFound),
	})
	err := service.SetWorkforceMemberEnabled(context.Background(), operations.ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage},
	}, operations.SetWorkforceMemberEnabledCommand{
		AgentProfileID: "profile-missing",
		Enabled:        true,
	})
	if !errors.Is(err, operations.ErrNotFound) {
		t.Fatalf("SetWorkforceMemberEnabled error = %v, want ErrNotFound", err)
	}
}

func TestCommandServiceUpsertWorkforceMemberCleansKnowledgeWhenProfileSaveFails(t *testing.T) {
	ctx := context.Background()
	saveErr := errors.New("save profile failed")
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}, saveError: saveErr}
	service := operations.NewCommandService(store)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "operator", DepartmentID: "operations", Name: "Operator"},
	})
	knowledge := &fakeKnowledgeLifecycle{}
	service.SetOrganizationCatalogStore(orgs)
	service.SetKnowledgeLifecycle(knowledge)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	_, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID:   "profile-ops",
		DisplayName:      "Ops",
		AgentKind:        "ai",
		EmploymentType:   "ai_agent",
		OrganizationID:   "orrery",
		DepartmentID:     "operations",
		PositionID:       "operator",
		ManagerAgentID:   "agent-manager",
		RuntimeKind:      "codex",
		Persona:          operations.WorkforcePersonaDTO{ResponsibilityBoundary: "Operate production workflows"},
		PersonalMemoryID: "memory-agent-ops",
		Enabled:          true,
	})
	if !errors.Is(err, saveErr) {
		t.Fatalf("UpsertWorkforceMember err = %v, want save error", err)
	}
	if len(knowledge.created) != 1 || len(knowledge.deleted) != 1 || knowledge.deleted[0].id != knowledge.created[0].id {
		t.Fatalf("created knowledge should be cleaned after save failure: created=%+v deleted=%+v", knowledge.created, knowledge.deleted)
	}
}

func TestCommandServiceManagedBaseAgentKeepsEditableCorePromptAfterIdentityNormalization(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "operator", DepartmentID: "operations", Name: "Operator"},
	})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	_, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-template",
		RealName:       "Template Agent",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		SystemPrompt:   "你负责把用户目标编排给真实专业 Agent，不直接代替执行。",
		Persona:        operations.WorkforcePersonaDTO{ResponsibilityBoundary: "Run template checks"},
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember: %v", err)
	}

	profile := store.profiles["profile-template"]
	if profile.BaseAgent.SystemPrompt != "你负责把用户目标编排给真实专业 Agent，不直接代替执行。" {
		t.Fatalf("personnel-managed system prompt was not saved: %q", profile.BaseAgent.SystemPrompt)
	}
	for _, unexpected := range []string{
		"Template Agent",
		"全局 BaseAgent 模板",
		"Memory 占位：TODO(memory-team)",
		"Knowledge 占位：TODO(knowledge-team)",
		"<character_card>",
		"<capabilities>",
	} {
		if strings.Contains(profile.BaseAgent.SystemPrompt, unexpected) {
			t.Fatalf("system prompt should remain editable core prompt without factory-expanded template %q, got: %s", unexpected, profile.BaseAgent.SystemPrompt)
		}
	}
}

func TestCommandServiceUpsertHumanUsesDisplayNameAndManagesLoginPassword(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	service.SetWorkforceSessionRevoker(&fakeWorkforceSessionRevoker{})
	accountProjector := &fakeWorkforceAccountProjector{result: operations.WorkforceAccountProjectionResult{
		Status: "applied", Revision: 7, InitialPassword: "initial-password-123",
	}}
	service.SetWorkforceAccountProjector(accountProjector)
	roles := newFakeRoleStore()
	service.SetRoleStore(roles)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "operator", DepartmentID: "operations", Name: "Operator"},
	})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	created, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-operator",
		DisplayName:    "Operator User",
		AgentKind:      "human",
		UserAccount: operations.WorkforceUserAccountDTO{
			Username:       "operator",
			AccountEnabled: true,
			Password:       "secret-12345",
		},
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		RoleIDs:        []string{"org.admin"},
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Handles human approvals."},
		Enabled:        true,
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember create: %v", err)
	}
	if created.RealName != "Operator User" || created.DisplayName != "Operator User" {
		t.Fatalf("created member should use display_name as canonical identity: %+v", created)
	}
	if accountProjector.personID != "profile-operator" || created.AccountProjection.Status != "applied" || created.AccountProjection.Revision != 7 || created.AccountProjection.InitialPassword != "initial-password-123" {
		t.Fatalf("account projection call=%q result=%+v", accountProjector.personID, created.AccountProjection)
	}
	profile := store.profiles["profile-operator"]
	if profile.RealName != "Operator User" || profile.DisplayName != "Operator User" {
		t.Fatalf("profile should store display_name as canonical identity: %+v", profile)
	}
	if profile.OrgIdentity.EmploymentType != agentcore.EmploymentTypeHuman {
		t.Fatalf("human member should keep human employment type internally: %+v", profile.OrgIdentity)
	}
	if profile.UserAccount.Username != "operator" || !profile.UserAccount.AccountEnabled {
		t.Fatalf("saved user account = %+v", profile.UserAccount)
	}
	if profile.UserAccount.Nickname != "Operator User" {
		t.Fatalf("account nickname should mirror display name when not explicitly managed: %+v", profile.UserAccount)
	}
	if profile.UserAccount.PasswordSalt == "" || profile.UserAccount.PasswordVerifier == "" {
		t.Fatalf("saved user account should have password verifier: %+v", profile.UserAccount)
	}
	if !userauth.PasswordVerifierMatches("secret-12345", profile.UserAccount.PasswordSalt, profile.UserAccount.PasswordVerifier) {
		t.Fatalf("password verifier mismatch")
	}
	if len(profile.UserAccount.PermissionScope) != 0 {
		t.Fatalf("managed user account should not retain direct permission scopes: %+v", profile.UserAccount.PermissionScope)
	}
	assignment := roles.assignments["workforce:profile-operator:org.admin"]
	if assignment.Actor.ActorType != "human" || assignment.Actor.ActorID != "profile-operator" || assignment.RoleID != "org.admin" || assignment.OrganizationNodeID != "operations" || assignment.ScopeMode != operations.RoleScopeSelf || assignment.Source != operations.RoleSourceSystem {
		t.Fatalf("workforce role assignment = %+v", assignment)
	}
	resolvedScopes, err := operations.NewRolePermissionScopeResolver(roles).ResolvePermissionScopes(ctx, identity.Principal{
		UserID:         "operator",
		Username:       "operator",
		AgentProfileID: "profile-operator",
		ViewerKind:     identity.ViewerKindUser,
	}, []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage})
	if err != nil {
		t.Fatalf("ResolvePermissionScopes: %v", err)
	}
	if !workforceTestHasScope(resolvedScopes, operations.PermissionOrgManage) || !workforceTestHasScope(resolvedScopes, operations.PermissionRolesManage) || workforceTestHasScope(resolvedScopes, operations.PermissionOperationsManage) {
		t.Fatalf("authoritative role scopes = %+v", resolvedScopes)
	}
	originalSalt := profile.UserAccount.PasswordSalt
	originalVerifier := profile.UserAccount.PasswordVerifier
	profile.OrgIdentity.ManagerAgentRef = agentcore.AgentProfileRef{ID: "human-manager"}
	profile.OrgIdentity.ReportingLineRef = agentcore.ReportingLineRef{ID: "line-ops-manager"}
	profile.OrgIdentity.AuthorityScope = "operations_lead"
	profile.OrgIdentity.SupervisionPolicy = agentcore.SupervisionPolicySupervised
	profile.RuntimeSpec = agentcore.RuntimeSpec{
		Kind:      agentcore.RuntimeKindHTTP,
		ConfigRef: "runtime://operator",
		Config:    json.RawMessage(`{"endpoint":"https://operator.example.test"}`),
	}
	store.profiles["profile-operator"] = profile

	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-operator",
		DisplayName:    "Operator User",
		AgentKind:      "human",
		UserAccount: operations.WorkforceUserAccountDTO{
			Username:       "operator",
			Nickname:       "legacy-nickname",
			AccountEnabled: true,
		},
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		RoleIDs:        []string{"org.admin"},
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Handles human approvals."},
		Enabled:        true,
		UpdateMask: []string{
			"display_name",
			"agent_kind",
			"user_account.username",
			"user_account.nickname",
			"user_account.account_enabled",
			"organization_id",
			"department_id",
			"position_id",
			"role_ids",
			"persona.backstory",
			"enabled",
		},
	}); err != nil {
		t.Fatalf("UpsertWorkforceMember blank password update: %v", err)
	}
	profile = store.profiles["profile-operator"]
	if profile.DisplayName != "Operator User" {
		t.Fatalf("legacy nickname should not override display name: %+v", profile)
	}
	if profile.UserAccount.PasswordSalt != originalSalt || profile.UserAccount.PasswordVerifier != originalVerifier {
		t.Fatalf("blank password update should keep existing verifier: before=%s/%s after=%s/%s", originalSalt, originalVerifier, profile.UserAccount.PasswordSalt, profile.UserAccount.PasswordVerifier)
	}
	if len(profile.UserAccount.PermissionScope) != 0 {
		t.Fatalf("blank account update should keep role-managed account scopes empty: %+v", profile.UserAccount.PermissionScope)
	}
	if profile.OrgIdentity.ManagerAgentRef.ID != "human-manager" || profile.OrgIdentity.ReportingLineRef.ID != "line-ops-manager" || profile.OrgIdentity.AuthorityScope != "operations_lead" || profile.OrgIdentity.SupervisionPolicy != agentcore.SupervisionPolicySupervised {
		t.Fatalf("partial workforce update should preserve non-editor org fields: %+v", profile.OrgIdentity)
	}
	if profile.RuntimeSpec.Kind != agentcore.RuntimeKindHTTP || profile.RuntimeSpec.ConfigRef != "runtime://operator" || string(profile.RuntimeSpec.Config) != `{"endpoint":"https://operator.example.test"}` {
		t.Fatalf("partial workforce update should preserve runtime fields omitted by the editor: %+v", profile.RuntimeSpec)
	}
	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-operator",
		UserAccount: operations.WorkforceUserAccountDTO{
			PermissionScope: []string{},
		},
		UpdateMask: []string{"user_account.permission_scope"},
	}); !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("UpsertWorkforceMember permission scope error = %v, want ErrInvalidRequest", err)
	}
	profile = store.profiles["profile-operator"]
	if len(profile.UserAccount.PermissionScope) != 0 {
		t.Fatalf("rejected permission_scope update changed account scopes: %+v", profile.UserAccount.PermissionScope)
	}

	if _, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-operator",
		DisplayName:    "Operator User",
		AgentKind:      "human",
		UserAccount: operations.WorkforceUserAccountDTO{
			Username:       "operator",
			AccountEnabled: true,
			Password:       "  new-secret-12  ",
		},
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		RoleIDs:        []string{"org.admin"},
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Handles human approvals."},
		Enabled:        true,
	}); err != nil {
		t.Fatalf("UpsertWorkforceMember password update: %v", err)
	}
	profile = store.profiles["profile-operator"]
	if profile.UserAccount.PasswordVerifier == originalVerifier {
		t.Fatalf("password update should change verifier")
	}
	if !userauth.PasswordVerifierMatches("  new-secret-12  ", profile.UserAccount.PasswordSalt, profile.UserAccount.PasswordVerifier) || userauth.PasswordVerifierMatches("new-secret-12", profile.UserAccount.PasswordSalt, profile.UserAccount.PasswordVerifier) {
		t.Fatalf("updated password verifier did not preserve surrounding spaces")
	}
}

func TestCommandServiceRejectsDuplicateHumanUsername(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"profile-existing": {
			ID:        "profile-existing",
			AgentKind: agentcore.AgentKindHuman,
			Enabled:   true,
			UserAccount: agentcore.UserAccountSpec{
				Username:       "operator",
				AccountEnabled: true,
			},
		},
	}}
	service := operations.NewCommandService(store)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{{
		DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations",
	}}, []operations.PositionDTO{{
		PositionID: "operator", DepartmentID: "operations", Name: "Operator",
	}})
	service.SetOrganizationCatalogStore(orgs)

	_, err := service.UpsertWorkforceMember(ctx, operations.ViewerRef{
		ViewerID:        "admin",
		PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage},
	}, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-duplicate",
		DisplayName:    "Duplicate Operator",
		AgentKind:      "human",
		UserAccount: operations.WorkforceUserAccountDTO{
			Username:       " Operator ",
			AccountEnabled: true,
			Password:       "correct-horse-1",
		},
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Handles approvals."},
		Enabled:        true,
	})
	if !errors.Is(err, operations.ErrInvalidRequest) {
		t.Fatalf("UpsertWorkforceMember error = %v, want ErrInvalidRequest", err)
	}
	if _, ok := store.profiles["profile-duplicate"]; ok {
		t.Fatalf("duplicate username profile was saved")
	}
}

func TestCommandServiceUpsertUsesStableProfileIdentityForWorkforceMember(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "operator", DepartmentID: "operations", Name: "Operator"},
	})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	created, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-ops",
		RealName:       "Ops Agent",
		DisplayName:    "Ops",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		RuntimeKind:    "codex",
		Enabled:        true,
		Persona:        operations.WorkforcePersonaDTO{ResponsibilityBoundary: "Operate production workflows"},
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember: %v", err)
	}
	if created.AgentInstanceID != created.AgentProfileID {
		t.Fatalf("workforce member instance id = %q, want stable profile id %q", created.AgentInstanceID, created.AgentProfileID)
	}
	if availability := store.availability["profile-ops"]; availability.AgentInstanceID != agentcore.AgentInstanceID("profile-ops") {
		t.Fatalf("availability should use stable profile identity: %+v", availability)
	}
	if owner := store.profiles["profile-ops"].PersonalMemoryProfileRef.OwnerAgentID; owner != "profile-ops" {
		t.Fatalf("personal memory owner should follow stable profile identity, got %q", owner)
	}
}

func TestCommandServiceUpsertKeepsStableProfileIdentityWhenOmitted(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	service := operations.NewCommandService(store)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "operator", DepartmentID: "operations", Name: "Operator"},
	})
	service.SetOrganizationCatalogStore(orgs)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	created, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-ops",
		RealName:       "Ops Agent",
		DisplayName:    "Ops",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		RuntimeKind:    "codex",
		Enabled:        true,
		Persona:        operations.WorkforcePersonaDTO{ResponsibilityBoundary: "Operate production workflows"},
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember create: %v", err)
	}
	if created.AgentInstanceID != "profile-ops" {
		t.Fatalf("created member = %+v", created)
	}

	updated, err := service.UpsertWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		AgentProfileID: "profile-ops",
		RealName:       "Ops Agent",
		DisplayName:    "Ops Updated",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		RuntimeKind:    "codex",
		Enabled:        true,
		Persona:        operations.WorkforcePersonaDTO{ResponsibilityBoundary: "Operate updated production workflows"},
	})
	if err != nil {
		t.Fatalf("UpsertWorkforceMember update: %v", err)
	}
	if updated.AgentInstanceID != "profile-ops" {
		t.Fatalf("updated member should keep stable profile identity: %+v", updated)
	}
	if availability := store.availability["profile-ops"]; availability.AgentInstanceID != "profile-ops" {
		t.Fatalf("availability should keep stable profile identity: %+v", availability)
	}
	if profile := store.profiles["profile-ops"]; profile.PersonalMemoryProfileRef.OwnerAgentID != "profile-ops" {
		t.Fatalf("profile memory owner should keep stable profile identity: %+v", profile.PersonalMemoryProfileRef)
	}
}

func TestServiceReleaseWorkforceMemberMovesProfileToTalentMarket(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{}}
	audit := &fakeOperationsAuditRecorder{}
	command := operations.NewCommandService(store)
	command.SetAuditRecorder(audit)
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "orrery", []operations.DepartmentDTO{
		{DepartmentID: "operations", OrganizationID: "orrery", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "operator", DepartmentID: "operations", Name: "Operator"},
	})
	command.SetOrganizationCatalogStore(orgs)
	service := operations.NewService(operations.NewQueryService(&fakeEmptyAgentDirectory{}, &fakeEmptyToolRegistry{}), command)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}, RequestID: "corr-delete"}
	created, err := service.CreateWorkforceMember(ctx, viewer, operations.UpsertWorkforceMemberCommand{
		RealName:       "Ops Agent",
		AgentKind:      "ai",
		EmploymentType: "ai_agent",
		OrganizationID: "orrery",
		DepartmentID:   "operations",
		PositionID:     "operator",
		RuntimeKind:    "codex",
		Enabled:        true,
		Persona:        operations.WorkforcePersonaDTO{Backstory: "Handles platform operations."},
	})
	if err != nil {
		t.Fatalf("CreateWorkforceMember: %v", err)
	}

	if err := service.ReleaseWorkforceMember(ctx, viewer, operations.ReleaseWorkforceMemberCommand{AgentProfileID: created.AgentProfileID, Reason: "left squad"}); err != nil {
		t.Fatalf("ReleaseWorkforceMember: %v", err)
	}

	profileID := agentcore.AgentProfileID(created.AgentProfileID)
	profile, ok := store.profiles[profileID]
	if !ok {
		t.Fatalf("profile should be preserved after release")
	}
	if store.deleted[profileID] {
		t.Fatalf("release should not tombstone profile: %+v", store.deleted)
	}
	if !profile.Placement.IsMarket(profile.OrgIdentity) ||
		profile.Placement.ReleasedFromOrganizationID != "orrery" ||
		profile.Placement.ReleasedFromDepartmentID != "operations" ||
		profile.Placement.ReleasedFromPositionID != "operator" ||
		profile.Placement.ReleasedReason != "left squad" {
		t.Fatalf("profile placement after release = %+v", profile.Placement)
	}
	if profile.OrgIdentity.OrganizationRef.ID != "" ||
		profile.OrgIdentity.DepartmentRef.ID != "" ||
		profile.OrgIdentity.PositionRef.ID != "" ||
		len(profile.OrgIdentity.RoleRefs) != 0 ||
		profile.OrgIdentity.ManagerAgentRef.ID != "" ||
		profile.OrgIdentity.ReportingLineRef.ID != "" ||
		profile.OrgIdentity.AuthorityScope != "" {
		t.Fatalf("internal org identity should be cleared after release: %+v", profile.OrgIdentity)
	}
	availability := store.availability[profileID]
	if availability.Status != agentcore.AvailabilityDisabled ||
		availability.AgentInstanceID.String() != created.AgentInstanceID ||
		availability.Reason != "workforce member released to talent market" {
		t.Fatalf("availability after delete = %+v", availability)
	}
	if len(audit.events) < 2 || audit.events[len(audit.events)-1].Action != "workforce.member.release_to_market" || audit.events[len(audit.events)-1].CorrelationID != "corr-delete" || audit.events[len(audit.events)-1].Metadata["agent_id"] != created.AgentProfileID || audit.events[len(audit.events)-1].Metadata["agent_instance_id"] != created.AgentInstanceID {
		t.Fatalf("release audit event missing: %+v", audit.events)
	}
}

func TestServiceCanReleaseSystemWorkflowMemberWithManagePermission(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"workflow-agent": {
			ID:        "workflow-agent",
			RealName:  "顾明澈",
			AgentKind: agentcore.AgentKindAI,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "org-agent-coordination"},
				RoleRefs:        []agentcore.RoleRef{{ID: "workflow_lead"}},
				EmploymentType:  agentcore.EmploymentTypeSystem,
			},
			Enabled: true,
		},
	}, availability: map[agentcore.AgentProfileID]agentcore.AgentAvailability{
		"workflow-agent": {AgentProfileID: "workflow-agent", AgentInstanceID: "workflow-agent:runtime", Status: agentcore.AvailabilityAvailable},
	}}
	audit := &fakeOperationsAuditRecorder{}
	command := operations.NewCommandService(store)
	command.SetAuditRecorder(audit)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	if err := command.ReleaseWorkforceMember(ctx, viewer, operations.ReleaseWorkforceMemberCommand{AgentProfileID: "workflow-agent", Reason: "available for other work"}); err != nil {
		t.Fatalf("ReleaseWorkforceMember: %v", err)
	}
	profile := store.profiles["workflow-agent"]
	if profile.Placement.Status != agentcore.TalentPlacementMarket || profile.OrgIdentity.EmploymentType != agentcore.EmploymentTypeSystem {
		t.Fatalf("released system workflow profile = %+v", profile)
	}
	if profile.OrgIdentity.OrganizationRef.ID != "" || len(profile.OrgIdentity.RoleRefs) != 0 {
		t.Fatalf("released profile should not keep organization bindings: %+v", profile.OrgIdentity)
	}
	if availability := store.availability["workflow-agent"]; availability.Status != agentcore.AvailabilityDisabled || availability.AgentInstanceID != "workflow-agent" || availability.Reason != "workforce member released to talent market" {
		t.Fatalf("availability after release = %+v", availability)
	}
	if len(audit.events) == 0 || audit.events[len(audit.events)-1].Action != "workforce.member.release_to_market" {
		t.Fatalf("release audit missing: %+v", audit.events)
	}
}

func TestServiceHireTalentFromMarketMovesProfileIntoOrganization(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"profile-market": {
			ID:          "profile-market",
			RealName:    "市场智员",
			DisplayName: "市场智员",
			AgentKind:   agentcore.AgentKindAI,
			Placement:   agentcore.TalentPlacement{Status: agentcore.TalentPlacementMarket, ReleasedReason: "released"},
			OrgIdentity: agentcore.AgentOrgIdentity{EmploymentType: agentcore.EmploymentTypeAIAgent, SupervisionPolicy: agentcore.SupervisionPolicyRoot},
			Persona:     agentcore.AgentPersona{ResponsibilityBoundary: "负责被招募后执行任务并提交证据。"},
			BaseAgent:   agentcore.BaseAgentSpec{InheritsBaseAgent: true, AuditRequired: true, SystemPrompt: "你是市场候选 Agent。", LLMAPIKeyRef: "secret://system/llm-api-key", SkillScope: "profile_bound", ToolScope: "profile_bound", MCPScope: "profile_bound"},
			SkillRefs:   []agentcore.SkillRef{},
			ToolRefs:    []agentcore.ToolRef{},
			RuntimeSpec: agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindCodex},
			PersonalMemoryProfileRef: agentcore.MemoryProfileRef{
				MemoryProfileID: "memory-market",
				OwnerAgentID:    "profile-market",
				MemoryKind:      agentcore.MemoryKindPersonal,
			},
			Enabled: true,
		},
	}, availability: map[agentcore.AgentProfileID]agentcore.AgentAvailability{
		"profile-market": {AgentProfileID: "profile-market", AgentInstanceID: "profile-market:runtime", Status: agentcore.AvailabilityDisabled},
	}}
	orgs := newFakeOrganizationCatalogStore()
	seedOrganizationCatalog(t, ctx, orgs, "org-1", []operations.DepartmentDTO{
		{DepartmentID: "dept-ops", OrganizationID: "org-1", Name: "Operations"},
	}, []operations.PositionDTO{
		{PositionID: "pos-operator", DepartmentID: "dept-ops", Name: "Operator"},
	})
	audit := &fakeOperationsAuditRecorder{}
	command := operations.NewCommandService(store)
	command.SetOrganizationCatalogStore(orgs)
	command.SetAuditRecorder(audit)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}, RequestID: "corr-hire"}

	member, err := command.HireTalentFromMarket(ctx, viewer, operations.HireTalentFromMarketCommand{
		AgentProfileID:  "profile-market",
		OrganizationID:  "org-1",
		DepartmentID:    "dept-ops",
		PositionID:      "pos-operator",
		RoleIDs:         []string{"operator"},
		ManagerAgentID:  "agent-manager",
		ReportingLineID: "ops-line",
	})
	if err != nil {
		t.Fatalf("HireTalentFromMarket: %v", err)
	}
	if member.PlacementStatus != "internal" || member.OrganizationID != "org-1" || member.DepartmentID != "dept-ops" || member.PositionID != "pos-operator" {
		t.Fatalf("hired member = %+v", member)
	}
	profile := store.profiles["profile-market"]
	if !profile.Placement.IsInternal(profile.OrgIdentity) || profile.OrgIdentity.ManagerAgentRef.ID != "agent-manager" || len(profile.OrgIdentity.RoleRefs) != 1 || profile.OrgIdentity.RoleRefs[0].ID != "operator" {
		t.Fatalf("profile after hire = placement:%+v org:%+v", profile.Placement, profile.OrgIdentity)
	}
	if store.availability["profile-market"].Status != agentcore.AvailabilityAvailable || store.availability["profile-market"].AgentInstanceID != "profile-market" {
		t.Fatalf("availability after hire = %+v", store.availability["profile-market"])
	}
	if len(audit.events) == 0 || audit.events[len(audit.events)-1].Action != "talent_market.member.hire" || audit.events[len(audit.events)-1].CorrelationID != "corr-hire" {
		t.Fatalf("hire audit missing: %+v", audit.events)
	}
}

func TestServiceCanDisableSystemWorkflowMemberWithManagePermission(t *testing.T) {
	ctx := context.Background()
	store := &fakeWorkforceProfileStore{profiles: map[agentcore.AgentProfileID]agentcore.AgentProfile{
		"workflow-agent": {
			ID:        "workflow-agent",
			RealName:  "顾明澈",
			AgentKind: agentcore.AgentKindAI,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "org-agent-coordination"},
				RoleRefs:        []agentcore.RoleRef{{ID: "workflow_lead"}},
				EmploymentType:  agentcore.EmploymentTypeSystem,
			},
			Enabled: true,
		},
		"staffing-agent": {
			ID:        "staffing-agent",
			RealName:  "秦若兰",
			AgentKind: agentcore.AgentKindAI,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "org-agent-coordination"},
				RoleRefs:        []agentcore.RoleRef{{ID: "talent_reviewer"}},
				EmploymentType:  agentcore.EmploymentTypeSystem,
			},
			Enabled: true,
		},
		"coordinator-agent": {
			ID:        "coordinator-agent",
			RealName:  "Coordinator",
			AgentKind: agentcore.AgentKindAI,
			OrgIdentity: agentcore.AgentOrgIdentity{
				OrganizationRef: agentcore.OrganizationRef{ID: "org-agent-coordination"},
				RoleRefs:        []agentcore.RoleRef{{ID: "coordinator"}},
				EmploymentType:  agentcore.EmploymentTypeSystem,
			},
			Enabled: true,
		},
	}, availability: map[agentcore.AgentProfileID]agentcore.AgentAvailability{
		"workflow-agent":    {AgentProfileID: "workflow-agent", AgentInstanceID: "workflow-agent:runtime", Status: agentcore.AvailabilityAvailable},
		"staffing-agent":    {AgentProfileID: "staffing-agent", AgentInstanceID: "staffing-agent:runtime", Status: agentcore.AvailabilityAvailable},
		"coordinator-agent": {AgentProfileID: "coordinator-agent", AgentInstanceID: "coordinator-agent:runtime", Status: agentcore.AvailabilityAvailable},
	}}
	command := operations.NewCommandService(store)
	viewer := operations.ViewerRef{ViewerID: "admin", PermissionScope: []string{operations.PermissionWorkforceManage, operations.PermissionRolesManage, operations.PermissionPermissionsManage}}

	if err := command.SetWorkforceMemberEnabled(ctx, viewer, operations.SetWorkforceMemberEnabledCommand{AgentProfileID: "workflow-agent", Enabled: false}); err != nil {
		t.Fatalf("SetWorkforceMemberEnabled: %v", err)
	}
	if store.profiles["workflow-agent"].Enabled {
		t.Fatalf("system workflow member should be disabled: %+v", store.profiles["workflow-agent"])
	}
	if availability := store.availability["workflow-agent"]; availability.Status != agentcore.AvailabilityDisabled || availability.AgentInstanceID != "workflow-agent" || availability.Reason != "workforce member disabled" {
		t.Fatalf("availability after disable = %+v", availability)
	}
}

func workforceTestHasScope(scopes []string, want string) bool {
	for _, scope := range scopes {
		if scope == want {
			return true
		}
	}
	return false
}
