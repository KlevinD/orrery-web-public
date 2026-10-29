package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	agentcore "orrery_v2/internal/agent/core"
	resourceadmin "orrery_v2/internal/app/resourceadmin"
)

func (r *Router) handleRegisterSkillVersion(w http.ResponseWriter, req *http.Request, skillID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	var body resourceadmin.SkillVersionCandidateRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	if body.Definition.ID == "" {
		body.Definition.ID = agentcore.SkillID(skillID)
	}
	if string(body.Definition.ID) != skillID {
		status, response := invalidRequest(cid, "skill_id", "definition skill id does not match path")
		writeJSON(w, status, response)
		return
	}
	dto, err := r.resourceCommand.RegisterSkillVersion(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (r *Router) handlePromoteSkillVersion(w http.ResponseWriter, req *http.Request, skillID, version string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	var body resourceadmin.PromoteSkillVersionRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	dto, err := r.resourceCommand.PromoteSkillVersion(req.Context(), viewer, skillID, version, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handleRollbackSkillVersion(w http.ResponseWriter, req *http.Request, skillID, version string) {
	cid := correlationID(req)
	viewer, ok := r.prepareResourceCommand(w, req, cid)
	if !ok {
		return
	}
	var body resourceadmin.RollbackSkillVersionRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		status, response := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, response)
		return
	}
	dto, err := r.resourceCommand.RollbackSkillVersion(req.Context(), viewer, skillID, version, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}
