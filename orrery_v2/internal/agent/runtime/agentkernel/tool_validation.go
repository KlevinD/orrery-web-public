package agentkernel

import (
	"context"
	"encoding/json"
	"orrery_v2/internal/agent/llm"

	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/runtime/providers/kernelcompat"
)

const toolValidationStopReview = "Argument correction review: a tool call was rejected before dispatch and has not yet been corrected. No operation in that rejected batch ran. Read its error and the supplied tool schema, correct the arguments with a new call ID, and continue the authorized task. Missing outputs from that rejected call are not proof of an external dependency failure. Do not repeat completed operations or change permissions, approval requirements, or the deadline. If the operation is optional, use an authorized alternative; if a real blocker remains, explain the specific evidence accurately. This review is bounded and uses the existing model budget."

func validationTurnMessages(messages []*llm.Message) []*llm.Message {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.User && messages[i].Content != toolValidationStopReview && messages[i].Content != blockedTaskStopReview && messages[i].Content != blockedResultStopReview {
			return messages[i+1:]
		}
	}
	return messages
}

// Invalid argument values have no side effects and can be corrected by the
// llm. Binding, identity, policy and uncertain execution errors cannot.
type toolValidationFailure struct {
	callID, code     string
	cause            error
	validationReason string
}

func newToolValidationFailure(callID, code string) error {
	return &toolValidationFailure{callID: callID, code: code, cause: kernelcompat.NewExecutionError(code, ErrProtocol)}
}

func (e *toolValidationFailure) Error() string { return e.cause.Error() }
func (e *toolValidationFailure) Unwrap() error { return e.cause }

// Only local pre-dispatch validation failures qualify. A later admitted call
// to the same tool proves argument correction, even if execution then reports
// a business failure. Unrelated bookkeeping cannot silently clear the error.
func hasUncorrectedToolValidation(messages []*llm.Message) bool {
	names := map[string]string{}
	pending := map[string]bool{}
	for _, message := range validationTurnMessages(messages) {
		for _, call := range message.ToolCalls {
			names[call.ID] = call.Function.Name
		}
		if message.Role != llm.ToolRole || names[message.ToolCallID] == "" {
			continue
		}
		var result struct {
			Code      string `json:"error_code"`
			Execution string `json:"execution_status"`
		}
		_ = json.Unmarshal([]byte(message.Content), &result)
		name := names[message.ToolCallID]
		if result.Execution == "not_dispatched" {
			if result.Code == "tool_arguments_invalid" || result.Code == "tool_schema_invalid" {
				pending[name] = true
			}
			continue
		}
		delete(pending, name)
	}
	return len(pending) != 0
}

func (r *Runner) rejectInvalidToolBatch(ctx context.Context, messages []*llm.Message, calls []llm.ToolCall, seen map[string]bool, invalid *toolValidationFailure, emit kernelcompat.Emit) ([]*llm.Message, error) {
	results := make([]string, len(calls))
	for i, call := range calls {
		code := "batch_not_dispatched"
		if call.ID == invalid.callID {
			code = invalid.code
		}
		feedback := map[string]any{
			"status": "error", "error_code": code, "execution_status": "not_dispatched", "retryable": true,
			"message": "No call in this batch was executed. Correct the arguments using the supplied tool schema, then submit a new call with a new ID. Do not end the task solely because of this correctable input error. Do not repeat already completed operations.",
		}
		raw, _ := json.Marshal(feedback)
		results[i] = string(raw)
		// Validator errors can contain the submitted values. Return the concrete
		// reason only to this authorized model turn and its private checkpoint;
		// canonical display events keep the value-free error above.
		if call.ID == invalid.callID && invalid.validationReason != "" {
			reason := invalid.validationReason
			if len(reason) > 2048 {
				reason = reason[:2048]
			}
			feedback["validation_reason"] = reason
			raw, _ = json.Marshal(feedback)
		}
		messages = append(messages, llm.ToolMessage(string(raw), call.ID))
		seen[call.ID] = true
	}
	// Persist the complete rejected batch at a safe model boundary. No execution
	// journal claim or pending side-effect barrier is created for rejected input.
	if err := r.saveCheckpoint(ctx, CheckpointReady, messages); err != nil {
		return nil, err
	}
	for i, call := range calls {
		// Rejected arguments may not even be JSON. Keep them in the private
		// checkpoint; canonical display events contain only the bound identity.
		if err := emit(agentcore.SessionEvent{Kind: agentcore.EventToolCall, ToolCall: &agentcore.ToolCallEvent{ToolUseID: call.ID, Name: call.Function.Name}}); err != nil {
			return nil, err
		}
		if err := emit(agentcore.SessionEvent{Kind: agentcore.EventToolResult, ToolResult: &agentcore.ToolResultEvent{ToolUseID: call.ID, IsError: true, Content: []agentcore.ToolResultContent{{Kind: agentcore.ToolResultText, Text: results[i]}}}}); err != nil {
			return nil, err
		}
	}
	return messages, nil
}
