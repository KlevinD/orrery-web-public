package openclaw

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
	runtimecore "orrery_v2/internal/agent/runtime/core"
)

func (s *Session) runTurn(ctx context.Context, cancel context.CancelFunc, generation uint64, clientMsgID, prompt string, opts runtimecore.ExecOptions) {
	defer s.wg.Done()
	defer cancel()
	if clientMsgID != "" {
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventPromptAccepted, PromptAccepted: &agentcore.PromptAcceptedEvent{ClientMsgID: clientMsgID}})
	}
	legacy, err := s.backend.Execute(ctx, prompt, opts)
	if err != nil {
		s.completeTurn(generation, openClawFailureEvent(generation, ctx, err.Error()))
		return
	}
	if legacy == nil || legacy.Messages == nil || legacy.Result == nil {
		s.completeTurn(generation, openClawFailureEvent(generation, ctx, "openclaw runtime returned an incomplete session"))
		return
	}

	messageChannel := legacy.Messages
	resultChannel := legacy.Result
	var result *runtimecore.RuntimeResult
	var sawText bool
	toolOrdinal := uint64(0)
	for messageChannel != nil || resultChannel != nil {
		select {
		case message, ok := <-messageChannel:
			if !ok {
				messageChannel = nil
				continue
			}
			if message.Type == runtimecore.MessageTypeText && strings.TrimSpace(message.Content) != "" {
				sawText = true
			}
			if event, ok := openClawMessageEvent(generation, message, &toolOrdinal); ok {
				s.emit(generation, event)
			}
		case value, ok := <-resultChannel:
			if !ok {
				resultChannel = nil
				continue
			}
			copied := value
			result = &copied
			resultChannel = nil
		}
	}
	if result == nil {
		s.completeTurn(generation, openClawFailureEvent(generation, ctx, "openclaw runtime closed without a result"))
		return
	}
	if strings.TrimSpace(result.Output) != "" && !sawText {
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: fmt.Sprintf("openclaw-message-%d", generation), Text: result.Output}})
	}
	if sessionID := strings.TrimSpace(result.SessionID); sessionID != "" {
		s.mu.Lock()
		s.opts.ResumeSessionID = sessionID
		s.mu.Unlock()
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventBackendBound, BackendBound: &agentcore.BackendBoundEvent{BackendSessionID: sessionID}})
	}
	if model := strings.TrimSpace(result.Metadata["model"]); model != "" {
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventConfigChanged, ConfigChanged: &agentcore.ConfigChangedEvent{Model: model}})
	}
	if transport := strings.TrimSpace(result.Metadata["transport"]); transport != "" {
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventConfigChanged, ConfigChanged: &agentcore.ConfigChangedEvent{Options: map[string]string{"transport": transport}}})
	}
	if usage := openClawUsageDelta(result.Usage); usage != nil {
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventUsageDelta, UsageDelta: usage})
	}
	s.completeTurn(generation, openClawResultEvent(generation, ctx, *result))
}

func (s *Session) completeTurn(generation uint64, event agentcore.SessionEvent) {
	s.mu.Lock()
	if generation != s.generation || !s.active {
		s.mu.Unlock()
		return
	}
	s.active = false
	s.turnCancel = nil
	s.mu.Unlock()
	s.emit(generation, event)
}

func (s *Session) emit(generation uint64, event agentcore.SessionEvent) {
	if err := event.Validate(); err != nil {
		event = agentcore.SessionEvent{Kind: agentcore.EventAdapterSpecific, AdapterSpecific: &agentcore.AdapterSpecificEvent{Tag: "openclaw_invalid_mapping", Summary: "provider event was rejected by canonical validation"}}
	}
	projection := agentcore.EventProjectionCanonical
	if event.Kind == agentcore.EventAdapterSpecific {
		projection = agentcore.EventProjectionDiagnostic
	}
	envelope := agentcore.SessionEnvelope{
		SessionID: s.sessionID, TurnGeneration: generation, Sequence: s.sequence.Add(1), Timestamp: s.now().UTC(),
		Source: agentcore.EventSourceProvider, Projection: projection, Event: event,
	}
	select {
	case s.events <- envelope:
	case <-s.ctx.Done():
	}
}

