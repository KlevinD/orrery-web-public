package core

import (
	"encoding/json"
	"testing"
)

func TestValidateAgentProfileAcceptsCompleteProfile(t *testing.T) {
	profile := validAgentProfile()
	profile.Email = "lin.qiao@example.test"

	if errs := ValidateAgentProfile(profile); len(errs) != 0 {
		t.Fatalf("ValidateAgentProfile returned errors: %#v", errs)
	}
}

func TestValidateAgentProfileRejectsReservedNonWorkforceIDs(t *testing.T) {
	for _, id := range []string{
		SystemSuperadminPrincipalID,
		LegacySuperadminAgentProfileID,
		LegacyLocalAdminAgentProfileID,
		LegacyLocalUserAgentProfileID,
	} {
		profile := validAgentProfile()
		profile.ID = AgentProfileID(id)
		assertValidationError(t, ValidateAgentProfile(profile), CodeInvalidRef, "id")
	}
}

func TestHumanProfileCarriesPeopleSourceFactsWithoutLoginRelationship(t *testing.T) {
	profile := validAgentProfile()
	profile.AgentKind = AgentKindHuman
	profile.PersonSource = PersonSourceFacts{
		EmployeeNumber: "E-001",
		Source:         "people.feishu",
		SourceRecordID: "ou_123",
		SourceTenantID: "tenant-1",
	}

	if errs := ValidateAgentProfile(profile); len(errs) != 0 {
		t.Fatalf("human profile with People source facts should validate: %#v", errs)
	}
	encoded, err := json.Marshal(profile.PersonSource)
	if err != nil {
		t.Fatalf("marshal source facts: %v", err)
	}
	if string(encoded) != `{"EmployeeNumber":"E-001","Source":"people.feishu","SourceRecordID":"ou_123","SourceTenantID":"tenant-1"}` {
		t.Fatalf("source facts = %s", encoded)
	}
}

func TestValidateAgentProfileRejectsIncompleteOrNonHumanPeopleSourceFacts(t *testing.T) {
	profile := validAgentProfile()
	profile.AgentKind = AgentKindHuman
	profile.PersonSource = PersonSourceFacts{Source: "people.feishu", SourceRecordID: "u_123"}
	assertValidationError(t, ValidateAgentProfile(profile), CodeMissingRequiredField, "person_source.source_tenant_id")

	profile = validAgentProfile()
	profile.PersonSource = PersonSourceFacts{EmployeeNumber: "E-001"}
	assertValidationError(t, ValidateAgentProfile(profile), CodeInvalidAgentKind, "person_source")
}

func TestValidateAgentProfileRejectsDisplayNameInMailboxField(t *testing.T) {
	profile := validAgentProfile()
	profile.Email = "Lin Qiao <lin.qiao@example.test>"

	errs := ValidateAgentProfile(profile)
	assertValidationError(t, errs, CodeInvalidRef, "email")
}

func TestValidateAgentProfileRequiresIdentityPersonaAndMemoryButAllowsManagedRuntimeToBeUnset(t *testing.T) {
	profile := AgentProfile{}

	errs := ValidateAgentProfile(profile)

	assertValidationError(t, errs, CodeMissingRequiredField, "real_name")
	assertValidationError(t, errs, CodeInvalidAgentKind, "agent_kind")
	assertValidationError(t, errs, CodeMissingRequiredField, "org_identity.organization_ref")
	assertValidationError(t, errs, CodeMissingRequiredField, "org_identity.department_ref")
	assertValidationError(t, errs, CodeMissingRequiredField, "org_identity.position_ref")
	assertValidationError(t, errs, CodeMissingRequiredField, "org_identity.manager_agent_ref")
	assertValidationError(t, errs, CodeMissingRequiredField, "persona")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.inherits_base_agent")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.audit_required")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.system_prompt")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.llm_api_key_ref")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.skill_scope")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.tool_scope")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.mcp_scope")
	assertValidationError(t, errs, CodeMissingRequiredField, "personal_memory_profile_ref")
	assertNoValidationError(t, errs, "runtime_spec.kind")
}

func TestValidateAgentProfileAcceptsSystemManagedAgentWithoutExternalRuntime(t *testing.T) {
	profile := validAgentProfile()
	profile.RuntimeSpec = RuntimeSpec{}

	if errs := ValidateAgentProfile(profile); len(errs) != 0 {
		t.Fatalf("managed BaseAgent without external runtime should be valid: %#v", errs)
	}
}

