package httptransport

import (
	"net/http"
	"strconv"
	"strings"

	missioncontrol "orrery_v2/internal/app/missioncontrol"
	missioncore "orrery_v2/internal/mission/core"
	discussion "orrery_v2/internal/mission/discussion"
)

func (r *Router) handleQADecision(w http.ResponseWriter, req *http.Request, missionID missioncore.MissionID, qaRef string) {
	cid := correlationID(req)
	viewer, ok := r.prepareCommand(w, req, cid, true)
	if !ok {
		return
	}
	cmd, err := decodeQADecision(req, missionID, qaRef)
	if err != nil {
		status, body := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, body)
		return
	}
	receipt, err := r.missionCommand.SubmitQADecision(req.Context(), viewer, cmd)
	writeCommandResult(w, cid, receipt, err)
}

func (r *Router) handleCreateMission(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareCommand(w, req, cid, true)
	if !ok {
		return
	}
	cmd, err := decodeCreateMission(req)
	if err != nil {
		status, body := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, body)
		return
	}
	if strings.TrimSpace(cmd.InitialHumanDisplayName) == "" {
		cmd.InitialHumanDisplayName = displayNameFromRequest(req)
	}
	dto, err := r.missionCommand.CreateMission(req.Context(), viewer, cmd)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (r *Router) handleDeleteMission(w http.ResponseWriter, req *http.Request, missionID missioncore.MissionID) {
	cid := correlationID(req)
	viewer, ok := r.prepareCommand(w, req, cid, true)
	if !ok {
		return
	}
	if err := r.missionCommand.DeleteMission(req.Context(), viewer, missionID); err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "deleted", "mission_id": missionID.String()})
}

func (r *Router) handleCancelMission(w http.ResponseWriter, req *http.Request, missionID missioncore.MissionID) {
	cid := correlationID(req)
	viewer, ok := r.prepareCommand(w, req, cid, true)
	if !ok {
		return
	}
	cmd, err := decodeCancelMission(req, missionID)
	if err != nil {
		status, body := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, body)
		return
	}
	receipt, err := r.missionCommand.CancelMission(req.Context(), viewer, cmd)
	writeCommandResult(w, cid, receipt, err)
}

func (r *Router) handleCreateTask(w http.ResponseWriter, req *http.Request, missionID missioncore.MissionID) {
	cid := correlationID(req)
	viewer, ok := r.prepareCommand(w, req, cid, true)
	if !ok {
		return
	}
	cmd, err := decodeCreateTask(req, missionID)
	if err != nil {
		status, body := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, body)
		return
	}
	dto, err := r.missionCommand.CreateTask(req.Context(), viewer, cmd)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (r *Router) handleAssignTask(w http.ResponseWriter, req *http.Request, missionID missioncore.MissionID, taskID missioncore.TaskID) {
	cid := correlationID(req)
	viewer, ok := r.prepareCommand(w, req, cid, true)
	if !ok {
		return
	}
	cmd, err := decodeAssignTask(req, missionID, taskID)
	if err != nil {
		status, body := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, body)
		return
	}
	receipt, err := r.missionCommand.AssignTask(req.Context(), viewer, cmd)
	writeCommandResult(w, cid, receipt, err)
}

func (r *Router) handleStartAgentRun(w http.ResponseWriter, req *http.Request, missionID missioncore.MissionID, taskID missioncore.TaskID) {
	cid := correlationID(req)
	viewer, ok := r.prepareCommand(w, req, cid, true)
	if !ok {
		return
	}
	cmd, err := decodeStartAgentRun(req, missionID, taskID)
	if err != nil {
		status, body := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, body)
		return
	}
	receipt, err := r.missionCommand.StartAgentRun(req.Context(), viewer, cmd)
	writeCommandResult(w, cid, receipt, err)
}

func (r *Router) handleCancelAgentRun(w http.ResponseWriter, req *http.Request, missionID missioncore.MissionID, runID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareCommand(w, req, cid, true)
	if !ok {
		return
	}
	cmd, err := decodeCancelAgentRun(req, missionID, runID)
	if err != nil {
		status, body := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, body)
		return
	}
	receipt, err := r.missionCommand.CancelAgentRun(req.Context(), viewer, cmd)
	writeCommandResult(w, cid, receipt, err)
}

