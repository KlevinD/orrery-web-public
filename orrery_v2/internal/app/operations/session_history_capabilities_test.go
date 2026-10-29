package operations

import (
	"testing"

	agentcore "orrery_v2/internal/agent/core"
	toolstd "orrery_v2/internal/resource/tool/std"
)

func TestSessionHistoryDefaultCapabilitiesKeepOtherBindings(t *testing.T) {
	profile := agentcore.AgentProfile{AgentKind: agentcore.AgentKindAI, ToolRefs: []agentcore.ToolRef{{ToolID: "custom", Alias: "local", PermissionScope: "custom.read"}}}
	ensureDefaultAgentReadCapabilities(&profile)
	if !toolstd.HasSessionHistoryReadToolRefs(profile.ToolRefs) || profile.ToolRefs[0].Alias != "local" {
		t.Fatalf("new agents lack history tools or lost existing bindings: %+v", profile.ToolRefs)
	}
}
