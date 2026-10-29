package discussionsession

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/workspaceprompt"
)

func (r *BaseAgentDiscussionAgentRunner) DeleteDiscussionRuntimeRecord(ctx context.Context, runID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || r.agent == nil {
		return nil
	}
	base := r.agent.Base()
	if base == nil {
		return nil
	}
	if reader, ok := base.Runtime().(agentcore.SessionRuntimeRecordReader); ok {
		return reader.DeleteRuntimeRecord(ctx, agentcore.AgentRunID(runID))
	}
	return nil
}

func (r *BaseAgentDiscussionAgentRunner) ReleaseRuntimeWorkspace(ctx context.Context, workspaceRoot string) error {
	if r == nil || r.agent == nil {
		return nil
	}
	base := r.agent.Base()
	if base == nil || base.Runtime() == nil {
		return nil
	}
	return base.Runtime().ReleaseRuntimeWorkspace(ctx, workspaceRoot)
}

func (r *BaseAgentDiscussionAgentRunner) PrewarmDiscussion(ctx context.Context, request DiscussionAgentRequest) error {
	if r == nil || r.agent == nil || r.agent.Base() == nil {
		return ErrRuntimeUnavailable
	}
	request.BoundTools = foldMissionPreparationTools(discussionAgentBoundToolsForRuntime(request, r.DiscussionAgentBoundTools()))
	request.BoundSkills = firstDiscussionAgentBoundSkills(request.BoundSkills, r.DiscussionAgentBoundSkills())
	input, err := r.agentInput(ctx, request)
	if err != nil {
		return NewRuntimeSetupError(string(r.spec.Kind), err.Error())
	}
	runtime := r.agent.Base().Runtime()
	if runtime == nil {
		return ErrRuntimeUnavailable
	}
	return runtime.PrewarmSession(ctx, r.agent.Base().BuildRuntimeRequest(input))
}

func (r *BaseAgentDiscussionAgentRunner) SteerDiscussionRun(ctx context.Context, run RunRecord, message string) error {
	if r == nil || r.agent == nil || strings.TrimSpace(run.RunID) == "" || strings.TrimSpace(message) == "" {
		return ErrInvalidRequest
	}
	base := r.agent.Base()
	if base == nil {
		return ErrRuntimeUnavailable
	}
	runtime := base.Runtime()
	if runtime == nil {
		return ErrRuntimeUnavailable
	}
	return runtime.SteerSession(ctx, agentcore.AgentRunID(run.RunID), strings.TrimSpace(message))
}

func runtimeResponseFromAgent(agent agentcore.Agent, runID agentcore.AgentRunID) (agentcore.RuntimeResponse, bool) {
	if agent == nil || agent.Base() == nil {
		return agentcore.RuntimeResponse{}, false
	}
	recorder, ok := agent.Base().Runtime().(agentcore.SessionRuntimeRecordReader)
	if !ok {
		return agentcore.RuntimeResponse{}, false
	}
	return recorder.RuntimeResponse(runID)
}

func runtimeRequestFromAgent(agent agentcore.Agent, runID agentcore.AgentRunID) (agentcore.RuntimeRequest, bool) {
	if agent == nil || agent.Base() == nil {
		return agentcore.RuntimeRequest{}, false
	}
	recorder, ok := agent.Base().Runtime().(agentcore.SessionRuntimeRecordReader)
	if !ok {
		return agentcore.RuntimeRequest{}, false
	}
	return recorder.RuntimeRequest(runID)
}

func runtimeOutboundPromptFromAgent(agent agentcore.Agent, runID agentcore.AgentRunID, kind agentcore.RuntimeKind) string {
	request, ok := runtimeRequestFromAgent(agent, runID)
	if !ok {
		return ""
	}
	response, hasResponse := runtimeResponseFromAgent(agent, runID)
	if hasResponse {
		return outboundPromptFromRuntimeExchange(request, response, kind)
	}
	return outboundPromptFromRuntimeRequest(request, kind)
}

