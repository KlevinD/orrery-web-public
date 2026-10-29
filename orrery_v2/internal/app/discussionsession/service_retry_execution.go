package discussionsession

import (
	"context"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *Service) capturePromptDebug(ctx context.Context, conversationID string, run *RunRecord, request DiscussionAgentRequest) (err error) {
	debugger, ok := s.discussionAgent.(DiscussionAgentPromptDebugger)
	if !ok || run == nil {
		return nil
	}
	finishStage := s.startDiscussionStage(ctx, conversationID, run.TurnID, run.RunID, "prompt_debug_save")
	defer func() { finishStage(err) }()
	raw := debugger.PromptDebug(request)
	if raw == nil {
		return nil
	}
	debug := RunDebugDTO{
		ConversationID: conversationID,
		RunID:          run.RunID,
		OutboundPrompt: raw.OutboundPrompt,
		Selected:       raw.Selected,
		CreatedAt:      s.now(),
		Redacted:       false,
	}
	run.Debug = &debug
	return s.repo.SaveRun(ctx, *run)
}

func (s *Service) applyResponseDebug(run *RunRecord, response DiscussionAgentResponse) {
	if run == nil {
		return
	}
	applyDiscussionResponseOutputPolicy(&response)
	*run = normalizeRunDebug(*run)
	if strings.TrimSpace(response.OutboundPrompt) != "" {
		run.Debug.OutboundPrompt = response.OutboundPrompt
	}
	if strings.TrimSpace(response.RuntimeResponse) != "" {
		run.Debug.RuntimeResponse = response.RuntimeResponse
	}
	if len(response.ProviderMetadata) > 0 {
		run.Debug.ProviderMetadata = cloneStringMap(response.ProviderMetadata)
	}
	debug := *run.Debug
	if traceRef := strings.TrimSpace(response.TraceRef); traceRef != "" {
		run.TraceRef = traceRef
		run.TraceVersion = 2
		run.TraceStatus = "hot"
		debug.OutboundPrompt = sanitizeRunDebugText(debug.OutboundPrompt)
		debug.RuntimeResponse = sanitizeRunDebugText(debug.RuntimeResponse)
		debug.ProviderMetadata = sanitizeRunDebugMetadata(debug.ProviderMetadata)
		debug.Redacted = true
	} else {
		debug.Redacted = false
	}
	run.Debug = &debug
}

func sanitizeRunDebugText(value string) string {
	sanitized, ok := agentcore.SanitizeTraceValue(value, agentcore.TraceSanitizerOptions{}).(string)
	if !ok {
		return "[REDACTED]"
	}
	return sanitized
}

func sanitizeRunDebugMetadata(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	sanitized := agentcore.SanitizeTraceValue(values, agentcore.TraceSanitizerOptions{})
	items, ok := sanitized.(map[string]any)
	if !ok {
		return map[string]string{"redacted": "[REDACTED]"}
	}
	out := make(map[string]string, len(items))
	for key, value := range items {
		text, ok := value.(string)
		if !ok {
			text = "[REDACTED]"
		}
		out[key] = text
	}
	return out
}
