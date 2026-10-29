package httptransport

import (
	"context"
	"encoding/json"
	"net/http"

	"orrery_v2/internal/app/memoryadmin"
)

func (r *Router) handleMemory(w http.ResponseWriter, req *http.Request, parts []string) {
	if len(parts) == 4 && parts[2] == "distill" && parts[3] == "experience" && req.Method == http.MethodPost {
		r.handleDistillExperienceMemory(w, req)
		return
	}
	if len(parts) == 4 && parts[2] == "distill" && parts[3] == "world" && req.Method == http.MethodPost {
		r.handleDistillWorldMemory(w, req)
		return
	}
	if len(parts) == 3 && parts[2] == "candidates" {
		switch req.Method {
		case http.MethodGet:
			r.handleListMemoryCandidates(w, req)
		case http.MethodPost:
			r.handleCreateMemoryCandidate(w, req)
		default:
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "route not found", CorrelationID: correlationID(req)})
		}
		return
	}
	if len(parts) == 5 && parts[2] == "candidates" {
		switch {
		case req.Method == http.MethodPost && parts[4] == "accept":
			r.handleAcceptMemoryCandidate(w, req, parts[3])
		case req.Method == http.MethodPost && parts[4] == "reject":
			r.handleRejectMemoryCandidate(w, req, parts[3])
		case req.Method == http.MethodPost && parts[4] == "merge":
			r.handleMergeMemoryCandidate(w, req, parts[3])
		default:
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "route not found", CorrelationID: correlationID(req)})
		}
		return
	}
	if len(parts) == 3 && parts[2] == "records" && req.Method == http.MethodGet {
		r.handleListMemoryRecords(w, req)
		return
	}
	if len(parts) == 4 && parts[2] == "records" && parts[3] == "touch" && req.Method == http.MethodPost {
		r.handleTouchMemoryRecords(w, req)
		return
	}
	if len(parts) == 4 && parts[2] == "records" && parts[3] == "sweep-retention" && req.Method == http.MethodPost {
		r.handleSweepMemoryRetention(w, req)
		return
	}
	if len(parts) == 4 && parts[2] == "records" && req.Method == http.MethodGet {
		r.handleGetMemoryRecord(w, req, parts[3])
		return
	}
	if len(parts) == 5 && parts[2] == "records" && req.Method == http.MethodPost {
		switch parts[4] {
		case "archive":
			r.handleArchiveMemoryRecord(w, req, parts[3])
		case "anonymize":
			r.handleAnonymizeMemoryRecord(w, req, parts[3])
		case "delete":
			r.handleDeleteMemoryRecord(w, req, parts[3])
		default:
			writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "route not found", CorrelationID: correlationID(req)})
		}
		return
	}
	writeJSON(w, http.StatusNotFound, ErrorResponse{Code: ErrorNotFound, Message: "route not found", CorrelationID: correlationID(req)})
}

