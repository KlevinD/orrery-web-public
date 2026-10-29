package session

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
)

// ResponseProjection derives the application response from the ordered
// canonical stream. It never feeds state back into the Orchestrator.
type ResponseProjection struct {
	runID      agentcore.AgentRunID
	runtime    agentcore.RuntimeKind
	model      string
	startedAt  time.Time
	completed  bool
	response   agentcore.RuntimeResponse
	toolNames  map[string]string
	lastPlanID uint64
	lastFold   []agentcore.RuntimeMessage
}

func NewResponseProjection(request agentcore.RuntimeRequest, kind agentcore.RuntimeKind, startedAt time.Time) *ResponseProjection {
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	return &ResponseProjection{
		runID:     request.RunID,
		runtime:   kind,
		model:     strings.TrimSpace(request.RuntimeOptions.Model),
		startedAt: startedAt.UTC(),
		response: agentcore.RuntimeResponse{
			RunID:            request.RunID,
			ProviderMetadata: runtimeContinuationMetadata(request),
		},
		toolNames: make(map[string]string),
	}
}

func runtimeContinuationMetadata(request agentcore.RuntimeRequest) map[string]string {
	metadata := make(map[string]string)
	// Only server-owned continuation and context diagnostics cross this boundary.
	// Provider settings can contain credentials and must never be copied wholesale.
	for _, key := range []string{
		"resolved_llm_profile_id", "resolved_llm_model", "llm_selection_source",
		"discussion_context_fingerprint", "discussion_persona_revision",
		"discussion_history_mode", "discussion_history_since_run_id",
		"discussion_history_since_completed_at",
		agentcore.RuntimeContextTokenLimitProviderKey, agentcore.RuntimeContextTruncatedProviderKey,
	} {
		if value := strings.TrimSpace(request.RuntimeOptions.ProviderSpecific[key]); value != "" {
			metadata[key] = value
		}
	}
	return metadata
}

func (p *ResponseProjection) bindBackendSessionID(sessionID string) {
	if p == nil {
		return
	}
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		p.response.ProviderMetadata["session_id"] = sessionID
	}
}

