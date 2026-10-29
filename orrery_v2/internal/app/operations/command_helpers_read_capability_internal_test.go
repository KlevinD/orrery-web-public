package operations

import (
	"encoding/json"
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	skillstd "orrery_v2/internal/resource/skill/std"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestEnsureDefaultAgentReadCapabilitiesGivesSessionOnlyAIWebResearch(t *testing.T) {
	profile := agentcore.AgentProfile{
		AgentKind: agentcore.AgentKindAI,
		BaseAgent: agentcore.BaseAgentSpec{Extensions: map[string]json.RawMessage{
			agentcore.BaseAgentMemoryModeExtensionKey: json.RawMessage(`{"mode":"session_only"}`),
		}},
	}

	ensureDefaultAgentReadCapabilities(&profile)

	if !skillstd.HasWebResearchSkillRef(profile.SkillRefs) ||
		!hasAgentToolRef(profile.ToolRefs, toolstd.WebSearch) ||
		!hasAgentToolRef(profile.ToolRefs, toolstd.WebFetch) {
		t.Fatalf("session-only AI web capabilities = skills=%+v tools=%+v", profile.SkillRefs, profile.ToolRefs)
	}
	for _, forbidden := range []agentcore.ToolID{
		toolstd.KnowledgeSearch,
		toolstd.MemorySearch,
		toolstd.EnterpriseGraphBuildContextBundle,
		toolstd.EnterpriseGraphSearchObjects,
	} {
		if hasAgentToolRef(profile.ToolRefs, forbidden) {
			t.Fatalf("session-only AI received %s: %+v", forbidden, profile.ToolRefs)
		}
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
