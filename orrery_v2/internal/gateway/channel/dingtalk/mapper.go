package dingtalk

import (
	"strings"

	"orrery_v2/internal/gateway/channel"
	"orrery_v2/internal/gateway/protocol"
)

type InboundMessage struct {
	MessageID        string `json:"message_id"`
	AccountID        string `json:"account_id"`
	TenantID         string `json:"tenant_id"`
	ConversationID   string `json:"conversation_id"`
	ConversationType string `json:"conversation_type"`
	SenderID         string `json:"sender_id"`
	SenderName       string `json:"sender_name"`
	SessionWebhook   string `json:"session_webhook,omitempty"`
	Text             string `json:"text"`
	SenderVerified   bool   `json:"sender_verified"`
}

func BuildSubmitInput(message InboundMessage) protocol.SubmitTurnInput {
	text := strings.TrimSpace(message.Text)
	telemetry := map[string]string{
		"dingtalk_message_id":  message.MessageID,
		"dingtalk_sender_id":   message.SenderID,
		"dingtalk_account_id":  message.AccountID,
		"dingtalk_tenant_id":   message.TenantID,
		"dingtalk_sender_name": message.SenderName,
	}
	if strings.HasPrefix(text, "/answer") {
		rest := strings.TrimSpace(strings.TrimPrefix(text, "/answer"))
		parts := strings.Fields(rest)
		if len(parts) > 0 {
			telemetry["elicitation_request_id"] = parts[0]
			text = strings.TrimSpace(strings.TrimPrefix(rest, parts[0]))
		}
	}
	return protocol.SubmitTurnInput{
		SessionKey: "dingtalk:" + message.ConversationID,
		ChannelKey: protocol.ChannelDingTalk,
		Message:    text,
		RunID:      channel.InboundRunID(string(protocol.ChannelDingTalk), message.AccountID, message.MessageID),
		Telemetry:  telemetry,
		Identity: &protocol.InboundIdentity{
			AccountID:        message.AccountID,
			ExternalTenantID: message.TenantID,
			ExternalUserID:   message.SenderID,
			ExternalChatID:   message.ConversationID,
			ChatKind:         normalizeConversationType(message.ConversationType),
			DisplayName:      message.SenderName,
			Verified:         message.SenderVerified,
		},
	}
}

func normalizeConversationType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	switch value {
	case "2", "group":
		return "group"
	case "1", "direct", "private", "single":
		return "direct"
	default:
		return "unknown"
	}
}
