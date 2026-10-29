package weixin

import (
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/gateway/channel"
	"orrery_v2/internal/gateway/protocol"
)

type InboundMessage struct {
	AccountID        string
	MessageID        string
	ChatID           string
	ContextToken     string
	ChatName         string
	SenderID         string
	SenderName       string
	Text             string
	InputModality    string
	TranscriptSource string
	Attachments      []InboundAttachment
	Group            bool
	SenderVerified   bool
}

type InboundAttachment struct {
	Type              string `json:"type"`
	Name              string `json:"name,omitempty"`
	MIMEType          string `json:"mime_type,omitempty"`
	Bytes             int64  `json:"bytes,omitempty"`
	ContentBase64     string `json:"-"`
	FullURL           string `json:"full_url,omitempty"`
	EncryptQueryParam string `json:"encrypt_query_param,omitempty"`
	AESKey            string `json:"aes_key,omitempty"`
	AESKeyHex         string `json:"aes_key_hex,omitempty"`
}

func BuildSubmitInput(message InboundMessage) protocol.SubmitTurnInput {
	text := strings.TrimSpace(message.Text)
	sessionKey := "weixin:" + message.ChatID
	newSession := false
	if message.InputModality != "voice" && strings.HasPrefix(text, "/new") {
		newSession = true
		sessionKey = "weixin:" + message.ChatID + ":" + time.Now().UTC().Format("20060102150405.000000000")
		text = strings.TrimSpace(strings.TrimPrefix(text, "/new"))
	}
	return protocol.SubmitTurnInput{
		SessionKey:       sessionKey,
		ChannelKey:       protocol.ChannelWeixin,
		Message:          text,
		InputModality:    message.InputModality,
		TranscriptSource: message.TranscriptSource,
		Attachments:      protocolAttachments(message.Attachments),
		RunID:            channel.InboundRunID(string(protocol.ChannelWeixin), message.AccountID, message.MessageID),
		Telemetry: map[string]string{
			"weixin_account_id":      message.AccountID,
			"weixin_message_id":      message.MessageID,
			"weixin_chat_id":         message.ChatID,
			"weixin_context_token":   message.ContextToken,
			"weixin_chat_name":       message.ChatName,
			"weixin_sender_id":       message.SenderID,
			"weixin_sender_name":     message.SenderName,
			"weixin_group":           strconv.FormatBool(message.Group),
			"weixin_new_session":     strconv.FormatBool(newSession),
			"weixin_sender_verified": strconv.FormatBool(message.SenderVerified),
		},
	}
}

func protocolAttachments(attachments []InboundAttachment) []protocol.Attachment {
	out := make([]protocol.Attachment, 0, len(attachments))
	for _, attachment := range attachments {
		if strings.TrimSpace(attachment.ContentBase64) == "" {
			continue
		}
		out = append(out, protocol.Attachment{
			Type:     strings.TrimSpace(attachment.Type),
			Name:     strings.TrimSpace(attachment.Name),
			MIMEType: strings.TrimSpace(attachment.MIMEType),
			Content:  attachment.ContentBase64,
			Bytes:    attachment.Bytes,
			Metadata: map[string]any{
				"content_encoding": "base64",
				"source":           "weixin_ilink",
			},
		})
	}
	return out
}
