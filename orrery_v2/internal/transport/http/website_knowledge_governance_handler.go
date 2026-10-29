package httptransport

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"orrery_v2/internal/gateway/channel/website"
)

const websiteKnowledgeRequestBodyLimit = 64 << 10

func (r *Router) handleWebsiteKnowledgeGovernanceRoute(w http.ResponseWriter, req *http.Request, parts []string) bool {
	if len(parts) < 3 || parts[0] != "api" || parts[1] != "website-knowledge" {
		return false
	}
	if r.websiteKnowledgeGovernance == nil {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: "service_dependency", Message: "website knowledge governance is unavailable", CorrelationID: correlationID(req)})
		return true
	}
	switch {
	case req.Method == http.MethodGet && len(parts) == 3 && parts[2] == "gaps":
		r.handleWebsiteKnowledgeGaps(w, req)
	case req.Method == http.MethodGet && len(parts) == 3 && parts[2] == "faqs":
		r.handleWebsiteFAQCatalog(w, req)
	case req.Method == http.MethodGet && len(parts) == 3 && parts[2] == "metrics":
		r.handleWebsiteFAQMetrics(w, req)
	case req.Method == http.MethodGet && len(parts) == 3 && parts[2] == "proposals":
		r.handleWebsiteKnowledgeProposalList(w, req)
	case req.Method == http.MethodPost && len(parts) == 3 && parts[2] == "proposals":
		r.handleWebsiteKnowledgeProposalCreate(w, req)
	case req.Method == http.MethodGet && len(parts) == 4 && parts[2] == "proposals":
		r.handleWebsiteKnowledgeProposalGet(w, req, parts[3])
	case req.Method == http.MethodGet && len(parts) == 5 && parts[2] == "proposals" && parts[4] == "receipts":
		r.handleWebsiteKnowledgeProposalReceipts(w, req, parts[3])
	case req.Method == http.MethodPost && len(parts) == 5 && parts[2] == "proposals" && parts[4] == "approve":
		r.handleWebsiteKnowledgeProposalApprove(w, req, parts[3])
	case req.Method == http.MethodPost && len(parts) == 5 && parts[2] == "proposals" && parts[4] == "publish":
		r.handleWebsiteKnowledgeProposalPublish(w, req, parts[3])
	case req.Method == http.MethodPost && len(parts) == 3 && parts[2] == "rules":
		r.handleWebsiteKnowledgeRuleCreate(w, req)
	case req.Method == http.MethodPost && len(parts) == 5 && parts[2] == "rules" && parts[4] == "evaluate":
		r.handleWebsiteKnowledgeRuleEvaluate(w, req, parts[3])
	default:
		writeJSON(w, http.StatusNotFound, ErrorResponse{Code: "not_found", Message: "route not found", CorrelationID: correlationID(req)})
	}
	return true
}

func (r *Router) handleWebsiteFAQCatalog(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"items":    r.websiteFAQCatalog.Entries(),
		"revision": r.websiteFAQCatalog.Revision(),
	})
}

func (r *Router) handleWebsiteFAQMetrics(w http.ResponseWriter, req *http.Request) {
	values, err := r.websiteKnowledgeGovernance.ListWebsiteFAQMetrics(req.Context(), time.Now().UTC().Add(-7*24*time.Hour))
	if err != nil {
		writeWebsiteKnowledgeGovernanceError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"metrics": values})
}

func (r *Router) handleWebsiteKnowledgeProposalList(w http.ResponseWriter, req *http.Request) {
	status := website.ProposalStatus(strings.TrimSpace(req.URL.Query().Get("status")))
	if status != "" && status != website.ProposalStatusDraft && status != website.ProposalStatusApproved && status != website.ProposalStatusPublished {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "invalid website knowledge proposal status", CorrelationID: correlationID(req)})
		return
	}
	limit := 50
	if raw := strings.TrimSpace(req.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "invalid website knowledge proposal limit", CorrelationID: correlationID(req)})
			return
		}
		limit = value
	}
	if limit > 100 {
		limit = 100
	}
	values, err := r.websiteKnowledgeGovernance.ListWebsiteKnowledgeProposals(req.Context(), status, limit)
	if err != nil {
		writeWebsiteKnowledgeGovernanceError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposals": values})
}

func (r *Router) handleWebsiteKnowledgeProposalGet(w http.ResponseWriter, req *http.Request, proposalID string) {
	value, err := r.websiteKnowledgeGovernance.GetWebsiteKnowledgeProposal(req.Context(), proposalID)
	if err != nil {
		writeWebsiteKnowledgeGovernanceError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proposal": value})
}

func (r *Router) handleWebsiteKnowledgeProposalReceipts(w http.ResponseWriter, req *http.Request, proposalID string) {
	values, err := r.websiteKnowledgeGovernance.ListWebsiteKnowledgeReceipts(req.Context(), proposalID)
	if err != nil {
		writeWebsiteKnowledgeGovernanceError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"receipts": values})
}

func (r *Router) handleWebsiteKnowledgeGaps(w http.ResponseWriter, req *http.Request) {
	minimum := 1
	if value, err := strconv.Atoi(strings.TrimSpace(req.URL.Query().Get("minimum_count"))); err == nil && value > 0 {
		minimum = value
	}
	if minimum > 100 {
		minimum = 100
	}
	values, err := r.websiteKnowledgeGovernance.ListKnowledgeGaps(req.Context(), minimum, time.Now().UTC())
	if err != nil {
		writeWebsiteKnowledgeGovernanceError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"gaps": values})
}