func (r *Router) handleRetryAgentRun(w http.ResponseWriter, req *http.Request, missionID missioncore.MissionID, runID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareCommand(w, req, cid, true)
	if !ok {
		return
	}
	cmd, err := decodeRetryAgentRun(req, missionID, runID)
	if err != nil {
		status, body := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, body)
		return
	}
	receipt, err := r.missionCommand.RetryAgentRun(req.Context(), viewer, cmd)
	writeCommandResult(w, cid, receipt, err)
}

func (r *Router) handlePostDiscussionMessage(w http.ResponseWriter, req *http.Request, missionID missioncore.MissionID) {
	cid := correlationID(req)
	viewer, ok := r.prepareCommand(w, req, cid, true)
	if !ok {
		return
	}
	cmd, err := decodePostDiscussion(req, missionID)
	if err != nil {
		status, body := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, body)
		return
	}
	receipt, err := r.missionCommand.PostDiscussionMessage(req.Context(), viewer, cmd)
	writeCommandResult(w, cid, receipt, err)
}

func (r *Router) handleReplyDiscussionMessage(w http.ResponseWriter, req *http.Request, missionID missioncore.MissionID, messageID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareCommand(w, req, cid, true)
	if !ok {
		return
	}
	cmd, err := decodeReplyDiscussion(req, missionID, messageID)
	if err != nil {
		status, body := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, body)
		return
	}
	receipt, err := r.missionCommand.ReplyDiscussionMessage(req.Context(), viewer, cmd)
	writeCommandResult(w, cid, receipt, err)
}

func (r *Router) handleListInbox(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, ok := r.prepareCommand(w, req, cid, false)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(req.URL.Query().Get("limit"))
	page, err := r.missionCommand.ListInbox(req.Context(), viewer, missioncontrol.ListInboxCommand{
		MissionID:     missioncore.MissionID(req.URL.Query().Get("mission_id")),
		IncludeClosed: req.URL.Query().Get("include_closed") == "true",
		Limit:         limit,
	})
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleMarkInboxRead(w http.ResponseWriter, req *http.Request, inboxID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareCommand(w, req, cid, true)
	if !ok {
		return
	}
	receipt, err := r.missionCommand.MarkInboxRead(req.Context(), viewer, missioncontrol.InboxItemCommand{RequestID: cid, IdempotencyKey: idempotencyKey(req), InboxItemID: discussion.InboxItemID(inboxID)})
	writeCommandResult(w, cid, receipt, err)
}

func (r *Router) handleCloseInboxItem(w http.ResponseWriter, req *http.Request, inboxID string) {
	cid := correlationID(req)
	viewer, ok := r.prepareCommand(w, req, cid, true)
	if !ok {
		return
	}
	cmd, err := decodeCloseInbox(req, inboxID)
	if err != nil {
		status, body := invalidRequest(cid, "body", "request body is invalid")
		writeJSON(w, status, body)
		return
	}
	receipt, err := r.missionCommand.CloseInboxItem(req.Context(), viewer, cmd)
	writeCommandResult(w, cid, receipt, err)
}

func (r *Router) prepareCommand(w http.ResponseWriter, req *http.Request, cid string, requireIdempotency bool) (missioncontrol.ViewerRef, bool) {
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return missioncontrol.ViewerRef{}, false
	}
	if requireIdempotency && idempotencyKey(req) == "" {
		status, body := invalidRequest(cid, "idempotency_key", "idempotency key is required for mutating commands")
		writeJSON(w, status, body)
		return missioncontrol.ViewerRef{}, false
	}
	if r.missionCommand == nil {
		status, body := MapError(errServiceDependency(), cid)
		writeJSON(w, status, body)
		return missioncontrol.ViewerRef{}, false
	}
	return viewer, true
}

func writeCommandResult(w http.ResponseWriter, correlationID string, receipt missioncontrol.CommandReceiptDTO, err error) {
	if err != nil {
		status, body := MapError(err, correlationID)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusAccepted, receipt)
}
