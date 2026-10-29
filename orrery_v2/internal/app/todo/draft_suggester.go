package todo

import (
	"context"
	"encoding/json"
	"fmt"
	"orrery_v2/internal/agent/llm"
	"strings"
)

type DraftSuggester interface {
	SuggestDraft(ctx context.Context, req SuggestDraftRequest) (DraftSuggestion, error)
}

type ChatModelFactory interface {
	ModelForRequest(ctx context.Context, req ChatModelRequest) (llm.ChatModel, error)
}

type ChatModelRequest struct {
	Model        string
	ConfigRef    string
	LLMProfileID string
}

type ChatDraftSuggester struct {
	models ChatModelFactory
}

func NewChatDraftSuggester(models ChatModelFactory) *ChatDraftSuggester {
	return &ChatDraftSuggester{models: models}
}

func (s *ChatDraftSuggester) SuggestDraft(ctx context.Context, req SuggestDraftRequest) (DraftSuggestion, error) {
	if err := ctx.Err(); err != nil {
		return DraftSuggestion{}, err
	}
	if s == nil || s.models == nil {
		return DraftSuggestion{}, ErrServiceDependency
	}
	chatModel, err := s.models.ModelForRequest(ctx, ChatModelRequest{})
	if err != nil {
		return DraftSuggestion{}, err
	}
	if chatModel == nil {
		return DraftSuggestion{}, ErrServiceDependency
	}
	out, err := chatModel.Generate(ctx, []*llm.Message{
		llm.SystemMessage(draftSuggestionSystemPrompt()),
		llm.UserMessage(draftSuggestionUserPrompt(req)),
	})
	if err != nil {
		return DraftSuggestion{}, err
	}
	return parseDraftSuggestion(out)
}

func draftSuggestionSystemPrompt() string {
	return strings.Join([]string{
		"你是 Orrery 的待办和灵感草稿命名助手。",
		"只返回 JSON，不要 Markdown，不要解释。",
		`JSON schema: {"title":"短标题，最多 18 个中文字符或 6 个英文词","tags":["2 到 5 个短标签"]}`,
		"标题要具体、可执行，标签要适合检索，保留用户已有标签并去重。",
	}, "\n")
}

func draftSuggestionUserPrompt(req SuggestDraftRequest) string {
	payload := map[string]any{
		"kind":         req.Kind,
		"title":        req.Title,
		"content":      req.Content,
		"current_tags": req.CurrentTags,
	}
	raw, _ := json.Marshal(payload)
	return string(raw)
}

func parseDraftSuggestion(message *llm.Message) (DraftSuggestion, error) {
	if message == nil || strings.TrimSpace(message.Content) == "" {
		return DraftSuggestion{}, ErrInvalidRequest
	}
	content := strings.TrimSpace(message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)
	var suggestion DraftSuggestion
	if err := json.Unmarshal([]byte(content), &suggestion); err != nil {
		return DraftSuggestion{}, fmt.Errorf("%w: invalid draft suggestion json", ErrInvalidRequest)
	}
	suggestion.Title = strings.TrimSpace(suggestion.Title)
	suggestion.Tags = normalizeTags(suggestion.Tags)
	if suggestion.Title == "" || len(suggestion.Tags) == 0 {
		return DraftSuggestion{}, ErrInvalidRequest
	}
	return suggestion, nil
}
