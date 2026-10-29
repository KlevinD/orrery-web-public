package discussionsession

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

func discussionResponse(response agentcore.RuntimeResponse, runtimeKind agentcore.RuntimeKind) DiscussionAgentResponse {
	return discussionResponseWithBoundTools(response, runtimeKind, nil)
}

func runtimeResponseWithResultSummary(response agentcore.RuntimeResponse, result agentcore.AgentResult) agentcore.RuntimeResponse {
	return response
}

func runtimeCompletedWithoutDiscussionOutput(response agentcore.RuntimeResponse) bool {
	status := response.Status
	if status == "" {
		status = agentcore.RuntimeStatusCompleted
	}
	return status == agentcore.RuntimeStatusCompleted && !runtimeHasDiscussionOutput(response)
}

func runtimeHasDiscussionOutput(response agentcore.RuntimeResponse) bool {
	if runtimeAssistantMessage(response.Messages) != "" || runtimeHasPlanMessage(response.Messages) {
		return true
	}
	if len(response.ToolCalls) > 0 || len(response.Artifacts) > 0 {
		return true
	}
	for _, message := range response.Messages {
		switch message.Type {
		case agentcore.RuntimeMessageToolUse:
			if strings.TrimSpace(message.Tool) != "" || strings.TrimSpace(message.CallID) != "" || len(message.Input) > 0 {
				return true
			}
		case agentcore.RuntimeMessageToolResult:
			if strings.TrimSpace(message.Tool) != "" || strings.TrimSpace(message.CallID) != "" || strings.TrimSpace(message.Output) != "" {
				return true
			}
		}
	}
	return false
}

func runtimeEmptyOutputCause(response agentcore.RuntimeResponse) string {
	if cause := strings.TrimSpace(response.Error.Message); cause != "" {
		return cause
	}
	for _, message := range response.Messages {
		if message.Type == agentcore.RuntimeMessageError {
			if cause := strings.TrimSpace(message.Content); cause != "" {
				return cause
			}
		}
	}
	return "runtime completed without assistant output"
}

func runtimeHasPlanMessage(messages []agentcore.RuntimeMessage) bool {
	for _, message := range messages {
		if message.Type == agentcore.RuntimeMessagePlan && message.Plan != nil {
			return true
		}
	}
	return false
}

func discussionResponseWithBoundTools(response agentcore.RuntimeResponse, runtimeKind agentcore.RuntimeKind, boundTools []DiscussionAgentBoundTool) DiscussionAgentResponse {
	status := string(response.Status)
	if status == "" {
		status = "completed"
	}
	return DiscussionAgentResponse{
		AssistantMessage:  runtimeAssistantMessage(response.Messages),
		Status:            status,
		RuntimeKind:       string(runtimeKind),
		SessionID:         firstRuntimeSessionID(response),
		ToolCalls:         mapRuntimeToolCalls(response.Messages),
		DisplayTimeline:   runtimeDisplayTimeline(response.Messages, string(response.RunID)),
		Usage:             modelUsageFromRuntimeUsage(response.Usage),
		DurationMs:        response.DurationMs,
		ProviderMetadata:  cloneStringMap(response.ProviderMetadata),
		BoundTools:        appendDiscussionAgentBoundTools(boundTools, nil),
		LinkedMissions:    runtimeLinkedMissions(response.Messages),
		RuntimeResponse:   runtimeResponseDebugText(response),
		ToolLoopCompleted: runtimeToolLoopCompleted(response),
	}
}

func runtimeAssistantMessage(messages []agentcore.RuntimeMessage) string {
	if text := firstRuntimeText(messages); text != "" {
		return text
	}
	if plan := completedDiscussionRuntimePlan(messages); plan != nil {
		return "Completed " + strconv.Itoa(len(plan.Items)) + " plan step(s)."
	}
	return ""
}

func completedDiscussionRuntimePlan(messages []agentcore.RuntimeMessage) *agentcore.RuntimePlanState {
	for i := len(messages) - 1; i >= 0; i-- {
		plan := messages[i].Plan
		if messages[i].Type == agentcore.RuntimeMessagePlan && plan != nil && strings.TrimSpace(plan.Status) == "completed" {
			return plan
		}
	}
	return nil
}

func runtimeToolLoopCompleted(response agentcore.RuntimeResponse) bool {
	if response.ProviderMetadata != nil {
		value := strings.TrimSpace(response.ProviderMetadata["tool_loop_completed"])
		if strings.EqualFold(value, "true") || value == "1" || strings.EqualFold(value, "yes") {
			return true
		}
	}
	return runtimeMessagesHaveCompletedToolLoop(response.Messages)
}

func runtimeMessagesHaveCompletedToolLoop(messages []agentcore.RuntimeMessage) bool {
	results := runtimeToolResultsByCallID(messages)
	if len(results) == 0 {
		return false
	}
	hasToolUse := false
	for _, message := range messages {
		if message.Type != agentcore.RuntimeMessageToolUse || strings.TrimSpace(message.Tool) == "" {
			continue
		}
		if isEmptyMissionBriefCreateRuntimeTool(message) {
			continue
		}
		hasToolUse = true
		callID := firstNonBlank(message.CallID, message.Tool)
		result, ok := results[callID]
		if callID == "" || !ok || len(result.Result) == 0 {
			return false
		}
	}
	return hasToolUse
}

func runtimeLinkedMissions(messages []agentcore.RuntimeMessage) []LinkedMissionDTO {
	var out []LinkedMissionDTO
	seen := map[string]bool{}
	for _, result := range runtimeToolResultsByCallID(messages) {
		for _, mission := range runtimeLinkedMissionsFromResult(result.Result) {
			id := strings.TrimSpace(mission.MissionID)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, mission)
		}
	}
	return out
}

func runtimeLinkedMissionsFromResult(result map[string]string) []LinkedMissionDTO {
	raw := strings.TrimSpace(result["linked_missions_json"])
	if raw != "" {
		var missions []LinkedMissionDTO
		if err := json.Unmarshal([]byte(raw), &missions); err == nil {
			return validRuntimeLinkedMissions(missions)
		}
	}
	missionID := strings.TrimSpace(result["mission_id"])
	if missionID == "" {
		return nil
	}
	return []LinkedMissionDTO{{
		MissionID: missionID,
		Title:     strings.TrimSpace(result["title"]),
		Status:    strings.TrimSpace(result["status"]),
		CreatedAt: parseRuntimeToolTime(result["created_at"]),
	}}
}

func validRuntimeLinkedMissions(missions []LinkedMissionDTO) []LinkedMissionDTO {
	out := make([]LinkedMissionDTO, 0, len(missions))
	for _, mission := range missions {
		mission.MissionID = strings.TrimSpace(mission.MissionID)
		mission.Title = strings.TrimSpace(mission.Title)
		mission.Status = strings.TrimSpace(mission.Status)
		if mission.MissionID == "" {
			continue
		}
		out = append(out, mission)
	}
	return out
}

func parseRuntimeToolTime(value string) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}
	}
	return at
}
