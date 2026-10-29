package todo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	"orrery_v2/internal/app/authorization"
	"orrery_v2/internal/app/discussionsession"
	"orrery_v2/internal/app/rebac"
	"orrery_v2/internal/platform/testkit"
)

type feedbackDiscussion struct {
	sourceDiscussionStub
	files         map[string]string
	attachmentIDs []string
}

func (p *feedbackDiscussion) UploadAttachment(_ context.Context, _ discussionsession.ViewerRef, cmd discussionsession.UploadAttachmentCommand) (discussionsession.AttachmentDTO, error) {
	if p.unavailable {
		return discussionsession.AttachmentDTO{}, errors.New("offline")
	}
	data, err := io.ReadAll(cmd.Reader)
	if err != nil {
		return discussionsession.AttachmentDTO{}, err
	}
	p.files[cmd.IdempotencyKey] = string(data)
	return discussionsession.AttachmentDTO{AttachmentID: cmd.IdempotencyKey}, nil
}

func (p *feedbackDiscussion) AppendNotice(ctx context.Context, v discussionsession.ViewerRef, cmd discussionsession.AppendNoticeCommand) (discussionsession.ConversationDTO, error) {
	p.attachmentIDs = cmd.AttachmentIDs
	return p.sourceDiscussionStub.AppendNotice(ctx, v, cmd)
}

func TestTodoFeedbackFilesAuthorizationPersistenceAndSourceDelivery(t *testing.T) {
	for _, source := range []string{"", "conversation-a"} {
		t.Run("source="+source, func(t *testing.T) {
			env := testkit.New(t)
			defer env.Close()
			ctx := context.Background()
			service := newServiceForTest(NewRepository(env.DBRouter))
			store := discussionsession.NewLocalAttachmentStore(t.TempDir())
			service.attachments = store
			access := authorization.NewService(authorization.NewReBACAdapter(rebac.NewMemoryStore(), nil))
			service.SetAuthorization(access, authorization.NewRelationProjector(access))
			port := &feedbackDiscussion{sourceDiscussionStub: sourceDiscussionStub{notices: map[string]string{}}, files: map[string]string{}}
			service.sourceDiscussion = port
			agent := authorizedTodoViewer("tenant-a", "agent-a", "agent-a", "todo.read", "todo.write")
			agent.ViewerKind = ViewerKindAgent
			human := authorizedTodoViewer("tenant-a", "ding", "human-a", "todo.read", "todo.write")
			outsider := authorizedTodoViewer("tenant-a", "other", "other", "todo.read", "todo.write")
			item, err := service.CreateTodo(ctx, agent, CreateTodoRequest{Title: "Confirm", Description: "Provide receipt", Status: TodoStatusPlanned, AssigneeID: "human-a", AssigneeKind: AssigneeKindHuman, SourceConversationID: source})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.UploadTodoAttachment(ctx, outsider, item.TodoID, "secret.txt", strings.NewReader("x")); err == nil {
				t.Fatal("outsider uploaded")
			}
			if _, err := service.UploadTodoAttachment(ctx, agent, item.TodoID, "secret.txt", strings.NewReader("x")); err == nil {
				t.Fatal("Agent submitted human attachment")
			}
			file, err := service.UploadTodoAttachment(ctx, human, item.TodoID, "../../receipt.txt", strings.NewReader("approved receipt"))
			if err != nil {
				t.Fatal(err)
			}
			if file.FileName != "receipt.txt" || file.SizeBytes != 16 {
				t.Fatalf("file=%+v", file)
			}
			for _, denied := range []ViewerRef{outsider, authorizedTodoViewer("tenant-b", "ding", "human-a", "todo.read")} {
				if _, r, err := service.OpenTodoAttachment(ctx, denied, item.TodoID, file.AttachmentID); err == nil {
					r.Close()
					t.Fatal("unauthorized download")
				}
			}
			if _, err := service.CompleteTodo(ctx, human, CompleteTodoRequest{TodoID: item.TodoID, ResultAttachmentIDs: []string{"another-todo-file"}}); err == nil {
				t.Fatal("foreign attachment accepted")
			}
			port.unavailable = true
			completed, err := service.CompleteTodo(ctx, human, CompleteTodoRequest{TodoID: item.TodoID, ResultAttachmentIDs: []string{file.AttachmentID}})
			if err != nil {
				t.Fatal(err)
			}
			if completed.Status != TodoStatusDone || completed.Feedback == nil || len(completed.Feedback.Attachments) != 1 {
				t.Fatalf("completed=%+v", completed)
			}
			if _, err := service.UploadTodoAttachment(ctx, human, item.TodoID, "late.txt", strings.NewReader("late")); err == nil {
				t.Fatal("terminal upload")
			}
			// New service instance proves both file metadata and pending delivery survive restart.
			service = newServiceForTest(service.repo)
			service.attachments, service.sourceDiscussion = store, port
			service.SetAuthorization(access, authorization.NewRelationProjector(access))
			_, reader, err := service.OpenTodoAttachment(ctx, human, item.TodoID, file.AttachmentID)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(reader)
			reader.Close()
			if err != nil || string(data) != "approved receipt" {
				t.Fatalf("data=%s err=%v", data, err)
			}
			port.unavailable = false
			if err := service.ReconcileSourceNotices(ctx); err != nil {
				t.Fatal(err)
			}
			if source != "" && (len(port.attachmentIDs) != 1 || len(port.files) != 1) {
				t.Fatalf("source files=%+v ids=%+v", port.files, port.attachmentIDs)
			}
			got, err := service.GetTodo(ctx, human, item.TodoID)
			if err != nil || got.Feedback == nil {
				t.Fatalf("feedback=%+v err=%v", got.Feedback, err)
			}
			raw, _ := json.Marshal(got)
			if bytes.Contains(raw, []byte("storage_path")) || bytes.Contains(raw, []byte("attachmentRecords")) {
				t.Fatal("internal storage exposed")
			}
		})
	}
}

func TestTodoAttachmentRejectsEmptyOversizeAndForeignIDs(t *testing.T) {
	env := testkit.New(t)
	defer env.Close()
	service := newServiceForTest(NewRepository(env.DBRouter))
	service.attachments = discussionsession.NewLocalAttachmentStore(t.TempDir())
	viewer := testViewer("tenant-a", "human-a")
	ctx := context.Background()
	item, err := service.CreateTodo(ctx, viewer, CreateTodoRequest{Title: "Test", AssigneeID: "human-a", AssigneeKind: AssigneeKindHuman})
	if err != nil {
		t.Fatal(err)
	}
	for _, data := range [][]byte{nil, make([]byte, MaxTodoAttachmentBytes+1)} {
		if _, err := service.UploadTodoAttachment(ctx, viewer, item.TodoID, "file.bin", bytes.NewReader(data)); err == nil {
			t.Fatal("invalid size accepted")
		}
	}
	got, err := service.GetTodo(ctx, viewer, item.TodoID)
	if err != nil || len(got.Attachments) != 0 || got.Version != item.Version {
		t.Fatalf("failed upload mutated todo: %+v %v", got, err)
	}
	file, err := service.UploadTodoAttachment(ctx, viewer, item.TodoID, "draft.txt", strings.NewReader("draft"))
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteTodoAttachment(ctx, viewer, item.TodoID, file.AttachmentID); err != nil {
		t.Fatal(err)
	}
	if _, r, err := service.OpenTodoAttachment(ctx, viewer, item.TodoID, file.AttachmentID); err == nil {
		r.Close()
		t.Fatal("removed file still accessible")
	}
}
