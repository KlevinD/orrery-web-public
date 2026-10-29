package main

import (
	"context"
	"strings"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
	"orrery_v2/internal/platform/config"
	"orrery_v2/internal/platform/testkit"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestEnsureDefaultOntologyStewardAgentCreatesManagedDingReport(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	organizations := operations.NewOrganizationCatalogStore(env.DBRouter)
	cfg, err := config.LoadYAML(testConfigSource(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := ensureDefaultCoordinatorAgents(ctx, cfg, profiles, organizations); err != nil {
		t.Fatal(err)
	}
	if err := ensureDefaultOntologyStewardAgent(ctx, cfg, profiles); err != nil {
		t.Fatal(err)
	}
	if err := ensureDefaultOntologyStewardAgent(ctx, cfg, profiles); err != nil {
		t.Fatal(err)
	}

	profile, err := profiles.GetProfile(ctx, defaultOntologyStewardAgentID)
	if err != nil {
		t.Fatal(err)
	}
	if profile.DisplayName != "大头" || profile.RoleName != "本体管理员" || profile.AgentKind != agentcore.AgentKindAI || !profile.Enabled {
		t.Fatalf("ontology steward identity = %+v", profile)
	}
	if profile.IsPersonalAssistant() || profile.OrgIdentity.ManagerAgentRef.ID != defaultOntologyStewardManagerID ||
		profile.OrgIdentity.SupervisionPolicy != agentcore.SupervisionPolicyManaged ||
		profile.OrgIdentity.OrganizationRef.ID != defaultBusinessOrganizationID ||
		profile.OrgIdentity.PositionRef.ID != defaultOntologyStewardPositionID {
		t.Fatalf("ontology steward placement = %+v", profile.OrgIdentity)
	}
	if !strings.Contains(profile.BaseAgent.SystemPrompt, "实际业务") || !strings.Contains(profile.BaseAgent.SystemPrompt, "直属 ding") {
		t.Fatalf("ontology steward prompt = %q", profile.BaseAgent.SystemPrompt)
	}
	for _, required := range []agentcore.ToolID{
		toolstd.EnterpriseGraphBuildContextBundle, toolstd.EnterpriseGraphSearchObjects,
		toolstd.KnowledgeSearch, toolstd.KnowledgeAdd, toolstd.MemorySearch,
		toolstd.RequestBusinessApproval, toolstd.ScheduledTaskList,
		toolstd.OntologyProposalCreate, toolstd.OntologyProposalDecide, toolstd.OntologyPromotionPlanCreate,
		toolstd.OntologyGroundedContext, toolstd.OntologyInfer, toolstd.OntologyInferenceWorkflow,
		toolstd.OntologyPromotionApply, toolstd.OntologyProjectionRefreshRetry,
	} {
		if !hasAgentToolRef(profile.ToolRefs, required) {
			t.Fatalf("ontology steward missing tool %s: %+v", required, profile.ToolRefs)
		}
	}
	availability, err := profiles.GetAvailability(ctx, profile.ID)
	if err != nil || availability.Status != agentcore.AvailabilityAvailable {
		t.Fatalf("ontology steward availability = %+v, err=%v", availability, err)
	}
	profile.BaseAgent.SystemPrompt = "Existing custom business instructions."
	if err := profiles.SaveProfile(ctx, profile); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := ensureDefaultOntologyStewardAgent(ctx, cfg, profiles); err != nil {
			t.Fatal(err)
		}
	}
	profile, err = profiles.GetProfile(ctx, profile.ID)
	if err != nil || !strings.Contains(profile.BaseAgent.SystemPrompt, "Existing custom business instructions.") || strings.Count(profile.BaseAgent.SystemPrompt, "ontology_inference_workflow") != 1 {
		t.Fatalf("model workflow instructions not refreshed safely: %q %v", profile.BaseAgent.SystemPrompt, err)
	}
}

func TestEnsureDefaultOntologyStewardRoleAssignmentIsPlatformScopedAndAgentOnly(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	roles := operations.NewRoleStore(env.DBRouter)
	if err := ensureDefaultOntologyStewardRoleAssignment(ctx, roles); err != nil {
		t.Fatal(err)
	}
	assignment, found, err := roles.GetRoleAssignment(ctx, defaultOntologyStewardAssignmentID)
	if err != nil || !found {
		t.Fatalf("role assignment found=%v err=%v", found, err)
	}
	if assignment.Actor != (operations.ActorRefDTO{ActorType: "agent", ActorID: defaultOntologyStewardAgentID}) ||
		assignment.RoleID != operations.RoleIDOntologySteward || assignment.ScopeMode != operations.RoleScopePlatform {
		t.Fatalf("ontology steward role assignment = %+v", assignment)
	}
	var definition operations.RoleDefinitionDTO
	for _, role := range operations.BuiltInRoleDefinitions() {
		if role.RoleID == operations.RoleIDOntologySteward {
			definition = role
			break
		}
	}
	if len(definition.AssignableActorTypes) != 1 || definition.AssignableActorTypes[0] != "agent" ||
		!ontologyTestContainsString(definition.PermissionRefs, "ontology.manage") ||
		!ontologyTestContainsString(definition.PermissionRefs, "memory.read") ||
		ontologyTestContainsString(definition.PermissionRefs, "roles.manage") || definition.ApprovalCapability {
		t.Fatalf("ontology steward role = %+v", definition)
	}
}

func hasAgentToolRef(refs []agentcore.ToolRef, id agentcore.ToolID) bool {
	for _, ref := range refs {
		if ref.ToolID == id {
			return true
		}
	}
	return false
}

func ontologyTestContainsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
