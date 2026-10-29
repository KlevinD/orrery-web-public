package discussionsession

import (
	"context"
	"sort"
	"strings"
)

const discussionToolResultContinuationMode = "tool_result_continuation"

func (s *Service) continueDiscussionRunAfterToolResults(ctx context.Context, run *RunRecord, request DiscussionAgentRequest, response DiscussionAgentResponse, linkedMissions []LinkedMissionDTO) (DiscussionAgentResponse, bool, error) {
	if s == nil || s.discussionAgent == nil {
		return response, false, nil
	}
	if response.ToolLoopCompleted {
		return response, false, nil
	}
	if run == nil {
		return response, false, ErrInvalidRequest
	}
	content := discussionToolResultContinuationContent(response.ToolCalls, boundToolsForResponse(request, response))
	if strings.TrimSpace(content) == "" {
		return response, false, nil
	}
	continuation := request
	continuation.Mode = discussionToolResultContinuationMode
	continuation.Content = content
	continuation.LinkedMissions = mergeLinkedMissions(request.LinkedMissions, response.LinkedMissions, linkedMissions)
	continuation.BoundTools = boundToolsForResponse(request, response)
	if sessionID := strings.TrimSpace(response.SessionID); sessionID != "" {
		continuation.ResumeSessionID = sessionID
		continuation.ForceNewSession = false
	}
	if err := s.prepareDiscussionAgentRequest(ctx, &continuation); err != nil {
		return response, false, err
	}
	if err := s.capturePromptDebug(ctx, run.ConversationID, run, continuation); err != nil {
		return response, false, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.registerActiveRun(run.RunID, cancel)
	continued, err := s.discussionAgent.RunDiscussion(runCtx, continuation)
	s.unregisterActiveRun(run.RunID)
	cancel()
	if err != nil {
		return response, false, err
	}
	applyDiscussionResponseOutputPolicy(&continued)
	continued.ProviderMetadata = mergeOutputPrivacyMetadata(response.ProviderMetadata, continued.ProviderMetadata)
	s.applyResponseDebug(run, continued)
	applyResponseTelemetry(run, continued)
	if strings.TrimSpace(continued.AssistantMessage) == "" && hasMissionPrepareTool(response.ToolCalls) {
		continued.AssistantMessage = response.AssistantMessage
	}
	newCalls := append([]ToolCallDTO(nil), continued.ToolCalls...)
	continued.ToolCalls = append(append([]ToolCallDTO(nil), response.ToolCalls...), newCalls...)
	continued.DisplayTimeline = mergeDisplayTimelinesForContinuation(response.DisplayTimeline, continued.DisplayTimeline)
	continued.BoundTools = boundToolsForResponse(continuation, continued)
	continued.LinkedMissions = mergeLinkedMissions(response.LinkedMissions, linkedMissions, continued.LinkedMissions)
	return continued, true, nil
}

func (s *Service) continueDiscussionMentionAfterToolResults(ctx context.Context, request DiscussionAgentMentionRequest, response DiscussionAgentResponse, linkedMissions []LinkedMissionDTO) (DiscussionAgentResponse, bool, error) {
	if s == nil || s.mentionResponder == nil {
		return response, false, nil
	}
	if response.ToolLoopCompleted {
		return response, false, nil
	}
	boundTools := appendDiscussionAgentBoundTools(request.BoundTools, nil)
	if len(boundTools) == 0 {
		boundTools = s.discussionAgentBoundToolsForConversationAgent(ctx, request.ConversationID, request.TargetAgent.ID)
	}
	content := discussionToolResultContinuationContent(response.ToolCalls, boundTools)
	if strings.TrimSpace(content) == "" {
		return response, false, nil
	}
	continuation := request
	continuation.Mode = discussionToolResultContinuationMode
	continuation.Content = content
	continuation.LinkedMissions = mergeLinkedMissions(request.LinkedMissions, response.LinkedMissions, linkedMissions)
	continuation.BoundTools = boundTools
	continuation.BoundToolsPolicy = request.BoundToolsPolicy || len(boundTools) > 0
	if sessionID := strings.TrimSpace(response.SessionID); sessionID != "" {
		continuation.ResumeSessionID = sessionID
		continuation.ForceNewSession = false
	}
	continuationRun := RunRecord{}
	continued, err := s.respondToDiscussionMention(ctx, &continuationRun, continuation, nil)
	if err != nil {
		return response, false, err
	}
	newCalls := append([]ToolCallDTO(nil), continued.ToolCalls...)
	continued.ToolCalls = append(append([]ToolCallDTO(nil), response.ToolCalls...), newCalls...)
	continued.DisplayTimeline = mergeDisplayTimelinesForContinuation(response.DisplayTimeline, continued.DisplayTimeline)
	continued.BoundTools = boundTools
	continued.LinkedMissions = mergeLinkedMissions(response.LinkedMissions, linkedMissions, continued.LinkedMissions)
	return continued, true, nil
}

func discussionToolResultContinuationContent(calls []ToolCallDTO, boundTools []DiscussionAgentBoundTool) string {
	if len(calls) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("工具调用已经执行并返回结果。请基于这些真实结果继续处理当前用户请求；不要重复已成功的工具调用。")
	b.WriteString("若某次调用返回 status=failed 或 error，请读取原方法、参数和错误信息；对于参数错误，应修正参数后再次调用；单次参数错误不能作为结束本轮交互的理由。网页超时、网络不可达等外部依赖问题应由你向用户说明，必要时使用其他可用来源，不要无变化地反复调用同一失败请求。无法取得必要资料时，请明确说明哪些内容无法确认；不要声称已成功访问，也不要要求用户为同一网络故障反复重发消息。")
	b.WriteString("如果下一步需要其他成员处理，请原样使用当前讨论组成员信息中的完整 Markdown 通知链接（例如 [@显示名](mention://agent/profile-id)）通知对方，不能只输出裸 mention://agent/... URI。mission_coordinator_plan_parallel 返回 accepted 或 pending 只表示执行已启动，此时应报告进行中或使用 discussion_ack，不得调用 discussion_close；discussion_close 返回 pending 表示子任务仍在运行，应停止重试关闭并向用户报告进行中；只有 Worker 结果已经交付且验收条件满足时，才能使用 discussion_close 标记完成。\n")
	for _, call := range calls {
		if len(call.Result) == 0 || !toolCallRequiresPostProcess(call, boundTools) {
			continue
		}
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		b.WriteString("- ")
		b.WriteString(name)
		if status := strings.TrimSpace(call.Status); status != "" {
			b.WriteString(" status=")
			b.WriteString(status)
		}
		if args := sortedToolMap(call.Arguments); args != "" {
			b.WriteString(" args={")
			b.WriteString(args)
			b.WriteString("}")
		}
		if result := sortedToolMap(call.Result); result != "" {
			b.WriteString(" result={")
			b.WriteString(result)
			b.WriteString("}")
		}
		b.WriteString("\n")
	}
	content := strings.TrimSpace(b.String())
	if !strings.Contains(content, "- ") {
		return ""
	}
	return content
}

func sortedToolMap(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key, value := range values {
		if strings.TrimSpace(key) == "" || strings.TrimSpace(value) == "" {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+"="+strings.TrimSpace(values[key]))
	}
	return strings.Join(parts, ", ")
}

func newToolCallsAfterContinuation(previous []ToolCallDTO, current []ToolCallDTO) []ToolCallDTO {
	if len(current) == 0 {
		return nil
	}
	seen := map[string]bool{}
	for _, call := range previous {
		key := toolCallContinuationKey(call)
		if key != "" {
			seen[key] = true
		}
	}
	out := make([]ToolCallDTO, 0, len(current))
	for _, call := range current {
		key := toolCallContinuationKey(call)
		if key != "" && seen[key] {
			continue
		}
		out = append(out, call)
	}
	return out
}

func mergeAppliedContinuationToolCalls(current []ToolCallDTO, applied []ToolCallDTO) []ToolCallDTO {
	if len(current) == 0 || len(applied) == 0 {
		return current
	}
	byKey := map[string]ToolCallDTO{}
	for _, call := range applied {
		key := toolCallContinuationKey(call)
		if key != "" {
			byKey[key] = call
		}
	}
	out := append([]ToolCallDTO(nil), current...)
	for i := range out {
		key := toolCallContinuationKey(out[i])
		if updated, ok := byKey[key]; ok {
			out[i] = updated
		}
	}
	return out
}

func toolCallContinuationKey(call ToolCallDTO) string {
	return firstNonBlank(call.ToolCallID, call.Name)
}
