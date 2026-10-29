package discussionsession

import (
	"encoding/json"
	"fmt"
	"strings"

	toolstd "orrery_v2/internal/resource/tool/std"
)

const (
	discussionOrchestrationWarningMetadataKey = "orchestration_warning"
	missionPrepareDirectAnswerWarning         = "提示：已创建 Mission，但 Lead Agent 没有将任务交给专业执行 Agent，而是直接给出了上述答复。该答复可能并非专业回答，请谨慎参考。"
)

func mergeLinkedMissions(groups ...[]LinkedMissionDTO) []LinkedMissionDTO {
	var out []LinkedMissionDTO
	seen := map[string]bool{}
	replaced := map[string]bool{}
	for _, group := range groups {
		for _, mission := range group {
			if id := strings.TrimSpace(mission.ReplacesMissionID); id != "" {
				replaced[id] = true
			}
		}
	}
	for _, group := range groups {
		for _, mission := range group {
			id := strings.TrimSpace(mission.MissionID)
			if id == "" || replaced[id] || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, mission)
		}
	}
	return out
}

func isMissionBriefCreateTool(name string) bool {
	switch normalizeToolName(name) {
	case string(toolstd.MissionPrepare), string(toolstd.MissionBriefCreate):
		return true
	default:
		return false
	}
}

func newOrchestrationIncompleteError(reason string) error {
	return fmt.Errorf("%w: %s", ErrOrchestrationIncomplete, reason)
}

func missionToolingError(calls []ToolCallDTO, cause error) error {
	if cause == nil {
		return nil
	}
	reason := missionToolingFailureReason(calls, cause)
	if reason == "" {
		reason = "当前 Agent 的 Mission 工具调用没有成功落地；本轮不会保存普通回复，避免把未完成的编排当成成功结果。"
	}
	return fmt.Errorf("%w: %s 原始错误：%w", ErrOrchestrationIncomplete, reason, cause)
}

func missionToolingFailureReason(calls []ToolCallDTO, cause error) string {
	raw := cause.Error()
	if strings.Contains(raw, "assignee_id must reference an enabled agent profile") {
		assigneeID := firstMissionAssigneeID(calls)
		if assigneeID == "" {
			return "当前 Agent 已经尝试创建或分配 Mission 任务，但指定的执行人不是人员目录中的已启用 Agent 档案。Mission Control 拒绝创建任务，避免把任务分给不存在或被禁用的人。请在人员目录启用/创建对应 Agent，或请选择真实可用成员。"
		}
		return "当前 Agent 已经尝试创建或分配 Mission 任务，指定执行人 " + assigneeID + "；但人员目录里没有这个已启用的 Agent 档案。Mission Control 拒绝创建任务，避免把任务分给不存在或被禁用的人。请在人员目录启用/创建 " + assigneeID + "，或请选择真实可用成员。"
	}
	return ""
}

func firstMissionAssigneeID(calls []ToolCallDTO) string {
	for _, call := range calls {
		if id := strings.TrimSpace(firstNonBlank(call.Arguments["assignee_id"], call.Result["assignee_id"])); id != "" {
			return id
		}
		if id := strings.TrimSpace(firstNonBlank(call.Arguments["agent_id"], call.Result["agent_id"])); id != "" && isCoordinatorPlanTool(call.Name) {
			return id
		}
		if id := firstWorkerLaneAgentID(call.Arguments["worker_lanes_json"]); id != "" {
			return id
		}
		if id := firstWorkerLaneAgentID(call.Result["worker_lanes_json"]); id != "" {
			return id
		}
	}
	return ""
}

func firstWorkerLaneAgentID(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	var lanes []struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.Unmarshal([]byte(raw), &lanes); err != nil {
		return ""
	}
	for _, lane := range lanes {
		if id := strings.TrimSpace(lane.AgentID); id != "" {
			return id
		}
	}
	return ""
}

