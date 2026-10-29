package httptransport

import (
	"net/http"
	"strconv"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
)

func (r *Router) handleMissionControl(w http.ResponseWriter, req *http.Request, missionID missioncore.MissionID) {
	cid := correlationID(req)
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.missionQuery == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	view, err := r.missionQuery.GetMissionControlView(req.Context(), viewer, missionID)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	w.Header().Set("ETag", `"`+strconv.Itoa(view.Version)+`"`)
	writeJSON(w, http.StatusOK, view)
}

func (r *Router) handleMissionOps(w http.ResponseWriter, req *http.Request, missionID missioncore.MissionID) {
	cid := correlationID(req)
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.missionQuery == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	view, err := r.missionQuery.GetMissionOpsView(req.Context(), viewer, missionID)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	w.Header().Set("ETag", `"`+strconv.Itoa(view.Version)+`"`)
	writeJSON(w, http.StatusOK, view)
}

func (r *Router) handleListMissions(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.missionQuery == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(req.URL.Query().Get("offset"))
	page, err := r.missionQuery.ListMissions(req.Context(), viewer, missioncontrol.ListMissionsQuery{
		Search: req.URL.Query().Get("search"), Status: req.URL.Query().Get("status"),
		OriginKind: req.URL.Query().Get("origin_kind"), OriginID: req.URL.Query().Get("origin_id"), Limit: limit, Offset: offset,
	})
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleMissionSubview(w http.ResponseWriter, req *http.Request, missionID missioncore.MissionID, viewName string) {
	cid := correlationID(req)
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.missionQuery == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return
	}
	view, err := r.missionQuery.GetMissionControlView(req.Context(), viewer, missionID)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	w.Header().Set("ETag", `"`+strconv.Itoa(view.Version)+`"`)
	switch viewName {
	case "team":
		writeJSON(w, http.StatusOK, view.Team)
	case "tasks":
		writeJSON(w, http.StatusOK, view.TaskBoard)
	case "progress":
		writeJSON(w, http.StatusOK, view.ProgressGraph)
	case "evidence":
		writeJSON(w, http.StatusOK, view.Evidence)
	case "qa":
		writeJSON(w, http.StatusOK, view.QA)
	case "discussion":
		writeJSON(w, http.StatusOK, view.Discussion)
	default:
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "route not found", CorrelationID: cid})
	}
}