func openClawPrompt(blocks []agentcore.ContentBlock) (string, error) {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Kind != agentcore.ContentBlockText {
			return "", agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeUnsupportedCommand, "openclaw only supports text prompt blocks", false)
		}
		if text := strings.TrimSpace(block.Text); text != "" {
			parts = append(parts, text)
		}
	}
	prompt := strings.Join(parts, "\n\n")
	if prompt == "" {
		return "", agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "openclaw prompt text is required", false)
	}
	return prompt, nil
}

func openClawMessageEvent(generation uint64, message runtimecore.RuntimeMessage, toolOrdinal *uint64) (agentcore.SessionEvent, bool) {
	switch message.Type {
	case runtimecore.MessageTypeText:
		if strings.TrimSpace(message.Content) == "" {
			return agentcore.SessionEvent{}, false
		}
		return agentcore.SessionEvent{Kind: agentcore.EventMessageDelta, MessageDelta: &agentcore.MessageDeltaEvent{ItemID: fmt.Sprintf("openclaw-message-%d", generation), Text: message.Content}}, true
	case runtimecore.MessageTypeThinking:
		if strings.TrimSpace(message.Content) == "" {
			return agentcore.SessionEvent{}, false
		}
		return agentcore.SessionEvent{Kind: agentcore.EventThoughtDelta, ThoughtDelta: &agentcore.ThoughtDeltaEvent{ItemID: fmt.Sprintf("openclaw-thought-%d", generation), Text: message.Content, ReasoningKind: agentcore.ReasoningContentRaw}}, true
	case runtimecore.MessageTypeToolUse:
		toolID := openClawToolID(generation, message.CallID, toolOrdinal)
		input, err := json.Marshal(message.Input)
		if err != nil {
			input = nil
		}
		return agentcore.SessionEvent{Kind: agentcore.EventToolCall, ToolCall: &agentcore.ToolCallEvent{ToolUseID: toolID, Name: firstNonEmpty(message.Tool, "tool"), Subagent: agentcore.SubagentInline, Input: input}}, true
	case runtimecore.MessageTypeToolResult:
		toolID := openClawToolID(generation, message.CallID, toolOrdinal)
		content := []agentcore.ToolResultContent(nil)
		if message.Output != "" {
			content = []agentcore.ToolResultContent{{Kind: agentcore.ToolResultText, Text: message.Output}}
		}
		return agentcore.SessionEvent{Kind: agentcore.EventToolResult, ToolResult: &agentcore.ToolResultEvent{ToolUseID: toolID, IsError: strings.EqualFold(message.Status, "failed"), Content: content}}, true
	case runtimecore.MessageTypePlan:
		if message.Plan == nil || len(message.Plan.Items) == 0 {
			return agentcore.SessionEvent{}, false
		}
		entries := make([]agentcore.PlanEntry, 0, len(message.Plan.Items))
		for _, item := range message.Plan.Items {
			if text := strings.TrimSpace(item.Text); text != "" {
				entries = append(entries, agentcore.PlanEntry{Content: text, Status: openClawPlanStatus(item.Status)})
			}
		}
		if len(entries) == 0 {
			return agentcore.SessionEvent{}, false
		}
		return agentcore.SessionEvent{Kind: agentcore.EventPlan, Plan: &agentcore.PlanEvent{Entries: entries}}, true
	case runtimecore.MessageTypeError:
		if strings.TrimSpace(message.Content) == "" {
			return agentcore.SessionEvent{}, false
		}
		return agentcore.SessionEvent{Kind: agentcore.EventNotice, Notice: &agentcore.NoticeEvent{Level: agentcore.NoticeWarning, Message: message.Content}}, true
	case runtimecore.MessageTypeStatus, runtimecore.MessageTypeLog:
		content := firstNonEmpty(message.Content, message.Status)
		if content == "" {
			return agentcore.SessionEvent{}, false
		}
		return agentcore.SessionEvent{Kind: agentcore.EventNotice, Notice: &agentcore.NoticeEvent{Level: agentcore.NoticeInfo, Message: content}}, true
	default:
		return agentcore.SessionEvent{}, false
	}
}

