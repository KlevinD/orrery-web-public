package feishu

import (
	"strings"

	"orrery_v2/internal/gateway/channel"
	"orrery_v2/internal/gateway/protocol"
)

const (
	InboundEventMessage = "message"
	InboundEventMeeting = "meeting"
)

type InboundEvent struct {
	Kind           string        `json:"kind,omitempty"`
	EventID        string        `json:"event_id"`
	MessageID      string        `json:"message_id"`
	AccountID      string        `json:"account_id"`
	TenantID       string        `json:"tenant_id"`
	ChatID         string        `json:"chat_id"`
	ChatType       string        `json:"chat_type"`
	SenderID       string        `json:"sender_id"`
	SenderName     string        `json:"sender_name"`
	Text           string        `json:"text"`
	SenderVerified bool          `json:"sender_verified"`
	Meeting        *MeetingEvent `json:"meeting,omitempty"`
}

func BuildSubmitInput(event InboundEvent) protocol.SubmitTurnInput {
	text := strings.TrimSpace(event.Text)
	projectKey := ""
	if strings.HasPrefix(text, "/switch-project") {
		rest := strings.TrimSpace(strings.TrimPrefix(text, "/switch-project"))
		parts := strings.Fields(rest)
		if len(parts) > 0 {
			projectKey = parts[0]
			text = strings.TrimSpace(strings.TrimPrefix(rest, projectKey))
		}
	}
	return protocol.SubmitTurnInput{
		SessionKey: "feishu:" + firstNonEmpty(event.ChatID, "general"),
		ChannelKey: protocol.ChannelFeishu,
		Message:    text,
		ProjectKey: projectKey,
		RunID: channel.InboundRunID(
			string(protocol.ChannelFeishu), event.AccountID, firstNonEmpty(event.EventID, event.MessageID),
		),
		Telemetry: map[string]string{
			"feishu_event_id":    event.EventID,
			"feishu_message_id":  event.MessageID,
			"feishu_account_id":  event.AccountID,
			"feishu_tenant_id":   event.TenantID,
			"feishu_sender_id":   event.SenderID,
			"feishu_sender_name": event.SenderName,
		},
		Identity: &protocol.InboundIdentity{
			AccountID:        event.AccountID,
			ExternalTenantID: event.TenantID,
			ExternalUserID:   event.SenderID,
			ExternalChatID:   event.ChatID,
			ChatKind:         event.ChatType,
			DisplayName:      event.SenderName,
			Verified:         event.SenderVerified,
		},
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