func (r *Router) handleListMemoryCandidates(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := r.memoryViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.memoryQuery == nil {
		status, body := MapError(memoryadmin.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	limit, _ := listPage(req)
	var reviewRequired *bool
	if value, ok := parseOptionalBool(req.URL.Query().Get("review_required")); ok {
		reviewRequired = &value
	}
	page, err := r.memoryQuery.ListCandidates(req.Context(), viewer, memoryadmin.ListCandidatesQuery{
		MissionID:      req.URL.Query().Get("mission_id"),
		ScopeKind:      req.URL.Query().Get("scope_kind"),
		ScopeID:        req.URL.Query().Get("scope_id"),
		Layer:          req.URL.Query().Get("layer"),
		Topic:          req.URL.Query().Get("topic"),
		Status:         req.URL.Query().Get("status"),
		ReviewRequired: reviewRequired,
		Limit:          limit,
	})
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleCreateMemoryCandidate(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := r.memoryViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.memoryCommand == nil {
		status, body := MapError(memoryadmin.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	var body memoryadmin.CreateCandidateRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "invalid JSON body")
		writeJSON(w, status, response)
		return
	}
	dto, err := r.memoryCommand.CreateCandidate(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusCreated, dto)
}

func (r *Router) handleAcceptMemoryCandidate(w http.ResponseWriter, req *http.Request, candidateID string) {
	cid := correlationID(req)
	viewer, err := r.memoryViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.memoryCommand == nil {
		status, body := MapError(memoryadmin.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	var body memoryadmin.AcceptCandidateRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "invalid JSON body")
		writeJSON(w, status, response)
		return
	}
	dto, err := r.memoryCommand.AcceptCandidate(req.Context(), viewer, candidateID, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, dto)
}

func (r *Router) handleRejectMemoryCandidate(w http.ResponseWriter, req *http.Request, candidateID string) {
	cid := correlationID(req)
	viewer, err := r.memoryViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.memoryCommand == nil {
		status, body := MapError(memoryadmin.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	var body memoryadmin.RejectCandidateRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "invalid JSON body")
		writeJSON(w, status, response)
		return
	}
	receipt, err := r.memoryCommand.RejectCandidate(req.Context(), viewer, candidateID, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, receipt)
}

func (r *Router) handleMergeMemoryCandidate(w http.ResponseWriter, req *http.Request, candidateID string) {
	cid := correlationID(req)
	viewer, err := r.memoryViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.memoryCommand == nil {
		status, body := MapError(memoryadmin.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	var body memoryadmin.MergeCandidateRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "invalid JSON body")
		writeJSON(w, status, response)
		return
	}
	receipt, err := r.memoryCommand.MergeCandidate(req.Context(), viewer, candidateID, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, receipt)
}

func (r *Router) handleListMemoryRecords(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := r.memoryViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.memoryQuery == nil {
		status, body := MapError(memoryadmin.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	limit, _ := listPage(req)
	page, err := r.memoryQuery.ListRecords(req.Context(), viewer, memoryadmin.ListRecordsQuery{
		ProfileID:       req.URL.Query().Get("profile_id"),
		ScopeKind:       req.URL.Query().Get("scope_kind"),
		ScopeID:         req.URL.Query().Get("scope_id"),
		Layer:           req.URL.Query().Get("layer"),
		Topic:           req.URL.Query().Get("topic"),
		LifecycleStatus: req.URL.Query().Get("lifecycle_status"),
		IncludeArchived: parseBoolQuery(req.URL.Query().Get("include_archived")),
		Limit:           limit,
	})
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, page)
}

func (r *Router) handleGetMemoryRecord(w http.ResponseWriter, req *http.Request, memoryID string) {
	cid := correlationID(req)
	viewer, err := r.memoryViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.memoryQuery == nil {
		status, body := MapError(memoryadmin.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	dto, err := r.memoryQuery.GetRecord(req.Context(), viewer, memoryID)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

func (r *Router) handleTouchMemoryRecords(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := r.memoryViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.memoryCommand == nil {
		status, body := MapError(memoryadmin.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	var body memoryadmin.TouchMemoryRecordsRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "invalid JSON body")
		writeJSON(w, status, response)
		return
	}
	receipt, err := r.memoryCommand.TouchRecords(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, receipt)
}

func (r *Router) handleSweepMemoryRetention(w http.ResponseWriter, req *http.Request) {
	cid := correlationID(req)
	viewer, err := r.memoryViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.memoryCommand == nil {
		status, body := MapError(memoryadmin.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	var body memoryadmin.MemoryRetentionSweepRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "invalid JSON body")
		writeJSON(w, status, response)
		return
	}
	result, err := r.memoryCommand.SweepRetention(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, result)
}

func (r *Router) handleArchiveMemoryRecord(w http.ResponseWriter, req *http.Request, memoryID string) {
	r.handleMemoryLifecycleCommand(w, req, memoryID, func(ctx context.Context, viewer memoryadmin.ViewerRef, body memoryadmin.MemoryLifecycleRequest) (any, error) {
		return r.memoryCommand.ArchiveRecord(ctx, viewer, memoryID, body)
	})
}

func (r *Router) handleAnonymizeMemoryRecord(w http.ResponseWriter, req *http.Request, memoryID string) {
	r.handleMemoryLifecycleCommand(w, req, memoryID, func(ctx context.Context, viewer memoryadmin.ViewerRef, body memoryadmin.MemoryLifecycleRequest) (any, error) {
		return r.memoryCommand.AnonymizeRecord(ctx, viewer, memoryID, body)
	})
}

func (r *Router) handleDeleteMemoryRecord(w http.ResponseWriter, req *http.Request, memoryID string) {
	r.handleMemoryLifecycleCommand(w, req, memoryID, func(ctx context.Context, viewer memoryadmin.ViewerRef, body memoryadmin.MemoryLifecycleRequest) (any, error) {
		return r.memoryCommand.DeleteRecord(ctx, viewer, memoryID, body)
	})
}

func (r *Router) handleMemoryLifecycleCommand(w http.ResponseWriter, req *http.Request, memoryID string, call func(context.Context, memoryadmin.ViewerRef, memoryadmin.MemoryLifecycleRequest) (any, error)) {
	cid := correlationID(req)
	viewer, err := r.memoryViewerFromRequest(req)
	if err != nil {
		status, body := MapError(err, cid)
		writeJSON(w, status, body)
		return
	}
	if r.memoryCommand == nil {
		status, body := MapError(memoryadmin.ErrServiceDependency, cid)
		writeJSON(w, status, body)
		return
	}
	var body memoryadmin.MemoryLifecycleRequest
	if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
		status, response := invalidRequest(cid, "body", "invalid JSON body")
		writeJSON(w, status, response)
		return
	}
	dto, err := call(req.Context(), viewer, body)
	if err != nil {
		status, response := MapError(err, cid)
		writeJSON(w, status, response)
		return
	}
	writeJSON(w, http.StatusAccepted, dto)
}
