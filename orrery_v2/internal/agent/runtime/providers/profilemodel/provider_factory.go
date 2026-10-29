package profilemodel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"orrery_v2/internal/agent/llm"
	"strings"
	"time"

	agentcore "orrery_v2/internal/agent/core"
	llmprofile "orrery_v2/internal/agent/runtime/llmprofile"
)

type ProfileModelFactoryConfig struct {
	Profiles llmprofile.ProfileProvider
	Secrets  llmprofile.SecretResolver
	Client   *http.Client
}

type ProfileModelFactory struct {
	profiles llmprofile.ProfileProvider
	secrets  llmprofile.SecretResolver
	client   *http.Client
}

func NewProfileModelFactory(config ProfileModelFactoryConfig) *ProfileModelFactory {
	client := config.Client
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &ProfileModelFactory{
		profiles: config.Profiles,
		secrets:  config.Secrets,
		client:   client,
	}
}

func (f *ProfileModelFactory) ModelForRequest(ctx context.Context, req ModelRequest) (llm.ChatModel, error) {
	profile, err := f.selectProfile(ctx, req)
	if err != nil {
		return nil, err
	}
	if modelName := strings.TrimSpace(req.Model); modelName != "" {
		profile.Model = modelName
	}
	chat, err := f.newModel(ctx, profile, req.ReasoningEffort)
	if compatible, ok := chat.(*openAICompatibleChatModel); ok {
		compatible.promptCacheKey = strings.TrimSpace(req.PromptCacheKey)
		compatible.managedContext = req.ManagedContext
		compatible.streamResponses = req.StreamResponses
	}
	return chat, err
}

func (f *ProfileModelFactory) selectProfile(ctx context.Context, req ModelRequest) (llmprofile.LLMProfile, error) {
	if f == nil {
		return llmprofile.LLMProfile{}, fmt.Errorf("eino profile provider is not configured")
	}
	selection, err := llmprofile.Resolve(ctx, f.profiles, llmprofile.SelectionRequest{
		ProfileID: req.LLMProfileID, ConfigRef: req.ConfigRef,
	})
	return selection.Profile, err
}

func (f *ProfileModelFactory) newModel(ctx context.Context, profile llmprofile.LLMProfile, reasoningEffort string) (llm.ChatModel, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	apiKey, err := f.resolveAPIKey(ctx, profile)
	if err != nil {
		return nil, err
	}
	if apiKey != "" && !asciiPrintable(apiKey) {
		return nil, fmt.Errorf("llm profile %q api key must contain only printable ASCII characters", profile.ProfileID)
	}
	endpoint, err := openAICompatibleChatCompletionsURL(profile.BaseURL)
	responses := usesResponsesAPI(profile.Provider)
	if responses {
		endpoint, err = openAICompatibleResponsesURL(profile.BaseURL)
	}
	if err != nil {
		return nil, err
	}
	return &openAICompatibleChatModel{
		profile:         profile,
		apiKey:          apiKey,
		endpoint:        endpoint,
		client:          f.client,
		reasoningEffort: strings.TrimSpace(reasoningEffort),
		responses:       responses,
	}, nil
}

func (f *ProfileModelFactory) resolveAPIKey(ctx context.Context, profile llmprofile.LLMProfile) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if key := strings.TrimSpace(profile.APIKey); key != "" {
		return key, nil
	}
	ref := strings.TrimSpace(profile.APIKeyRef)
	if ref == "" {
		return "", fmt.Errorf("llm profile %q has no api key", profile.ProfileID)
	}
	if f == nil || f.secrets == nil {
		return "", fmt.Errorf("llm profile %q references %q but no secret resolver is configured", profile.ProfileID, ref)
	}
	return f.secrets.ResolveSecret(ctx, ref)
}

type openAICompatibleChatModel struct {
	profile         llmprofile.LLMProfile
	apiKey          string
	endpoint        string
	client          *http.Client
	reasoningEffort string
	responses       bool
	promptCacheKey  string
	managedContext  bool
	streamResponses bool
}

const requiredToolRetryInstruction = "You must call one of the supplied tools before answering. Return a tool call now and no prose."

// ModelIdentity invalidates durable working memory when a resolved profile or
// credential changes under the same profile ID. No credential leaves this hash.
func (m *openAICompatibleChatModel) ModelIdentity() string {
	raw, _ := json.Marshal([]any{m.profile, m.apiKey, m.reasoningEffort})
	return fmt.Sprintf("%x", sha256.Sum256(raw))
}

