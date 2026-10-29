package httptransport

import (
	"net/http"

	missioncore "orrery_v2/internal/mission/core"
)

func (r *Router) handleSubmitResult(w http.ResponseWriter, req *http.Request, missionID missioncore.MissionID, taskID missioncore.TaskID) {
	cid := correlationID(req)
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if idempotencyKey(req) == "" {
		status, body := invalidRequest(cid, "idempotency_key", "idempotency key is required for mutating commands")
		writeJSON(w, status, body)
		return
	}
	if r.missionCommand == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	cmd, err := decodeSubmitResult(req, missionID, taskID)
	if err != nil {
		if fieldErr, ok := err.(forbiddenRequestFieldError); ok {
			status, body := invalidRequest(cid, fieldErr.field, "request field is not allowed")
			writeJSON(w, status, body)
			return
		}
		status, body := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, body)
		return
	}
	receipt, err := r.missionCommand.SubmitAgentResult(req.Context(), viewer, cmd)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusAccepted, receipt)
}
