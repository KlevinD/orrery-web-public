package discussionsession

import (
	"context"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

const maxRunThinkingSummaryRunes = 8000

var discussionClaudeCreatedTaskIDPattern = regexp.MustCompile(`(?i)task\s*#?\s*([0-9]+)\s+created`)

func (s *Service) flushStreamedThinking(ctx context.Context, run *RunRecord, buffer *discussionStreamThinkingBuffer, sequence *int, emit func(StreamEventDTO)) error {
	if s == nil || run == nil || buffer == nil || sequence == nil || strings.TrimSpace(run.RunID) == "" {
		return nil
	}
	delta := buffer.flush()
	if delta == "" {
		return nil
	}
	markRunReasoningSummaryPublic(run)
	run.ThinkingSummary = appendRunThinkingSummary(run.ThinkingSummary, delta)
	*sequence++
	if emit != nil {
		emit(StreamEventDTO{Kind: StreamEventThinkingDelta, Delta: delta, RunID: run.RunID, Sequence: *sequence})
	}
	if s.repo != nil {
		if err := s.repo.SaveRun(ctx, *run); err != nil {
			return err
		}
	}
	return s.appendRealtimeThinkingDelta(ctx, *run, delta, *sequence)
}

func appendRunThinkingSummary(current, delta string) string {
	combined := []rune(current + delta)
	if len(combined) > maxRunThinkingSummaryRunes {
		combined = combined[len(combined)-maxRunThinkingSummaryRunes:]
	}
	return string(combined)
}

func (s *Service) persistStreamedToolCall(ctx context.Context, run *RunRecord, tool ToolCallDTO) error {
	if s == nil || s.repo == nil || run == nil || strings.TrimSpace(run.RunID) == "" {
		return nil
	}
	if tool.CreatedAt.IsZero() {
		tool.CreatedAt = s.now()
	}
	run.ToolCalls = upsertStreamedToolCall(run.ToolCalls, tool)
	return s.repo.SaveRun(ctx, *run)
}

func (s *Service) persistStreamedPlan(ctx context.Context, run *RunRecord, plan *agentcore.RuntimePlanState) error {
	if s == nil || s.repo == nil || run == nil || strings.TrimSpace(run.RunID) == "" || plan == nil {
		return nil
	}
	run.Plan = cloneRuntimePlanStatePointer(plan)
	run.HasPlan = true
	return s.repo.SaveRun(ctx, *run)
}

func mergeRunToolCalls(current, incoming []ToolCallDTO) []ToolCallDTO {
	out := append([]ToolCallDTO(nil), current...)
	for _, tool := range incoming {
		out = upsertStreamedToolCall(out, tool)
	}
	return out
}

func applyResponseRunProgress(run *RunRecord, response DiscussionAgentResponse) {
	if run == nil {
		return
	}
	run.HasPlan = run.HasPlan || discussionResponseHasPlan(response)
	for i := len(response.DisplayTimeline) - 1; i >= 0; i-- {
		if response.DisplayTimeline[i].Plan == nil {
			continue
		}
		run.Plan = cloneRuntimePlanStatePointer(response.DisplayTimeline[i].Plan)
		run.HasPlan = true
		break
	}
	run.ToolCalls = mergeRunToolCalls(run.ToolCalls, response.ToolCalls)
}

// Older runs persisted Claude task tools but not the derived task state. Recover
// that state when reading them so interrupted work remains inspectable.
func recoverClaudeTaskPlan(calls []ToolCallDTO, runStatus string) *agentcore.RuntimePlanState {
	items := make([]agentcore.RuntimePlanItem, 0)
	for _, call := range calls {
		switch normalizedClaudeTaskToolName(call.Name) {
		case "taskcreate":
			text := firstNonBlank(call.Arguments["subject"], call.Arguments["description"], call.Arguments["activeForm"], call.Arguments["active_form"])
			if text == "" {
				continue
			}
			id := recoveredClaudeTaskID(call)
			if id == "" {
				id = "created:" + firstNonBlank(call.ToolCallID, strconv.Itoa(len(items)+1))
			}
			status := "pending"
			if terminal := recoveredToolTerminalStatus(call.Status); terminal != "" {
				status = terminal
			}
			items = upsertRecoveredClaudeTask(items, agentcore.RuntimePlanItem{ID: id, Text: text, Status: status})
		case "taskupdate":
			id := firstNonBlank(call.Arguments["taskId"], call.Arguments["task_id"], call.Arguments["id"])
			if id == "" {
				continue
			}
			status := normalizeRecoveredClaudeTaskStatus(call.Arguments["status"])
			if terminal := recoveredToolTerminalStatus(call.Status); terminal != "" {
				status = terminal
			}
			text := firstNonBlank(call.Arguments["subject"], call.Arguments["description"])
			items = updateRecoveredClaudeTask(items, id, text, status)
		case "todowrite":
			if todos := recoveredClaudeTodos(call.Arguments["todos"]); len(todos) > 0 {
				items = todos
			}
		}
	}
	if len(items) == 0 {
		return nil
	}
	plan := &agentcore.RuntimePlanState{PlanID: "claude-tasks", Status: claudeTaskPlanStatus(items), Items: items}
	switch strings.ToLower(strings.TrimSpace(runStatus)) {
	case "cancelled", "canceled":
		terminateRecoveredClaudeTaskPlan(plan, "cancelled")
	case "failed", "error", "timeout":
		terminateRecoveredClaudeTaskPlan(plan, "failed")
	}
	return plan
}

func normalizedClaudeTaskToolName(name string) string {
	name = strings.TrimSpace(name)
	if index := strings.LastIndexAny(name, ".:/"); index >= 0 {
		name = name[index+1:]
	}
	return strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(name))
}

