package discussionsession

import (
	"context"
	"sort"
	"strings"

	"orrery_v2/internal/platform/db"
)

func (r *Repository) SaveAttachment(ctx context.Context, attachment AttachmentRecord) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	payload, err := encodePayload(attachment)
	if err != nil {
		return err
	}
	return r.router.PutRecord(ctx, attachmentTable, attachment.AttachmentID, db.Record{
		"attachment_id":     attachment.AttachmentID,
		"conversation_id":   attachment.ConversationID,
		"message_id":        attachment.MessageID,
		"file_name":         attachment.FileName,
		"uploaded_by":       attachment.UploadedBy,
		"idempotency_key":   attachment.IdempotencyKey,
		"created_at":        formatTime(attachment.CreatedAt),
		conversationPayload: payload,
	})
}

func (r *Repository) GetAttachment(ctx context.Context, attachmentID string) (AttachmentRecord, error) {
	if r == nil || r.router == nil {
		return AttachmentRecord{}, ErrServiceDependency
	}
	record, ok, err := r.router.GetRecord(ctx, attachmentTable, attachmentID)
	if err != nil {
		return AttachmentRecord{}, err
	}
	if !ok {
		return AttachmentRecord{}, ErrNotFound
	}
	var out AttachmentRecord
	if err := decodePayload(record, &out); err != nil {
		return AttachmentRecord{}, err
	}
	return out, nil
}

func (r *Repository) BindAttachmentToMessage(ctx context.Context, attachmentID string, conversationID string, messageID string) (AttachmentRecord, error) {
	if r == nil || r.router == nil {
		return AttachmentRecord{}, ErrServiceDependency
	}
	record, err := r.GetAttachment(ctx, attachmentID)
	if err != nil {
		return AttachmentRecord{}, err
	}
	if record.ConversationID != conversationID || strings.TrimSpace(messageID) == "" {
		return AttachmentRecord{}, ErrInvalidRequest
	}
	record.MessageID = strings.TrimSpace(messageID)
	if err := r.SaveAttachment(ctx, record); err != nil {
		return AttachmentRecord{}, err
	}
	return record, nil
}

func (r *Repository) DeleteAttachment(ctx context.Context, attachmentID string) error {
	if r == nil || r.router == nil {
		return ErrServiceDependency
	}
	return r.router.DeleteRecord(ctx, attachmentTable, attachmentID)
}

func (r *Repository) ListAttachments(ctx context.Context, conversationID string) ([]AttachmentRecord, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	if _, ok, err := r.router.GetRecord(ctx, conversationTable, conversationID); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrNotFound
	}
	return r.listAttachments(ctx, conversationID)
}

func (r *Repository) listAttachments(ctx context.Context, conversationID string) ([]AttachmentRecord, error) {
	records, err := r.conversationRecords(ctx, attachmentTable, conversationID)
	if err != nil {
		return nil, err
	}
	out := make([]AttachmentRecord, 0)
	for _, record := range records {
		if got, _ := record["conversation_id"].(string); got != conversationID {
			continue
		}
		var item AttachmentRecord
		if err := decodePayload(record, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].AttachmentID < out[j].AttachmentID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (r *Repository) listAttachmentsByIdempotencyKey(ctx context.Context, idempotencyKey string) ([]AttachmentRecord, error) {
	if r == nil || r.router == nil {
		return nil, ErrServiceDependency
	}
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	if idempotencyKey == "" {
		return nil, nil
	}
	records, err := snapshot(ctx, r.router, attachmentTable)
	if err != nil {
		return nil, err
	}
	out := make([]AttachmentRecord, 0, 1)
	for _, record := range records {
		if got, _ := record["idempotency_key"].(string); strings.TrimSpace(got) != idempotencyKey {
			continue
		}
		var item AttachmentRecord
		if err := decodePayload(record, &item); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].AttachmentID > out[j].AttachmentID
		}
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}