func orchestrationStopError(applied []LinkedMissionDTO, calls []ToolCallDTO, assistantMessage string) error {
	if createsMissionWithoutRootTask(applied, calls) {
		return newOrchestrationIncompleteError("Agent created a Mission but stopped before returning a usable root task; expected mission_task_create_root or mission_coordinator_plan_parallel")
	}
	if missionPrepareStoppedWithoutHandoff(applied, calls, assistantMessage) {
		if strings.TrimSpace(assistantMessage) != "" {
			return nil
		}
		return newOrchestrationIncompleteError("Agent 已调用 mission_prepare 创建或更新 Mission Brief 和团队根任务，但没有继续把执行交给任何真实 Agent。mission_prepare 只是记录团队目标，不代表已经开工；本轮需要继续调用 discussion_talent_list/discussion_member_add、mission_coordinator_plan_parallel 或 discussion_close，或用标准 mention://agent 链接通知明确的执行 Agent 继续处理。")
	}
	if missionCompletionClaimedWithoutClose(applied, calls, assistantMessage) {
		return newOrchestrationIncompleteError("Agent 不能只通过普通消息确认 Mission/任务完成；如果当前轮次确实已经最终交付，必须调用 discussion_close 标记总目标完成并记录结果。")
	}
	if toolName := firstTodoToolName(calls); toolName != "" {
		return newOrchestrationIncompleteError("Agent stopped before task completion with unresolved tool " + toolName + " still marked todo")
	}
	if toolName, status, detail := firstIncompleteTool(calls, strings.TrimSpace(assistantMessage) != ""); toolName != "" {
		reason := "Agent stopped before task completion with unresolved tool " + toolName + " in status " + status
		if detail != "" {
			reason += ": " + detail
		}
		return newOrchestrationIncompleteError(reason)
	}
	return nil
}

func orchestrationStopWarning(applied []LinkedMissionDTO, calls []ToolCallDTO, assistantMessage string) string {
	if strings.TrimSpace(assistantMessage) == "" || !missionPrepareStoppedWithoutHandoff(applied, calls, assistantMessage) {
		return ""
	}
	return missionPrepareDirectAnswerWarning
}

func missionPrepareStoppedWithoutHandoff(applied []LinkedMissionDTO, calls []ToolCallDTO, assistantMessage string) bool {
	if !hasAppliedMission(applied) || !hasMissionPrepareTool(calls) {
		return false
	}
	for _, call := range calls {
		if isTaskContinuationTool(call.Name) || normalizeToolName(call.Name) == string(toolstd.MissionTaskDelegate) {
			return false
		}
	}
	return !hasDiscussionAgentMentionLink(strings.TrimSpace(assistantMessage))
}