func openClawToolID(generation uint64, value string, ordinal *uint64) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	(*ordinal)++
	return fmt.Sprintf("openclaw-tool-%d-%d", generation, *ordinal)
}

func openClawPlanStatus(value string) agentcore.PlanStatus {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "completed", "complete", "done":
		return agentcore.PlanCompleted
	case "pending", "todo", "not_started":
		return agentcore.PlanPending
	default:
		return agentcore.PlanInProgress
	}
}

func openClawUsageDelta(usage map[string]agentcore.TokenUsage) *agentcore.UsageDeltaEvent {
	var input, output, cacheRead, cacheWrite uint64
	for _, value := range usage {
		input += openClawUint(value.InputTokens)
		output += openClawUint(value.OutputTokens)
		cacheRead += openClawUint(value.CacheReadTokens)
		cacheWrite += openClawUint(value.CacheWriteTokens)
	}
	total := input + output + cacheRead + cacheWrite
	if total == 0 {
		return nil
	}
	return &agentcore.UsageDeltaEvent{InputTokens: input, OutputTokens: output, TotalTokens: total}
}

func openClawUint(value int64) uint64 {
	if value < 1 {
		return 0
	}
	return uint64(value)
}

func openClawResultEvent(generation uint64, ctx context.Context, result runtimecore.RuntimeResult) agentcore.SessionEvent {
	if err := ctx.Err(); err != nil {
		return openClawFailureEvent(generation, ctx, firstNonEmpty(result.Error, "openclaw execution cancelled"))
	}
	text := firstNonEmpty(result.Output, result.Error)
	switch result.Status {
	case runtimecore.ResultStatusCompleted:
		return agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{Epoch: generation, ResultText: text, Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeEndTurn}}}
	case runtimecore.ResultStatusCancelled, runtimecore.ResultStatusAborted:
		return agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{Epoch: generation, IsError: true, ResultText: firstNonEmpty(text, "openclaw execution cancelled"), Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCancelled, CancelReason: agentcore.CancelReasonUserCancel}}}
	default:
		return agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{Epoch: generation, IsError: true, ResultText: firstNonEmpty(text, "openclaw execution failed"), Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}}}
	}
}

func openClawFailureEvent(generation uint64, ctx context.Context, message string) agentcore.SessionEvent {
	if ctx != nil && ctx.Err() != nil {
		return agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{Epoch: generation, IsError: true, ResultText: firstNonEmpty(message, "openclaw execution cancelled"), Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCancelled, CancelReason: agentcore.CancelReasonUserCancel}}}
	}
	return agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{Epoch: generation, IsError: true, ResultText: firstNonEmpty(message, "openclaw execution failed"), Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}}}
}

func cloneOpenClawConfig(source Config) Config {
	out := source
	out.Env = cloneOpenClawEnvironment(source.Env)
	return out
}

func cloneOpenClawEnvironment(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	out := make(map[string]string, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}

func cloneOpenClawExecOptions(source runtimecore.ExecOptions) runtimecore.ExecOptions {
	out := source
	out.ExtraArgs = append([]string(nil), source.ExtraArgs...)
	out.CustomArgs = append([]string(nil), source.CustomArgs...)
	out.MCPConfig = append([]byte(nil), source.MCPConfig...)
	out.ProviderConfig = append([]byte(nil), source.ProviderConfig...)
	out.ProviderSpecific = cloneOpenClawEnvironment(source.ProviderSpecific)
	out.Environment = cloneOpenClawEnvironment(source.Environment)
	out.ToolBindings = append([]agentcore.RuntimeToolBinding(nil), source.ToolBindings...)
	out.SkillBindings = append([]agentcore.RuntimeSkillBinding(nil), source.SkillBindings...)
	out.SandboxProfile = source.SandboxProfile.Clone()
	out.WorkspacePolicy.WritableRoots = append([]string(nil), source.WorkspacePolicy.WritableRoots...)
	out.WorkspacePolicy.Profile = source.WorkspacePolicy.Profile.Clone()
	return out
}
