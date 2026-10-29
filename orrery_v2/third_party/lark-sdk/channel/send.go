package channel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/larksuite/oapi-sdk-go/v3/channel/normalize"
	"github.com/larksuite/oapi-sdk-go/v3/channel/outbound"
	"github.com/larksuite/oapi-sdk-go/v3/channel/types"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"

	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type interactiveMarkdownCardPayload struct {
	Schema string                         `json:"schema"`
	Config interactiveMarkdownCardConfig  `json:"config"`
	Header *interactiveMarkdownCardHeader `json:"header,omitempty"`
	Body   interactiveMarkdownCardBody    `json:"body"`
}

type interactiveMarkdownCardConfig struct {
	WidthMode string `json:"width_mode"`
}

type interactiveMarkdownCardHeader struct {
	Template string                       `json:"template"`
	Title    interactiveMarkdownCardTitle `json:"title"`
}

type interactiveMarkdownCardTitle struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
}

type interactiveMarkdownCardBody struct {
	Elements []interactiveMarkdownCardElement `json:"elements"`
}

type interactiveMarkdownCardElement struct {
	Tag     string `json:"tag"`
	Content string `json:"content"`
}

func (c *channelImpl) Send(ctx context.Context, input *types.SendInput) (*types.SendResult, error) {
	if input == nil {
		return nil, fmt.Errorf("input cannot be nil")
	}

	receiveIDType := "open_id"
	receiveID := input.UserID
	if input.ReceiveID != "" {
		receiveID = input.ReceiveID
		t, err := outbound.DetectReceiveIdType(receiveID)
		if err == nil {
			receiveIDType = string(t)
		}
	} else if input.ChatID != "" {
		receiveIDType = "chat_id"
		receiveID = input.ChatID
	}

	if receiveID == "" {
		return nil, fmt.Errorf("ReceiveID, ChatID, or UserID must be provided")
	}

	// 1. Process local files first
	if input.ImagePath != "" && input.ImageKey == "" {
		// Upload image
		imageKey, err := c.uploader.UploadImagePath(ctx, "message", input.ImagePath)
		if err != nil {
			return nil, fmt.Errorf("failed to upload image: %v", err)
		}
		input.ImageKey = imageKey
	}

	if input.FilePath != "" && input.FileKey == "" {
		// Upload file
		fileKey, err := c.uploader.UploadFilePath(ctx, "stream", input.FilePath)
		if err != nil {
			return nil, fmt.Errorf("failed to upload file: %v", err)
		}
		input.FileKey = fileKey
	}

	if input.Media != nil && input.AudioKey == "" && input.VideoKey == "" && input.FileKey == "" && input.ImageKey == "" {
		res, err := c.uploader.UploadMedia(ctx, input.Media, nil) // Assume default SsrfGuardOptions or nil
		if err != nil {
			return nil, fmt.Errorf("failed to upload media: %v", err)
		}
		if res.Kind == types.MediaKindImage {
			input.ImageKey = res.FileKey
		} else if res.Kind == types.MediaKindAudio {
			input.AudioKey = res.FileKey
		} else if res.Kind == types.MediaKindVideo {
			input.VideoKey = res.FileKey
		} else {
			input.FileKey = res.FileKey
		}
	}

	// 2. Determine MsgType and Content
	msgType := input.MsgType
	var content string

	if input.ImageKey != "" {
		msgType = "image"
		contentMap := map[string]string{"image_key": input.ImageKey}
		b, _ := json.Marshal(contentMap)
		content = string(b)
	} else if input.AudioKey != "" {
		msgType = "audio"
		contentMap := map[string]string{"file_key": input.AudioKey}
		b, _ := json.Marshal(contentMap)
		content = string(b)
	} else if input.VideoKey != "" {
		msgType = "media"
		contentMap := map[string]string{"file_key": input.VideoKey}
		b, _ := json.Marshal(contentMap)
		content = string(b)
	} else if input.FileKey != "" {
		msgType = "file"
		contentMap := map[string]string{"file_key": input.FileKey}
		b, _ := json.Marshal(contentMap)
		content = string(b)
	} else if input.Card != "" {
		msgType = "interactive"
		content = input.Card
	} else if input.Post != "" {
		msgType = "post"
		content = input.Post
	} else if input.ShareChatID != "" {
		msgType = "share_chat"
		contentMap := map[string]string{"chat_id": input.ShareChatID}
		b, _ := json.Marshal(contentMap)
		content = string(b)
	} else if input.ShareUserID != "" {
		msgType = "share_user"
		// 飞书的 share_user 类型里，如果是 ou_ 开头说明是 open_id，通常底层需要显式传 user_id 作为通用键
		// 但为了保险，我们可以把它赋值给 user_id
		contentMap := map[string]string{"user_id": input.ShareUserID}
		b, _ := json.Marshal(contentMap)
		content = string(b)
	} else if input.StickerFileKey != "" {
		msgType = "sticker"
		contentMap := map[string]string{"file_key": input.StickerFileKey}
		b, _ := json.Marshal(contentMap)
		content = string(b)
	} else if input.Markdown != "" {
		// For Markdown, we split by code fences and newlines to avoid breaking syntax
		chunks := outbound.SplitWithCodeFences(input.Markdown, c.config.Outbound.TextChunkLimit)
		var ids []string
		var firstChatID string
		for i, chunk := range chunks {
			var mentions []types.Mention
			if i == 0 {
				mentions = input.Mentions
			}
			var chunkMsgType string
			var chunkContent string
			var err error
			if input.MsgType == "interactive" {
				chunkMsgType = "interactive"
				chunkContent, err = interactiveMarkdownCard(input.Title, chunk)
			} else {
				chunkMsgType = "post"
				chunkContent, err = normalize.SimpleMarkdownToPost(input.Title, chunk, mentions)
			}
			if err != nil {
				return nil, fmt.Errorf("failed to format markdown: %v", err)
			}
			id, chatID, err := c.sendOneWithFallback(ctx, receiveIDType, receiveID, chunkMsgType, chunkContent, input, chunkIdempotencyKey(input.IdempotencyKey, i))
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
			if i == 0 {
				firstChatID = chatID
			}
		}
		res := &types.SendResult{
			MessageID: ids[0],
			ChatID:    firstChatID,
		}
		if len(ids) > 1 {
			res.ChunkIDs = ids
		}
		return res, nil

	} else if input.Text != "" {
		msgType = "text"

		prefix := normalize.ComposeMentionsTextPrefix(input.Mentions)
		fullText := prefix + input.Text

		// For text, we can also split simply by length
		chunks := splitPlain(fullText, c.config.Outbound.TextChunkLimit)
		var ids []string
		var firstChatID string
		for i, chunk := range chunks {
			contentMap := map[string]string{"text": chunk}
			b, _ := json.Marshal(contentMap)
			id, chatID, err := c.sendOneWithFallback(ctx, receiveIDType, receiveID, "text", string(b), input, chunkIdempotencyKey(input.IdempotencyKey, i))
			if err != nil {
				return nil, err
			}
			ids = append(ids, id)
			if i == 0 {
				firstChatID = chatID
			}
		}
		res := &types.SendResult{
			MessageID: ids[0],
			ChatID:    firstChatID,
		}
		if len(ids) > 1 {
			res.ChunkIDs = ids
		}
		return res, nil
	}

	if msgType == "" || content == "" {
		return nil, fmt.Errorf("no valid message content provided")
	}

	// 3. Send single message for other types
	id, chatID, err := c.sendOneWithFallback(ctx, receiveIDType, receiveID, msgType, content, input, chunkIdempotencyKey(input.IdempotencyKey, 0))
	if err != nil {
		return nil, err
	}

	return &types.SendResult{
		MessageID: id,
		ChatID:    chatID,
	}, nil
}

