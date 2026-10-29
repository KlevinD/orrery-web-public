package httptransport

import (
	"bytes"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strconv"

	resourceadmin "orrery_v2/internal/app/resourceadmin"
)

func (r *Router) handleRegisterTool(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	var body resourceadmin.ToolDefinitionRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	body.ToolID = ""
	dto, err := r.resourceCommand.RegisterTool(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (r *Router) handleUpdateTool(w http.ResponseWriter, req *http.Request, toolID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	var body resourceadmin.ToolDefinitionRequest
	mask, err := decodeJSONWithUpdateMask(req.Body, &body)
	if err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	body.UpdateMask = mask
	body.ToolID = toolID
	dto, err := r.resourceCommand.UpdateTool(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handleDisableTool(w http.ResponseWriter, req *http.Request, toolID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	if err := r.resourceCommand.DisableTool(req.Context(), viewer, toolID); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "tool_id": toolID})
}

func (r *Router) handleDeleteTool(w http.ResponseWriter, req *http.Request, toolID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	if err := r.resourceCommand.DeleteTool(req.Context(), viewer, toolID); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "tool_id": toolID})
}

func (r *Router) handleRegisterMCPServer(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	var body resourceadmin.RegisterMCPServerRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	body.ToolID = ""
	dto, err := r.resourceCommand.RegisterMCPServer(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (r *Router) handleUpdateMCPServer(w http.ResponseWriter, req *http.Request, toolID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	var body resourceadmin.RegisterMCPServerRequest
	mask, err := decodeJSONWithUpdateMask(req.Body, &body)
	if err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	body.UpdateMask = mask
	body.ToolID = toolID
	dto, err := r.resourceCommand.UpdateMCPServer(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handleSyncMCPServer(w http.ResponseWriter, req *http.Request, toolID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	dto, err := r.resourceCommand.SyncMCPServer(req.Context(), viewer, toolID)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, dto)
}

func (r *Router) handleMCPServerHealth(w http.ResponseWriter, req *http.Request, toolID string) {
	cid := correlationID(req)
	viewer, err := resourceViewerFromRequest(req)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	if r.resourceCommand == nil {
		status, response := MapError(errServiceDependency(), cid)
		writeJSON(w, status, response)
		return
	}
	viewer.RequestID = cid
	viewer.IdempotencyKey = idempotencyKey(req)
	dto, err := r.resourceCommand.GetMCPServerHealth(req.Context(), viewer, toolID)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handleDisableMCPServer(w http.ResponseWriter, req *http.Request, toolID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	if err := r.resourceCommand.DisableTool(req.Context(), viewer, toolID); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "tool_id": toolID})
}

func (r *Router) handleDeleteMCPServer(w http.ResponseWriter, req *http.Request, toolID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	if err := r.resourceCommand.DeleteMCPServer(req.Context(), viewer, toolID); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "tool_id": toolID})
}

func (r *Router) handleImportSkill(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	if err := req.ParseMultipartForm(32 << 20); err != nil {
		status, response := invalidRequest(cid, "package", "multipart package is required")
		writeJSON(w, status, response)
		return
	}
	file, _, err := req.FormFile("package")
	if err != nil {
		status, response := invalidRequest(cid, "package", "package file is required")
		writeJSON(w, status, response)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		status, response := invalidRequest(cid, "package", "package file could not be read")
		writeJSON(w, status, response)
		return
	}
	dto, err := r.resourceCommand.ImportSkillPackage(req.Context(), viewer, bytes.NewReader(data), int64(len(data)))
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (r *Router) handleDisableSkill(w http.ResponseWriter, req *http.Request, skillID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	if err := r.resourceCommand.DisableSkill(req.Context(), viewer, skillID); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "skill_id": skillID})
}

func (r *Router) handleDeleteSkill(w http.ResponseWriter, req *http.Request, skillID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	if err := r.resourceCommand.DeleteSkill(req.Context(), viewer, skillID); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "skill_id": skillID})
}

func (r *Router) handleExportSkill(w http.ResponseWriter, req *http.Request, skillID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	pkg, err := r.resourceCommand.ExportSkillPackage(req.Context(), viewer, skillID)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	contentType := pkg.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	if pkg.FileName != "" {
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": pkg.FileName}))
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(pkg.Bytes)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pkg.Bytes)
}

func (r *Router) handleBindResource(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	var body resourceadmin.BindResourceRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	dto, err := r.resourceCommand.BindResourceToAgent(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (r *Router) handleUnbindResource(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	var body resourceadmin.UnbindResourceRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	if err := r.resourceCommand.UnbindResourceFromAgent(req.Context(), viewer, body); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "resource_id": body.ResourceID, "agent_id": body.AgentID})
}

func (r *Router) prepareResourceCommand(w http.ResponseWriter, req *http.Request, cid string) (resourceadmin.ViewerRef, bool) {
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