func TestValidateAgentProfileAcceptsSessionOnlyAgentWithoutLongTermMemory(t *testing.T) {
	profile := validAgentProfile()
	profile.PersonalMemoryProfileRef = MemoryProfileRef{}
	profile.CollectiveMemoryScopeRefs = []MemoryProfileRef{}
	profile.BaseAgent.Extensions = map[string]json.RawMessage{
		BaseAgentMemoryModeExtensionKey: json.RawMessage(`{"mode":"session_only"}`),
	}

	if errs := ValidateAgentProfile(profile); len(errs) != 0 {
		t.Fatalf("session-only profile should validate without long-term memory: %#v", errs)
	}
}

func TestValidateAgentProfileRejectsMissingMemoryWithoutSessionOnlyMode(t *testing.T) {
	profile := validAgentProfile()
	profile.PersonalMemoryProfileRef = MemoryProfileRef{}
	profile.BaseAgent.Extensions = map[string]json.RawMessage{
		BaseAgentMemoryModeExtensionKey: json.RawMessage(`{"mode":"persistent"}`),
	}

	errs := ValidateAgentProfile(profile)
	assertValidationError(t, errs, CodeMissingRequiredField, "personal_memory_profile_ref")
}

func TestValidateAgentProfileRequiresBaseAgentAuditContractForManagedAgents(t *testing.T) {
	profile := validAgentProfile()
	profile.BaseAgent = BaseAgentSpec{}

	errs := ValidateAgentProfile(profile)

	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.inherits_base_agent")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.audit_required")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.system_prompt")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.llm_api_key_ref")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.skill_scope")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.tool_scope")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.mcp_scope")
}

func TestValidateAgentProfileRequiresBaseAgentAuditContractForHumanAgents(t *testing.T) {
	profile := validAgentProfile()
	profile.AgentKind = AgentKindHuman
	profile.BaseAgent = BaseAgentSpec{}

	errs := ValidateAgentProfile(profile)

	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.inherits_base_agent")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.audit_required")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.system_prompt")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.llm_api_key_ref")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.skill_scope")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.tool_scope")
	assertValidationError(t, errs, CodeMissingRequiredField, "base_agent.mcp_scope")
}

func TestValidateAgentProfileAllowsHumanNamesFromViewerInputWithBaseAgentContract(t *testing.T) {
	profile := validAgentProfile()
	profile.AgentKind = AgentKindHuman
	profile.RealName = "Operator User"
	profile.DisplayName = "Operator User"
	profile.Persona = AgentPersona{
		Backstory:              "Real human participant represented by the current viewer.",
		ResponsibilityBoundary: "Provide original intent, clarification, review, and collaboration context.",
	}

	if errs := ValidateAgentProfile(profile); len(errs) != 0 {
		t.Fatalf("human profile with BaseAgent contract should keep viewer identity: %#v", errs)
	}
}

func TestBaseAgentSpecLeavesExtensionSlotForFutureManagedAgentAttributes(t *testing.T) {
	profile := validAgentProfile()
	profile.BaseAgent.Extensions = map[string]json.RawMessage{
		"handoff_policy": json.RawMessage(`{"mode":"supervised"}`),
	}

	if errs := ValidateAgentProfile(profile); len(errs) != 0 {
		t.Fatalf("ValidateAgentProfile returned errors: %#v", errs)
	}
	if string(profile.BaseAgent.Extensions["handoff_policy"]) != `{"mode":"supervised"}` {
		t.Fatalf("extension should be preserved: %+v", profile.BaseAgent.Extensions)
	}
}

func TestValidateAgentProfileRequiresChineseHumanizedIdentity(t *testing.T) {
	profile := validAgentProfile()
	profile.RealName = "Lin Qiao"
	profile.Persona.ResponsibilityBoundary = "Owns scoped task implementation and evidence."

	errs := ValidateAgentProfile(profile)

	assertValidationError(t, errs, CodeMissingRequiredField, "real_name")
	assertValidationError(t, errs, CodeMissingRequiredField, "persona.responsibility_boundary")
}

