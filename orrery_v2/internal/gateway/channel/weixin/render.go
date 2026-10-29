package weixin

import (
	"encoding/base64"
	"strings"

	"orrery_v2/internal/gateway/protocol"
)

type OutboundMessage struct {
	AccountID    string
	ChatID       string
	ContextToken string
	ClientID     string
	Text         string
	Image        []byte
}

func RenderReplies(chatID string, events []protocol.Event, opts Options) []OutboundMessage {
	var text string
	var images []OutboundMessage
	var imageMarkdown []string
	for _, event := range events {
		switch event.Type {
		case protocol.EventAssistantImage:
			if a := event.Attachment; a != nil && a.Type == "image" && a.MIMEType == "image/png" && len(a.Content) <= (maxIlinkImageBytes*4/3)+4 {
				data, err := base64.StdEncoding.DecodeString(a.Content)
				if err == nil && len(data) > 0 && len(data) <= maxIlinkImageBytes {
					images = append(images, OutboundMessage{ChatID: chatID, Image: data})
					if markdown, ok := a.Metadata["markdown"].(string); ok && markdown != "" {
						imageMarkdown = append(imageMarkdown, markdown)
					}
				}
			}
		case protocol.EventAssistantTextDelta:
			text += event.Text
		case protocol.EventPermissionRequest:
			text += renderPermissionRequest(event)
		}
	}
	for _, markdown := range imageMarkdown {
		text = strings.ReplaceAll(text, markdown, "")
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return images
	}
	chunkSize := opts.ReplyChunkSize
	if chunkSize <= 0 {
		chunkSize = 1800
	}
	runes := []rune(text)
	replies := make([]OutboundMessage, 0, (len(runes)+chunkSize-1)/chunkSize)
	for len(runes) > chunkSize {
		replies = append(replies, OutboundMessage{ChatID: chatID, Text: string(runes[:chunkSize])})
		runes = runes[chunkSize:]
	}
	replies = append(replies, OutboundMessage{ChatID: chatID, Text: string(runes)})
	return append(replies, images...)
}

func renderPermissionRequest(event protocol.Event) string {
	payload, _ := event.Payload.(map[string]string)
	if strings.EqualFold(strings.TrimSpace(payload["personal_assistant_reply"]), "notification") {
		return "需要你的决定。个人助理会发送审批详情；收到后直接回复“同意”或“拒绝”即可。"
	}
	toolName := strings.TrimSpace(event.ToolName)
	if toolName == "" {
		return "\u9700\u8981\u5ba1\u6279\u624d\u80fd\u7ee7\u7eed\u6267\u884c\u3002\u8bf7\u524d\u5f80 Orrery \u5ba1\u6279\u4e2d\u5fc3\u5904\u7406\uff1b\u5ba1\u6279\u5b8c\u6210\u540e\uff0c\u5f53\u524d\u5bf9\u8bdd\u4f1a\u7ee7\u7eed\u8fd0\u884c\u3002"
	}
	return "\u9700\u8981\u5ba1\u6279\u624d\u80fd\u7ee7\u7eed\u6267\u884c\u5de5\u5177\u300c" + toolName + "\u300d\u3002\u8bf7\u524d\u5f80 Orrery \u5ba1\u6279\u4e2d\u5fc3\u5904\u7406\uff1b\u5ba1\u6279\u5b8c\u6210\u540e\uff0c\u5f53\u524d\u5bf9\u8bdd\u4f1a\u7ee7\u7eed\u8fd0\u884c\u3002"
}
