package main

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	todoapp "orrery_v2/internal/app/todo"
)

func TestProductionTodoFeedbackAttachmentRoutes(t *testing.T) {
	root := t.TempDir()
	setTestConfigOverride(t, "paths.base_dir", root)
	setTestConfigOverride(t, "database.urls", productionTestDatabaseURLs(root))
	selectTestServerConfig(t)
	handler, cleanup, err := newServerHandler(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(cleanup)
	request := func(method, path, body, contentType, viewer string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("X-User-ID", viewer)
		req.Header.Set("X-Agent-Profile-ID", viewer)
		req.Header.Set("X-Viewer-Kind", "user")
		req.Header.Set("X-Tenant-ID", "system")
		req.Header.Set("X-Permission-Scope", "todo.read,todo.write")
		if contentType != "" {
			req.Header.Set("Content-Type", contentType)
		}
		rec := httptest.NewRecorder()
		serveServerTestHTTP(t, handler, rec, req)
		return rec
	}
	created := request(http.MethodPost, "/api/todos", `{"title":"Attachment test","description":"Confirm with a file","assignee_id":"feedback-human","assignee_kind":"human"}`, "application/json", "feedback-human")
	if created.Code != 201 {
		t.Fatalf("create=%d %s", created.Code, created.Body.String())
	}
	var item todoapp.TodoItem
	if err := json.Unmarshal(created.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	path := "/api/todos/" + item.TodoID
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "receipt.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write([]byte("receipt"))
	_ = writer.Close()
	upload := request(http.MethodPost, path+"/attachments", body.String(), writer.FormDataContentType(), "feedback-human")
	if upload.Code != 201 {
		t.Fatalf("upload=%d %s", upload.Code, upload.Body.String())
	}
	var attachment todoapp.TodoAttachment
	if err := json.Unmarshal(upload.Body.Bytes(), &attachment); err != nil {
		t.Fatal(err)
	}
	filePath := path + "/attachments/" + attachment.AttachmentID
	got := request(http.MethodGet, filePath, "", "", "feedback-human")
	if got.Code != 200 || got.Body.String() != "receipt" {
		t.Fatalf("download=%d %s", got.Code, got.Body.String())
	}
	denied := request(http.MethodGet, filePath, "", "", "feedback-outsider")
	if denied.Code != 403 && denied.Code != 404 {
		t.Fatalf("outsider=%d %s", denied.Code, denied.Body.String())
	}
	deleted := request(http.MethodDelete, filePath, "", "", "feedback-human")
	if deleted.Code != 204 {
		t.Fatalf("delete=%d %s", deleted.Code, deleted.Body.String())
	}
	upload = request(http.MethodPost, path+"/attachments", body.String(), writer.FormDataContentType(), "feedback-human")
	if upload.Code != 201 {
		t.Fatalf("second upload=%d %s", upload.Code, upload.Body.String())
	}
	if err := json.Unmarshal(upload.Body.Bytes(), &attachment); err != nil {
		t.Fatal(err)
	}
	result, _ := json.Marshal(todoapp.MoveTodoRequest{Status: todoapp.TodoStatusDone, Result: "Confirmed", ResultAttachmentIDs: []string{attachment.AttachmentID}})
	completed := request(http.MethodPost, path+"/move", string(result), "application/json", "feedback-human")
	if completed.Code != 200 {
		t.Fatalf("complete=%d %s", completed.Code, completed.Body.String())
	}
	if err := json.Unmarshal(completed.Body.Bytes(), &item); err != nil {
		t.Fatal(err)
	}
	if item.Feedback == nil || item.Feedback.Body != "Confirmed" || len(item.Feedback.Attachments) != 1 {
		t.Fatalf("feedback=%+v", item.Feedback)
	}
}
