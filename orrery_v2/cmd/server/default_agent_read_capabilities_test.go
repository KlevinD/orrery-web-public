package main

import (
	"context"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	memstore "orrery_v2/internal/memory/store"
	"orrery_v2/internal/platform/testkit"
	skillstd "orrery_v2/internal/resource/skill/std"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestEnsureAgentReadCapabilitiesRepairsLegacyProfilesAndPreservesSessionOnlyIsolation(t *testing.T) {
	ctx := context.Background()
	env := testkit.New(t)
	defer env.Close()
	profiles := agentcore.NewPlatformProfileRepository(env.UnitOfWork)
	memory := memstore.NewPlatformStore(env.DB)
	template, err := readDefaultAgentTemplate("bug-fixer.json")
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	legacy := defaultFreelanceAgentProfile(agentcore.RuntimeSpec{Kind: agentcore.RuntimeKindManaged}, template)
	legacy.ID = "legacy-reader"
	legacy.PersonalMemoryProfileRef.OwnerAgentID = "legacy-reader"
	legacy.PersonalMemoryProfileRef = agentcore.MemoryProfileRef{}
	legacy.CollectiveMemoryScopeRefs = nil
	legacy.ToolRefs = []agentcore.ToolRef{{ToolID: "tool.keep", PermissionScope: "keep.read"}}
	if err := saveRawAgentProfile(ctx, env.UnitOfWork, legacy); err != nil {
		t.Fatalf("save legacy profile: %v", err)
	}
	website := defaultWebsiteAdvisorProfile()
	website.SkillRefs = []agentcore.SkillRef{}
	website.ToolRefs = []agentcore.ToolRef{defaultOrreryPublicMCPServerToolRef()}
	if err := profiles.SaveProfile(ctx, website); err != nil {
		t.Fatalf("save website profile: %v", err)
	}

	if err := ensureAgentReadCapabilities(ctx, profiles); err != nil {
		t.Fatalf("ensure read capabilities: %v", err)
	}
	repaired, err := profiles.GetProfile(ctx, legacy.ID)
	if err != nil {
		t.Fatalf("get repaired profile: %v", err)
	}
	if repaired.PersonalMemoryProfileRef.MemoryProfileID != "memory-legacy-reader" ||
		repaired.PersonalMemoryProfileRef.OwnerAgentID != "legacy-reader" ||
		repaired.PersonalMemoryProfileRef.MemoryKind != agentcore.MemoryKindPersonal {
		t.Fatalf("repaired personal memory ref = %+v", repaired.PersonalMemoryProfileRef)
	}
	if !hasKnowledgeSearchReadCapability(repaired.ToolRefs) ||
		!toolstd.HasSessionHistoryReadToolRefs(repaired.ToolRefs) ||
		!hasMemorySearchReadCapability(repaired.ToolRefs) ||
		!hasLearningReviewCapability(repaired.ToolRefs) ||
		!hasEnterpriseGraphReadCapability(repaired.ToolRefs) ||
		!hasWebSearchReadCapability(repaired.ToolRefs) ||
		!hasWebFetchReadCapability(repaired.ToolRefs) ||
		!hasToolRef(repaired.ToolRefs, "tool.keep") {
		t.Fatalf("repaired tool refs = %+v", repaired.ToolRefs)
	}
	if !skillstd.HasWebResearchSkillRef(repaired.SkillRefs) {
		t.Fatalf("repaired skill refs = %+v", repaired.SkillRefs)
	}

	if err := ensureAgentMemoryProfiles(ctx, profiles, memory); err != nil {
		t.Fatalf("ensure agent memory profiles: %v", err)
	}
	if _, err := memory.GetProfile(ctx, "memory-legacy-reader"); err != nil {
		t.Fatalf("get materialized personal memory profile: %v", err)
	}
	repaired, err = profiles.GetProfile(ctx, legacy.ID)
	if err != nil {
		t.Fatalf("get repaired profile after memory bootstrap: %v", err)
	}
	if len(repaired.CollectiveMemoryScopeRefs) != 2 {
		t.Fatalf("collective memory refs = %+v", repaired.CollectiveMemoryScopeRefs)
	}

	isolated, err := profiles.GetProfile(ctx, website.ID)
	if err != nil {
		t.Fatalf("get website profile: %v", err)
	}
	if !isolated.PersonalMemoryProfileRef.IsZero() || len(isolated.CollectiveMemoryScopeRefs) != 0 {
		t.Fatalf("session-only website profile received memory refs: %+v", isolated)
	}
	if hasToolRef(isolated.ToolRefs, toolstd.KnowledgeSearch) {
		t.Fatalf("session-only website profile received standard knowledge search: %+v", isolated.ToolRefs)
	}
	if hasLearningReviewCapability(isolated.ToolRefs) || hasToolRef(isolated.ToolRefs, toolstd.SessionSearch) || hasToolRef(isolated.ToolRefs, toolstd.SessionRead) || hasToolRef(isolated.ToolRefs, toolstd.MemorySearch) || hasToolRef(isolated.ToolRefs, toolstd.EnterpriseGraphBuildContextBundle) ||
		hasToolRef(isolated.ToolRefs, toolstd.EnterpriseGraphSearchObjects) {
		t.Fatalf("session-only website profile received autonomous context tools: %+v", isolated.ToolRefs)
	}
	if !hasWebSearchReadCapability(isolated.ToolRefs) || !hasWebFetchReadCapability(isolated.ToolRefs) {
		t.Fatalf("session-only website profile did not receive public web tools: %+v", isolated.ToolRefs)
	}
	if !skillstd.HasWebResearchSkillRef(isolated.SkillRefs) {
		t.Fatalf("session-only website profile did not receive web research skill: %+v", isolated.SkillRefs)
	}
}
