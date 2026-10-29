package httptransport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/app/agenttrace"
)

type AgentTraceService interface {
	Metadata(context.Context, string, string) (agenttrace.MetadataDTO, error)
	Manifest(context.Context, string, string) (any, error)
	Events(context.Context, string, string, uint64, int) (agenttrace.EventPageDTO, error)
	ApplyLegalHold(context.Context, string, string, string, string, time.Time) error
	ReleaseLegalHold(context.Context, string, string) error
}

func (r *Router) handleAgentTraceRoute(w http.ResponseWriter, req *http.Request, parts []string) bool {
	if len(parts) < 4 || parts[0] != "api" || parts[1] != "agent-runs" || parts[3] != "trace" {
		return false
	}
	if r.agentTraces == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "service_dependency", Message: "Agent trace service is unavailable.", CorrelationID: correlationID(req)})
		return true
	}
	viewer, err := ViewerFromRequest(req)
	if err != nil || strings.TrimSpace(viewer.TenantID) == "" || strings.TrimSpace(parts[2]) == "" {
		writeAgentTraceError(w, req, agenttrace.ErrInvalidTraceRequest)
		return true
	}
	tenantID, runID := strings.TrimSpace(viewer.TenantID), strings.TrimSpace(parts[2])
	switch {
	case req.Method == http.MethodGet && len(parts) == 4:
		value, err := r.agentTraces.Metadata(req.Context(), tenantID, runID)
		writeAgentTraceResult(w, req, value, err, http.StatusOK)
	case req.Method == http.MethodGet && len(parts) == 5 && parts[4] == "manifest":
		value, err := r.agentTraces.Manifest(req.Context(), tenantID, runID)
		writeAgentTraceResult(w, req, value, err, http.StatusOK)
	case req.Method == http.MethodGet && len(parts) == 5 && parts[4] == "events":
		after, afterErr := strconv.ParseUint(firstNonEmpty(req.URL.Query().Get("after"), "0"), 10, 64)
		limit, limitErr := strconv.Atoi(firstNonEmpty(req.URL.Query().Get("limit"), "100"))
		if afterErr != nil || limitErr != nil {
			writeAgentTraceError(w, req, agenttrace.ErrInvalidTraceRequest)
			return true
		}
		value, err := r.agentTraces.Events(req.Context(), tenantID, runID, after, limit)
		writeAgentTraceResult(w, req, value, err, http.StatusOK)
	case req.Method == http.MethodPost && len(parts) == 5 && parts[4] == "legal-hold":
		var input struct {
			Reason string `json:"reason"`
		}
		if json.NewDecoder(req.Body).Decode(&input) != nil || strings.TrimSpace(input.Reason) == "" {
			writeAgentTraceError(w, req, agenttrace.ErrInvalidTraceRequest)
			return true
		}
		err := r.agentTraces.ApplyLegalHold(req.Context(), tenantID, runID, viewer.ViewerID, input.Reason, time.Now().UTC())
		writeAgentTraceResult(w, req, map[string]string{"status": "accepted"}, err, http.StatusAccepted)
	case req.Method == http.MethodDelete && len(parts) == 5 && parts[4] == "legal-hold":
		err := r.agentTraces.ReleaseLegalHold(req.Context(), tenantID, runID)
		writeAgentTraceResult(w, req, map[string]string{"status": "accepted"}, err, http.StatusAccepted)
	default:
		http.NotFound(w, req)
	}
	return true
}

func writeAgentTraceResult(w http.ResponseWriter, req *http.Request, value any, err error, status int) {
	if err != nil {
		writeAgentTraceError(w, req, err)
		return
	}
	writeJSON(w, status, value)
}

func writeAgentTraceError(w http.ResponseWriter, req *http.Request, err error) {
	status, code := http.StatusInternalServerError, "internal_error"
	switch {
	case errors.Is(err, agenttrace.ErrTraceNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, agenttrace.ErrInvalidTraceRequest):
		status, code = http.StatusBadRequest, "invalid_request"
	}
	writeJSON(w, status, ErrorResponse{Code: code, Message: err.Error(), CorrelationID: correlationID(req)})
}
