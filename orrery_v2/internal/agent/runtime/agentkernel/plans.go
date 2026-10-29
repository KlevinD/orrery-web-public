package agentkernel

import (
	"encoding/json"
	"slices"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/providers/toolbridge"
)

// Reuse the same local plan contract as PI, only for explicitly authorized IDs.
func appendAuthorizedPlanTools(req agentcore.RuntimeRequest) []agentcore.RuntimeToolBinding {
	bindings := append([]agentcore.RuntimeToolBinding(nil), req.ToolBindings...)
	if req.ExecutionEnvelope == nil || req.ExecutionEnvelope.Verify() != nil {
		return bindings
	}
	var allowed []string
	raw := req.RuntimeOptions.ProviderSpecific[agentcore.RuntimeAllowedToolIDsProviderKey]
	if raw != "" && json.Unmarshal([]byte(raw), &allowed) != nil {
		return bindings
	}
	for _, plan := range toolbridge.PlanTools() {
		if !slices.Contains(req.ExecutionEnvelope.AllowedToolIDs, plan.SourceToolID) ||
			(raw != "" && !slices.Contains(allowed, plan.SourceToolID)) {
			continue
		}
		exists := slices.ContainsFunc(bindings, func(b agentcore.RuntimeToolBinding) bool { return string(b.ToolID) == plan.SourceToolID })
		if !exists {
			bindings = append(bindings, agentcore.RuntimeToolBinding{ToolID: agentcore.ToolID(plan.SourceToolID), Alias: plan.Name,
				Source: "internal_plan", Summary: plan.Description, InputSchema: plan.Parameters})
		}
	}
	return bindings
}