func TestAgentKindDescribesExecutionSubjectNotBusinessRole(t *testing.T) {
	profile := validAgentProfile()
	profile.AgentKind = AgentKindAI
	profile.RoleName = "Implementation Worker"

	if errs := ValidateAgentProfile(profile); len(errs) != 0 {
		t.Fatalf("ai execution subject should be valid independent of business role: %#v", errs)
	}
	if AgentKind("qa").IsValid() || AgentKind("manager").IsValid() {
		t.Fatal("business roles should not be hard-coded as agent kinds")
	}
}

func TestValidateAgentProfileAcceptsExplicitRootSupervisionPolicy(t *testing.T) {
	profile := validAgentProfile()
	profile.OrgIdentity.ManagerAgentRef = AgentProfileRef{}
	profile.OrgIdentity.SupervisionPolicy = SupervisionPolicyRoot

	if errs := ValidateAgentProfile(profile); len(errs) != 0 {
		t.Fatalf("ValidateAgentProfile returned errors: %#v", errs)
	}
}

func TestAgentOrgIdentityManagerRefUsesStableAgentProfile(t *testing.T) {
	profile := validAgentProfile()
	profile.OrgIdentity.ManagerAgentRef = AgentProfileRef{ID: AgentProfileID("profile-manager-001")}

	if errs := ValidateAgentProfile(profile); len(errs) != 0 {
		t.Fatalf("stable manager profile ref should validate: %#v", errs)
	}
	if profile.OrgIdentity.ManagerAgentRef.ID != AgentProfileID("profile-manager-001") {
		t.Fatalf("manager ref = %#v, want stable profile id", profile.OrgIdentity.ManagerAgentRef)
	}
}

func TestValidateAgentProfileAcceptsMarketProfileWithoutOrganizationRefs(t *testing.T) {
	profile := validAgentProfile()
	profile.Placement = TalentPlacement{Status: TalentPlacementMarket}
	profile.OrgIdentity = AgentOrgIdentity{EmploymentType: EmploymentTypeAIAgent, SupervisionPolicy: SupervisionPolicyRoot}

	if errs := ValidateAgentProfile(profile); len(errs) != 0 {
		t.Fatalf("market profile should not require org identity: %#v", errs)
	}
}

func TestValidateAgentProfileAcceptsPersonalAssistantWithoutWorkforceAssignment(t *testing.T) {
	profile := validAgentProfile()
	profile.PersonalAssistantOwnerID = "owner-1"
	profile.PersonalAssistantTenantID = "org-001"
	profile.OrgIdentity.DepartmentRef = DepartmentRef{}
	profile.OrgIdentity.PositionRef = PositionRef{}
	profile.OrgIdentity.RoleRefs = []RoleRef{}
	profile.OrgIdentity.ManagerAgentRef = AgentProfileRef{}
	profile.OrgIdentity.SupervisionPolicy = SupervisionPolicySupervised

	if errs := ValidateAgentProfile(profile); len(errs) != 0 {
		t.Fatalf("personal assistant should not require workforce assignment: %#v", errs)
	}

	profile.OrgIdentity.OrganizationRef = OrganizationRef{}
	errs := ValidateAgentProfile(profile)
	assertValidationError(t, errs, CodeMissingRequiredField, "org_identity.organization_ref")
}

func TestValidateAgentProfileRequiresExplicitToolAndSkillBoundaries(t *testing.T) {
	profile := validAgentProfile()
	profile.SkillRefs = nil
	profile.ToolRefs = nil

	errs := ValidateAgentProfile(profile)

	assertValidationError(t, errs, CodeMissingRequiredField, "skill_refs")
	assertValidationError(t, errs, CodeMissingRequiredField, "tool_refs")
}

func TestValidateAgentProfileAcceptsExplicitEmptyToolAndSkillBoundaries(t *testing.T) {
	profile := validAgentProfile()
	profile.SkillRefs = []SkillRef{}
	profile.ToolRefs = []ToolRef{}

	if errs := ValidateAgentProfile(profile); len(errs) != 0 {
		t.Fatalf("ValidateAgentProfile returned errors: %#v", errs)
	}
}

