package pi

import (
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/providers/toolbridge"
)

const (
	planCreateToolName   = "plan_create"
	planUpdateToolName   = "plan_update"
	planCompleteToolName = "plan_complete"
	planCreateToolID     = "plan.create"
	planUpdateToolID     = "plan.update"
	planCompleteToolID   = "plan.complete"
)

func planBridgeTools() []piBridgeTool {
	var tools []piBridgeTool
	for _, plan := range toolbridge.PlanTools() {
		tools = append(tools, piBridgeTool{
			Name: plan.Name, Label: plan.Label, Description: plan.Description,
			PromptSnippet: plan.PromptSnippet, Parameters: plan.Parameters, SourceToolID: plan.SourceToolID,
		})
	}
	return tools
}

func isPlanTool(name string) bool { return toolbridge.IsPlanTool(name) }
func handlePlanTool(name string, input map[string]any) (map[string]any, error) {
	return toolbridge.HandlePlanTool(name, input)
}
func runtimePlanStateFromOutput(name, output string) *agentcore.RuntimePlanState {
	return toolbridge.RuntimePlanStateFromOutput(name, output)
}
