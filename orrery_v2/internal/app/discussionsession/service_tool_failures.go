package discussionsession

import "strings"

func toolCallWithFailure(call ToolCallDTO, cause error) ToolCallDTO {
	message := "tool execution failed"
	if cause != nil && strings.TrimSpace(cause.Error()) != "" {
		message = strings.TrimSpace(cause.Error())
	}
	call.Status = "failed"
	call.Result = cloneStringMap(call.Result)
	if call.Result == nil {
		call.Result = map[string]string{}
	}
	call.Result["status"] = "failed"
	call.Result["error"] = message
	call.Result["retryable"] = "true"
	call.Result["recovery_hint"] = "Read the error, correct the tool arguments, and call the tool again. A single failed call must not end the interaction."
	return call
}

func toolCallsWithFailure(calls []ToolCallDTO, cause error) []ToolCallDTO {
	out := append([]ToolCallDTO(nil), calls...)
	for i := range out {
		out[i] = toolCallWithFailure(out[i], cause)
	}
	return out
}