func (m *openAICompatibleChatModel) Generate(ctx context.Context, input []*llm.Message, opts ...llm.Option) (*llm.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m == nil || m.client == nil {
		return nil, fmt.Errorf("openai-compatible chat model is not configured")
	}
	body, err := m.buildRequest(input, opts...)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		completion, err := m.generateCompletion(ctx, body)
		if err != nil {
			return nil, err
		}
		message, err := messageFromOpenAICompatibleCompletion(completion)
		if err != nil {
			return nil, err
		}
		if body.ToolChoice == "required" && len(message.ToolCalls) == 0 {
			if attempt == 0 {
				body.Messages = append(body.Messages, openAICompatibleChatMessage{Role: "system", Content: requiredToolRetryInstruction})
				continue
			}
			return nil, fmt.Errorf("llm did not return a required tool call")
		}
		return message, nil
	}
	return nil, fmt.Errorf("llm did not return a response")
}

func (m *openAICompatibleChatModel) generateCompletion(ctx context.Context, body openAICompatibleChatCompletionRequest) (openAICompatibleChatCompletionResponse, error) {
	if m.responses {
		return m.generateResponses(ctx, body)
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return openAICompatibleChatCompletionResponse{}, fmt.Errorf("marshal openai-compatible chat request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.endpoint, bytes.NewReader(payload))
	if err != nil {
		return openAICompatibleChatCompletionResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiKey)
	}
	resp, err := m.client.Do(req)
	if err != nil {
		return openAICompatibleChatCompletionResponse{}, fmt.Errorf("call llm profile %q: %w", m.profile.ProfileID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if isContextWindowError(resp.StatusCode, raw) {
			return openAICompatibleChatCompletionResponse{}, ErrContextWindowExceeded
		}
		return openAICompatibleChatCompletionResponse{}, fmt.Errorf("llm returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}
	var completion openAICompatibleChatCompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return openAICompatibleChatCompletionResponse{}, fmt.Errorf("decode llm response: %w", err)
	}
	return completion, nil
}

func (m *openAICompatibleChatModel) Stream(ctx context.Context, input []*llm.Message, opts ...llm.Option) (*llm.MessageStream, error) {
	if m != nil && m.streamResponses {
		if m.responses {
			return m.streamResponsesCompletion(ctx, input, opts...)
		}
		return m.streamCompletion(ctx, input, opts...)
	}
	message, err := m.Generate(ctx, input, opts...)
	if err != nil {
		return nil, err
	}
	return llm.NewMessageStream([]*llm.Message{message}), nil
}

func (m *openAICompatibleChatModel) buildRequest(input []*llm.Message, opts ...llm.Option) (openAICompatibleChatCompletionRequest, error) {
	common := llm.Apply(opts...)
	modelName := firstNonBlank(stringPointerValue(common.Model), m.profile.Model, "gpt-5")
	body := openAICompatibleChatCompletionRequest{
		Model:           modelName,
		Messages:        openAICompatibleMessages(input),
		ReasoningEffort: m.reasoningEffort,
		PromptCacheKey:  m.promptCacheKey,
	}
	if common.Temperature != nil {
		temperature := float64(*common.Temperature)
		body.Temperature = &temperature
	} else if m.profile.Temperature != 0 {
		temperature := m.profile.Temperature
		body.Temperature = &temperature
	}
	if len(common.Tools) > 0 {
		tools, err := openAICompatibleTools(common.Tools)
		if err != nil {
			return openAICompatibleChatCompletionRequest{}, err
		}
		body.Tools = tools
		body.ToolChoice = openAICompatibleToolChoice(common.ToolChoice)
	}
	if m.managedContext {
		if managedPromptTokens(body.Messages, body.Tools) > m.inputTokenLimit() {
			return openAICompatibleChatCompletionRequest{}, ErrContextWindowExceeded
		}
	} else {
		body.Messages = limitOpenAICompatibleMessages(body.Messages, m.profile.ContextLength, body.Tools)
		contextTokens := agentcore.EffectiveRuntimeContextTokenLimit(map[string]string{"llm_context_length": contextLengthString(m.profile.ContextLength)})
		budget := agentcore.RuntimePromptTokenBudgetAfterReserved(agentcore.RuntimePromptTokenBudget(contextTokens), estimateOpenAICompatibleTokens(body.Tools))
		if estimateOpenAICompatibleMessagesTokens(body.Messages) > budget {
			return openAICompatibleChatCompletionRequest{}, fmt.Errorf("required model instructions, current request and tool call references exceed the input budget")
		}
	}
	return body, nil
}

type openAICompatibleChatCompletionRequest struct {
	Model           string                        `json:"model"`
	Messages        []openAICompatibleChatMessage `json:"messages"`
	ReasoningEffort string                        `json:"reasoning_effort,omitempty"`
	PromptCacheKey  string                        `json:"prompt_cache_key,omitempty"`
	Temperature     *float64                      `json:"temperature,omitempty"`
	Tools           []openAICompatibleTool        `json:"tools,omitempty"`
	ToolChoice      string                        `json:"tool_choice,omitempty"`
	Stream          bool                          `json:"stream,omitempty"`
	StreamOptions   *openAIStreamOptions          `json:"stream_options,omitempty"`
}

type openAICompatibleChatMessage struct {
	Role         string                        `json:"role"`
	Content      string                        `json:"content,omitempty"`
	ContentParts []openAICompatibleContentPart `json:"-"`
	Name         string                        `json:"name,omitempty"`
	ToolCallID   string                        `json:"tool_call_id,omitempty"`
	ToolCalls    []openAICompatibleToolCall    `json:"tool_calls,omitempty"`
	Reasoning    []json.RawMessage             `json:"-"`
}

type openAICompatibleTool struct {
	Type     string                       `json:"type"`
	Function openAICompatibleToolFunction `json:"function"`
}

type openAICompatibleToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	Parameters  map[string]any `json:"parameters,omitempty"`
}

type openAICompatibleToolCall struct {
	ID       string                           `json:"id"`
	Type     string                           `json:"type"`
	Function openAICompatibleToolCallFunction `json:"function"`
}

type openAICompatibleToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAICompatibleChatCompletionResponse struct {
	Model     string                             `json:"model"`
	Choices   []openAICompatibleCompletionChoice `json:"choices"`
	Usage     openAICompatibleCompletionUsage    `json:"usage"`
	Reasoning []json.RawMessage                  `json:"-"`
}

type openAICompatibleCompletionChoice struct {
	FinishReason string                            `json:"finish_reason,omitempty"`
	Message      openAICompatibleCompletionMessage `json:"message"`
}

type openAICompatibleCompletionMessage struct {
	Content   string                     `json:"content"`
	ToolCalls []openAICompatibleToolCall `json:"tool_calls"`
}

type openAICompatibleCompletionUsage struct {
	PromptTokens            int                   `json:"prompt_tokens"`
	CompletionTokens        int                   `json:"completion_tokens"`
	TotalTokens             int                   `json:"total_tokens"`
	PromptTokensDetails     cachedTokenDetails    `json:"prompt_tokens_details"`
	CompletionTokensDetails reasoningTokenDetails `json:"completion_tokens_details"`
}

func openAICompatibleMessages(messages []*llm.Message) []openAICompatibleChatMessage {
	out := make([]openAICompatibleChatMessage, 0, len(messages))
	for _, message := range messages {
		if message == nil {
			continue
		}
		item := openAICompatibleChatMessage{
			Role:       string(message.Role),
			Content:    message.Content,
			Name:       message.Name,
			ToolCallID: message.ToolCallID,
		}
		if raw, err := json.Marshal(message.Extra[responsesReasoningKey]); err == nil {
			_ = json.Unmarshal(raw, &item.Reasoning)
		}
		for _, part := range message.UserInputMultiContent {
			switch part.Type {
			case llm.ChatMessagePartTypeText:
				item.ContentParts = append(item.ContentParts, openAICompatibleContentPart{Type: "text", Text: part.Text})
			case llm.ChatMessagePartTypeImageURL:
				if part.Image != nil {
					url := ""
					if part.Image.Base64Data != nil {
						url = "data:" + part.Image.MIMEType + ";base64," + *part.Image.Base64Data
					} else if part.Image.URL != nil {
						url = *part.Image.URL
					}
					item.ContentParts = append(item.ContentParts, openAICompatibleContentPart{Type: "image_url", ImageURL: &openAICompatibleImageURL{URL: url, Detail: string(part.Image.Detail)}})
				}
			}
		}
		for _, call := range message.ToolCalls {
			item.ToolCalls = append(item.ToolCalls, openAICompatibleToolCall{
				ID:   firstNonBlank(call.ID, call.Function.Name),
				Type: firstNonBlank(call.Type, "function"),
				Function: openAICompatibleToolCallFunction{
					Name:      normalizeToolName(call.Function.Name),
					Arguments: call.Function.Arguments,
				},
			})
		}
		out = append(out, item)
	}
	return out
}

func openAICompatibleTools(tools []*llm.Tool) ([]openAICompatibleTool, error) {
	out := make([]openAICompatibleTool, 0, len(tools))
	for _, tool := range tools {
		if tool == nil || strings.TrimSpace(tool.Name) == "" {
			continue
		}
		parameters, err := openAICompatibleToolParameters(tool.Parameters)
		if err != nil {
			return nil, fmt.Errorf("convert tool %q parameters: %w", tool.Name, err)
		}
		out = append(out, openAICompatibleTool{
			Type: "function",
			Function: openAICompatibleToolFunction{
				Name:        normalizeToolName(tool.Name),
				Description: strings.TrimSpace(tool.Description),
				Parameters:  parameters,
			},
		})
	}
	return out, nil
}

func openAICompatibleToolParameters(params map[string]any) (map[string]any, error) {
	if len(params) == 0 {
		return map[string]any{"type": "object", "additionalProperties": true}, nil
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	var result map[string]any
	if err = json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	return result, nil
}

func openAICompatibleToolChoice(choice *llm.ToolChoice) string {
	if choice == nil {
		return "auto"
	}
	switch *choice {
	case llm.ToolChoiceForbidden:
		return "none"
	case llm.ToolChoiceForced:
		return "required"
	default:
		return "auto"
	}
}

func messageFromOpenAICompatibleCompletion(completion openAICompatibleChatCompletionResponse) (*llm.Message, error) {
	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("llm response did not include choices")
	}
	choice := completion.Choices[0]
	message := &llm.Message{
		Role:    llm.Assistant,
		Content: choice.Message.Content,
		ResponseMeta: &llm.ResponseMeta{
			FinishReason: strings.TrimSpace(choice.FinishReason),
			Usage:        completion.Usage.schemaUsage(),
		},
	}
	if len(completion.Reasoning) > 0 {
		message.Extra = map[string]any{responsesReasoningKey: completion.Reasoning}
	}
	completion.Usage.markDetails(message)
	if message.ResponseMeta.Usage.TotalTokens == 0 {
		message.ResponseMeta.Usage.TotalTokens = message.ResponseMeta.Usage.PromptTokens + message.ResponseMeta.Usage.CompletionTokens
	}
	if message.ResponseMeta.FinishReason == "" && message.ResponseMeta.Usage.TotalTokens == 0 {
		message.ResponseMeta = nil
	}
	for _, call := range choice.Message.ToolCalls {
		message.ToolCalls = append(message.ToolCalls, llm.ToolCall{
			ID:   strings.TrimSpace(call.ID),
			Type: firstNonBlank(call.Type, "function"),
			Function: llm.FunctionCall{
				Name:      normalizeToolName(call.Function.Name),
				Arguments: strings.TrimSpace(call.Function.Arguments),
			},
		})
	}
	return message, nil
}

func openAICompatibleChatCompletionsURL(base string) (string, error) {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return "", fmt.Errorf("llm base url is required")
	}
	if strings.HasSuffix(base, "/chat/completions") {
		return base, nil
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("llm base url is invalid: %q", base)
	}
	if pathEndsWithVersion(u.Path) {
		u.Path = strings.TrimRight(u.Path, "/") + "/chat/completions"
	} else {
		u.Path = strings.TrimRight(u.Path, "/") + "/v1/chat/completions"
	}
	return u.String(), nil
}

func pathEndsWithVersion(path string) bool {
	last := strings.TrimPrefix(strings.TrimSpace(strings.TrimRight(path, "/")), "/")
	if idx := strings.LastIndex(last, "/"); idx >= 0 {
		last = last[idx+1:]
	}
	if len(last) < 2 || last[0] != 'v' && last[0] != 'V' {
		return false
	}
	for _, r := range last[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func normalizeLLMProfile(profile llmprofile.LLMProfile) llmprofile.LLMProfile {
	profile.ProfileID = strings.TrimSpace(profile.ProfileID)
	profile.DisplayName = strings.TrimSpace(profile.DisplayName)
	profile.Provider = strings.TrimSpace(profile.Provider)
	profile.BaseURL = strings.TrimSpace(profile.BaseURL)
	profile.APIKey = strings.TrimSpace(profile.APIKey)
	profile.APIKeyRef = strings.TrimSpace(profile.APIKeyRef)
	profile.Model = strings.TrimSpace(profile.Model)
	return profile
}

func asciiPrintable(value string) bool {
	for _, r := range value {
		if r < 0x20 || r > 0x7e {
			return false
		}
	}
	return true
}

func stringPointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func normalizeToolName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, ".", "_")
	name = strings.ReplaceAll(name, ":", "_")
	name = strings.ReplaceAll(name, "-", "_")
	return name
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
