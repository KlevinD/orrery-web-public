package claude

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

const claudeTaskPlanID = "claude-tasks"

var claudeCreatedTaskIDPattern = regexp.MustCompile(`(?i)task\s*#?\s*([0-9]+)\s+created`)

type claudePlanToolUse struct {
	name  string
	input map[string]any
}

type claudePlanTracker struct {
	items   []agentcore.RuntimePlanItem
	pending map[string]claudePlanToolUse
}

func newClaudePlanTracker() *claudePlanTracker {
	return &claudePlanTracker{pending: make(map[string]claudePlanToolUse)}
}

func (t *claudePlanTracker) recordToolUse(name, callID string, input map[string]any) *agentcore.RuntimePlanState {
	if t == nil || !isClaudePlanTool(name) || strings.TrimSpace(callID) == "" {
		return nil
	}
	callID = strings.TrimSpace(callID)
	tool := claudePlanToolUse{name: strings.TrimSpace(name), input: cloneClaudePlanInput(input)}
	t.pending[callID] = tool
	changed := false
	switch normalizeClaudePlanToolName(tool.name) {
	case "taskcreate":
		changed = t.applyTaskCreate(tool.input, "", callID)
	case "taskupdate":
		changed = t.applyTaskUpdate(tool.input)
	case "todowrite":
		changed = t.applyTodoWrite(tool.input)
	}
	if !changed || len(t.items) == 0 {
		return nil
	}
	return t.snapshot()
}

func (t *claudePlanTracker) recordToolResult(callID, output string, failed bool) *agentcore.RuntimePlanState {
	if t == nil {
		return nil
	}
	callID = strings.TrimSpace(callID)
	tool, ok := t.pending[callID]
	if !ok {
		return nil
	}
	delete(t.pending, callID)
	if failed {
		if index := t.itemIndex("created:" + callID); index >= 0 {
			t.items[index].Status = "failed"
		}
		if len(t.items) == 0 {
			return nil
		}
		plan := t.snapshot()
		plan.Status = "failed"
		return plan
	}
	changed := false
	switch normalizeClaudePlanToolName(tool.name) {
	case "taskcreate":
		changed = t.applyTaskCreate(tool.input, output, callID)
	case "taskupdate":
		changed = t.applyTaskUpdate(tool.input)
	case "todowrite":
		changed = t.applyTodoWrite(tool.input)
	}
	if !changed || len(t.items) == 0 {
		return nil
	}
	return t.snapshot()
}

func (t *claudePlanTracker) applyTaskCreate(input map[string]any, output, callID string) bool {
	text := firstClaudePlanString(input, "subject", "description", "activeForm", "active_form")
	if text == "" {
		return false
	}
	id := claudeCreatedTaskID(output)
	if id == "" {
		id = "created:" + callID
	}
	provisional := "created:" + callID
	if id != provisional {
		if index := t.itemIndex(provisional); index >= 0 {
			changed := t.items[index].ID != id || t.items[index].Text != text || t.items[index].Status != "pending"
			t.items[index] = agentcore.RuntimePlanItem{ID: id, Text: text, Status: "pending"}
			return changed
		}
	}
	if index := t.itemIndex(id); index >= 0 {
		changed := t.items[index].Text != text || t.items[index].Status != "pending"
		t.items[index].Text = text
		t.items[index].Status = "pending"
		return changed
	}
	t.items = append(t.items, agentcore.RuntimePlanItem{ID: id, Text: text, Status: "pending"})
	return true
}

func (t *claudePlanTracker) applyTaskUpdate(input map[string]any) bool {
	id := firstClaudePlanString(input, "taskId", "task_id", "id")
	if id == "" {
		return false
	}
	status := normalizeClaudePlanItemStatus(firstClaudePlanString(input, "status"))
	text := firstClaudePlanString(input, "subject", "description")
	if index := t.itemIndex(id); index >= 0 {
		changed := false
		if status != "" && t.items[index].Status != status {
			t.items[index].Status = status
			changed = true
		}
		if text != "" && t.items[index].Text != text {
			t.items[index].Text = text
			changed = true
		}
		return changed
	}
	if text == "" {
		text = "Task #" + id
	}
	if status == "" {
		status = "pending"
	}
	t.items = append(t.items, agentcore.RuntimePlanItem{ID: id, Text: text, Status: status})
	return true
}

