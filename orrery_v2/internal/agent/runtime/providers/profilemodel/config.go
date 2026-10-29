package profilemodel

import (
	"context"
	"orrery_v2/internal/agent/llm"
)

type ModelFactory interface {
	ModelForRequest(ctx context.Context, req ModelRequest) (llm.ChatModel, error)
}

type ModelRequest struct {
	Model           string
	ConfigRef       string
	LLMProfileID    string
	ReasoningEffort string
	PromptCacheKey  string
	// ManagedContext leaves lossless history management to the caller.
	ManagedContext  bool
	StreamResponses bool
}
