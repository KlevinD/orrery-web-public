package codex

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

var nativeHTTPStatusPattern = regexp.MustCompile(`(?i)(?:status|http)[^0-9]{0,12}([1-5][0-9]{2})`)

func (s *NativeSession) completeNativeTurn(params map[string]any) {
	turn := nativeMap(params, "turn")
	turnID := nativeString(turn, "id")
	s.mu.Lock()
	generation := s.turnGeneration
	if turnID != "" {
		if mapped := s.turnGenerations[turnID]; mapped != 0 {
			generation = mapped
		} else {
			s.turnGenerations[turnID] = generation
		}
	}
	if generation == 0 || s.terminated[generation] {
		s.mu.Unlock()
		return
	}
	s.terminated[generation] = true
	delete(s.idlePending, generation)
	if turnID == "" || turnID == s.activeTurnID || generation == s.turnGeneration {
		s.turnInFlight = false
		s.activeTurnID = ""
	}
	s.mu.Unlock()
	s.emit(generation, nativeTurnResult(generation, turn))
}

func (s *NativeSession) markNativeIdle() {
	s.mu.Lock()
	if s.turnInFlight && s.turnGeneration != 0 && !s.terminated[s.turnGeneration] {
		s.idlePending[s.turnGeneration] = true
	}
	s.mu.Unlock()
}

func (s *NativeSession) failActiveTurn(message string, status *uint16) {
	s.mu.Lock()
	generation := s.turnGeneration
	if generation == 0 || !s.turnInFlight || s.terminated[generation] {
		s.mu.Unlock()
		return
	}
	s.terminated[generation] = true
	delete(s.idlePending, generation)
	s.turnInFlight = false
	s.activeTurnID = ""
	s.mu.Unlock()
	s.emit(generation, failedTurnResult(generation, nativeDiagnosticText(message, "codex turn failed"), status))
}

func (s *NativeSession) flushPendingIdleTerminal() {
	s.mu.Lock()
	generation := s.turnGeneration
	idle := s.idlePending[generation]
	active := s.turnInFlight
	if generation == 0 || s.terminated[generation] || (!idle && !active) {
		s.mu.Unlock()
		return
	}
	s.terminated[generation] = true
	delete(s.idlePending, generation)
	s.turnInFlight = false
	s.activeTurnID = ""
	s.mu.Unlock()
	if idle {
		s.emit(generation, agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{
			IsError: false, Epoch: generation,
			Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCompleted, StopReason: &agentcore.StopReason{Kind: agentcore.StopReasonEndTurn}},
		}})
		return
	}
	s.emit(generation, failedTurnResult(generation, "codex process exited before turn completion", nil))
}

func nativeTurnResult(generation uint64, turn map[string]any) agentcore.SessionEvent {
	status := strings.ToLower(nativeString(turn, "status"))
	errorObject := nativeMap(turn, "error")
	message := nativeDiagnosticText(nativeString(errorObject, "message"), "")
	httpStatus := nativeHTTPStatus(errorObject, message)
	result := &agentcore.TurnResultEvent{Epoch: generation, ResultText: message, APIErrorStatus: httpStatus}
	switch status {
	case "interrupted", "cancelled", "canceled":
		result.IsError = false
		result.Outcome = agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCancelled, CancelReason: agentcore.CancelReasonUserCancel}
	case "failed":
		result.IsError = true
		result.Outcome = agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed}
	default:
		result.IsError = false
		result.Outcome = agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeCompleted, StopReason: &agentcore.StopReason{Kind: agentcore.StopReasonEndTurn}}
	}
	return agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: result}
}

func failedTurnResult(generation uint64, message string, status *uint16) agentcore.SessionEvent {
	return agentcore.SessionEvent{Kind: agentcore.EventTurnResult, TurnResult: &agentcore.TurnResultEvent{
		IsError: true, APIErrorStatus: status, ResultText: nativeDiagnosticText(message, "codex turn failed"), Epoch: generation,
		Outcome: agentcore.TurnOutcome{Kind: agentcore.TurnOutcomeFailed},
	}}
}

func nativeHTTPStatus(errorObject map[string]any, message string) *uint16 {
	if value, ok := nativeNumber(errorObject["httpStatusCode"]); ok && value >= 100 && value <= 599 {
		status := uint16(value)
		return &status
	}
	if info := nativeMap(errorObject, "codexErrorInfo"); info != nil {
		for _, variant := range info {
			if object, ok := variant.(map[string]any); ok {
				if value, ok := nativeNumber(object["httpStatusCode"]); ok && value >= 100 && value <= 599 {
					status := uint16(value)
					return &status
				}
			}
		}
	}
	match := nativeHTTPStatusPattern.FindStringSubmatch(message)
	if len(match) == 2 {
		if value, err := strconv.Atoi(match[1]); err == nil {
			status := uint16(value)
			return &status
		}
	}
	return nil
}

func nativeNumber(value any) (uint64, bool) {
	switch typed := value.(type) {
	case float64:
		if typed >= 0 && typed == float64(uint64(typed)) {
			return uint64(typed), true
		}
	case json.Number:
		value, err := strconv.ParseUint(string(typed), 10, 64)
		return value, err == nil
	}
	return 0, false
}
