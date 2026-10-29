package httptransport

import (
	"encoding/json"
	"net/http"

	"orrery_v2/internal/app/memorydistill"
)

func (r *Router) handleDistillExperienceMemory(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := r.memoryDistillViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.memoryDistill == nil {
		status, body := MapError(memorydistill.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	var body memorydistill.DistillExperienceRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "invalid JSON body")
		writeJSON(w, status, response)
		return
	}
	dto, err := r.memoryDistill.DistillExperience(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, dto)
}

func (r *Router) handleDistillWorldMemory(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := r.memoryDistillViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.memoryDistill == nil {
		status, body := MapError(memorydistill.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	var body memorydistill.DistillWorldRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "invalid JSON body")
		writeJSON(w, status, response)
		return
	}
	dto, err := r.memoryDistill.DistillWorld(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, dto)
}
