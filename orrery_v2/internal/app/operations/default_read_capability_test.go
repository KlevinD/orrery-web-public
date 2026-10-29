package operations_test

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/app/operations"
	skillstd "orrery_v2/internal/resource/skill/std"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func assertAgentWebResearchCapabilities(t *testing.T, skills []agentcore.SkillRef, tools []agentcore.ToolRef) {
	t.Helper()
	if !skillstd.HasWebResearchSkillRef(skills) {
		t.Fatalf("skill refs should contain %s: %+v", skillstd.WebResearch, skills)
	}
	for _, want := range []agentcore.ToolRef{
		{ToolID: toolstd.EnterpriseGraphBuildContextBundle, PermissionScope: toolstd.PermissionEnterpriseGraphRead},
		{ToolID: toolstd.EnterpriseGraphSearchObjects, PermissionScope: toolstd.PermissionEnterpriseGraphRead},
		{ToolID: toolstd.WebSearch, PermissionScope: toolstd.PermissionWebRead},
		{ToolID: toolstd.WebFetch, PermissionScope: toolstd.PermissionWebRead},
	} {
		assertToolRef(t, tools, want)
	}
}

func assertConfiguredAgentToolAndDefaultReads(t *testing.T, profile agentcore.AgentProfile, configured agentcore.ToolRef) {
	t.Helper()
	assertToolRef(t, profile.ToolRefs, configured)
	assertToolRef(t, profile.ToolRefs, knowledgeSearchReadToolRef())
	assertMemoryRead(t, profile.ToolRefs)
	assertAgentWebResearchCapabilities(t, profile.SkillRefs, profile.ToolRefs)
}

func assertConfiguredWorkforceAgentToolAndDefaultReads(t *testing.T, member operations.WorkforceMemberDTO, configured operations.WorkforceToolRefDTO) {
	t.Helper()
	skills := make([]agentcore.SkillRef, 0, len(member.SkillRefs))
	for _, ref := range member.SkillRefs {
		skills = append(skills, agentcore.SkillRef{SkillID: agentcore.SkillID(ref.SkillID), Alias: ref.Alias})
	}
	tools := make([]agentcore.ToolRef, 0, len(member.ToolRefs))
	for _, ref := range member.ToolRefs {
		tools = append(tools, agentcore.ToolRef{
			ToolID: agentcore.ToolID(ref.ToolID), VersionConstraint: ref.VersionConstraint, Alias: ref.Alias,
			PermissionScope: ref.PermissionScope, BindingPolicy: ref.BindingPolicy,
		})
	}
	assertConfiguredAgentToolAndDefaultReads(t, agentcore.AgentProfile{SkillRefs: skills, ToolRefs: tools}, agentcore.ToolRef{
		ToolID: agentcore.ToolID(configured.ToolID), VersionConstraint: configured.VersionConstraint, Alias: configured.Alias,
		PermissionScope: configured.PermissionScope, BindingPolicy: configured.BindingPolicy,
	})
}