func interactiveMarkdownCard(title string, markdown string) (string, error) {
	card := interactiveMarkdownCardPayload{
		Schema: "2.0",
		Config: interactiveMarkdownCardConfig{WidthMode: "fill"},
		Body: interactiveMarkdownCardBody{Elements: []interactiveMarkdownCardElement{{
			Tag:     "markdown",
			Content: markdown,
		}}},
	}
	if title = strings.TrimSpace(title); title != "" {
		card.Header = &interactiveMarkdownCardHeader{
			Template: "blue",
			Title: interactiveMarkdownCardTitle{
				Tag:     "plain_text",
				Content: title,
			},
		}
	}
	payload, err := json.Marshal(card)
	if err != nil {
		return "", err
	}
	return string(payload), nil
}

func (c *channelImpl) sendOneWithFallback(ctx context.Context, idType, id, msgType, content string, input *types.SendInput, idempotencyKey string) (string, string, error) {
	msgID, chatID, err := c.rawSendWithRetry(ctx, idType, id, msgType, content, input.ReplyMessageID, idempotencyKey)
	if err == nil {
		return msgID, chatID, nil
	}

	fce := types.ClassifyError(err)

	// Fallback 1: Reply target gone -> remove ReplyMessageID and resend
	if fce.Code == types.ErrCodeTargetRevoked && input.ReplyMessageID != "" {
		input.ReplyMessageID = "" // downgrade to new message
		return c.sendOneWithFallback(ctx, idType, id, msgType, content, input, idempotencyKey)
	}

	// Fallback 2: Format error -> downgrade to text
	if fce.Code == types.ErrCodeFormatError && msgType != "text" {
		fallbackText := ""
		if input.Markdown != "" {
			fallbackText = input.Markdown
		} else if input.Text != "" {
			fallbackText = input.Text
		} else {
			return "", "", err
		}

		prefix := normalize.ComposeMentionsTextPrefix(input.Mentions)
		fullText := prefix + fallbackText

		contentMap := map[string]string{"text": fullText}
		b, _ := json.Marshal(contentMap)

		return c.rawSendWithRetry(ctx, idType, id, "text", string(b), input.ReplyMessageID, idempotencyKey)
	}

	return "", "", err
}

