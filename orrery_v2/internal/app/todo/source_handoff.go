package todo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"time"

	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/platform/db"
)

type TodoSource struct {
	LastAttemptAt  time.Time `json:"last_attempt_at,omitempty"`
	CanOpen        bool      `json:"can_open,omitempty"`
	ConversationID string    `json:"conversation_id"`
	Result         string    `json:"result,omitempty"`
	ResultBy       string    `json:"result_by,omitempty"`
	DeliveryStatus string    `json:"delivery_status"`
}

type todoSourceNotice struct {
	AttachmentIDs []string `json:"attachment_ids,omitempty"`
	Key           string   `json:"key"`
	Content       string   `json:"content"`
}

type TodoSourceDiscussionPort interface {
	GetConversation(context.Context, discussionsession.ViewerRef, string) (discussionsession.ConversationDTO, error)
	AppendNotice(context.Context, discussionsession.ViewerRef, discussionsession.AppendNoticeCommand) (discussionsession.ConversationDTO, error)
}

func sourceViewer(item TodoItem) discussionsession.ViewerRef {
	return discussionsession.ViewerRef{TenantID: item.TenantID, ViewerID: firstNonEmptyText(item.CreatedBySubjectID, item.CreatedBy), IdentityIDs: []string{item.CreatedBy}, ViewerKind: discussionsession.ViewerKind(item.CreatedByKind)}
}

func (s *Service) prepareTodoSource(ctx context.Context, item *TodoItem, conversationID string) error {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil
	}
	if s.sourceDiscussion == nil {
		return ErrServiceDependency
	}
	if item.AssigneeKind != AssigneeKindHuman || item.AssigneeID == "" || item.Description == "" || item.AutomationMode != TodoAutomationModeManual || item.WorkKind != TodoWorkKindGeneral {
		return fmt.Errorf("%w: a conversation handoff needs a human assignee, context and manual mode", ErrInvalidRequest)
	}
	// Validate the caller's existing membership. Handoffs never grant conversation access.
	if _, err := s.sourceDiscussion.GetConversation(ctx, sourceViewer(*item), conversationID); err != nil {
		return ErrForbidden
	}
	item.Source = &TodoSource{ConversationID: conversationID}
	queueSourceNotice(item, "Human action requested", item.Description)
	return nil
}

func queueSourceNotice(item *TodoItem, label, result string) {
	source := *item.Source
	source.DeliveryStatus = "pending"
	item.Source = &source
	// Content is captured with the mutation so retries cannot change an already delivered event.
	content := fmt.Sprintf("%s: %s\n\nAssigned to: %s\n\n%s\n\n[Open todo](/todos/%s)", label, item.Title, firstNonEmptyText(item.AssigneeName, item.AssigneeID), result, url.PathEscape(item.TodoID))
	if item.Status == TodoStatusDone && source.ResultBy != "" {
		content += "\n\nProcessed by: " + source.ResultBy
	}
	notice := todoSourceNotice{Key: fmt.Sprintf("todo-source:%s:%s:%d", item.TenantID, item.TodoID, item.Version), Content: content}
	if item.Status == TodoStatusDone && item.Feedback != nil {
		for _, attachment := range item.Feedback.Attachments {
			notice.AttachmentIDs = append(notice.AttachmentIDs, attachment.AttachmentID)
		}
	}
	item.sourceNotices = append(append([]todoSourceNotice(nil), item.sourceNotices...), notice)
}

func (s *Service) prepareSourceTransition(viewer ViewerRef, item *TodoItem, status TodoStatus, result string) error {
	if item.Source == nil {
		return nil
	}
	if status == TodoStatusDone {
		if viewer.ViewerKind != ViewerKindUser && viewer.ViewerKind != ViewerKindHuman {
			return ErrForbidden
		}
		result = strings.TrimSpace(result)
		if (result == "" && (item.Feedback == nil || len(item.Feedback.Attachments) == 0)) || len(result) > 32000 {
			return fmt.Errorf("%w: a human processing result is required", ErrInvalidRequest)
		}
		source := *item.Source
		source.Result, source.ResultBy = result, viewer.ViewerID
		item.Source = &source
	} else if !isTerminalTodoStatus(status) {
		source := *item.Source
		source.Result, source.ResultBy = "", ""
		item.Source = &source
	}
	queueSourceNotice(item, "Human todo status: "+string(status), strings.TrimSpace(result))
	return nil
}

