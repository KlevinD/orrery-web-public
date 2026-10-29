package feishu

import "orrery_v2/internal/gateway/protocol"

type Reply struct {
	ChatID         string
	ReplyMessageID string
	IdempotencyKey string
	Text           string
}

func RenderReply(chatID string, events []protocol.Event) (Reply, bool) {
	var text string
	for _, event := range events {
		switch event.Type {
		case protocol.EventAssistantTextDelta:
			text += event.Text
		}
	}
	if text == "" {
		return Reply{}, false
	}
	return Reply{ChatID: chatID, Text: text}, true
}