func (p *ResponseProjection) Fold(envelope agentcore.SessionEnvelope) error {
	if p == nil {
		return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "response projection is nil", false)
	}
	p.lastFold = nil
	if envelope.Event.Validate() != nil {
		return envelope.Event.Validate()
	}
	event := envelope.Event
	sessionID := string(envelope.SessionID)
	switch event.Kind {
	case agentcore.EventMessageDelta:
		p.appendMessage(agentcore.RuntimeMessage{Type: agentcore.RuntimeMessageText, Content: event.MessageDelta.Text, CallID: event.MessageDelta.ItemID, SessionID: sessionID})
	case agentcore.EventThoughtDelta:
		p.appendMessage(agentcore.RuntimeMessage{Type: agentcore.RuntimeMessageThinking, Content: event.ThoughtDelta.Text, ReasoningKind: event.ThoughtDelta.ReasoningKind, CallID: event.ThoughtDelta.ItemID, SessionID: sessionID})
	case agentcore.EventMessageFinalized:
		message := event.MessageFinalized.Message
		kind := agentcore.RuntimeMessageText
		if message.Kind == agentcore.ItemThinking {
			kind = agentcore.RuntimeMessageThinking
		}
		p.appendMessage(agentcore.RuntimeMessage{Type: kind, Content: message.Content, ReasoningKind: message.ReasoningKind, CallID: message.ItemID, Status: agentcore.RuntimeMessageStatusFinal, SessionID: sessionID})
	case agentcore.EventToolCall:
		input := map[string]any(nil)
		if len(event.ToolCall.Input) != 0 {
			if err := json.Unmarshal(event.ToolCall.Input, &input); err != nil {
				return agentcore.NewRuntimeBridgeError(agentcore.RuntimeBridgeInvalidPayload, "canonical tool input is invalid", false)
			}
		}
		p.toolNames[event.ToolCall.ToolUseID] = event.ToolCall.Name
		p.appendMessage(agentcore.RuntimeMessage{Type: agentcore.RuntimeMessageToolUse, Tool: event.ToolCall.Name, CallID: event.ToolCall.ToolUseID, Input: input, Status: "running", SessionID: sessionID})
		p.response.ToolCalls = append(p.response.ToolCalls, agentcore.RuntimeToolCall{ID: event.ToolCall.ToolUseID, Name: event.ToolCall.Name, Input: cloneAnyMap(input)})
	case agentcore.EventToolOutputDelta:
		p.appendMessage(agentcore.RuntimeMessage{Type: agentcore.RuntimeMessageLog, CallID: event.ToolOutputDelta.ItemID, Content: event.ToolOutputDelta.Text, SessionID: sessionID})
	case agentcore.EventToolResult:
		output := canonicalToolResultText(event.ToolResult.Content)
		status := "completed"
		if event.ToolResult.IsError {
			status = "failed"
		}
		name := p.toolNames[event.ToolResult.ToolUseID]
		p.appendMessage(agentcore.RuntimeMessage{Type: agentcore.RuntimeMessageToolResult, Tool: name, CallID: event.ToolResult.ToolUseID, Output: output, Status: status, SessionID: sessionID})
		for index := range p.response.ToolCalls {
			if p.response.ToolCalls[index].ID == event.ToolResult.ToolUseID {
				p.response.ToolCalls[index].Output = output
				break
			}
		}
	case agentcore.EventPlan:
		p.lastPlanID++
		items := make([]agentcore.RuntimePlanItem, 0, len(event.Plan.Entries))
		complete := len(event.Plan.Entries) != 0
		for index, entry := range event.Plan.Entries {
			items = append(items, agentcore.RuntimePlanItem{ID: fmt.Sprintf("step-%d", index+1), Text: entry.Content, Status: string(entry.Status)})
			complete = complete && entry.Status == agentcore.PlanCompleted
		}
		status := "in_progress"
		if complete {
			status = "completed"
		}
		p.appendMessage(agentcore.RuntimeMessage{Type: agentcore.RuntimeMessagePlan, SessionID: sessionID, Plan: &agentcore.RuntimePlanState{PlanID: fmt.Sprintf("plan-%d", p.lastPlanID), Status: status, Items: items}})
	case agentcore.EventNotice:
		p.appendMessage(agentcore.RuntimeMessage{Type: agentcore.RuntimeMessageLog, Content: event.Notice.Message, Level: string(event.Notice.Level), SessionID: sessionID})
	case agentcore.EventProvisioning:
		content := string(event.Provisioning.Phase)
		if event.Provisioning.Reason != "" {
			content += ": " + event.Provisioning.Reason
		}
		p.appendMessage(agentcore.RuntimeMessage{Type: agentcore.RuntimeMessageStatus, Content: content, SessionID: sessionID})
	case agentcore.EventUsageDelta:
		model := p.model
		if model == "" {
			model = string(p.runtime)
		}
		if p.response.Usage == nil {
			p.response.Usage = make(map[string]agentcore.TokenUsage)
		}
		usage, seen := p.response.Usage[model]
		cachedKnown := event.UsageDelta.CachedInputTokens != nil
		reasoningKnown := event.UsageDelta.ReasoningTokens != nil
		usage.CachedInputTokensKnown = cachedKnown && (!seen || usage.CachedInputTokensKnown)
		usage.ReasoningTokensKnown = reasoningKnown && (!seen || usage.ReasoningTokensKnown)
		if cachedKnown {
			usage.CachedInputTokens += clampUint64ToInt64(*event.UsageDelta.CachedInputTokens)
		}
		if reasoningKnown {
			usage.ReasoningTokens += clampUint64ToInt64(*event.UsageDelta.ReasoningTokens)
		}
		input := clampUint64ToInt64(event.UsageDelta.InputTokens)
		output := clampUint64ToInt64(event.UsageDelta.OutputTokens)
		usage.InputTokens += input
		usage.OutputTokens += output
		accounted := event.UsageDelta.InputTokens + event.UsageDelta.OutputTokens
		if event.UsageDelta.TotalTokens > accounted {
			// The canonical reference exposes cache-inclusive total usage but not
			// separate cache buckets. Preserve the billable total conservatively.
			usage.CacheReadTokens += clampUint64ToInt64(event.UsageDelta.TotalTokens - accounted)
		}
		p.response.Usage[model] = usage
	case agentcore.EventBackendBound:
		if value := strings.TrimSpace(event.BackendBound.BackendSessionID); value != "" {
			p.response.ProviderMetadata["session_id"] = value
		}
	case agentcore.EventConfigChanged:
		if event.ConfigChanged.Model != "" {
			p.model = event.ConfigChanged.Model
			p.response.ProviderMetadata["model"] = event.ConfigChanged.Model
		}
		if event.ConfigChanged.Mode != "" {
			p.response.ProviderMetadata["mode"] = event.ConfigChanged.Mode
		}
		if transport := strings.TrimSpace(event.ConfigChanged.Options["transport"]); transport != "" {
			p.response.ProviderMetadata["transport"] = transport
		}
	case agentcore.EventTurnResult:
		p.foldTerminal(envelope.Timestamp, *event.TurnResult)
	case agentcore.EventDetached:
		if !p.completed {
			message := strings.TrimSpace(event.Detached.RedactedSummary)
			if message == "" {
				message = "runtime transport detached before a terminal result"
			}
			p.finish(envelope.Timestamp, agentcore.RuntimeStatusFailed, "runtime_detached", message, true)
		}
	}
	return nil
}

