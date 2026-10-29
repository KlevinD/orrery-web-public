package agentkernel

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"orrery_v2/internal/agent/llm"
	"strings"

	agentcore "orrery_v2/internal/agent/core"
)

const maxKernelMediaBytes = 16 << 20

func (*Engine) SupportsImageInputs() bool { return true }

func inputParts(blocks []agentcore.ContentBlock) ([]llm.MessageInputPart, error) {
	var parts []llm.MessageInputPart
	bytes, images := 0, 0
	for _, b := range blocks {
		switch b.Kind {
		case agentcore.ContentBlockText:
			// Text remains in Message.Content, including the first send prompt.
			if b.Text != "" {
				parts = append(parts, llm.MessageInputPart{Type: llm.ChatMessagePartTypeText, Text: b.Text})
			}
		case agentcore.ContentBlockImage:
			bytes += len(b.Data)
			images++
			detected := http.DetectContentType(b.Data)
			if len(b.Data) == 0 || b.URI != "" || bytes > maxKernelMediaBytes || images > 32 || !strings.HasPrefix(detected, "image/") || detected != b.MediaType {
				return nil, ErrInput
			}
			data := base64.StdEncoding.EncodeToString(b.Data)
			parts = append(parts, llm.MessageInputPart{Type: llm.ChatMessagePartTypeImageURL, Image: &llm.MessageInputImage{MessagePartCommon: llm.MessagePartCommon{Base64Data: &data, MIMEType: b.MediaType}}})
		default:
			return nil, ErrInput
		}
	}
	return parts, nil
}

func inputMessage(blocks []agentcore.ContentBlock) (*llm.Message, error) {
	text, err := inputText(agentcore.SendCommand{Content: blocks})
	if err != nil {
		return nil, err
	}
	parts, err := inputParts(blocks)
	if err != nil {
		return nil, err
	}
	message := llm.UserMessage(text)
	for _, p := range parts {
		if p.Type != llm.ChatMessagePartTypeText {
			message.UserInputMultiContent = append(message.UserInputMultiContent, p)
		}
	}
	return message, nil
}

// The resumed turn uses the newly authorized request, never images left in the
// previous checkpoint's last user message.
func freshLastUserMessage(req agentcore.RuntimeRequest) (*llm.Message, error) {
	messages, err := initialMessages(req)
	index := lastInputUserIndex(messages)
	if err != nil || index < 0 {
		return nil, ErrInput
	}
	return messages[index], nil
}

// Host policy blocks may follow current input. Media never belongs to those
// instructions or a historical user turn behind assistant/tool output.
func lastInputUserIndex(messages []*llm.Message) int {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i] == nil {
			return -1
		}
		switch messages[i].Role {
		case llm.User:
			return i
		case llm.System:
			continue
		default:
			return -1
		}
	}
	return -1
}

func requestPrompt(req agentcore.RuntimeRequest) string {
	text := agentcore.RuntimePromptMessagesFallbackPrompt(req.InputMessages)
	for _, p := range req.InputMedia {
		if p.Kind == agentcore.ContentBlockText {
			text += p.Text
		}
	}
	return strings.TrimSpace(text)
}

func sameInputImages(a, b []agentcore.ContentBlock) bool {
	only := func(blocks []agentcore.ContentBlock) []agentcore.ContentBlock {
		var out []agentcore.ContentBlock
		for _, p := range blocks {
			if p.Kind != agentcore.ContentBlockText {
				out = append(out, p)
			}
		}
		return out
	}
	x, _ := json.Marshal(only(a))
	y, _ := json.Marshal(only(b))
	return string(x) == string(y)
}

// Media bytes have a separate hard cap. Base64 is a transport encoding, not
// millions of text tokens; keep the existing text context budget independent.
func contextMessageBytes(messages []*llm.Message, encodedBytes int) (int, error) {
	media := 0
	for _, m := range messages {
		if m == nil {
			continue
		}
		for _, p := range m.UserInputMultiContent {
			if p.Image != nil && p.Image.Base64Data != nil {
				size := len(*p.Image.Base64Data)
				media += size
				encodedBytes -= size
			}
		}
	}
	if media > base64.StdEncoding.EncodedLen(maxKernelMediaBytes) {
		return 0, ErrBudget
	}
	return max(0, encodedBytes), nil
}
