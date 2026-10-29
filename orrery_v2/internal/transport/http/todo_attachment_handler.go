package httptransport

import (
	"context"
	"io"
	"mime"
	"net/http"
	"strconv"

	"orrery_v2/internal/app/todo"
)

type todoAttachmentService interface {
	DeleteTodoAttachment(context.Context, todo.ViewerRef, string, string) error
	UploadTodoAttachment(context.Context, todo.ViewerRef, string, string, io.Reader) (todo.TodoAttachment, error)
	OpenTodoAttachment(context.Context, todo.ViewerRef, string, string) (todo.TodoAttachment, io.ReadCloser, error)
}

func (r *Router) handleTodoAttachments(w http.ResponseWriter, req *http.Request, parts []string) {
	service, ok := r.todo.(todoAttachmentService)
	if !ok {
		writeTodoError(w, req, todo.ErrServiceDependency)
		return
	}
	viewer, ok := todoViewerFromRequest(w, req)
	if !ok {
		return
	}
	if req.Method == http.MethodPost && len(parts) == 4 {
		req.Body = http.MaxBytesReader(w, req.Body, todo.MaxTodoAttachmentBytes+(1<<20))
		if err := req.ParseMultipartForm(1 << 20); err != nil {
			writeTodoError(w, req, todo.ErrInvalidRequest)
			return
		}
		defer req.MultipartForm.RemoveAll()
		file, header, err := req.FormFile("file")
		if err != nil {
			writeTodoError(w, req, todo.ErrInvalidRequest)
			return
		}
		defer file.Close()
		attachment, err := service.UploadTodoAttachment(req.Context(), viewer, parts[2], header.Filename, file)
		if err != nil {
			writeTodoError(w, req, err)
			return
		}
		writeJSON(w, http.StatusCreated, attachment)
		return
	}
	if req.Method == http.MethodGet && len(parts) == 5 {
		attachment, file, err := service.OpenTodoAttachment(req.Context(), viewer, parts[2], parts[4])
		if err != nil {
			writeTodoError(w, req, err)
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", attachment.ContentType)
		w.Header().Set("Content-Length", strconv.FormatInt(attachment.SizeBytes, 10))
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": attachment.FileName}))
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, no-store")
		_, _ = io.Copy(w, file)
		return
	}
	if req.Method == http.MethodDelete && len(parts) == 5 {
		if err := service.DeleteTodoAttachment(req.Context(), viewer, parts[2], parts[4]); err != nil {
			writeTodoError(w, req, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeTodoError(w, req, todo.ErrNotFound)
}
