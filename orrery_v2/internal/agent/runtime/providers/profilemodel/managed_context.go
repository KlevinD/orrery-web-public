package profilemodel

import (
	"encoding/json"
	"errors"
	"orrery_v2/internal/agent/llm"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

var ErrContextWindowExceeded = errors.New("model context window exceeded")

// ModelContextUsage counts the wire messages and tool catalog, before any HTTP
// request. Estimates are conservative; providers may still reject a request.
type ModelContextUsage struct {
	EstimatedInputTokens int
	InputTokenLimit      int
	ContextWindowTokens  int
	OutputReserveTokens  int
}

type ContextBudgetModel interface {
	ContextUsage([]*llm.Message, []*llm.Tool) (ModelContextUsage, error)
}

func (m *openAICompatibleChatModel) contextWindow() int {
	if m.profile.ContextLength > 0 {
		return m.profile.ContextLength
	}
	return agentcore.RuntimeContextHardLimitTokens
}

func (m *openAICompatibleChatModel) inputTokenLimit() int {
	window := m.contextWindow()
	return max(1, window-agentcore.RuntimeContextOutputReserveTokens(window))
}

func (m *openAICompatibleChatModel) ContextUsage(messages []*llm.Message, tools []*llm.Tool) (ModelContextUsage, error) {
	definitions, err := openAICompatibleTools(tools)
	if err != nil {
		return ModelContextUsage{}, err
	}
	return ModelContextUsage{
		EstimatedInputTokens: managedPromptTokens(openAICompatibleMessages(messages), definitions),
		InputTokenLimit:      m.inputTokenLimit(), ContextWindowTokens: m.contextWindow(),
		OutputReserveTokens: agentcore.RuntimeContextOutputReserveTokens(m.contextWindow()),
	}, nil
}

func managedPromptTokens(messages []openAICompatibleChatMessage, tools []openAICompatibleTool) int {
	tokens := estimateOpenAICompatibleMessagesTokens(messages)
	if len(tools) > 0 {
		tokens += estimateOpenAICompatibleTokens(tools)
	}
	return tokens
}

func isContextWindowError(status int, raw []byte) bool {
	if status != 400 && status != 413 && status != 422 {
		return false
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
			Type string `json:"type"`
		} `json:"error"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return false
	}
	for _, code := range []string{body.Error.Code, body.Error.Type} {
		switch strings.ToLower(code) {
		case "context_length_exceeded", "context_window_exceeded":
			return true
		}
	}
	return false
}