func (p *ResponseProjection) Response(now time.Time) agentcore.RuntimeResponse {
	if p == nil {
		return agentcore.RuntimeResponse{}
	}
	out := cloneRuntimeResponse(p.response)
	out.Messages = consolidateFinalizedMessages(out.Messages)
	if out.CompletedAt.IsZero() && p.completed {
		out.CompletedAt = now.UTC()
	}
	return out
}

// LastFoldMessages returns only the incremental application messages produced by
// the most recent canonical event. It is used by live consumers, while
// Response returns the consolidated terminal projection.
func (p *ResponseProjection) LastFoldMessages() []agentcore.RuntimeMessage {
	if p == nil {
		return nil
	}
	return cloneProjectedMessages(p.lastFold)
}

// IncrementalMessages preserves event-by-event projection cardinality for
// replaying the same progress stream observed by a live subscriber.
func (p *ResponseProjection) IncrementalMessages() []agentcore.RuntimeMessage {
	if p == nil {
		return nil
	}
	return cloneProjectedMessages(p.response.Messages)
}

func (p *ResponseProjection) Terminal() bool { return p != nil && p.completed }

func (p *ResponseProjection) foldTerminal(at time.Time, result agentcore.TurnResultEvent) {
	if p.completed {
		return
	}
	status := agentcore.RuntimeStatusCompleted
	code := ""
	message := ""
	retryable := false
	switch result.Outcome.Kind {
	case agentcore.TurnOutcomeCancelled:
		status = agentcore.RuntimeStatusCancelled
		code = "runtime_cancelled"
		message = firstNonBlank(result.ResultText, "runtime turn cancelled")
	case agentcore.TurnOutcomeFailed:
		status = agentcore.RuntimeStatusFailed
		code = "runtime_failed"
		message = firstNonBlank(result.ResultText, "runtime turn failed")
		if result.APIErrorStatus != nil {
			retryable = *result.APIErrorStatus == 408 || *result.APIErrorStatus == 429 || *result.APIErrorStatus >= 500
		}
	default:
		if result.IsError {
			status = agentcore.RuntimeStatusFailed
			code = "runtime_failed"
			message = firstNonBlank(result.ResultText, "runtime turn failed")
		}
	}
	if result.ResultText != "" && !runtimeMessagesContainText(p.response.Messages) && status == agentcore.RuntimeStatusCompleted {
		p.appendMessage(agentcore.RuntimeMessage{Type: agentcore.RuntimeMessageText, Content: result.ResultText, Status: agentcore.RuntimeMessageStatusFinal})
	}
	p.finish(at, status, code, message, retryable)
}

func (p *ResponseProjection) appendMessage(message agentcore.RuntimeMessage) {
	p.response.Messages = append(p.response.Messages, message)
	p.lastFold = append(p.lastFold, message)
}

