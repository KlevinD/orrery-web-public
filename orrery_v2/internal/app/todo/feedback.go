package todo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"orrery_v2/internal/app/discussionsession"
)

const MaxTodoAttachmentBytes int64 = 20 << 20
const MaxTodoAttachments = 10

type TodoAttachment struct {
	AttachmentID string `json:"attachment_id"`
	FileName     string `json:"file_name"`
	ContentType  string `json:"content_type"`
	SizeBytes    int64  `json:"size_bytes"`
	DownloadURL  string `json:"download_url"`
}

type todoAttachmentRecord struct {
	TodoAttachment
	StoragePath string `json:"storage_path"`
}

type TodoFeedback struct {
	Body        string           `json:"body"`
	Attachments []TodoAttachment `json:"attachments,omitempty"`
	AuthorID    string           `json:"author_id"`
	CreatedAt   time.Time        `json:"created_at"`
}

func humanManualTodo(item TodoItem) bool {
	return item.AssigneeKind == AssigneeKindHuman && item.WorkKind == TodoWorkKindGeneral && item.AutomationMode == TodoAutomationModeManual
}

func todoAttachmentWorkspace(item TodoItem) string {
	return fmt.Sprintf("todo-%x", sha256.Sum256([]byte(item.TenantID+"\x00"+item.TodoID)))
}

func (s *Service) UploadTodoAttachment(ctx context.Context, viewer ViewerRef, todoID, name string, reader io.Reader) (TodoAttachment, error) {
	viewer, item, err := s.todoForMutation(ctx, viewer, todoID)
	if err != nil {
		return TodoAttachment{}, err
	}
	if s.attachments == nil {
		return TodoAttachment{}, ErrServiceDependency
	}
	if !humanManualTodo(item) || isTerminalTodoStatus(item.Status) || (viewer.ViewerKind != ViewerKindUser && viewer.ViewerKind != ViewerKindHuman) {
		return TodoAttachment{}, ErrForbidden
	}
	name = filepath.Base(strings.ReplaceAll(strings.TrimSpace(name), "\\", "/"))
	if reader == nil || name == "" || name == "." || len(name) > 255 || len(item.Attachments) >= MaxTodoAttachments {
		return TodoAttachment{}, ErrInvalidRequest
	}
	data, err := io.ReadAll(io.LimitReader(reader, MaxTodoAttachmentBytes+1))
	if err != nil {
		return TodoAttachment{}, err
	}
	if len(data) == 0 || int64(len(data)) > MaxTodoAttachmentBytes {
		return TodoAttachment{}, fmt.Errorf("%w: attachment must be between 1 byte and 20 MiB", ErrInvalidRequest)
	}
	id, err := s.ids.NewIDContext()
	if err != nil {
		return TodoAttachment{}, err
	}
	stored, err := s.attachments.SaveAttachment(ctx, todoAttachmentWorkspace(item), "", id, name, bytes.NewReader(data))
	if err != nil {
		return TodoAttachment{}, err
	}
	a := TodoAttachment{AttachmentID: id, FileName: name, ContentType: http.DetectContentType(data), SizeBytes: stored.Size, DownloadURL: "/api/todos/" + url.PathEscape(item.TodoID) + "/attachments/" + url.PathEscape(id)}
	item.Attachments = append(append([]TodoAttachment(nil), item.Attachments...), a)
	item.attachmentRecords = append(append([]todoAttachmentRecord(nil), item.attachmentRecords...), todoAttachmentRecord{TodoAttachment: a, StoragePath: stored.Path})
	expected := item.Version
	item.Version++
	item.UpdatedAt = s.clock.Now().UTC()
	if err := s.repo.UpdateTodo(ctx, item, expected); err != nil {
		_ = s.removeAttachmentFile(context.WithoutCancel(ctx), item, item.attachmentRecords[len(item.attachmentRecords)-1])
		return TodoAttachment{}, err
	}
	return a, nil
}

func (s *Service) removeAttachmentFile(ctx context.Context, item TodoItem, record todoAttachmentRecord) error {
	store, ok := s.attachments.(interface {
		RemoveAttachment(context.Context, discussionsession.AttachmentRecord) error
	})
	if !ok {
		return ErrServiceDependency
	}
	return store.RemoveAttachment(ctx, discussionsession.AttachmentRecord{ConversationID: todoAttachmentWorkspace(item), StoragePath: record.StoragePath})
}

