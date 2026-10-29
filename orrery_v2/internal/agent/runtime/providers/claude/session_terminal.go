package claude

import (
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

func (s *NativeSession) mapClaudeResult(frame map[string]any) {
	generation := s.claimClaudeResultGeneration()
	isError, _ := frame["is_error"].(bool)
	resultText := claudeStringRaw(frame, "result")
	if resultText == "" {
		errors := make([]string, 0)
		if values, ok := frame["errors"].([]any); ok {
			for _, raw := range values {
				value, _ := raw.(string)
				value = strings.TrimSpace(value)
				if value == "" || strings.HasPrefix(value, "[ede_diagnostic]") {
					continue
				}
				errors = append(errors, value)
			}
		}
		resultText = strings.Join(errors, "; ")
		if resultText == "" && isError {
			resultText = claudeString(frame, "subtype")
		}
	}

	if usage := claudeMap(frame, "usage"); len(usage) != 0 {
		input, _ := claudeUint(usage["input_tokens"])
		output, _ := claudeUint(usage["output_tokens"])
		cacheCreation, _ := claudeUint(usage["cache_creation_input_tokens"])
		cacheRead, _ := claudeUint(usage["cache_read_input_tokens"])
		total, ok := claudeUint(usage["total_tokens"])
		if !ok {
			total = input + output + cacheCreation + cacheRead
		}
		var cost *float64
		if value, ok := claudeFloat(frame["total_cost_usd"]); ok && value >= 0 {
			cost = &value
		}
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventUsageDelta, UsageDelta: &agentcore.UsageDeltaEvent{
			InputTokens: input, OutputTokens: output, TotalTokens: total, CostUSD: cost,
		}})
	}
	result := claudeTurnEvent(generation, isError, resultText, claudeAPIStatus(frame), claudeString(frame, "terminal_reason", "stop_reason"))
	s.completeClaudeTurn(generation, result)
}

func (s *NativeSession) claimClaudeResultGeneration() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.resultQueue) != 0 {
		generation := s.resultQueue[0]
		s.resultQueue = s.resultQueue[1:]
		return generation
	}
	return s.claudeEventGenerationLocked()
}

func (s *NativeSession) completeClaudeTurn(generation uint64, event agentcore.SessionEvent) {
	if generation == 0 {
		return
	}
	s.mu.Lock()
	if s.terminated[generation] {
		s.mu.Unlock()
		return
	}
	s.terminated[generation] = true
	delete(s.active, generation)
	if s.turnOpen == generation {
		s.turnOpen = 0
	}
	s.mu.Unlock()
	s.emit(generation, event)
}

func claudeTurnEvent(generation uint64, isError bool, resultText string, status *uint16, reason string) agentcore.SessionEvent {
	outcome := claudeTurnOutcome(reason)
	return agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{
		IsError: isError, APIErrorStatus: status, ResultText: claudeCanonicalText(resultText, 1024*1024), Epoch: generation, Outcome: outcome,
	}}
}

func claudeFailedTurn(generation uint64, message string) agentcore.SessionEvent {
	return agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{
		IsError: true, ResultText: claudeDiagnosticText(message, "claude turn failed"), Epoch: generation,
		Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed},
	}}
}

func claudeTurnOutcome(reason string) agentcore.TurnOutcome {
	switch strings.TrimSpace(reason) {
	case "cancelled", "canceled", "aborted_streaming", "aborted_tools":
		return agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCancelled, CancelReason: agentcore.CancelReasonUserCancel}
	case "max_tokens", "prompt_too_long":
		return agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCompleted, StopReason: &agentcore.StopReason{Kind: agentcore.StopReasonTruncated, Truncation: agentcore.TruncationMaxTokens}}
	case "max_turns":
		return agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCompleted, StopReason: &agentcore.StopReason{Kind: agentcore.StopReasonTruncated, Truncation: agentcore.TruncationMaxTurns}}
	case "refusal":
		return agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCompleted, StopReason: &agentcore.StopReason{Kind: agentcore.StopReasonRefused}}
	default:
		return agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCompleted, StopReason: &agentcore.StopReason{Kind: agentcore.StopReasonEndTurn}}
	}
}

func claudeAPIStatus(frame map[string]any) *uint16 {
	value, ok := claudeUint(frame["api_error_status"])
	if !ok || value < 100 || value > 599 {
		return nil
	}
	status := uint16(value)
	return &status
}

func claudeFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case float64:
		return typed, true
	case interface{ Float64() (float64, error) }:
		parsed, err := typed.Float64()
		return parsed, err == nil
	default:
		return 0, false
	}
}