func recoveredClaudeTaskID(call ToolCallDTO) string {
	for _, key := range []string{"taskId", "task_id", "id"} {
		if id := strings.TrimSpace(call.Result[key]); id != "" {
			return id
		}
	}
	match := discussionClaudeCreatedTaskIDPattern.FindStringSubmatch(call.Result["output"])
	if len(match) == 2 {
		return match[1]
	}
	return ""
}

func upsertRecoveredClaudeTask(items []agentcore.RuntimePlanItem, item agentcore.RuntimePlanItem) []agentcore.RuntimePlanItem {
	for i := range items {
		if items[i].ID == item.ID {
			items[i] = item
			return items
		}
	}
	return append(items, item)
}

func updateRecoveredClaudeTask(items []agentcore.RuntimePlanItem, id, text, status string) []agentcore.RuntimePlanItem {
	for i := range items {
		if items[i].ID != id {
			continue
		}
		if text != "" {
			items[i].Text = text
		}
		if status != "" {
			items[i].Status = status
		}
		return items
	}
	if text == "" {
		text = "Task #" + id
	}
	if status == "" {
		status = "pending"
	}
	return append(items, agentcore.RuntimePlanItem{ID: id, Text: text, Status: status})
}

func recoveredClaudeTodos(raw string) []agentcore.RuntimePlanItem {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var values []map[string]any
	if json.Unmarshal([]byte(raw), &values) != nil {
		return nil
	}
	items := make([]agentcore.RuntimePlanItem, 0, len(values))
	for index, value := range values {
		text := recoveredClaudeTodoString(value, "content", "subject", "text", "activeForm", "active_form")
		if text == "" {
			continue
		}
		id := recoveredClaudeTodoString(value, "id")
		if id == "" {
			id = "todo-" + strconv.Itoa(index+1)
		}
		status := normalizeRecoveredClaudeTaskStatus(recoveredClaudeTodoString(value, "status"))
		if status == "" {
			status = "pending"
		}
		items = append(items, agentcore.RuntimePlanItem{ID: id, Text: text, Status: status})
	}
	return items
}

func recoveredClaudeTodoString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		switch typed := value[key].(type) {
		case string:
			if text := strings.TrimSpace(typed); text != "" {
				return text
			}
		case json.Number:
			return typed.String()
		case float64:
			if typed == float64(int64(typed)) {
				return strconv.FormatInt(int64(typed), 10)
			}
		}
	}
	return ""
}

func normalizeRecoveredClaudeTaskStatus(status string) string {
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

func recoveredToolTerminalStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "failed", "error", "timeout":
		return "failed"
	case "cancelled", "canceled":
		return "cancelled"
	default:
		return ""
	}
}

