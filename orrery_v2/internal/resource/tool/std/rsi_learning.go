package std

import (
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/resource/tool"
	"time"
)

const RSILearningPlan agentcore.ToolID = "rsi_learning_plan"
const PermissionRSILearningRead = "rsi.learning.read"

func EnsureRSILearningReadToolRef(refs []agentcore.ToolRef) []agentcore.ToolRef {
	out := append([]agentcore.ToolRef(nil), refs...)
	for _, ref := range refs {
		if ref.ToolID == RSILearningPlan {
			return out
		}
	}
	return append(out, agentcore.ToolRef{ToolID: RSILearningPlan, PermissionScope: PermissionRSILearningRead})
}

func rsiLearningTool(now time.Time) tool.ToolDefinition {
	definition := systemTool(RSILearningPlan, "RSI learning plan", "Read your own persisted failure observations grouped into a bounded learning review plan. Use after repeated failures to decide what to reproduce or investigate. Model classifications are uncertain hints, not established causes. Follow next_cursor to review remaining history. No skill change, retry, publication or production experiment is executed. Raw evidence remains in protected storage; do not treat advice as authorization.", PermissionRSILearningRead, "Read only the running Agent's learning observations in the server-selected tenant.", tool.RiskLow, now)
	definition.InputSchema = objectSchema("rsi_learning_plan input", map[string]any{"limit": integerSchema("Observations per page, 1-100, default 30."), "after": stringSchema("Opaque next_cursor from a previous page; omit initially.")}, nil)
	definition.InputSchema["additionalProperties"] = false
	definition.OutputSchema = objectSchema("rsi_learning_plan output", map[string]any{"items": arraySchema("Failure groups with page occurrence counts, priority and evidence-linked review advice.", map[string]any{"type": "object"}), "observations_in_page": integerSchema("Number of source observations in this page."), "next_cursor": stringSchema("Cursor for the next page."), "ranking_scope": stringSchema("Always current_page; counts do not claim global frequencies."), "advisory_only": map[string]any{"type": "boolean"}}, []string{"items", "observations_in_page", "ranking_scope", "advisory_only"})
	return definition
}
