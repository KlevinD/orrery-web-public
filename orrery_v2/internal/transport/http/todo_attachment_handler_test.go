package httptransport

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"orrery_v2/internal/app/todo"
)

type attachmentTodoService struct {
	fakeTodoService
	fileName, content string
	deleted           bool
}

func (s *attachmentTodoService) UploadTodoAttachment(_ context.Context, _ todo.ViewerRef, id, name string, r io.Reader) (todo.TodoAttachment, error) {
	data, err := io.ReadAll(r)
	s.fileName, s.content = name, string(data)
	return todo.TodoAttachment{AttachmentID: "att", FileName: name, ContentType: "text/plain", SizeBytes: int64(len(data))}, err
}
func (s *attachmentTodoService) OpenTodoAttachment(_ context.Context, _ todo.ViewerRef, _, _ string) (todo.TodoAttachment, io.ReadCloser, error) {
	return todo.TodoAttachment{FileName: "receipt.txt", ContentType: "text/plain", SizeBytes: 7}, io.NopCloser(strings.NewReader("receipt")), nil
}
func (s *attachmentTodoService) DeleteTodoAttachment(_ context.Context, _ todo.ViewerRef, _, _ string) error {
	s.deleted = true
	return nil
}

func TestTodoAttachmentRoutes(t *testing.T) {
	service := &attachmentTodoService{}
	handler := newTestRouter(RouterOptions{Todo: service})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "receipt.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("receipt"))
	_ = writer.Close()
	req := authedRequest(http.MethodPost, "/api/todos/todo-1/attachments", body.String())
	req.Header.Set("Content-Type", writer.FormDataContentType())
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated || service.content != "receipt" || service.fileName != "receipt.txt" {
		t.Fatalf("upload=%d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodGet, "/api/todos/todo-1/attachments/att", ""))
	if rec.Code != 200 || rec.Body.String() != "receipt" || rec.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment;") {
		t.Fatalf("download=%d %v %s", rec.Code, rec.Header(), rec.Body.String())
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodDelete, "/api/todos/todo-1/attachments/att", ""))
	if rec.Code != 204 || !service.deleted {
		t.Fatalf("delete=%d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, authedRequest(http.MethodPost, "/api/todos/todo-1/attachments", "invalid"))
	if rec.Code != 400 {
		t.Fatalf("malformed=%d", rec.Code)
	}
}
