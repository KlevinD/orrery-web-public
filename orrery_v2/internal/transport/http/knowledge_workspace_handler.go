package httptransport

import (
	"encoding/json"
	"net/http"
	"strconv"
)

type updateKnowledgeSourceRequest struct {
	ExpectedRevision int    `json:"expected_revision"`
	Content          string `json:"content"`
}

func (r *Router) handleKnowledgeDocumentSource(w http.ResponseWriter, req *http.Request, knowledgeBaseID, documentID string) {
	if r.knowledgeWorkspace == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge workspace unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	viewer := r.knowledgeViewerFromRequest(req)
	switch req.Method {
	case http.MethodGet:
		revision, _ := strconv.Atoi(req.URL.Query().Get("revision"))
		result, err := r.knowledgeWorkspace.GetSource(req.Context(), viewer, knowledgeBaseID, documentID, revision)
		if err != nil {
			status, body := MapError(err, correlationID(req))
			writeJSON(w, status, body)
			return
		}
		writeJSON(w, http.StatusOK, result)
	case http.MethodPut:
		var body updateKnowledgeSourceRequest
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: err.Error(), CorrelationID: correlationID(req)})
			return
		}
		result, err := r.knowledgeWorkspace.UpdateSource(req.Context(), viewer, knowledgeBaseID, documentID, body.ExpectedRevision, body.Content)
		if err != nil {
			status, response := MapError(err, correlationID(req))
			writeJSON(w, status, response)
			return
		}
		writeJSON(w, http.StatusOK, result)
	default:
		http.NotFound(w, req)
	}
}

func (r *Router) handleListKnowledgeDocumentRevisions(w http.ResponseWriter, req *http.Request, knowledgeBaseID, documentID string) {
	if r.knowledgeWorkspace == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge workspace unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	items, err := r.knowledgeWorkspace.ListRevisions(req.Context(), r.knowledgeViewerFromRequest(req), knowledgeBaseID, documentID)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (r *Router) handlePublishKnowledgeDocumentRevision(w http.ResponseWriter, req *http.Request, knowledgeBaseID, documentID, rawRevision string) {
	if r.knowledgeWorkspace == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge workspace unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	revision, err := strconv.Atoi(rawRevision)
	if err != nil || revision < 1 {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: "positive revision is required", CorrelationID: correlationID(req)})
		return
	}
	result, err := r.knowledgeWorkspace.PublishRevision(req.Context(), r.knowledgeViewerFromRequest(req), knowledgeBaseID, documentID, revision)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) handleGetKnowledgeDocumentOntology(w http.ResponseWriter, req *http.Request, knowledgeBaseID, documentID string) {
	if r.knowledgeWorkspace == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge workspace unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	revision, _ := strconv.Atoi(req.URL.Query().Get("revision"))
	result, err := r.knowledgeWorkspace.GetOntologyProjection(req.Context(), r.knowledgeViewerFromRequest(req), knowledgeBaseID, documentID, revision)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (r *Router) handleListKnowledgeDocumentRelations(w http.ResponseWriter, req *http.Request, knowledgeBaseID, documentID string) {
	if r.knowledgeWorkspace == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "knowledge workspace unavailable", CorrelationID: correlationID(req), Retryable: true})
		return
	}
	limit := 50
	if raw := req.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: "limit must be between 1 and 100", CorrelationID: correlationID(req)})
			return
		}
		limit = parsed
	}
	direction := req.URL.Query().Get("direction")
	if direction != "" && direction != "in" && direction != "out" && direction != "both" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: ErrorInvalidRequest, Message: "direction must be in, out, or both", CorrelationID: correlationID(req)})
		return
	}
	result, err := r.knowledgeWorkspace.ListDocumentRelations(req.Context(), r.knowledgeViewerFromRequest(req), knowledgeBaseID, documentID, direction, limit)
	if err != nil {
		status, body := MapError(err, correlationID(req))
		writeJSON(w, status, body)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
