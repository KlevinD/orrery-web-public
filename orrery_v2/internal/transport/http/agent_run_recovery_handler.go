package httptransport

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"orrery_v2/internal/app/agentrunrecovery"
	"orrery_v2/internal/app/missioncontrol"
	"orrery_v2/internal/app/operations"
	missioncore "orrery_v2/internal/mission/core"
)

type agentRunRecoveryService interface {
	GetAgentRunRecovery(context.Context, missioncontrol.ViewerRef, missioncore.MissionID, string) (agentrunrecovery.View, error)
	ReviewAgentRunRecovery(context.Context, missioncontrol.ViewerRef, missioncore.MissionID, string, string, agentrunrecovery.ReviewInput) (agentrunrecovery.Review, error)
}

func (r *Router) handleAgentRunRecovery(w http.ResponseWriter, req *http.Request, mission missioncore.MissionID, run string, review bool) {
	cid := correlationID(req)
	fail := func(err error) { status, body := MapError(err, cid); writeJSON(w, status, body) }
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		fail(err)
		return
	}
	required := operations.PermissionOperationsRead
	if review {
		required = operations.PermissionOperationsManage
	}
	if !viewerHasScope(viewer.PermissionScope, required, operations.PermissionOperationsManage) {
		fail(operations.ErrPermissionDenied)
		return
	}
	service, ok := r.missionQuery.(agentRunRecoveryService)
	if !ok {
		fail(errServiceDependency())
		return
	}
	// Responses contain operator notes and execution metadata, never cache them
	// in a browser/shared proxy after a user or tenant change.
	w.Header().Set("Cache-Control", "no-store")
	if !review {
		result, err := service.GetAgentRunRecovery(req.Context(), viewer, mission, run)
		if err != nil {
			fail(err)
			return
		}
		writeJSON(w, http.StatusOK, result)
		return
	}
	if idempotencyKey(req) == "" {
		status, body := invalidRequest(cid, "idempotency_key", "idempotency key is required")
		writeJSON(w, status, body)
		return
	}
	var input agentrunrecovery.ReviewInput
	decoder := json.NewDecoder(http.MaxBytesReader(w, req.Body, 8192))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		fail(missioncontrol.ErrInvalidRequest)
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		fail(missioncontrol.ErrInvalidRequest)
		return
	}
	result, err := service.ReviewAgentRunRecovery(req.Context(), viewer, mission, run, idempotencyKey(req), input)
	if err != nil {
		fail(err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}
