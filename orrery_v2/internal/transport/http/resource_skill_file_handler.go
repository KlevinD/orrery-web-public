package httptransport

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"

	resourceadmin "orrery_v2/internal/app/resourceadmin"
)

type saveSkillMarkdownRequest struct {
	Raw string `json:"raw"`
}

type saveSkillFileRequest struct {
	Content string `json:"content"`
}

type createSkillFolderRequest struct {
	Path string `json:"path"`
}

func (r *Router) handleCreateSkill(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	var body resourceadmin.CreateSkillRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	detail, err := r.resourceCommand.CreateSkill(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusCreated, detail)
}

func (r *Router) handleSaveSkillMarkdown(w http.ResponseWriter, req *http.Request, skillID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	var body saveSkillMarkdownRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	detail, err := r.resourceCommand.SaveSkillMarkdown(req.Context(), viewer, skillID, body.Raw)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, detail)
}

func (r *Router) handleGetSkillFile(w http.ResponseWriter, req *http.Request, skillID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceFileCommand(w, req, cid)
	if !ok {
		return
	}
	file, err := r.resourceCommand.GetSkillFile(req.Context(), viewer, resourceadmin.SkillFileRequest{
		SkillID: skillID,
		Path:    req.URL.Query().Get("path"),
	})
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, file)
}

func (r *Router) handleSaveSkillFile(w http.ResponseWriter, req *http.Request, skillID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	var body saveSkillFileRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	file, err := r.resourceCommand.SaveSkillFile(req.Context(), viewer, resourceadmin.SkillFileWriteRequest{
		SkillID: skillID,
		Path:    req.URL.Query().Get("path"),
		Content: body.Content,
	})
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, file)
}

func (r *Router) handleUploadSkillFile(w http.ResponseWriter, req *http.Request, skillID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	if err := req.ParseMultipartForm(16 << 20); err != nil {
		status, response := invalidRequest(cid, "file", "multipart file upload is required")
		writeJSON(w, status, response)
		return
	}
	file, header, err := req.FormFile("file")
	if err != nil {
		status, response := invalidRequest(cid, "file", "file is required")
		writeJSON(w, status, response)
		return
	}
	defer file.Close()
	raw, err := io.ReadAll(file)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	path := req.URL.Query().Get("path")
	if path == "" && header != nil {
		path = header.Filename
	}
	uploaded, err := r.resourceCommand.UploadSkillFile(req.Context(), viewer, resourceadmin.SkillFileUploadRequest{
		SkillID: skillID,
		Path:    path,
		Name:    header.Filename,
		Content: raw,
	})
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusCreated, uploaded)
}

func (r *Router) handleDownloadSkillFile(w http.ResponseWriter, req *http.Request, skillID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceFileCommand(w, req, cid)
	if !ok {
		return
	}
	file, err := r.resourceCommand.DownloadSkillFile(req.Context(), viewer, resourceadmin.SkillFileRequest{
		SkillID: skillID,
		Path:    req.URL.Query().Get("path"),
	})
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	contentType := file.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	if file.FileName != "" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.FileName}))
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(file.Bytes)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(file.Bytes)
}

func (r *Router) handleCreateSkillFolder(w http.ResponseWriter, req *http.Request, skillID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	var body createSkillFolderRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	if err := r.resourceCommand.CreateSkillFolder(req.Context(), viewer, resourceadmin.SkillFileRequest{
		SkillID: skillID,
		Path:    body.Path,
	}); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"status": "created", "skill_id": skillID, "path": body.Path})
}

func (r *Router) handleDeleteSkillFile(w http.ResponseWriter, req *http.Request, skillID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	if err := r.resourceCommand.DeleteSkillFile(req.Context(), viewer, resourceadmin.SkillFileRequest{
		SkillID: skillID,
		Path:    req.URL.Query().Get("path"),
	}); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "skill_id": skillID})
}

func (r *Router) prepareResourceFileCommand(w http.ResponseWriter, req *http.Request, cid string) (resourceadmin.ViewerRef, bool) {
	viewer, err := resourceViewerFromRequest(req)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return resourceadmin.ViewerRef{}, false
	}
	viewer.RequestID = cid
	viewer.IdempotencyKey = idempotencyKey(req)
	if r.resourceCommand == nil {
		status, response := MapError(errServiceDependency(), cid)
		writeJSON(w, status, response)
		return resourceadmin.ViewerRef{}, false
	}
	return viewer, true
}