func consolidateFinalizedMessages(messages []agentcore.RuntimeMessage) []agentcore.RuntimeMessage {
	finalized := make(map[string]struct{})
	for _, message := range messages {
		if (message.Type == agentcore.RuntimeMessageText || message.Type == agentcore.RuntimeMessageThinking) && message.Status == agentcore.RuntimeMessageStatusFinal && strings.TrimSpace(message.CallID) != "" {
			finalized[message.CallID] = struct{}{}
		}
	}
	if len(finalized) == 0 {
		return messages
	}
	out := make([]agentcore.RuntimeMessage, 0, len(messages))
	for _, message := range messages {
		if (message.Type == agentcore.RuntimeMessageText || message.Type == agentcore.RuntimeMessageThinking) && message.Status != agentcore.RuntimeMessageStatusFinal {
			if _, ok := finalized[message.CallID]; ok {
				continue
			}
		}
		out = append(out, message)
	}
	return out
}

func cloneProjectedMessages(messages []agentcore.RuntimeMessage) []agentcore.RuntimeMessage {
	out := append([]agentcore.RuntimeMessage(nil), messages...)
	for index := range out {
		out[index].Input = cloneAnyMap(messages[index].Input)
		if messages[index].Plan != nil {
			plan := *messages[index].Plan
			plan.Items = append([]agentcore.RuntimePlanItem(nil), messages[index].Plan.Items...)
			out[index].Plan = &plan
		}
	}
	return out
}

func (p *ResponseProjection) finish(at time.Time, status agentcore.RuntimeStatus, code, message string, retryable bool) {
	p.completed = true
	p.response.Status = status
	// Codex owns the tool loop. Empty output (or a declined patch) is still a
	// consumed result, so the application must not replay its tools afterward.
	if p.runtime == agentcore.RuntimeKindCodex && status == agentcore.RuntimeStatusCompleted {
		p.response.ProviderMetadata["tool_loop_completed"] = "true"
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	p.response.CompletedAt = at.UTC()
	p.response.DurationMs = p.response.CompletedAt.Sub(p.startedAt).Milliseconds()
	if p.response.DurationMs < 0 {
		p.response.DurationMs = 0
	}
	if code != "" || message != "" {
		p.response.Error = agentcore.RuntimeFailure{Code: code, Message: message, Retryable: retryable}
	}
}

func canonicalToolResultText(content []agentcore.ToolResultContent) string {
	parts := make([]string, 0, len(content))
	for _, item := range content {
		switch item.Kind {
		case agentcore.ToolResultText:
			if item.Text != "" {
				parts = append(parts, item.Text)
			}
		case agentcore.ToolResultFilePath:
			if item.Path != "" {
				parts = append(parts, item.Path)
			}
		case agentcore.ToolResultImage:
			parts = append(parts, "[image "+item.MediaType+"]")
		}
	}
	return strings.Join(parts, "\n")
}

func clampUint64ToInt64(value uint64) int64 {
	const maxInt64 = uint64(^uint64(0) >> 1)
	if value > maxInt64 {
		return int64(maxInt64)
	}
	return int64(value)
}

func cloneRuntimeResponse(response agentcore.RuntimeResponse) agentcore.RuntimeResponse {
	out := response
	out.Messages = append([]agentcore.RuntimeMessage(nil), response.Messages...)
	for index := range out.Messages {
		out.Messages[index].Input = cloneAnyMap(response.Messages[index].Input)
		if response.Messages[index].Plan != nil {
			plan := *response.Messages[index].Plan
			plan.Items = append([]agentcore.RuntimePlanItem(nil), response.Messages[index].Plan.Items...)
			out.Messages[index].Plan = &plan
		}
	}
	out.ToolCalls = append([]agentcore.RuntimeToolCall(nil), response.ToolCalls...)
	for index := range out.ToolCalls {
		out.ToolCalls[index].Input = cloneAnyMap(response.ToolCalls[index].Input)
	}
	out.Artifacts = append([]agentcore.ArtifactRef(nil), response.Artifacts...)
	out.Usage = make(map[string]agentcore.TokenUsage, len(response.Usage))
	for model, usage := range response.Usage {
		out.Usage[model] = usage
	}
	out.ProviderMetadata = cloneStringMap(response.ProviderMetadata)
	out.OutboundMessages = append([]agentcore.RuntimeChatMessage(nil), response.OutboundMessages...)
	return out
}

func runtimeMessagesContainText(messages []agentcore.RuntimeMessage) bool {
	for _, message := range messages {
		if message.Type == agentcore.RuntimeMessageText && strings.TrimSpace(message.Content) != "" {
			return true
		}
	}
	return false
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
