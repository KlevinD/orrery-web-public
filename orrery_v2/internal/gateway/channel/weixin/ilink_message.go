package weixin

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type ilinkWeixinMessage struct {
	MessageID    any               `json:"message_id"`
	ClientID     string            `json:"client_id"`
	Seq          any               `json:"seq"`
	FromUserID   string            `json:"from_user_id"`
	ToUserID     string            `json:"to_user_id"`
	SessionID    string            `json:"session_id"`
	GroupID      string            `json:"group_id"`
	ContextToken string            `json:"context_token"`
	ItemList     []ilinkWeixinItem `json:"item_list"`
	Items        []ilinkWeixinItem `json:"items"`
	ChatType     string            `json:"chat_type"`
}

type ilinkWeixinItem struct {
	Type      int                 `json:"type"`
	TextItem  *ilinkTextItem      `json:"text_item"`
	ImageItem *ilinkImageItem     `json:"image_item"`
	VoiceItem *ilinkVoiceItem     `json:"voice_item"`
	RefMsg    *ilinkReferenceItem `json:"ref_msg"`
}

type ilinkTextItem struct {
	Text string `json:"text"`
}

type ilinkVoiceItem struct {
	Text       string         `json:"text"`
	Media      *ilinkCDNMedia `json:"media"`
	EncodeType int            `json:"encode_type"`
	SampleRate int            `json:"sample_rate"`
	Playtime   int            `json:"playtime"`
}

type ilinkImageItem struct {
	Media      *ilinkCDNMedia `json:"media"`
	ThumbMedia *ilinkCDNMedia `json:"thumb_media"`
	AESKey     string         `json:"aeskey"`
}

type ilinkCDNMedia struct {
	EncryptQueryParam string `json:"encrypt_query_param"`
	AESKey            string `json:"aes_key"`
	FullURL           string `json:"full_url"`
}

type ilinkReferenceItem struct {
	Title       string           `json:"title"`
	MessageItem *ilinkWeixinItem `json:"message_item"`
}

func inboundFromWeixinMessage(msg ilinkWeixinMessage) InboundMessage {
	items := msg.ItemList
	if len(items) == 0 {
		items = msg.Items
	}
	inbound := InboundMessage{
		AccountID:    strings.TrimSpace(msg.ToUserID),
		MessageID:    messageIDString(msg.MessageID),
		ChatID:       firstNonBlank(msg.GroupID, msg.FromUserID, msg.SessionID, msg.ToUserID),
		ContextToken: strings.TrimSpace(msg.ContextToken),
		SenderID:     strings.TrimSpace(msg.FromUserID),
		Text:         textFromWeixinItems(items),
		Attachments:  imageAttachmentsFromWeixinItems(items),
		Group:        msg.GroupID != "" || strings.EqualFold(msg.ChatType, "group"),
	}
	for _, item := range items {
		if item.VoiceItem == nil {
			continue
		}
		inbound.InputModality = "voice"
		voice := item.VoiceItem
		if strings.TrimSpace(voice.Text) != "" {
			inbound.TranscriptSource = "platform"
			continue
		}
		if voice.Media != nil {
			inbound.Attachments = append(inbound.Attachments, InboundAttachment{
				Type: "audio", FullURL: voice.Media.FullURL,
				EncryptQueryParam: voice.Media.EncryptQueryParam, AESKey: voice.Media.AESKey,
			})
		}
	}
	if inbound.InputModality == "voice" {
		// A callback can contain both text and voice; do not discard either part.
		parts := make([]string, 0, len(items))
		for _, item := range items {
			if text := textFromWeixinItems([]ilinkWeixinItem{item}); text != "" {
				parts = append(parts, text)
			}
		}
		inbound.Text = strings.Join(parts, "\n")
	}
	return inbound
}

func imageAttachmentsFromWeixinItems(items []ilinkWeixinItem) []InboundAttachment {
	const maxImages = 4
	attachments := make([]InboundAttachment, 0, 1)
	appendImage := func(item ilinkWeixinItem) {
		if len(attachments) >= maxImages || item.ImageItem == nil || item.ImageItem.Media == nil {
			return
		}
		media := item.ImageItem.Media
		if strings.TrimSpace(media.FullURL) == "" && strings.TrimSpace(media.EncryptQueryParam) == "" {
			return
		}
		attachments = append(attachments, InboundAttachment{
			Type:              "image",
			FullURL:           strings.TrimSpace(media.FullURL),
			EncryptQueryParam: strings.TrimSpace(media.EncryptQueryParam),
			AESKey:            strings.TrimSpace(media.AESKey),
			AESKeyHex:         strings.TrimSpace(item.ImageItem.AESKey),
		})
	}
	for _, item := range items {
		appendImage(item)
	}
	if len(attachments) > 0 {
		return attachments
	}
	for _, item := range items {
		if item.RefMsg != nil && item.RefMsg.MessageItem != nil {
			appendImage(*item.RefMsg.MessageItem)
		}
	}
	return attachments
}

func textFromWeixinItems(items []ilinkWeixinItem) string {
	for _, item := range items {
		if item.TextItem != nil && strings.TrimSpace(item.TextItem.Text) != "" {
			if item.RefMsg != nil && item.RefMsg.MessageItem != nil {
				if reference := textFromWeixinItems([]ilinkWeixinItem{*item.RefMsg.MessageItem}); reference != "" {
					return "[引用: " + reference + "]\n" + strings.TrimSpace(item.TextItem.Text)
				}
			}
			return strings.TrimSpace(item.TextItem.Text)
		}
		if item.VoiceItem != nil && strings.TrimSpace(item.VoiceItem.Text) != "" {
			return strings.TrimSpace(item.VoiceItem.Text)
		}
	}
	return ""
}

func messageIDString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	default:
		return fmt.Sprint(typed)
	}
}

func logInfo(logger Logger, message string, metadata map[string]any) {
	if logger != nil {
		logger.Info(message, metadata)
	}
}

func logWarn(logger Logger, message string, metadata map[string]any) {
	if logger != nil {
		logger.Warn(message, metadata)
	}
}
