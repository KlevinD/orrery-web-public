package std

import (
	"testing"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/resource/tool"
)

func TestSessionHistoryToolsArePublishedReadOnlyAndPreserveBindings(t *testing.T) {
	refs := []agentcore.ToolRef{{ToolID: "custom", PermissionScope: "custom.read"}, {ToolID: SessionRead, Alias: "read_old", PermissionScope: "wrong"}}
	updated := EnsureSessionHistoryReadToolRefs(refs)
	if !HasSessionHistoryReadToolRefs(updated) || len(updated) != 3 || updated[0] != refs[0] || updated[1].Alias != "read_old" || refs[1].PermissionScope != "wrong" {
		t.Fatalf("tool binding update: %+v", updated)
	}
	if len(EnsureSessionHistoryReadToolRefs(updated)) != len(updated) {
		t.Fatal("binding update must be idempotent")
	}
	definitions := definitionByID(Definitions(time.Now()))
	for _, id := range []agentcore.ToolID{SessionSearch, SessionRead} {
		definition := definitions[id]
		if !definition.Enabled || definition.RiskLevel != tool.RiskLow || len(definition.Permissions) != 1 || string(definition.Permissions[0].Scope) != PermissionDiscussionRead {
			t.Fatalf("not a read-only history capability: %+v", definition)
		}
		properties := definition.InputSchema["properties"].(map[string]any)
		for _, key := range []string{"tenant_id", "owner_id", "agent_id", "run_id", "logical_agent_id"} {
			if _, exists := properties[key]; exists {
				t.Fatalf("model can set %s", key)
			}
		}
		if definition.InputSchema["additionalProperties"] != false {
			t.Fatal("history arguments must be closed")
		}
	}
}
