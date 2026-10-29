package toolbridge

import (
	"encoding/json"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

const (
	planCreateToolName   = "plan_create"
	planUpdateToolName   = "plan_update"
	planCompleteToolName = "plan_complete"

	planCreateToolID   = "plan.create"
	planUpdateToolID   = "plan.update"
	planCompleteToolID = "plan.complete"
)

// PlanTool describes a provider-independent, local progress operation.
type PlanTool struct {
	Name, Label, Description, PromptSnippet, SourceToolID string
	Parameters                                            map[string]any
}

func PlanTools() []PlanTool {
	itemSchema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":     map[string]any{"type": "string", "description": "Stable step id."},
			"text":   map[string]any{"type": "string", "description": "Human-readable step text."},
			"status": map[string]any{"type": "string", "description": "Step status: pending, in_progress, completed, failed, or cancelled."},
		},
		"required": []string{"id", "text", "status"},
	}
	return []PlanTool{
		{
			Name:          planCreateToolName,
			Label:         "Create execution plan",
			Description:   "Create a structured execution plan. Use this before multi-step work.",
			PromptSnippet: "Create a structured execution plan with stable step ids and explicit statuses.",
			Parameters:    planSchema("plan_create input", itemSchema, true),
			SourceToolID:  planCreateToolID,
		},
		{
			Name:          planUpdateToolName,
			Label:         "Update execution plan",
			Description:   "Update statuses for an existing execution plan.",
			PromptSnippet: "Update the structured execution plan as work progresses.",
			Parameters:    planSchema("plan_update input", itemSchema, true),
			SourceToolID:  planUpdateToolID,
		},
		{
			Name:          planCompleteToolName,
			Label:         "Complete execution plan",
			Description:   "Mark an execution plan complete when all tracked work is done.",
			PromptSnippet: "Complete the structured execution plan after all steps are complete.",
			Parameters:    planSchema("plan_complete input", itemSchema, false),
			SourceToolID:  planCompleteToolID,
		},
	}
}

func planSchema(title string, itemSchema map[string]any, itemsRequired bool) map[string]any {
	required := []string{"plan_id"}
	if itemsRequired {
		required = append(required, "items")
	}
	return map[string]any{
		"type":        "object",
		"title":       title,
		"description": title,
		"properties": map[string]any{
			"plan_id": map[string]any{"type": "string", "description": "Stable plan id."},
			"items":   map[string]any{"type": "array", "description": "Structured plan items.", "items": itemSchema},
		},
		"required": required,
	}
}

func IsPlanTool(name string) bool {
	switch strings.TrimSpace(name) {
	case planCreateToolName, planUpdateToolName, planCompleteToolName:
		return true
	default:
		return false
	}
}

func HandlePlanTool(name string, input map[string]any) (map[string]any, error) {
	plan, err := parsePlanInput(name, input)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(plan.Items))
	for _, item := range plan.Items {
		items = append(items, map[string]any{
			"id":     item.ID,
			"text":   item.Text,
			"status": item.Status,
		})
	}
	return map[string]any{
		"plan_id": plan.PlanID,
		"status":  plan.Status,
		"items":   items,
	}, nil
}

type planInput struct {
	PlanID string
	Status string
	Items  []planItem
}

type planItem struct {
	ID     string
	Text   string
	Status string
}

func parsePlanInput(toolName string, input map[string]any) (planInput, error) {
	planID := strings.TrimSpace(inputString(input, "plan_id"))
	if planID == "" {
		return planInput{}, fmt.Errorf("plan_id is required")
	}
	status := "active"
	if toolName == planCompleteToolName {
		status = "completed"
	}
	items, err := parsePlanItems(input["items"], toolName != planCompleteToolName)
	if err != nil {
		return planInput{}, err
	}
	return planInput{PlanID: planID, Status: status, Items: items}, nil
}

func parsePlanItems(raw any, required bool) ([]planItem, error) {
	if raw == nil {
		if required {
			return nil, fmt.Errorf("items are required")
		}
		return nil, nil
	}
	values, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("items must be an array")
	}
	if len(values) == 0 && required {
		return nil, fmt.Errorf("items must not be empty")
	}
	out := make([]planItem, 0, len(values))
	for i, rawItem := range values {
		item, ok := rawItem.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("items[%d] must be an object", i)
		}
		id := strings.TrimSpace(inputString(item, "id"))
		text := strings.TrimSpace(inputString(item, "text"))
		status := strings.TrimSpace(inputString(item, "status"))
		if id == "" {
			return nil, fmt.Errorf("items[%d].id is required", i)
		}
		if text == "" {
			return nil, fmt.Errorf("items[%d].text is required", i)
		}
		if !validPlanItemStatus(status) {
			return nil, fmt.Errorf("items[%d].status is invalid", i)
		}
		out = append(out, planItem{ID: id, Text: text, Status: status})
	}
	return out, nil
}

func validPlanItemStatus(status string) bool {
	switch status {
	case "pending", "in_progress", "completed", "failed", "cancelled":
		return true
	default:
		return false
	}
}

func inputString(input map[string]any, key string) string {
	value, ok := input[key]
	if !ok || value == nil {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return text
}

func RuntimePlanStateFromOutput(toolName string, output string) *agentcore.RuntimePlanState {
	output = strings.TrimSpace(output)
	if output == "" {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(output), &payload); err != nil {
		return nil
	}
	plan, err := parsePlanOutput(toolName, payload)
	if err != nil {
		return nil
	}
	items := make([]agentcore.RuntimePlanItem, 0, len(plan.Items))
	for _, item := range plan.Items {
		items = append(items, agentcore.RuntimePlanItem{ID: item.ID, Text: item.Text, Status: item.Status})
	}
	return &agentcore.RuntimePlanState{PlanID: plan.PlanID, Status: plan.Status, Items: items}
}

func parsePlanOutput(toolName string, output map[string]any) (planInput, error) {
	planID := strings.TrimSpace(inputString(output, "plan_id"))
	if planID == "" {
		return planInput{}, fmt.Errorf("plan_id is required")
	}
	status := strings.TrimSpace(inputString(output, "status"))
	if status == "" {
		status = "active"
	}
	if toolName == planCompleteToolName {
		status = "completed"
	}
	items, err := parsePlanItems(output["items"], false)
	if err != nil {
		return planInput{}, err
	}
	return planInput{PlanID: planID, Status: status, Items: items}, nil
}