func missionCompletionClaimedWithoutClose(applied []LinkedMissionDTO, calls []ToolCallDTO, assistantMessage string) bool {
	if !hasAppliedMission(applied) || hasDiscussionCloseTool(calls) || hasDiscussionAgentMentionLink(strings.TrimSpace(assistantMessage)) {
		return false
	}
	message := strings.ToLower(strings.TrimSpace(assistantMessage))
	if message == "" {
		return false
	}
	for _, marker := range []string{
		"交付完成",
		"已经交付",
		"目标完成",
		"任务完成",
		"mission completed",
		"task completed",
		"done",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func hasDiscussionCloseTool(calls []ToolCallDTO) bool {
	for _, call := range calls {
		if normalizeToolName(call.Name) != string(toolstd.DiscussionClose) || !isCompletedToolStatus(call.Status) {
			continue
		}
		switch strings.TrimSpace(strings.ToLower(call.Result["status"])) {
		case MissionClosePending:
			continue
		default:
			return true
		}
	}
	return false
}

func hasMissionPrepareTool(calls []ToolCallDTO) bool {
	for _, call := range calls {
		if normalizeToolName(call.Name) == string(toolstd.MissionPrepare) {
			return true
		}
	}
	return false
}

func createsMissionWithoutRootTask(applied []LinkedMissionDTO, calls []ToolCallDTO) bool {
	if !hasAppliedMission(applied) {
		return false
	}
	createdBrief := false
	for _, call := range calls {
		name := normalizeToolName(call.Name)
		if !isCompletedToolStatus(call.Status) {
			continue
		}
		if name == string(toolstd.MissionBriefCreate) {
			createdBrief = true
		}
		if isTaskCreateTool(name) || isCoordinatorPlanTool(name) {
			return false
		}
	}
	return createdBrief
}

func hasAppliedMission(applied []LinkedMissionDTO) bool {
	for _, mission := range applied {
		if strings.TrimSpace(mission.MissionID) != "" {
			return true
		}
	}
	return false
}

func firstTodoToolName(calls []ToolCallDTO) string {
	completedLater := map[string]bool{}
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		name := normalizeToolName(call.Name)
		status := strings.TrimSpace(strings.ToLower(call.Status))
		if isCompletedToolStatus(status) {
			if isTaskContinuationTool(name) {
				return ""
			}
			completedLater[name] = true
			continue
		}
		if completedLater[name] {
			continue
		}
		if strings.TrimSpace(strings.ToLower(call.Status)) == "todo" {
			return strings.TrimSpace(call.Name)
		}
	}
	return ""
}

func firstIncompleteTool(calls []ToolCallDTO, allowAnswerCompatibleFailure bool) (string, string, string) {
	completedLater := map[string]bool{}
	for i := len(calls) - 1; i >= 0; i-- {
		call := calls[i]
		name := normalizeToolName(call.Name)
		status := strings.TrimSpace(strings.ToLower(call.Status))
		if isCompletedToolStatus(status) {
			if isTaskContinuationTool(name) {
				return "", "", ""
			}
			completedLater[name] = true
			continue
		}
		if status == "" || completedLater[name] {
			continue
		}
		if allowAnswerCompatibleFailure && status == "failed" && isAnswerCompatibleToolFailure(call) {
			continue
		}
		return strings.TrimSpace(call.Name), status, toolCallFailureDetail(call)
	}
	return "", "", ""
}

func isAnswerCompatibleToolFailure(call ToolCallDTO) bool {
	name := normalizeToolName(call.Name)
	// Public web access failures are tool results for the Agent to explain.
	// Preserve its final answer without claiming that the fetch succeeded.
	if name == normalizeToolName(string(toolstd.WebFetch)) || name == normalizeToolName(string(toolstd.WebSearch)) {
		return true
	}
	if isOptionalRetrievalTool(name) {
		return true
	}
	if name == "read" {
		return true
	}
	return strings.Contains(strings.ToLower(toolCallFailureDetail(call)), "blocked by orrery runtime approval")
}

func isOptionalRetrievalTool(name string) bool {
	switch normalizeToolName(name) {
	case normalizeToolName(string(toolstd.KnowledgeSearch)),
		normalizeToolName(string(toolstd.MemorySearch)),
		normalizeToolName(string(toolstd.SessionSearch)),
		normalizeToolName(string(toolstd.SessionRead)),
		normalizeToolName(string(toolstd.EnterpriseGraphBuildContextBundle)),
		normalizeToolName(string(toolstd.EnterpriseGraphSearchObjects)),
		normalizeToolName(string(toolstd.SkillList)),
		normalizeToolName(string(toolstd.SkillLoad)),
		normalizeToolName(string(toolstd.SkillResourceList)),
		normalizeToolName(string(toolstd.SkillResourceLoad)),
		normalizeToolName(string(toolstd.RSILearningPlan)):
		return true
	default:
		return false
	}
}

func toolCallFailureDetail(call ToolCallDTO) string {
	for _, key := range []string{"error", "message", "detail", "output", "recovery_hint"} {
		if value := strings.TrimSpace(call.Result[key]); value != "" {
			return value
		}
	}
	return ""
}

func isCompletedToolStatus(status string) bool {
	switch strings.TrimSpace(strings.ToLower(status)) {
	case "completed", "complete", "done", "success", "succeeded", "accepted":
		return true
	default:
		return false
	}
}

func isTaskCreateTool(name string) bool {
	switch normalizeToolName(name) {
	case string(toolstd.MissionPrepare), string(toolstd.MissionTaskCreateRoot):
		return true
	default:
		return false
	}
}

func isTaskContinuationTool(name string) bool {
	switch normalizeToolName(name) {
	case string(toolstd.DiscussionClose):
		return true
	default:
		return isCoordinatorPlanTool(name)
	}
}

func isCoordinatorPlanTool(name string) bool {
	switch normalizeToolName(name) {
	case string(toolstd.MissionCoordinatorPlanParallel):
		return true
	default:
		return false
	}
}