func outboundPromptFromRuntimeRequest(req agentcore.RuntimeRequest, kind agentcore.RuntimeKind) string {
	var parts []string
	if system := outboundSystemPromptFromRuntimeRequest(req); system != "" {
		parts = append(parts, system)
	}
	if prompt := outboundUserPromptFromRuntimeRequest(req, kind); prompt != "" {
		parts = append(parts, prompt)
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func outboundPromptFromRuntimeExchange(req agentcore.RuntimeRequest, response agentcore.RuntimeResponse, kind agentcore.RuntimeKind) string {
	if len(response.OutboundMessages) > 0 {
		return runtimeChatMessagesDebugText(response.OutboundMessages)
	}
	return outboundPromptFromRuntimeRequest(req, kind)
}

func runtimeChatMessagesDebugText(messages []agentcore.RuntimeChatMessage) string {
	parts := make([]string, 0, len(messages))
	for _, message := range messages {
		role := strings.TrimSpace(message.Role)
		if role == "" {
			role = "message"
		}
		lines := []string{"role: " + role}
		if strings.TrimSpace(message.ToolName) != "" {
			lines = append(lines, "tool_name: "+strings.TrimSpace(message.ToolName))
		}
		if strings.TrimSpace(message.ToolCallID) != "" {
			lines = append(lines, "tool_call_id: "+strings.TrimSpace(message.ToolCallID))
		}
		if strings.TrimSpace(message.Content) != "" {
			lines = append(lines, strings.TrimSpace(message.Content))
		}
		for _, call := range message.ToolCalls {
			line := "tool_call"
			if strings.TrimSpace(call.Name) != "" {
				line += " " + strings.TrimSpace(call.Name)
			}
			if strings.TrimSpace(call.ID) != "" {
				line += " id=" + strings.TrimSpace(call.ID)
			}
			lines = append(lines, line)
			if len(call.Input) > 0 {
				if raw, err := json.MarshalIndent(call.Input, "", "  "); err == nil {
					lines = append(lines, string(raw))
				}
			}
		}
		parts = append(parts, strings.TrimSpace(strings.Join(lines, "\n")))
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func outboundSystemPromptFromRuntimeRequest(req agentcore.RuntimeRequest) string {
	return strings.TrimSpace(workspaceprompt.SystemPrompt(req.RuntimeOptions.SystemPrompt, runtimecore.ExecOptions{
		Cwd:             req.RuntimeOptions.WorkingDirectory,
		WorkspacePolicy: req.RuntimeOptions.WorkspacePolicy,
	}))
}

func outboundUserPromptFromRuntimeRequest(req agentcore.RuntimeRequest, kind agentcore.RuntimeKind) string {
	if prompt := strings.TrimSpace(agentcore.RuntimePromptMessagesFallbackPrompt(req.InputMessages)); prompt != "" {
		return prompt
	}
	if kind == agentcore.RuntimeKindManaged {
		return runtimeRequestPromptForDebug(req)
	}
	if strings.TrimSpace(req.ContextPack.WorkerPersonalGoal) != "" {
		return strings.TrimSpace(req.ContextPack.WorkerPersonalGoal)
	}
	if strings.TrimSpace(req.ContextPack.MissionFinalGoal) != "" {
		return strings.TrimSpace(req.ContextPack.MissionFinalGoal)
	}
	return strings.TrimSpace(req.TaskID)
}

func runtimeRequestPromptForDebug(req agentcore.RuntimeRequest) string {
	var lines []string
	if strings.TrimSpace(req.MissionID) != "" {
		lines = append(lines, "Mission ID: "+strings.TrimSpace(req.MissionID))
	}
	if strings.TrimSpace(req.TaskID) != "" {
		lines = append(lines, "Task ID: "+strings.TrimSpace(req.TaskID))
	}
	if strings.TrimSpace(req.ContextPack.MissionFinalGoal) != "" {
		lines = append(lines, strings.TrimSpace(req.ContextPack.MissionFinalGoal))
	}
	if strings.TrimSpace(req.ContextPack.WorkerPersonalGoal) != "" && req.ContextPack.WorkerPersonalGoal != req.ContextPack.MissionFinalGoal {
		lines = append(lines, "Worker goal: "+strings.TrimSpace(req.ContextPack.WorkerPersonalGoal))
	}
	if len(lines) == 0 {
		return strings.TrimSpace(req.TaskID)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func runtimeResponseDebugText(response agentcore.RuntimeResponse) string {
	var parts []string
	finalTextIndex := -1
	for i, message := range response.Messages {
		if message.Type == agentcore.RuntimeMessageText && message.Status == agentcore.RuntimeMessageStatusFinal && strings.TrimSpace(message.Content) != "" {
			finalTextIndex = i
		}
	}
	for i, message := range response.Messages {
		if finalTextIndex >= 0 && message.Type == agentcore.RuntimeMessageText && i != finalTextIndex {
			continue
		}
		if text := runtimeMessageDebugText(message); text != "" {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 && strings.TrimSpace(response.Error.Message) != "" {
		parts = append(parts, strings.TrimSpace(response.Error.Message))
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func runtimeMessageDebugText(message agentcore.RuntimeMessage) string {
	switch message.Type {
	case agentcore.RuntimeMessageThinking:
		if message.ReasoningKind != agentcore.ReasoningContentSummary {
			return ""
		}
		return strings.TrimSpace(message.Content)
	case agentcore.RuntimeMessageText, agentcore.RuntimeMessageStatus, agentcore.RuntimeMessageError, agentcore.RuntimeMessageLog:
		return strings.TrimSpace(message.Content)
	case agentcore.RuntimeMessageToolUse:
		lines := []string{"tool_use"}
		if strings.TrimSpace(message.Tool) != "" {
			lines[0] += " " + strings.TrimSpace(message.Tool)
		}
		if strings.TrimSpace(message.CallID) != "" {
			lines = append(lines, "call_id: "+strings.TrimSpace(message.CallID))
		}
		if len(message.Input) > 0 {
			if raw, err := json.MarshalIndent(message.Input, "", "  "); err == nil {
				lines = append(lines, string(raw))
			}
		}
		if strings.TrimSpace(message.Status) != "" {
			lines = append(lines, "status: "+strings.TrimSpace(message.Status))
		}
		return strings.TrimSpace(strings.Join(lines, "\n"))
	case agentcore.RuntimeMessageToolResult:
		lines := []string{"tool_result"}
		if strings.TrimSpace(message.Tool) != "" {
			lines[0] += " " + strings.TrimSpace(message.Tool)
		}
		if strings.TrimSpace(message.CallID) != "" {
			lines = append(lines, "call_id: "+strings.TrimSpace(message.CallID))
		}
		if strings.TrimSpace(message.Output) != "" {
			lines = append(lines, strings.TrimSpace(message.Output))
		}
		if strings.TrimSpace(message.Status) != "" {
			lines = append(lines, "status: "+strings.TrimSpace(message.Status))
		}
		return strings.TrimSpace(strings.Join(lines, "\n"))
	default:
		return strings.TrimSpace(message.Content)
	}
}

func (DiscussionAgentResultNormalizer) Normalize(input agentcore.AgentInput, response agentcore.RuntimeResponse, now time.Time) (agentcore.AgentResult, error) {
	summary := firstRuntimeText(response.Messages)
	return agentcore.AgentResult{
		ID:        agentcore.AgentResultID("result-" + input.RunID.String()),
		MissionID: input.ContextPack.MissionID,
		TaskID:    input.ContextPack.TaskID,
		AgentID:   input.ContextPack.AgentID,
		RunID:     input.RunID,
		Summary:   agentcore.Summary{Text: summary},
		CreatedAt: now,
	}, nil
}