func claudeTaskPlanStatus(items []agentcore.RuntimePlanItem) string {
	allDone := len(items) > 0
	for _, item := range items {
		switch item.Status {
		case "failed":
			return "failed"
		case "completed", "cancelled":
		default:
			allDone = false
		}
	}
	if allDone {
		return "completed"
	}
	return "active"
}

func terminateRecoveredClaudeTaskPlan(plan *agentcore.RuntimePlanState, status string) {
	if plan == nil {
		return
	}
	for i := range plan.Items {
		if progressStatusIsActive(plan.Items[i].Status) {
			plan.Items[i].Status = status
		}
	}
	if progressStatusIsActive(plan.Status) {
		plan.Status = status
	}
}

func cancelRunProgress(run *RunRecord) {
	terminateRunProgress(run, "cancelled")
}

func failRunProgress(run *RunRecord) {
	terminateRunProgress(run, "failed")
}

func terminateRunProgress(run *RunRecord, terminalStatus string) {
	if run == nil {
		return
	}
	for i := range run.ToolCalls {
		if progressStatusIsActive(run.ToolCalls[i].Status) {
			run.ToolCalls[i].Status = terminalStatus
		}
	}
	if run.Plan == nil {
		return
	}
	plan := cloneRuntimePlanState(run.Plan)
	for i := range plan.Items {
		if progressStatusIsActive(plan.Items[i].Status) {
			plan.Items[i].Status = terminalStatus
		}
	}
	if progressStatusIsActive(plan.Status) {
		plan.Status = terminalStatus
	}
	run.Plan = &plan
	run.HasPlan = true
}

func terminateResponseProgress(response *DiscussionAgentResponse, terminalStatus string) {
	if response == nil {
		return
	}
	for i := range response.DisplayTimeline {
		plan := response.DisplayTimeline[i].Plan
		if plan == nil {
			continue
		}
		terminated := cloneRuntimePlanState(plan)
		for itemIndex := range terminated.Items {
			if progressStatusIsActive(terminated.Items[itemIndex].Status) {
				terminated.Items[itemIndex].Status = terminalStatus
			}
		}
		if progressStatusIsActive(terminated.Status) {
			terminated.Status = terminalStatus
		}
		response.DisplayTimeline[i].Plan = &terminated
	}
}

func progressStatusIsActive(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "", "active", "in_progress", "in-progress", "pending", "running", "started", "todo":
		return true
	default:
		return false
	}
}

func upsertStreamedToolCall(calls []ToolCallDTO, tool ToolCallDTO) []ToolCallDTO {
	key := streamedToolCallKey(tool)
	out := append([]ToolCallDTO(nil), calls...)
	for i := range out {
		if key != "" && streamedToolCallKey(out[i]) != key {
			continue
		}
		if key == "" && (strings.TrimSpace(out[i].ToolCallID) != "" || strings.TrimSpace(out[i].Name) != strings.TrimSpace(tool.Name)) {
			continue
		}
		out[i] = mergeStreamedToolCall(out[i], tool)
		return out
	}
	return append(out, tool)
}

func mergeStreamedToolCall(existing ToolCallDTO, next ToolCallDTO) ToolCallDTO {
	if strings.TrimSpace(next.ToolCallID) == "" {
		next.ToolCallID = existing.ToolCallID
	}
	if strings.TrimSpace(next.Name) == "" {
		next.Name = existing.Name
	}
	if strings.TrimSpace(next.Status) == "" {
		next.Status = existing.Status
	}
	if len(next.Arguments) == 0 && len(existing.Arguments) > 0 {
		next.Arguments = existing.Arguments
	}
	if len(next.Result) == 0 && len(existing.Result) > 0 {
		next.Result = existing.Result
	}
	if !existing.CreatedAt.IsZero() {
		next.CreatedAt = existing.CreatedAt
	}
	return next
}

func streamedToolCallKey(tool ToolCallDTO) string {
	if id := strings.TrimSpace(tool.ToolCallID); id != "" {
		return id
	}
	return strings.TrimSpace(tool.Name)
}

func mergedStreamedToolCall(calls []ToolCallDTO, fallback ToolCallDTO) ToolCallDTO {
	key := streamedToolCallKey(fallback)
	for i := len(calls) - 1; i >= 0; i-- {
		if key != "" && streamedToolCallKey(calls[i]) == key {
			return calls[i]
		}
	}
	return fallback
}