func TestValidateAgentProfileRejectsInvalidRefsAndKinds(t *testing.T) {
	profile := validAgentProfile()
	profile.ToolRefs = []ToolRef{{Alias: "repo"}}
	profile.SkillRefs = []SkillRef{{Alias: "go"}}
	profile.PersonalMemoryProfileRef.MemoryKind = MemoryKind("unknown")
	profile.CollectiveMemoryScopeRefs = []MemoryProfileRef{{MemoryProfileID: MemoryProfileID("collective"), MemoryKind: MemoryKindPersonal}}
	profile.RuntimeSpec.Kind = RuntimeKind("shell")

	errs := ValidateAgentProfile(profile)

	assertValidationError(t, errs, CodeInvalidRef, "tool_refs[0].tool_id")
	assertValidationError(t, errs, CodeInvalidRef, "skill_refs[0].skill_id")
	assertValidationError(t, errs, CodeInvalidRef, "personal_memory_profile_ref.memory_kind")
	assertValidationError(t, errs, CodeInvalidRef, "collective_memory_scope_refs[0].memory_kind")
	assertValidationError(t, errs, CodeInvalidRef, "runtime_spec.kind")
}

func TestAgentIdentityKeepsStableAgentReferences(t *testing.T) {
	identity := AgentIdentity{
		AgentID:        AgentID("agent-001"),
		AgentProfileID: AgentProfileID("profile-001"),
		InstanceRef:    AgentInstanceRef{ID: AgentInstanceID("instance-001")},
	}

	if identity.IsZero() {
		t.Fatalf("AgentIdentity with agent and profile ids should not be zero: %#v", identity)
	}
}

func TestAgentConflictErrorCarriesStableCodeAndFields(t *testing.T) {
	err := NewConflictError("agent_profile_conflict", "agent_profile_id", "agent profile changed")

	if err.Code != CodeConflict || err.ConflictCode != "agent_profile_conflict" || err.Field != "agent_profile_id" {
		t.Fatalf("unexpected conflict error: %#v", err)
	}
}

func validAgentProfile() AgentProfile {
	return AgentProfile{
		ID:          AgentProfileID("profile-worker-001"),
		RealName:    "林乔",
		DisplayName: "林乔",
		RoleName:    "Implementation Worker",
		AgentKind:   AgentKindAI,
		OrgIdentity: AgentOrgIdentity{
			OrganizationRef:   OrganizationRef{ID: OrganizationID("org-001")},
			DepartmentRef:     DepartmentRef{ID: DepartmentID("engineering")},
			PositionRef:       PositionRef{ID: PositionID("worker")},
			RoleRefs:          []RoleRef{{ID: RoleID("implementer")}},
			ManagerAgentRef:   AgentProfileRef{ID: AgentProfileID("profile-manager-001")},
			AuthorityScope:    "implementation",
			EmploymentType:    EmploymentTypeAIAgent,
			SupervisionPolicy: SupervisionPolicyManaged,
		},
		Persona: AgentPersona{
			Backstory:              "谨慎可靠的实现专家，习惯留下可审计证据。",
			ResponsibilityBoundary: "负责限定范围内的任务实现、交付物整理和证据提交。",
		},
		BaseAgent: BaseAgentSpec{
			InheritsBaseAgent: true,
			AuditRequired:     true,
			SystemPrompt:      "你是 Orrery 托管的实现 Agent，必须遵守审计、权限和交付流程。",
			LLMAPIKeyRef:      "secret://system/llm-api-key",
			SkillScope:        "profile_bound",
			ToolScope:         "profile_bound",
			MCPScope:          "profile_bound",
		},
		PersonalMemoryProfileRef: MemoryProfileRef{
			MemoryProfileID: MemoryProfileID("memory-lin"),
			OwnerAgentID:    AgentID("agent-lin"),
			MemoryKind:      MemoryKindPersonal,
			AccessScope:     "private",
		},
		CollectiveMemoryScopeRefs: []MemoryProfileRef{{
			MemoryProfileID: MemoryProfileID("memory-engineering"),
			MemoryKind:      MemoryKindCollective,
			AccessScope:     "department",
		}},
		SkillRefs: []SkillRef{{SkillID: SkillID("go-contracts"), Level: "senior"}},
		ToolRefs:  []ToolRef{{ToolID: ToolID("repo"), Alias: "repository"}},
		RuntimeSpec: RuntimeSpec{
			Kind: RuntimeKindCodex,
		},
		Enabled: true,
	}
}
