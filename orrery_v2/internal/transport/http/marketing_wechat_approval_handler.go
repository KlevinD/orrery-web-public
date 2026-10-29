package httptransport

import (
	"context"
	"crypto/subtle"
	"net/http"
	"strings"

	"orrery_v2/internal/app/hil"
)

const marketingWechatApprovalRequestLimit = 1 << 20

type marketingWechatApprovalRequest struct {
	GenerationID         string                         `json:"generation_id"`
	AccountID            string                         `json:"account_id"`
	ContentHash          string                         `json:"content_hash"`
	AssetHashes          []string                       `json:"asset_hashes"`
	Material             hil.BusinessApprovalMaterial   `json:"material"`
	Artifacts            []hil.BusinessApprovalArtifact `json:"artifacts"`
	EvidenceRefs         []string                       `json:"evidence_refs"`
	IdempotencyKey       string                         `json:"idempotency_key"`
	SubmissionID         string                         `json:"submission_id,omitempty"`
	ProjectID            string                         `json:"project_id,omitempty"`
	ContentRevision      int                            `json:"content_revision,omitempty"`
	BundleHash           string                         `json:"bundle_hash,omitempty"`
	SupersedesDecisionID string                         `json:"supersedes_decision_id,omitempty"`
}

type marketingWechatApprovalResponse struct {
	ArgsHash           string             `json:"args_hash"`
	DecisionID         string             `json:"decision_id"`
	WorkflowInstanceID string             `json:"workflow_instance_id"`
	Status             hil.DecisionStatus `json:"status"`
	MaterialHash       string             `json:"material_hash"`
	Revision           int                `json:"revision"`
}

func (r *Router) handleMarketingWechatApprovalRoute(w http.ResponseWriter, req *http.Request) bool {
	if req == nil {
		return false
	}
	path := strings.Trim(req.URL.Path, "/")
	const base = "api/business-approvals/marketing-wechat-drafts"
	decisionID, isStatus := strings.CutPrefix(path, base+"/")
	if path != base && (!isStatus || decisionID == "" || strings.Contains(decisionID, "/")) {
		return false
	}
	method := http.MethodPost
	if isStatus {
		method = http.MethodGet
	}
	if req.Method != method {
		w.Header().Set("Allow", method)
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return true
	}
	if r.marketingWechatApproval.Service == nil || strings.TrimSpace(r.marketingWechatApproval.Authorization) == "" {
		writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{
			Code: ErrorResourceUnavailable, Message: "service dependency unavailable", CorrelationID: correlationID(req), Retryable: true,
		})
		return true
	}
	if !marketingWechatApprovalAuthorizationMatches(req, r.marketingWechatApproval.Authorization) {
		writeJSON(w, http.StatusUnauthorized, ErrorResponse{
			Code: ErrorUnauthorized, Message: "authorization is required", CorrelationID: correlationID(req),
		})
		return true
	}
	if isStatus {
		reader, ok := r.marketingWechatApproval.Service.(interface {
			GetMarketingWechatApprovalStatus(context.Context, string, string, string) (hil.MarketingWechatApprovalStatus, error)
		})
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, ErrorResponse{Code: ErrorResourceUnavailable, Message: "approval status dependency unavailable"})
			return true
		}
		result, err := reader.GetMarketingWechatApprovalStatus(req.Context(), "system", "marketing", decisionID)
		if err != nil {
			status, response := MapError(err, correlationID(req))
			writeJSON(w, status, response)
			return true
		}
		w.Header().Set("Cache-Control", "no-store")
		writeJSON(w, http.StatusOK, result)
		return true
	}
	req.Body = http.MaxBytesReader(w, req.Body, marketingWechatApprovalRequestLimit)
	var body marketingWechatApprovalRequest
	if err := decodeStrictJSON(req, &body); err != nil {
		status, response := MapError(hil.ErrInvalidRequest, correlationID(req))
		writeJSON(w, status, response)
		return true
	}
	result, err := r.marketingWechatApproval.Service.RequestMarketingWechatApproval(req.Context(), hil.MarketingWechatApprovalInput{
		TenantID: "system", RequesterID: "marketing", GenerationID: body.GenerationID, AccountID: body.AccountID,
		ContentHash: body.ContentHash, AssetHashes: body.AssetHashes, Material: body.Material, Artifacts: body.Artifacts,
		EvidenceRefs: body.EvidenceRefs, IdempotencyKey: body.IdempotencyKey,
		SubmissionID: body.SubmissionID, ProjectID: body.ProjectID, ContentRevision: body.ContentRevision,
		BundleHash: body.BundleHash, SupersedesDecisionID: body.SupersedesDecisionID,
	})
	if err != nil {
		status, response := MapError(err, correlationID(req))
		writeJSON(w, status, response)
		return true
	}
	writeJSON(w, http.StatusCreated, marketingWechatApprovalResponse{
		ArgsHash:   result.ArgsHash,
		DecisionID: result.DecisionID, WorkflowInstanceID: result.WorkflowInstanceID, Status: result.Status,
		MaterialHash: result.MaterialHash, Revision: result.Revision,
	})
	return true
}

func marketingWechatApprovalAuthorizationMatches(req *http.Request, expected string) bool {
	if req == nil {
		return false
	}
	actual := strings.TrimSpace(req.Header.Get("Authorization"))
	expected = strings.TrimSpace(expected)
	return expected != "" && len(actual) == len(expected) && subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}