func chunkIdempotencyKey(base string, index int) string {
	if base == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", base, index)))
	return "gw_" + hex.EncodeToString(sum[:16])
}

func splitPlain(text string, limit int) []string {
	if len(text) <= limit {
		return []string{text}
	}
	var out []string
	runes := []rune(text)
	for i := 0; i < len(runes); i += limit {
		end := i + limit
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[i:end]))
	}
	return out
}

func (c *channelImpl) rawSendWithRetry(ctx context.Context, idType, id, msgType, content, replyMessageID, idempotencyKey string) (string, string, error) {
	var op func(attempt int) (interface{}, error)

	if replyMessageID != "" {
		bodyBuilder := larkim.NewReplyMessageReqBodyBuilder().MsgType(msgType).Content(content)
		if idempotencyKey != "" {
			bodyBuilder.Uuid(idempotencyKey)
		}
		req := larkim.NewReplyMessageReqBuilder().
			MessageId(replyMessageID).
			Body(bodyBuilder.Build()).
			Build()

		op = func(attempt int) (interface{}, error) {
			resp, err := c.client.Im.V1.Message.Reply(ctx, req)
			if err != nil {
				return nil, err
			}
			if !resp.Success() {
				apiErr := &larkcore.CodeError{Code: resp.Code, Msg: resp.Msg}
				return nil, apiErr
			}
			if resp.Data == nil || resp.Data.MessageId == nil || strings.TrimSpace(*resp.Data.MessageId) == "" {
				return nil, fmt.Errorf("Feishu reply response contained no message receipt")
			}
			return resp, nil
		}
	} else {
		bodyBuilder := larkim.NewCreateMessageReqBodyBuilder().ReceiveId(id).MsgType(msgType).Content(content)
		if idempotencyKey != "" {
			bodyBuilder.Uuid(idempotencyKey)
		}
		req := larkim.NewCreateMessageReqBuilder().
			ReceiveIdType(idType).
			Body(bodyBuilder.Build()).
			Build()

		op = func(attempt int) (interface{}, error) {
			resp, err := c.client.Im.V1.Message.Create(ctx, req)
			if err != nil {
				return nil, err
			}
			if !resp.Success() {
				apiErr := &larkcore.CodeError{Code: resp.Code, Msg: resp.Msg}
				return nil, apiErr
			}
			if resp.Data == nil || resp.Data.MessageId == nil || strings.TrimSpace(*resp.Data.MessageId) == "" {
				return nil, fmt.Errorf("Feishu create response contained no message receipt")
			}
			return resp, nil
		}
	}

	res, err := outbound.Retry(ctx, op, &outbound.RetryOptions{
		MaxAttempts: c.config.Outbound.Retry.MaxAttempts,
		BaseDelay:   c.config.Outbound.Retry.BaseDelayMs,
	})
	if err != nil {
		return "", "", types.ClassifyError(err)
	}

	chatID := ""
	if replyMessageID != "" {
		resp := res.(*larkim.ReplyMessageResp)
		if resp.Data.ChatId != nil {
			chatID = *resp.Data.ChatId
		}
		return *resp.Data.MessageId, chatID, nil
	} else {
		resp := res.(*larkim.CreateMessageResp)
		if resp.Data.ChatId != nil {
			chatID = *resp.Data.ChatId
		}
		return *resp.Data.MessageId, chatID, nil
	}
}
