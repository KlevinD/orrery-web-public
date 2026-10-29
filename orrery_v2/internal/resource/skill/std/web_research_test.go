package std

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestWebResearchDefinitionRequiresAutonomousReadOnlyWebTools(t *testing.T) {
	definition := WebResearchDefinition(time.Unix(0, 0).UTC())
	if definition.ID != WebResearch || !definition.Enabled || len(definition.RequiredTools) != 2 {
		t.Fatalf("definition = %+v", definition)
	}
	for index, want := range []agentcore.ToolID{toolstd.WebSearch, toolstd.WebFetch} {
		ref := definition.RequiredTools[index]
		if ref.ToolID != want || ref.PermissionScope != toolstd.PermissionWebRead || ref.BindingPolicy != "allow" {
			t.Fatalf("required tool %d = %+v", index, ref)
		}
	}
}

func TestEnsureWebResearchSkillRefIsIdempotentAndPreservesBindings(t *testing.T) {
	refs := []agentcore.SkillRef{{SkillID: "skill.keep", Alias: "Keep"}, {SkillID: "SKILL.WEB-RESEARCH"}}
	first := EnsureWebResearchSkillRef(refs)
	second := EnsureWebResearchSkillRef(first)
	if len(first) != 2 || len(second) != 2 ||
		first[0].SkillID != refs[0].SkillID || first[0].Alias != refs[0].Alias {
		t.Fatalf("skill refs = first:%+v second:%+v", first, second)
	}
	if first[1].SkillID != WebResearch || first[1].Alias != "Web research" || !HasWebResearchSkillRef(second) {
		t.Fatalf("web research ref = %+v", first[1])
	}
}
