package email

import (
	"strings"

	"orrery_v2/internal/gateway/protocol"
)

func BuildSubmitInput(message InboundEmail) protocol.SubmitTurnInput {
	body := strings.TrimSpace(message.Body)
	telemetry := map[string]string{
		"email_message_id": message.MessageID,
		"email_from":       message.From,
	}
	if strings.HasPrefix(body, "/answer") {
		rest := strings.TrimSpace(strings.TrimPrefix(body, "/answer"))
		parts := strings.Fields(rest)
		if len(parts) > 0 {
			telemetry["elicitation_request_id"] = parts[0]
			body = strings.TrimSpace(strings.TrimPrefix(rest, parts[0]))
		}
	} else if message.Subject != "" {
		body = "Subject: " + message.Subject + "\n\n" + body
	}
	return protocol.SubmitTurnInput{
		SessionKey: "email:" + firstNonEmpty(message.ThreadID, message.MessageID),
		ChannelKey: protocol.ChannelEmail,
		Message:    body,
		Telemetry:  telemetry,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
