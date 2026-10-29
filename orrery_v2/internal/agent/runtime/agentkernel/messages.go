package agentkernel

import (
	"encoding/json"
	agentcore "orrery_v2/internal/agent/core"
	"orrery_v2/internal/agent/llm"
	runtimecore "orrery_v2/internal/agent/runtime/core"
	"orrery_v2/internal/agent/runtime/providers/workspaceprompt"
	"strings"
)

func cloneBounded(messages []*llm.Message, limit int) ([]*llm.Message, error) {
	raw, err := json.Marshal(messages)
	if err != nil {
		return nil, ErrInput
	}
	textBytes, err := contextMessageBytes(messages, len(raw))
	if err != nil || textBytes > limit {
		return nil, ErrBudget
	}
	var cloned []*llm.Message
	if json.Unmarshal(raw, &cloned) != nil {
		return nil, ErrInput
	}
	return cloned, nil
}

func inputText(input agentcore.SendCommand) (string, error) {
	if len(input.Content) == 0 {
		return "", ErrInput
	}
	var text strings.Builder
	for _, block := range input.Content {
		if block.Kind == agentcore.ContentBlockImage {
			continue
		}
		if block.Kind != agentcore.ContentBlockText {
			return "", ErrInput
		}
		text.WriteString(block.Text)
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", ErrInput
	}
	return text.String(), nil
}

func initialMessages(req agentcore.RuntimeRequest) ([]*llm.Message, error) {
	var messages []*llm.Message
	hasSystem := false
	for _, input := range req.InputMessages {
		hasSystem = hasSystem || input.Role == "system"
	}
	if !hasSystem && strings.TrimSpace(req.RuntimeOptions.SystemPrompt) != "" {
		messages = append(messages, llm.SystemMessage(req.RuntimeOptions.SystemPrompt))
	}
	boundary := workspaceprompt.Boundary(runtimecore.ExecOptions{Cwd: req.RuntimeOptions.WorkingDirectory, WorkspacePolicy: req.RuntimeOptions.WorkspacePolicy})
	if boundary != "" {
		messages = append(messages, llm.SystemMessage(boundary))
	}
	pending, seen := map[string]bool{}, map[string]bool{}
	for _, input := range req.InputMessages {
		if len(pending) > 0 && input.Role != "tool" {
			return nil, ErrInput
		}
		message := &llm.Message{Role: llm.Role(input.Role), Content: input.Content}
		switch input.Role {
		case "system", "user":
			if len(input.ToolCalls) > 0 || input.ToolCallID != "" {
				return nil, ErrInput
			}
		case "assistant":
			if input.ToolCallID != "" {
				return nil, ErrInput
			}
			for _, call := range input.ToolCalls {
				if call.ID == "" || call.Name == "" || seen[call.ID] {
					return nil, ErrInput
				}
				raw, err := json.Marshal(call.Input)
				if err != nil {
					return nil, ErrInput
				}
				message.ToolCalls = append(message.ToolCalls, llm.ToolCall{ID: call.ID, Type: "function", Function: llm.FunctionCall{Name: call.Name, Arguments: string(raw)}})
				pending[call.ID], seen[call.ID] = true, true
			}
		case "tool":
			if !pending[input.ToolCallID] || len(input.ToolCalls) != 0 {
				return nil, ErrInput
			}
			delete(pending, input.ToolCallID)
			message.ToolCallID, message.ToolName = input.ToolCallID, input.ToolName
		default:
			return nil, ErrInput
		}
		messages = append(messages, message)
	}
	if len(pending) > 0 {
		return nil, ErrInput
	}
	if len(req.InputMedia) > 0 {
		index := lastInputUserIndex(messages)
		if index < 0 {
			return nil, ErrInput
		}
		parts, err := inputParts(req.InputMedia)
		if err != nil {
			return nil, err
		}
		messages[index].UserInputMultiContent = parts
	}
	return messages, nil
}