// Delivery is passive and idempotent. A durable pending event survives errors and restarts.
func (s *Service) deliverTodoSource(ctx context.Context, item TodoItem) TodoItem {
	if item.Source == nil || len(item.sourceNotices) == 0 || s.sourceDiscussion == nil {
		return item
	}
	for _, notice := range item.sourceNotices {
		err := s.deliverSourceNotice(ctx, item, notice)
		if err != nil {
			next := item
			source := *item.Source
			source.LastAttemptAt = s.clock.Now().UTC()
			next.Source = &source
			next.Version++
			if s.repo.UpdateTodo(ctx, next, item.Version) == nil {
				return next
			}
			return item
		}
	}
	next := item
	source := *item.Source
	source.DeliveryStatus = "delivered"
	source.LastAttemptAt = s.clock.Now().UTC()
	next.Source, next.sourceNotices = &source, nil
	next.Version++
	if err := s.repo.UpdateTodo(ctx, next, item.Version); err != nil {
		return item
	}
	return next
}

func (s *Service) deliverSourceNotice(ctx context.Context, item TodoItem, notice todoSourceNotice) error {
	var attachmentIDs []string
	for _, id := range notice.AttachmentIDs {
		port, ok := s.sourceDiscussion.(interface {
			UploadAttachment(context.Context, discussionsession.ViewerRef, discussionsession.UploadAttachmentCommand) (discussionsession.AttachmentDTO, error)
		})
		if !ok {
			return ErrServiceDependency
		}
		attachment, reader, err := s.openTodoAttachment(ctx, item, id)
		if err != nil {
			return err
		}
		uploaded, err := port.UploadAttachment(ctx, sourceViewer(item), discussionsession.UploadAttachmentCommand{
			ConversationID: item.Source.ConversationID, FileName: attachment.FileName, ContentType: attachment.ContentType,
			SizeBytes: attachment.SizeBytes, Reader: reader, IdempotencyKey: notice.Key + ":" + id,
		})
		_ = reader.Close()
		if err != nil {
			return err
		}
		attachmentIDs = append(attachmentIDs, uploaded.AttachmentID)
	}
	_, err := s.sourceDiscussion.AppendNotice(ctx, sourceViewer(item), discussionsession.AppendNoticeCommand{
		ConversationID: item.Source.ConversationID, Content: notice.Content, IdempotencyKey: notice.Key, AttachmentIDs: attachmentIDs,
		Metadata: map[string]string{"kind": "human_todo", "todo_id": item.TodoID},
	})
	return err
}

func (s *Service) reconcileTodoSourceNotices(ctx context.Context) error {
	if s.sourceDiscussion == nil {
		return nil
	}
	store, table, err := s.repo.router.StoreForTable(todoTable)
	if err != nil {
		return err
	}
	expression := recordTextExpr(store.Dialect(), "source_notice_pending")
	rows, err := store.Query(ctx, "SELECT payload_json FROM platform_records WHERE table_name = ? AND "+expression+" = ? ORDER BY "+recordTextExpr(store.Dialect(), "source_notice_attempted_at")+", record_key LIMIT 100", table, "true")
	if err != nil {
		return err
	}
	var items []TodoItem
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			_ = rows.Close()
			return err
		}
		var record db.Record
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			_ = rows.Close()
			return err
		}
		item, err := todoFromRecord(record, "", "")
		if err != nil {
			_ = rows.Close()
			return err
		}
		items = append(items, item)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, item := range items {
		s.deliverTodoSource(ctx, item)
	}
	return nil
}

// ReconcileSourceNotices retries conversation handoffs independently of Todo automation.
func (s *Service) ReconcileSourceNotices(ctx context.Context) error {
	return s.reconcileTodoSourceNotices(ctx)
}