func (r *Router) handleWebsiteKnowledgeProposalCreate(w http.ResponseWriter, req *http.Request) {
	var body struct {
		GapID              string   `json:"gap_id"`
		TargetKnowledgeKey string   `json:"target_knowledge_key"`
		Draft              string   `json:"draft"`
		SourceRefs         []string `json:"source_refs"`
		Rationale          string   `json:"rationale"`
	}
	if !decodeWebsiteKnowledgeBody(w, req, &body) {
		return
	}
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		writeWebsiteKnowledgeGovernanceError(w, req, err)
		return
	}
	proposal, err := r.websiteKnowledgeGovernance.CreateProposal(req.Context(), website.WebsiteKnowledgeProposalInput{
		GapID: body.GapID, TargetKnowledgeKey: body.TargetKnowledgeKey, Draft: body.Draft,
		SourceRefs: body.SourceRefs, Rationale: body.Rationale, CreatedBy: viewer.ViewerID, Now: time.Now().UTC(),
	})
	if err != nil {
		writeWebsiteKnowledgeGovernanceError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, proposal)
}

func (r *Router) handleWebsiteKnowledgeProposalApprove(w http.ResponseWriter, req *http.Request, proposalID string) {
	r.handleWebsiteKnowledgeProposalDecision(w, req, proposalID, true)
}

func (r *Router) handleWebsiteKnowledgeProposalPublish(w http.ResponseWriter, req *http.Request, proposalID string) {
	r.handleWebsiteKnowledgeProposalDecision(w, req, proposalID, false)
}

func (r *Router) handleWebsiteKnowledgeProposalDecision(w http.ResponseWriter, req *http.Request, proposalID string, approve bool) {
	var body struct {
		RevisionHash string `json:"revision_hash"`
	}
	if !decodeWebsiteKnowledgeBody(w, req, &body) {
		return
	}
	if strings.TrimSpace(body.RevisionHash) == "" {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "revision_hash is required", CorrelationID: correlationID(req)})
		return
	}
	viewer, err := ViewerFromRequest(req)
	if err != nil {
		writeWebsiteKnowledgeGovernanceError(w, req, err)
		return
	}
	var proposal website.WebsiteKnowledgeProposal
	if approve {
		proposal, err = r.websiteKnowledgeGovernance.ApproveProposal(req.Context(), proposalID, body.RevisionHash, viewer.ViewerID, time.Now().UTC())
	} else {
		proposal, err = r.websiteKnowledgeGovernance.PublishProposal(req.Context(), proposalID, body.RevisionHash, viewer.ViewerID, time.Now().UTC())
		if err == nil {
			err = r.websiteFAQCatalog.Reload(req.Context(), r.websiteKnowledgeGovernance)
		}
	}
	if err != nil {
		writeWebsiteKnowledgeGovernanceError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func (r *Router) handleWebsiteKnowledgeRuleCreate(w http.ResponseWriter, req *http.Request) {
	var body struct {
		Kind                   website.RuleKind `json:"kind"`
		Before                 string           `json:"before"`
		After                  string           `json:"after"`
		EvidenceWindow         string           `json:"evidence_window"`
		AuthoringJob           string           `json:"authoring_job"`
		MetricsSnapshot        string           `json:"metrics_snapshot"`
		ApprovedAnswerRevision string           `json:"approved_answer_revision"`
		PreviousRevisionID     string           `json:"previous_revision_id"`
	}
	if !decodeWebsiteKnowledgeBody(w, req, &body) {
		return
	}
	revision, err := website.NewAutomaticRuleRevision(website.AutomaticRuleRevisionInput{
		Kind: body.Kind, Before: body.Before, After: body.After, EvidenceWindow: body.EvidenceWindow,
		AuthoringJob: body.AuthoringJob, MetricsSnapshot: body.MetricsSnapshot,
		ApprovedAnswerRevision: body.ApprovedAnswerRevision, PreviousRevisionID: body.PreviousRevisionID, Now: time.Now().UTC(),
	})
	if err == nil {
		err = r.websiteKnowledgeGovernance.SaveRuleRevision(req.Context(), revision)
	}
	if err != nil {
		writeWebsiteKnowledgeGovernanceError(w, req, err)
		return
	}
	writeJSON(w, http.StatusCreated, revision)
}

func (r *Router) handleWebsiteKnowledgeRuleEvaluate(w http.ResponseWriter, req *http.Request, revisionID string) {
	var body struct {
		SourceValid      bool    `json:"source_valid"`
		ResolutionBefore float64 `json:"resolution_before"`
		ResolutionAfter  float64 `json:"resolution_after"`
	}
	if !decodeWebsiteKnowledgeBody(w, req, &body) {
		return
	}
	revision, err := r.websiteKnowledgeGovernance.EvaluateRuleRevision(req.Context(), revisionID, body.SourceValid, body.ResolutionBefore, body.ResolutionAfter, time.Now().UTC())
	if err != nil {
		writeWebsiteKnowledgeGovernanceError(w, req, err)
		return
	}
	writeJSON(w, http.StatusOK, revision)
}

func decodeWebsiteKnowledgeBody(w http.ResponseWriter, req *http.Request, target any) bool {
	defer req.Body.Close()
	req.Body = http.MaxBytesReader(w, req.Body, websiteKnowledgeRequestBodyLimit)
	decoder := json.NewDecoder(req.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "invalid website knowledge governance request", CorrelationID: correlationID(req)})
		return false
	}
	return true
}

func writeWebsiteKnowledgeGovernanceError(w http.ResponseWriter, req *http.Request, _ error) {
	writeJSON(w, http.StatusBadRequest, ErrorResponse{Code: "invalid_request", Message: "website knowledge governance request was rejected", CorrelationID: correlationID(req)})
}