func (t *claudePlanTracker) applyTodoWrite(input map[string]any) bool {
	raw, ok := input["todos"].([]any)
	if !ok || len(raw) == 0 {
		return false
	}
	items := make([]agentcore.RuntimePlanItem, 0, len(raw))
	for index, value := range raw {
		todo, ok := value.(map[string]any)
		if !ok {
			continue
		}
		text := firstClaudePlanString(todo, "content", "subject", "text", "activeForm", "active_form")
		if text == "" {
			continue
		}
		id := firstClaudePlanString(todo, "id")
		if id == "" {
			id = "todo-" + strconv.Itoa(index+1)
		}
		status := normalizeClaudePlanItemStatus(firstClaudePlanString(todo, "status"))
		if status == "" {
			status = "pending"
		}
		items = append(items, agentcore.RuntimePlanItem{ID: id, Text: text, Status: status})
	}
	if len(items) == 0 || claudePlanItemsEqual(t.items, items) {
		return false
	}
	t.items = items
	return true
}

func (t *claudePlanTracker) itemIndex(id string) int {
	for index := range t.items {
		if t.items[index].ID == id {
			return index
		}
	}
	return -1
}

func (t *claudePlanTracker) snapshot() *agentcore.RuntimePlanState {
	items := append([]agentcore.RuntimePlanItem(nil), t.items...)
	return &agentcore.RuntimePlanState{PlanID: claudeTaskPlanID, Status: claudePlanStatus(items), Items: items}
}

func claudePlanStatus(items []agentcore.RuntimePlanItem) string {
	allDone := len(items) != 0
	for _, item := range items {
		if item.Status == "failed" {
			return "failed"
		}
		if item.Status != "completed" && item.Status != "cancelled" {
			allDone = false
		}
	}
	if allDone {
		return "completed"
	}
	return "active"
}

func claudeCreatedTaskID(output string) string {
	var payload map[string]any
	if json.Unmarshal([]byte(strings.TrimSpace(output)), &payload) == nil {
		if id := firstClaudePlanString(payload, "taskId", "task_id", "id"); id != "" {
			return id
		}
	}
	match := claudeCreatedTaskIDPattern.FindStringSubmatch(output)
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func isClaudePlanTool(name string) bool {
	switch normalizeClaudePlanToolName(name) {
	case "taskcreate", "taskupdate", "todowrite":
		return true
	default:
		return false
	}
}

func normalizeClaudePlanToolName(name string) string {
	name = strings.TrimSpace(name)
	if index := strings.LastIndexAny(name, ".:/"); index >= 0 {
		name = name[index+1:]
	}
	return strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(name))
}

func normalizeClaudePlanItemStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "pending", "todo":
		return "pending"
	case "in_progress", "in-progress", "running", "started":
		return "in_progress"
	case "completed", "complete", "done", "succeeded":
		return "completed"
	case "failed", "error":
		return "failed"
	case "deleted", "cancelled", "canceled":
		return "cancelled"
	default:
		return ""
	}
}

func firstClaudePlanString(input map[string]any, keys ...string) string {
	for _, key := range keys {
		switch value := input[key].(type) {
		case string:
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		case json.Number:
			return value.String()
		case float64:
			if value == float64(int64(value)) {
				return strconv.FormatInt(int64(value), 10)
			}
		}
	}
	return ""
}

func cloneClaudePlanInput(input map[string]any) map[string]any {
	if len(input) == 0 {
		return nil
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return input
	}
	var cloned map[string]any
	if json.Unmarshal(raw, &cloned) != nil {
		return input
	}
	return cloned
}

func claudePlanItemsEqual(left, right []agentcore.RuntimePlanItem) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
