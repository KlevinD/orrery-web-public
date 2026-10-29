package email

import "orrery_v2/internal/gateway/protocol"

func RenderReply(inbound InboundEmail, events []protocol.Event) (OutboundEmail, bool) {
	var body string
	for _, event := range events {
		switch event.Type {
		case protocol.EventAssistantTextDelta:
			body += event.Text
		}
	}
	if body == "" {
		return OutboundEmail{}, false
	}
	return OutboundEmail{To: inbound.From, InReplyTo: inbound.MessageID, Subject: "Re: " + inbound.Subject, Body: body}, true
}
