package httptransport

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	resourceadmin "orrery_v2/internal/app/resourceadmin"
	skillres "orrery_v2/internal/resource/skill"
)

const maxSkillBundleImportRequestBytes int64 = skillres.DefaultMaxPackageTotalBytes + (1 << 20)

func (r *Router) handleListSkillBundles(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := resourceViewerFromRequest(req)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	if r.resourceQuery == nil {
		status, response := MapError(errServiceDependency(), cid)
		writeJSON(w, status, response)
		return
	}
	dto, err := r.resourceQuery.ListSkillBundles(req.Context(), viewer)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handleCreateSkillBundle(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	var body resourceadmin.CreateSkillBundleRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	dto, err := r.resourceCommand.CreateSkillBundle(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (r *Router) handleAddSkillsToBundle(w http.ResponseWriter, req *http.Request, bundleID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	var body resourceadmin.AddSkillsToBundleRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	dto, err := r.resourceCommand.AddSkillsToBundle(req.Context(), viewer, bundleID, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handleImportSkillIntoBundle(w http.ResponseWriter, req *http.Request, bundleID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	if req.ContentLength > maxSkillBundleImportRequestBytes {
		_, response := invalidRequest(cid, "package", "package upload exceeds the allowed size")
		writeJSON(w, http.StatusRequestEntityTooLarge, response)
		return
	}
	req.Body = http.MaxBytesReader(w, req.Body, maxSkillBundleImportRequestBytes)
	if err := req.ParseMultipartForm(8 << 20); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			_, response := invalidRequest(cid, "package", "package upload exceeds the allowed size")
			writeJSON(w, http.StatusRequestEntityTooLarge, response)
			return
		}
		status, response := invalidRequest(cid, "package", "multipart package is required")
		writeJSON(w, status, response)
		return
	}
	defer req.MultipartForm.RemoveAll()
	file, header, err := req.FormFile("package")
	if err != nil {
		status, response := invalidRequest(cid, "package", "package file is required")
		writeJSON(w, status, response)
		return
	}
	defer file.Close()
	dto, err := r.resourceCommand.ImportSkillIntoBundle(req.Context(), viewer, bundleID, file, header.Size)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handleRemoveSkillFromBundle(w http.ResponseWriter, req *http.Request, bundleID, skillID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	dto, err := r.resourceCommand.RemoveSkillFromBundle(req.Context(), viewer, bundleID, skillID)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handleDeleteSkillBundle(w http.ResponseWriter, req *http.Request, bundleID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	deleteSkills := false
	if raw := strings.TrimSpace(req.URL.Query().Get("delete_skills")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			status, response := invalidRequest(cid, "delete_skills", "delete_skills must be a boolean")
			writeJSON(w, status, response)
			return
		}
		deleteSkills = parsed
	}
	if err := r.resourceCommand.DeleteSkillBundle(req.Context(), viewer, bundleID, resourceadmin.DeleteSkillBundleRequest{DeleteSkills: deleteSkills}); err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "accepted", "bundle_id": bundleID})
}
