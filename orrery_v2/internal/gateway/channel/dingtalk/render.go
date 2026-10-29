package dingtalk

import "orrery_v2/internal/gateway/protocol"

type OutboundMessage struct {
	ConversationID string
	SessionWebhook string
	Text           string
}

func RenderReply(conversationID string, events []protocol.Event) (OutboundMessage, bool) {
	var text string
	for _, event := range events {
		switch event.Type {
		case protocol.EventAssistantTextDelta:
			text += event.Text
		}
	}
	if text == "" {
		return OutboundMessage{}, false
	}
	return OutboundMessage{ConversationID: conversationID, Text: text}, true
}
