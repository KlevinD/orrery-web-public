package profilemodel

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"orrery_v2/internal/agent/llm"
	"strings"
	"testing"

	"orrery_v2/internal/agent/runtime/llmprofile"
)

func TestBothModelProtocolsTransmitImageBytesAndOrderedFrameLabels(t *testing.T) {
	for _, provider := range []string{"custom-openai-responses", "custom_openai"} {
		t.Run(provider, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				key, imageType, urlKey := "messages", "image_url", "image_url"
				if provider == "custom-openai-responses" {
					key, imageType = "input", "input_image"
				}
				messages := body[key].([]any)
				content := messages[len(messages)-1].(map[string]any)["content"].([]any)
				if len(content) != 3 {
					t.Fatalf("content=%v", content)
				}
				if content[1].(map[string]any)["text"] != "Frame at 2 seconds" {
					t.Fatal("frame label order lost")
				}
				part := content[2].(map[string]any)
				if part["type"] != imageType {
					t.Fatal("image omitted")
				}
				url := part[urlKey]
				if obj, ok := url.(map[string]any); ok {
					url = obj["url"]
				}
				if url != "data:image/png;base64,AQID" {
					t.Fatalf("image bytes missing: %v", url)
				}
				if provider == "custom-openai-responses" {
					_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`))
				} else {
					_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
				}
			}))
			defer server.Close()
			factory := NewProfileModelFactory(ProfileModelFactoryConfig{Profiles: llmprofile.StaticProfileProvider{Profiles: []llmprofile.LLMProfile{{Enabled: true, Provider: provider, BaseURL: server.URL + "/v1", APIKey: "test", Model: "vision"}}}})
			chat, err := factory.ModelForRequest(context.Background(), ModelRequest{ManagedContext: true})
			if err != nil {
				t.Fatal(err)
			}
			data := "AQID"
			m := llm.UserMessage("Inspect the frames")
			m.UserInputMultiContent = []llm.MessageInputPart{{Type: llm.ChatMessagePartTypeText, Text: "Frame at 2 seconds"}, {Type: llm.ChatMessagePartTypeImageURL, Image: &llm.MessageInputImage{MessagePartCommon: llm.MessagePartCommon{Base64Data: &data, MIMEType: "image/png"}}}}
			result, err := chat.Generate(context.Background(), []*llm.Message{m})
			if err != nil || result.Content != "ok" || calls != 1 {
				t.Fatalf("result=%v err=%v calls=%d", result, err, calls)
			}
		})
	}
}

func TestImageTokenEstimatePreservesPayloadWithoutCountingBase64AsText(t *testing.T) {
	m := openAICompatibleChatMessage{Role: "user", Content: "look", ContentParts: []openAICompatibleContentPart{{Type: "image_url", ImageURL: &openAICompatibleImageURL{URL: "data:image/png;base64," + strings.Repeat("A", 2<<20)}}}}
	before := m.ContentParts[0].ImageURL.URL
	if tokens := estimateOpenAICompatibleMessageTokens(m); tokens > 3000 || tokens < 2048 {
		t.Fatalf("image token estimate=%d", tokens)
	}
	if m.ContentParts[0].ImageURL.URL != before {
		t.Fatal("estimation mutated real payload")
	}
}
