package profilemodel

import "encoding/json"

type openAICompatibleImageURL struct {
	URL    string `json:"url"`
	Detail string `json:"detail,omitempty"`
}
type openAICompatibleContentPart struct {
	Type     string                    `json:"type"`
	Text     string                    `json:"text,omitempty"`
	ImageURL *openAICompatibleImageURL `json:"image_url,omitempty"`
}

func (m openAICompatibleChatMessage) contentParts() []openAICompatibleContentPart {
	parts := make([]openAICompatibleContentPart, 0, len(m.ContentParts)+1)
	if m.Content != "" {
		parts = append(parts, openAICompatibleContentPart{Type: "text", Text: m.Content})
	}
	return append(parts, m.ContentParts...)
}

func (m openAICompatibleChatMessage) MarshalJSON() ([]byte, error) {
	type alias openAICompatibleChatMessage
	var content any = m.Content
	if len(m.ContentParts) > 0 {
		content = m.contentParts()
	}
	return json.Marshal(struct {
		alias
		Content any `json:"content,omitempty"`
	}{alias: alias(m), Content: content})
}

func (m openAICompatibleChatMessage) responsesContent() any {
	if len(m.ContentParts) == 0 {
		return m.Content
	}
	parts := make([]map[string]any, 0, len(m.ContentParts)+1)
	for _, p := range m.contentParts() {
		switch p.Type {
		case "text":
			parts = append(parts, map[string]any{"type": "input_text", "text": p.Text})
		case "image_url":
			if p.ImageURL != nil {
				image := map[string]any{"type": "input_image", "image_url": p.ImageURL.URL}
				if p.ImageURL.Detail != "" {
					image["detail"] = p.ImageURL.Detail
				}
				parts = append(parts, image)
			}
		}
	}
	return parts
}