func (s *Service) DeleteTodoAttachment(ctx context.Context, viewer ViewerRef, todoID, attachmentID string) error {
	viewer, item, err := s.todoForMutation(ctx, viewer, todoID)
	if err != nil {
		return err
	}
	if !humanManualTodo(item) || isTerminalTodoStatus(item.Status) || (viewer.ViewerKind != ViewerKindUser && viewer.ViewerKind != ViewerKindHuman) {
		return ErrForbidden
	}
	for _, notice := range item.sourceNotices {
		for _, id := range notice.AttachmentIDs {
			if id == attachmentID {
				return ErrConflict
			}
		}
	}
	if item.Feedback != nil {
		for _, a := range item.Feedback.Attachments {
			if a.AttachmentID == attachmentID {
				return ErrConflict
			}
		}
	}
	var target *todoAttachmentRecord
	records := make([]todoAttachmentRecord, 0, len(item.attachmentRecords))
	attachments := make([]TodoAttachment, 0, len(item.Attachments))
	for _, record := range item.attachmentRecords {
		if record.AttachmentID == attachmentID {
			copy := record
			target = &copy
			continue
		}
		records = append(records, record)
		attachments = append(attachments, record.TodoAttachment)
	}
	if target == nil {
		return ErrNotFound
	}
	expected := item.Version
	item.attachmentRecords, item.Attachments = records, attachments
	item.Version++
	item.UpdatedAt = s.clock.Now().UTC()
	if err := s.repo.UpdateTodo(ctx, item, expected); err != nil {
		return err
	}
	// Metadata revocation comes first; a failed cleanup cannot leave the file downloadable.
	_ = s.removeAttachmentFile(context.WithoutCancel(ctx), item, *target)
	return nil
}

func (s *Service) OpenTodoAttachment(ctx context.Context, viewer ViewerRef, todoID, attachmentID string) (TodoAttachment, io.ReadCloser, error) {
	item, err := s.GetTodo(ctx, viewer, todoID)
	if err != nil {
		return TodoAttachment{}, nil, err
	}
	return s.openTodoAttachment(ctx, item, attachmentID)
}

func (s *Service) openTodoAttachment(ctx context.Context, item TodoItem, attachmentID string) (TodoAttachment, io.ReadCloser, error) {
	if s.attachments == nil {
		return TodoAttachment{}, nil, ErrServiceDependency
	}
	for _, record := range item.attachmentRecords {
		if record.AttachmentID != attachmentID {
			continue
		}
		file, err := s.attachments.OpenAttachment(ctx, discussionsession.AttachmentRecord{ConversationID: todoAttachmentWorkspace(item), StoragePath: record.StoragePath}, "")
		return record.TodoAttachment, file, err
	}
	return TodoAttachment{}, nil, ErrNotFound
}

func (s *Service) prepareFeedback(viewer ViewerRef, item *TodoItem, req MoveTodoRequest) error {
	if !isTerminalTodoStatus(item.Status) {
		item.Feedback = nil
	}
	if req.Status != TodoStatusDone || (strings.TrimSpace(req.Result) == "" && len(req.ResultAttachmentIDs) == 0) {
		return nil
	}
	if !humanManualTodo(*item) || (viewer.ViewerKind != ViewerKindUser && viewer.ViewerKind != ViewerKindHuman) {
		return ErrForbidden
	}
	if len(req.Result) > 32000 || len(req.ResultAttachmentIDs) > MaxTodoAttachments {
		return ErrInvalidRequest
	}
	feedback := TodoFeedback{Body: strings.TrimSpace(req.Result), AuthorID: viewer.ViewerID, CreatedAt: s.clock.Now().UTC()}
	seen := map[string]bool{}
	for _, id := range req.ResultAttachmentIDs {
		if seen[id] {
			return ErrInvalidRequest
		}
		seen[id] = true
		found := false
		for _, a := range item.Attachments {
			if a.AttachmentID == id {
				feedback.Attachments = append(feedback.Attachments, a)
				found = true
				break
			}
		}
		if !found {
			return ErrInvalidRequest
		}
	}
	item.Feedback = &feedback
	return nil
}
